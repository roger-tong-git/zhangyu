package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/front"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx := context.Background()
	sFront := front.NewFront(ctx)
	defer sFront.Close()

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("front服务收到停止指令，服务将终止")
}
