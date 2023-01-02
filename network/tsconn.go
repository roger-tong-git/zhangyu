package network

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/google/uuid"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type TransportClient struct {
	clientInfo   *ClientConnInfo
	invokeRoute  *InvokeRoute
	connected    bool
	keepaliveRun bool
	lastAddr     string
	utils.Closer
}

func (w *TransportClient) Connected() bool {
	return w.connected
}

func (w *TransportClient) InvokeRoute() *InvokeRoute {
	return w.invokeRoute
}

func (w *TransportClient) DefaultInvoker() *Invoker {
	if w.invokeRoute == nil {
		return nil
	}

	return w.invokeRoute.DefaultInvoker()
}

func (w *TransportClient) Invoke(r *InvokeRequest) *InvokeResponse {
	re, err := w.invokeRoute.defaultInvoker.Invoke(r)
	if err != nil && err == WebTransportConnectError {
		w.connected = false
		return NewInvokeResponse(r.RequestId, InvokeResult_Error, err.Error())
	}
	return re
}

func (w *TransportClient) dial(addr string, connId string, connType string, connectedHandler func(*Invoker)) error {
	var err error
	header := http.Header{}
	header.Set(HeadKey_TerminalId, w.clientInfo.TerminalId)
	header.Set(HeadKey_ConnectionId, connId)
	header.Set(HeadKey_ConnectionType, connType)

	var d webtransport.Dialer
	var session *webtransport.Session
	d.RoundTripper = &http3.RoundTripper{}
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	if _, session, err = d.Dial(w.Ctx(), addr, header); err != nil {
		return err
	}

	if stream, err1 := session.OpenStream(); err1 != nil {
		return err1
	} else {
		w.connected = true
		w.lastAddr = addr
		invoker := w.invokeRoute.AddInvoker(connId, session, stream)
		if connectedHandler != nil {
			connectedHandler(invoker)
		}
		return nil
	}
}

func (w *TransportClient) ConnectTo(addr string, connectedHandler func()) {
	_ = w.dial(addr, w.clientInfo.ConnectionId, Connection_Command, func(invoker *Invoker) {
		w.invokeRoute.SetDefaultInvoker(invoker)
		go w.invokeRoute.DispatchInvoke(invoker)
		if connectedHandler != nil {
			connectedHandler()
		}
	})

	go func() {
		if w.keepaliveRun {
			return
		}
		w.keepaliveRun = true
		for {
			select {
			case <-w.Ctx().Done():
				return
			default:
				if !w.connected {
					log.Println("连接服务端出错,系统将尝试重新连接...")
					time.Sleep(time.Second)
					w.ConnectTo(addr, connectedHandler)
				}
				time.Sleep(time.Second)
			}
		}
	}()

	//var err error
	//header := http.Header{}
	//header.Set(HeadKey_TerminalId, w.clientInfo.TerminalId)
	//header.Set(HeadKey_ConnectionId, w.clientInfo.ConnectionId)
	//header.Set(HeadKey_ConnectionType, Connection_Command)
	//
	//var d webtransport.Dialer
	//var session *webtransport.Session
	//d.RoundTripper = &http3.RoundTripper{}
	//d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	//
	//if !w.connected {
	//	if _, session, err = d.Dial(w.Ctx(), addr, header); err != nil {
	//		return err
	//	}
	//
	//	if stream, err1 := session.OpenStream(); err1 != nil {
	//		return err1
	//	} else {
	//		w.lastAddr = addr
	//		ip := strings.Split(session.RemoteAddr().String(), ":")[0]
	//		invoker := w.invokeRoute.AddInvoker(ip, w.clientInfo.ConnectionId, stream, stream)
	//		go w.invokeRoute.DispatchInvoke(invoker)
	//		w.invokeRoute.SetDefaultInvoker(invoker)
	//		w.connected = true
	//		if connectedHandler != nil {
	//			w.connectedHandler = connectedHandler
	//			connectedHandler()
	//		}
	//
	//		go func() {
	//			select {
	//			case <-invoker.Ctx().Done():
	//				return
	//			default:
	//				_ = w.ConnectTo(addr, w.connectedHandler)
	//			}
	//		}()
	//	}
	//} else {
	//	time.Sleep(time.Second)
	//}
}

func (w *TransportClient) initEvents() {
	w.invokeRoute.AddHandler("/client/kick", w.onKick)                         //当前用户被踢下线
	w.invokeRoute.AddHandler("/_zhangyu/transfer/map/add", w.onTransferMapAdd) //收到添加转发通道的命令
	w.invokeRoute.AddHandler("/_zhangyu/transfer/map/start", w.onTransferMapStart)
}

func (w *TransportClient) onKick(invoker *Invoker, request *InvokeRequest) *InvokeResponse {
	go log.Fatalln("有新的客户端上线，当前客户端已被踢下线")
	return NewSuccessResponse(request, "")
}

