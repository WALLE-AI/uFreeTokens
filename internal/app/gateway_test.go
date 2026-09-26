// 纯路由装配层面的测试，不需要真实 Postgres/Redis：只验证
// NewGatewayRouter 有没有按 gateway.cors_origins 正确挂载/不挂载 CORS
// 中间件（迭代1，见 docs/frontend-web 与 Go 后端集成迭代执行方案.md）。
// 需要真实基础设施的端到端场景见 admin_gateway_e2e_test.go 和
// internal/relay 包的 relay_test.go。
package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

func newTestLogger() *slog.Logger {
	return observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
}

func TestNewGatewayRouter_CORSDisabledByDefault(t *testing.T) {
	r := NewGatewayRouter(GatewayDeps{
		Logger: newTestLogger(),
		Cfg:    &config.Config{},
	})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// 没挂 CORS 中间件时，OPTIONS 请求会落到 chi 的默认 405（这个路由组下没有
	// 注册 OPTIONS handler），并且绝不会带上 Access-Control-Allow-Origin。
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (gateway.cors_origins unset)", got)
	}
}

func TestNewGatewayRouter_CORSEnabledViaConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CORSOrigins = "https://app.example.com, https://admin.example.com"
	r := NewGatewayRouter(GatewayDeps{
		Logger: newTestLogger(),
		Cfg:    cfg,
	})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (CORS preflight answered before auth middleware)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want https://app.example.com", got)
	}
}

func TestNewGatewayRouter_CORSNotMountedOnConsoleOrRoot(t *testing.T) {
	// /console 还没实现（迭代3），这里先确认根路径不会意外带上 CORS 头——
	// CORS 只应该挂在 /v1 路由组。
	cfg := &config.Config{}
	cfg.Gateway.CORSOrigins = "https://app.example.com"
	r := NewGatewayRouter(GatewayDeps{
		Logger: newTestLogger(),
		Cfg:    cfg,
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (CORS must not apply outside /v1)", got)
	}
}

func TestSplitCommaList(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want []string
	}{
		{"nil_cfg", nil, nil},
		{"empty_string", &config.Config{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitCommaList(c.cfg)
			if len(got) != len(c.want) {
				t.Errorf("splitCommaList() = %v, want %v", got, c.want)
			}
		})
	}

	trimCfg := &config.Config{}
	trimCfg.Gateway.CORSOrigins = " https://a.example.com ,https://b.example.com,, "
	got := splitCommaList(trimCfg)
	want := []string{"https://a.example.com", "https://b.example.com"}
	if len(got) != len(want) {
		t.Fatalf("splitCommaList() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitCommaList()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
