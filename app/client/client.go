package client

import (
	"context"
	"fmt"
	"github.com/armon/go-socks5"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/rpc/quic/cli"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
	"log"
	"net"
	"net/url"
	"os"
)

type Client struct {
	NodeAddr         string `json:"nodeAddr,omitempty"`
	HeartBeatSeconds int    `json:"heartBeatSeconds,omitempty"`
	TerminalId       string `json:"terminalId,omitempty"`
	Token            string `json:"token,omitempty"`
	TunnelId         string
	AuthCode         string
	ConnectionId     string `json:"-"`
	client           *cli.Client
	utils.Closer     `json:"-"`
	listenStarted    bool
	socks5Srv        *socks5.Server
}

func (c *Client) getSocksServer() *socks5.Server {
	if c.socks5Srv == nil {
		c.socks5Srv, _ = socks5.New(&socks5.Config{
			Logger: log.New(io.Discard, "", log.LstdFlags),
		})
	}
	return c.socks5Srv
}

func (c *Client) OnNewClient(clientRec *rpc.ClientRec) {
	c.TerminalId = clientRec.ClientId
	c.Token = clientRec.Token
	c.TunnelId = clientRec.TunnelId
	c.login()
}

func (c *Client) Invoke(request *rpc.InvokeRequest) (*rpc.InvokeResponse, error) {
	return c.client.DefaultInvoker().Invoke(request)
}

func (c *Client) TcpListen(listen *rpc.Listen) {
	listenUrl, _ := url.Parse(listen.ListenAddr)
	listener, e := net.Listen("tcp", listenUrl.Host)
	if e != nil {
		log.Printf("打开监听端口[%v]失败:%v", listenUrl.Host, e)
		return
	} else {
		log.Println(fmt.Sprintf("已打开监听端口[%v]=>[%v|%v]", listenUrl.String(), listen.TargetTunnelId, listen.TargetAddr))
	}

	go func() {
		for {
			var conn net.Conn
			var err error
			conn, err = listener.Accept()
			if err != nil {
				log.Println(err.Error())
				continue
			}

			go func() {
				err = c.client.Dial(c.getServerAddr(), rpc.ConnectionType_Listen, func(i *rpc.Invoker, session *webtransport.Session) {
					req := rpc.NewInvokeRequest(rpc.InvokePath_Transfer_ListenIn)
					req.PutValue(listen)
					if i.WriteRequest(req) != nil {
						_ = i.Close()
					}
					i.Copy(conn)
				})

				if err != nil {
					log.Println(err)
				}
			}()
		}
	}()
}

func (c *Client) UdpListen(listen *rpc.Listen) {
	listenUrl, _ := url.Parse(listen.ListenAddr)
	udpAddr, _ := net.ResolveUDPAddr("udp", listenUrl.Host)
	conn, e := net.ListenUDP("udp", udpAddr)
	if e != nil {
		log.Printf("打开监听端口[%v]失败:%v", listenUrl.Host, e)
		return
	} else {
		log.Println(fmt.Sprintf("已打开监听端口[%v]=>[%v|%v]", listenUrl.String(), listen.TargetTunnelId, listen.TargetAddr))
	}

	//conn.wr

	go func() {
		err := c.client.Dial(c.getServerAddr(), rpc.ConnectionType_Listen, func(i *rpc.Invoker, session *webtransport.Session) {
			req := rpc.NewInvokeRequest(rpc.InvokePath_Transfer_ListenIn)
			req.PutValue(listen)
			if i.WriteRequest(req) != nil {
				_ = i.Close()
			}
			i.Copy(conn)
		})

		if err != nil {
			log.Println(err)
		}

	}()

	//go func() {
	//	for {
	//		var conn net.Conn
	//		var err error
	//		conn, err = listener.Accept()
	//		if err != nil {
	//			log.Println(err.Error())
	//			continue
	//		}
	//
	//		go func() {
	//			err = c.client.Dial(c.getServerAddr(), rpc.ConnectionType_Listen, func(i *rpc.Invoker, session *webtransport.Session) {
	//				req := rpc.NewInvokeRequest(rpc.InvokePath_Transfer_ListenIn)
	//				req.PutValue(listen)
	//				if i.WriteRequest(req) != nil {
	//					_ = i.Close()
	//				}
	//				i.Copy(conn)
	//			})
	//
	//			if err != nil {
	//				log.Println(err)
	//			}
	//		}()
	//	}
	//}()
}

func (c *Client) AddListen(listen *rpc.Listen) {
	listenUrl, _ := url.Parse(listen.ListenAddr)
	scheme := listenUrl.Scheme

	if scheme == "" || scheme == "http" || scheme == "https" {
		return
	}

	if scheme == "socks5" {
		scheme = "tcp"
	}

	if scheme == "tcp" {
		c.TcpListen(listen)
	}
	//if scheme == "udp" {
	//	c.UdpListen(listen)
	//}
}

