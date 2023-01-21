package rpc

import (
	"net/url"
)

// TerminalInfo 终端信息
type TerminalInfo struct {
	TerminalId string
	Type       TerminalType
}

// ConnectionInfo 终端连接信息
type ConnectionInfo struct {
	ConnectionId string
}

// NodeInfo 节点信息
type NodeInfo struct {
	NodeAddr string
	TerminalInfo
	ConnectionInfo
}

// ClientInfo 章鱼客户端信息
type ClientInfo struct {
	TunnelId string
	AuthCode string
	Token    string
	TerminalInfo
}

// ClientConnInfo 章鱼客户端连接信息
type ClientConnInfo struct {
	NodeId string
	ClientInfo
	ConnectionInfo
}

// TransferRequest 传输请求
type TransferRequest struct {
	ListenTerminalId       string `json:"listen_terminal_id,omitempty"`
	ListenTerminalToken    string `json:"listen_terminal_token,omitempty"`
	ListenTerminalUri      string `json:"listen_terminal_uri,omitempty"`
	TargetTerminalTunnelId string `json:"target_terminal_tunnel_id,omitempty"`
	TargetTerminalAuthCode string `json:"target_terminal_auth_code,omitempty"`
	TargetTerminalUri      string `json:"target_terminal_uri,omitempty"`
}

func (t *TransferRequest) GetListenUrl() *url.URL {
	uri, _ := url.Parse(t.ListenTerminalUri)
	return uri
}

func (t *TransferRequest) GetTargetUrl() *url.URL {
	uri, _ := url.Parse(t.TargetTerminalUri)
	return uri
}
