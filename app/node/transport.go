package node

import (
	"github.com/roger-tong-git/zhangyu/rpc"
	"net/http"
)

type httpTransport struct {
	key       string
	invoker   *rpc.Invoker
	transChan chan bool
	transport *http.Transport
}

func NewHttpTransport(key string) *httpTransport {
	return &httpTransport{key: key, transChan: make(chan bool)}
}
