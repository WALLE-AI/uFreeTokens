// Package app 组装各进程（gateway/admin/worker）的依赖与路由。
// 这是模块化单体的"装配层"：具体业务逻辑都在各自的 internal/<domain> 包里，
// 这里只做依赖注入和路由挂载，保持 internal/<domain> 之间不互相直接依赖数据库表。
package app

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/console"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/rankings"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
	"github.com/WALLE-AI/uFreeTokens/internal/relay"
)

// GatewayDeps 是构造网关路由所需的全部依赖，由 cmd/gateway/main.go 装配后传入。
type GatewayDeps struct {
	Cfg       *config.Config
	Logger    *slog.Logger
	Metrics   *observability.Metrics
	PG        *pgxpool.Pool
	Redis     *redis.Client
	AuthStore auth.Store
	Pepper    []byte
	Relay     *relay.Service   // nil 时 /v1/chat/completions 等 relay 端点返回 503 not_implemented
	Console   *console.Service // nil 时不挂载 /console/*（技术方案迭代3：Console 接口挂在 gateway 进程）
	// Catalog 为 nil 时 GET /v1/catalog 返回 503 not_implemented（技术方案
	// 迭代5：公开模型目录）。和 Relay 共用同一个 catalog.Store 实例——
	// 二者本来就该看到同一份配置快照，没必要各自维护一份。
	Catalog *catalog.Store
	// RateLimit 目前只给 GET /v1/catalog 的按 IP 限流用；nil 时跳过限流。
	RateLimit  *ratelimit.Limiter
	TestWebDir string // 非空时在根路径同源提供 test_web/user.html（手工联调用，见 staticweb.go）；空字符串（默认）不开启
}

// NewGatewayRouter 组装数据面路由。/v1/chat/completions、/v1/embeddings 已接入
// 完整的路由 → 预扣 → 转发（含换 Key/换渠道重试与全局重试预算）→ 结算链路
// （relay.Service，见技术方案 §3.2、§7.1、§7.7）；两者共用同一套鉴权/限流/
// 重试/结算管线，区别只在没有流式、没有工具调用、没有输出 token（embeddings
// 用量只有 input）。/v1/completions（旧式补全接口，多数上游已经不推荐）、
// /v1/images、/v1/audio 尚未实现，返回 503/not_implemented。
func NewGatewayRouter(d GatewayDeps) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestID)
	r.Use(httpx.Recover(d.Logger))
	r.Use(httpx.AccessLog(d.Logger))

	r.Get("/healthz", healthzHandler)
	r.Get("/readyz", readyzHandler(d.PG, d.Redis))
	// 根路径给手工联调用的测试页面用，同 internal/app/admin.go 的
	// NewAdminRouter；根路径本来就在 auth.APIKey 中间件挂载的 /v1 分组之外，
	// 不需要额外处理鉴权。
	r.Get("/", serveStaticHTML(d.TestWebDir, "user.html"))

	r.Route("/v1", func(v1 chi.Router) {
		// CORS 只挂在 /v1（BYOK 浏览器客户端跨域调用），且必须在 auth.APIKey
		// 之前，否则不带 Authorization 的预检 OPTIONS 会被鉴权中间件拦成 401
		// （见 httpx.CORS 的包注释）。/console 的 Cookie 会话只走同源，不挂这个。
		if origins := splitCommaList(d.Cfg); len(origins) > 0 {
			v1.Use(httpx.CORS(origins))
		}

		// /v1/catalog 是公开模型目录（技术方案迭代5），故意不挂 auth.APIKey——
		// 免鉴权是它存在的意义（给未注册的访客展示模型库）；按 IP 限流 +
		// Cache-Control 防止被爬虫打爆，见 catalogHandler 的注释。
		if d.Catalog != nil {
			v1.Get("/catalog", catalogHandler(d.Catalog, d.RateLimit))
		} else {
			v1.Get("/catalog", notImplementedHandler("catalog"))
		}

		// 公开排行榜与基准测试（免鉴权，同 /v1/catalog），见 public.go。
		pub := newPublicHandlers(d)
		v1.Get("/rankings/models", pub.rankingsModels)
		v1.Get("/rankings/authors", pub.rankingsAuthors)
		v1.Get("/rankings/speed", pub.rankingsByLimit("speed", func(ctx context.Context, s *rankings.Service, p rankings.Period, n int) (any, error) {
			return s.Speed(ctx, p, n)
		}))
		v1.Get("/rankings/tools", pub.rankingsByLimit("tools", func(ctx context.Context, s *rankings.Service, p rankings.Period, n int) (any, error) {
			return s.Tools(ctx, p, n)
		}))
		v1.Get("/rankings/multimodal", pub.rankingsByLimit("multimodal", func(ctx context.Context, s *rankings.Service, p rankings.Period, n int) (any, error) {
			return s.Multimodal(ctx, p, n)
		}))
		v1.Get("/rankings/apps", pub.rankingsByLimit("apps", func(ctx context.Context, s *rankings.Service, p rankings.Period, n int) (any, error) {
			return s.Apps(ctx, p, n)
		}))
		v1.Get("/benchmarks", pub.listBenchmarks)
		v1.Get("/benchmarks/{slug}", pub.getBenchmark)
		v1.Get("/model-benchmarks", pub.modelBenchmarks)

		v1.Group(func(authed chi.Router) {
			authed.Use(auth.APIKey(d.AuthStore, d.Pepper))

			authed.Get("/models", listModelsHandler(d.PG))
			authed.Get("/usage", usageHandler(d.PG))
			disabled := disabledEndpoints(d.Cfg)
			relayRoute := func(path, name, group string, h func(http.ResponseWriter, *http.Request)) {
				if d.Relay == nil || disabled[group] {
					authed.Post(path, notImplementedHandler(name))
					return
				}
				authed.Post(path, h)
			}
			rs := d.Relay
			relayRoute("/chat/completions", "chat.completions", "chat", relayHandler(rs, (*relay.Service).ChatCompletions))
			relayRoute("/embeddings", "embeddings", "embeddings", relayHandler(rs, (*relay.Service).Embeddings))
			relayRoute("/messages", "messages", "messages", relayHandler(rs, (*relay.Service).Messages))
			relayRoute("/rerank", "rerank", "rerank", relayHandler(rs, (*relay.Service).Rerank))
			relayRoute("/images/generations", "images.generations", "images", relayHandler(rs, (*relay.Service).ImagesGenerations))
			relayRoute("/audio/speech", "audio.speech", "audio", relayHandler(rs, (*relay.Service).AudioSpeech))
			relayRoute("/audio/transcriptions", "audio.transcriptions", "audio", relayHandler(rs, (*relay.Service).AudioTranscriptions))
			authed.Post("/completions", notImplementedHandler("completions"))
		})
	})

	// /console/* 完全不挂 CORS（httpOnly Cookie 会话只信任同源请求，见
	// internal/console 包文档），也不挂 auth.APIKey——鉴权走 Cookie Session
	// （console.RequireSession），和 /v1 的 API Key 鉴权是两套完全独立的机制。
	if d.Console != nil {
		r.Route("/console", func(c chi.Router) {
			c.Post("/register", d.Console.HandleRegister)
			c.Post("/login", d.Console.HandleLogin)
			c.Post("/logout", d.Console.HandleLogout)

			c.Group(func(authed chi.Router) {
				authed.Use(console.RequireSession(d.Console.Sessions()))
				authed.Get("/me", d.Console.HandleMe)
				authed.Get("/api-keys", d.Console.HandleListKeys)
				authed.Get("/wallet", d.Console.HandleWallet)
				authed.Get("/usage", d.Console.HandleUsageInterval)
				authed.Get("/logs", d.Console.HandleLogs)

				authed.Group(func(mutating chi.Router) {
					mutating.Use(console.CSRFGuard)
					mutating.Post("/api-keys", d.Console.HandleCreateKey)
					mutating.Post("/api-keys/{id}/revoke", d.Console.HandleRevokeKey)
					mutating.Put("/settings/public-stats", d.Console.HandleSetPublicStats)
				})
			})
		})
	}

	return r
}

