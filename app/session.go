package app

import (
	"github.com/roger-tong-git/zhangyu/rpc"
)

type ClientSession struct {
	ClientIP      string
	ClientInvoker *rpc.Invoker `json:"-"`
	rpc.ClientConnInfo
}
