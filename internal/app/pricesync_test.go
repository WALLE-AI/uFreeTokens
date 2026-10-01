// 端到端集成测试：价格同步流水线（技术方案 §7.16）通过 internal/app.NewAdminRouter
// 的真实 HTTP 接口跑通——账户/Provider/渠道/初始成本价用现有的管理接口创建，
// 价格来源、价格观测、变更提案的审批/驳回全部通过新增的 pricesync 接口完成。
// 和 admin_gateway_e2e_test.go 一样的理由：只有真正走 HTTP 层才能验证
// internal/app 的 JSON 编解码和 internal/pricesync 的 Go 类型之间没有对不上的地方。
package app_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestPriceSyncHTTP_IngestApproveReject(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	walletSvc := wallet.New(pool)
	adminSvc := admin.New(pool, walletSvc, box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	engine := pricesync.NewEngine(pool, adminSvc)
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, PriceSync: engine, AdminToken: testAdminToken}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var provider struct{ ID int64 }
	ac.post("/providers", map[string]any{"code": "ps-provider-" + suffix, "name": "x", "protocol": "openai"}, &provider)

	var providerAccount struct{ ID int64 }
	ac.post("/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "acc", "base_url": "https://x"}, &providerAccount)

	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"name": "ps-vm-" + suffix, "type": "chat", "context_window": 128000, "max_output": 8192,
	}, &vm)

	var channel struct{ ID int64 }
	ac.post("/channels", map[string]any{
		"virtual_model_id": vm.ID, "provider_account_id": providerAccount.ID, "upstream_model": "up-" + suffix,
	}, &channel)

	// 初始成本价：10 元/百万 input token。
	ac.post(fmt.Sprintf("/channels/%d/cost-price", channel.ID), map[string]any{
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "10"}},
	}, nil)

	var source struct{ ID int64 }
	ac.post("/price-sources", map[string]any{"provider_id": provider.ID, "level": "L2", "kind": "manual", "fetcher": "http-test"}, &source)

	// 降价：L2 来源，应该自动通过并直接发布。
	var autoResult ingestResultJSON
	ac.post(fmt.Sprintf("/channels/%d/price-observations", channel.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": "up-" + suffix, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "8"}},
	}, &autoResult)
	if autoResult.Decision != string(pricesync.DecisionAutoApproved) {
		t.Fatalf("Decision = %q, want auto_approved", autoResult.Decision)
	}
	if autoResult.AppliedBookID == nil {
		t.Fatal("expected AppliedBookID to be set for an auto-approved decrease")
	}

	// 涨价 50%：超过自动通过阈值，应该是 pending。
	var pendingResult ingestResultJSON
	ac.post(fmt.Sprintf("/channels/%d/price-observations", channel.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": "up-" + suffix, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "12"}},
	}, &pendingResult)
	if pendingResult.Decision != string(pricesync.DecisionPending) {
		t.Fatalf("Decision = %q, want pending", pendingResult.Decision)
	}
	if pendingResult.ChangeRequestID == nil {
		t.Fatal("expected a change request ID for a pending decision")
	}

	var list struct {
		ChangeRequests []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	// 按本测试的渠道过滤：共享测试库里其它测试累积的待审请求可能超过一页
	ac.get(fmt.Sprintf("/price-change-requests?channel_id=%d", channel.ID), &list)
	var found bool
	for _, cr := range list.ChangeRequests {
		if cr.ID == *pendingResult.ChangeRequestID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected change request %d in GET /price-change-requests, got %+v", *pendingResult.ChangeRequestID, list.ChangeRequests)
	}

	var approveResp struct {
		AppliedBookID int64 `json:"applied_book_id"`
	}
	ac.post(fmt.Sprintf("/price-change-requests/%d/approve", *pendingResult.ChangeRequestID), map[string]any{"decided_by": 1}, &approveResp)
	if approveResp.AppliedBookID == 0 {
		t.Error("expected a non-zero applied_book_id after approval")
	}

	var currency string
	if err := pool.QueryRow(t.Context(), `SELECT currency FROM price_books WHERE id = $1`, approveResp.AppliedBookID).Scan(&currency); err != nil {
		t.Fatalf("query applied price_book: %v", err)
	}
	if currency != "CNY" {
		t.Errorf("currency = %q, want CNY", currency)
	}

	// 再来一次涨价制造第二条 pending，走 reject 分支。
	var pending2 ingestResultJSON
	ac.post(fmt.Sprintf("/channels/%d/price-observations", channel.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": "up-" + suffix, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "100"}},
	}, &pending2)
	if pending2.ChangeRequestID == nil {
		t.Fatal("expected a second change request")
	}

	var rejectResp struct {
		Status string `json:"status"`
	}
	ac.post(fmt.Sprintf("/price-change-requests/%d/reject", *pending2.ChangeRequestID), map[string]any{"decided_by": 1, "reason": "涨价幅度未经确认"}, &rejectResp)
	if rejectResp.Status != "rejected" {
		t.Errorf("status = %q, want rejected", rejectResp.Status)
	}

	// 拒绝之后重复操作应该报错（409），验证 writeAdminError 把
	// pricesync.ErrChangeRequestNotPending 映射对了状态码。
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/price-change-requests/%d/reject", adminSrv.URL, *pending2.ChangeRequestID),
		bytes.NewReader([]byte(`{"reason":"重复驳回"}`)))
	if err != nil {
		t.Fatalf("build second reject request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second reject: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second reject status = %d, want 409", resp.StatusCode)
	}
}

