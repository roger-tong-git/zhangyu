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
	"time"
)

type InvokeTerminal int
type InvokeResultCode int

const (
	InvokeResult_Success = InvokeResultCode(200)
	InvokeResult_Error   = InvokeResultCode(400)
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

type RpcInvokeHandler func(invoker *Invoker, request *InvokeRequest) *InvokeResponse
type UniInvokeHandler func(invoker *Invoker, request *InvokeRequest)

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
	sessionCancel func()
	transportConn *WebtransportConn
	writerLock    *sync.Mutex
	readerLock    *sync.Mutex
	route         *InvokeRoute
	invokeMap     map[string]chan *InvokeResponse
	invokeMapLock sync.Mutex
	utils.Closer
}

func (s *Invoker) WriterLock() *sync.Mutex {
	if s.writerLock == nil {
		s.writerLock = &sync.Mutex{}
	}
	return s.writerLock
}

func (s *Invoker) ReaderLock() *sync.Mutex {
	if s.readerLock == nil {
		s.readerLock = &sync.Mutex{}
	}
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
	return ReadInvoke(s.ReaderLock(), s)
}

// WriteInvoke 写入数据到 io.writer中，写入的数据只可能是 InvokeRequest/InvokeResponse
// 写入的格式为:
// 类型-byte InvokeRequest-1 / InvokeResponse-2
// 数据体字节流长度-Int64
// 数据体字节流 参数r转化为json字符串后取字节流
func (s *Invoker) WriteInvoke(r any) error {
	return WriteInvoke(s.WriterLock(), s, r)
}

func (s *Invoker) Invoke(r *InvokeRequest) (*InvokeResponse, error) {
	defer func() {
		go s.deleteInvokeMap(r.RequestId)
	}()

	respChan := make(chan *InvokeResponse)
	s.setInvokeMap(r.RequestId, respChan)
	if err := s.WriteInvoke(r); err != nil {
		s.CtxCancel()
		return nil, err
	}

	select {
	case <-time.After(time.Second * 5):
		return nil, errors.New(fmt.Sprintf("Invoke[%v]TimeOut:%v", s.connId, r.Path))
	case re := <-respChan:
		return re, nil
	}
}

type InvokeRoute struct {
	rpcHandlers    map[string]RpcInvokeHandler
	uniHandlers    map[string]UniInvokeHandler
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
	re := &InvokeRoute{rpcHandlers: map[string]RpcInvokeHandler{}, uniHandlers: map[string]UniInvokeHandler{}, invokes: map[string]*Invoker{}}
	re.SetCtx(ctx)
	return re
}

func (r *InvokeRoute) AddRpcHandler(path string, handler RpcInvokeHandler) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	r.rpcHandlers[strings.ToLower(strings.TrimSpace(path))] = handler
}

func (r *InvokeRoute) AddUniHandler(path string, handler UniInvokeHandler) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	r.uniHandlers[strings.ToLower(strings.TrimSpace(path))] = handler
}

func (r *InvokeRoute) RemoveUniHandler(path string) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	delete(r.uniHandlers, strings.ToLower(strings.TrimSpace(path)))
}

func (r *InvokeRoute) GetUniHandler(path string) UniInvokeHandler {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	return r.uniHandlers[strings.ToLower(strings.TrimSpace(path))]
}

func (r *InvokeRoute) RemoveRpcHandler(path string) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	delete(r.rpcHandlers, strings.ToLower(strings.TrimSpace(path)))
}

func (r *InvokeRoute) GetRpcHandler(path string) RpcInvokeHandler {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	return r.rpcHandlers[strings.ToLower(strings.TrimSpace(path))]
}

func (r *InvokeRoute) HasInvoke(connId string) bool {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	_, ok := r.invokes[connId]
	return ok
}

func (r *InvokeRoute) DispatchInvoke(invoker *Invoker, HeartbeatHandler func(*InvokeRequest)) {
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
			if resp != nil {
				go invoker.receiveResponse(resp)
				continue
			}

			if req != nil {
				uniHandler := r.GetUniHandler(req.Path)
				if uniHandler != nil {
					go uniHandler(invoker, req)
					continue
				}

				rpcHandler := r.GetRpcHandler(req.Path)
				if rpcHandler != nil {
					go func() {
						var callResp *InvokeResponse
						callResp = rpcHandler(invoker, req)
						if callErr := invoker.WriteInvoke(callResp); callErr != nil {
							invoker.CtxCancel()
							go r.RemoveInvoker(invoker.connId)
							return
						}
					}()
					continue
				}
				if req.Path != "" {
					log.Println(fmt.Sprintf("找不到[%v]对应的处理过程", req.Path))
				}
				continue
			}
		}
		time.Sleep(time.Millisecond * 100)
	}
}

func (r *InvokeRoute) AddInvoker(connId string, sessionCancel func(), session *webtransport.Session, stream webtransport.Stream) *Invoker {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	invoker := &Invoker{
		connIP:        session.RemoteAddr().String(),
		sessionCancel: sessionCancel,
		connId:        connId,
		transportConn: NewWebtransportConn(session, stream),
		invokeMap:     map[string]chan *InvokeResponse{}}
	invoker.SetCtx(r.Ctx())
	invoker.SetOnClose(func() {
		sessionCancel()
		_ = session.CloseWithError(0, "")
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
