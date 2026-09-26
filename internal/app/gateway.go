// Package app 组装各进程（gateway/admin/worker）的依赖与路由。
// 这是模块化单体的"装配层"：具体业务逻辑都在各自的 internal/<domain> 包里，
// 这里只做依赖注入和路由挂载，保持 internal/<domain> 之间不互相直接依赖数据库表。
package app

import (
	"log/slog"
	"net/http"
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
	Cfg       *config.Config
	Logger    *slog.Logger
	Metrics   *observability.Metrics
	PG        *pgxpool.Pool
	Redis     *redis.Client
	AuthStore auth.Store
	Pepper    []byte
	Relay     *relay.Service // nil 时 /v1/chat/completions 等 relay 端点返回 503 not_implemented
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

	r.Route("/v1", func(v1 chi.Router) {
		v1.Use(auth.APIKey(d.AuthStore, d.Pepper))

		v1.Get("/models", listModelsHandler(d.PG))
		if d.Relay != nil {
			v1.Post("/chat/completions", d.Relay.ChatCompletions)
			v1.Post("/embeddings", d.Relay.Embeddings)
		} else {
			v1.Post("/chat/completions", notImplementedHandler("chat.completions"))
			v1.Post("/embeddings", notImplementedHandler("embeddings"))
		}
		v1.Post("/completions", notImplementedHandler("completions"))
		v1.Post("/images/generations", notImplementedHandler("images.generations"))
		v1.Post("/audio/transcriptions", notImplementedHandler("audio.transcriptions"))
		v1.Post("/audio/speech", notImplementedHandler("audio.speech"))
	})

	return r
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