// splitCommaList 把 gateway.cors_origins 的逗号分隔字符串拆成 Origin 列表，
// 修剪空白、丢弃空项；cfg 为 nil（测试里直接构造 GatewayDeps 不带 Cfg 的场景）
// 或字段为空都返回 nil，调用方据此判断"不启用 CORS"。
// disabledEndpoints 解析 relay.disabled_endpoints（逗号分隔）。只认多模态端点组
// rerank / images / audio——对话与向量是核心链路，不提供配置下线。
func disabledEndpoints(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	if cfg == nil {
		return out
	}
	for _, p := range strings.Split(cfg.Relay.DisabledEndpoints, ",") {
		switch g := strings.TrimSpace(strings.ToLower(p)); g {
		case "rerank", "images", "audio":
			out[g] = true
		}
	}
	return out
}

// relayHandler 把 relay.Service 的方法表达式绑定到实例上。
func relayHandler(rs *relay.Service, m func(*relay.Service, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { m(rs, w, r) }
}

func splitCommaList(cfg *config.Config) []string {
	if cfg == nil || cfg.Gateway.CORSOrigins == "" {
		return nil
	}
	parts := strings.Split(cfg.Gateway.CORSOrigins, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if o := strings.TrimSpace(p); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

func notImplementedHandler(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented",
			"The "+endpoint+" relay pipeline is not yet implemented (Phase1 in progress). See docs roadmap.")
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	// 存活探针：不检查依赖，只证明进程在处理请求。
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func readyzHandler(pg *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := timeoutCtx(r, 2*time.Second)
		defer cancel()

		result := map[string]string{"postgres": "ok", "redis": "ok"}
		ready := true

		if err := pg.Ping(ctx); err != nil {
			result["postgres"] = "error: " + err.Error()
			ready = false
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			result["redis"] = "error: " + err.Error()
			ready = false
		}

		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		httpx.WriteJSON(w, status, result)
	}
}