func (c *Client) OnConnected(invoker *rpc.Invoker) {
	if c.Token != "" && c.TerminalId != "" {
		c.login()

		if !c.listenStarted {
			req := rpc.NewInvokeRequest(rpc.InvokePath_Client_TransferList)
			resp, _ := c.Invoke(req)

			var listenList []*rpc.Listen
			if resp.GetValue(&listenList) {
				for _, listen := range listenList {
					go c.AddListen(listen)
				}
			}
			c.listenStarted = true
		}
	}
}

func (c *Client) GetTerminalId() string {
	return c.TerminalId
}

func (c *Client) SetTerminalId(terminalId string) {
	c.TerminalId = terminalId
}

func (c *Client) GetConnectionId() string {
	return c.ConnectionId
}

func (c *Client) SetConnectionId(connectionId string) {
	c.ConnectionId = connectionId
}

func (c *Client) getServerAddr() string {
	return fmt.Sprintf("https://%v%v", c.NodeAddr, rpc.ServicePath)
}

func (c *Client) InitClient(terminalId, token string) {
	c.client = cli.NewClient(c.Ctx(), c.getServerAddr(), c)
	c.client.SetHeartBeatSeconds(c.HeartBeatSeconds)
	_ = c.client.ConnectTo()

	c.client.InvokeRoute().AddUniHandler(rpc.InvokePath_Client_Kick, c.onInvoker_Kick)
	c.client.InvokeRoute().AddUniHandler(rpc.InvokePath_Transfer_TargetOut, c.onInvoker_DialOut)
	c.client.InvokeRoute().AddUniHandler(rpc.InvokePath_Transfer_ListenAdd, c.onInvoker_ListenAdd)
}

func (c *Client) login() {
	req := rpc.NewInvokeRequest(rpc.InvokePath_Client_Login)
	loginReq := &rpc.LoginRequest{
		ClientId: c.TerminalId,
		Token:    c.Token,
	}
	req.PutValue(loginReq)

	resp, err := c.client.DefaultInvoker().Invoke(req)
	if err != nil {
		log.Println(err)
		return
	}
	if resp.ResultCode != rpc.InvokeResult_Success {
		log.Println(resp.ResultMessage)
		os.Exit(1)
	}
	rec := &rpc.ClientRec{}
	if resp.GetValue(rec) {
		c.TunnelId = rec.TunnelId
		c.AuthCode = rec.AuthCode
		log.Println(fmt.Sprintf("通道[%v]初始化成功,验证码[%v]", rec.TunnelId, rec.AuthCode))
	}
	utils.SaveJsonSetting("client.json", c)
}

func (c *Client) onInvoker_Kick(invoker *rpc.Invoker, request *rpc.InvokeRequest) {
	log.Println("有新的客户端录入，当前客户端将退出")
	_ = invoker.Close()
	os.Exit(1)
}

func (c *Client) onInvoker_DialOut(_ *rpc.Invoker, request *rpc.InvokeRequest) {
	_ = c.client.Dial(c.getServerAddr(), rpc.ConnectionType_Target, func(i *rpc.Invoker, session *webtransport.Session) {
		listen := &rpc.Listen{}
		canNext := request.GetValue(listen)
		if !canNext {
			_ = i.Close()
			return
		}

		listenUrl, _ := url.Parse(listen.ListenAddr)
		targetUrl, _ := url.Parse(listen.TargetAddr)
		rawScheme := listenUrl.Scheme
		if rawScheme == "" {
			rawScheme = targetUrl.Scheme
		}

		rawHost := targetUrl.Host
		host := rawHost
		scheme := rawScheme
		if scheme == "http" || scheme == "https" {
			scheme = "tcp"
			if listenUrl.Port() == "" {
				switch rawScheme {
				case "http":
					host = host + ":80"
					break
				case "https":
					host = host + ":443"
					break
				}
			}
		}

		request.Path = rpc.InvokePath_Transfer_Go
		request.JsonBody = ""
		if wErr := i.WriteRequest(request); wErr == nil {
			switch scheme {
			case "socks5":
				conn := i.GetAttach("Conn").(net.Conn)
				go func() {
					_ = c.getSocksServer().ServeConn(conn)
					_ = i.Close()
				}()
				break
			case "tcp":
				conn, err := net.Dial("tcp", host)
				if err != nil {
					_ = i.Close()
				}
				go i.Copy(conn)
				break
			case "udp":
				conn, err := net.Dial("udp", host)
				if err != nil {
					_ = i.Close()
				}
				go i.Copy(conn)
				break
			}
		} else {
			_ = i.Close()
		}
	})
}

func (c *Client) onInvoker_ListenAdd(_ *rpc.Invoker, request *rpc.InvokeRequest) {
	listen := &rpc.Listen{}
	canNext := request.GetValue(listen)
	if canNext {
		c.AddListen(listen)
	}
}

func NewClient(ctx context.Context) *Client {
	re := &Client{}
	re.SetCtx(ctx)
	utils.ReadJsonSetting("client.json", re, func() {
		re.HeartBeatSeconds = 10
		re.NodeAddr = "zhangyu.io"
	})
	re.InitClient(re.TerminalId, re.Token)
	return re
}
