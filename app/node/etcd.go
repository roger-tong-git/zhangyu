package node

import (
	"context"
	"fmt"
	"github.com/roger-tong-git/zhangyu/utils"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"log"
	"strings"
	"time"
)

const (
	EtcdKey_Node                  = "/zhangyu/nodes"
	EtcdKey_Client_Connection     = "/zhangyu/clients/connections"
	EtcdKey_Client_Record         = "/zhangyu/clients/records"
	EtcdKey_Client_MapItem        = "/zhangyu/clients/records/%v/transferMaps/[%v]"
	EtcdKey_Client_TransferPrefix = "/zhangyu/clients/records/%v/transferMaps/"
	EtcdKey_HttpDomain_Bind       = "/zhangyu/domain/binds"

	// EtcdKey_Transfer_Local_TargetIn 目标流量连接进入到node时，etcd中会收到这个通知
	EtcdKey_Transfer_Local_TargetIn = "/zhangyu/transfer/local/%v/target/in/"

	// EtcdKey_Transfer_Local_ListenIn 监听流量连接进入到Node时，etcd中会收到这个通知
	EtcdKey_Transfer_Local_ListenIn = "/zhangyu/transfer/local/%v/listen/in/"
)

func GetEtcdCommandPrefix(etcdKey string, terminalId string) string {
	return fmt.Sprintf(etcdKey, terminalId)
}

func GetEtcdCommand(etcdKey string, terminalId string, connId string) string {
	return GetEtcdCommandPrefix(etcdKey, terminalId) + connId
}

func GetEtcdCommandConnId(command string, etcdKey string, terminalId string) string {
	return strings.TrimPrefix(command, GetEtcdCommandPrefix(etcdKey, terminalId))
}

type EtcdOp struct {
	EtcdUri string //Etcd连接字符串
	etcdCli *clientv3.Client
	utils.Closer
}

type EtcdMutex struct {
	Ttl     int64  //租约时间
	Key     string //etcd的key
	lease   clientv3.Lease
	leaseID clientv3.LeaseID
	client  *clientv3.Client
	txn     clientv3.Txn
	utils.Closer
}

func (em *EtcdMutex) UnLock() {
	_, _ = em.lease.Revoke(em.Ctx(), em.leaseID)
	_, _ = em.client.Delete(em.Ctx(), em.Key)
}

func (em *EtcdMutex) Lock() bool {
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

func NewEtcdMutex(ctx context.Context, client *clientv3.Client, key string, ttl int64) (*EtcdMutex, error) {
	var err error
	em := &EtcdMutex{client: client, Key: key, Ttl: ttl}
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

func NewEtcdOp(ctx context.Context, etcdUri string) *EtcdOp {
	op := &EtcdOp{
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

func (s *EtcdOp) onClose() {
	if s.etcdCli != nil {
		_ = s.etcdCli.Close()
		log.Println("ETCD连接已关闭")
	}
}

func (s *EtcdOp) AddWatcher(prefix string, watcher func(eventType string, key string, value []byte)) {
	go func() {
		watchChan := s.etcdCli.Watch(s.Ctx(), prefix, clientv3.WithPrefix())
		for watch := range watchChan {
			for _, event := range watch.Events {
				switch event.Type {
				case mvccpb.DELETE:
					watcher("DELETE", string(event.Kv.Key), event.Kv.Value)
					break
				case mvccpb.PUT:
					watcher("PUT", string(event.Kv.Key), event.Kv.Value)
				}
			}
		}
	}()
}

func (s *EtcdOp) CreateLease(seconds int, keepalive bool) (clientv3.Lease, clientv3.LeaseID) {
	ls := clientv3.NewLease(s.etcdCli)
	resp, _ := ls.Grant(s.Ctx(), int64(seconds))
	if keepalive {
		_, _ = ls.KeepAlive(s.Ctx(), resp.ID)
	}
	return ls, resp.ID
}

func (s *EtcdOp) RenewLease(lease clientv3.Lease, seconds int) error {
	resp, _ := lease.Grant(s.Ctx(), int64(seconds))
	if _, err := lease.KeepAliveOnce(s.Ctx(), resp.ID); err != nil {
		return err
	}
	return nil
}

func (s *EtcdOp) PutValueWithLease(key string, value any, leaseId clientv3.LeaseID) (clientv3.OpResponse, error) {
	return s.etcdCli.Do(s.Ctx(),
		clientv3.OpPut(key, utils.GetJsonString(value), clientv3.WithLease(leaseId)))
}

func (s *EtcdOp) PutValueAndExpire(key string, value any, expireSeconds int) (clientv3.OpResponse, error) {
	_, leaseId := s.CreateLease(expireSeconds, false)
	return s.etcdCli.Do(s.Ctx(),
		clientv3.OpPut(key, utils.GetJsonString(value), clientv3.WithLease(leaseId)))
}

func (s *EtcdOp) PutValue(key string, value any) (clientv3.OpResponse, error) {
	return s.etcdCli.Do(s.Ctx(),
		clientv3.OpPut(key, utils.GetJsonString(value)))
}

func (s *EtcdOp) GetValue(key string) (*clientv3.GetResponse, error) {
	return s.etcdCli.Get(s.Ctx(), key)
}

func (s *EtcdOp) GetJsonValue(key string, value any) bool {
	if resp, err := s.etcdCli.Get(s.Ctx(), key); err == nil {
		if resp.Count > 0 {
			v := resp.Kvs[0].Value
			utils.GetJsonValue(value, string(v))
			return true
		}
	}
	return false
}

func (s *EtcdOp) GetArray(key string, arrayHandler func(s string)) bool {
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

func (s *EtcdOp) Delete(key string) (*clientv3.DeleteResponse, error) {
	return s.etcdCli.Delete(s.Ctx(), key)
}

func (s *EtcdOp) GetNodeKey(terminalId string) string {
	return fmt.Sprintf("%v/%v", EtcdKey_Node, terminalId)
}
