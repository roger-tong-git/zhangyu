package network

import (
	"github.com/marten-seemann/webtransport-go"
	"io"
	"log"
	"net"
	"net/url"
	"time"
)

type TerminalType int

type ConnectionType string

const (
	TerminalType_Node          = TerminalType(1)
	TerminalType_Client        = TerminalType(2)
	HeadKey_TerminalId         = "Zhangyu-TerminalId"
	HeadKey_ConnectionId       = "Zhangyu-ConnectionId"
	HeadKey_ConnectionType     = "Zhangyu-ConnectionType" //连接类型
	WebSocket_ServicePath      = "/_zhangyu/websocket"
	Connection_Command         = "Command"       //命令连接
	Connection_Instance_From   = "Instance_From" //流量连接
	Connection_Instance_Target = "Instance_Target"
)

// TerminalInfo 终端信息
type TerminalInfo struct {
	TerminalId string
	Type       TerminalType
}

// ConnectionInfo 终端连接信息
type ConnectionInfo struct {
	ConnectionId string
}

// NodeInfo 节点信息
type NodeInfo struct {
	NodeAddr string
	TerminalInfo
	ConnectionInfo
}

// ClientInfo 客户端信息
type ClientInfo struct {
	TunnelId string
	AuthCode string
	Token    string
	TerminalInfo
}

// ClientConnInfo 客户端连接信息
type ClientConnInfo struct {
	NodeId string
	ClientInfo
	ConnectionInfo
}

// TransferRequest 传输请求
type TransferRequest struct {
	ListenTerminalId       string `json:"listen_terminal_id,omitempty"`
	ListenTerminalToken    string `json:"listen_terminal_token,omitempty"`
	ListenTerminalUri      string `json:"listen_terminal_uri,omitempty"`
	TargetTerminalTunnelId string `json:"target_terminal_tunnel_id,omitempty"`
	TargetTerminalAuthCode string `json:"target_terminal_auth_code,omitempty"`
	TargetTerminalUri      string `json:"target_terminal_uri,omitempty"`
}

func (t *TransferRequest) GetListenUrl() *url.URL {
	uri, _ := url.Parse(t.ListenTerminalUri)
	return uri
}

func (t *TransferRequest) GetTargetUrl() *url.URL {
	uri, _ := url.Parse(t.TargetTerminalUri)
	return uri
}

type ReadWriter interface {
	io.ReadWriter
	io.Closer
}

// WebtransportConn 将Webtransport的session/stream包装成net.conn接口
type WebtransportConn struct {
	session *webtransport.Session
	stream  webtransport.Stream
}

func NewWebtransportConn(session *webtransport.Session, stream webtransport.Stream) *WebtransportConn {
	return &WebtransportConn{session: session, stream: stream}
}

func (w *WebtransportConn) Session() *webtransport.Session {
	return w.session
}

func (w *WebtransportConn) Stream() webtransport.Stream {
	return w.stream
}

func (w *WebtransportConn) Read(p []byte) (int, error) {
	return w.stream.Read(p)
}

func (w *WebtransportConn) Write(p []byte) (int, error) {
	return w.stream.Write(p)
}

func (w *WebtransportConn) Close() error {
	return w.stream.Close()
}

func (w *WebtransportConn) LocalAddr() net.Addr {
	return w.session.LocalAddr()
}

func (w *WebtransportConn) RemoteAddr() net.Addr {
	return w.session.RemoteAddr()
}

func (w *WebtransportConn) SetDeadline(t time.Time) error {
	return w.stream.SetDeadline(t)
}

func (w *WebtransportConn) SetReadDeadline(t time.Time) error {
	return w.stream.SetReadDeadline(t)
}

func (w *WebtransportConn) SetWriteDeadline(t time.Time) error {
	return w.stream.SetWriteDeadline(t)
}

type TransferStream struct {
	transferId   string
	sourceStream ReadWriter
	targetStream ReadWriter
}

func (t *TransferStream) SetTargetStream(targetStream ReadWriter) {
	t.targetStream = targetStream
}

func (t *TransferStream) TransferId() string {
	return t.transferId
}

func (t *TransferStream) SourceStream() ReadWriter {
	return t.sourceStream
}

func (t *TransferStream) TargetStream() ReadWriter {
	return t.targetStream
}

func NewTransferStream(transferId string, sourceStream ReadWriter, targetStream ReadWriter) *TransferStream {
	return &TransferStream{transferId: transferId, sourceStream: sourceStream, targetStream: targetStream}
}

func (t *TransferStream) Transfer() {
	if t.sourceStream == nil {
		log.Println("传输失败,sourceStream不能为nil")
		return
	}

	if t.targetStream == nil {
		log.Println("传输失败,targetStream不能为nil")
		return
	}

	errChan := make(chan error)

	go func() {
		_, err := io.Copy(t.sourceStream, t.targetStream)
		errChan <- err
	}()

	go func() {
		_, err := io.Copy(t.targetStream, t.sourceStream)
		errChan <- err
	}()

	<-errChan
	_ = t.targetStream.Close()
	_ = t.sourceStream.Close()
}
