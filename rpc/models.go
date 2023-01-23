package rpc

import (
	"time"
)

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
	ListenClientId string
	ListenTunnelId string
	TargetClientId string
	TargetTunnelId string
	ListenAddr     string
	TargetAddr     string
	CreateTime     time.Time
}

type Domain struct {
	DomainName string
	ListenKey  string
	ListenInfo *Listen `json:"listen_Info,omitempty"`
}

type LoginRequest struct {
	ClientId string `json:"clientId,omitempty"`
	Token    string `json:"token,omitempty"`
	AppId    string `json:"appId,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

type ListenRequest struct {
	ListenClientId string `json:"listenClientId"`
	TargetTunnelId string `json:"targetTunnelId"`
	TargetAuthCode string `json:"targetAuthCode"`
	ListenAddr     string `json:"listenAddr"`
	TargetAddr     string `json:"targetAddr,omitempty"`
}

type LoginResponse struct {
	TokenString string `json:"tokenString"`
}
