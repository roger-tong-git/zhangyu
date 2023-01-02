package node

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/marten-seemann/webtransport-go"
	"github.com/roger-tong-git/zhangyu/network"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	"math/big"
	rand2 "math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Node struct {
	TerminalId    string //设备ID
	EtcdUri       string //Etcd连接字符串
	ServerPort    int
	ClientTimeOut int
	connectionId  string
	invokeRoute   *network.InvokeRoute
	etcdOp        *EtcdOp
	h3Server      *http3.Server
	clients       *utils.Cache[*ClientSession]
	transfers     *utils.Cache[*TransferSession]
	quicMux       *echo.Echo
	httpMux       *echo.Echo
	clientIdMaps  sync.Map //维护一份简单的列表，记得客户端TerminalId与客户端ConnectionId的对应关系，
	//如果ConnectionId发生改变，则说明客户端有新的进程登录或是在别处登录，需要踢掉之前的客户端
	transportServer *webtransport.Server
	utils.Closer
}

func (s *Node) OnClose() {

}

func (s *Node) getTransfer(connId string) *TransferSession {
	return s.transfers.Get(connId)
}

func (s *Node) setTransfer(connId string, instance *TransferSession) {
	s.transfers.Set(connId, instance)
}

func (s *Node) deleteTransfer(connId string) {
	s.transfers.Delete(connId)
}

func (s *Node) nodeWatcher(eventType string, key string, value []byte) {
	str := fmt.Sprintf("[%v]%v:%v", eventType, key, string(value))
	log.Println(str)
}

func (s *Node) Close() {
	if s.etcdOp != nil {
		_, _ = s.etcdOp.Delete(s.etcdOp.GetNodeKey(s.TerminalId))
	}
	s.CtxCancel()
	time.Sleep(time.Millisecond * 200)
}

func (s *Node) generateTLSConfig() {
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

	keyFile, _ := os.Create("node.key")
	_, _ = keyFile.Write(keyPEM)
	_ = keyFile.Close()

	crtFile, _ := os.Create("node.pem")
	_, _ = crtFile.Write(certPEM)
	_ = crtFile.Close()
}

func (s *Node) onClientExpire(_ string, value any) {
	cli := value.(*ClientSession)
	connKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, cli.TunnelId)
	canRemove := false

	if v, ok := s.clientIdMaps.Load(cli.TerminalId); ok {
		oldConnId := v.(string)
		if oldConnId == cli.ConnectionId {
			canRemove = true
			log.Println("准备移除客户端:", cli.TunnelId)
			s.clientIdMaps.Delete(cli.TerminalId)
		}
	}

	if canRemove {
		s.clientIdMaps.Delete(cli.TerminalId)
		if resp, _ := s.etcdOp.GetValue(connKey); resp != nil && resp.Count > 0 {
			kv := resp.Kvs[0]
			sCli := &network.ClientConnInfo{}
			utils.GetJsonValue(sCli, string(kv.Value))
			if sCli.NodeId == s.TerminalId && sCli.ConnectionId == cli.ConnectionId {
				_, _ = s.etcdOp.Delete(connKey)
			}
		}
	}
}

func NewNode(ctx context.Context) *Node {
	rand2.Intn(time.Now().Nanosecond())
	node := &Node{}
	node.SetCtx(ctx)
	node.SetOnClose(node.OnClose)
	node.invokeRoute = network.NewInvokeRoute(node.Ctx())
	node.connectionId = uuid.New().String()
	node.clients = utils.NewCache[*ClientSession](node.Ctx())
	node.clients.SetExpireHandler(node.onClientExpire)
	node.clientIdMaps = sync.Map{}
	node.transfers = utils.NewCache[*TransferSession](node.Ctx())
	node.transfers.SetExpireHandler(func(key string, value any) {
		if transfer, ok := value.(*TransferSession); ok {
			_ = transfer.Close()
		}
	})

	utils.ReadJsonSetting("node.json", node, func() {
		node.TerminalId = uuid.New().String()
		node.ServerPort = 18888
		node.ClientTimeOut = 20
	})

	if node.ClientTimeOut == 0 {
		node.ClientTimeOut = 20
	}

	node.initEtcd()
	node.initQuic()
	log.Println(fmt.Sprintf("Node[%v]服务已启动成功", node.TerminalId))
	return node
}

func (s *Node) createEtcdMutex(key string, ttl int64) (*EtcdMutex, error) {
	return NewEtcdMutex(s.Ctx(), s.etcdOp.etcdCli, key, ttl)
}

