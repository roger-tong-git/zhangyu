package network

import (
	"context"
	"github.com/roger-tong-git/zhangyu/utils"
	"net/http"
)

type TransferSession struct {
	transferChan chan bool
	transport    *http.Transport
	TransferStream
	utils.Closer
}

func (t *TransferSession) Transport() *http.Transport {
	return t.transport
}

func (t *TransferSession) SetTransport(transport *http.Transport) {
	t.transport = transport
}

func (t *TransferSession) TransferChan() chan bool {
	return t.transferChan
}

func NewTransferSession(connId string, sourceStream SessionReadWriter) *TransferSession {
	re := &TransferSession{transferChan: make(chan bool, 1)}
	re.TransferStream = *NewTransferStream(connId, sourceStream, nil)
	re.SetCtx(sourceStream.Ctx())
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

func NewTransferSessionWithValue(ctx context.Context, connId string) *TransferSession {
	re := &TransferSession{transferChan: make(chan bool)}
	re.TransferStream = *NewTransferStream(connId, nil, nil)
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
