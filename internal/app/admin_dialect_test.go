package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDialect_AdminLifecycle 覆盖多供应商实施方案 §3.3 / §7：新建账号时带方言、读取
// 生效配置与端点支持、保存非法方言被拒、历史版本、导入与新建渠道的上架校验。
func TestDialect_AdminLifecycle(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	suffix := fmt.Sprint(time.Now().UnixNano())

	var presets struct {
		Data []struct{ Name, Notes string } `json:"data"`
	}
	ac.get("/meta/dialect-presets", &presets)
	names := map[string]bool{}
	for _, p := range presets.Data {
		names[p.Name] = true
	}
	if !names["openrouter"] || !names["dashscope"] || !names["volcengine"] {
		t.Fatalf("presets = %+v", presets)
	}

	var p, a idResp
	ac.post("/providers", map[string]any{"code": "dl-" + suffix, "name": "DL", "protocol": "openai"}, &p)
	ac.post("/provider-accounts", map[string]any{"provider_id": p.ID, "name": "dl-acc", "base_url": "https://dl.example/v1",
		"dialect": map[string]any{"preset": "volcengine"}}, &a)

	type dialectResp struct {
		Dialect   json.RawMessage `json:"dialect"`
		Effective struct {
			Preset string `json:"preset"`
		} `json:"effective"`
		Endpoints map[string]bool `json:"endpoints"`
		History   []any           `json:"history"`
	}
	var d dialectResp
	ac.get(fmt.Sprintf("/provider-accounts/%d/dialect", a.ID), &d)
	if d.Effective.Preset != "volcengine" || d.Endpoints["rerank"] || !d.Endpoints["images"] || !d.Endpoints["chat"] {
		t.Fatalf("dialect = %+v", d)
	}

	// 非法方言：400，原配置不变
	if status, b := ac.do(http.MethodPut, fmt.Sprintf("/provider-accounts/%d/dialect", a.ID),
		map[string]any{"dialect": map[string]any{"endpoints": map[string]any{"images": map[string]any{"codec": "nope"}}}}, nil); status != http.StatusBadRequest {
		t.Errorf("invalid dialect: status = %d body = %s", status, b)
	}

	// 改成 openrouter 预设：rerank 变为支持，旧值进历史
	status, b := ac.do(http.MethodPut, fmt.Sprintf("/provider-accounts/%d/dialect", a.ID), map[string]any{"dialect": map[string]any{"preset": "openrouter"}}, nil)
	if status != http.StatusOK {
		t.Fatalf("set dialect: %d %s", status, b)
	}
	_ = json.Unmarshal(b, &d)
	if d.Effective.Preset != "openrouter" || !d.Endpoints["rerank"] || len(d.History) != 1 {
		t.Errorf("after update = %+v", d)
	}

	// 回到方舟预设，导入 rerank 模型被拒，新建 rerank 渠道被拒
	ac.do(http.MethodPut, fmt.Sprintf("/provider-accounts/%d/dialect", a.ID), map[string]any{"dialect": map[string]any{"preset": "volcengine"}}, nil)
	var plan struct {
		Items []struct {
			Errors []string `json:"errors"`
		} `json:"items"`
	}
	ac.post(fmt.Sprintf("/provider-accounts/%d/import-models", a.ID), map[string]any{
		"dry_run": true, "currency": "CNY", "markup_percent": "30",
		"items": []map[string]any{{"upstream_model": "dl-rerank-" + suffix, "family": "x", "type": "rerank", "context_window": 1, "max_output": 1,
			"cost_components": []any{map[string]any{"meter": "input", "unit": "per_1m_tokens", "price": "1"}}}},
	}, &plan)
	if len(plan.Items) != 1 || !strings.Contains(strings.Join(plan.Items[0].Errors, ","), "不支持重排序") {
		t.Errorf("import plan = %+v", plan)
	}
	var vm idResp
	ac.post("/virtual-models", map[string]any{"name": "dl-vm-" + suffix, "family": "x", "type": "rerank", "context_window": 1, "max_output": 1}, &vm)
	if status, b := ac.do(http.MethodPost, "/channels", map[string]any{"virtual_model_id": vm.ID, "provider_account_id": a.ID, "upstream_model": "r"}, nil); status != http.StatusBadRequest {
		t.Errorf("create unsupported channel: status = %d body = %s", status, b)
	}

	// 删除方言：恢复纯透传
	status, b = ac.do(http.MethodPut, fmt.Sprintf("/provider-accounts/%d/dialect", a.ID), map[string]any{"dialect": nil}, nil)
	_ = json.Unmarshal(b, &d)
	if status != http.StatusOK || string(d.Dialect) != "null" || !d.Endpoints["rerank"] {
		t.Errorf("clear dialect: %d %s", status, b)
	}
}
