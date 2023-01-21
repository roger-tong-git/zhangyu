package rpc

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
	"log"
	"strings"
	"sync"
	"time"
)

type InvokeData struct {
	RequestId string
	Header    map[string]string
	JsonBody  string
}

func (i *InvokeData) PutValue(value any) {
	i.JsonBody = utils.GetJsonString(value)
}

func (i *InvokeData) GetValue(value any) bool {
	return utils.GetJsonValue(value, i.JsonBody)
}

type InvokeRequest struct {
	InvokeData
	Path string
}

func NewInvokeRequest(path string) *InvokeRequest {
	re := &InvokeRequest{Path: path}
	re.Header = map[string]string{}
	re.RequestId = uuid.New().String()
	return re
}

func (ir *InvokeRequest) PutValue(v any) *InvokeRequest {
	ir.JsonBody = utils.GetJsonString(v)
	return ir
}

func NewInvokeResponse(requestId string, resultCode InvokeResult, message string) *InvokeResponse {
	re := &InvokeResponse{ResultCode: resultCode}
	re.Header = map[string]string{}
	re.RequestId = requestId
	re.ResultMessage = message
	return re
}

func NewSuccessResponse(requestId string, message string) *InvokeResponse {
	return NewInvokeResponse(requestId, InvokeResult_Success, message)
}

func NewErrorResponse(requestId string, message string) *InvokeResponse {
	return NewInvokeResponse(requestId, InvokeResult_Error, message)
}

type InvokeResponse struct {
	InvokeData
	ResultCode    InvokeResult
	ResultMessage string
}

func (ir *InvokeResponse) PutValue(v any) *InvokeResponse {
	ir.JsonBody = utils.GetJsonString(v)
	return ir
}

type Invoker struct {
	remoteAddr        string
	clientIP          string
	invokerId         string
	terminalId        string
	isCommandTunnel   bool
	connectionType    ConnectionType
	writerLock        *sync.Mutex
	readerLock        *sync.Mutex
	attach            map[string]any
	invokeMap         map[string]chan *InvokeResponse
	invokeMapLock     *sync.Mutex
	writeErrorHandler func(err error)
	readErrorHandler  func(err error)
	readWriter        io.ReadWriteCloser
	invokeRoute       *InvokeRoute
	utils.Closer
}

func (i *Invoker) SetReadErrorHandler(readErrorHandler func(err error)) {
	i.readErrorHandler = readErrorHandler
}

func (i *Invoker) ClientIP() string {
	return i.clientIP
}

func (i *Invoker) SetClientIP(clientIP string) {
	i.clientIP = clientIP
}

func (i *Invoker) ConnectionType() ConnectionType {
	return i.connectionType
}

func (i *Invoker) SetConnectionType(connectionType ConnectionType) {
	i.connectionType = connectionType
}

func (i *Invoker) SetIsCommandTunnel(isCommandTunnel bool) {
	i.isCommandTunnel = isCommandTunnel
}

func (i *Invoker) TerminalId() string {
	return i.terminalId
}

func (i *Invoker) SetTerminalId(terminalId string) {
	i.terminalId = terminalId
}

func (i *Invoker) RemoteAddr() string {
	return i.remoteAddr
}

func (i *Invoker) SetRemoteAddr(remoteAddr string) {
	i.remoteAddr = remoteAddr
}

func (i *Invoker) Read(p []byte) (n int, err error) {
	return i.readWriter.Read(p)
}

func (i *Invoker) Write(p []byte) (n int, err error) {
	return i.readWriter.Write(p)
}

func (i *Invoker) Copy(target io.ReadWriteCloser) {
	copyChan := make(chan bool)

	c := func(src, dst io.ReadWriteCloser) {
		_, _ = io.Copy(dst, src)
		copyChan <- true
	}

	go c(i, target)
	go c(target, i)

	<-copyChan
	_ = i.Close()
	_ = target.Close()
}

func (i *Invoker) IsCommandTunnel() bool {
	return i.isCommandTunnel
}

func (i *Invoker) GetAttach(key string) any {
	if v, ok := i.attach[key]; ok {
		return v
	}
	return nil
}

func (i *Invoker) SetAttach(key string, value any) {
	i.attach[key] = value
}

func (i *Invoker) SetWriteErrorHandler(writeErrorHandler func(err error)) {
	i.writeErrorHandler = writeErrorHandler
}

func (i *Invoker) InvokerId() string {
	return i.invokerId
}

