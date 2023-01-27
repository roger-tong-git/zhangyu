package node

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/roger-tong-git/zhangyu/app"
	"github.com/roger-tong-git/zhangyu/app/node/etcd"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/rpc/quic/srv"
	"github.com/roger-tong-git/zhangyu/utils"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Node struct {
	TerminalId    string
	HttpPort      int
	QuicPort      int
	HttpsPort     int
	EtcdAddr      string
	Domain        string
	ClientTimeOut int
	connectionId  string
	quicSrv       *srv.Server
	jwtSingKey    string
	etcdOp        *etcd.Client
	transports    *utils.Cache[*httpTransport]
	utils.Closer
}

func (n *Node) getTransport(r *http.Request, listen *rpc.Listen) (*httpTransport, error) {
	key := fmt.Sprintf("%v|%v", listen.TargetTunnelId, r.RemoteAddr)
	transport := n.transports.Get(key)
	if transport != nil {
		n.transports.SetExpire(key, time.Second*20)
		return transport, nil
	}

	targetCmdInvoker := n.GetInvoker(listen.TargetClientId)
	if targetCmdInvoker == nil {
		return nil, errors.New(fmt.Sprintf("目标客户端[TunnelId:%v]不在线，转发失败", listen.TargetTunnelId))
	}

	request := rpc.NewInvokeRequest(rpc.InvokePath_Transfer_TargetOut)
	request.Header["HttpTransKey"] = key
	request.PutValue(listen)
	if targetCmdInvoker.WriteRequest(request) != nil {
		_ = targetCmdInvoker.Close()
		return nil, errors.New(fmt.Sprintf("目标客户端[TunnelId:%v]不在线，转发失败", listen.TargetTunnelId))
	}

	transport = NewHttpTransport(key)
	n.transports.Set(key, transport)
	n.transports.SetExpire(key, time.Second*20)
	select {
	case <-time.After(10 * time.Second):
		break
	case <-transport.transChan:
		break
	}

	if transport.invoker == nil {
		n.transports.Delete(key)
		return nil, errors.New(fmt.Sprintf("转发到目标客户端[TunnelId:%v]遇到未知错误，转发失败", listen.TargetTunnelId))
	}

	conn := transport.invoker.GetAttach("Conn").(net.Conn)
	transport.transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	transport.transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return conn, nil
	}

	return transport, nil
}

func (n *Node) OnHttpRoot(c echo.Context, next echo.HandlerFunc) error {
	hostUrl, _ := url.Parse(fmt.Sprintf("https://%v", c.Request().Host))
	host := hostUrl.Hostname()
	domainInfo := n.etcdOp.GetDomainInfo(host)
	if domainInfo == nil {
		return next(c)
	}

	transfer, err := n.getTransport(c.Request(), domainInfo)
	if err != nil {
		return c.String(200, err.Error())
	}

	r := c.Request()
	w := c.Response().Writer
	targetUri, _ := url.Parse(domainInfo.TargetAddr)
	outReq := &http.Request{}
	*outReq = *r
	outReq.Host = targetUri.Host
	outReq.URL.Host = targetUri.Host
	outReq.URL.Scheme = targetUri.Scheme
	var resp *http.Response

	if resp, err = transfer.transport.RoundTrip(outReq); err == nil {
		for k, v := range resp.Header {
			value := strings.Join(v, ";")
			w.Header().Set(k, value)
		}
		w.WriteHeader(resp.StatusCode)
		_, err = io.Copy(w, resp.Body)
		return err
	} else {
		_ = transfer.invoker.Close()
		n.transports.Delete(transfer.key)
		return c.String(http.StatusInternalServerError, err.Error())
	}
}

func (n *Node) getJwtSignKey() string {
	n.jwtSingKey = n.etcdOp.GetJwtSignKey()
	return n.jwtSingKey
}

func (n *Node) ConnectionIn(connType rpc.ConnectionType, invoker *rpc.Invoker) {
	if connType == rpc.ConnectionType_Listen ||
		connType == rpc.ConnectionType_Target {
		req, _, _ := invoker.ReadInvoke()
		handler := n.quicSrv.InvokeRoute().GetUniHandler(req.Path)
		handler(invoker, req)
	}
}

