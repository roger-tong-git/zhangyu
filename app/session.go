package app

import (
	"github.com/roger-tong-git/zhangyu/network"
)

type ClientSession struct {
	ClientIP      string
	ClientInvoker *network.Invoker `json:"-"`
	network.ClientConnInfo
}
