package pricesync

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// openRouterModelsFixture 是按 OpenRouter GET /api/v1/models 公开文档的字段
// 手工构造的样例（不是抓取到的真实响应——本包没有接网络，见包级注释），但字段名/
// 单位/取值形态都按其文档核对：pricing 下的每个数值都是"USD/token"的十进制
// 字符串，空字符串表示该模型不支持这个计量项。用来把 normalizeOpenRouter
// 当作一个稳定的 golden fixture 测试，而不是每次都手写内联 JSON。
const openRouterModelsFixture = `{
  "data": [
    {
      "id": "anthropic/claude-sonnet-4.5",
      "pricing": {
        "prompt": "0.000003",
        "completion": "0.000015",
        "input_cache_read": "0.0000003",
        "input_cache_write": "0.00000375",
        "internal_reasoning": "0.000015",
        "image": "0.0048",
        "web_search": "0.01"
      }
    },
    {
      "id": "some-provider/free-model",
      "pricing": {
        "prompt": "0",
        "completion": "0"
      }
    },
    {
      "id": "some-provider/no-cache-support",
      "pricing": {
        "prompt": "0.000001",
        "completion": "0.000002",
        "input_cache_read": ""
      }
    },
    {
      "id": "some-provider/malformed",
      "pricing": {
        "prompt": "not-a-number"
      }
    }
  ]
}`

func TestNormalizeOpenRouterResponse_GoldenFixture(t *testing.T) {
	obs, skipped, err := normalizeOpenRouterResponse([]byte(openRouterModelsFixture))
	if err != nil {
		t.Fatalf("normalizeOpenRouterResponse: %v", err)
	}

	if err := skipped["some-provider/malformed"]; err == nil {
		t.Error("expected some-provider/malformed to be skipped with an error (pricing.prompt is not a number)")
	}
	if len(skipped) != 1 {
		t.Errorf("skipped = %v, want exactly 1 entry", skipped)
	}

	byModel := map[string][]Component{}
	for _, o := range obs {
		byModel[o.UpstreamModel] = o.Spec.Components
	}

	claude, ok := byModel["anthropic/claude-sonnet-4.5"]
	if !ok {
		t.Fatal("expected an observation for anthropic/claude-sonnet-4.5")
	}
	// image/web_search 没有对应的 pricing.Meter，应该被丢弃，不产生假计量项。
	if len(claude) != 5 {
		t.Fatalf("claude components = %+v, want exactly 5 (prompt/completion/cache_read/cache_write/reasoning)", claude)
	}
	byMeter := map[pricing.Meter]Component{}
	for _, c := range claude {
		byMeter[c.Meter] = c
	}
	// 0.000003 USD/token * 1,000,000 = 3 USD/百万 token。
	if want := decimal.NewFromInt(3); !byMeter[pricing.MeterInput].UnitPrice.Equal(want) {
		t.Errorf("input unit_price = %s, want %s", byMeter[pricing.MeterInput].UnitPrice, want)
	}
	if want := decimal.NewFromInt(15); !byMeter[pricing.MeterOutput].UnitPrice.Equal(want) {
		t.Errorf("output unit_price = %s, want %s", byMeter[pricing.MeterOutput].UnitPrice, want)
	}
	if want := decimal.NewFromFloat(0.3); !byMeter[pricing.MeterInputCacheRead].UnitPrice.Equal(want) {
		t.Errorf("input_cache_read unit_price = %s, want %s", byMeter[pricing.MeterInputCacheRead].UnitPrice, want)
	}
	if want := decimal.NewFromFloat(3.75); !byMeter[pricing.MeterInputCacheWrite].UnitPrice.Equal(want) {
		t.Errorf("input_cache_write unit_price = %s, want %s", byMeter[pricing.MeterInputCacheWrite].UnitPrice, want)
	}
	if want := decimal.NewFromInt(15); !byMeter[pricing.MeterOutputReasoning].UnitPrice.Equal(want) {
		t.Errorf("internal_reasoning unit_price = %s, want %s", byMeter[pricing.MeterOutputReasoning].UnitPrice, want)
	}
	for _, c := range claude {
		if c.Unit != pricing.UnitPer1MTokens {
			t.Errorf("meter %s unit = %q, want per_1m_tokens", c.Meter, c.Unit)
		}
	}

	free, ok := byModel["some-provider/free-model"]
	if !ok {
		t.Fatal("expected an observation for some-provider/free-model")
	}
	if len(free) != 2 {
		t.Fatalf("free-model components = %+v, want 2 (a literal \"0\" price is still a real, present price)", free)
	}
	for _, c := range free {
		if !c.UnitPrice.IsZero() {
			t.Errorf("free-model meter %s price = %s, want 0", c.Meter, c.UnitPrice)
		}
	}

	noCache, ok := byModel["some-provider/no-cache-support"]
	if !ok {
		t.Fatal("expected an observation for some-provider/no-cache-support")
	}
	if len(noCache) != 2 {
		t.Fatalf("no-cache-support components = %+v, want exactly 2 (empty string means \"unsupported\", not a 0 price)", noCache)
	}
}

func TestNormalizeOpenRouter_EmptyPricingProducesNoComponents(t *testing.T) {
	got, err := normalizeOpenRouter(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("normalizeOpenRouter: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty", got)
	}
}

func TestNormalizeOpenRouter_RejectsNonNumericPrice(t *testing.T) {
	_, err := normalizeOpenRouter(map[string]json.RawMessage{"prompt": json.RawMessage(`"abc"`)})
	if err == nil {
		t.Error("expected an error for a non-numeric price string")
	}
}
