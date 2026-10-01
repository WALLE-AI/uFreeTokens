// 端到端测试：基准测试与排行榜（docs/基准测试与排行榜数据服务技术方案.md）。
// 运营后台录入基准 → 发布 run → 公开接口 GET /v1/benchmarks 可见；scores 键名白名单；
// 账户"不计入公开榜单"标记；/v1/rankings/* 的参数校验、总开关与缓存头。
package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

func publicGet(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func errorCode(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Error.Code
}

func TestMetadataScores_Whitelist(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"name": fmt.Sprintf("scores-%d", time.Now().UnixNano()), "family": "test", "type": "chat",
		"context_window": 8192, "max_output": 1024, "visible_tiers": []string{"free"},
	}, &vm)
	path := fmt.Sprintf("/virtual-models/%d/metadata", vm.ID)
	for name, scores := range map[string]any{
		"camelCase":          map[string]any{"intelligenceIndex": 50},
		"unknown key":        map[string]any{"elo": 1200},
		"not a number":       map[string]any{"coding_index": "high"},
		"unknown arena key":  map[string]any{"design_arena": map[string]any{"music": 1}},
		"arena not object":   map[string]any{"design_arena": 1300},
		"arena not a number": map[string]any{"design_arena": map[string]any{"svg": "1300"}},
	} {
		if status, body := ac.do(http.MethodPut, path, map[string]any{"scores": scores}, nil); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, body = %s; want 400", name, status, body)
		}
	}
	ok := map[string]any{"intelligence_index": 49.5, "coding_index": 76, "agentic_index": 51,
		"design_arena": map[string]any{"code": 1320, "ui_component": 1335, "three_d": 1360}}
	if status, body := ac.do(http.MethodPut, path, map[string]any{"scores": ok}, nil); status != http.StatusOK {
		t.Fatalf("valid scores: status = %d, body = %s", status, body)
	}
	if status, body := ac.do(http.MethodPut, path, map[string]any{"scores": nil}, nil); status != http.StatusOK {
		t.Fatalf("clearing scores: status = %d, body = %s", status, body)
	}
}

