package pricesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// litellmDatasetFixture 是按 LiteLLM model_prices_and_context_window.json 公开
// 文档的字段手工构造的样例（不是真的下载到的数据集——本包不发网络请求做测试，
// 见包级注释），但字段名/单位/取值形态都按其文档核对：价格是"每 token 的
// 美元"JSON 数字，不是字符串；sample_spec 是一条固定存在的占位说明条目。
const litellmDatasetFixture = `{
  "sample_spec": {
    "input_cost_per_token": 0.0000001,
    "output_cost_per_token": 0.0000002
  },
  "gpt-4o": {
    "max_tokens": 4096,
    "input_cost_per_token": 0.0000025,
    "output_cost_per_token": 0.00001,
    "cache_read_input_token_cost": 0.00000125,
    "litellm_provider": "openai",
    "mode": "chat"
  },
  "claude-sonnet-4-5": {
    "input_cost_per_token": 0.000003,
    "output_cost_per_token": 0.000015,
    "cache_read_input_token_cost": 0.0000003,
    "cache_creation_input_token_cost": 0.00000375,
    "litellm_provider": "anthropic"
  },
  "metadata-only-model": {
    "max_tokens": 8192,
    "litellm_provider": "someprovider",
    "mode": "chat"
  },
  "malformed-model": "not-an-object"
}`

func TestNormalizeLiteLLMResponse_GoldenFixture(t *testing.T) {
	obs, skipped, err := normalizeLiteLLMResponse([]byte(litellmDatasetFixture))
	if err != nil {
		t.Fatalf("normalizeLiteLLMResponse: %v", err)
	}

	if _, ok := skipped["malformed-model"]; !ok {
		t.Error("expected malformed-model to be skipped with an error (value is a string, not an object)")
	}

	byModel := map[string][]Component{}
	for _, o := range obs {
		byModel[o.UpstreamModel] = o.Spec.Components
	}

	if _, ok := byModel[litellmSampleSpecKey]; ok {
		t.Error("sample_spec must never be treated as a real model")
	}
	if _, ok := byModel["metadata-only-model"]; ok {
		t.Error("a model with no recognized price fields should not produce an observation")
	}

	gpt4o, ok := byModel["gpt-4o"]
	if !ok {
		t.Fatal("expected an observation for gpt-4o")
	}
	if len(gpt4o) != 3 {
		t.Fatalf("gpt-4o components = %+v, want 3 (input/output/cache_read)", gpt4o)
	}
	byMeter := map[pricing.Meter]decimal.Decimal{}
	for _, c := range gpt4o {
		byMeter[c.Meter] = c.UnitPrice
		if c.Unit != pricing.UnitPer1MTokens {
			t.Errorf("meter %s unit = %q, want per_1m_tokens", c.Meter, c.Unit)
		}
	}
	// 0.0000025 USD/token * 1,000,000 = 2.5 USD/百万 token。
	if want := decimal.NewFromFloat(2.5); !byMeter[pricing.MeterInput].Equal(want) {
		t.Errorf("input price = %s, want %s", byMeter[pricing.MeterInput], want)
	}
	if want := decimal.NewFromInt(10); !byMeter[pricing.MeterOutput].Equal(want) {
		t.Errorf("output price = %s, want %s", byMeter[pricing.MeterOutput], want)
	}
	if want := decimal.NewFromFloat(1.25); !byMeter[pricing.MeterInputCacheRead].Equal(want) {
		t.Errorf("cache_read price = %s, want %s", byMeter[pricing.MeterInputCacheRead], want)
	}

	claude, ok := byModel["claude-sonnet-4-5"]
	if !ok {
		t.Fatal("expected an observation for claude-sonnet-4-5")
	}
	if len(claude) != 4 {
		t.Fatalf("claude components = %+v, want 4 (input/output/cache_read/cache_write)", claude)
	}
}

func TestNormalizeLiteLLM_NoRecognizedFieldsReturnsEmpty(t *testing.T) {
	got, err := normalizeLiteLLM(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("normalizeLiteLLM: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty", got)
	}
}

func TestLiteLLMFetcher_Fetch_RequiresURL(t *testing.T) {
	fetcher := &LiteLLMFetcher{}
	if _, err := fetcher.Fetch(context.Background(), Source{}); err == nil {
		t.Error("expected an error when source has no URL")
	}
}

func TestLiteLLMFetcher_Fetch_ParsesLocalTestServerResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(litellmDatasetFixture))
	}))
	defer srv.Close()

	fetcher := &LiteLLMFetcher{}
	obs, err := fetcher.Fetch(context.Background(), Source{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(obs) != 2 {
		t.Errorf("observations = %+v, want 2 (gpt-4o, claude-sonnet-4-5)", obs)
	}
}

func TestLiteLLMFetcher_Fetch_PropagatesNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fetcher := &LiteLLMFetcher{}
	if _, err := fetcher.Fetch(context.Background(), Source{URL: srv.URL}); err == nil {
		t.Error("expected an error for a non-200 upstream response")
	}
}