func (n *Node) GetInvoker(terminalId string) *rpc.Invoker {
	onlineRec := n.etcdOp.GetOnlineClient(terminalId)
	if onlineRec != nil {
		return n.quicSrv.InvokeRoute().GetInvoker(onlineRec.InvokerId)
	}
	return nil
}

func (n *Node) GetContext(terminalId string) context.Context {
	onlineRec := n.etcdOp.GetOnlineClient(terminalId)
	if onlineRec != nil {
		invoker := n.quicSrv.InvokeRoute().GetInvoker(onlineRec.InvokerId)
		if invoker != nil {
			return invoker.Ctx()
		}
	}
	return n.quicSrv.InvokeRoute().Ctx()
}

func (n *Node) OnNewClientRec() *rpc.ClientRec {
	return n.etcdOp.GenerateNewClient()
}

func (n *Node) OnCloseClient(invoker *rpc.Invoker) {
	httpTransKeyValue := invoker.GetAttach("HttpTransKey")
	if httpTransKeyValue != nil {
		if httpTransKey, ok := httpTransKeyValue.(string); ok {
			v := n.transports.Get(httpTransKey)
			if v != nil && v.invoker != nil {
				_ = v.invoker.Close()
			}
			n.transports.Delete(httpTransKey)
		}
	}

	if invoker.IsCommandTunnel() {
		onlineInfo := n.etcdOp.GetOnlineClient(invoker.TerminalId())
		if onlineInfo != nil && onlineInfo.InvokerId == invoker.InvokerId() {
			n.etcdOp.DeleteOnlineClient(invoker.TerminalId())
		}
	}
}

func (n *Node) POST(path string, f echo.HandlerFunc) {
	n.quicSrv.HttpRouter().POST(path, f)
}

func (n *Node) GET(path string, f echo.HandlerFunc) {
	n.quicSrv.HttpRouter().GET(path, f)
}

func (n *Node) AuthGET(path string, f echo.HandlerFunc) {
	n.quicSrv.HttpRouter().GET(path, f, n.verifyToken())
}

func (n *Node) AuthPOST(path string, f echo.HandlerFunc) {
	n.quicSrv.HttpRouter().POST(path, f, n.verifyToken())
}

func (n *Node) verifyToken() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			jwtToken := c.Request().Header.Get("JwtToken")
			if jwtToken == "" {
				return c.JSON(200, utils.ErrResponse("身份认证失败,缺少JwtToken"))
			} else {
				token, err := app.ValidToken(jwtToken, n.getJwtSignKey())
				if token.Valid {
					claim := &app.Claim{}
					if utils.GetJsonValue(claim, utils.GetJsonString(token.Claims)) {
						c.Request().Header.Set("Zhangyu-ClientId", claim.ClientId)
					}
				} else if ve, ok := err.(*jwt.ValidationError); ok {
					if ve.Errors&jwt.ValidationErrorExpired != 0 {
						return c.JSON(200, utils.ErrResponse("身份认证失败,Token已失效"))
					}
				}
			}
			return next(c)
		}
	}
}

func (n *Node) init() {
	n.quicSrv.AddRpcHandler(rpc.InvokePath_Client_Login, n.onInvokerClientLogin)
	n.quicSrv.AddRpcHandler(rpc.InvokePath_Client_TransferList, n.onInvokerTransferList)
	n.quicSrv.AddUniHandler(rpc.InvokePath_Transfer_ListenIn, n.onInvokerListenIn)
	n.quicSrv.AddUniHandler(rpc.InvokePath_Transfer_Go, n.onInvokerTransferGo)

	n.etcdOp.AddWatcher(fmt.Sprintf(etcd.Key_ClientOnline, ""), n.onWatcher_ClientOnline)

	n.POST(HttpPath_Post_Login, n.httpHandler_Login)
	n.GET(HttpPath_Get_DomainList, n.httpHandler_DomainList)
	n.AuthPOST(HttpPath_Post_ListenAdd, n.httpHandler_ListenAdd)
	n.AuthPOST(HttpPath_Post_ListenDel, n.httpHandler_ListenDel)
}

