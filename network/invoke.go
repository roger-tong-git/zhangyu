package network

import (
	"context"
	"errors"
	"fmt"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"strings"
	"sync"
)

type InvokeTerminal int
type InvokeResultCode int

const (
	InvokeTerminal_Node     = InvokeTerminal(1)
	InvokeTerminal_Client   = InvokeTerminal(2)
	InvokeTerminal_Instance = InvokeTerminal(3)
	InvokeResult_Success    = InvokeResultCode(200)
	InvokeResult_Error      = InvokeResultCode(400)
	InvokeResult_Warn       = InvokeResultCode(300)
)

var WebTransportConnectError = errors.New("到服务端的连接断开")

type InvokeRequest struct {
	RequestId string
	Path      string
	Header    map[string]string
	BodyJson  string
}

func NewInvokeRequest(requestId string, path string) *InvokeRequest {
	return &InvokeRequest{RequestId: requestId, Path: path, Header: map[string]string{}}
}

func NewSuccessResponse(r *InvokeRequest, v any) *InvokeResponse {
	return &InvokeResponse{
		RequestId:     r.RequestId,
		ResultCode:    InvokeResult_Success,
		ResultMessage: "",
		Header:        nil,
		BodyJson:      utils.GetJsonString(v),
	}
}

func NewErrorResponse(r *InvokeRequest, message string) *InvokeResponse {
	return &InvokeResponse{
		RequestId:     r.RequestId,
		ResultCode:    InvokeResult_Error,
		ResultMessage: message,
	}
}

type InvokeHandler func(invoker *Invoker, request *InvokeRequest) *InvokeResponse

type InvokeResponse struct {
	RequestId     string
	ResultCode    InvokeResultCode
	ResultMessage string
	Header        map[string]string
	BodyJson      string
}

func NewInvokeResponse(requestId string, resultCode InvokeResultCode, resultMessage string) *InvokeResponse {
	return &InvokeResponse{RequestId: requestId, ResultCode: resultCode, ResultMessage: resultMessage, Header: map[string]string{}}
}

type Invoker struct {
	connIP        string
	connId        string
	transportConn *WebtransportConn
	writerLock    sync.Mutex
	readerLock    sync.Mutex
	route         *InvokeRoute
	invokeMap     map[string]chan *InvokeResponse
	invokeMapLock sync.Mutex
	utils.Closer
}

func (s *Invoker) WriterLock() sync.Mutex {
	return s.writerLock
}

func (s *Invoker) ReaderLock() sync.Mutex {
	return s.readerLock
}

func (s *Invoker) TransportConn() *WebtransportConn {
	return s.transportConn
}

func (s *Invoker) ConnId() string {
	return s.connId
}

func (s *Invoker) Read(p []byte) (int, error) {
	return s.transportConn.Read(p)
}

func (s *Invoker) Write(p []byte) (int, error) {
	return s.transportConn.Write(p)
}

func (s *Invoker) ConnIP() string {
	return s.connIP
}

func (s *Invoker) readBytes(readLen int) (*[]byte, error) {
	b := make([]byte, readLen)
	r := make([]byte, 0)
	totalRead := 0
	for {
		if readSize, err := s.transportConn.Read(b); err != nil {
			return nil, err
		} else {
			r = append(r, b[:readSize]...)
			totalRead += readSize
			if totalRead >= readLen {
				break
			}
		}
	}
	return &r, nil
}

func (s *Invoker) writeBytes(b []byte) error {
	totalWrite := 0
	writeLen := len(b)
	for {
		if l, err := s.transportConn.Write(b[totalWrite:]); err != nil {
			return err
		} else {
			totalWrite += l
			if totalWrite >= writeLen {
				break
			}
		}
	}
	return nil
}

func (s *Invoker) setInvokeMap(key string, v chan *InvokeResponse) {
	defer s.invokeMapLock.Unlock()
	s.invokeMapLock.Lock()
	s.invokeMap[key] = v
}

func (s *Invoker) getInvokeMap(key string) chan *InvokeResponse {
	defer s.invokeMapLock.Unlock()
	s.invokeMapLock.Lock()
	return s.invokeMap[key]
}

func (s *Invoker) deleteInvokeMap(key string) {
	defer s.invokeMapLock.Unlock()
	s.invokeMapLock.Lock()
	delete(s.invokeMap, key)
}

func (s *Invoker) receiveResponse(resp *InvokeResponse) {
	defer s.invokeMapLock.Unlock()
	s.invokeMapLock.Lock()
	if v, ok := s.invokeMap[resp.RequestId]; ok {
		v <- resp
	}
}

