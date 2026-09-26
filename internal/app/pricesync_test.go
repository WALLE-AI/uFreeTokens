// 端到端集成测试：价格同步流水线（技术方案 §7.16）通过 internal/app.NewAdminRouter
// 的真实 HTTP 接口跑通——账户/Provider/渠道/初始成本价用现有的管理接口创建，
// 价格来源、价格观测、变更提案的审批/驳回全部通过新增的 pricesync 接口完成。
// 和 admin_gateway_e2e_test.go 一样的理由：只有真正走 HTTP 层才能验证
// internal/app 的 JSON 编解码和 internal/pricesync 的 Go 类型之间没有对不上的地方。
package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func adminGet(t *testing.T, baseURL, path string, out any) {
	t.Helper()
	resp, err := http.Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s: status = %d, body = %s", path, resp.StatusCode, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("GET %s: unmarshal response %s: %v", path, body, err)
		}
	}
}

func TestPriceSyncHTTP_IngestApproveReject(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	walletSvc := wallet.New(pool)
	adminSvc := admin.New(pool, walletSvc, box, []byte(testPepper))
	engine := pricesync.NewEngine(pool, adminSvc)
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, PriceSync: engine}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var provider struct{ ID int64 }
	ac.post("/providers", map[string]any{"Code": "ps-provider-" + suffix, "Name": "x", "Protocol": "openai"}, &provider)

	var providerAccount struct{ ID int64 }
	ac.post("/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "acc", "base_url": "https://x"}, &providerAccount)

	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"Name": "ps-vm-" + suffix, "Type": "chat", "ContextWindow": 128000, "MaxOutput": 8192,
	}, &vm)

	var channel struct{ ID int64 }
	ac.post("/channels", map[string]any{
		"VirtualModelID": vm.ID, "ProviderAccountID": providerAccount.ID, "UpstreamModel": "up-" + suffix,
	}, &channel)

	// 初始成本价：10 元/百万 input token。
	ac.post(fmt.Sprintf("/channels/%d/cost-price", channel.ID), map[string]any{
		"components": []map[string]any{{"Meter": "input", "Unit": "per_1m_tokens", "UnitPrice": "10"}},
	}, nil)

	var source struct{ ID int64 }
	ac.post("/price-sources", map[string]any{"provider_id": provider.ID, "level": "L2", "kind": "manual", "fetcher": "http-test"}, &source)

	// 降价：L2 来源，应该自动通过并直接发布。
	var autoResult pricesync.IngestResult
	ac.post(fmt.Sprintf("/channels/%d/price-observations", channel.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": "up-" + suffix, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "8"}},
	}, &autoResult)
	if autoResult.Decision != pricesync.DecisionAutoApproved {
		t.Fatalf("Decision = %q, want auto_approved", autoResult.Decision)
	}
	if autoResult.AppliedBookID == nil {
		t.Fatal("expected AppliedBookID to be set for an auto-approved decrease")
	}

	// 涨价 50%：超过自动通过阈值，应该是 pending。
	var pendingResult pricesync.IngestResult
	ac.post(fmt.Sprintf("/channels/%d/price-observations", channel.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": "up-" + suffix, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "12"}},
	}, &pendingResult)
	if pendingResult.Decision != pricesync.DecisionPending {
		t.Fatalf("Decision = %q, want pending", pendingResult.Decision)
	}
	if pendingResult.ChangeRequestID == nil {
		t.Fatal("expected a change request ID for a pending decision")
	}

	var list struct {
		ChangeRequests []pricesync.ChangeRequestSummary `json:"change_requests"`
	}
	adminGet(t, adminSrv.URL, "/price-change-requests", &list)
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
	var pending2 pricesync.IngestResult
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
	ac.post(fmt.Sprintf("/price-change-requests/%d/reject", *pending2.ChangeRequestID), map[string]any{"decided_by": 1}, &rejectResp)
	if rejectResp.Status != "rejected" {
		t.Errorf("status = %q, want rejected", rejectResp.Status)
	}

	// 拒绝之后重复操作应该报错（409），验证 writeAdminError 把
	// pricesync.ErrChangeRequestNotPending 映射对了状态码。
	resp, err := http.Post(fmt.Sprintf("%s/price-change-requests/%d/reject", adminSrv.URL, *pending2.ChangeRequestID),
		"application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("second reject: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second reject status = %d, want 409", resp.StatusCode)
	}
}

func TestPriceSyncHTTP_NotConfiguredReturns503(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	// PriceSync 故意留空——验证接口在没装配的情况下不会 panic，而是干净地返回 503。
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc}))
	defer adminSrv.Close()

	resp, err := http.Post(adminSrv.URL+"/price-sources", "application/json", bytes.NewReader([]byte(`{"level":"L5","kind":"manual","fetcher":"x"}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