func (n *Node) onInvokerClientLogin(invoker *rpc.Invoker, request *rpc.InvokeRequest) *rpc.InvokeResponse {
	clientRec := n.etcdOp.GetClientRec(invoker.TerminalId())
	if clientRec == nil {
		errMsg := fmt.Sprintf("客户端登录失败,TerminalId[%v]无效", invoker.TerminalId())
		log.Println(errMsg)
		go func() {
			time.Sleep(time.Millisecond * 50)
			_ = invoker.Close()
		}()

		return rpc.NewErrorResponse(request.RequestId, errMsg)
	}

	loginReq := &rpc.LoginRequest{}
	if !request.GetValue(loginReq) {
		errMsg := "客户端登录失败,客户登录信息无效"
		log.Println(errMsg)
		go func() {
			time.Sleep(time.Millisecond * 50)
			_ = invoker.Close()
		}()
		return rpc.NewErrorResponse(request.RequestId, errMsg)
	}

	if loginReq.ClientId != clientRec.ClientId || loginReq.Token != clientRec.Token {
		errMsg := "客户端登录失败,客户端身份认证失败"
		log.Println(errMsg)
		go func() {
			time.Sleep(time.Millisecond * 50)
			_ = invoker.Close()
		}()
		return rpc.NewErrorResponse(request.RequestId, errMsg)
	}

	//是否有同ID的客户端存在，如果存在，并且ConnectionId不相同，需要踢下线
	onlineClient := n.etcdOp.GetOnlineClient(invoker.TerminalId())

	onlineClient = &rpc.OnlineClient{
		NodeId:       n.TerminalId,
		ClientId:     invoker.TerminalId(),
		InvokerId:    invoker.InvokerId(),
		ConnectTime:  time.Now(),
		RemoteAddr:   invoker.RemoteAddr(),
		ClientIP:     invoker.ClientIP(),
		SendBytes:    0,
		ReceiveBytes: 0,
	}
	n.etcdOp.PutOnlineClient(invoker.TerminalId(), onlineClient)

	return rpc.NewSuccessResponse(request.RequestId, "").PutValue(clientRec)
}

func (n *Node) onWatcher_ClientOnline(eventType string, key string, prevValue, value string) {
	eventType = strings.ToLower(eventType)
	if eventType == "put" {
		clientOnline := &rpc.OnlineClient{}
		if prevValue != "" && utils.GetJsonValue(clientOnline, prevValue) {
			invoker := n.quicSrv.InvokeRoute().GetInvoker(clientOnline.InvokerId)
			if invoker != nil {
				req := rpc.NewInvokeRequest(rpc.InvokePath_Client_Kick)
				req.Header["Message"] = "有新的客户端上线，当前客户端已被服务端踢下线"
				_ = invoker.WriteRequest(req)
			}
		}
	}
}

func (n *Node) successResponse(w http.ResponseWriter, value any) {
	resp := utils.SuccessResponse(value)
	w.WriteHeader(200)
	bodyBytes := utils.GetJsonBytes(resp)
	_, _ = io.Copy(w, bufio.NewReader(bytes.NewReader(bodyBytes)))
}

func (n *Node) errorResponse(w http.ResponseWriter, message string) {
	resp := utils.ErrResponse(message)
	w.WriteHeader(200)
	bodyBytes := utils.GetJsonBytes(resp)
	_, _ = io.Copy(w, bufio.NewReader(bytes.NewReader(bodyBytes)))
}

func (n *Node) getJsonValue(r *http.Request, v any) bool {
	strBytes, _ := io.ReadAll(r.Body)
	return utils.GetJsonValue(v, string(strBytes))
}

func (n *Node) httpHandler_DomainList(c echo.Context) error {
	clientId := c.Param("clientId")
	domainList := n.etcdOp.GetDomainList(clientId)
	return c.JSON(200, utils.SuccessResponse(domainList))
}

