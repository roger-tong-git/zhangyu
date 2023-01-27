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
	InvokePath_Transfer_Go         = "/transfer/go"
	InvokePath_Transfer_ListenIn   = "/transfer/listen/in"
	InvokePath_Transfer_ListenAdd  = "/transfer/listen/add"
	InvokePath_Transfer_ListenDel  = "/transfer/listen/del"
	InvokePath_Transfer_TargetOut  = "/transfer/target/out"
	InvokePath_Client_Kick         = "/client/kick"
	InvokePath_Client_SetId        = "/client/connection/id"
	InvokePath_Client_New          = "/client/record/new"
	InvokePath_Client_Login        = "/client/login"
	InvokePath_Client_Heartbeat    = "/client/heartbeat"
	InvokePath_Client_TransferList = "/client/transfer/list"
)

const (
	HeadKey_Connection_TerminalId = "Zhangyu-Connection-TerminalId" //当前连接对应的终端ID
	HeadKey_Connection_Type       = "Zhangyu-Connection-Type"       //连接类型
)

const (
	ConnectionType_Command = "Command"         //命令连接
	ConnectionType_Listen  = "Instance_Listen" //流量连接
	ConnectionType_Target  = "Instance_Target"
)

var (
	InvalidInvokerConnect = errors.New("无效的WebTransport连接")
)

const (
	InvokeResult_Success = InvokeResult(200)
	InvokeResult_Error   = InvokeResult(400)
)
