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
	"sync"
	"time"
)

type TransportClient struct {
	clientInfo   *ClientConnInfo
	invokeRoute  *InvokeRoute
	transfers    map[string]*TransferSession
	transferLock sync.Mutex
	connected    bool
	keepaliveRun bool
	lastAddr     string
	utils.Closer
}

func (w *TransportClient) getTransfer(key string) *TransferSession {
	defer w.transferLock.Unlock()
	w.transferLock.Lock()
	return w.transfers[key]
}

func (w *TransportClient) deleteTransfer(key string) {
	defer w.transferLock.Unlock()
	w.transferLock.Lock()
	delete(w.transfers, key)
	log.Println("transfer连接删除:", key)

}

func (w *TransportClient) setTransfer(key string, value *TransferSession) {
	defer w.transferLock.Unlock()
	w.transferLock.Lock()
	w.transfers[key] = value
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
	if err != nil {
		w.connected = false
		return NewInvokeResponse(r.RequestId, InvokeResult_Error, err.Error())
	}
	return re
}

func (w *TransportClient) Dial(addr string, connId string, connType string, connectedHandler func(*Invoker)) error {
	header := http.Header{}
	header.Set(HeadKey_TerminalId, w.clientInfo.TerminalId)
	header.Set(HeadKey_TunnelId, w.clientInfo.TunnelId)
	header.Set(HeadKey_ConnectionId, connId)
	header.Set(HeadKey_ConnectionType, connType)

	var err error
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

func (w *TransportClient) heartbeat() {
	for {
		select {
		case <-w.Ctx().Done():
			return
		case <-time.After(time.Second * 5):
			if !w.Connected() {
				continue
			}
			req := NewInvokeRequest(w.clientInfo.TerminalId, InvokePath_Client_Heartbeat)
			_ = WriteInvoke(w.DefaultInvoker().WriterLock(), w.DefaultInvoker(), req)
		}
	}
}

func (w *TransportClient) ConnectTo(addr string, connectedHandler func()) {
	_ = w.Dial(addr, w.clientInfo.ConnectionId, Connection_Command, func(invoker *Invoker) {
		w.invokeRoute.SetDefaultInvoker(invoker)
		go w.invokeRoute.DispatchInvoke(invoker, nil)
		if connectedHandler != nil {
			connectedHandler()
		}
	})

	go func() {
		if w.keepaliveRun {
			return
		}
		//go w.heartbeat()
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
}

func (w *TransportClient) initEvents() {
	w.invokeRoute.AddRpcHandler(InvokePath_Client_Kick, w.onKick)               //当前用户被踢下线
	w.invokeRoute.AddRpcHandler(InvokePath_Transfer_Listen, w.onTransferListen) //收到添加转发通道的命令
	w.invokeRoute.AddUniHandler(InvokePath_Transfer_Dial, w.onTransferConnDial)
	w.invokeRoute.AddUniHandler(InvokePath_Transfer_Go, w.onTransferGo)
	w.invokeRoute.AddUniHandler(InvokePath_Transfer_Disconnect, w.onTransferDisconnect)
}

func (w *TransportClient) onKick(invoker *Invoker, request *InvokeRequest) *InvokeResponse {
	go log.Fatalln("有新的客户端上线，当前客户端已被踢下线")
	return NewSuccessResponse(request, "")
}

func (w *TransportClient) onTransferListen(invoker *Invoker, r *InvokeRequest) *InvokeResponse {
	transferReq := &TransferRequest{}
	utils.GetJsonValue(transferReq, r.BodyJson)
	if err := w.AppendTransferListen(transferReq); err != nil {
		return NewErrorResponse(r, fmt.Sprintf("监听端返回错误:%v", err.Error()))
	}
	return NewSuccessResponse(r, nil)
}

// AppendTransferListen 开启监听端口，把收到的连接传送到服务端
func (w *TransportClient) AppendTransferListen(tq *TransferRequest) error {
	listenUrl := tq.GetListenUrl()
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

					connId := uuid.New().String()
					// 将接收连接的处理过程放入协程
					go func() {
						w.Dial(w.lastAddr, connId, Connection_Instance_From, func(invoker *Invoker) {
							invReq := NewInvokeRequest(uuid.New().String(), "")
							invReq.BodyJson = utils.GetJsonString(tq)
							invReq.Header[HeadKey_ConnectionId] = connId
							invReq.Header[HeadKey_TunnelId] = w.clientInfo.TunnelId
							invReq.Header[HeadKey_TerminalId] = w.clientInfo.TerminalId
							reqErr := invoker.WriteInvoke(invReq)

							if reqErr != nil {
								log.Println(reqErr)
								invoker.CtxCancel()
								return
							}

							transferStream := NewTransferSession(connId, invoker)
							transferStream.SetTargetStream(conn)
							w.setTransfer(connId, transferStream)

							go func() {
								select {
								case <-transferStream.Ctx().Done():
									log.Println("Delete ConnId(Done):", connId)
									return
								default:
									transferStream.Transfer()
									log.Println("Delete ConnId:", connId)
								}
								w.deleteTransfer(connId)
								_ = w.DefaultInvoker().WriteInvoke(NewInvokeRequest(connId, InvokePath_Transfer_Disconnect))
							}()
						})
					}()
				}
			}
		}()
	}
	return nil
}

func (w *TransportClient) connectTarget(connId string, tq *TransferRequest) error {
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
	log.Println("transfer连接建立:", connId)

	return w.Dial(w.lastAddr, connId, Connection_Instance_Target, func(invoker *Invoker) {
		// 此处写入1个前置字节，只是为了推动流在服务端能够AcceptStream
		b := []byte{88}
		_ = utils.WriteBytes(invoker, b)
		transferStream := NewTransferSession(connId, invoker)
		transferStream.SetTargetStream(conn)
		w.setTransfer(connId, transferStream)
		transferStream.Transfer()
	})
}

func (w *TransportClient) onTransferConnDial(_ *Invoker, r *InvokeRequest) {
	transferMapReq := &TransferRequest{}
	utils.GetJsonValue(transferMapReq, r.BodyJson)
	connId := r.Header[HeadKey_ConnectionId]
	if err := w.connectTarget(connId, transferMapReq); err != nil {
		log.Println(fmt.Sprintf("被控通道[%v]连接到目标服务[%v]失败:%v",
			transferMapReq.TargetTerminalTunnelId, transferMapReq.TargetTerminalUri, err.Error()))
	}

}

func (w *TransportClient) onTransferGo(invoker *Invoker, r *InvokeRequest) {
	connId := r.Header[HeadKey_ConnectionId]
	transfer := w.getTransfer(connId)
	if transfer != nil {
		transfer.TransferChan() <- true
	} else {
		log.Println("Transfer连接丢失:", connId)
	}
}

func (w *TransportClient) onTransferDisconnect(invoker *Invoker, request *InvokeRequest) {
	transfer := w.getTransfer(request.RequestId)
	if transfer != nil {
		_ = transfer.Close()
		w.deleteTransfer(request.RequestId)
		log.Println("onTransferDisconnect:", request.RequestId)
	}
}

func NewTransferClient(ctx context.Context, clientInfo *ClientConnInfo) *TransportClient {
	w := &TransportClient{clientInfo: clientInfo}
	w.SetCtx(ctx)
	w.invokeRoute = NewInvokeRoute(w.Ctx())
	w.initEvents()
	w.transfers = make(map[string]*TransferSession)
	return w
}
