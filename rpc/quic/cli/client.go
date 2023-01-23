package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/rpc/quic"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"net/http"
	"time"
)

type ClientAdapter interface {
	OnNewClient(clientRec *rpc.ClientRec)
	OnConnected(invoker *rpc.Invoker)
	GetTerminalId() string
	SetTerminalId(terminalId string)
	GetConnectionId() string
	SetConnectionId(connectionId string)
}

// Client WebTransport客户端
type Client struct {
	invokeRoute      *rpc.InvokeRoute
	connected        bool
	keepaliveRun     bool
	serverAddr       string
	heartBeatSeconds int
	adapter          ClientAdapter
	utils.Closer
}

func (c *Client) SetHeartBeatSeconds(heartBeatSeconds int) {
	c.heartBeatSeconds = heartBeatSeconds
}

func (c *Client) getHeartBeatTime() time.Duration {
	if c.heartBeatSeconds <= 0 {
		c.heartBeatSeconds = 20
	}
	return time.Duration(c.heartBeatSeconds) * time.Second
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

func (c *Client) Dial(addr string, connType string,
	connectedHandler func(invoker *rpc.Invoker, session *webtransport.Session)) error {
	header := http.Header{}
	header.Set(rpc.HeadKey_Connection_TerminalId, c.adapter.GetTerminalId())
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
		invoker := c.invokeRoute.AddDialInvoker(c.invokeRoute.Ctx(), stream)
		invoker.SetAttach("Session", session)
		invoker.SetAttach("Conn", quic.NewConnWrapper(invoker.Ctx(), stream, session))
		invoker.SetOnClose(func() {
			_ = invoker.ReadWriter().Close()
			_ = session.CloseWithError(0, "")
			c.invokeRoute.RemoveInvoker(invoker.InvokerId())
		})
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
		case <-time.After(c.getHeartBeatTime()):
			if !c.connected {
				continue
			}
			req := rpc.NewInvokeRequest(rpc.InvokePath_Client_Heartbeat)
			c.connected = c.DefaultInvoker().WriteRequest(req) == nil
		}
	}
}

func (c *Client) ConnectTo() error {
	err := c.Dial(c.serverAddr, rpc.ConnectionType_Command,
		func(invoker *rpc.Invoker, _ *webtransport.Session) {
			c.invokeRoute.SetDefaultInvoker(invoker)
			invoker.SetIsCommandTunnel(true)
			c.adapter.SetConnectionId(invoker.InvokerId())
			c.adapter.SetTerminalId(invoker.TerminalId())
			invoker.SetWriteErrorHandler(func(_ error) {
				c.connected = false
			})
			invoker.SetReadErrorHandler(func(_ error) {
				c.connected = false
			})
			go c.invokeRoute.DispatchInvoke(invoker)
			c.adapter.OnConnected(invoker)
			log.Println(fmt.Sprintf("已连接到Zhangyu服务端,当前客户ID[%v]", c.adapter.GetTerminalId()))
			log.Println(fmt.Sprintf("已连接到Zhangyu服务端,当前InvokerIDD[%v]", c.adapter.GetConnectionId()))
		})

	if err != nil {
		log.Println(err)
	}

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

func (c *Client) OnClientNew(invoker *rpc.Invoker, request *rpc.InvokeRequest) {
	clientRec := &rpc.ClientRec{}
	request.GetValue(clientRec)
	c.adapter.OnNewClient(clientRec)
}

func NewClient(ctx context.Context, serverAddr string, adapter ClientAdapter) *Client {
	w := &Client{serverAddr: serverAddr, adapter: adapter}
	w.SetCtx(ctx)
	w.invokeRoute = rpc.NewInvokeRoute(w.Ctx())
	w.invokeRoute.AddUniHandler(rpc.InvokePath_Client_New, w.OnClientNew)
	return w
}
