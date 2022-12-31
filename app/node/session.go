package node

import "github.com/roger-tong-git/zhangyu/network"

type ClientSession struct {
	ClientInvoker *network.Invoker `json:"-"`
	network.ClientConnInfo
}