func (w *TransportClient) onTransferMapAdd(invoker *Invoker, r *InvokeRequest) *InvokeResponse {
	transferMapReq := &TransferRequest{}
	utils.GetJsonValue(transferMapReq, r.BodyJson)
	if err := w.AppendTransferMap(transferMapReq); err != nil {
		return NewErrorResponse(r, fmt.Sprintf("监听端返回错误:%v", err.Error()))
	}
	return NewSuccessResponse(r, nil)
}

// AppendTransferMap 开启监听端口，把收到的连接传送到服务端
func (w *TransportClient) AppendTransferMap(tq *TransferRequest) error {
	listenUrl := tq.GetListenUrl()
	connId := uuid.New().String()
	switch listenUrl.Scheme {
	case "tcp":
		//打开监听端口
		listener, listenErr := net.Listen("tcp", listenUrl.Host)
		listenPortStr := fmt.Sprintf("%v|%v", tq.TargetTerminalTunnelId, tq.TargetTerminalUri)
		if listenErr != nil {
			log.Println(fmt.Sprintf("打开主控端监听端口[%v]失败", listenPortStr))
			return listenErr
		}
		log.Println(fmt.Sprintf("已打开主控端监听端口[%v] -> [%v]", listenUrl.Host, listenPortStr))

		//以下对监听端口接收到的连接做处理
		go func() {
			for {
				select {
				case <-w.Ctx().Done():
					return
				default:
					conn, connErr := listener.Accept()
					if connErr != nil {
						log.Println(connErr)
						continue
					}

					// 将接收连接的处理过程放入协程
					go func() {
						w.dial(w.lastAddr, connId, Connection_Instance_From, func(invoker *Invoker) {
							invReq := NewInvokeRequest(uuid.New().String(), "")
							invReq.BodyJson = utils.GetJsonString(tq)
							reqErr := invoker.WriteInvoke(invReq)

							if reqErr != nil {
								log.Println(reqErr)
								invoker.CtxCancel()
								return
							}

							_, resp, respErr := invoker.ReadInvoke()
							if respErr != nil {
								log.Println(respErr)
								invoker.CtxCancel()
								return
							}

							if resp.ResultCode == InvokeResult_Error {
								log.Println(resp.ResultMessage)
								invoker.CtxCancel()
								return
							} else {
								log.Println(resp.ResultMessage)
							}

							transferStream := &TransferStream{
								transferId:   connId,
								sourceStream: invoker,
								targetStream: conn,
							}

							transferStream.Transfer()
						})
					}()
				}
			}
		}()
	}
	return nil
}

func (w *TransportClient) AppendTransferStart(connId string, tq *TransferRequest) error {
	sourceUrl := tq.GetTargetUrl()
	host := strings.ToLower(strings.TrimSpace(sourceUrl.Host))
	port := ""
	scheme := sourceUrl.Scheme
	ar := strings.Split(host, ":")
	if len(ar) > 1 {
		port = ar[1]
	}
	switch sourceUrl.Scheme {
	case "http":
		scheme = "tcp"
		if port == "" {
			host = host + ":80"
		}
		break
	case "https":
		scheme = "tcp"
		if port == "" {
			host = host + ":443"
		}
		break
	}

	conn, listenErr := net.Dial(scheme, host)
	if listenErr != nil {
		log.Println(fmt.Sprintf("被控通道连接到目标服务[%v]失败", sourceUrl.Host))
		return listenErr
	}

	return w.dial(w.lastAddr, connId, Connection_Instance_Target, func(invoker *Invoker) {
		go func() {
			if sourceUrl.Scheme == "tcp" {
				transferStream := NewTransferStream(connId, invoker, conn)
				transferStream.Transfer()
			}
		}()
	})
}

func (w *TransportClient) onTransferMapStart(_ *Invoker, r *InvokeRequest) *InvokeResponse {
	transferMapReq := &TransferRequest{}
	utils.GetJsonValue(transferMapReq, r.BodyJson)
	connId := r.Header["ConnectionId"]
	if err := w.AppendTransferStart(connId, transferMapReq); err != nil {
		return NewErrorResponse(r, fmt.Sprintf("被控通道[%v]连接到目标服务[%v]失败:%v",
			transferMapReq.TargetTerminalTunnelId, transferMapReq.TargetTerminalUri, err.Error()))
	}
	s := fmt.Sprintf("被控通道[%v]已成功连接到目标服务[%v]",
		transferMapReq.TargetTerminalTunnelId, transferMapReq.TargetTerminalUri)
	resp := NewSuccessResponse(r, nil)
	resp.ResultMessage = s
	return resp
}

func NewWebSocketClient(ctx context.Context, clientInfo *ClientConnInfo) *TransportClient {
	w := &TransportClient{clientInfo: clientInfo}
	w.SetCtx(ctx)
	w.invokeRoute = NewInvokeRoute(w.Ctx())
	w.initEvents()
	return w
}
