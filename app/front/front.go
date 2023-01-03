package front

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/google/uuid"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/roger-tong-git/zhangyu/app"
	"github.com/roger-tong-git/zhangyu/network"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Front struct {
	NodeAddr     string
	TerminalId   string `json:"-"`
	ConnectionId string `json:"-"`
	ServerPort   int
	client       *network.TransportClient
	transfers    map[string]*app.TransferSession
	transferLock sync.Mutex
	httpCli      *http.Client
	utils.Closer `json:"-"`
}

func (f *Front) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	uri, _ := url.Parse(f.NodeAddr)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Body = io.NopCloser(bytes.NewReader(body))
	fullUrl := fmt.Sprintf("%s://%s%s", uri.Scheme, uri.Host, req.RequestURI)
	proxyReq, _ := http.NewRequest(req.Method, fullUrl, bytes.NewReader(body))
	proxyReq.Host = req.Host
	proxyReq.Header = make(http.Header)
	for h, val := range req.Header {
		proxyReq.Header[h] = val
	}
	resp, respErr := f.httpCli.Do(proxyReq)

	if respErr != nil {
		utils.WriteResponse(w, respErr.Error())
	} else {
		for k, v := range resp.Header {
			w.Header().Set(k, strings.Join(v, ";"))
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func NewFront(ctx context.Context) *Front {
	f := &Front{}
	f.SetCtx(ctx)
	f.transfers = map[string]*app.TransferSession{}
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
