package quic

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"net/http"
	"time"
)

// Client WebTransport客户端
type Client struct {
	clientInfo   *rpc.ClientConnInfo
	invokeRoute  *rpc.InvokeRoute
	connected    bool
	keepaliveRun bool
	serverAddr   string
	utils.Closer
}

func (c *Client) Connected() bool {
	return c.connected
}

func (c *Client) InvokeRoute() *rpc.InvokeRoute {
	return c.invokeRoute
}

func (c *Client) DefaultInvoker() *rpc.Invoker {
	if c.invokeRoute == nil {
		return nil
	}

	return c.invokeRoute.DefaultInvoker()
}

func (c *Client) Invoke(r *rpc.InvokeRequest) *rpc.InvokeResponse {
	re, err := c.invokeRoute.DefaultInvoker().Invoke(r)
	if err != nil {
		c.connected = false
		return rpc.NewInvokeResponse(r.RequestId, rpc.InvokeResult_Error, err.Error())
	}
	return re
}

func (c *Client) AddRpcHandler(path string, handler rpc.BidiInvokeHandler) {
	c.invokeRoute.AddRpcHandler(path, handler)
}

func (c *Client) AddUniHandler(path string, handler rpc.UniInvokeHandler) {
	c.invokeRoute.AddUniHandler(path, handler)
}

func (c *Client) RemoveUniHandler(path string) {
	c.invokeRoute.RemoveUniHandler(path)
}

func (c *Client) RemoveRpcHandler(path string) {
	c.invokeRoute.RemoveRpcHandler(path)
}

func (c *Client) Dial(addr string, connId string, connType string,
	connectedHandler func(invoker *rpc.Invoker, session *webtransport.Session)) error {
	header := http.Header{}
	header.Set(rpc.HeadKey_Connection_TerminalId, c.clientInfo.TerminalId)
	header.Set(rpc.HeadKey_Connection_Id, connId)
	header.Set(rpc.HeadKey_Connection_Type, connType)

	var err error
	var d webtransport.Dialer
	var session *webtransport.Session
	d.RoundTripper = &http3.RoundTripper{}
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	if _, session, err = d.Dial(c.Ctx(), addr, header); err != nil {
		return err
	}

	if stream, err1 := session.OpenStream(); err1 != nil {
		return err1
	} else {
		c.connected = true
		isCommand := connType == rpc.ConnectionType_Command
		invoker := c.invokeRoute.AddNewInvoker(connId, c.clientInfo.TerminalId, c.Ctx(), stream)
		invoker.SetAttach("Session", session)
		invoker.SetAttach("Conn", NewConnWrapper(invoker.Ctx(), stream, session))
		invoker.SetOnClose(func() {
			_ = invoker.ReadWriter().Close()
			_ = session.CloseWithError(0, "")
			c.invokeRoute.RemoveInvoker(invoker.ConnectionId())
		})
		if !isCommand {
			// 非Command通道，写入一个字节，推动服务端Accept
			if err = bufio.NewWriter(invoker.ReadWriter()).WriteByte(88); err != nil {
				_ = invoker.Close()
			}
		}
		if connectedHandler != nil {
			connectedHandler(invoker, session)
		}
		return nil
	}
}

func (c *Client) heartbeat() {
	for {
		select {
		case <-c.Ctx().Done():
			return
		case <-time.After(time.Second * 15):
			if !c.connected {
				continue
			}
			req := rpc.NewInvokeRequest(rpc.InvokePath_Client_Heartbeat)
			_ = c.DefaultInvoker().WriteRequest(req)
		}
	}
}

