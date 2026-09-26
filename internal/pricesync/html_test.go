package pricesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// pricingPageFixture 是手工构造的定价页表格片段（不是真的抓取到的页面——本包
// 不发真实的网络请求做测试，见包级注释），但结构上模拟了常见的定价页布局：
// 表头行（应该被跳过，取不到模型名）、正常一行、一个缺某一列（该模型不支持
// 缓存计价）、货币符号 + 千分位逗号混进价格文本里、一行整行是空白/占位（应该
// 被跳过）。
const pricingPageFixture = `
<html><body>
<table class="pricing">
  <thead><tr><th>Model</th><th>Input</th><th>Output</th><th>Cache Read</th></tr></thead>
  <tbody>
    <tr><td class="name">GPT Ultra</td><td class="in">$3.00</td><td class="out">$15.00</td><td class="cache">$0.30</td></tr>
    <tr><td class="name">GPT Mini</td><td class="in">$0.15</td><td class="out">$0.60</td><td class="cache"></td></tr>
    <tr><td class="name"></td><td class="in">--</td><td class="out">--</td><td class="cache">--</td></tr>
    <tr><td class="name">Big Number</td><td class="in">¥1,234.50</td><td class="out">¥9,876.00</td><td class="cache"></td></tr>
  </tbody>
</table>
</body></html>`

func testTableConfig() HTMLTableConfig {
	return HTMLTableConfig{
		RowSelector:       "table.pricing tbody tr",
		ModelNameSelector: "td.name",
		ModelNameMap:      map[string]string{"GPT Ultra": "gpt-ultra-2025"},
		Currency:          "USD",
		Columns: []HTMLPriceColumn{
			{Selector: "td.in", Meter: string(pricing.MeterInput), Unit: string(pricing.UnitPer1MTokens)},
			{Selector: "td.out", Meter: string(pricing.MeterOutput), Unit: string(pricing.UnitPer1MTokens)},
			{Selector: "td.cache", Meter: string(pricing.MeterInputCacheRead), Unit: string(pricing.UnitPer1MTokens)},
		},
	}
}

func TestParseHTMLPriceTable_GoldenFixture(t *testing.T) {
	obs, err := ParseHTMLPriceTable([]byte(pricingPageFixture), testTableConfig())
	if err != nil {
		t.Fatalf("ParseHTMLPriceTable: %v", err)
	}
	// 4 行数据里：表头不算(在 thead 里，不匹配 tbody tr 选择器)；一行模型名是空
	// 字符串应该被跳过；应该剩 3 条观测（Ultra/Mini/Big Number）。
	if len(obs) != 3 {
		t.Fatalf("observations = %+v, want exactly 3", obs)
	}

	byModel := map[string]Observation{}
	for _, o := range obs {
		byModel[o.UpstreamModel] = o
	}

	ultra, ok := byModel["gpt-ultra-2025"]
	if !ok {
		t.Fatal("expected ModelNameMap to translate 'GPT Ultra' -> 'gpt-ultra-2025'")
	}
	if len(ultra.Spec.Components) != 3 {
		t.Fatalf("ultra components = %+v, want 3 (input/output/cache_read all present)", ultra.Spec.Components)
	}
	if ultra.Spec.Currency != "USD" {
		t.Errorf("currency = %q, want USD", ultra.Spec.Currency)
	}
	byMeter := map[pricing.Meter]decimal.Decimal{}
	for _, c := range ultra.Spec.Components {
		byMeter[c.Meter] = c.UnitPrice
	}
	if !byMeter[pricing.MeterInput].Equal(decimal.NewFromFloat(3.00)) {
		t.Errorf("input price = %s, want 3.00 (stripped $ sign)", byMeter[pricing.MeterInput])
	}
	if !byMeter[pricing.MeterInputCacheRead].Equal(decimal.NewFromFloat(0.30)) {
		t.Errorf("cache_read price = %s, want 0.30", byMeter[pricing.MeterInputCacheRead])
	}

	mini, ok := byModel["GPT Mini"] // 没在 ModelNameMap 里，原样当 upstream_model
	if !ok {
		t.Fatal("expected an observation for GPT Mini")
	}
	if len(mini.Spec.Components) != 2 {
		t.Errorf("mini components = %+v, want exactly 2 (empty cache cell should be skipped, not treated as 0)", mini.Spec.Components)
	}

	big, ok := byModel["Big Number"]
	if !ok {
		t.Fatal("expected an observation for Big Number")
	}
	for _, c := range big.Spec.Components {
		if c.Meter == pricing.MeterInput && !c.UnitPrice.Equal(decimal.NewFromFloat(1234.50)) {
			t.Errorf("Big Number input price = %s, want 1234.50 (stripped currency symbol + thousands comma)", c.UnitPrice)
		}
	}
}

