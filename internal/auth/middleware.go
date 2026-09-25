package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

type principalCtxKey struct{}

// FromContext 取出鉴权中间件放入 context 的 Principal。
func FromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(*Principal)
	return p, ok
}

// APIKey 是网关 /v1/* 路由的鉴权中间件：解析 Authorization: Bearer sk-uft-...，
// 计算 HMAC 后查库，把 Principal 注入 context。错误响应遵循附录 A 的错误码。
func APIKey(store Store, pepper []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Missing or malformed Authorization header.")
				return
			}

			mac, err := ComputeHMAC(pepper, raw)
			if err != nil {
				httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
				return
			}

			principal, err := store.FindByHMAC(r.Context(), mac)
			if err != nil {
				writeAuthError(w, r, err)
				return
			}

			store.TouchLastUsed(r.Context(), principal.APIKeyID, time.Now())

			ctx := context.WithValue(r.Context(), principalCtxKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(header string) (string, bool) {
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

func writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound), errors.Is(err, ErrKeyDisabled), errors.Is(err, ErrKeyExpired):
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
	case errors.Is(err, ErrAccountSuspended):
		httpx.WriteError(w, r, http.StatusForbidden, "account_suspended", "This account has been suspended.")
	default:
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to authenticate request.")
	}
}