func (s *Node) initEtcd() {
	endPoints := strings.Split(s.EtcdUri, ",")
	if len(endPoints) > 0 {
		addr := endPoints[0]
		localIP := ""
		if conn, err := net.Dial("tcp4", addr); err == nil {
			localIP = strings.Split(conn.LocalAddr().String(), ":")[0]
			_ = conn.Close()
		} else {
			log.Panicln("etcd服务地址不正确")
		}

		s.etcdOp = NewEtcdOp(s.Ctx(), s.EtcdUri)
		nodeAddr := fmt.Sprintf("%v:%v", localIP, s.ServerPort)
		nodeInfo := &network.NodeInfo{NodeAddr: nodeAddr}
		nodeInfo.TerminalId = s.TerminalId
		nodeInfo.ConnectionId = s.connectionId
		nodeInfo.Type = network.TerminalType_Node
		_, leaseId := s.etcdOp.CreateLease(5, true)
		_, _ = s.etcdOp.PutValueWithLease(s.etcdOp.GetNodeKey(s.TerminalId), nodeInfo, leaseId)
		s.etcdOp.AddWatcher(EtcdKey_Node, s.nodeWatcher)
		s.etcdOp.AddWatcher(EtcdKey_Client_Connection, s.clientLoginWatcher)
	} else {
		log.Panicln("没有配置Etcd连接")
	}
}

func (s *Node) initInvokeHandles() {
	s.invokeRoute.AddHandler("/client/register", s.clientRegister)
	s.invokeRoute.AddHandler("/client/login", s.clientLogin)
	s.invokeRoute.AddHandler("/client/sayOnline", s.clientSayOnline)
	s.invokeRoute.AddHandler("/client/transfer/map/list", s.getClientTransferMapList)
}

func (s *Node) initHttpHandlers(e *echo.Echo) {
	e.GET("/test", s.onHttpTest)
	e.POST("/_zhangyu/transfer/map/add", s.onHttpPortAdd)
}

func (s *Node) transfer(r *network.Invoker) {
	req, _, err := r.ReadInvoke()
	if err != nil {
		log.Println(err)
		return
	}
	connId := r.ConnId()
	mapRequest := &network.TransferRequest{}
	utils.GetJsonValue(mapRequest, req.BodyJson)
	invResp := s.connectTarget(mapRequest, connId)

	invErr := r.WriteInvoke(invResp)
	if err != nil {
		log.Println(invErr)
		return
	}

	transfer := NewTransferSession(r.Ctx(), connId, r.TransportConn())
	s.setTransfer(connId, transfer)
	select {
	case <-s.Ctx().Done():
		return
	case <-transfer.TransferChan():
		transfer.Transfer()
		s.deleteTransfer(transfer.TransferId())
	}
}

func (s *Node) createRoundTripper(invoker *network.Invoker) *http.Transport {
	re := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	re.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return invoker.TransportConn(), nil
	}
	return re
}

// 通知被控通道，建立一条流量连接到服务端
func (s *Node) connectTarget(mapRequest *network.TransferRequest, connId string) *network.InvokeResponse {
	connKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, mapRequest.TargetTerminalTunnelId)
	targetSession := &ClientSession{}
	invResp := &network.InvokeResponse{}
	//检查目标tunnel是否在线
	if s.etcdOp.GetJsonValue(connKey, targetSession) {
		//核对校验码
		if mapRequest.TargetTerminalAuthCode != targetSession.AuthCode {
			invResp.ResultCode = network.InvokeResult_Error
			invResp.ResultMessage = fmt.Sprintf("转发调用失败,被控通道[%v]的验证码不正确", mapRequest.TargetTerminalTunnelId)
		} else {
			targetReq := network.NewInvokeRequest(uuid.New().String(), "/_zhangyu/transfer/map/start")
			targetReq.BodyJson = utils.GetJsonString(mapRequest)
			targetReq.Header["ConnectionId"] = connId
			if session := s.clients.Get(targetSession.ConnectionId); session != nil {
				//发送连接指令，通知目标tunnel建立流量连接
				if targetResp, targetErr := session.ClientInvoker.Invoke(targetReq); targetErr != nil {
					invResp.ResultCode = network.InvokeResult_Error
					invResp.ResultMessage = fmt.Sprintf("被控通道接入出错：%v", targetErr)
				} else {
					invResp.ResultCode = targetResp.ResultCode
					invResp.ResultMessage = targetResp.ResultMessage
				}
			} else {
				invResp.ResultCode = network.InvokeResult_Error
				invResp.ResultMessage = "被控通道接入出错：跨服务端的功能还未完成开发"
			}
		}
	} else {
		invResp.ResultCode = network.InvokeResult_Error
		invResp.ResultMessage = fmt.Sprintf("转发调用失败,被控通道[%v]不在线", mapRequest.TargetTerminalTunnelId)
	}
	return invResp
}

