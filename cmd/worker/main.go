// Command worker 是异步任务入口：日志落库、用量聚合、对账、冻结回收、
// 健康探测、价格同步（§7.16）等。当前仅有进程骨架与依赖连通性检查；
// 具体任务循环见技术方案路线图，待后续迭代补齐。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "worker: fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv("UFT_CONFIG"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := observability.NewLogger(cfg.Log)
	logger.Info("starting worker")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pg, err := store.NewPostgres(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pg.Close()

	rdb, err := store.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer rdb.Close()

	logger.Info("worker dependencies connected; task loops not yet implemented",
		"todo", "reservation reclaim, reconcile, price sync (§7.16), request_logs partitioner")

	<-ctx.Done()
	logger.Info("worker shutting down")
	return nil
}
