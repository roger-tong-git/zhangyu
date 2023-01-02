package node

import (
	"context"
	"github.com/roger-tong-git/zhangyu/network"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
)

type ClientSession struct {
	ClientIP      string
	ClientInvoker *network.Invoker `json:"-"`
	network.ClientConnInfo
}

type Instance struct {
	ConnectionId string
	TargetUri    string
	SourceConn   io.ReadWriter
	TargetConn   io.ReadWriter
	TransChan    chan bool
	utils.Closer
}

func (i *Instance) Close() error {
	if i.SourceConn != nil {
		if v, ok := i.SourceConn.(io.Closer); ok {
			_ = v.Close()
		}
	}

	if i.TargetConn != nil {
		if v, ok := i.TargetConn.(io.Closer); ok {
			_ = v.Close()
		}
	}
	i.TransChan <- true
	return nil
}

func NewInstance(ctx context.Context, connectionId string, targetUri string, sourceConn io.ReadWriter) *Instance {
	s := &Instance{ConnectionId: connectionId, SourceConn: sourceConn, TargetUri: targetUri, TransChan: make(chan bool)}
	s.SetCtx(ctx)
	s.SetOnClose(func() {
		_ = s.Close()
	})
	return s
}

func (i *Instance) Transfer(endHandler func()) {
	<-i.TransChan
	go func() {
		if _, err := io.Copy(i.SourceConn, i.TargetConn); err != nil {
			if v, ok := i.SourceConn.(io.Closer); ok {
				_ = v.Close()
			}
			if v, ok := i.TargetConn.(io.Closer); ok {
				_ = v.Close()
			}
		}
		if endHandler != nil {
			endHandler()
		}
	}()
	go func() {
		if _, err := io.Copy(i.TargetConn, i.SourceConn); err != nil {
			if v, ok := i.SourceConn.(io.Closer); ok {
				_ = v.Close()
			}
			if v, ok := i.TargetConn.(io.Closer); ok {
				_ = v.Close()
			}
		}

		if endHandler != nil {
			endHandler()
		}
	}()
}