// ReadInvoke 从io.reader中读取数据，数据只可能是InvokeRequest/InvokeResponse/error
func (s *Invoker) ReadInvoke() (*InvokeRequest, *InvokeResponse, error) {
	return ReadInvoke(s.readerLock, s)
}

// WriteInvoke 写入数据到 io.writer中，写入的数据只可能是 InvokeRequest/InvokeResponse
// 写入的格式为:
// 类型-byte InvokeRequest-1 / InvokeResponse-2
// 数据体字节流长度-Int64
// 数据体字节流 参数r转化为json字符串后取字节流
func (s *Invoker) WriteInvoke(r any) error {
	return WriteInvoke(s.writerLock, s, r)
}

func (s *Invoker) Invoke(r *InvokeRequest) (*InvokeResponse, error) {
	respChan := make(chan *InvokeResponse, 1)
	s.setInvokeMap(r.RequestId, respChan)
	if err := s.WriteInvoke(r); err != nil {
		s.CtxCancel()
		return nil, err
	}
	select {
	case <-s.Ctx().Done():
		return nil, WebTransportConnectError
	case re := <-respChan:
		s.deleteInvokeMap(r.RequestId)
		return re, nil
	}
}

type InvokeRoute struct {
	handlers       map[string]InvokeHandler
	invokes        map[string]*Invoker
	defaultInvoker *Invoker
	handlerLock    sync.Mutex
	invokerLock    sync.Mutex
	utils.Closer
}

func (r *InvokeRoute) DefaultInvoker() *Invoker {
	return r.defaultInvoker
}

func (r *InvokeRoute) SetDefaultInvoker(defaultInvoker *Invoker) {
	r.defaultInvoker = defaultInvoker
}

func NewInvokeRoute(ctx context.Context) *InvokeRoute {
	re := &InvokeRoute{handlers: map[string]InvokeHandler{}, invokes: map[string]*Invoker{}}
	re.SetCtx(ctx)
	return re
}

func (r *InvokeRoute) AddHandler(path string, handler InvokeHandler) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	r.handlers[strings.ToLower(strings.TrimSpace(path))] = handler
}

func (r *InvokeRoute) RemoveHandler(path string) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	delete(r.handlers, strings.ToLower(strings.TrimSpace(path)))
}

func (r *InvokeRoute) GetHandler(path string) InvokeHandler {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	return r.handlers[strings.ToLower(strings.TrimSpace(path))]
}

func (r *InvokeRoute) HasInvoke(connId string) bool {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	_, ok := r.invokes[connId]
	return ok
}

func (r *InvokeRoute) DispatchInvoke(invoker *Invoker) {
	for {
		if !r.HasInvoke(invoker.connId) {
			break
		}
		req, resp, err := invoker.ReadInvoke()
		if err != nil {
			invoker.CtxCancel()
			r.RemoveInvoker(invoker.connId)
			return
		} else {
			if req != nil {
				go func() {
					handler := r.GetHandler(strings.TrimSpace(strings.ToLower(req.Path)))
					var callResp *InvokeResponse
					if handler != nil {
						callResp = handler(invoker, req)
					} else {
						str := fmt.Sprintf("无效的路由：%v", req.Path)
						callResp = NewInvokeResponse(req.RequestId, InvokeResult_Error, str)
					}

					if callErr := invoker.WriteInvoke(callResp); callErr != nil {
						invoker.CtxCancel()
						r.RemoveInvoker(invoker.connId)
						log.Println("WriteInvoke Error", callErr)
						return
					}
				}()
			} else if resp != nil {
				invoker.receiveResponse(resp)
			}
		}
	}
}

func (r *InvokeRoute) AddInvoker(connId string, session *webtransport.Session, stream webtransport.Stream) *Invoker {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	invoker := &Invoker{
		connIP:        session.RemoteAddr().String(),
		connId:        connId,
		transportConn: NewWebtransportConn(session, stream),
		invokeMap:     map[string]chan *InvokeResponse{}}
	invoker.SetCtx(r.Ctx())
	invoker.SetOnClose(func() {
		if invoker.transportConn != nil {
			_ = invoker.transportConn.Close()
		}
		go r.RemoveInvoker(connId)
	})
	r.invokes[connId] = invoker
	return invoker
}

func (r *InvokeRoute) GetInvoker(connId string) *Invoker {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	return r.invokes[connId]
}

func (r *InvokeRoute) RemoveInvoker(connId string) {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	delete(r.invokes, connId)
}
