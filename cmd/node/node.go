package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/node"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)
	ctx := context.Background()
	sNode := node.NewNode(ctx)
	defer sNode.Close()

	go func() {
		_ = http.ListenAndServe("0.0.0.0:6080", nil)
	}()

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("node服务收到停止指令，服务将终止")
}
