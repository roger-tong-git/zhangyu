package client

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/roger-tong-git/zhangyu/network"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"time"
)

type Client struct {
	TerminalId     string `json:"terminalId,omitempty"`
	NodeAddr       string `json:"nodeAddr,omitempty"`
	TunnelId       string `json:"tunnelId,omitempty"`
	AuthCode       string `json:"authCode,omitempty"`
	Token          string `json:"token,omitempty"`
	CanUseAuthCode bool   `json:"canUseAuthCode,omitempty"`
	connectionId   string
	transportCli   *network.TransportClient
	utils.Closer   `json:"-"`
}

func (c *Client) onClose() {

}

func (c *Client) Close() {
	c.CtxCancel()
}

func (c *Client) sayOnline() {
	for {
		select {
		case <-c.Ctx().Done():
			return
		case <-time.After(time.Second * 5):
			req := network.NewInvokeRequest(c.TerminalId, "/client/sayOnline")
			req.FromId = c.TerminalId
			req.FromType = network.InvokeTerminal_Client
			cliInfo := &network.ClientConnInfo{}
			cliInfo.TerminalId = c.TerminalId
			cliInfo.ConnectionId = c.connectionId
			cliInfo.TunnelId = c.TunnelId
			cliInfo.Token = c.Token
			cliInfo.Type = network.TerminalType_Client
			req.BodyJson = utils.GetJsonString(cliInfo)
			_ = c.transportCli.Invoke(req)
		}
	}
}

// Connect 注册新的客户端
func (c *Client) Connect() error {
	cliInfo := &network.ClientConnInfo{}
	cliInfo.TerminalId = c.TerminalId
	cliInfo.ConnectionId = c.connectionId
	cliInfo.Token = c.Token
	cliInfo.Type = network.TerminalType_Client
	c.transportCli = network.NewWebSocketClient(c.Ctx(), cliInfo)
	if err := c.transportCli.ConnectTo(c.NodeAddr); err == nil {
		time.Sleep(time.Second)
		path := "/client/register"
		if c.Token != "" || c.TunnelId != "" {
			path = "/client/login"
		}
		req := network.NewInvokeRequest(c.TerminalId, path)
		req.FromId = cliInfo.TerminalId
		req.FromType = network.InvokeTerminal_Client
		req.BodyJson = utils.GetJsonString(cliInfo)
		resp := c.transportCli.Invoke(req)
		if resp.ResultCode == network.InvokeResult_Success {
			utils.GetJsonValue(cliInfo, resp.BodyJson)
			c.TunnelId = cliInfo.TunnelId
			c.AuthCode = cliInfo.AuthCode
			c.Token = cliInfo.Token
			utils.SaveJsonSetting("client.json", c)
			log.Println(fmt.Sprintf("客户端ID[%v]注册成功,获得通道ID[%v]", c.TunnelId, c.TerminalId))
			go c.sayOnline()
		} else {
			log.Fatalln(resp.ResultMessage)
		}
	} else {
		log.Println("连接到服务端失败,稍后将重试")
		return err
	}
	return nil
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
		c.NodeAddr = "https://127.0.0.1:18888" + network.WebSocket_ServicePath
	})
	c.SetCtx(ctx)
	c.SetOnClose(c.onClose)
	go func() {
		for {
			select {
			case <-c.Ctx().Done():
				return
			default:
				if c.IsConnected() {
					time.Sleep(time.Second)
					continue
				} else {
					_ = c.Connect()
				}
			}
		}
	}()
	return c
}