func (c *Client) ConnectTo() error {
	err := c.Dial(c.serverAddr, c.clientInfo.ConnectionId, rpc.ConnectionType_Command,
		func(invoker *rpc.Invoker, _ *webtransport.Session) {
			log.Println(fmt.Sprintf("已连接到SocksCloud服务端,当前客户ID[%v]", c.clientInfo.TerminalId))
			c.invokeRoute.SetDefaultInvoker(invoker)
			invoker.SetWriteErrorHandler(func(_ error) {
				c.connected = false
			})
			req := rpc.NewInvokeRequest(rpc.InvokePath_Client_Heartbeat)
			_ = c.DefaultInvoker().WriteRequest(req)
			go c.invokeRoute.DispatchInvoke(invoker)
		})

	go func() {
		if c.keepaliveRun {
			return
		}
		go c.heartbeat()
		c.keepaliveRun = true
		for {
			select {
			case <-c.Ctx().Done():
				return
			default:
				if !c.connected {
					log.Println("连接服务端出错,系统将尝试重新连接...")
					_ = c.ConnectTo()
					time.Sleep(time.Second)
				}
				time.Sleep(time.Second)
			}
		}
	}()

	return err
}

//func (w *Client) initEvents() {
//	w.invokeRoute.AddRpcHandler(InvokePath_Client_Kick, w.onKick)               //当前用户被踢下线
//	//w.invokeRoute.AddRpcHandler(InvokePath_Transfer_Listen, w.onTransferListen) //收到添加转发通道的命令
//	//w.invokeRoute.AddUniHandler(InvokePath_Transfer_Dial, w.onTransferConnDial)
//	//w.invokeRoute.AddUniHandler(InvokePath_Transfer_Go, w.onTransferGo)
//	//w.invokeRoute.AddUniHandler(InvokePath_Transfer_Disconnect, w.onTransferDisconnect)
//}
//
//func (w *Client) onKick(invoker *Invoker, request *InvokeRequest) *InvokeResponse {
//	go log.Fatalln("有新的客户端上线，当前客户端已被踢下线")
//	return NewSuccessResponse(request.RequestId, "")
//}

//func (w *Client) onTransferListen(invoker *Invoker, r *InvokeRequest) *InvokeResponse {
//	transferReq := &TransferRequest{}
//	utils.GetJsonValue(transferReq, r.JsonBody)
//	if err := w.AppendTransferListen(transferReq); err != nil {
//		return NewErrorResponse(r.RequestId, "监听端返回错误:%v", err.Error())
//	}
//	return NewSuccessResponse(r.RequestId, "")
//}

//// AppendTransferListen 开启监听端口，把收到的连接传送到服务端
//func (w *Client) AppendTransferListen(tq *TransferRequest) error {
//	listenUrl := tq.GetListenUrl()
//	switch listenUrl.Scheme {
//	case "tcp":
//		//打开监听端口
//		listener, listenErr := net.Listen("tcp", listenUrl.Host)
//		listenPortStr := fmt.Sprintf("%v|%v", tq.TargetTerminalTunnelId, tq.TargetTerminalUri)
//		if listenErr != nil {
//			log.Println(fmt.Sprintf("打开主控端监听端口[%v]失败", listenPortStr))
//			return listenErr
//		}
//		log.Println(fmt.Sprintf("已打开主控端监听端口[%v] -> [%v]", listenUrl.Host, listenPortStr))
//
//		//以下对监听端口接收到的连接做处理
//		go func() {
//			for {
//				select {
//				case <-w.Ctx().Done():
//					return
//				default:
//					conn, connErr := listener.Accept()
//					if connErr != nil {
//						log.Println(connErr)
//						continue
//					}
//
//					connId := uuid.New().String()
//					// 将接收连接的处理过程放入协程
//					go func() {
//						w.Dial(w.serverAddr, connId, ConnectionType_From, func(invoker *Invoker) {
//							invReq := NewInvokeRequest(uuid.New().String(), "")
//							invReq.BodyJson = utils.GetJsonString(tq)
//							invReq.Header[HeadKey_Connection_Id] = connId
//							invReq.Header[HeadKey_TunnelId] = w.clientInfo.TunnelId
//							invReq.Header[HeadKey_TerminalId] = w.clientInfo.TerminalId
//							reqErr := invoker.WriteInvoke(invReq)
//
//							if reqErr != nil {
//								log.Println(reqErr)
//								invoker.CtxCancel()
//								return
//							}
//
//							transferStream := NewTransferSession(connId, invoker)
//							transferStream.SetTargetStream(conn)
//							transferStream.SetOnClose(func() {
//								transferStream.CloseStream()
//								w.deleteTransfer(connId)
//							})
//							w.setTransfer(connId, transferStream)
//
//							go func() {
//								select {
//								case <-transferStream.Ctx().Done():
//									break
//								default:
//									transferStream.Transfer()
//								}
//								_ = transferStream.Close()
//								_ = w.DefaultInvoker().WriteInvoke(NewInvokeRequest(connId, InvokePath_Transfer_Disconnect))
//							}()
//						})
//					}()
//				}
//			}
//		}()
//	}
//	return nil
//}

