package quic

import (
	"context"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/utils"
	"net"
	"time"
)

type ConnWrapper struct {
	utils.Closer
	stream  webtransport.Stream
	session *webtransport.Session
}

func (c *ConnWrapper) Read(b []byte) (n int, err error) {
	return c.stream.Read(b)
}

func (c *ConnWrapper) Write(b []byte) (n int, err error) {
	return c.stream.Write(b)
}

func (c *ConnWrapper) LocalAddr() net.Addr {
	return c.session.LocalAddr()
}

func (c *ConnWrapper) RemoteAddr() net.Addr {
	return c.session.RemoteAddr()
}

func (c *ConnWrapper) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *ConnWrapper) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *ConnWrapper) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

func NewConnWrapper(ctx context.Context, stream webtransport.Stream, session *webtransport.Session) *ConnWrapper {
	re := &ConnWrapper{stream: stream, session: session}
	re.SetCtx(ctx)
	return re
}
