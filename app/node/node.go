package node

import (
	"bufio"
	"bytes"
	"context"
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
	"strings"
	"time"
)

type Node struct {
	TerminalId    string `json:"-"` //设备ID
	QuicPort      int
	ClientTimeOut int
	connectionId  string
	quicSrv       *srv.Server
	jwtSingKey    string
	etcdOp        *etcd.Client
	utils.Closer
}

func (n *Node) getJwtSignKey() string {
	n.jwtSingKey = n.etcdOp.GetJwtSignKey()
	return n.jwtSingKey
}

func (n *Node) ConnectionIn(connId string, connType rpc.ConnectionType, invoker *rpc.Invoker) {

}

func (n *Node) GetInvoker(terminalId string) *rpc.Invoker {
	return n.quicSrv.InvokeRoute().GetInvoker(terminalId)
}

func (n *Node) GetContext(terminalId string) context.Context {
	return n.quicSrv.InvokeRoute().Ctx()
}

func (n *Node) OnNewClientRec() *rpc.ClientRec {
	return n.etcdOp.GenerateNewClient()
}

func (n *Node) OnCloseClient(invoker *rpc.Invoker) {
	onlineInfo := n.etcdOp.GetOnlineClient(invoker.TerminalId())
	if onlineInfo != nil && onlineInfo.InvokerId == invoker.InvokerId() {
		n.etcdOp.DeleteOnlineClient(invoker.TerminalId())
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
	n.etcdOp.AddWatcher(fmt.Sprintf(etcd.Key_ClientOnline, ""), n.onWatcher_ClientOnline)

	n.POST(HttpPath_Post_Login, n.httpHandler_Login)
	n.GET(HttpPath_Get_DomainList, n.httpHandler_DomainList)
	n.AuthPOST(HttpPath_Post_ListenAdd, n.httpHandler_ListenAdd)

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

	if req.ListenAddr == "" {
		return c.JSON(200, utils.ErrResponse("ListenAddr不能为空"))
	}

	clientId := c.Request().Header.Get("Zhangyu-ClientId")

	if req.ListenClientId != clientId {
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

	n.etcdOp.AddListenWithTunnel(req.TargetTunnelId, req.ListenClientId, req.ListenAddr, req.TargetAddr)
	return c.JSON(200, utils.SuccessResponse(nil))
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

func NewNode(ctx context.Context, etcdUri string, quicPort int) *Node {
	re := &Node{QuicPort: quicPort}
	re.SetCtx(ctx)
	re.connectionId = uuid.New().String()
	re.TerminalId = re.connectionId
	re.quicSrv = srv.NewServer(re.Ctx(), quicPort, rpc.ServicePath, re)

	endPoints := strings.Split(etcdUri, ",")
	if len(endPoints) > 0 {
		addr := endPoints[0]
		localIP := ""
		if conn, err := net.Dial("tcp", addr); err == nil {
			localIP = strings.Split(conn.LocalAddr().String(), ":")[0]
			_ = conn.Close()
		} else {
			log.Panicln("etcd服务地址不正确")
		}

		re.etcdOp = etcd.NewEtcdOp(re.Ctx(), etcdUri)
		nodeAddr := fmt.Sprintf("%v:%v", localIP, quicPort)
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
