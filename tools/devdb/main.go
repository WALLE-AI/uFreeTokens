// Command devdb 在没有 Docker 的机器上启动一个真实的本地 PostgreSQL 实例
// （下载官方二进制、以子进程方式运行），用于本地开发与手工联调测试。
// 不用于生产环境；生产/CI 环境请用 deploy/docker-compose.yml 或托管数据库。
//
// 用法：
//
//	go run ./tools/devdb                 # 前台启动，Ctrl+C 停止并清理
//	go run ./tools/devdb -data ./.data/pg # 指定数据目录（默认 .data/pgdata）
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

func main() {
	var (
		port    = flag.Int("port", 5432, "监听端口")
		dataDir = flag.String("data", ".data/pgdata", "数据目录")
		user    = flag.String("user", "uft", "用户名")
		pass    = flag.String("pass", "uft", "密码")
		db      = flag.String("db", "uft", "数据库名")
	)
	flag.Parse()

	cfg := embeddedpostgres.DefaultConfig().
		Port(uint32(*port)).
		Username(*user).
		Password(*pass).
		Database(*db).
		DataPath(*dataDir).
		StartTimeout(90 * time.Second)

	pg := embeddedpostgres.NewDatabase(cfg)

	fmt.Printf("devdb: starting embedded postgres on :%d (data=%s) — 首次运行需要下载二进制，请耐心等待\n", *port, *dataDir)
	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "devdb: start failed:", err)
		os.Exit(1)
	}
	fmt.Printf("devdb: ready. dsn=postgres://%s:%s@localhost:%d/%s?sslmode=disable\n", *user, *pass, *port, *db)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	fmt.Println("devdb: stopping...")
	if err := pg.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "devdb: stop failed:", err)
		os.Exit(1)
	}
}
