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

func newCORSProtectedHandler(origins []string) (http.Handler, *bool) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	return CORS(origins)(inner), &called
}

// TestCORS_PreflightFromAllowedOrigin_RespondsWithHeadersAndSkipsNext 验证挂在
// auth.APIKey 之前的目的：预检 OPTIONS 请求由 CORS 中间件自己应答（不带
// Authorization 也不会被后面的鉴权拦成 401），并带上浏览器需要的
// Access-Control-* 头。
func TestCORS_PreflightFromAllowedOrigin_RespondsWithHeadersAndSkipsNext(t *testing.T) {
	h, called := newCORSProtectedHandler([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the requesting origin", got)
	}
	if rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("Access-Control-Allow-Headers should be set on a preflight response")
	}
	if *called {
		t.Error("preflight OPTIONS should be answered by CORS itself, not reach the inner handler")
	}
}

func TestCORS_PreflightFromDisallowedOrigin_NoAllowHeader(t *testing.T) {
	h, called := newCORSProtectedHandler([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (origin not in allowlist)", got)
	}
	if *called {
		t.Error("preflight OPTIONS should never reach the inner handler")
	}
}

// TestCORS_ActualRequestFromAllowedOrigin_SetsHeaderAndCallsNext 验证真正的请求
// （非预检）从允许的 Origin 发起时，会带上 Access-Control-Allow-Origin，并且
// 正常放行给后面的鉴权/业务处理。
func TestCORS_ActualRequestFromAllowedOrigin_SetsHeaderAndCallsNext(t *testing.T) {
	h, called := newCORSProtectedHandler([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the requesting origin", got)
	}
	if !*called {
		t.Error("actual request should reach the inner handler")
	}
}

func TestCORS_RequestWithoutOriginHeader_PassesThroughUnmodified(t *testing.T) {
	h, called := newCORSProtectedHandler([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (server-to-server request, no Origin header)", got)
	}
	if !*called {
		t.Error("request without an Origin header should still reach the inner handler")
	}
}
