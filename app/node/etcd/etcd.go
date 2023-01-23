package etcd

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/roger-tong-git/zhangyu/rpc"
	"github.com/roger-tong-git/zhangyu/utils"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"log"
	rand2 "math/rand"
	"strings"
	"time"
)

type Client struct {
	EtcdUri  string //Etcd连接字符串
	etcdCli  *clientv3.Client
	nodeInfo *rpc.NodeInfo
	utils.Closer
}

type Mutex struct {
	Ttl     int64  //租约时间
	Key     string //etcd的key
	lease   clientv3.Lease
	leaseID clientv3.LeaseID
	client  *clientv3.Client
	txn     clientv3.Txn
	utils.Closer
}

func (em *Mutex) UnLock() {
	_, _ = em.lease.Revoke(em.Ctx(), em.leaseID)
	_, _ = em.client.Delete(em.Ctx(), em.Key)
}

func (em *Mutex) Lock() bool {
	em.txn.If(clientv3.Compare(clientv3.CreateRevision(em.Key), "=", 0)).
		Then(clientv3.OpPut(em.Key, "", clientv3.WithLease(em.leaseID))).
		Else()
	txnResp, err := em.txn.Commit()
	if err != nil {
		log.Println(err)
		return false
	}
	return txnResp.Succeeded
}

func NewEtcdMutex(ctx context.Context, client *clientv3.Client, key string, ttl int64) (*Mutex, error) {
	var err error
	em := &Mutex{client: client, Key: key, Ttl: ttl}
	em.SetCtx(ctx)
	em.SetOnClose(em.UnLock)
	em.txn = clientv3.NewKV(em.client).Txn(em.Ctx())
	em.lease = clientv3.NewLease(em.client)
	leaseResp, err := em.lease.Grant(em.Ctx(), em.Ttl)
	if err != nil {
		return nil, err
	}
	em.leaseID = leaseResp.ID
	_, err = em.lease.KeepAlive(ctx, em.leaseID)
	return em, err
}

func NewEtcdOp(ctx context.Context, etcdUri string) *Client {
	op := &Client{
		EtcdUri: etcdUri,
	}

	op.SetCtx(ctx)
	op.SetOnClose(op.onClose)
	etcdConf := clientv3.Config{
		Endpoints:   strings.Split(op.EtcdUri, ","),
		DialTimeout: 5 * time.Second,
		LogConfig: &zap.Config{
			Level:       zap.NewAtomicLevelAt(zap.ErrorLevel),
			Development: false,
			Sampling: &zap.SamplingConfig{
				Initial:    100,
				Thereafter: 100,
			},
			Encoding:      "json",
			EncoderConfig: zap.NewProductionEncoderConfig(),
			// Use "/dev/null" to discard all
			OutputPaths:      []string{"stderr"},
			ErrorOutputPaths: []string{"stderr"},
		},
	}

	if etcdCli, err := clientv3.New(etcdConf); err == nil {
		op.etcdCli = etcdCli
	} else {
		log.Fatalln(err)
	}
	return op
}

func (s *Client) onClose() {
	if s.etcdCli != nil {
		_ = s.etcdCli.Close()
		log.Println("ETCD连接已关闭")
	}
}

func (s *Client) AddWatcher(prefix string, watcher func(eventType string, key string, prev, curValue string)) {
	go func() {
		watchChan := s.etcdCli.Watch(s.Ctx(), prefix, clientv3.WithPrefix(), clientv3.WithPrevKV())
		for watch := range watchChan {
			for _, event := range watch.Events {
				prevValue := ""
				curValue := string(event.Kv.Value)
				if event.PrevKv != nil {
					prevValue = string(event.PrevKv.Value)
				}
				switch event.Type {
				case mvccpb.DELETE:
					watcher("DELETE", string(event.Kv.Key), prevValue, curValue)
					break
				case mvccpb.PUT:
					watcher("PUT", string(event.Kv.Key), prevValue, curValue)
				}
			}
		}
	}()
}

func (s *Client) CreateLease(seconds int, keepalive bool) (clientv3.Lease, clientv3.LeaseID) {
	ls := clientv3.NewLease(s.etcdCli)
	resp, _ := ls.Grant(s.Ctx(), int64(seconds))
	if keepalive {
		_, _ = ls.KeepAlive(s.Ctx(), resp.ID)
	}
	return ls, resp.ID
}

