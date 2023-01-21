package quic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/google/uuid"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

// ServerAdapter 服务适配接口
type ServerAdapter interface {
	// ConnectionIn 连接进入时的处理过程
	ConnectionIn(connId string, connType rpc.ConnectionType, invoker *rpc.Invoker)

	// GetInvoker 取得终端ID对应的Invoker，存在跨服务端的情况
	GetInvoker(terminalId string) *rpc.Invoker

	// GetContext 取得终端ID对应的Context，存在跨服务端的情况
	GetContext(terminalId string) context.Context

	// OnNewClientRec 建立新的客户端记录
	OnNewClientRec() *rpc.ClientRec

	OnCloseClient(invoker *rpc.Invoker)
}

type Server struct {
	quicPort     int
	quicPath     string
	leaseSeconds int
	invokeRoute  *rpc.InvokeRoute
	httpMux      *http.ServeMux
	webTransPort *webtransport.Server
	adapter      ServerAdapter
	utils.Closer `json:"-"`
}

func (s *Server) LeaseSeconds() int {
	if s.leaseSeconds == 0 {
		s.leaseSeconds = 30
	}
	return s.leaseSeconds
}

func (s *Server) SetLeaseSeconds(leaseSeconds int) {
	s.leaseSeconds = leaseSeconds
}

func (s *Server) InvokeRoute() *rpc.InvokeRoute {
	return s.invokeRoute
}

func (s *Server) generateTLSConfig() {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyFile, _ := os.Create("server.key")
	_, _ = keyFile.Write(keyPEM)
	_ = keyFile.Close()

	crtFile, _ := os.Create("server.pem")
	_, _ = crtFile.Write(certPEM)
	_ = crtFile.Close()
}

//取得主通道(命令通道)对应的Invoker
//如果主通道(命令通道)在当前节点，则取当前节点Invokers中对应的主通道
//如果主通道(命令通道)不在当前节点，对取转发命令通道，转发命令通道登记的connId为目标节点的ConnId
//暂时只取当前节点的

