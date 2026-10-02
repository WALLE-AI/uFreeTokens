package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

// TestMedia_ImportByComponentsAndMargins 覆盖多模态技术方案 §5：按计量项导入图像/语音
// 模型、按类型校验必需计量项与 tts/asr 能力、渠道 param_overrides、以及列表毛利
// （Go 的 marginRatio 与 v_admin_channel_margin 视图）把多模态计量项算进去。
func TestMedia_ImportByComponentsAndMargins(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	f := newB2Fixture(t, ac) // 假币种 → CNY 汇率 7，上游账号倍率 1.2
	img, tts := "media-img-"+f.suffix, "media-tts-"+f.suffix
	comp := func(meter, unit, price string) map[string]any {
		return map[string]any{"meter": meter, "unit": unit, "price": price}
	}
	body := map[string]any{
		"currency": f.currency, "markup_percent": "25",
		"items": []map[string]any{
			{"upstream_model": img, "family": "kolors", "type": "image", "context_window": 1, "max_output": 1,
				"cost_components": []any{comp("image", "per_image", "0.1")}},
			{"upstream_model": tts, "family": "cosy", "type": "audio", "capabilities": []string{"tts"}, "context_window": 1, "max_output": 1,
				"cost_components": []any{comp("input_char", "per_1m_chars", "10")},
				"param_overrides": map[string]any{"$voice_prefix_upstream_model": true}},
			// audio 没声明 tts/asr
			{"upstream_model": "media-bad-audio-" + f.suffix, "family": "x", "type": "audio", "context_window": 1, "max_output": 1,
				"cost_components": []any{comp("audio_second", "per_second", "0.01")}},
			// image 按 token 定价，缺少 image 计量项
			{"upstream_model": "media-bad-img-" + f.suffix, "family": "x", "type": "image", "context_window": 1, "max_output": 1,
				"cost_input": "1", "cost_output": "1"},
		},
	}
	type item struct {
		admin.ImportModelPlan
		OK    bool                      `json:"ok"`
		Error *struct{ Message string } `json:"error"`
	}
	var dry struct{ Items []item }
	body["dry_run"] = true
	ac.post(fmt.Sprintf("/provider-accounts/%d/import-models", f.accountID), body, &dry)
	if len(dry.Items) != 4 {
		t.Fatalf("dry run = %+v", dry.Items)
	}
	// 0.1 × 7 × 1.2 = 0.84 → ×1.25 = 1.05，毛利率 1 - 0.84/1.05 = 0.2
	if it := dry.Items[0]; !it.OK || len(it.Components) != 1 || it.Components[0].Sell.String() != "1.05" || it.MarginRatio == nil || it.MarginRatio.String() != "0.2" {
		t.Errorf("image plan = %+v", it)
	}
	if it := dry.Items[1]; !it.OK || it.Components[0].Sell.String() != "105" {
		t.Errorf("tts plan = %+v, want sell 105", it)
	}
	if it := dry.Items[2]; it.OK || !strings.Contains(strings.Join(it.Errors, ","), "tts") {
		t.Errorf("audio without tts/asr = %+v, want capability error", it)
	}
	if it := dry.Items[3]; it.OK || !strings.Contains(strings.Join(it.Errors, ","), "image") {
		t.Errorf("image priced by tokens = %+v, want missing-meter error", it)
	}

	// meter 与 unit 不匹配直接 400
	bad := map[string]any{"currency": "CNY", "markup_percent": "0", "dry_run": true, "items": []map[string]any{
		{"upstream_model": "x", "family": "x", "type": "image", "context_window": 1, "max_output": 1,
			"cost_components": []any{comp("image", "per_1m_tokens", "1")}}}}
	if status, b := ac.do(http.MethodPost, fmt.Sprintf("/provider-accounts/%d/import-models", f.accountID), bad, nil); status != http.StatusBadRequest {
		t.Errorf("image priced per_1m_tokens: status = %d body = %s, want 400", status, b)
	}

	body["dry_run"] = false
	body["items"] = body["items"].([]map[string]any)[:2]
	var real struct{ Items []item }
	ac.post(fmt.Sprintf("/provider-accounts/%d/import-models", f.accountID), body, &real)
	for i, it := range real.Items {
		if !it.OK {
			t.Fatalf("import item %d failed: %+v", i, it)
		}
	}

	var overrides []byte
	if err := pool.QueryRow(t.Context(), `SELECT param_overrides FROM channels WHERE upstream_model = $1`, tts).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	var ov map[string]any
	_ = json.Unmarshal(overrides, &ov)
	if ov["$voice_prefix_upstream_model"] != true {
		t.Errorf("tts channel param_overrides = %s", overrides)
	}
	var caps []string
	_ = pool.QueryRow(t.Context(), `SELECT capabilities FROM virtual_models WHERE name = $1`, tts).Scan(&caps)
	if len(caps) != 1 || caps[0] != "tts" {
		t.Errorf("tts capabilities = %v", caps)
	}

	// 列表毛利（Go）与视图毛利（SQL）都应是 0.2
	var page admin.Page[admin.ChannelSummary]
	ac.get(fmt.Sprintf("/channels?provider_account_id=%d&q=%s", f.accountID, img), &page)
	if len(page.Data) != 1 || page.Data[0].MarginRatio == nil || page.Data[0].MarginRatio.String() != "0.2" {
		t.Fatalf("image channel list = %+v", page.Data)
	}
	if sp := page.Data[0].SellPrice; sp == nil || len(sp.Media) != 1 || sp.Media[0].Meter != "image" {
		t.Errorf("sell price media = %+v", sp)
	}
	var viewMargin *string
	if err := pool.QueryRow(t.Context(), `SELECT m.margin_ratio::text FROM v_admin_channel_margin m WHERE m.channel_id = $1`, page.Data[0].ID).Scan(&viewMargin); err != nil {
		t.Fatal(err)
	}
	if viewMargin == nil || *viewMargin != "0.2000" {
		t.Errorf("view margin = %v, want 0.2000", viewMargin)
	}

	// 纠正录错的类型：改成 audio 必须同时给出 tts/asr 能力
	var vm idResp
	ac.post("/virtual-models", map[string]any{"name": "media-fix-" + f.suffix, "family": "x", "type": "chat", "context_window": 1, "max_output": 1}, &vm)
	if status, b := ac.do(http.MethodPatch, fmt.Sprintf("/virtual-models/%d", vm.ID), map[string]any{"type": "audio"}, nil); status != http.StatusBadRequest {
		t.Errorf("type=audio without tts/asr: status = %d body = %s, want 400", status, b)
	}
	if status, b := ac.do(http.MethodPatch, fmt.Sprintf("/virtual-models/%d", vm.ID), map[string]any{"type": "audio", "capabilities": []string{"asr"}}, nil); status != http.StatusOK {
		t.Fatalf("type=audio with asr: status = %d body = %s", status, b)
	}
	var typ string
	_ = pool.QueryRow(t.Context(), `SELECT type FROM virtual_models WHERE id = $1`, vm.ID).Scan(&typ)
	if typ != "audio" {
		t.Errorf("type = %q, want audio", typ)
	}

	// 直接创建 audio 模型也要求 tts/asr 之一
	if status, _ := ac.do(http.MethodPost, "/virtual-models", map[string]any{"name": "media-raw-" + f.suffix, "family": "x", "type": "audio",
		"context_window": 1, "max_output": 1}, nil); status != http.StatusBadRequest {
		t.Errorf("create audio vm without tts/asr: status = %d, want 400", status)
	}
}