func TestAccount_ExcludeFromPublicStats(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	var acct struct {
		ID      int64 `json:"id"`
		Exclude bool  `json:"exclude_from_public_stats"`
	}
	ac.post("/accounts", map[string]any{"type": "personal", "name": "eval-account", "exclude_from_public_stats": true}, &acct)
	if !acct.Exclude {
		t.Fatal("create: exclude_from_public_stats not persisted")
	}
	status, body := ac.do(http.MethodPatch, fmt.Sprintf("/accounts/%d", acct.ID), map[string]any{"exclude_from_public_stats": false}, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"exclude_from_public_stats":false`) {
		t.Fatalf("patch: status = %d, body = %s", status, body)
	}
}

func TestBenchmarks_AdminToPublic(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, false)
	defer done()
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Logger: logger, PG: pool, Catalog: catalog.NewStore(pool, testBox(t), 0)}))
	defer gw.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	slug := "bench-" + suffix
	vmName := "bench-model-" + suffix
	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"name": vmName, "family": "test", "type": "chat", "context_window": 8192, "max_output": 1024, "visible_tiers": []string{"free"},
	}, &vm)

	if status, body := ac.do(http.MethodPost, "/benchmarks", map[string]any{"slug": "Bad Slug", "name": "x", "category": "reasoning", "metric_name": "accuracy"}, nil); status != http.StatusBadRequest {
		t.Errorf("invalid slug: status = %d, body = %s", status, body)
	}
	var b struct {
		ID      int64 `json:"id"`
		Version int   `json:"version"`
	}
	ac.post("/benchmarks", map[string]any{
		"slug": slug, "name": "GPQA Diamond " + suffix, "category": "reasoning", "metric_name": "accuracy",
		"source_name": "Example", "source_url": "https://example.com/gpqa",
	}, &b)

	// 草稿基准：公开接口不可见。
	if status, _, _ := publicGet(t, gw.URL+"/v1/benchmarks/"+slug); status != http.StatusNotFound {
		t.Errorf("draft benchmark: public status = %d, want 404", status)
	}
	if status, body := ac.do(http.MethodPatch, fmt.Sprintf("/benchmarks/%d", b.ID), map[string]any{"status": "published"}, nil); status != http.StatusOK {
		t.Fatalf("publish benchmark: %d %s", status, body)
	}

	results := []map[string]any{
		{"model_label": "Model A", "virtual_model": vmName, "score": 90, "cost_per_task_micro": 900, "avg_duration_ms": 9000},
		{"model_label": "Model B", "score": 80, "cost_per_task_micro": 100, "avg_duration_ms": 1000, "error_rate": 0.01},
		{"model_label": "Model C", "score": 10, "cost_per_task_micro": 1, "avg_duration_ms": 1},
	}
	if status, body := ac.do(http.MethodPost, fmt.Sprintf("/benchmarks/%d/runs", b.ID), map[string]any{
		"run_at": time.Now().UTC().Format(time.RFC3339), "results": append(results, map[string]any{"model_label": "Model A", "score": 1}),
	}, nil); status != http.StatusBadRequest || !strings.Contains(string(body), "duplicated") {
		t.Errorf("duplicate label: status = %d, body = %s", status, body)
	}
	var run1 struct {
		ID      int64 `json:"id"`
		Results []struct {
			ModelLabel   string  `json:"model_label"`
			VirtualModel *string `json:"virtual_model"`
		} `json:"results"`
	}
	ac.post(fmt.Sprintf("/benchmarks/%d/runs", b.ID), map[string]any{
		"origin": "import", "run_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "results": results,
	}, &run1)
	if len(run1.Results) != 3 || run1.Results[0].VirtualModel == nil || *run1.Results[0].VirtualModel != vmName {
		t.Fatalf("run1 results = %+v, want Model A linked to %s first", run1.Results, vmName)
	}

	// 已发布基准但还没有已发布的 run：可见，run 为 null，没有结果。
	type benchDetail struct {
		Run       *struct{ ID int64 } `json:"run"`
		Models    int                 `json:"models_count"`
		Results   []json.RawMessage   `json:"results"`
		Champions struct {
			Quality, Value, Speed *struct {
				ModelLabel string  `json:"model_label"`
				Model      *string `json:"model"`
			}
		} `json:"champions"`
	}
	var detail benchDetail
	status, hdr, body := publicGet(t, gw.URL+"/v1/benchmarks/"+slug)
	if status != http.StatusOK || json.Unmarshal(body, &detail) != nil || detail.Run != nil || len(detail.Results) != 0 {
		t.Fatalf("before publishing a run: status = %d, body = %s", status, body)
	}
	if hdr.Get("Cache-Control") != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", hdr.Get("Cache-Control"))
	}

	ac.post(fmt.Sprintf("/benchmark-runs/%d/publish", run1.ID), nil, nil)
	// 公开接口有 5 分钟进程内缓存：换一个新的网关实例读取最新数据。
	gw2 := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Logger: logger, PG: pool, Catalog: catalog.NewStore(pool, testBox(t), 0)}))
	defer gw2.Close()
	status, _, body = publicGet(t, gw2.URL+"/v1/benchmarks/"+slug)
	detail = benchDetail{}
	if status != http.StatusOK || json.Unmarshal(body, &detail) != nil || detail.Run == nil || detail.Run.ID != run1.ID || detail.Models != 3 {
		t.Fatalf("after publish: status = %d, body = %s", status, body)
	}
	c := detail.Champions
	if c.Quality == nil || c.Quality.ModelLabel != "Model A" || c.Quality.Model == nil || *c.Quality.Model != vmName ||
		c.Value == nil || c.Value.ModelLabel != "Model B" || c.Speed == nil || c.Speed.ModelLabel != "Model B" {
		t.Errorf("champions = %s", body)
	}
	status, _, body = publicGet(t, gw2.URL+"/v1/benchmarks?category=reasoning")
	if status != http.StatusOK || !strings.Contains(string(body), `"slug":"`+slug+`"`) {
		t.Errorf("list: status = %d, body does not contain %s", status, slug)
	}
	if status, _, _ := publicGet(t, gw2.URL+"/v1/benchmarks?category=nope"); status != http.StatusBadRequest {
		t.Errorf("invalid category: status = %d", status)
	}

	// 第二个 run 发布后，第一个退居历史；发布过的 run 不能删，草稿可以删。
	var run2, run3 struct{ ID int64 }
	ac.post(fmt.Sprintf("/benchmarks/%d/runs", b.ID), map[string]any{
		"run_at": time.Now().UTC().Format(time.RFC3339), "results": results[:2], "publish": true,
	}, &run2)
	var bd struct {
		Runs []struct {
			ID        int64 `json:"id"`
			Published bool  `json:"published"`
		} `json:"runs"`
	}
	ac.get(fmt.Sprintf("/benchmarks/%d", b.ID), &bd)
	for _, r := range bd.Runs {
		if (r.ID == run2.ID) != r.Published {
			t.Errorf("run %d published = %v after publishing run %d", r.ID, r.Published, run2.ID)
		}
	}
	if status, body := ac.do(http.MethodDelete, fmt.Sprintf("/benchmark-runs/%d", run1.ID), nil, nil); status != http.StatusConflict {
		t.Errorf("delete published run: status = %d, body = %s", status, body)
	}
	ac.post(fmt.Sprintf("/benchmarks/%d/runs", b.ID), map[string]any{"run_at": time.Now().UTC().Format(time.RFC3339), "results": results[:1]}, &run3)
	if status, body := ac.do(http.MethodDelete, fmt.Sprintf("/benchmark-runs/%d", run3.ID), nil, nil); status != http.StatusNoContent {
		t.Errorf("delete draft run: status = %d, body = %s", status, body)
	}

	var audits int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM admin_audit_logs WHERE action LIKE 'benchmark%' AND (target_id = $1 OR target_id IN ($2, $3, $4))`,
		fmt.Sprint(b.ID), fmt.Sprint(run1.ID), fmt.Sprint(run2.ID), fmt.Sprint(run3.ID)).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	// create + patch + 3×run.create + publish + delete = 7
	if audits < 7 {
		t.Errorf("audit entries = %d, want >= 7", audits)
	}
}

