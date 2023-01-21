package etcd

/*
ETCD连接键：
/zhangyu/node/online/{nodeId} 记录在线的Node
包含的键值：
	nodeId			节点ID
	connectionId	当前连接ID
	connectionTime  当前连接建立时间
	ServerAddr		节点服务地址

/zhangyu/client/online/{clientId} 记录在线的客户端
包含的键值：
	clientId		客户端ID
	nodeId			所在节点ID
	connectionId	当前连接ID
	connectionTime  当前连接建立时间
	remoteAddr		客户端连接信息
	clientIP		从Http头获取的真实IP信息
	sendBytes		本次已发送的字节
	receiveBytes	本次已接收的字节

/zhangyu/client/tunnel/{tunnelId} 记录tunnelId与terminalId的对应关系
包含的键值：
	tunnelId	终端通道ID
	terminalId	终端ID
	authCode	通道验证码

/zhangyu/client/record/{clientId} 记录登记过的客户端
包含的键值：
	clientId	终端ID
	token		终端令牌
	tunnelId	终端通道ID
	createTime	终端创建时间

/zhangyu/client/listen/{clientId}/{key} 记录客户端需要监听转发的信息
包含的键值：
	listenClientId	监听终端ID
	listenTunnelId  监听终端TunnelId
	targetClientId  目标终端ID
    targetTunnelId  目标终端TunnelId
	listenAddr      监听地址URI
    targetAddr      目标地址URI

    key的值为: listenTunnelId|listenAddr

/zhangyu/domain/{domain} 登记Http子域名
包含的键值：
	domain 登记的域名
	listenKey 指向监听的Key
*/

const (
	Key_Domain       = "/zhangyu/domain/%v"
	Key_NodeOnline   = "/zhangyu/node/online/%v"
	Key_ClientOnline = "/zhangyu/client/online/%v"
	Key_ClientTunnel = "/zhangyu/client/tunnel/%v"
	Key_ClientRecord = "/zhangyu/client/record/%v"
	Key_ClientListen = "/zhangyu/client/listen/%v/%v"
)
