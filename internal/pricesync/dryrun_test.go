package pricesync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

func TestDryRun_HTMLTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pricingPageFixture))
	}))
	defer srv.Close()
	env := datasync.NewEnv(datasync.EnvOptions{AllowPrivateNetworks: true, MinHostInterval: -1})

	cfg := map[string]any{
		"row_selector": "table.pricing tbody tr", "model_name_selector": "td.name", "currency": "USD",
		"columns": []any{
			map[string]any{"selector": "td.in", "meter": "input", "unit": "per_1m_tokens"},
			map[string]any{"selector": "td.out", "meter": "output", "unit": "per_1m_tokens"},
		},
	}
	res, err := DryRun(context.Background(), env, DryRunInput{Fetcher: "html_table", URL: srv.URL, Config: cfg, SampleSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || res.Count != 3 || len(res.Sample) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if got := res.Sample[0].Prices["input/per_1m_tokens"]; got != "3" {
		t.Errorf("sample[0] input = %q, want 3 (%+v)", got, res.Sample[0])
	}

	// 选择器失效：抓取成功但解析出 0 条 → 告警而不是错误。
	cfg["row_selector"] = "table.missing tr"
	res, err = DryRun(context.Background(), env, DryRunInput{Fetcher: "html_table", URL: srv.URL, Config: cfg})
	if err != nil || res.Count != 0 || len(res.Warnings) == 0 {
		t.Fatalf("broken selector: res=%+v err=%v", res, err)
	}

	if _, err := DryRun(context.Background(), env, DryRunInput{Fetcher: "offer_page", URL: srv.URL}); !errors.Is(err, ErrUnsupportedFetcher) {
		t.Errorf("offer_page: err = %v, want ErrUnsupportedFetcher", err)
	}
}
