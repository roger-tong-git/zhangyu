package network

import (
	"bufio"
	"context"
	"encoding/base64"
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
	WriterLock() *sync.Mutex
	ReaderLock() *sync.Mutex
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

func (t *TransferStream) CloseStream() {
	if s := t.SourceStream(); s != nil {
		_ = s.Close()
	}
	if d := t.TargetStream(); d != nil {
		_ = d.Close()
	}
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

	errChan := make(chan error, 2)

	go func() {
		_, err := io.Copy(t.sourceStream, t.targetStream)
		errChan <- err
	}()

	go func() {
		_, err := io.Copy(t.targetStream, t.sourceStream)
		errChan <- err
	}()

	_ = <-errChan
	t.CloseStream()
}

// ReadInvoke 从io.reader中读取数据，数据只可能是InvokeRequest/InvokeResponse/error
func ReadInvoke(mutex *sync.Mutex, reader io.Reader) (*InvokeRequest, *InvokeResponse, error) {
	defer mutex.Unlock()
	mutex.Lock()

	str, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		return nil, nil, err
	}

	if bytes, deErr := base64.StdEncoding.DecodeString(str); deErr != nil {
		log.Println("base64解码失败")
		return nil, nil, deErr
	} else {
		str = string(bytes)
	}

	v := &map[string]any{}
	utils.GetJsonValue(v, str)
	invType := (*v)["type"].(string)
	if invType != "1" && invType != "2" {
		return nil, nil, errors.New(fmt.Sprintf("读取到的invoke类型无效：%v", invType))
	}
	data := (*v)["data"].(string)
	if invType == "1" {
		invReq := &InvokeRequest{}
		utils.GetJsonValue(invReq, data)
		return invReq, nil, nil
	} else {
		invResp := &InvokeResponse{}
		utils.GetJsonValue(invResp, data)
		return nil, invResp, nil
	}
}

// WriteInvoke 写入数据到 io.writer中，写入的数据只可能是 InvokeRequest/InvokeResponse
/* 写入的方式为:
	1. 建立一个字典
       data : 1-InvokeRequest, 2-InvokeResponse


*/
func WriteInvoke(mutex *sync.Mutex, write io.Writer, r any) error {
	defer mutex.Unlock()
	mutex.Lock()

	v := make(map[string]any)
	invType := "1"
	if _, ok := r.(*InvokeRequest); ok {
		invType = "1"
	} else if _, ok = r.(*InvokeResponse); ok {
		invType = "2"
	} else {
		return errors.New("写入的值必须是* InvokeRequest/InvokeResponse 类型")
	}
	v["type"] = invType
	v["data"] = utils.GetJsonString(r)
	writeBytes := utils.GetJsonBytes(v)
	sWriter := bufio.NewWriter(write)
	enStr := base64.StdEncoding.EncodeToString(writeBytes)
	_, err := sWriter.WriteString(enStr + "\n")
	if err != nil {
		return err
	}
	err = sWriter.Flush()
	return err
}
