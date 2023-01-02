package node

import (
	"context"
	"github.com/roger-tong-git/zhangyu/network"
	"github.com/roger-tong-git/zhangyu/utils"
)

type ClientSession struct {
	ClientIP      string
	ClientInvoker *network.Invoker `json:"-"`
	network.ClientConnInfo
}

type TransferSession struct {
	transferChan chan bool
	*network.TransferStream
	utils.Closer
}

func (t *TransferSession) TransferChan() chan bool {
	return t.transferChan
}

func NewTransferSession(ctx context.Context, transferId string, sourceStream network.ReadWriter) *TransferSession {
	re := &TransferSession{transferChan: make(chan bool)}
	re.TransferStream = network.NewTransferStream(transferId, sourceStream, nil)
	re.SetCtx(ctx)
	re.SetOnClose(func() {
		if s := re.SourceStream(); s != nil {
			_ = s.Close()
		}
		if d := re.TargetStream(); d != nil {
			_ = d.Close()
		}
	})
	return re
}
