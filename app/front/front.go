package front

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/google/uuid"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/roger-tong-git/zhangyu/network"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ConnRoundTripper struct {
	conn      net.Conn
	transport *http.Transport
}

func (r *ConnRoundTripper) Transport() *http.Transport {
	return r.transport
}

func NewConnRoundTripper(invoker net.Conn) *ConnRoundTripper {
	re := &ConnRoundTripper{conn: invoker}
	re.transport = re.createRoundTripper()
	return re
}

func (r *ConnRoundTripper) createRoundTripper() *http.Transport {
	re := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	re.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return r.conn, nil
	}
	return re
}

type Front struct {
	NodeAddr     string
	TerminalId   string `json:"-"`
	ConnectionId string `json:"-"`
	ServerPort   int
	client       *network.TransportClient
	httpCli      *http.Client
	transports   *utils.Cache[*ConnRoundTripper]
	utils.Closer `json:"-"`
}

func (f *Front) dumpRequest(req *http.Request) (*http.Request, error) {
	uri, _ := url.Parse(f.NodeAddr)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	req.Body = io.NopCloser(bytes.NewReader(body))
	fullUrl := fmt.Sprintf("%s://%s%s", uri.Scheme, uri.Host, req.RequestURI)
	proxyReq, _ := http.NewRequest(req.Method, fullUrl, bytes.NewReader(body))
	proxyReq.Host = req.Host
	proxyReq.Header = make(http.Header)
	for h, val := range req.Header {
		proxyReq.Header[h] = val
	}
	return proxyReq, err
}

func (f *Front) addInvoker(connId string, conn net.Conn) *ConnRoundTripper {
	re := NewConnRoundTripper(conn)
	f.transports.Set(connId, re)
	f.transports.SetOnClose(func() {
		go f.transports.Delete(connId)
	})
	return re
}

func (f *Front) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("X-Client-IP") == "" {
		req.Header.Set("X-Client-IP", req.RemoteAddr)
	}
	host := strings.Split(strings.ToLower(strings.TrimSpace(req.Host)), ":")[0]
	invReq := network.NewInvokeRequest(uuid.New().String(), network.InvokePath_Domain_Info)
	invReq.Header["Domain"] = host
	invResp := f.client.Invoke(invReq)
	if invResp.ResultCode == network.InvokeResult_Success {
		tq := &network.TransferRequest{}
		utils.GetJsonValue(tq, invResp.BodyJson)
		connId := fmt.Sprintf("%v|%v", host, req.RemoteAddr)
		header := http.Header{}
		header.Set(network.HeadKey_ConnectionId, connId)
		header.Set(network.HeadKey_ConnectionType, network.Connection_Instance_From)
		header.Set(network.HeadKey_Domain, host)

		transfer := f.transports.Get(connId)
		if transfer == nil {
			conn, err := net.Dial("tcp", "127.0.0.1:18889")
			if err != nil {
				f.transports.Delete(connId)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			invReq := network.NewInvokeRequest(uuid.New().String(), "")
			invReq.BodyJson = utils.GetJsonString(tq)
			invReq.Header[network.HeadKey_ConnectionId] = connId
			connWrapper := network.NewConnWrapper(f.Ctx(), connId, conn)
			err = network.WriteInvoke(connWrapper.WriterLock(), connWrapper, invReq)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				f.transports.Delete(connId)
				return
			}

			_, _, respErr := network.ReadInvoke(connWrapper.ReaderLock(), connWrapper)
			if respErr != nil {
				f.transports.Delete(connId)
				http.Error(w, respErr.Error(), http.StatusInternalServerError)
				return
			}

			transfer = f.addInvoker(connId, conn)
		}

		trans := transfer.transport
		proxyReq, reqErr := f.dumpRequest(req)
		if reqErr != nil {
			http.Error(w, reqErr.Error(), http.StatusInternalServerError)
			return
		}
		if resp, respErr := trans.RoundTrip(proxyReq); respErr != nil {
			http.Error(w, respErr.Error(), http.StatusInternalServerError)
		} else {
			f.writeResp(w, resp)
			return
		}
		return
	}

	proxyReq, reqErr := f.dumpRequest(req)
	if reqErr != nil {
		http.Error(w, reqErr.Error(), http.StatusInternalServerError)
		return
	}

	resp, respErr := f.httpCli.Do(proxyReq)

	if respErr != nil {
		http.Error(w, respErr.Error(), http.StatusInternalServerError)
	} else {
		f.writeResp(w, resp)
	}
}

func (f *Front) writeResp(w http.ResponseWriter, resp *http.Response) {
	for k, v := range resp.Header {
		w.Header().Set(k, strings.Join(v, ";"))
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func NewFront(ctx context.Context) *Front {
	f := &Front{}
	f.SetCtx(ctx)
	utils.ReadJsonSetting("front.json", f, func() {
		f.ServerPort = 18888
		f.NodeAddr = "https://127.0.0.1:18888" + network.WebSocket_ServicePath
	})

	f.TerminalId = uuid.New().String()
	f.ConnectionId = uuid.New().String()

	cliInfo := &network.ClientConnInfo{}
	cliInfo.TerminalId = f.TerminalId
	cliInfo.Type = network.TerminalType_Front
	cliInfo.ConnectionId = f.ConnectionId
	f.transports = utils.NewCache[*ConnRoundTripper](f.Ctx())

	f.client = network.NewTransferClient(f.Ctx(), cliInfo)

	f.client.ConnectTo(f.NodeAddr, func() {
		log.Println(fmt.Sprintf("Api服务[%v]注册成功", f.TerminalId))
	})

	f.httpCli = &http.Client{
		Transport: &http3.RoundTripper{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	httpSrv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%v", f.ServerPort),
		Handler: f,
	}

	go func() {
		_ = httpSrv.ListenAndServe()
	}()
	return f
}

func (f *Front) Close() {
	f.CtxCancel()
	time.Sleep(time.Millisecond * 200)
}