func (i *Invoker) ReadWriter() io.ReadWriteCloser {
	return i.readWriter
}

func NewInvoker(ctx context.Context, invokerId string, terminalId string, readWriter io.ReadWriteCloser) *Invoker {
	re := &Invoker{
		invokerId:     invokerId,
		terminalId:    terminalId,
		readWriter:    readWriter,
		writerLock:    &sync.Mutex{},
		readerLock:    &sync.Mutex{},
		invokeMapLock: &sync.Mutex{},
		attach:        map[string]any{},
		invokeMap:     make(map[string]chan *InvokeResponse),
	}
	re.SetCtx(ctx)
	return re
}

func (i *Invoker) WriteRequest(request *InvokeRequest) error {
	return i.writeInvokeData("1", request)
}

func (i *Invoker) WriteResponse(response *InvokeResponse) error {
	return i.writeInvokeData("2", response)
}

func (i *Invoker) ReadInvoke() (*InvokeRequest, *InvokeResponse, error) {
	defer i.readerLock.Unlock()
	i.readerLock.Lock()

	str, err := bufio.NewReader(i.readWriter).ReadString('\n')
	if err != nil {
		if i.readErrorHandler != nil {
			i.readErrorHandler(err)
		}
		return nil, nil, err
	}

	if bytes, deErr := base64.StdEncoding.DecodeString(str); deErr != nil {
		if i.readErrorHandler != nil {
			i.readErrorHandler(deErr)
		}
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
	data := utils.GetJsonString((*v)["data"].(map[string]any))
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

func (i *Invoker) receiveResponse(resp *InvokeResponse) {
	defer i.invokeMapLock.Unlock()
	i.invokeMapLock.Lock()
	if v, ok := i.invokeMap[resp.RequestId]; ok {
		v <- resp
	}
}

func (i *Invoker) getResponseChan(request *InvokeRequest) chan *InvokeResponse {
	defer i.invokeMapLock.Unlock()
	i.invokeMapLock.Lock()
	if v, ok := i.invokeMap[request.RequestId]; ok {
		return v
	} else {
		respChan := make(chan *InvokeResponse)
		i.invokeMap[request.RequestId] = respChan
		return respChan
	}
}

func (i *Invoker) RemoveResponseChan(requestId string) {
	defer i.invokeMapLock.Unlock()
	i.invokeMapLock.Lock()
	delete(i.invokeMap, requestId)
}

func (i *Invoker) Invoke(request *InvokeRequest) (*InvokeResponse, error) {
	var respChan = i.getResponseChan(request)
	defer i.RemoveResponseChan(request.RequestId)

	err := i.WriteRequest(request)
	if err != nil {
		return nil, err
	}

	var resp *InvokeResponse
	select {
	case <-i.Ctx().Done():
		break
	case resp = <-respChan:
		break
	}

	if resp == nil {
		return nil, errors.New("invoker context done")
	}

	return resp, nil
}

func (i *Invoker) writeInvokeData(invType string, invokeData any) error {
	writeMap := map[string]any{}
	writeMap["type"] = invType
	writeMap["data"] = invokeData
	writeBytes := utils.GetJsonBytes(writeMap)
	sWriter := bufio.NewWriter(i.readWriter)
	enStr := base64.StdEncoding.EncodeToString(writeBytes)

	defer i.writerLock.Unlock()
	i.writerLock.Lock()

	_, err := sWriter.WriteString(enStr + "\n")
	if err != nil {
		if i.writeErrorHandler != nil {
			i.writeErrorHandler(err)
		}
		log.Println("Write Error:", writeMap)
		return err
	}

	err = sWriter.Flush()
	if err != nil {
		if i.writeErrorHandler != nil {
			i.writeErrorHandler(err)
		}
	}
	return err
}

func (i *Invoker) InvokeRoute() *InvokeRoute {
	return i.invokeRoute
}

type BidiInvokeHandler func(invoker *Invoker, request *InvokeRequest) *InvokeResponse
type UniInvokeHandler func(invoker *Invoker, request *InvokeRequest)

type InvokeRoute struct {
	bidiHandlers   map[string]BidiInvokeHandler
	uniHandlers    map[string]UniInvokeHandler
	invokes        *utils.Cache[*Invoker]
	defaultInvoker *Invoker
	handlerLock    *sync.Mutex
	leaseSeconds   int
	utils.Closer
}

func (r *InvokeRoute) LeaseSeconds() int {
	return r.leaseSeconds
}

func (r *InvokeRoute) SetLeaseSeconds(leaseSeconds int) {
	r.leaseSeconds = leaseSeconds
}

func NewInvokeRoute(ctx context.Context) *InvokeRoute {
	re := &InvokeRoute{
		bidiHandlers: make(map[string]BidiInvokeHandler),
		uniHandlers:  make(map[string]UniInvokeHandler),
		handlerLock:  &sync.Mutex{},
	}
	re.invokes = utils.NewCache[*Invoker](re.Ctx())
	re.invokes.SetExpireHandler(func(key string, value any) {
		if v, ok := value.(*Invoker); ok {
			_ = v.Close()
		}
	})
	re.SetCtx(ctx)
	return re
}

func (r *InvokeRoute) getLeaseDuration() time.Duration {
	if r.leaseSeconds == 0 {
		r.leaseSeconds = 30
	}

	return time.Duration(r.leaseSeconds) * time.Second
}

func (r *InvokeRoute) AddAcceptInvoker(invokerId string, terminalId string, ctx context.Context, readWriter io.ReadWriteCloser) *Invoker {
	invoker := NewInvoker(ctx, invokerId, terminalId, readWriter)
	r.invokes.Set(invokerId, invoker)
	_, _ = bufio.NewReader(invoker.readWriter).ReadByte()
	return invoker
}

func (r *InvokeRoute) AddDialInvoker(ctx context.Context, readWriter io.ReadWriteCloser) *Invoker {
	invoker := NewInvoker(ctx, "", "", readWriter)
	w := bufio.NewWriter(readWriter)
	_ = w.WriteByte(88)
	_ = w.Flush()
	req, _, _ := invoker.ReadInvoke()
	if req != nil {
		invoker.invokerId = req.Header["InvokerId"]
		invoker.terminalId = req.Header["TerminalId"]
	}
	r.invokes.Set(invoker.invokerId, invoker)
	return invoker
}

func (r *InvokeRoute) DefaultInvoker() *Invoker {
	return r.defaultInvoker
}

func (r *InvokeRoute) SetDefaultInvoker(defaultInvoker *Invoker) {
	r.defaultInvoker = defaultInvoker
}

func (r *InvokeRoute) AddRpcHandler(path string, handler BidiInvokeHandler) {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	r.bidiHandlers[strings.ToLower(strings.TrimSpace(path))] = handler
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
	delete(r.bidiHandlers, strings.ToLower(strings.TrimSpace(path)))
}

func (r *InvokeRoute) GetBidiHandler(path string) BidiInvokeHandler {
	defer r.handlerLock.Unlock()
	r.handlerLock.Lock()
	return r.bidiHandlers[strings.ToLower(strings.TrimSpace(path))]
}

func (r *InvokeRoute) HasInvoke(invokerId string) bool {
	return r.invokes.HasKey(invokerId)
}

func (r *InvokeRoute) SetExpire(invokerId string, duration time.Duration) {
	r.invokes.SetExpire(invokerId, duration)
}

func (r *InvokeRoute) GetInvoker(invokerId string) *Invoker {
	return r.invokes.Get(invokerId)
}

func (r *InvokeRoute) RemoveInvoker(invokerId string) {
	r.invokes.Delete(invokerId)
}

func (r *InvokeRoute) DispatchInvoke(invoker *Invoker) {
	for {
		if !r.HasInvoke(invoker.invokerId) {
			break
		}
		req, resp, err := invoker.ReadInvoke()
		if err != nil {
			_ = invoker.Close()
			r.RemoveInvoker(invoker.invokerId)
			return
		} else {
			if resp != nil {
				go invoker.receiveResponse(resp)
				continue
			}

			if req != nil {
				if invoker.IsCommandTunnel() {
					r.SetExpire(invoker.invokerId, r.getLeaseDuration())
				}
				uniHandler := r.GetUniHandler(req.Path)
				if uniHandler != nil {
					go uniHandler(invoker, req)
					continue
				}

				rpcHandler := r.GetBidiHandler(req.Path)
				if rpcHandler != nil {

					go func() {
						var callResp *InvokeResponse
						callResp = rpcHandler(invoker, req)
						if callErr := invoker.WriteResponse(callResp); callErr != nil {
							invoker.CtxCancel()
							go r.RemoveInvoker(invoker.invokerId)
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
	}
}
