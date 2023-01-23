package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/client"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx := context.Background()
	cli := client.NewClient(ctx, "127.0.0.1:18888")

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	_ = cli.Close()
	time.Sleep(time.Millisecond * 200)
	log.Println("client服务收到停止指令，服务将终止")
}
