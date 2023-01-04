package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/node"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)
	ctx := context.Background()
	sNode := node.NewNode(ctx)
	defer sNode.Close()

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("node服务收到停止指令，服务将终止")
}
