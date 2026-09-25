// Command admin 是控制面入口：账户/API Key/Provider/渠道/虚拟模型/价格管理
// （技术方案 §7 相关章节）。用户控制台、支付回调、促销管理尚未实现。
//
// 见 internal/app.NewAdminRouter 和 internal/admin 包文档：目前完全没有
// 鉴权/权限控制，只应该部署在内网。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "admin: fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", os.Getenv("UFT_CONFIG"), "path to config YAML (optional)")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := observability.NewLogger(cfg.Log)
	logger.Info("starting admin")

	pepper := []byte(os.Getenv(cfg.Secrets.APIKeyPepperEnv))
	if len(pepper) == 0 {
		return fmt.Errorf("env %s is required (API key HMAC pepper)", cfg.Secrets.APIKeyPepperEnv)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pg, err := store.NewPostgres(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pg.Close()

	// KEK 缺失时不阻止启动：账户/Key/渠道管理仍然可用，只是 AddProviderKey
	// 会拒绝请求（internal/admin.Service.AddProviderKey 在 box 为 nil 时报错），
	// 便于在还没配好 KMS 的环境里先跑通其它管理功能。
	var box *secretbox.Box
	if kekB64 := os.Getenv(cfg.Secrets.KEKEnv); kekB64 != "" {
		box, err = secretbox.NewBox(kekB64)
		if err != nil {
			return fmt.Errorf("build secretbox: %w", err)
		}
	} else {
		logger.Warn("no KEK configured, AddProviderKey will be unavailable", "env", cfg.Secrets.KEKEnv)
	}

	walletSvc := wallet.New(pg)
	adminSvc := admin.New(pg, walletSvc, box, pepper)

	router := app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc})

	addr := ":8081"
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("admin listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		logger.Error("server error", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
