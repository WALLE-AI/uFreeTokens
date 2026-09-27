package console

import (
	"context"
	"net/http"
	"net/url"

	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

type sessionCtxKey struct{}

// SessionFromContext 取出 RequireSession 中间件放入 context 的会话数据。
func SessionFromContext(ctx context.Context) (*SessionData, bool) {
	s, ok := ctx.Value(sessionCtxKey{}).(*SessionData)
	return s, ok
}

// RequireSession 从 Cookie 里解析出会话，校验通过后放进 context；没带 Cookie
// 和 Cookie 过期/被吊销统一返回 401，不区分细节，避免泄露"这个会话曾经存在
// 过"这类信息。
func RequireSession(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
				return
			}
			data, err := store.Get(r.Context(), cookie.Value)
			if err != nil {
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Session expired or invalid.")
				return
			}
			ctx := context.WithValue(r.Context(), sessionCtxKey{}, data)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRFGuard 防止 httpOnly Cookie 会话被第三方站点通过跨站请求冒用——Cookie
// 本身对 XSS 免疫，但天然会跟着跨站请求一起发送，需要额外的 CSRF 防护。对
// 非 GET/HEAD/OPTIONS 请求要求同时满足两个条件：
//  1. 带有自定义头 X-UFT-CSRF: 1——跨站的 <form> 提交没有办法附加自定义头，
//     常见的"简单请求"型 CSRF 打不进来；跨站 fetch/XHR 想加这个头会先触发
//     CORS 预检，而 /console 完全不开 CORS（见 internal/httpx.CORS 的包注释），
//     预检必然失败。
//  2. Origin（或退而求其次 Referer）的 host 和请求本身的 Host 一致（同源）。
//
// 两者都满足才放行：只查自定义头，在一些会剥离/伪造非标准头的中间代理场景
// 下不够可靠；只查 Origin，在极少数会清空 Origin/Referer 头的老旧客户端上
// 会误伤同源的合法请求——两个一起用更稳，任一失败都判定为跨站请求。
func CSRFGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-UFT-CSRF") != "1" {
			httpx.WriteError(w, r, http.StatusForbidden, "csrf_check_failed", "Missing required CSRF header.")
			return
		}
		if !sameOrigin(r) {
			httpx.WriteError(w, r, http.StatusForbidden, "csrf_check_failed", "Cross-site request rejected.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		// 两道防线里的自定义头那道已经在 CSRFGuard 里生效；这里选择放行而不是
		// 拒绝，避免误伤那些会清空 Origin/Referer 头的同源请求。
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
