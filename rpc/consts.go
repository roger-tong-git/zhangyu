package rpc

import "errors"

type TerminalType int
type ConnectionType string
type InvokeResult int

const (
	ServicePath = "/_zhangyu/transport"
)

const (
	TerminalType_Node   = TerminalType(1)
	TerminalType_Client = TerminalType(2)
)

const (
	InvokePath_Transfer_Dial        = "/transfer/dial"
	InvokePath_Transfer_Go          = "/transfer/go"
	InvokePath_Transfer_Listen      = "/transfer/listen"
	InvokePath_Transfer_Disconnect  = "/transfer/disconnect"
	InvokePath_Client_Kick          = "/client/kick"
	InvokePath_Client_Register      = "/client/register"
	InvokePath_Client_Login         = "/client/login"
	InvokePath_Client_Heartbeat     = "/client/heartbeat"
	InvokePath_Client_Transfer_List = "/client/transfer/list"
)

const (
	HeadKey_TerminalId          = "Zhangyu-TerminalId"
	HeadKey_TunnelId            = "Zhangyu-TunnelId"
	HeadKey_ConnectionId        = "Zhangyu-ConnectionId"         // 当前连接ID
	HeadKey_CommandConnectionId = "Zhangyu-Command-ConnectionId" //主通道连接ID
	HeadKey_ConnectionType      = "Zhangyu-ConnectionType"       //连接类型
)

const (
	HttpPath_Transfer_Add = "/_zhangyu/transfer/add"
)

const (
	ConnectionType_Command = "Command"       //命令连接
	ConnectionType_From    = "Instance_From" //流量连接
	ConnectionType_Target  = "Instance_Target"
)

var (
	InvalidInvokerConnect = errors.New("无效的WebTransport连接")
)

const (
	InvokeResult_Success = InvokeResult(200)
	InvokeResult_Error   = InvokeResult(400)
)