func (s *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RequestURI() != network.WebSocket_ServicePath {
		host := strings.ToLower(strings.Split(strings.TrimSpace(r.Host), ":")[0])
		httpKey := fmt.Sprintf("%v/%v", EtcdKey_HttpDomain_Bind, host)
		tq := &network.TransferRequest{}

		if s.etcdOp.GetJsonValue(httpKey, tq) {
			targetCliConn := &network.ClientConnInfo{}
			targetCliKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, tq.TargetTerminalTunnelId)
			targetUri, _ := url.Parse(tq.TargetTerminalUri)
			if s.etcdOp.GetJsonValue(targetCliKey, targetCliConn) {
				httpConnId := fmt.Sprintf("%v|%v", r.RemoteAddr, tq.TargetTerminalTunnelId)
				transfer := s.transfers.Get(httpConnId)
				if transfer == nil {
					transfer = NewTransferSession(s.Ctx(), httpConnId, nil)
					s.setTransfer(transfer.TransferId(), transfer)
				}
				go func() {
					_ = s.connectTarget(tq, httpConnId)
				}()
				<-transfer.TransferChan()
				s.transfers.SetExpire(transfer.TransferId(), time.Minute*5)
				if targetStream := transfer.TargetStream(); targetStream != nil {
					if invoker, ok := targetStream.(*network.Invoker); ok {
						outReq := &http.Request{}
						*outReq = *r
						outReq.Host = targetUri.Host
						outReq.URL.Host = targetUri.Host
						outReq.URL.Scheme = targetUri.Scheme

						if resp, err := s.createRoundTripper(invoker).RoundTrip(outReq); err == nil {
							for k, v := range resp.Header {
								value := strings.Join(v, ";")
								w.Header().Set(k, value)
							}
							w.WriteHeader(resp.StatusCode)
							_ = resp.Write(w)
						}
					}
				}

			}
		} else {
			s.httpMux.ServeHTTP(w, r)
		}
	} else {
		s.httpMux.ServeHTTP(w, r)
	}
}

func (s *Node) initQuic() {
	addr := fmt.Sprintf("0.0.0.0:%v", s.ServerPort)
	s.quicMux = echo.New()
	//s.httpMux = echo.New()

	// 开启传统的HTTP服务
	// 针对Http服务，注册handler
	// 这里分开quic跟Http，是因为webtransport-go中有bug,http调用过程未完成时，不接受新的session

	//s.initHttpHandlers(s.httpMux)
	//go func() {
	//	//tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	//	//if err != nil {
	//	//	log.Fatalln(err)
	//	//	return
	//	//}
	//
	//	server := &http.Server{
	//		Addr:    "0.0.0.0:19999",
	//		Handler: s,
	//	}
	//	_ = server.ListenAndServe()
	//}()

	s.generateTLSConfig()
	certs := make([]tls.Certificate, 1)
	certs[0], _ = tls.LoadX509KeyPair("node.pem", "node.key")

	tlsConfig := &tls.Config{
		Certificates: certs,
	}

	//初始化http3服务
	h3 := &http3.Server{
		Addr:      addr,
		TLSConfig: tlsConfig,
		Handler:   s.quicMux,
	}

	//在Http3的基础上，升级成WebTransport服务
	s.transportServer = &webtransport.Server{
		H3: *h3,
		CheckOrigin: func(r *http.Request) bool {
			return strings.HasPrefix(r.URL.RequestURI(), network.WebSocket_ServicePath)
		},
	}

	s.h3Server = h3
	s.initInvokeHandles()
	// 针对指定的路径，升级成WebTransport
	s.quicMux.Any(network.WebSocket_ServicePath, s.upgradeWebtransportConn)

	// 开启quic协议
	go func() {
		_ = s.transportServer.ListenAndServe()
	}()
}

