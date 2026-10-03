package app_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel/fakemodel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/playbooks"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
)

// TestAgent_QueryAnalyticsDataset：全局助手调用 query_analytics → 结果存为数据集（模型只拿到预览与 dataset_id）
// → GET /agent/datasets/{id} 读取完整结果、/export 下载 CSV；参数错误以 400 原样回给模型。
func TestAgent_QueryAnalyticsDataset(t *testing.T) {
	vm := fmt.Sprintf("analytics-%d", time.Now().UnixNano())
	model := fakemodel.New(
		fakemodel.Call("query_analytics", map[string]any{"subject": "usage", "metrics": []string{"nope"}}),
		fakemodel.Call("query_analytics", map[string]any{
			"metrics": []string{"requests", "revenue"}, "group_by": "virtual_model",
			"filters": map[string]any{"virtual_model": vm}, "from": time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
		}),
		fakemodel.Text("近 24 小时该模型收入 ¥2.5（数据集见上）。"),
	)
	ac, pool, done := newAgentTestServer(t, model, true)
	defer done()
	ctx := t.Context()
	for i, charge := range []int64{1_000_000, 1_500_000} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream,
			   status, http_status, attempts, latency_ms, input_tokens, output_tokens, usage_source, charged_amount, list_amount, cost_amount)
			 VALUES ($1, now() - interval '1 hour', 7, 7, $2, '/v1/chat/completions', false, 'success', 200, 1, 300, 100, 50, 'upstream', $3::bigint, $3::bigint, $3::bigint / 2)`,
			fmt.Sprintf("%s-%d", vm, i), vm, charge); err != nil {
			t.Fatalf("seed request_log: %v", err)
		}
	}

	ac.loginAs(pool, "智能体-分析", "operator")
	var sess struct {
		ID int64 `json:"id"`
	}
	ac.post("/agent/sessions", map[string]any{}, &sess)
	status, body := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/messages", sess.ID),
		map[string]any{"content": "这个模型最近的收入？", "page": map[string]any{"path": "/analytics?range=1d", "title": "用量分析"}}, nil)
	if status != http.StatusOK {
		t.Fatalf("send = %d %s", status, body)
	}
	evs := readSSE(t, body)
	var results []*sseEvent
	for i := range evs {
		if evs[i].Event == "tool_result" {
			results = append(results, &evs[i])
		}
	}
	if len(results) != 2 {
		t.Fatalf("events = %v, want two tool_result", eventNames(evs))
	}
	if results[0].Data["status"] == kernel.CallDone || results[0].Data["http_status"] != float64(http.StatusBadRequest) {
		t.Errorf("invalid metric result = %+v, want an error with HTTP 400", results[0].Data)
	}
	ok := results[1].Data
	if ok["status"] != kernel.CallDone || ok["http_status"] != float64(http.StatusOK) {
		t.Fatalf("query_analytics result = %+v", ok)
	}
	m := regexp.MustCompile(`数据集 #(\d+)`).FindStringSubmatch(fmt.Sprint(ok["summary"]))
	if m == nil {
		t.Fatalf("summary %q has no dataset id", ok["summary"])
	}

	var ds struct {
		SessionID int64            `json:"session_id"`
		RowCount  int              `json:"row_count"`
		Rows      []map[string]any `json:"rows"`
		Totals    map[string]any   `json:"totals"`
	}
	ac.get("/agent/datasets/"+m[1], &ds)
	if ds.SessionID != sess.ID || ds.RowCount != 1 || ds.Rows[0]["key"] != vm || ds.Rows[0]["revenue"] != 2.5 || ds.Totals["requests"] != float64(2) {
		t.Fatalf("dataset = %+v", ds)
	}
	status, csvBody := ac.do(http.MethodGet, "/agent/datasets/"+m[1]+"/export", nil, nil)
	csv := string(csvBody)
	if status != http.StatusOK || !strings.HasPrefix(csv, "\xEF\xBB\xBF") || !strings.Contains(csv, "收入（元）") || !strings.Contains(csv, vm+",2,2.5") {
		t.Errorf("export = %d %q", status, csv)
	}
	if status, _ := ac.do(http.MethodGet, "/agent/datasets/999999999", nil, nil); status != http.StatusNotFound {
		t.Errorf("missing dataset = %d, want 404", status)
	}
}

