// 运营后台（frontend/admin）接口契约的端到端测试：见
// docs/cmd-admin 运营后台接口补全技术方案.md。这里覆盖 B1 批次——409 映射、
// X-Actor-Name、调价审批的 confirm_blocked 与审批理由。
package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// do 发一个请求并返回状态码和响应体，不在非 2xx 时 Fatal——用于断言错误码。
func (c *adminClient) do(method, path string, body any, headers map[string]string) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal request body for %s: %v", path, err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rd)
	if err != nil {
		c.t.Fatalf("build request for %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

func newAdminTestServer(t *testing.T, withPriceSync bool) (*adminClient, func()) {
	t.Helper()
	ac, _, done := newAdminTestServerWithPool(t, withPriceSync)
	return ac, done
}

// newAdminTestServerWithPool 同 newAdminTestServer，额外返回连接池（登录测试管理员用）。
func newAdminTestServerWithPool(t *testing.T, withPriceSync bool) (*adminClient, *pgxpool.Pool, func()) {
	t.Helper()
	return newAdminTestServerWithDeps(t, withPriceSync, nil)
}

// newAdminTestServerWithDeps 允许测试在构造路由前调整依赖（例如接上 Redis、打开 If-Match 强制）。
func newAdminTestServerWithDeps(t *testing.T, withPriceSync bool, mutate func(*app.AdminDeps)) (*adminClient, *pgxpool.Pool, func()) {
	t.Helper()
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	deps := app.AdminDeps{Logger: logger, Admin: adminSvc, Auth: adminauth.New(pool, adminauth.Config{Box: box, MaxIPFailures: 1 << 30}), AdminToken: testAdminToken}
	if withPriceSync {
		deps.PriceSync = pricesync.NewEngine(pool, adminSvc)
	}
	if mutate != nil {
		mutate(&deps)
	}
	srv := httptest.NewServer(app.NewAdminRouter(deps))
	return &adminClient{t: t, baseURL: srv.URL}, pool, srv.Close
}

func TestAdminAPI_UniqueViolationsReturn409(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var account struct {
		ID int64 `json:"id"`
	}
	ac.post("/accounts", map[string]any{"type": "personal", "name": "dup-" + suffix}, &account)

	adjust := map[string]any{"amount": 1000, "ref_id": "dup-ref-" + suffix, "reason": "dup test"}
	var receipt struct {
		AmountMicro    int64 `json:"amount_micro"`
		CashAfterMicro int64 `json:"cash_after_micro"`
	}
	ac.post(fmt.Sprintf("/accounts/%d/wallet/adjust", account.ID), adjust, &receipt)
	if receipt.AmountMicro != 1000 || receipt.CashAfterMicro != 1000 {
		t.Errorf("receipt = %+v, want amount_micro=1000 cash_after_micro=1000", receipt)
	}
	// 同一个 ref_id 再调一次：ledger_entries 的唯一约束兜底防重复提交，应该是 409 而不是 400。
	if status, body := ac.do(http.MethodPost, fmt.Sprintf("/accounts/%d/wallet/adjust", account.ID), adjust, nil); status != http.StatusConflict {
		t.Errorf("duplicate ref_id: status = %d, body = %s, want 409", status, body)
	}

	provider := map[string]any{"code": "dup-provider-" + suffix, "name": "x", "protocol": "openai"}
	ac.post("/providers", provider, nil)
	if status, body := ac.do(http.MethodPost, "/providers", provider, nil); status != http.StatusConflict {
		t.Errorf("duplicate provider code: status = %d, body = %s, want 409", status, body)
	}

	// 请求体改为 snake_case 之后，旧的 PascalCase 多词字段必须被拒绝，而不是被静默忽略。
	if status, _ := ac.do(http.MethodPost, "/accounts", map[string]any{"Type": "personal", "Name": "x", "CreditLimit": 1}, nil); status != http.StatusBadRequest {
		t.Errorf("PascalCase CreditLimit: status = %d, want 400", status)
	}
}

func TestPriceSyncHTTP_ApproveBlockedRequiresConfirmation(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var provider, providerAccount, vm, channel, source struct {
		ID int64 `json:"id"`
	}
	ac.post("/providers", map[string]any{"code": "blk-provider-" + suffix, "name": "x", "protocol": "openai"}, &provider)
	ac.post("/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "acc", "base_url": "https://x"}, &providerAccount)
	ac.post("/virtual-models", map[string]any{"name": "blk-vm-" + suffix, "type": "chat", "context_window": 128000, "max_output": 8192}, &vm)
	ac.post("/channels", map[string]any{"virtual_model_id": vm.ID, "provider_account_id": providerAccount.ID, "upstream_model": "up-" + suffix}, &channel)
	ac.post(fmt.Sprintf("/channels/%d/cost-price", channel.ID), map[string]any{
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "10"}},
	}, nil)
	ac.post("/price-sources", map[string]any{"provider_id": provider.ID, "level": "L2", "kind": "manual", "fetcher": "http-test"}, &source)

	// 涨 10 倍：命中 magnitude 拦截规则，状态直接是 blocked。
	var result ingestResultJSON
	ac.post(fmt.Sprintf("/channels/%d/price-observations", channel.ID), map[string]any{
		"source_id": source.ID, "level": "L2", "upstream_model": "up-" + suffix, "currency": "CNY",
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "100"}},
	}, &result)
	if result.Decision != string(pricesync.DecisionBlocked) || result.ChangeRequestID == nil {
		t.Fatalf("ingest result = %+v, want blocked with a change request", result)
	}
	crPath := fmt.Sprintf("/price-change-requests/%d/approve", *result.ChangeRequestID)
	// 审批人来自登录会话；客户端自填的 X-Actor-* 头必须被忽略。
	approver := ac.loginAs(pool, "李四", "pricing")
	actor := map[string]string{"X-Actor-ID": "9", "X-Actor-Name": url.QueryEscape("伪造")}

	if status, body := ac.do(http.MethodPost, crPath, map[string]any{"reason": "厂商确认"}, actor); status != http.StatusConflict {
		t.Fatalf("approve blocked without confirm: status = %d, body = %s, want 409", status, body)
	}
	status, body := ac.do(http.MethodPost, crPath, map[string]any{"reason": "厂商确认", "confirm_blocked": true}, actor)
	if status != http.StatusOK {
		t.Fatalf("approve blocked with confirm: status = %d, body = %s", status, body)
	}

	var decidedBy int64
	var decidedByName, reason, crStatus string
	if err := pool.QueryRow(t.Context(),
		`SELECT decided_by, decided_by_name, decision_reason, status FROM price_change_requests WHERE id = $1`, *result.ChangeRequestID,
	).Scan(&decidedBy, &decidedByName, &reason, &crStatus); err != nil {
		t.Fatalf("query change request: %v", err)
	}
	if decidedBy != approver.ID || decidedByName != "李四" || reason != "厂商确认" || crStatus != "applied" {
		t.Errorf("decision = (%d, %q, %q, %q), want (%d, 李四, 厂商确认, applied)", decidedBy, decidedByName, reason, crStatus, approver.ID)
	}

	var audit struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.get(fmt.Sprintf("/audit-logs?target_type=price_change_request&target_id=%d", *result.ChangeRequestID), &audit)
	if len(audit.Data) != 1 || audit.Data[0].Action != "price_change.approve" || audit.Data[0].ActorName != "李四" {
		t.Errorf("audit = %+v, want one price_change.approve by 李四", audit.Data)
	}
}
