// Command admin 是控制面入口：账户/API Key/Provider/渠道/虚拟模型/价格管理
// （技术方案 §7 相关章节）。用户控制台、支付回调、促销管理尚未实现。
//
// 见 internal/app.NewAdminRouter 和 internal/admin 包文档：鉴权目前只到
// "共享密钥"这一级，不是完整的多用户登录 + RBAC，只应该部署在内网/加一层
// 反向代理。
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
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
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

	// 管理接口的鉴权密钥是必需的，不像 KEK 那样缺失时降级——这组接口能创建账户、
	// 调余额、加上游 Key、改价格，缺鉴权直接暴露等于把金库门打开，不应该允许
	// 静默跳过（httpx.RequireBearerToken 对空字符串也会拒绝所有请求，这里提前
	// 报错只是为了给一个更清楚的启动期错误信息，而不是让运维靠"发现全都 401"
	// 才意识到没配置）。
	adminToken := os.Getenv(cfg.Secrets.AdminTokenEnv)
	if adminToken == "" {
		return fmt.Errorf("env %s is required (admin API bearer token)", cfg.Secrets.AdminTokenEnv)
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
	priceSyncEngine := pricesync.NewEngine(pg, adminSvc)

	// UFT_TEST_WEB_DIR 是手工联调用的开关（见 internal/app/staticweb.go）：
	// 留空（默认）不开启，不接入 internal/config 的分层配置——这是本地调试
	// 用的旁路开关，不是需要区分 dev/staging/prod 的正式参数。
	testWebDir := os.Getenv("UFT_TEST_WEB_DIR")

	router := app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, PriceSync: priceSyncEngine, AdminToken: adminToken, TestWebDir: testWebDir})

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
