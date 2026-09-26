// 集成测试：POST /pricesync/reference-price-lookup 走真实 HTTP 层，两个假
// 上游都用 httptest.Server（不发任何真实外部请求，同 internal/pricesync 的
// 一贯约束）。
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

// litellmDatasetFixture 同 internal/pricesync/litellm_test.go 的 fixture：
// 价格是 JSON 数字（每 token 的美元），不是字符串。gpt-4o 和 shared-model
// 都在这里，shared-model 同时也在 openRouterModelsLookupFixture 里，用来
// 验证 OpenRouter 优先。
const litellmDatasetFixture = `{
  "sample_spec": {"input_cost_per_token": 0.0000001, "output_cost_per_token": 0.0000002},
  "gpt-4o": {"input_cost_per_token": 0.0000025, "output_cost_per_token": 0.00001, "litellm_provider": "openai", "mode": "chat"},
  "shared-model": {"input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002, "litellm_provider": "test", "mode": "chat"}
}`

// openRouterModelsLookupFixture 遵循 OpenRouter GET /api/v1/models 的形状：
// pricing 下每个数值是 USD/token 字符串。shared-model 的价格和
// litellmDatasetFixture 里的不一样，用来断言 OpenRouter 优先生效。
const openRouterModelsLookupFixture = `{
  "data": [
    {"id": "shared-model", "pricing": {"prompt": "0.000003", "completion": "0.000006"}},
    {"id": "openrouter-only-model", "pricing": {"prompt": "0.0000005", "completion": "0.000001"}}
  ]
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

func fakeJSONServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fake500Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type priceLookupResult struct {
	Matched bool   `json:"matched"`
	Source  string `json:"source"`
	Input   string `json:"input"`
	Output  string `json:"output"`
}

type priceLookupResponse struct {
	Data            map[string]priceLookupResult `json:"data"`
	Currency        string                       `json:"currency"`
	OpenRouterError string                       `json:"openrouter_error"`
	LiteLLMError    string                       `json:"litellm_error"`
}

func TestReferencePriceLookup_OpenRouterTakesPriorityOverLiteLLM(t *testing.T) {
	litellmSrv := fakeJSONServer(t, litellmDatasetFixture)
	openrouterSrv := fakeJSONServer(t, openRouterModelsLookupFixture)
	adminSrv := newAdminRouterForPriceLookup(t)
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	var out priceLookupResponse
	ac.post("/pricesync/reference-price-lookup", map[string]any{
		"litellm_dataset_url":   litellmSrv.URL,
		"openrouter_models_url": openrouterSrv.URL,
		"upstream_models":       []string{"shared-model", "gpt-4o", "openrouter-only-model", "totally-unknown-model"},
	}, &out)

	if out.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", out.Currency)
	}

	shared, ok := out.Data["shared-model"]
	if !ok || !shared.Matched || shared.Source != "openrouter" {
		t.Fatalf("shared-model = %+v, want matched=true source=openrouter (OpenRouter must win over LiteLLM)", shared)
	}
	if shared.Input != "3" || shared.Output != "6" {
		t.Errorf("shared-model price = input=%s output=%s, want 3/6 (OpenRouter's numbers, not LiteLLM's 1/2)", shared.Input, shared.Output)
	}

	gpt4o, ok := out.Data["gpt-4o"]
	if !ok || !gpt4o.Matched || gpt4o.Source != "litellm" {
		t.Fatalf("gpt-4o = %+v, want matched=true source=litellm (only present in the LiteLLM fixture)", gpt4o)
	}

	orOnly, ok := out.Data["openrouter-only-model"]
	if !ok || !orOnly.Matched || orOnly.Source != "openrouter" {
		t.Fatalf("openrouter-only-model = %+v, want matched=true source=openrouter", orOnly)
	}

	unknown, ok := out.Data["totally-unknown-model"]
	if !ok || unknown.Matched {
		t.Fatalf("totally-unknown-model = %+v, want matched=false", unknown)
	}
}

func TestReferencePriceLookup_EmptyModelsList_400(t *testing.T) {
	adminSrv := newAdminRouterForPriceLookup(t)

	raw := []byte(`{"upstream_models":[]}`)
	req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/pricesync/reference-price-lookup", bytes.NewReader(raw))
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

func TestReferencePriceLookup_BothSourcesUnavailable_502(t *testing.T) {
	litellmSrv := fake500Server(t)
	openrouterSrv := fake500Server(t)
	adminSrv := newAdminRouterForPriceLookup(t)

	raw := []byte(`{"litellm_dataset_url":"` + litellmSrv.URL + `","openrouter_models_url":"` + openrouterSrv.URL + `","upstream_models":["gpt-4o"]}`)
	req, err := http.NewRequest(http.MethodPost, adminSrv.URL+"/pricesync/reference-price-lookup", bytes.NewReader(raw))
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

func TestReferencePriceLookup_OneSourceDown_StillReturnsTheOtherOnesMatches(t *testing.T) {
	litellmSrv := fakeJSONServer(t, litellmDatasetFixture)
	openrouterSrv := fake500Server(t)
	adminSrv := newAdminRouterForPriceLookup(t)
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	var out priceLookupResponse
	ac.post("/pricesync/reference-price-lookup", map[string]any{
		"litellm_dataset_url":   litellmSrv.URL,
		"openrouter_models_url": openrouterSrv.URL,
		"upstream_models":       []string{"gpt-4o"},
	}, &out)

	gpt4o, ok := out.Data["gpt-4o"]
	if !ok || !gpt4o.Matched || gpt4o.Source != "litellm" {
		t.Fatalf("gpt-4o = %+v, want matched=true source=litellm even though openrouter is down", gpt4o)
	}
	if out.OpenRouterError == "" {
		t.Error("expected openrouter_error to be set when that source failed")
	}
}
