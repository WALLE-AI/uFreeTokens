// Command admin 是控制面入口：账户/API Key/Provider/渠道/虚拟模型/价格管理
// （技术方案 §7 相关章节）。用户控制台、支付回调、促销管理尚未实现。
//
// 鉴权：管理员用 POST /auth/login 登录拿会话令牌，按角色权限访问接口（见
// internal/adminauth、internal/app/admin_routes.go）。UFT_ADMIN_TOKEN（配置项
// secrets.admin_token_env）是可选的应急共享令牌，身份为 system，生产环境建议
// 在建好管理员账号后关闭。
//
// 子命令：
//
//	admin create-admin -email a@b.com -name 张三 -role super_admin   # 从环境变量 UFT_ADMIN_PASSWORD 读密码
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/jobs"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/llm"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "create-admin" {
		if err := createAdmin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "admin create-admin:", err)
			os.Exit(1)
		}
		return
	}
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

	// 应急共享令牌是可选的：为空时只能用管理员账号登录。设置了就打 WARN，
	// 提醒运维它拥有全部权限、审计里只记为 system。
	adminToken := os.Getenv(cfg.Secrets.AdminTokenEnv)
	if adminToken != "" && len(adminToken) < 24 {
		return fmt.Errorf("env %s is too short (need >= 24 chars for the break-glass token)", cfg.Secrets.AdminTokenEnv)
	}
	trustedProxies, err := parsePrefixes(os.Getenv("UFT_ADMIN_TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("parse UFT_ADMIN_TRUSTED_PROXIES: %w", err)
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
	// 上游地址策略：生产必须 https + 禁止内网地址；本地联调（上游是本机 mock）
	// 可以用 UFT_ADMIN_ALLOW_PRIVATE_UPSTREAM=true 放开，启动时打 WARN。
	if os.Getenv("UFT_ADMIN_ALLOW_PRIVATE_UPSTREAM") == "true" {
		logger.Warn("upstream URL policy relaxed: http and private networks allowed (dev only)")
		adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	}
	// 展示元数据「AI 生成介绍」用的 LLM（可选，与 worker 的优惠抽取共用配置段 datasync.llm_*）。
	if c, missing := llm.FromConfig(cfg.DataSync); c != nil {
		adminSvc.SetDescriptionGenerator(admin.NewLLMDescriptionGenerator(c))
		logger.Info("metadata AI description enabled", "llm_model", c.Model)
	} else {
		logger.Info("metadata AI description disabled: LLM not configured", "missing", missing)
	}
	priceSyncEngine := pricesync.NewEngine(pg, adminSvc)
	authSvc := adminauth.New(pg, adminauth.Config{Box: box})
	if adminToken != "" {
		logger.Warn("break-glass admin token is enabled; it has full permissions and is audited as 'system'", "env", cfg.Secrets.AdminTokenEnv)
	}
	go purgeLoop(ctx, authSvc, adminSvc, logger)

	// UFT_TEST_WEB_DIR 是手工联调用的开关（见 internal/app/staticweb.go）：
	// 留空（默认）不开启，不接入 internal/config 的分层配置——这是本地调试
	// 用的旁路开关，不是需要区分 dev/staging/prod 的正式参数。
	testWebDir := os.Getenv("UFT_TEST_WEB_DIR")

	// Redis 是可选依赖：用来读网关的熔断/冷却状态、吊销 Key 时清冷却。连不上时降级
	// （健康页的运行时状态显示为未知），不阻止后台启动。
	rdb, err := store.NewRedis(ctx, cfg.Redis)
	if err != nil {
		logger.Warn("redis unavailable; channel health runtime state and cooldown clearing disabled", "error", err)
		rdb = nil
	} else {
		defer rdb.Close()
	}

	statsTZ, err := time.LoadLocation(envOr("UFT_ADMIN_STATS_TZ", "Asia/Shanghai"))
	if err != nil {
		return fmt.Errorf("load UFT_ADMIN_STATS_TZ: %w", err)
	}
	requireIfMatch := os.Getenv("UFT_ADMIN_REQUIRE_IF_MATCH") == "true"

	// 出站抓取环境（禁内网、按主机限速）：数据源试运行、优惠抽取预览与智能体 fetch_page 共用。
	dsEnv := datasync.NewEnv(datasync.EnvOptions{Timeout: 45 * time.Second})
	var offerLLM offers.LLM
	if c, _ := llm.FromConfig(cfg.DataSync); c != nil {
		offerLLM = offers.NewLLM(c)
	}
	deps := app.AdminDeps{
		Logger: logger, Admin: adminSvc, PriceSync: priceSyncEngine, Offers: offers.NewStore(pg), Auth: authSvc,
		AdminToken: adminToken, TrustedProxies: trustedProxies, TestWebDir: testWebDir,
		Redis: rdb, StatsTZ: statsTZ, RequireIfMatch: requireIfMatch, PublicMinAccounts: cfg.Public.RankingsMinAccounts,
		DataSyncEnv: dsEnv, OfferLLM: offerLLM,
	}
	// 运营智能体（Harness）：未启用或 LLM 未配置时 /agent/meta 返回 enabled=false，其余 /agent/* 返回 503。
	agentSvc, err := app.NewAgentService(app.AgentSetup{
		Config: *cfg, Pool: pg, Admin: adminSvc, Deps: deps, Fetch: dsEnv, Logger: logger, Location: statsTZ,
	})
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	if agentSvc.Enabled() {
		logger.Info("operations agent enabled", "model", agentSvc.Cfg.Model, "tools", len(agentSvc.Tools))
	} else {
		logger.Info("operations agent disabled", "missing", agentSvc.Missing)
	}
	deps.Agent, deps.AgentInfo = agentSvc, app.AgentInfo{JobsEnabled: cfg.Agent.JobsEnabled}
	deps.AgentJobs = &jobs.Store{Pool: pg}
	router := app.NewAdminRouter(deps)

	// UFT_ADMIN_METRICS_ADDR 非空时在该地址暴露 /metrics（agent_* 指标）。
	if maddr := os.Getenv("UFT_ADMIN_METRICS_ADDR"); maddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", observability.Handler())
		go func() {
			msrv := &http.Server{Addr: maddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			if err := msrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("admin metrics server failed", "error", err)
			}
		}()
	}

	addr := ":8081"
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 参考价格查询、上游模型发现会同步请求外部服务，留足余量。智能体的 SSE 接口按请求解除写超时
		// （http.ResponseController.SetWriteDeadline）。
		WriteTimeout:   90 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 64 << 10,
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

// purgeLoop 每小时清理一次过期的管理员会话与 Idempotency-Key 记录。
func purgeLoop(ctx context.Context, svc *adminauth.Service, adminSvc *admin.Service, logger *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := svc.PurgeExpiredSessions(ctx); err != nil {
				logger.Warn("purge admin sessions failed", "error", err)
			} else if n > 0 {
				logger.Info("purged expired admin sessions", "count", n)
			}
			if n, err := adminSvc.PurgeIdempotencyKeys(ctx); err != nil {
				logger.Warn("purge idempotency keys failed", "error", err)
			} else if n > 0 {
				logger.Info("purged expired idempotency keys", "count", n)
			}
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parsePrefixes 解析逗号分隔的 CIDR 或单个 IP 列表。
func parsePrefixes(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// createAdmin 创建一个管理员（初始化第一个 super_admin 用）。密码从环境变量
// UFT_ADMIN_PASSWORD 读，避免出现在 shell 历史和进程列表里。
func createAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	configPath := fs.String("config", os.Getenv("UFT_CONFIG"), "path to config YAML (optional)")
	email := fs.String("email", "", "admin email (required)")
	name := fs.String("name", "", "display name (required)")
	role := fs.String("role", "super_admin", "comma-separated roles: super_admin,operator,pricing,finance,support")
	if err := fs.Parse(args); err != nil {
		return err
	}
	password := os.Getenv("UFT_ADMIN_PASSWORD")
	if *email == "" || *name == "" || password == "" {
		return errors.New("-email, -name and env UFT_ADMIN_PASSWORD are required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := context.Background()
	pg, err := store.NewPostgres(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pg.Close()
	u, err := adminauth.New(pg, adminauth.Config{}).CreateAdmin(ctx, adminauth.CreateAdminInput{
		Email: *email, Name: *name, Password: password, Roles: strings.Split(*role, ","),
	})
	if err != nil {
		return err
	}
	fmt.Printf("created admin #%d %s <%s> roles=%v\n", u.ID, u.Name, u.Email, u.Roles)
	return nil
}
