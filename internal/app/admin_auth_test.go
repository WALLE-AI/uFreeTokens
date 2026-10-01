// 端到端集成测试：cmd/admin 的鉴权中间件（httpx.RequireBearerToken）真的接在
// internal/app.NewAdminRouter 上，覆盖了业务路由但放行 /healthz。
package app_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestAdminRouter_RejectsRequestsWithoutValidToken(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()

	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong token", "Bearer not-the-token"},
		{"malformed", "not-even-bearer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/accounts", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestAdminRouter_HealthzIsAlwaysOpen 验证 /healthz 不需要鉴权——运维/负载均衡器
// 探活不应该依赖一个可能过期/泄露的管理密钥。
func TestAdminRouter_HealthzIsAlwaysOpen(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()

	resp, err := http.Get(adminSrv.URL + "/healthz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (no Authorization header sent)", resp.StatusCode)
	}
}

// TestAdminRouter_EmptyAdminTokenRejectsEvenTheCorrectSharedSecret 验证
// AdminDeps.AdminToken 留空（比如启动时的配置疏漏）不会让业务路由退化成不鉴权——
// 这条测的是 internal/app 这一层真的把 AdminToken 原样传给了
// httpx.RequireBearerToken，而不是不小心传了个别的默认值。
func TestAdminRouter_EmptyAdminTokenRejectsEvenTheCorrectSharedSecret(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc})) // AdminToken 留空
	defer adminSrv.Close()

	req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/accounts", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer ") // 空 token 配合空 Authorization 也不该放行
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
