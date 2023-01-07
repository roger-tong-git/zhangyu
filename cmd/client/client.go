package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/client"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx := context.Background()
	sClient := client.NewClient(ctx)
	defer sClient.Close()

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("client服务收到停止指令，服务将终止")
}
