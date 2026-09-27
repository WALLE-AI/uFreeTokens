// 端到端测试：GET /v1/catalog（技术方案迭代5的公开模型目录）。用真实的
// internal/admin HTTP 接口建虚拟模型 + 售价（同 usage_test.go 的套路），
// 验证公开目录只暴露 free tier 可见的模型、售价字段来自 SellPriceBooks、
// 免鉴权也能访问。
package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

type catalogResponseModel struct {
	Name      string `json:"name"`
	Family    string `json:"family"`
	Status    string `json:"status"`
	SellPrice *struct {
		Currency   string `json:"currency"`
		Components []struct {
			Meter     string `json:"meter"`
			Unit      string `json:"unit"`
			UnitPrice string `json:"unit_price"`
		} `json:"components"`
	} `json:"sell_price"`
}

func TestCatalog_PublicEndpoint_ReturnsActiveFreeTierModelsWithSellPrice(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	visibleName := "catalog-e2e-" + suffix
	hiddenName := "catalog-e2e-hidden-" + suffix

	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"Name": visibleName, "Family": "test", "Type": "chat",
		"ContextWindow": 128000, "MaxOutput": 8192, "Capabilities": []string{"stream"},
		"VisibleTiers": []string{"free"},
	}, &vm)
	ac.post(fmt.Sprintf("/virtual-models/%d/sell-price", vm.ID), map[string]any{
		"components": []map[string]any{
			{"Meter": "input", "Unit": "per_1m_tokens", "UnitPrice": "1.5"},
			{"Meter": "output", "Unit": "per_1m_tokens", "UnitPrice": "3"},
		},
	}, nil)

	// visible_tiers 不含 free：不应该出现在公开目录里。
	ac.post("/virtual-models", map[string]any{
		"Name": hiddenName, "Family": "test", "Type": "chat",
		"ContextWindow": 128000, "MaxOutput": 8192, "Capabilities": []string{},
		"VisibleTiers": []string{"enterprise"},
	}, nil)

	catalogStore := catalog.NewStore(pool, box, 0) // TTL=0：每次 Get 都重新加载
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Logger: logger, PG: pool, Catalog: catalogStore}))
	defer gw.Close()

	// 完全不带 Authorization 头——这是这个端点存在的意义。
	resp, err := http.Get(gw.URL + "/v1/catalog")
	if err != nil {
		t.Fatalf("GET /v1/catalog: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want 'public, max-age=60'", cc)
	}

	var out struct {
		Data []catalogResponseModel `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response %s: %v", body, err)
	}

	var found bool
	for _, m := range out.Data {
		if m.Name == hiddenName {
			t.Errorf("model with visible_tiers=[enterprise] leaked into the public (free-tier) catalog: %s", m.Name)
		}
		if m.Name == visibleName {
			found = true
			if m.SellPrice == nil {
				t.Fatal("expected sell_price to be populated from SellPriceBooks")
			}
			if m.SellPrice.Currency != "CNY" {
				t.Errorf("sell_price.currency = %q, want CNY", m.SellPrice.Currency)
			}
			if len(m.SellPrice.Components) != 2 {
				t.Errorf("sell_price.components = %v, want 2 entries (input+output)", m.SellPrice.Components)
			}
		}
	}
	if !found {
		t.Errorf("free-tier model %q not found in public catalog: %+v", visibleName, out.Data)
	}
}

// TestCatalog_IncludesDeprecatedModelsWithStatusField 验证 GET /v1/catalog
// 会把 status='deprecated' 的模型也带出来（供前端"Show deprecated"筛选项
// 使用），且 status 字段准确反映 active/deprecated——deprecated 模型没有
// 走 admin 的创建接口（目前没有能把模型置为 deprecated 的公开接口），
// 直接用 SQL 插入，和 internal/catalog 包测试里"直接建库表行"的套路一致。
func TestCatalog_IncludesDeprecatedModelsWithStatusField(t *testing.T) {
	pool := testPool(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	deprecatedName := "catalog-e2e-deprecated-" + suffix

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, 'test', 'chat', 128000, 8192, '{}', '{free}', 'deprecated')`,
		deprecatedName); err != nil {
		t.Fatalf("insert deprecated virtual_model: %v", err)
	}

	catalogStore := catalog.NewStore(pool, testBox(t), 0)
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Logger: logger, PG: pool, Catalog: catalogStore}))
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/v1/catalog")
	if err != nil {
		t.Fatalf("GET /v1/catalog: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var out struct {
		Data []catalogResponseModel `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response %s: %v", body, err)
	}

	var found bool
	for _, m := range out.Data {
		if m.Name == deprecatedName {
			found = true
			if m.Status != "deprecated" {
				t.Errorf("status = %q, want %q", m.Status, "deprecated")
			}
		}
	}
	if !found {
		t.Errorf("deprecated model %q not found in public catalog: %+v", deprecatedName, out.Data)
	}
}

func TestCatalog_ReturnsNotImplementedWhenNotConfigured(t *testing.T) {
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Logger: logger}))
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/v1/catalog")
	if err != nil {
		t.Fatalf("GET /v1/catalog: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, body)
	}
}
