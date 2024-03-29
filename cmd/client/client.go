package main

import (
	"context"
	"github.com/roger-tong-git/zhangyu/app/client"
	"github.com/roger-tong-git/zhangyu/utils"
	"log"
	//_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	defer utils.PrintError()
	utils.SetLogFile("client")
	ctx := context.Background()
	cli := client.NewClient(ctx)
	//go func() {
	//	_ = http.ListenAndServe("0.0.0.0:6060", nil)
	//}()

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	_ = cli.Close()
	time.Sleep(time.Millisecond * 200)
	log.Println("client服务收到停止指令，服务将终止")
}
