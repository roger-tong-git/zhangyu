package rpc

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