//func (w *Client) connectTarget(connId string, tq *TransferRequest) error {
//	sourceUrl := tq.GetTargetUrl()
//	host := strings.ToLower(strings.TrimSpace(sourceUrl.Host))
//	port := ""
//	scheme := sourceUrl.Scheme
//	ar := strings.Split(host, ":")
//	if len(ar) > 1 {
//		port = ar[1]
//	}
//	switch sourceUrl.Scheme {
//	case "http":
//		scheme = "tcp"
//		if port == "" {
//			host = host + ":80"
//		}
//		break
//	case "https":
//		scheme = "tcp"
//		if port == "" {
//			host = host + ":443"
//		}
//		break
//	}
//
//	conn, listenErr := net.Dial(scheme, host)
//	if listenErr != nil {
//		log.Println(fmt.Sprintf("被控通道连接到目标服务[%v]失败", sourceUrl.Host))
//		return listenErr
//	}
//
//	return w.Dial(w.serverAddr, connId, ConnectionType_Target, func(invoker *Invoker) {
//		// 此处写入1个前置字节，只是为了推动流在服务端能够AcceptStream
//		b := []byte{88}
//		_ = utils.WriteBytes(invoker, b)
//		transferStream := NewTransferSession(connId, invoker)
//		transferStream.SetTargetStream(conn)
//		w.setTransfer(connId, transferStream)
//		transferStream.Transfer()
//	})
//}
//
//func (w *Client) onTransferConnDial(_ *Invoker, r *InvokeRequest) {
//	transferMapReq := &TransferRequest{}
//	utils.GetJsonValue(transferMapReq, r.BodyJson)
//	connId := r.Header[HeadKey_Connection_Id]
//	if err := w.connectTarget(connId, transferMapReq); err != nil {
//		log.Println(fmt.Sprintf("被控通道[%v]连接到目标服务[%v]失败:%v",
//			transferMapReq.TargetTerminalTunnelId, transferMapReq.TargetTerminalUri, err.Error()))
//	}
//
//}
//
//func (w *Client) onTransferGo(invoker *Invoker, r *InvokeRequest) {
//	connId := r.Header[HeadKey_Connection_Id]
//	transfer := w.getTransfer(connId)
//	if transfer != nil {
//		transfer.TransferChan() <- true
//	} else {
//		log.Println("Transfer连接丢失:", connId)
//	}
//}
//
//func (w *Client) onTransferDisconnect(invoker *Invoker, request *InvokeRequest) {
//	transfer := w.getTransfer(request.RequestId)
//	if transfer != nil {
//		_ = transfer.Close()
//	}
//}

func NewTransferClient(ctx context.Context, serverAddr string, clientInfo *rpc.ClientConnInfo) *Client {
	w := &Client{serverAddr: serverAddr, clientInfo: clientInfo}
	w.SetCtx(ctx)
	w.invokeRoute = rpc.NewInvokeRoute(w.Ctx())
	return w
}
