package network

import (
	"context"
	"errors"
	"fmt"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
	"log"
	"net"
	"net/url"
	"sync"
	"time"
)

type TerminalType int

type ConnectionType string

const (
	TerminalType_Front         = TerminalType(0)
	TerminalType_Node          = TerminalType(1)
	TerminalType_Client        = TerminalType(2)
	HeadKey_TerminalId         = "Zhangyu-TerminalId"
	HeadKey_TunnelId           = "Zhangyu-TunnelId"
	HeadKey_ConnectionId       = "Zhangyu-ConnectionId"
	HeadKey_ConnectionType     = "Zhangyu-ConnectionType" //连接类型
	HeadKey_Domain             = "Zhangyu-Domain"
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

type SessionReadWriter interface {
	ReadWriter
	WriterLock() sync.Mutex
	ReaderLock() sync.Mutex
	Close() error
	Ctx() context.Context
}

type ConnWrapper struct {
	connId      string
	readLocker  sync.Mutex
	writeLocker sync.Mutex
	conn        ReadWriter
	utils.Closer
}

func NewConnWrapper(ctx context.Context, connId string, conn ReadWriter) *ConnWrapper {
	re := &ConnWrapper{connId: connId, conn: conn}
	re.SetCtx(ctx)
	return re
}

func (c *ConnWrapper) Read(p []byte) (n int, err error) {
	return c.conn.Read(p)
}

func (c *ConnWrapper) Write(p []byte) (n int, err error) {
	return c.conn.Write(p)
}

func (c *ConnWrapper) Close() error {
	return c.conn.Close()
}

func (c *ConnWrapper) WriterLock() sync.Mutex {
	return c.writeLocker
}

func (c *ConnWrapper) ReaderLock() sync.Mutex {
	return c.readLocker
}

func (c *ConnWrapper) ConnId() string {
	return c.connId
}

func (c *ConnWrapper) Ctx() context.Context {
	return c.Closer.Ctx()
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

// ReadInvoke 从io.reader中读取数据，数据只可能是InvokeRequest/InvokeResponse/error
func ReadInvoke(mutex sync.Mutex, reader io.Reader) (*InvokeRequest, *InvokeResponse, error) {
	defer mutex.Unlock()
	mutex.Lock()

	readTypeBytes, err := utils.ReadBytes(reader, 1)
	if err != nil {
		return nil, nil, err
	}

	readType := (*readTypeBytes)[0]
	if readType != byte(1) && readType != byte(2) {
		return nil, nil, errors.New(fmt.Sprintf("读取到的invoke类型无效：%v", readType))
	}

	readSizeBytes, err := utils.ReadBytes(reader, 8)
	if err != nil {
		return nil, nil, err
	}
	var readBytes *[]byte
	readSize := utils.ByteArrayToInt(*readSizeBytes)
	if readBytes, err = utils.ReadBytes(reader, int(readSize)); err != nil {
		return nil, nil, err
	} else {
		jsonValue := string(*readBytes)
		if readType == byte(1) {
			req := &InvokeRequest{}
			utils.GetJsonValue(req, jsonValue)
			return req, nil, nil
		} else {
			resp := &InvokeResponse{}
			utils.GetJsonValue(resp, jsonValue)
			return nil, resp, nil
		}
	}
}

// WriteInvoke 写入数据到 io.writer中，写入的数据只可能是 InvokeRequest/InvokeResponse
// 写入的格式为:
// 类型-byte InvokeRequest-1 / InvokeResponse-2
// 数据体字节流长度-Int64
// 数据体字节流 参数r转化为json字符串后取字节流
func WriteInvoke(mutex sync.Mutex, write io.Writer, r any) error {
	defer mutex.Unlock()
	mutex.Lock()

	invokeTypeByte := make([]byte, 1)
	if _, ok := r.(*InvokeRequest); ok {
		invokeTypeByte[0] = 1
	} else if _, ok = r.(*InvokeResponse); ok {
		invokeTypeByte[0] = 2
	} else {
		return errors.New("写入的值必须是* InvokeRequest/InvokeResponse 类型")
	}

	err := utils.WriteBytes(write, invokeTypeByte)
	if err != nil {
		return err
	}

	jsonValue := []byte(utils.GetJsonString(r))
	writeLen := uint32(len(jsonValue))
	writeLenBytes := utils.IntToByteArray(int64(writeLen))
	err = utils.WriteBytes(write, writeLenBytes)
	if err != nil {
		return err
	}
	err = utils.WriteBytes(write, jsonValue)
	if err != nil {
		return err
	}
	return nil
}
