package client

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/rpc/quic"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"time"
)

func StartClient(serverAddr string) bool {
	c := &Client{}
	c.connectionId = uuid.New().String()
	utils.ReadJsonSetting("client.json", c, func() {
		c.TerminalId = uuid.New().String()
	})
	c.NodeAddr = serverAddr
	c.SetCtx(context.Background())
	c.SetOnClose(c.onClose)
	return c.Connect() == nil
}

type Client struct {
	TerminalId       string `json:"terminalId,omitempty"`
	NodeAddr         string `json:"nodeAddr,omitempty"`
	TunnelId         string `json:"tunnelId,omitempty"`
	AuthCode         string `json:"authCode,omitempty"`
	Token            string `json:"token,omitempty"`
	CanUseAuthCode   bool   `json:"canUseAuthCode,omitempty"`
	connectionId     string
	transportCli     *quic.Client
	utils.Closer     `json:"-"`
	addListenPorted  bool
	sayOnlineRunning bool
}

func (c *Client) onClose() {

}

func (c *Client) Close() {
	c.CtxCancel()
}

// Connect 注册新的客户端
func (c *Client) Connect() error {
	cliInfo := &rpc.ClientConnInfo{}
	cliInfo.TerminalId = c.TerminalId
	cliInfo.ConnectionId = c.connectionId
	cliInfo.Token = c.Token
	cliInfo.Type = rpc.TerminalType_Client
	c.transportCli = quic.NewTransferClient(c.Ctx(), cliInfo)

	return c.transportCli.ConnectTo(c.NodeAddr, func() {
		time.Sleep(time.Second)
		path := rpc.InvokePath_Client_Register
		if c.Token != "" || c.TunnelId != "" {
			path = rpc.InvokePath_Client_Login
		}
		req := rpc.NewInvokeRequest(c.TerminalId, path)
		req.BodyJson = utils.GetJsonString(cliInfo)
		resp := c.transportCli.Invoke(req)
		if resp.ResultCode == rpc.InvokeResult_Success {
			utils.GetJsonValue(cliInfo, resp.BodyJson)
			c.TunnelId = cliInfo.TunnelId
			c.AuthCode = cliInfo.AuthCode
			c.Token = cliInfo.Token
			utils.SaveJsonSetting("client.json", c)
			log.Println(fmt.Sprintf("客户端ID[%v]注册成功", c.TerminalId))
			log.Println(fmt.Sprintf("客户端通道ID[%v],客户端验证码[%v]", c.TunnelId, c.AuthCode))

			if !c.addListenPorted {
				recPath := rpc.InvokePath_Client_Transfer_List
				req.Path = recPath
				resp = c.transportCli.Invoke(req)
				if resp.ResultCode == rpc.InvokeResult_Success {
					var mapReqs []*rpc.TransferRequest
					utils.GetJsonValue(&mapReqs, resp.BodyJson)
					for _, v := range mapReqs {
						c.transportCli.AppendTransferListen(v)
					}
				}
				c.addListenPorted = true
			}
		}
	})
}

func (c *Client) IsConnected() bool {
	if c.transportCli == nil {
		return false
	}

	return c.transportCli.Connected()
}

func NewClient(ctx context.Context) *Client {
	c := &Client{}
	c.connectionId = uuid.New().String()
	utils.ReadJsonSetting("client.json", c, func() {
		c.TerminalId = uuid.New().String()
		c.NodeAddr = "https://127.0.0.1:18888" + rpc.ServicePath
	})
	c.SetCtx(ctx)
	c.SetOnClose(c.onClose)
	_ = c.Connect()
	return c
}
