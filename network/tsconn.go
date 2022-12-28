package network

import (
	"context"
	"crypto/tls"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"net/http"
	"time"
)

type TransportClient struct {
	clientInfo  *ClientConnInfo
	invokeRoute *InvokeRoute
	connected   bool
	lastAddr    string
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
	if err != nil && err == WebTransportConnectError && w.lastAddr != "" {
		go func() {
			log.Println("尝试重新连接到服务端")
			w.connected = false
			_ = w.ConnectTo(w.lastAddr)
		}()
		return NewInvokeResponse(r.RequestId, InvokeResult_Error, err.Error())
	}
	return re
}

func (w *TransportClient) ConnectTo(addr string) error {
	var err error
	header := http.Header{}
	header.Set(HeadKey_TerminalId, w.clientInfo.TerminalId)
	header.Set(HeadKey_ConnectionId, w.clientInfo.ConnectionId)

	var d webtransport.Dialer
	var session *webtransport.Session
	d.RoundTripper = &http3.RoundTripper{}
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	if !w.connected {
		if _, session, err = d.Dial(w.Ctx(), addr, header); err != nil {
			return err
		}

		if stream, err1 := session.OpenStream(); err1 != nil {
			return err1
		} else {
			w.connected = true
			w.lastAddr = addr
			invoker := w.invokeRoute.AddInvoker(w.clientInfo.ConnectionId, stream, stream)
			w.invokeRoute.SetDefaultInvoker(invoker)

			go func() {
				select {
				case <-invoker.Ctx().Done():
					return
				default:
					_ = w.ConnectTo(addr)
				}
			}()
		}
	} else {
		time.Sleep(time.Second)
	}

	return nil
}

func NewWebSocketClient(ctx context.Context, clientInfo *ClientConnInfo) *TransportClient {
	w := &TransportClient{clientInfo: clientInfo}
	w.SetCtx(ctx)
	w.invokeRoute = NewInvokeRoute(w.Ctx())
	return w
}
