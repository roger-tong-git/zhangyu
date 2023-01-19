package app

import (
	"github.com/roger-tong-git/zhangyu/rpc"
)

type ClientSession struct {
	ClientInvoker *rpc.Invoker `json:"-"`
	rpc.ClientConnInfo
}
