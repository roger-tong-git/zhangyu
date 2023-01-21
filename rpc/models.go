package rpc

import "time"

type OnlineNode struct {
	NodeId         string
	ConnectionId   string
	ConnectionTime time.Time
	ServerAddr     string
}

type OnlineClient struct {
	NodeId       string
	ClientId     string
	InvokerId    string
	ConnectTime  time.Time
	RemoteAddr   string
	ClientIP     string
	SendBytes    int64
	ReceiveBytes int64
}

type Tunnel struct {
	TunnelId   string
	TerminalId string
	AuthCode   string
}

type ClientRec struct {
	ClientId   string
	Token      string
	TunnelId   string
	AuthCode   string
	CreateTime time.Time
}

type Listen struct {
	ListenKey      string
	ListenClientId string
	ListenTunnelId string
	TargetClientId string
	TargetTunnelId string
	ListenAddr     string
	TargetAddr     string
}

type Domain struct {
	DomainName string
	ListenKey  string
}

type ClientLoginRequest struct {
	ClientId string
	Token    string
}
