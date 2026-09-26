// Package httpx 提供网关/控制面共用的 HTTP 中间件与统一响应格式。
package httpx

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID 生成/透传请求 ID（ULID，时间有序），贯穿 reservation、ledger、request_log
// （见技术方案 §7.9.3）。若客户端已带 X-Request-Id，则复用（便于客户端侧关联排障）。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = ulid.Make().String()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext 取出当前请求的 request_id；不存在时返回空字符串。
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Recover 捕获 panic，返回 500，并记录堆栈（不包含请求体，避免泄漏敏感信息）。
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", rec),
						slog.String("request_id", RequestIDFromContext(r.Context())),
						slog.String("path", r.URL.Path),
					)
					WriteError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequireBearerToken 是一个共享密钥鉴权中间件：要求 Authorization: Bearer <token>
// 与 token 完全匹配（crypto/subtle 常数时间比较，避免计时攻击猜出密钥）。
//
// 这不是完整的管理员登录/RBAC——没有"是谁在操作"的概念，知道这一个密钥的人
// 能做任何事，见 internal/admin 包文档的已知范围限制。用在 cmd/admin 这类内部
// 管理接口上，是"完全没有鉴权"和"完整的多用户 RBAC"之间一个真实、可用的中间态：
// 至少不再是任何能连上这个端口的人都能改价格、调余额、加上游 Key。
//
// token 为空字符串时，任何请求都会被拒绝（不会退化成"不鉴权"）——調用方应该在
// 没配置密钥时直接拒绝启动，而不是依赖这个中间件的兜底行为，但即使真的传了个
// 空字符串进来，这里也不会意外放行。
func RequireBearerToken(token string) func(http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractBearerToken(r.Header.Get("Authorization"))
			if !ok || subtle.ConstantTimeCompare([]byte(raw), tokenBytes) != 1 {
				WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Missing or invalid admin token.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

// statusRecorder 包装 ResponseWriter 以捕获实际写出的状态码，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush 透传给底层 ResponseWriter，流式响应（SSE）依赖它及时刷出数据。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AccessLog 记录每次请求的方法、路径、状态码、耗时；request_id 由 RequestID 中间件注入。
// 注意：不记录请求体/响应体（可能含用户 prompt），只记录元数据。
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			ctx := observability.WithLogger(r.Context(), logger)
			next.ServeHTTP(rec, r.WithContext(ctx))

			logger.InfoContext(r.Context(), "http_request",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}