func (s *Node) upgradeWebtransportConn(c echo.Context) error {
	connectionId := c.Request().Header.Get(network.HeadKey_ConnectionId)
	isCmdTrans := c.Request().Header.Get(network.HeadKey_ConnectionType) == network.Connection_Command
	isInstanceFrom := c.Request().Header.Get(network.HeadKey_ConnectionType) == network.Connection_Instance_From
	isInstanceTarget := c.Request().Header.Get(network.HeadKey_ConnectionType) == network.Connection_Instance_Target
	if session, err := s.transportServer.Upgrade(c.Response().Writer, c.Request()); err != nil {
		log.Println(err)
	} else {
		if stream, err := session.AcceptStream(s.Ctx()); err != nil {
			return err
		} else {
			invoker := s.invokeRoute.AddInvoker(connectionId, session, stream)
			if isCmdTrans {
				go s.invokeRoute.DispatchInvoke(invoker)
			}
			if isInstanceFrom {
				go s.transfer(invoker)
			}
			if isInstanceTarget {
				go func() {
					transfer := s.getTransfer(invoker.ConnId())
					if transfer == nil {
						invoker.CtxCancel()
					} else {
						transfer.SetTargetStream(invoker)
						transfer.TransferChan() <- true
					}
				}()
			}
		}
	}
	return nil
}

func (s *Node) checkClientLogin(cliInfo *ClientSession, isNew bool) bool {
	key := fmt.Sprintf("%v/%v", EtcdKey_Client_Record, cliInfo.TerminalId)
	clientInfo := &network.ClientInfo{}
	exists := s.etcdOp.GetJsonValue(key, clientInfo)
	if isNew {
		return !exists
	}

	re := exists && clientInfo.Token == cliInfo.Token
	if re {
		cliInfo.TunnelId = clientInfo.TunnelId
		cliInfo.AuthCode = clientInfo.AuthCode
	}

	return re
}

// 新的客户端连接
func (s *Node) clientRegister(invoker *network.Invoker, r *network.InvokeRequest) *network.InvokeResponse {
	cliInfo := &ClientSession{ClientInvoker: invoker, ClientIP: invoker.ConnIP()}
	utils.GetJsonValue(cliInfo, r.BodyJson)
	if !s.checkClientLogin(cliInfo, true) {
		return network.NewErrorResponse(r, "Token信息不正确,注册失败")
	}

	for {
		tmpInt := rand2.Int63n(99999999)
		if tmpInt < 10000000 {
			tmpInt = tmpInt + 10000000
		}
		tunnelId := fmt.Sprintf("%d", tmpInt)
		if etcdMux, err := s.createEtcdMutex(tunnelId, 30); err != nil {
			return network.NewInvokeResponse(r.RequestId, network.InvokeResult_Error, err.Error())
		} else {
			if etcdMux.Lock() {
				cliRecKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Record, cliInfo.TerminalId)
				cliInfo.NodeId = s.TerminalId
				cliInfo.TunnelId = tunnelId
				cliInfo.Token = utils.RandStringWithFullChar(64)
				cliInfo.AuthCode = utils.RandStringWithLetterChar(8)
				_, _ = s.etcdOp.PutValue(cliRecKey, cliInfo.ClientInfo)
				return s.updateClients(r, cliInfo)
			}
		}
	}
}

// clientLogin 已有的客户端登录
func (s *Node) clientLogin(invoker *network.Invoker, r *network.InvokeRequest) *network.InvokeResponse {
	cliInfo := &ClientSession{ClientInvoker: invoker}
	utils.GetJsonValue(cliInfo, r.BodyJson)
	if !s.checkClientLogin(cliInfo, false) {
		return network.NewErrorResponse(r, "Token信息不正确,注册失败")
	}
	cliInfo.NodeId = s.TerminalId
	cliInfo.ClientIP = invoker.ConnIP()
	re := s.updateClients(r, cliInfo)
	return re
}

