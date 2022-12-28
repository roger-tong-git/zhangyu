package network

import (
	"context"
	"errors"
	"fmt"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
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
	FromType  InvokeTerminal
	FromId    string
	ToType    InvokeTerminal
	ToId      string
	Path      string
	Header    map[string]string
	BodyJson  string
}

func NewInvokeRequest(requestId string, path string) *InvokeRequest {
	return &InvokeRequest{RequestId: requestId, Path: path}
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

type InvokeHandler func(request *InvokeRequest) *InvokeResponse

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
	connId        string
	reader        io.Reader
	writer        io.Writer
	writerLock    sync.Mutex
	readerLock    sync.Mutex
	route         *InvokeRoute
	invokeMap     map[string]chan *InvokeResponse
	invokeMapLock sync.Mutex
	utils.Closer
}

func (s *Invoker) readBytes(readLen int) (*[]byte, error) {
	b := make([]byte, readLen)
	r := make([]byte, 0)
	totalRead := 0
	for {
		if readSize, err := s.reader.Read(b); err != nil {
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
		if l, err := s.writer.Write(b[totalWrite:]); err != nil {
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

func (s *Invoker) receiveResponse(resp *InvokeResponse) {
	defer s.invokeMapLock.Unlock()
	s.invokeMapLock.Lock()
	if v, ok := s.invokeMap[resp.RequestId]; ok {
		v <- resp
	}
}

// readInvoke 从io.reader中读取数据，数据只可能是InvokeRequest/InvokeResponse/error
func (s *Invoker) readInvoke() (*InvokeRequest, *InvokeResponse, error) {
	defer s.readerLock.Unlock()
	s.readerLock.Lock()

	readTypeBytes, err := s.readBytes(1)
	if err != nil {
		return nil, nil, err
	}

	readType := (*readTypeBytes)[0]
	if readType != byte(1) && readType != byte(2) {
		return nil, nil, errors.New(fmt.Sprintf("读取到的invoke类型无效：%v", readType))
	}

	readSizeBytes, err := s.readBytes(8)
	if err != nil {
		return nil, nil, err
	}
	var readBytes *[]byte
	readSize := utils.ByteArrayToInt(*readSizeBytes)
	if readBytes, err = s.readBytes(int(readSize)); err != nil {
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

// writeInvoke 写入数据到 io.writer中，写入的数据只可能是 InvokeRequest/InvokeResponse
// 写入的格式为:
// 类型-byte InvokeRequest-1 / InvokeResponse-2
// 数据体字节流长度-Int64
// 数据体字节流 参数r转化为json字符串后取字节流
func (s *Invoker) writeInvoke(r any) error {
	defer s.writerLock.Unlock()
	s.writerLock.Lock()

	invokeTypeByte := make([]byte, 1)
	if _, ok := r.(*InvokeRequest); ok {
		invokeTypeByte[0] = 1
	} else if _, ok = r.(*InvokeResponse); ok {
		invokeTypeByte[0] = 2
	} else {
		return errors.New("写入的值必须是* InvokeRequest/InvokeResponse 类型")
	}

	err := s.writeBytes(invokeTypeByte)
	if err != nil {
		return err
	}

	jsonValue := []byte(utils.GetJsonString(r))
	writeLen := uint32(len(jsonValue))
	writeLenBytes := utils.IntToByteArray(int64(writeLen))
	err = s.writeBytes(writeLenBytes)
	if err != nil {
		return err
	}
	err = s.writeBytes(jsonValue)
	if err != nil {
		return err
	}
	return nil
}

func (s *Invoker) Invoke(r *InvokeRequest) (*InvokeResponse, error) {
	respChan := make(chan *InvokeResponse, 1)
	s.invokeMap[r.RequestId] = respChan
	if err := s.writeInvoke(r); err != nil {
		s.CtxCancel()
		return nil, err
	}
	select {
	case <-s.Ctx().Done():
		return nil, WebTransportConnectError
	case re := <-respChan:
		delete(s.invokeMap, r.RequestId)
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

func (r *InvokeRoute) dispatchInvoke(invoker *Invoker) {
	for {
		req, resp, err := invoker.readInvoke()
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
						callResp = handler(req)
					} else {
						str := fmt.Sprintf("无效的路由：%v", req.Path)
						callResp = NewInvokeResponse(req.RequestId, InvokeResult_Error, str)
					}

					if callErr := invoker.writeInvoke(callResp); callErr != nil {
						invoker.CtxCancel()
						r.RemoveInvoker(invoker.connId)
						log.Println("writeInvoke Error", callErr)
						return
					}
				}()
			} else if resp != nil {
				invoker.receiveResponse(resp)
			}
		}
	}
}

func (r *InvokeRoute) AddInvoker(connId string, reader io.Reader, writer io.Writer) *Invoker {
	defer r.invokerLock.Unlock()
	r.invokerLock.Lock()
	invoker := &Invoker{connId: connId, reader: reader, writer: writer, invokeMap: map[string]chan *InvokeResponse{}}
	invoker.SetCtx(r.Ctx())
	invoker.SetOnClose(func() {
		go r.RemoveInvoker(connId)
	})
	r.invokes[connId] = invoker
	go r.dispatchInvoke(invoker)

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