func (n *Node) httpHandler_ListenAdd(c echo.Context) error {
	req := &rpc.ListenRequest{}
	err := c.Bind(req)
	if err != nil {
		return err
	}

	clientId := c.Request().Header.Get("Zhangyu-ClientId")

	if req.ListenClientId != "" && req.ListenClientId != clientId {
		return c.JSON(200, utils.ErrResponse(fmt.Sprintf("无效的客户端ID:%v", req.ListenClientId)))
	}

	if req.TargetTunnelId == "" {
		return c.JSON(200, utils.ErrResponse("TargetClientId不能为空"))
	}

	tunnel := n.etcdOp.GetTunnel(req.TargetTunnelId)
	if tunnel == nil {
		return c.JSON(200, utils.ErrResponse(fmt.Sprintf("无效的客户端TunnelId:%v", req.TargetTunnelId)))
	}

	if tunnel.AuthCode != req.TargetAuthCode {
		return c.JSON(200, utils.ErrResponse("AuthCode验证失败"))
	}

	if req.TargetAddr != "" {
		targetUrl, _ := url.Parse(req.TargetAddr)
		scheme := targetUrl.Scheme
		listenHost := ""
		if req.ListenAddr != "" {
			listenUrl, _ := url.Parse(req.ListenAddr)
			listenHost = listenUrl.Hostname()
		}

		if scheme == "http" || scheme == "https" {
			if listenHost != "" && strings.HasSuffix(listenHost, n.Domain) {
				return c.JSON(200, utils.ErrResponse(fmt.Sprintf("添加http/https流量转发失败,不能指定域名[%v]", listenHost)))
			}

			if listenHost == "" {
				req.ListenAddr = n.etcdOp.GenerateDomainName()
			} else {
				req.ListenAddr = listenHost
			}
		}

		if targetUrl.Scheme == "tcp" || targetUrl.Scheme == "udp" {
			if req.ListenAddr == "" {
				return c.JSON(200, utils.ErrResponse("ListenAddr不能为空"))
			}
		}
	}

	if req.TargetAddr == "" {
		if req.ListenAddr == "" {
			return c.JSON(200, utils.ErrResponse("ListenAddr不能为空"))
		}
	}

	listen := n.etcdOp.AddListenWithTunnel(req.TargetTunnelId, clientId, req.ListenAddr, req.TargetAddr)

	listenReq := &rpc.ListenRequest{
		ListenClientId: clientId,
		TargetTunnelId: req.TargetTunnelId,
		TargetAuthCode: req.TargetAuthCode,
		ListenAddr:     req.ListenAddr,
		TargetAddr:     req.TargetAddr,
	}

	listenInvoker := n.GetInvoker(req.ListenClientId)
	if listenInvoker != nil {
		invReq := rpc.NewInvokeRequest(rpc.InvokePath_Transfer_ListenAdd)
		invReq.PutValue(listenReq)
		if listenInvoker.WriteRequest(invReq) != nil {
			_ = listenInvoker.Close()
		}
	}
	return c.JSON(200, utils.SuccessResponse(listen))
}

func (n *Node) httpHandler_Login(c echo.Context) error {
	loginReq := &rpc.LoginRequest{}
	err := c.Bind(loginReq)
	if err != nil {
		return err
	}

	clientId := loginReq.ClientId
	clientToken := loginReq.Token

	if clientId != "" {
		rec := n.etcdOp.GetClientRec(clientId)
		if rec != nil && rec.Token == clientToken {
			claim := &app.Claim{
				LoginType: 2,
				ClientId:  clientId,
				StandardClaims: jwt.StandardClaims{
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
					Id:        clientId,
					Issuer:    "Zhangyu Node",
					Subject:   "Client Login",
				},
			}
			tokenStr, _ := app.CreateToken(claim, n.getJwtSignKey())
			loginResp := &rpc.LoginResponse{
				TokenString: tokenStr,
			}
			return c.JSON(200, utils.SuccessResponse(loginResp))

		}
	}
	return c.JSON(200, utils.ErrResponse("当前客户端身份认证失败"))
}