// TestPlaybooks_AllowedToolsExist：剧本 allowed_tools 里的每个名字都必须是真实的工具（拼错会让剧本悄悄失去该工具）。
func TestPlaybooks_AllowedToolsExist(t *testing.T) {
	st := pgstore.New(nil)
	tools, err := app.BuildAgentTools(app.AgentToolDeps{Handler: http.NotFoundHandler(), Datasets: st, Reports: st})
	if err != nil {
		t.Fatalf("BuildAgentTools: %v", err)
	}
	names := map[string]bool{"fetch_page": true} // 只在配置了出站环境时注册
	for _, tool := range tools {
		names[tool.Spec().Name] = true
	}
	for _, pb := range playbooks.All() {
		for _, n := range pb.AllowedTools {
			if !names[n] {
				t.Errorf("playbook %s allows unknown tool %q", pb.Name, n)
			}
		}
	}
}

// TestAgent_ChartsAndReports：render_chart 校验数据集归属与列名、create_report 落库；报表的列表、详情、导出与分享权限。
func TestAgent_ChartsAndReports(t *testing.T) {
	vm := fmt.Sprintf("report-%d", time.Now().UnixNano())
	model := fakemodel.New(
		fakemodel.Call("query_analytics", map[string]any{"metrics": []string{"revenue", "requests"}, "group_by": "virtual_model",
			"filters": map[string]any{"virtual_model": vm}, "from": time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)}),
		fakemodel.Text("查到了。"),
	)
	ac, pool, done := newAgentTestServer(t, model, true)
	defer done()
	ctx := t.Context()
	if _, err := pool.Exec(ctx,
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream,
		   status, http_status, attempts, latency_ms, input_tokens, output_tokens, usage_source, charged_amount, list_amount, cost_amount)
		 VALUES ($1, now() - interval '1 hour', 7, 7, $1, '/v1/chat/completions', false, 'success', 200, 1, 300, 100, 50, 'upstream', 2500000, 2500000, 1000000)`,
		vm); err != nil {
		t.Fatalf("seed request_log: %v", err)
	}
	// 只有 agent:use + observe:read、没有 audit:read 的角色：用来验证私有/共享报表的可见性。
	if _, err := pool.Exec(ctx, `INSERT INTO admin_roles (code, name, permissions) VALUES ('agent_viewer_test', '测试', ARRAY['agent:use','observe:read'])
		ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	ac.loginAs(pool, "智能体-报表", "operator")
	var sess struct {
		ID int64 `json:"id"`
	}
	ac.post("/agent/sessions", map[string]any{}, &sess)
	send := func(text string) []map[string]any {
		t.Helper()
		status, body := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/messages", sess.ID), map[string]any{"content": text}, nil)
		if status != http.StatusOK {
			t.Fatalf("send = %d %s", status, body)
		}
		var out []map[string]any
		for _, e := range readSSE(t, body) {
			if e.Event == "tool_result" {
				out = append(out, e.Data)
			}
		}
		return out
	}
	first := send("这个模型最近的收入")
	dm := regexp.MustCompile(`数据集 #(\d+)`).FindStringSubmatch(fmt.Sprint(first[0]["summary"]))
	if dm == nil {
		t.Fatalf("query_analytics summary = %v", first[0]["summary"])
	}
	dsID, _ := strconv.ParseInt(dm[1], 10, 64)

	model.Push(
		fakemodel.Call("render_chart", map[string]any{"dataset_id": dsID + 1000000, "type": "bar", "x": "label", "y": []string{"revenue"}}),
		fakemodel.Call("render_chart", map[string]any{"dataset_id": dsID, "type": "bar", "x": "label", "y": []string{"nope"}}),
		fakemodel.Call("render_chart", map[string]any{"dataset_id": dsID, "type": "bar", "x": "label", "y": []string{"revenue"}}),
		fakemodel.Call("create_report", map[string]any{"title": "测试周报", "summary": "收入 ¥2.5", "sections": []any{
			map[string]any{"type": "markdown", "text": "本周收入 **¥2.5**。"},
			map[string]any{"type": "chart", "chart": map[string]any{"dataset_id": dsID, "type": "kpi", "y": []string{"revenue"}}},
			map[string]any{"type": "chart", "chart": map[string]any{"dataset_id": dsID, "type": "table"}},
		}}),
		fakemodel.Text("已生成报表。"),
	)
	results := send("画个图并做成周报")
	if len(results) != 4 {
		t.Fatalf("tool results = %+v", results)
	}
	for i, want := range []float64{400, 400, 200, 201} {
		if results[i]["http_status"] != want {
			t.Errorf("tool_result[%d] = %+v, want HTTP %v", i, results[i], want)
		}
	}
	if results[3]["status"] != kernel.CallDone {
		t.Errorf("create_report must execute without approval, got %+v", results[3])
	}
	m := regexp.MustCompile(`报表 #(\d+)`).FindStringSubmatch(fmt.Sprint(results[3]["summary"]))
	if m == nil {
		t.Fatalf("create_report summary = %v", results[3]["summary"])
	}
	reportPath := "/agent/reports/" + m[1]

	var detail struct {
		Report struct {
			Title      string  `json:"title"`
			Visibility string  `json:"visibility"`
			DatasetIDs []int64 `json:"dataset_ids"`
		} `json:"report"`
		Datasets []struct {
			ID int64 `json:"id"`
		} `json:"datasets"`
		CanEdit bool `json:"can_edit"`
	}
	ac.get(reportPath, &detail)
	if detail.Report.Title != "测试周报" || detail.Report.Visibility != "private" || len(detail.Datasets) != 1 || detail.Datasets[0].ID != dsID || !detail.CanEdit {
		t.Fatalf("report detail = %+v", detail)
	}
	var list struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	ac.get("/agent/reports?scope=mine", &list)
	if len(list.Data) == 0 || fmt.Sprint(list.Data[0].ID) != m[1] {
		t.Errorf("my reports = %+v", list.Data)
	}
	status, md := ac.do(http.MethodGet, reportPath+"/export?format=md", nil, nil)
	if status != http.StatusOK || !strings.Contains(string(md), "# 测试周报") || !strings.Contains(string(md), "¥2.50") || !strings.Contains(string(md), vm) {
		t.Errorf("markdown export = %d %s", status, md)
	}
	status, xlsx := ac.do(http.MethodGet, reportPath+"/export?format=xlsx", nil, nil)
	if status != http.StatusOK || !strings.HasPrefix(string(xlsx), "PK") {
		t.Errorf("xlsx export = %d (%d bytes)", status, len(xlsx))
	}

	// 私有报表对没有 audit:read 的其他管理员不可见；分享后可见但不能修改。
	owner := ac.token
	ac.loginAs(pool, "智能体-旁观", "agent_viewer_test")
	if status, _ := ac.do(http.MethodGet, reportPath, nil, nil); status != http.StatusNotFound {
		t.Errorf("private report for others = %d, want 404", status)
	}
	viewer := ac.token
	ac.token = owner
	if status, body := ac.do(http.MethodPatch, reportPath, map[string]any{"visibility": "shared"}, nil); status != http.StatusOK {
		t.Fatalf("share = %d %s", status, body)
	}
	ac.token = viewer
	if status, _ := ac.do(http.MethodGet, reportPath, nil, nil); status != http.StatusOK {
		t.Errorf("shared report for others = %d, want 200", status)
	}
	if status, _ := ac.do(http.MethodDelete, reportPath, nil, nil); status != http.StatusForbidden {
		t.Errorf("delete by non-owner = %d, want 403", status)
	}
	ac.token = owner
	if status, _ := ac.do(http.MethodDelete, reportPath, nil, nil); status != http.StatusNoContent {
		t.Errorf("delete by owner = %d, want 204", status)
	}
}
