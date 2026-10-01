package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// idempotency 让带 Idempotency-Key 头的 POST 请求可以安全重试：同一管理员、同一
// Key、同一请求（方法 + 路径 + 请求体）第二次到达时直接重放第一次的响应。
//   - Key 相同但请求不同 → 422 idempotency_key_reused；
//   - 第一次还在处理中 → 409 idempotency_in_progress；
//   - 第一次以 5xx 结束 → 释放 Key，允许用同一个 Key 重试。
//
// 不带这个头的请求不受影响。只对 POST 生效（PATCH 由 If-Match 保护）。
func (h *adminHandlers) idempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if r.Method != http.MethodPost || key == "" || h.svc == nil {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 128 {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Idempotency-Key must be at most 128 characters.")
			return
		}
		p := adminauth.FromContext(r.Context())
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "unreadable request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.RequestURI()+"\n"), raw...))

		claimed, prev, err := h.svc.ClaimIdempotencyKey(r.Context(), p.AdminID, key, sum[:])
		if err != nil {
			writeAdminError(w, r, h.log, err)
			return
		}
		if !claimed {
			switch {
			case !bytes.Equal(prev.RequestHash, sum[:]) && prev.RequestHash != nil:
				httpx.WriteError(w, r, http.StatusUnprocessableEntity, "idempotency_key_reused", "This Idempotency-Key was already used for a different request.")
			case prev.StatusCode == 0:
				httpx.WriteError(w, r, http.StatusConflict, "idempotency_in_progress", "A request with this Idempotency-Key is still being processed.")
			default:
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Idempotent-Replayed", "true")
				w.WriteHeader(prev.StatusCode)
				_, _ = w.Write(prev.Body)
			}
			return
		}

		rec := &capturingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// 请求上下文可能已结束（客户端断开），用独立的短超时上下文落库。
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if rec.status >= 500 {
			if err := h.svc.ReleaseIdempotencyKey(ctx, p.AdminID, key); err != nil {
				h.log.Error("release idempotency key failed", "error", err)
			}
			return
		}
		if err := h.svc.CompleteIdempotencyKey(ctx, p.AdminID, key, rec.status, rec.body.Bytes()); err != nil {
			h.log.Error("store idempotent response failed", "status", strconv.Itoa(rec.status), "error", err)
		}
	})
}

// capturingWriter 在写出响应的同时保留一份副本（上限 1 MiB，管理接口响应远小于此）。
type capturingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (c *capturingWriter) WriteHeader(code int) {
	if !c.wroteHeader {
		c.status, c.wroteHeader = code, true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.wroteHeader = true
	}
	if c.body.Len()+len(b) <= 1<<20 {
		c.body.Write(b)
	}
	return c.ResponseWriter.Write(b)
}
