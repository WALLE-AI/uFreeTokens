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
// （见技术方案 §7.9.3）。若客户端已带合法的 X-Request-Id（1-64 位字母数字、'-'、'_'），
// 则复用（便于客户端侧关联排障）；不合法的值会被替换，避免日志/响应头注入。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = ulid.Make().String()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// MaxBodyBytes 限制请求体大小，超出时后续的 JSON 解码会失败（返回 400）。
func MaxBodyBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
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

// CORS 允许配置的 Origin 列表跨域调用 /v1（技术方案：BYOK 浏览器客户端直接
// 用自己的 API Key 调网关，不经过同源反代）。只做精确匹配的白名单，不开
// credentials——鉴权信息走 Authorization: Bearer <api-key>，不依赖 Cookie，
// 也就不需要、不应该允许带 Cookie 跨域（/console 的 Cookie 会话只走同源，
// 完全不挂这个中间件）。调用方应该只在 origins 非空时挂载：origins 为空
// 表示不启用跨域，调用方不应该挂一个允许列表为空的 CORS 中间件。
//
// 必须挂在 auth.APIKey 之前：预检请求（OPTIONS）不带 Authorization 头，
// 如果先过鉴权中间件会被判 401，浏览器就看不到这里设置的 CORS 头，等于
// 跨域请求整体失败。
func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id, Retry-After")
			}
			if r.Method == http.MethodOptions {
				if origin != "" && allowed[origin] {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
					w.Header().Set("Access-Control-Max-Age", "600")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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

// Unwrap 让 http.ResponseController 能拿到底层 ResponseWriter（SSE 接口用它解除写超时）。
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

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