func TestParseHTMLPriceTable_AppliesMultiplier(t *testing.T) {
	html := `<table class="pricing"><tbody>
		<tr><td class="name">m</td><td class="in">0.003</td><td class="out">0.006</td><td class="cache"></td></tr>
	</tbody></table>`
	cfg := testTableConfig()
	cfg.Columns[0].Multiplier = 1000 // 页面写的是"每千 token"，换算成 per_1m_tokens 要乘 1000
	obs, err := ParseHTMLPriceTable([]byte(html), cfg)
	if err != nil {
		t.Fatalf("ParseHTMLPriceTable: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("observations = %+v, want 1", obs)
	}
	for _, c := range obs[0].Spec.Components {
		if c.Meter == pricing.MeterInput && !c.UnitPrice.Equal(decimal.NewFromInt(3)) {
			t.Errorf("input price = %s, want 3 (0.003 * 1000 multiplier)", c.UnitPrice)
		}
	}
}

func TestParseHTMLPriceTable_MinModelsGuardsAgainstStructureChange(t *testing.T) {
	cfg := testTableConfig()
	cfg.MinModels = 10 // 网页改版、选择器全失效的模拟：只解析出 3 条，远低于历史正常值
	_, err := ParseHTMLPriceTable([]byte(pricingPageFixture), cfg)
	if err == nil {
		t.Fatal("expected an error when parsed model count is below min_models")
	}
}

func TestParseHTMLPriceTable_UnparsablePriceReturnsError(t *testing.T) {
	html := `<table class="pricing"><tbody>
		<tr><td class="name">m</td><td class="in">contact sales</td><td class="out">0.006</td><td class="cache"></td></tr>
	</tbody></table>`
	_, err := ParseHTMLPriceTable([]byte(html), testTableConfig())
	if err == nil {
		t.Fatal("expected an error for a non-numeric price cell")
	}
}

func TestParseHTMLPriceTable_RequiresSelectors(t *testing.T) {
	if _, err := ParseHTMLPriceTable([]byte("<html></html>"), HTMLTableConfig{}); err == nil {
		t.Error("expected an error for a config with no selectors")
	}
}

func TestParsePriceText(t *testing.T) {
	cases := map[string]string{
		"$3.00":      "3",
		"¥1,234.50":  "1234.5",
		"  0.003  ":  "0.003",
		"USD 12.500": "12.500",
	}
	for in, want := range cases {
		got, err := parsePriceText(in)
		if err != nil {
			t.Errorf("parsePriceText(%q): %v", in, err)
			continue
		}
		if !got.Equal(decimalFromString(t, want)) {
			t.Errorf("parsePriceText(%q) = %s, want %s", in, got, want)
		}
	}
	if _, err := parsePriceText("contact sales"); err == nil {
		t.Error("expected an error for non-numeric text")
	}
	if _, err := parsePriceText(""); err == nil {
		t.Error("expected an error for empty text")
	}
}

func decimalFromString(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("decimal.NewFromString(%q): %v", s, err)
	}
	return d
}

// TestHTMLFetcher_Fetch_ParsesLocalTestServerResponse 验证 HTMLFetcher.Fetch 的
// HTTP 接线（GET URL、读 body、按 source.config 解析）是对的——用本地
// httptest.Server 提供同一份 fixture，不是真的请求外部网站。
func TestHTMLFetcher_Fetch_ParsesLocalTestServerResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("expected a non-empty User-Agent identifying the fetcher")
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(pricingPageFixture))
	}))
	defer srv.Close()

	cfg := testTableConfig()
	cfgMap := map[string]any{
		"row_selector": cfg.RowSelector, "model_name_selector": cfg.ModelNameSelector,
		"model_name_map": cfg.ModelNameMap, "currency": cfg.Currency,
		"columns": []map[string]any{
			{"selector": "td.in", "meter": "input", "unit": "per_1m_tokens"},
			{"selector": "td.out", "meter": "output", "unit": "per_1m_tokens"},
			{"selector": "td.cache", "meter": "input_cache_read", "unit": "per_1m_tokens"},
		},
	}

	fetcher := &HTMLFetcher{}
	obs, err := fetcher.Fetch(context.Background(), Source{URL: srv.URL, Config: cfgMap})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(obs) != 3 {
		t.Errorf("observations = %+v, want 3", obs)
	}
}

func TestHTMLFetcher_Fetch_RejectsMissingURL(t *testing.T) {
	fetcher := &HTMLFetcher{}
	if _, err := fetcher.Fetch(context.Background(), Source{Config: map[string]any{}}); err == nil {
		t.Error("expected an error when source has no URL")
	}
}

func TestHTMLFetcher_Fetch_PropagatesNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	fetcher := &HTMLFetcher{}
	_, err := fetcher.Fetch(context.Background(), Source{URL: srv.URL, Config: map[string]any{
		"row_selector": "tr", "model_name_selector": "td", "columns": []map[string]any{{"selector": "td", "meter": "input", "unit": "per_1m_tokens"}},
	}})
	if err == nil {
		t.Error("expected an error for a non-200 upstream response")
	}
}
