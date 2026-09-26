// 集成测试：GET /v1/usage 走真实 HTTP 层——账户/API Key/Provider/渠道/售价
// 全部通过 internal/app.NewAdminRouter 建，一个真实的 /v1/chat/completions
// 请求打完之后（同 admin_gateway_e2e_test.go 的套路），断言 GET /v1/usage
// 返回的钱包余额和累计用量跟这次请求实际扣的钱、用的 token 数一致。
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

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/relay"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestUsageEndpoint_ReflectsWalletAndRequestLogTotals(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	walletSvc := wallet.New(pool)
	adminSvc := admin.New(pool, walletSvc, box, []byte(testPepper))
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1000, "completion_tokens": 500}}`))
	}))
	defer upstream.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var account struct{ ID int64 }
	ac.post("/accounts", map[string]any{"Type": "personal", "Name": "usage-" + suffix, "Tier": "free"}, &account)
	ac.post(fmt.Sprintf("/accounts/%d/wallet/adjust", account.ID),
		map[string]any{"amount": 1_000_000, "ref_id": "usage-recharge-" + suffix}, nil)

	var apiKey struct{ RawKey string }
	ac.post(fmt.Sprintf("/accounts/%d/api-keys", account.ID), map[string]any{"name": "usage-key"}, &apiKey)
	if apiKey.RawKey == "" {
		t.Fatal("admin did not return a raw API key")
	}

	const upstreamSecret = "sk-usage-test-upstream-secret"
	var provider struct{ ID int64 }
	ac.post("/providers", map[string]any{"Code": "usage-provider-" + suffix, "Name": "UsageTest", "Protocol": "openai"}, &provider)
	var providerAccount struct{ ID int64 }
	ac.post("/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "usage-account", "base_url": upstream.URL}, &providerAccount)
	ac.post(fmt.Sprintf("/provider-accounts/%d/keys", providerAccount.ID), map[string]any{"secret": upstreamSecret, "weight": 100}, nil)

	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"Name": "usage-model-" + suffix, "Family": "test", "Type": "chat",
		"ContextWindow": 128000, "MaxOutput": 8192, "Capabilities": []string{"stream"},
	}, &vm)
	ac.post("/channels", map[string]any{
		"VirtualModelID": vm.ID, "ProviderAccountID": providerAccount.ID,
		"UpstreamModel": "upstream-model-name", "Priority": 0, "Weight": 100,
	}, nil)
	ac.post(fmt.Sprintf("/virtual-models/%d/sell-price", vm.ID), map[string]any{
		"components": []map[string]any{
			{"Meter": "input", "Unit": "per_1m_tokens", "UnitPrice": "1"},
			{"Meter": "output", "Unit": "per_1m_tokens", "UnitPrice": "1"},
		},
	}, nil)

	// 和 admin_gateway_e2e_test.go 的关键区别：这里配了一个真实的
	// reqlog.Writer，不是 nil——GET /v1/usage 的累计用量是从 request_logs
	// 聚合出来的，没有真实写入这张表，这个测试什么都验证不了。
	reqLogWriter := reqlog.NewWriter(pool, logger)
	relaySvc := &relay.Service{
		Catalog:  catalog.NewStore(pool, box, 0),
		Wallet:   walletSvc,
		Adapters: adapter.NewRegistry(),
		HTTP:     http.DefaultClient,
		Logger:   logger,
		Cfg:      relay.DefaultConfig(),
		ReqLog:   reqLogWriter,
	}
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{
		Logger: logger, PG: pool, AuthStore: auth.NewPostgresStore(pool),
		Pepper: []byte(testPepper), Relay: relaySvc,
	}))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{"model": "usage-model-" + suffix, "messages": []any{}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey.RawKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat completion request: %v", err)
	}
	chatBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat completion status = %d, body = %s", resp.StatusCode, chatBody)
	}

	// 同步 flush 掉 reqlog 的批量写入队列，不用靠 sleep 猜时间。
	reqLogWriter.Close()

	usageReq, _ := http.NewRequest(http.MethodGet, gw.URL+"/v1/usage", nil)
	usageReq.Header.Set("Authorization", "Bearer "+apiKey.RawKey)
	usageResp, err := http.DefaultClient.Do(usageReq)
	if err != nil {
		t.Fatalf("GET /v1/usage: %v", err)
	}
	defer usageResp.Body.Close()
	usageBody, _ := io.ReadAll(usageResp.Body)
	if usageResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/usage status = %d, body = %s", usageResp.StatusCode, usageBody)
	}

	var out struct {
		Wallet struct {
			CashBalanceMicro int64 `json:"cash_balance_micro"`
		} `json:"wallet"`
		Usage struct {
			TotalRequests           int64 `json:"total_requests"`
			TotalInputTokens        int64 `json:"total_input_tokens"`
			TotalOutputTokens       int64 `json:"total_output_tokens"`
			TotalChargedAmountMicro int64 `json:"total_charged_amount_micro"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(usageBody, &out); err != nil {
		t.Fatalf("unmarshal usage response %s: %v", usageBody, err)
	}

	// 1500 token 合计，单价 1 元/百万 -> 1500 微元，和 admin_gateway_e2e_test.go
	// 的手算方式一致。
	if out.Usage.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1", out.Usage.TotalRequests)
	}
	if out.Usage.TotalInputTokens != 1000 {
		t.Errorf("TotalInputTokens = %d, want 1000", out.Usage.TotalInputTokens)
	}
	if out.Usage.TotalOutputTokens != 500 {
		t.Errorf("TotalOutputTokens = %d, want 500", out.Usage.TotalOutputTokens)
	}
	if out.Usage.TotalChargedAmountMicro != 1500 {
		t.Errorf("TotalChargedAmountMicro = %d, want 1500", out.Usage.TotalChargedAmountMicro)
	}
	if out.Wallet.CashBalanceMicro != 1_000_000-1500 {
		t.Errorf("CashBalanceMicro = %d, want %d", out.Wallet.CashBalanceMicro, 1_000_000-1500)
	}
}

func TestUsageEndpoint_RequiresAuth(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	relaySvc := &relay.Service{
		Catalog:  catalog.NewStore(pool, box, 0),
		Wallet:   wallet.New(pool),
		Adapters: adapter.NewRegistry(),
		HTTP:     http.DefaultClient,
		Logger:   logger,
		Cfg:      relay.DefaultConfig(),
	}
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{
		Logger: logger, PG: pool, AuthStore: auth.NewPostgresStore(pool),
		Pepper: []byte(testPepper), Relay: relaySvc,
	}))
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/v1/usage")
	if err != nil {
		t.Fatalf("GET /v1/usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 401, body = %s", resp.StatusCode, body)
	}
}