func (s *Node) updateClients(r *network.InvokeRequest, cliInfo *ClientSession) *network.InvokeResponse {
	if v, ok := s.clientIdMaps.Load(cliInfo.TerminalId); ok {
		curConnectionId := cliInfo.ConnectionId
		oldConnectionId := v.(string)
		if oldConnectionId != curConnectionId {
			if oldCli := s.clients.Get(oldConnectionId); oldCli != nil {
				Req := network.NewInvokeRequest(uuid.New().String(), "/client/kick")
				go func() {
					_, _ = oldCli.ClientInvoker.Invoke(Req)
				}()
				time.Sleep(time.Millisecond * 100)
				s.invokeRoute.RemoveInvoker(oldConnectionId)
				log.Println(fmt.Sprintf("通知客户端[%v]下线", oldConnectionId))
			}
		}
	}

	connKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, cliInfo.TunnelId)
	_, leaseID := s.etcdOp.CreateLease(s.ClientTimeOut, true)
	_, _ = s.etcdOp.PutValueWithLease(connKey, cliInfo, leaseID)

	re := network.NewSuccessResponse(r, cliInfo)
	log.Println(fmt.Sprintf("客户端[%v]注册成功，分配通道ID[%v]", cliInfo.TerminalId, cliInfo.TunnelId))
	s.clients.Set(cliInfo.ConnectionId, cliInfo)
	s.clientIdMaps.Store(cliInfo.TerminalId, cliInfo.ConnectionId)
	s.clients.SetExpire(cliInfo.ConnectionId, time.Second*time.Duration(s.ClientTimeOut))
	return re
}

func (s *Node) clientSayOnline(_ *network.Invoker, r *network.InvokeRequest) *network.InvokeResponse {
	cliInfo := &network.ClientConnInfo{}
	utils.GetJsonValue(cliInfo, r.BodyJson)

	key := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, cliInfo.TunnelId)
	clientInfo := &network.ClientConnInfo{}
	if s.etcdOp.GetJsonValue(key, clientInfo) {
		if clientInfo.Token == cliInfo.Token && clientInfo.ConnectionId == cliInfo.ConnectionId {
			s.clients.SetExpire(clientInfo.ConnectionId, time.Second*time.Duration(s.ClientTimeOut))
		}
	}

	return network.NewSuccessResponse(r, "")
}

func (s *Node) clientLoginWatcher(eventType string, key string, value []byte) {
	if eventType != "PUT" {
		return
	}

	cliInfo := &network.ClientConnInfo{}
	utils.GetJsonValue(cliInfo, string(value))
	//如果登录的ID在当前节点，说明登录时已处理过，不需要再处理
	if cliInfo.NodeId == s.TerminalId {
		return
	}

}

func (s *Node) onHttpTest(c echo.Context) error {
	c.String(200, "test ok")
	return nil
}

func (s *Node) onHttpPortAdd(c echo.Context) error {
	transferMapReq := &network.TransferRequest{}
	if err := c.Bind(transferMapReq); err != nil {
		_ = c.JSON(200, utils.ErrResponse(err.Error()))
		return err
	}

	listenCli := &network.ClientInfo{}
	listenKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Record, transferMapReq.ListenTerminalId)
	if !s.etcdOp.GetJsonValue(listenKey, listenCli) {
		err := c.JSON(200, utils.ErrResponse(fmt.Sprintf("无效的通道ID[%v]", transferMapReq.TargetTerminalTunnelId)))
		return err
	}

	if listenCli.TerminalId != transferMapReq.ListenTerminalId || listenCli.Token != transferMapReq.ListenTerminalToken {
		err := c.JSON(200, utils.ErrResponse("添加映射端口失败,token验证未能通过"))
		return err
	}

	var connId string
	if v, ok := s.clientIdMaps.Load(listenCli.TerminalId); ok {
		connId = v.(string)
	} else {
		err := c.JSON(200, utils.ErrResponse(fmt.Sprintf("监听通道不在线[%v]", listenCli.TerminalId)))
		return err
	}

	cliSession := s.clients.Get(connId)
	if cliSession == nil {
		err := c.JSON(200, utils.ErrResponse(fmt.Sprintf("监听通道不在线[%v]", listenCli.TerminalId)))
		return err
	}

	invokeReq := network.NewInvokeRequest(uuid.New().String(), c.Request().RequestURI)
	invokeReq.BodyJson = utils.GetJsonString(transferMapReq)

	uri, _ := url.Parse(transferMapReq.ListenTerminalUri)
	if uri.Scheme == "http" {
		host := strings.ToLower(strings.Split(strings.TrimSpace(uri.Host), ",")[0])
		httpKey := fmt.Sprintf("%v/%v", EtcdKey_HttpDomain_Bind, host)
		bufReq := &network.TransferRequest{}
		if s.etcdOp.GetJsonValue(httpKey, bufReq) {
			err := c.JSON(200, utils.ErrResponse(fmt.Sprintf("添加连接通道失败：域名[%v]已被其他通道使用", host)))
			return err
		} else {
			_, _ = s.etcdOp.PutValue(httpKey, transferMapReq)
		}
	}

	if resp, err := cliSession.ClientInvoker.Invoke(invokeReq); err != nil {
		err := c.JSON(200, utils.ErrResponse(fmt.Sprintf("添加连接通道失败：%v", err.Error())))
		return err
	} else {
		if resp.ResultCode == network.InvokeResult_Success {
			key := fmt.Sprintf(EtcdKey_Client_MapItem, transferMapReq.ListenTerminalId, transferMapReq.ListenTerminalUri)
			_, _ = s.etcdOp.PutValue(key, transferMapReq)
			return c.JSON(200, utils.SuccessResponse())
		} else {
			return c.JSON(200, utils.ErrResponse(resp.ResultMessage))
		}
	}
}

