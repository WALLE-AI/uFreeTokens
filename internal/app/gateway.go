// Package app 组装各进程（gateway/admin/worker）的依赖与路由。
// 这是模块化单体的"装配层"：具体业务逻辑都在各自的 internal/<domain> 包里，
// 这里只做依赖注入和路由挂载，保持 internal/<domain> 之间不互相直接依赖数据库表。
package app

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/relay"
)

// GatewayDeps 是构造网关路由所需的全部依赖，由 cmd/gateway/main.go 装配后传入。
type GatewayDeps struct {
	Cfg        *config.Config
	Logger     *slog.Logger
	Metrics    *observability.Metrics
	PG         *pgxpool.Pool
	Redis      *redis.Client
	AuthStore  auth.Store
	Pepper     []byte
	Relay      *relay.Service // nil 时 /v1/chat/completions 等 relay 端点返回 503 not_implemented
	TestWebDir string         // 非空时在根路径同源提供 test_web/user.html（手工联调用，见 staticweb.go）；空字符串（默认）不开启
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
		v1.Use(auth.APIKey(d.AuthStore, d.Pepper))

		v1.Get("/models", listModelsHandler(d.PG))
		v1.Get("/usage", usageHandler(d.PG))
		if d.Relay != nil {
			v1.Post("/chat/completions", d.Relay.ChatCompletions)
			v1.Post("/embeddings", d.Relay.Embeddings)
			v1.Post("/messages", d.Relay.Messages)
		} else {
			v1.Post("/chat/completions", notImplementedHandler("chat.completions"))
			v1.Post("/embeddings", notImplementedHandler("embeddings"))
			v1.Post("/messages", notImplementedHandler("messages"))
		}
		v1.Post("/completions", notImplementedHandler("completions"))
		v1.Post("/images/generations", notImplementedHandler("images.generations"))
		v1.Post("/audio/transcriptions", notImplementedHandler("audio.transcriptions"))
		v1.Post("/audio/speech", notImplementedHandler("audio.speech"))
	})

	return r
}

// splitCommaList 把 gateway.cors_origins 的逗号分隔字符串拆成 Origin 列表，
// 修剪空白、丢弃空项；cfg 为 nil（测试里直接构造 GatewayDeps 不带 Cfg 的场景）
// 或字段为空都返回 nil，调用方据此判断"不启用 CORS"。
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
