package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newProtectedHandler(token string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return RequireBearerToken(token)(inner)
}

func TestRequireBearerToken_RejectsMissingHeader(t *testing.T) {
	h := newProtectedHandler("secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireBearerToken_RejectsWrongToken(t *testing.T) {
	h := newProtectedHandler("secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireBearerToken_RejectsMalformedHeader(t *testing.T) {
	h := newProtectedHandler("secret")
	for _, header := range []string{"secret", "Basic secret", "Bearer", "Bearer "} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: status = %d, want 401", header, rec.Code)
		}
	}
}

func TestRequireBearerToken_AllowsCorrectToken(t *testing.T) {
	h := newProtectedHandler("secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestRequireBearerToken_EmptyConfiguredTokenAlwaysRejects 验证空字符串
// token（比如启动配置疏漏）不会意外退化成"不鉴权"——这是这个中间件存在的
// 全部意义，绝不能悄悄失效。
func TestRequireBearerToken_EmptyConfiguredTokenAlwaysRejects(t *testing.T) {
	h := newProtectedHandler("")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (empty configured token must never allow requests through)", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer anything")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec2.Code)
	}
}
