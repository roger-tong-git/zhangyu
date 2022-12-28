package network

type TerminalType int

const (
	TerminalType_Node     = TerminalType(1)
	TerminalType_Client   = TerminalType(2)
	HeadKey_TerminalId    = "Zhangyu-TerminalId"
	HeadKey_ConnectionId  = "Zhangyu-ConnectionId"
	WebSocket_ServicePath = "/_zhangyu/websocket"
)

type TerminalInfo struct {
	TerminalId string
	Type       TerminalType
}

type ConnectionInfo struct {
	ConnectionId string
}

type NodeInfo struct {
	NodeAddr string
	TerminalInfo
	ConnectionInfo
}

type ClientInfo struct {
	TunnelId string
	AuthCode string
	Token    string
	TerminalInfo
}

type ClientConnInfo struct {
	NodeId string
	ClientInfo
	ConnectionInfo
}
