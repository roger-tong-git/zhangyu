package client

import (
	"context"
	"fmt"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/rpc/quic/cli"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"os"
)

type Client struct {
	NodeAddr         string `json:"nodeAddr,omitempty"`
	HeartBeatSeconds int    `json:"heartBeatSeconds,omitempty"`
	TerminalId       string `json:"terminalId,omitempty"`
	Token            string `json:"token,omitempty"`
	TunnelId         string `json:"-"`
	ConnectionId     string `json:"-"`
	client           *cli.Client
	utils.Closer     `json:"-"`
}

func (c *Client) OnNewClient(clientRec *rpc.ClientRec) {
	c.TerminalId = clientRec.ClientId
	c.Token = clientRec.Token
	c.TunnelId = clientRec.TunnelId
	c.login()
}

func (c *Client) OnConnected(invoker *rpc.Invoker) {
	if c.Token != "" && c.TerminalId != "" {
		c.login()
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
		log.Println(fmt.Sprintf("通道[%v]初始化成功,验证码[%v]", rec.TunnelId, rec.AuthCode))
	}
	utils.SaveJsonSetting("client.json", c)
}

func (c *Client) onInvoker_Kick(invoker *rpc.Invoker, request *rpc.InvokeRequest) {
	log.Println("有新的客户端录入，当前客户端将退出")
	_ = invoker.Close()
	os.Exit(1)
}

func NewClient(ctx context.Context, nodeAddr string) *Client {
	re := &Client{NodeAddr: nodeAddr}
	re.SetCtx(ctx)
	utils.ReadJsonSetting("client.json", re, func() {
		re.HeartBeatSeconds = 10
	})
	re.NodeAddr = nodeAddr
	re.InitClient(re.TerminalId, re.Token)
	return re
}
