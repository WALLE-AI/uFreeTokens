// 集成测试：GET /pricesync/litellm-lookup 走真实 HTTP 层，假上游用
// httptest.Server（不发任何真实外部请求，同 internal/pricesync 的一贯约束）。
package app_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// litellmDatasetFixture 是手工构造的、遵循 LiteLLM model_prices_and_context_window.json
// 字段形态的样例——同 internal/pricesync/litellm_test.go 的 fixture，价格是
// JSON 数字（每 token 的美元），不是字符串。
const litellmDatasetFixture = `{
  "sample_spec": {"input_cost_per_token": 0.0000001, "output_cost_per_token": 0.0000002},
  "gpt-4o": {"input_cost_per_token": 0.0000025, "output_cost_per_token": 0.00001, "litellm_provider": "openai", "mode": "chat"}
}`

func newAdminRouterForPriceLookup(t *testing.T) *httptest.Server {
	t.Helper()
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	srv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLiteLLMPriceLookup_HappyPath(t *testing.T) {
	fakeDataset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(litellmDatasetFixture))
	}))
	defer fakeDataset.Close()

	adminSrv := newAdminRouterForPriceLookup(t)
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	var out struct {
		Data map[string]struct {
			Matched bool   `json:"matched"`
			Input   string `json:"input"`
			Output  string `json:"output"`
		} `json:"data"`
		Currency string `json:"currency"`
	}
	ac.post("/pricesync/litellm-lookup", map[string]any{
		"dataset_url":     fakeDataset.URL,
		"upstream_models": []string{"gpt-4o", "totally-unknown-model"},
	}, &out)

	if out.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", out.Currency)
	}
	gpt4o, ok := out.Data["gpt-4o"]
	if !ok || !gpt4o.Matched {
		t.Fatalf("gpt-4o result = %+v, want matched=true", gpt4o)
	}
	if gpt4o.Input != "2.5" {
		t.Errorf("gpt-4o.Input = %q, want 2.5 (0.0000025 USD/token * 1e6)", gpt4o.Input)
	}
	if gpt4o.Output != "10" {
		t.Errorf("gpt-4o.Output = %q, want 10", gpt4o.Output)
	}

	unknown, ok := out.Data["totally-unknown-model"]
	if !ok || unknown.Matched {
		t.Fatalf("totally-unknown-model result = %+v, want matched=false", unknown)
	}
}

func TestLiteLLMPriceLookup_EmptyModelsList_400(t *testing.T) {
	adminSrv := newAdminRouterForPriceLookup(t)

	raw := []byte(`{"upstream_models":[]}`)
	req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/pricesync/litellm-lookup", bytes.NewReader(raw))
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
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, body)
	}
}

func TestLiteLLMPriceLookup_DatasetUnavailable_502(t *testing.T) {
	fakeDataset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeDataset.Close()

	adminSrv := newAdminRouterForPriceLookup(t)

	raw := []byte(`{"dataset_url":"` + fakeDataset.URL + `","upstream_models":["gpt-4o"]}`)
	req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/pricesync/litellm-lookup", bytes.NewReader(raw))
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
	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, body)
	}
}