func TestPriceSyncHTTP_NewModelDiscoveryAndPublish(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	walletSvc := wallet.New(pool)
	adminSvc := admin.New(pool, walletSvc, box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	engine := pricesync.NewEngine(pool, adminSvc)
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, PriceSync: engine, AdminToken: testAdminToken}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var provider struct{ ID int64 }
	ac.post("/providers", map[string]any{"code": "discover-provider-" + suffix, "name": "x", "protocol": "openai"}, &provider)

	var providerAccount struct{ ID int64 }
	ac.post("/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "acc", "base_url": "https://x"}, &providerAccount)

	var source struct{ ID int64 }
	ac.post("/price-sources", map[string]any{"provider_id": provider.ID, "level": "L2", "kind": "manual", "fetcher": "http-test"}, &source)

	// 提交一条观测，这个 provider 底下压根没有任何渠道叫这个 upstream_model
	// 名字——应该落进"待上架"队列，而不是报错或者被静默丢弃。
	model := "brand-new-model-" + suffix
	var ingestResult struct {
		ListingID *int64 `json:"listing_id"`
	}
	ac.post(fmt.Sprintf("/providers/%d/price-observations", provider.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": model, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "8"}},
	}, &ingestResult)
	if ingestResult.ListingID == nil {
		t.Fatal("expected a pending listing ID, got nil (no channel should have matched)")
	}

	var list struct {
		PendingListings []struct {
			ID            int64  `json:"id"`
			UpstreamModel string `json:"upstream_model"`
		} `json:"data"`
	}
	ac.get("/pending-model-listings", &list)
	var found bool
	for _, l := range list.PendingListings {
		if l.ID == *ingestResult.ListingID {
			found = true
			if l.UpstreamModel != model {
				t.Errorf("UpstreamModel = %q, want %q", l.UpstreamModel, model)
			}
		}
	}
	if !found {
		t.Fatalf("expected listing %d in GET /pending-model-listings, got %+v", *ingestResult.ListingID, list.PendingListings)
	}

	var publishResult struct {
		VirtualModelID int64 `json:"virtual_model_id"`
		ChannelID      int64 `json:"channel_id"`
		CostBookID     int64 `json:"cost_book_id"`
		SellBookID     int64 `json:"sell_book_id"`
	}
	ac.post(fmt.Sprintf("/pending-model-listings/%d/publish", *ingestResult.ListingID), map[string]any{
		"virtual_model": map[string]any{
			"name": "discovered-vm-" + suffix, "type": "chat", "context_window": 128000, "max_output": 8192,
		},
		"provider_account_id": providerAccount.ID,
		"sell_markup":         "0.25",
	}, &publishResult)
	if publishResult.VirtualModelID == 0 || publishResult.ChannelID == 0 {
		t.Fatalf("PublishListing result has a zero ID: %+v", publishResult)
	}

	var sellPrice decimal.Decimal
	if err := pool.QueryRow(t.Context(), `SELECT unit_price FROM price_components WHERE price_book_id = $1`, publishResult.SellBookID).Scan(&sellPrice); err != nil {
		t.Fatalf("query sell price: %v", err)
	}
	if !sellPrice.Equal(decimal.NewFromInt(10)) { // 8 * 1.25
		t.Errorf("sell price = %s, want 10 (8 * 1.25 markup)", sellPrice)
	}
}

func TestPriceSyncHTTP_NotConfiguredReturns503(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	// PriceSync 故意留空——验证接口在没装配的情况下不会 panic，而是干净地返回 503
	// （鉴权本身是通过的，这条测的是鉴权通过之后 handler 自己的兜底检查）。
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()

	req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/price-sources", bytes.NewReader([]byte(`{"level":"L5","kind":"manual","fetcher":"x"}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// ingestResultJSON 是 POST .../price-observations 响应的 snake_case 形状
// （internal/app/admin_dto.go 的 ingestResultDTO）。
type ingestResultJSON struct {
	ObservationID   int64  `json:"observation_id"`
	ChangeRequestID *int64 `json:"change_request_id"`
	Decision        string `json:"decision"`
	AppliedBookID   *int64 `json:"applied_book_id"`
}