func (s *Client) RenewLease(lease clientv3.Lease, seconds int) error {
	resp, _ := lease.Grant(s.Ctx(), int64(seconds))
	if _, err := lease.KeepAliveOnce(s.Ctx(), resp.ID); err != nil {
		return err
	}
	return nil
}

func (s *Client) PutValueWithLease(key string, value any, leaseId clientv3.LeaseID) (clientv3.OpResponse, error) {
	return s.etcdCli.Do(s.Ctx(),
		clientv3.OpPut(key, utils.GetJsonString(value), clientv3.WithLease(leaseId)))
}

func (s *Client) PutValueAndExpire(key string, value any, expireSeconds int) (clientv3.OpResponse, error) {
	_, leaseId := s.CreateLease(expireSeconds, false)
	return s.etcdCli.Do(s.Ctx(),
		clientv3.OpPut(key, utils.GetJsonString(value), clientv3.WithLease(leaseId)))
}

func (s *Client) PutValueAndKeepAlive(key string, value any, expireSeconds int) (clientv3.OpResponse, error) {
	_, id := s.CreateLease(10, true)
	return s.PutValueWithLease(key, value, id)
}

func (s *Client) PutValue(key string, value any) (clientv3.OpResponse, error) {
	return s.etcdCli.Do(s.Ctx(),
		clientv3.OpPut(key, utils.GetJsonString(value)))
}

func (s *Client) GetValue(key string) (*clientv3.GetResponse, error) {
	return s.etcdCli.Get(s.Ctx(), key)
}

func (s *Client) GetJsonValue(key string, value any) bool {
	if resp, err := s.etcdCli.Get(s.Ctx(), key); err == nil {
		if resp.Count > 0 {
			v := resp.Kvs[0].Value
			utils.GetJsonValue(value, string(v))
			return true
		}
	}
	return false
}

func (s *Client) GetArray(key string, arrayHandler func(s string)) bool {
	if resp, err := s.etcdCli.Get(s.Ctx(), key, clientv3.WithPrefix()); err == nil {
		if resp.Count > 0 {
			for i := int64(0); i < resp.Count; i++ {
				v := resp.Kvs[i].Value
				if arrayHandler != nil {
					arrayHandler(string(v))
				}
			}
			return true
		}
	}
	return false
}

func (s *Client) Delete(key string) (*clientv3.DeleteResponse, error) {
	return s.etcdCli.Delete(s.Ctx(), key)
}

func (s *Client) getNodeKey(terminalId string) string {
	return fmt.Sprintf(Key_NodeOnline, terminalId)
}

func (s *Client) getTunnelKey(tunnelId string) string {
	return fmt.Sprintf(Key_ClientTunnel, tunnelId)
}

func (s *Client) getClientKey(clientId string) string {
	return fmt.Sprintf(Key_ClientRecord, clientId)
}

func (s *Client) getOnlineClientKey(clientId string) string {
	return fmt.Sprintf(Key_ClientOnline, clientId)
}

func (s *Client) getDomainKey(clientId string, domain string) string {
	return fmt.Sprintf(Key_Domain, clientId, domain)
}

func (s *Client) RegisterNode(info *rpc.NodeInfo) {
	s.nodeInfo = info

	nodeKey := s.getNodeKey(info.TerminalId)
	online := &rpc.OnlineNode{
		NodeId:         info.TerminalId,
		ConnectionId:   info.ConnectionId,
		ConnectionTime: time.Now(),
		ServerAddr:     info.NodeAddr,
	}

	_, _ = s.PutValueAndKeepAlive(nodeKey, online, 10)
}

func (s *Client) lock(key string, ttl int) bool {
	_, leaseId := s.CreateLease(ttl, false)
	txn := clientv3.NewKV(s.etcdCli).Txn(s.Ctx())
	txn.If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
		Then(clientv3.OpPut(key, "", clientv3.WithLease(leaseId))).
		Else()
	txnResp, err := txn.Commit()
	if err != nil {
		log.Println(err)
		return false
	}
	return txnResp.Succeeded
}

func (s *Client) GenerateTunnelId() string {
	for {
		tmpInt := rand2.Int63n(99999999)
		if tmpInt < 10000000 {
			tmpInt = tmpInt + 10000000
		}
		tunnelId := fmt.Sprintf("%d", tmpInt)
		if s.lock(s.getTunnelKey(tunnelId), 5) {
			return tunnelId
		}
	}
}