func (s *Server) upgradeWebTransport(w http.ResponseWriter, r *http.Request) {
	terminalId := r.Header.Get(rpc.HeadKey_Connection_TerminalId)
	connectionType := rpc.ConnectionType(r.Header.Get(rpc.HeadKey_Connection_Type))
	isCmdTrans := connectionType == rpc.ConnectionType_Command
	cmdInvoker := s.adapter.GetInvoker(terminalId)
	remoteAddr := utils.GetRealRemoteAddr(r)
	isNewClient := false
	var clientRec *rpc.ClientRec

	if isCmdTrans && terminalId == "" {
		//terminalId为空，表明当前客户端为新的客户端，需要产生新客户端的tunnelId/terminalId
		clientRec = s.adapter.OnNewClientRec()
		isNewClient = true
		terminalId = clientRec.ClientId
	}

	if !isCmdTrans && cmdInvoker == nil {
		// 如果不是命令通道，必须有依附的命令通道存在
		http.Error(w, rpc.InvalidInvokerConnect.Error(), http.StatusInternalServerError)
		return
	}

	session, err := s.webTransPort.Upgrade(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
	}

	var stream webtransport.Stream
	stream, err = session.AcceptStream(s.Ctx())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
	}
	ctx := s.adapter.GetContext(terminalId)
	//分配connectionId给新的连接
	invokerId := uuid.New().String()
	var invoker = s.invokeRoute.AddAcceptInvoker(invokerId, terminalId, ctx, stream)
	req := rpc.NewInvokeRequest(rpc.InvokePath_Client_SetId)
	req.Header["InvokerId"] = invokerId
	req.Header["TerminalId"] = terminalId

	err = invoker.WriteRequest(req)
	if err != nil {
		_ = invoker.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	invoker.SetIsCommandTunnel(isCmdTrans)
	invoker.SetConnectionType(connectionType)
	invoker.SetRemoteAddr(remoteAddr)
	invoker.SetClientIP(utils.GetRealRemoteAddr(r))
	invoker.SetAttach("Session", session)
	invoker.SetAttach("Conn", NewConnWrapper(invoker.Ctx(), stream, session))
	invoker.SetReadErrorHandler(func(_ error) {
		_ = invoker.Close()
	})
	invoker.SetWriteErrorHandler(func(_ error) {
		_ = invoker.Close()
	})
	invoker.SetOnClose(func() {
		_ = invoker.ReadWriter().Close()
		_ = session.CloseWithError(0, "")
		s.invokeRoute.RemoveInvoker(invokerId)
		if isCmdTrans {
			s.adapter.OnCloseClient(invoker)
			s.adapter.OnCloseClient(invoker)
			log.Println(fmt.Sprintf("已关闭客户端[%v]连接", invokerId))
		}
	})

	if isNewClient && clientRec != nil {
		req := rpc.NewInvokeRequest(rpc.InvokePath_Client_New)
		req.PutValue(clientRec)
		err = invoker.WriteRequest(req)
		if err != nil {
			_ = invoker.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if isCmdTrans {
		log.Println(fmt.Sprintf("客户端[%v]已连接到Quic服务", invoker.TerminalId()))
		s.invokeRoute.SetExpire(invokerId, time.Second*30)
		s.invokeRoute.DispatchInvoke(invoker)
	} else {
		// 非命令通道，为了推动Accept，通道连接后，会发布一个Accept推动字节，通道首个字节为88
		s.adapter.ConnectionIn(invokerId, connectionType, invoker)
	}
}

func (s *Server) OnInvokerHeartbeat(invoker *rpc.Invoker, request *rpc.InvokeRequest) {
	if invoker.IsCommandTunnel() {
		s.invokeRoute.SetExpire(invoker.InvokerId(), time.Second*30)
	}
}

func (s *Server) AddRpcHandler(path string, handler rpc.BidiInvokeHandler) {
	s.invokeRoute.AddRpcHandler(path, handler)
}

func (s *Server) AddUniHandler(path string, handler rpc.UniInvokeHandler) {
	s.invokeRoute.AddUniHandler(path, handler)
}

func (s *Server) RemoveUniHandler(path string) {
	s.invokeRoute.RemoveUniHandler(path)
}

func (s *Server) RemoveRpcHandler(path string) {
	s.invokeRoute.RemoveRpcHandler(path)
}

func NewServer(ctx context.Context, quicPort int, quicPath string, adapter ServerAdapter) *Server {
	re := &Server{adapter: adapter, quicPort: quicPort, quicPath: quicPath}
	re.SetCtx(ctx)
	re.invokeRoute = rpc.NewInvokeRoute(re.Ctx())
	re.invokeRoute.SetLeaseSeconds(re.LeaseSeconds())
	re.generateTLSConfig()

	certs := make([]tls.Certificate, 1)
	certs[0], _ = tls.LoadX509KeyPair("server.pem", "server.key")
	tlsConfig := &tls.Config{
		Certificates: certs,
	}

	re.httpMux = http.NewServeMux()
	re.httpMux.HandleFunc(quicPath, re.upgradeWebTransport)

	//初始化http3服务
	h3 := http3.Server{
		Addr:      fmt.Sprintf("0.0.0.0:%v", quicPort),
		TLSConfig: tlsConfig,
		Handler:   re.httpMux,
	}

	re.webTransPort = &webtransport.Server{
		H3: h3,
		CheckOrigin: func(r *http.Request) bool {
			return strings.HasPrefix(r.URL.RequestURI(), quicPath)
		},
	}

	re.invokeRoute.AddUniHandler(rpc.InvokePath_Client_Heartbeat, re.OnInvokerHeartbeat)

	go func() {
		log.Println(fmt.Sprintf("QUIC[:%v]服务启动,QUIC路径[%v]", quicPort, quicPath))
		_ = re.webTransPort.ListenAndServe()
	}()

	go func() {
		log.Println(fmt.Sprintf("HTTP[:%v]服务启动", quicPort))
		_ = http.ListenAndServe(fmt.Sprintf(":%v", quicPort), re.httpMux)
	}()

	return re
}