func TestRankings_PublicEndpoint(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	cfg := &config.Config{Public: config.PublicConfig{RankingsEnabled: true, RankingsMinAccounts: 3}}
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Cfg: cfg, Logger: logger, PG: pool, Catalog: catalog.NewStore(pool, box, 0)}))
	defer gw.Close()

	for _, path := range []string{"/v1/rankings/models?period=year", "/v1/rankings/models?limit=0", "/v1/rankings/models?series=hour", "/v1/rankings/authors?period=x"} {
		if status, _, body := publicGet(t, gw.URL+path); status != http.StatusBadRequest || errorCode(body) != "invalid_request" {
			t.Errorf("%s: status = %d, body = %s", path, status, body)
		}
	}
	status, hdr, body := publicGet(t, gw.URL+"/v1/rankings/models?period=month&series=day")
	var resp struct {
		Period      string            `json:"period"`
		TZ          string            `json:"tz"`
		TotalTokens *int64            `json:"total_tokens"`
		Models      []json.RawMessage `json:"models"`
		Methodology struct {
			MinDistinctAccounts int  `json:"min_distinct_accounts"`
			ShowsAbsolute       bool `json:"shows_absolute"`
		} `json:"methodology"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &resp) != nil {
		t.Fatalf("models: status = %d, body = %s", status, body)
	}
	if resp.Period != "month" || resp.TZ != "Asia/Shanghai" || resp.TotalTokens != nil || resp.Methodology.MinDistinctAccounts != 3 || resp.Methodology.ShowsAbsolute {
		t.Errorf("models response = %s", body)
	}
	if hdr.Get("Cache-Control") != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", hdr.Get("Cache-Control"))
	}
	for _, path := range []string{"/v1/rankings/authors", "/v1/rankings/speed?limit=5", "/v1/rankings/tools", "/v1/rankings/multimodal?period=day", "/v1/rankings/apps"} {
		if status, _, body := publicGet(t, gw.URL+path); status != http.StatusOK {
			t.Errorf("%s: status = %d, body = %s", path, status, body)
		}
	}
	if status, _, _ := publicGet(t, gw.URL+"/v1/rankings/apps?limit=101"); status != http.StatusBadRequest {
		t.Errorf("apps limit=101: status = %d, want 400", status)
	}

	// 总开关关闭：503 service_unavailable；基准测试不受影响。
	off := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{Cfg: &config.Config{}, Logger: logger, PG: pool, Catalog: catalog.NewStore(pool, box, 0)}))
	defer off.Close()
	if status, _, body := publicGet(t, off.URL+"/v1/rankings/models"); status != http.StatusServiceUnavailable || errorCode(body) != "service_unavailable" {
		t.Errorf("disabled rankings: status = %d, body = %s", status, body)
	}
	if status, _, _ := publicGet(t, off.URL+"/v1/benchmarks"); status != http.StatusOK {
		t.Errorf("benchmarks must not be affected by the rankings switch, status = %d", status)
	}
}

func TestPublicAppRules_Admin(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	key := fmt.Sprintf("name:fake-app-%d", time.Now().UnixNano())
	for name, body := range map[string]map[string]any{
		"bad key":              {"app_key": "fake", "action": "block"},
		"bad action":           {"app_key": key, "action": "hide"},
		"merge without target": {"app_key": key, "action": "merge"},
		"merge into itself":    {"app_key": key, "action": "merge", "merge_into": key},
		"rename without name":  {"app_key": key, "action": "rename"},
		"block with target":    {"app_key": key, "action": "block", "merge_into": "url:https://x.example"},
	} {
		if status, resp := ac.do(http.MethodPost, "/public-app-rules", body, nil); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, body = %s", name, status, resp)
		}
	}
	var rule struct{ ID int64 }
	ac.post("/public-app-rules", map[string]any{"app_key": key, "action": "block", "note": "impersonation"}, &rule)
	if status, _ := ac.do(http.MethodPost, "/public-app-rules", map[string]any{"app_key": key, "action": "block"}, nil); status != http.StatusConflict {
		t.Errorf("duplicate rule: status = %d, want 409", status)
	}
	ac.get("/public-app-rules", nil)
	ac.get("/public-apps?days=30", nil)
	if status, _ := ac.do(http.MethodGet, "/public-apps?days=91", nil, nil); status != http.StatusBadRequest {
		t.Errorf("days=91: status = %d, want 400", status)
	}
	if status, body := ac.do(http.MethodDelete, fmt.Sprintf("/public-app-rules/%d", rule.ID), nil, nil); status != http.StatusNoContent {
		t.Errorf("delete: status = %d, body = %s", status, body)
	}
	if status, _ := ac.do(http.MethodDelete, fmt.Sprintf("/public-app-rules/%d", rule.ID), nil, nil); status != http.StatusNotFound {
		t.Errorf("delete again: status = %d, want 404", status)
	}
}