func (s *Client) GetJwtSignKey() string {
	key := utils.RandStringWithLetterChar(32)
	txn := clientv3.NewKV(s.etcdCli).Txn(s.Ctx())
	txn.If(clientv3.Compare(clientv3.CreateRevision(Key_JwtSignKey), "=", 0)).
		Then(clientv3.OpPut(Key_JwtSignKey, key)).
		Else(clientv3.OpGet(Key_JwtSignKey))
	txnResp, _ := txn.Commit()
	if txnResp.Succeeded {
		return key
	} else {
		return string(txnResp.OpResponse().Txn().Responses[0].GetResponseRange().Kvs[0].Value)
	}
}

func (s *Client) GenerateNewClient() *rpc.ClientRec {
	cInfo := &rpc.ClientRec{
		ClientId:   uuid.New().String(),
		Token:      utils.RandStringWithFullChar(64),
		TunnelId:   s.GenerateTunnelId(),
		AuthCode:   utils.RandStringWithLetterChar(8),
		CreateTime: time.Now(),
	}
	tInfo := &rpc.Tunnel{
		TunnelId:   cInfo.TunnelId,
		TerminalId: cInfo.ClientId,
		AuthCode:   cInfo.AuthCode,
	}

	_, _ = s.PutValue(s.getClientKey(cInfo.ClientId), cInfo)
	_, _ = s.PutValue(s.getTunnelKey(cInfo.TunnelId), tInfo)
	return cInfo
}

func (s *Client) GetClientRec(clientId string) *rpc.ClientRec {
	clientKey := s.getClientKey(clientId)
	rec := &rpc.ClientRec{}
	if s.GetJsonValue(clientKey, rec) {
		return rec
	}
	return nil
}

func (s *Client) GetTunnel(tunnelId string) *rpc.Tunnel {
	tunnelKey := s.getTunnelKey(tunnelId)
	tunnel := &rpc.Tunnel{}
	if s.GetJsonValue(tunnelKey, tunnel) {
		return tunnel
	}
	return nil
}

func (s *Client) getListenKey(listen *rpc.Listen) string {
	return fmt.Sprintf(Key_ClientListen, listen.ListenClientId, listen.ListenAddr)
}

func (s *Client) GetOnlineClient(clientId string) *rpc.OnlineClient {
	clientKey := s.getOnlineClientKey(clientId)
	rec := &rpc.OnlineClient{}
	if s.GetJsonValue(clientKey, rec) {
		return rec
	}
	return nil
}

func (s *Client) DeleteOnlineClient(clientId string) {
	clientKey := s.getOnlineClientKey(clientId)
	_, _ = s.Delete(clientKey)
}

func (s *Client) PutOnlineClient(clientId string, client *rpc.OnlineClient) {
	clientKey := s.getOnlineClientKey(clientId)
	_, _ = s.PutValueAndKeepAlive(clientKey, client, 10)
}

func (s *Client) GetDomainList(clientId string) []*rpc.Domain {
	clientKey := s.getDomainKey(clientId, "")
	domainList := make([]*rpc.Domain, 0)
	s.GetArray(clientKey, func(jsonValue string) {
		domain := &rpc.Domain{}
		if utils.GetJsonValue(domain, jsonValue) {
			listen := &rpc.Listen{}
			if s.GetJsonValue(domain.ListenKey, listen) {
				domain.ListenInfo = listen
			}
			domainList = append(domainList, domain)
		}
	})
	return domainList
}

func (s *Client) AddListenWithTunnel(targetTunnelId, listenClientId, listenAddr, targetAddr string) {
	listen := &rpc.Listen{
		ListenClientId: listenClientId,
		TargetTunnelId: targetTunnelId,
		ListenAddr:     listenAddr,
		TargetAddr:     targetAddr,
		CreateTime:     time.Now(),
	}

	listenRec := s.GetClientRec(listenClientId)
	targetTunnel := s.GetTunnel(targetTunnelId)
	if listenRec != nil {
		listen.ListenTunnelId = listenRec.TunnelId
	}
	if targetTunnel != nil {
		listen.TargetClientId = targetTunnel.TerminalId
	}
	_, _ = s.PutValue(s.getListenKey(listen), listen)
}
