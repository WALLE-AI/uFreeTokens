// Command gateway 是数据面入口：对外提供 OpenAI 兼容的 /v1/* API。
// 见技术方案 §3.1、§5。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/health"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/relay"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// catalogTTL 是 catalog.Store 内存快照的刷新周期。技术方案 §7.3 设计的是
// LISTEN/NOTIFY 驱动的即时热加载，这里先用一个短 TTL 兜底，改配置后最多
// 延迟这么久生效——本阶段的已知简化，见 internal/catalog 包注释。
const catalogTTL = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gateway: fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", os.Getenv("UFT_CONFIG"), "path to gateway config YAML (optional)")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := observability.NewLogger(cfg.Log)
	logger.Info("starting gateway", "config_path", configPath)

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

	rdb, err := store.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer rdb.Close()

	metrics := observability.NewMetrics(prometheus.DefaultRegisterer)
	authStore := auth.NewPostgresStore(pg)

	relaySvc, err := buildRelayService(pg, rdb, cfg, logger)
	if err != nil {
		// KEK 缺失/格式错误时不应该让整个网关起不来——没有它只是意味着
		// /v1/chat/completions 继续返回 503（未实现），其余端点仍然可用，
		// 便于在还没配好上游 Key 的环境里先跑通鉴权/计费之外的部分。
		logger.Warn("relay service unavailable, /v1/chat/completions will return 503", "error", err)
	}

	router := app.NewGatewayRouter(app.GatewayDeps{
		Cfg:       cfg,
		Logger:    logger,
		Metrics:   metrics,
		PG:        pg,
		Redis:     rdb,
		AuthStore: authStore,
		Pepper:    pepper,
		Relay:     relaySvc,
	})

	srv := &http.Server{
		Addr:              cfg.Gateway.Addr,
		Handler:           http.MaxBytesHandler(router, cfg.Gateway.MaxBodyBytes),
		ReadHeaderTimeout: cfg.Gateway.ReadHeaderTimeout,
		ReadTimeout:       cfg.Gateway.ReadTimeout,
		// WriteTimeout 故意不设置：流式响应（SSE）没有固定上限，
		// 空闲超时改由 relay 层的 stream_idle_timeout 负责（见技术方案 §7.1、§7.8）。
		IdleTimeout: cfg.Gateway.IdleTimeout,
	}

	metricsSrv := &http.Server{
		Addr:    cfg.Metrics.Addr,
		Handler: observability.Handler(),
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("gateway listening", "addr", cfg.Gateway.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("gateway server: %w", err)
		}
	}()
	go func() {
		logger.Info("metrics listening", "addr", cfg.Metrics.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("metrics server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		logger.Error("server error, shutting down", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Gateway.ShutdownGrace)
	defer cancel()

	// TODO(Phase1 后续): 优雅退出时应等待在途流式请求完成并结算（§7.8），
	// 当前是标准的 HTTP 层优雅关闭，Shutdown 会等待已建立的连接完成当前请求，
	// 但没有对"在途流式响应"做特别处理/上限告警。
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("gateway shutdown error", "error", err)
	}
	_ = metricsSrv.Shutdown(shutdownCtx)

	if relaySvc != nil {
		relaySvc.ReqLog.Close() // flush 掉还在队列里的 request_logs 记录
	}

	time.Sleep(100 * time.Millisecond) // 留出日志 flush 的余量
	return nil
}

// buildRelayService 组装 relay.Service 所需的全部依赖：解密上游 Key 的信封加密盒、
// 配置快照 Store、钱包引擎、协议适配器注册表、健康度/熔断注册表、面向上游的 HTTP 客户端。
func buildRelayService(pg *pgxpool.Pool, rdb *redis.Client, cfg *config.Config, logger *slog.Logger) (*relay.Service, error) {
	kekB64 := os.Getenv(cfg.Secrets.KEKEnv)
	if kekB64 == "" {
		return nil, fmt.Errorf("env %s is required (upstream key envelope encryption KEK)", cfg.Secrets.KEKEnv)
	}
	box, err := secretbox.NewBox(kekB64)
	if err != nil {
		return nil, fmt.Errorf("build secretbox: %w", err)
	}

	catalogStore := catalog.NewStore(pg, box, catalogTTL)
	walletSvc := wallet.New(pg)
	registry := adapter.NewRegistry()
	// Key 冷却经 Redis 跨实例共享；渠道熔断器是进程内的（各实例独立统计），见 internal/health。
	healthRegistry := health.NewRegistry(rdb, health.DefaultBreakerSettings())
	reqLogWriter := reqlog.NewWriter(pg, logger)

	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   64,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second, // 等首字节的上限；流式响应体本身不受此约束
			ForceAttemptHTTP2:     true,
		},
		// 不设置 Client.Timeout：流式响应可能持续数十秒到数分钟，
		// 全局超时会在正常长流上误杀。总耗时上限留给 §7.7 的重试预算/总 deadline 实现。
	}

	return &relay.Service{
		Catalog:  catalogStore,
		Wallet:   walletSvc,
		Adapters: registry,
		HTTP:     httpClient,
		Health:   healthRegistry,
		ReqLog:   reqLogWriter,
		Logger:   logger,
		Cfg:      relay.DefaultConfig(),
	}, nil
}
