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
	"os"
	"strings"
	"time"
)

type Node struct {
	TerminalId      string //设备ID
	EtcdUri         string //Etcd连接字符串
	ServerPort      int
	ClientTimeOut   int
	connectionId    string
	invokeRoute     *network.InvokeRoute
	etcdOp          *EtcdOp
	h3Server        *http3.Server
	clients         *utils.Cache[*network.ClientConnInfo]
	transportServer *webtransport.Server
	utils.Closer
}

func (s *Node) OnClose() {

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
	cli := value.(*network.ClientConnInfo)
	log.Println("准备移除客户端:", cli.TunnelId)
	key1 := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, cli.TunnelId)
	if resp, _ := s.etcdOp.GetValue(key1); resp != nil && resp.Count > 0 {
		kv := resp.Kvs[0]
		sCli := &network.ClientConnInfo{}
		utils.GetJsonValue(sCli, string(kv.Value))
		if sCli.NodeId == s.TerminalId && sCli.ConnectionId == cli.ConnectionId {
			_, _ = s.etcdOp.Delete(key1)
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
	node.clients = utils.NewCache[*network.ClientConnInfo](node.Ctx())
	node.clients.SetExpireHandler(node.onClientExpire)

	utils.ReadJsonSetting("node.json", node, func() {
		node.TerminalId = uuid.New().String()
		node.ServerPort = 18888
		node.ClientTimeOut = 20
	})

	if node.ClientTimeOut == 0 {
		node.ClientTimeOut = 20
	}

	node.initEtcd()
	node.initHttp()
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
	} else {
		log.Panicln("没有配置Etcd连接")
	}
}

func (s *Node) initInvokeHandles() {
	s.invokeRoute.AddHandler("/client/register", s.clientRegister)
	s.invokeRoute.AddHandler("/client/login", s.clientLogin)
	s.invokeRoute.AddHandler("/client/sayOnline", s.clientSayOnline)

	//s.invokeRoute.AddHandler("/test/call1", func(r *network.InvokeRequest) *network.InvokeResponse {
	//	log.Println("invoke /test/call1")
	//	return network.NewInvokeResponse(r.RequestId, network.InvokeResult_Success, "AAAA调用完成")
	//})
}

func (s *Node) initHttp() {
	s.generateTLSConfig()
	echoMux := echo.New()

	certs := make([]tls.Certificate, 1)
	certs[0], _ = tls.LoadX509KeyPair("node.pem", "node.key")
	addr := fmt.Sprintf("0.0.0.0:%v", s.ServerPort)

	//初始化http3服务
	h3 := &http3.Server{
		Addr: addr,
		TLSConfig: &tls.Config{
			Certificates: certs,
		},
		Handler: echoMux,
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
	echoMux.Any(network.WebSocket_ServicePath, func(c echo.Context) error {
		connectionId := c.Request().Header.Get(network.HeadKey_ConnectionId)
		if session, err := s.transportServer.Upgrade(c.Response().Writer, c.Request()); err != nil {
			log.Println(err)
		} else {
			if stream, err := session.AcceptStream(s.Ctx()); err != nil {
				log.Println(err)
				return err
			} else {
				s.invokeRoute.AddInvoker(connectionId, stream, stream)
			}
		}
		return nil
	})

	go func() {
		_ = s.transportServer.ListenAndServe()
	}()
}

func (s *Node) checkClient(cliInfo *network.ClientConnInfo, isNew bool) bool {
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
func (s *Node) clientRegister(r *network.InvokeRequest) *network.InvokeResponse {
	cliInfo := &network.ClientConnInfo{}
	utils.GetJsonValue(cliInfo, r.BodyJson)
	if !s.checkClient(cliInfo, true) {
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
func (s *Node) clientLogin(r *network.InvokeRequest) *network.InvokeResponse {
	cliInfo := &network.ClientConnInfo{}
	utils.GetJsonValue(cliInfo, r.BodyJson)
	if !s.checkClient(cliInfo, false) {
		return network.NewErrorResponse(r, "Token信息不正确,注册失败")
	}
	cliInfo.NodeId = s.TerminalId
	re := s.updateClients(r, cliInfo)
	return re
}

func (s *Node) updateClients(r *network.InvokeRequest, cliInfo *network.ClientConnInfo) *network.InvokeResponse {
	connKey := fmt.Sprintf("%v/%v", EtcdKey_Client_Connection, cliInfo.TunnelId)
	_, leaseID := s.etcdOp.CreateLease(s.ClientTimeOut, true)
	_, _ = s.etcdOp.PutValueWithLease(connKey, cliInfo, leaseID)
	re := network.NewSuccessResponse(r, cliInfo)
	log.Println(fmt.Sprintf("客户端[%v]注册成功，分配通道ID[%v]", cliInfo.TerminalId, cliInfo.TunnelId))
	s.clients.Set(cliInfo.ConnectionId, cliInfo)
	s.clients.SetExpire(cliInfo.ConnectionId, time.Second*time.Duration(s.ClientTimeOut))
	return re
}

func (s *Node) clientSayOnline(r *network.InvokeRequest) *network.InvokeResponse {
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