func (s *Node) getClientTransferMapList(invoker *network.Invoker, request *network.InvokeRequest) *network.InvokeResponse {
	clientInfo := &network.ClientInfo{}
	utils.GetJsonValue(clientInfo, request.BodyJson)
	terminalId := clientInfo.TerminalId
	savedCliInfo := &network.ClientInfo{}
	key := fmt.Sprintf("%v/%v", EtcdKey_Client_Record, terminalId)
	if s.etcdOp.GetJsonValue(key, savedCliInfo) {
		if savedCliInfo.Token != clientInfo.Token {
			return network.NewErrorResponse(request, "用户身份验证失败")
		} else {
			key = fmt.Sprintf("/zhangyu/clients/records/%v/transferMaps/", terminalId)
			var transMap []*network.TransferRequest
			if s.etcdOp.GetArray(key, func(s string) {
				mapReq := &network.TransferRequest{}
				utils.GetJsonValue(mapReq, s)
				transMap = append(transMap, mapReq)
			}) {
				return network.NewSuccessResponse(request, transMap)
			}
		}
	}

	return network.NewErrorResponse(request, "未知错误")
}

//func (s *Node) httpTransport(next echo.HandlerFunc) echo.HandlerFunc {
//	return func(c echo.Context) error {
//		host := strings.ToLower(strings.Split(strings.TrimSpace(c.Request().Host), ":")[0])
//		httpKey := fmt.Sprintf("%v/%v", EtcdKey_HttpDomain_Bind, host)
//		tq := &network.TransferRequest{}
//
//		if s.etcdOp.GetJsonValue(httpKey, tq) {
//			portStr := fmt.Sprintf("%v|%v", tq.TargetTerminalTunnelId, tq.TargetTerminalUri)
//			targetCliConn := &network.ClientConnInfo{}
//			targetCliKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, tq.TargetTerminalTunnelId)
//			targetUri, _ := url.Parse(tq.TargetTerminalUri)
//			if s.etcdOp.GetJsonValue(targetCliKey, targetCliConn) {
//				httpConnId := fmt.Sprintf("%v|%v", c.Request().RemoteAddr, tq.TargetTerminalTunnelId)
//				transfer := s.transfers.Get(httpConnId)
//				if transfer == nil {
//					transfer = NewTransferSession(s.Ctx(), httpConnId, nil)
//					s.setTransfer(transfer.TransferId(), transfer)
//				}
//				go func() {
//					_ = s.connectTarget(tq, httpConnId)
//				}()
//				<-transfer.TransferChan()
//
//				s.transfers.SetExpire(transfer.TransferId(), time.Minute*5)
//				if targetStream := transfer.TargetStream(); targetStream != nil {
//					if invoker, ok := targetStream.(*network.Invoker); ok {
//						outReq := &http.Request{}
//						*outReq = *c.Request()
//						outReq.Host = targetUri.Host
//						outReq.URL.Host = targetUri.Host
//						outReq.URL.Scheme = targetUri.Scheme
//
//						if resp, err := s.createRoundTripper(invoker).RoundTrip(outReq); err == nil {
//							for k, v := range resp.Header {
//								value := strings.Join(v, ";")
//								c.Response().Header().Set(k, value)
//							}
//							c.Response().WriteHeader(resp.StatusCode)
//							_ = resp.Write(c.Response().Writer)
//						}
//					}
//				}
//
//				return nil
//			} else {
//				return c.JSON(200, utils.ErrResponse(fmt.Sprintf("被控通道[%v]不在线，转发[%v]无法启动",
//					tq.TargetTerminalTunnelId, portStr)))
//			}
//
//		} else {
//			return next(c)
//		}
//	}
//}