func (n *Node) onInvokerTransferList(invoker *rpc.Invoker, request *rpc.InvokeRequest) *rpc.InvokeResponse {
	clientId := invoker.TerminalId()
	re := rpc.NewInvokeResponse(request.RequestId, rpc.InvokeResult_Success, "")
	re.PutValue(n.etcdOp.GetTransferList(clientId))
	return re
}

// Listen 端有连接进入，将Listen信息转发到Target端
func (n *Node) onInvokerListenIn(invoker *rpc.Invoker, request *rpc.InvokeRequest) {
	listen := &rpc.Listen{}
	if request.GetValue(listen) {
		tunnel := n.etcdOp.GetTunnel(listen.TargetTunnelId)
		targetCmdInvoker := n.GetInvoker(tunnel.TerminalId)
		if targetCmdInvoker != nil {
			request.Path = rpc.InvokePath_Transfer_TargetOut
			request.Header["ListenInvokerId"] = invoker.InvokerId()
			request.Header["ListenClientId"] = invoker.TerminalId()
			go func() {
				if targetCmdInvoker.WriteRequest(request) != nil {
					_ = targetCmdInvoker.Close()
					_ = invoker.Close()
				}
			}()
		}
	}
}

func (n *Node) onInvokerTransferGo(invoker *rpc.Invoker, request *rpc.InvokeRequest) {
	listenInvokerId := request.Header["ListenInvokerId"]
	httpTransKey := request.Header["HttpTransKey"]

	if httpTransKey != "" {
		transport := n.transports.Get(httpTransKey)
		if transport != nil {
			invoker.SetAttach("HttpTransKey", httpTransKey)
			transport.invoker = invoker
			transport.transChan <- true
		}
		return
	}

	listenInvoker := n.quicSrv.InvokeRoute().GetInvoker(listenInvokerId)
	if listenInvoker != nil {
		invoker.Copy(listenInvoker)
	}
}

func (n *Node) httpHandler_ListenDel(c echo.Context) error {
	//clientId := c.Request().Header.Get("Zhangyu-ClientId")
	//addr := c.Param("listenAddr")

	return nil
}

func NewNode(ctx context.Context) *Node {
	re := &Node{}
	utils.ReadJsonSetting("node.json", re, func() {
		re.QuicPort = 18888
		re.HttpPort = 18888
		re.HttpsPort = 18889

		re.EtcdAddr = "127.0.0.1:2379"
		re.TerminalId = uuid.New().String()
	})

	re.transports = utils.NewCache[*httpTransport](re.Ctx())
	re.transports.SetDeleteHandler(func(key string) {
		v := re.transports.Get(key)
		if v != nil && v.invoker != nil {
			_ = v.invoker.Close()
		}
	})

	re.SetCtx(ctx)
	re.connectionId = uuid.New().String()
	if re.QuicPort == 0 {
		re.QuicPort = 18888
	}
	if re.HttpPort == 0 {
		re.HttpPort = 18888
	}
	if re.HttpsPort == 0 {
		re.HttpsPort = 18889
	}

	utils.SaveJsonSetting("node.json", re)

	re.quicSrv = srv.NewServer(re.Ctx(), re.QuicPort, re.HttpPort, re.HttpsPort, rpc.ServicePath, re)

	endPoints := strings.Split(re.EtcdAddr, ",")
	if len(endPoints) > 0 {
		addr := endPoints[0]
		localIP := ""
		if conn, err := net.Dial("tcp", addr); err == nil {
			localIP = strings.Split(conn.LocalAddr().String(), ":")[0]
			_ = conn.Close()
		} else {
			log.Panicln("etcd服务地址不正确")
		}

		re.etcdOp = etcd.NewEtcdOp(re.Ctx(), re.EtcdAddr, re.Domain)
		nodeAddr := fmt.Sprintf("%v:%v", localIP, re.QuicPort)
		nodeInfo := &rpc.NodeInfo{NodeAddr: nodeAddr}
		nodeInfo.TerminalId = re.TerminalId
		nodeInfo.ConnectionId = re.connectionId
		nodeInfo.Type = rpc.TerminalType_Node
		re.etcdOp.RegisterNode(nodeInfo)
	} else {
		log.Fatalln("ETCD服务地址设置不正确")
	}
	re.init()
	return re
}
