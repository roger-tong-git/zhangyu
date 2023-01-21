package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/node"
	"golang.org/x/exp/rand"
	"log"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)
	rand.Intn(time.Now().Nanosecond())
	ctx := context.Background()
	sNode := node.NewNode(ctx, "127.0.0.1:2379", 18888)

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	_ = sNode.Close()
	time.Sleep(200 * time.Millisecond)
	log.Println("node服务收到停止指令，服务将终止")
}
