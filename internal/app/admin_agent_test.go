// 运营智能体（Harness）的端到端测试：真实的 cmd/admin 路由 + Postgres，模型用脚本化的 fakemodel
// （CI 不调用真实模型）。覆盖实施方案 M1/M2 的验收点：SSE 事件序列、写操作 100% 走审批、
// 审批后以审批人身份执行、审计关联会话、重复审批只执行一次、无权限与应急令牌被拒绝。
package app_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/jobs"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel/fakemodel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/routes"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

// TestAgentTools_BoundToRoutes：每个路由工具都绑定到路由表中真实存在的路由、权限点与路由一致；
// GET 以外的路由只有白名单内的只读接口可以标为 read；禁止暴露的高风险路由不出现在工具集里。
func TestAgentTools_BoundToRoutes(t *testing.T) {
	tools, err := app.BuildAgentTools(app.AgentToolDeps{Handler: http.NotFoundHandler()})
	if err != nil {
		t.Fatalf("BuildAgentTools: %v", err)
	}
	readOnlyPOST := map[string]bool{
		"/pricing/preview": true, "/pricesync/reference-price-lookup": true,
		"/price-sources/dry-run": true, "/offer-pages/extract-preview": true,
	}
	forbiddenPerm := map[adminauth.Permission]bool{
		adminauth.PermWalletAdjust: true, adminauth.PermProviderKeyWrite: true, adminauth.PermAdminUserManage: true,
		adminauth.PermAccountWrite: true,
	}
	for _, tool := range tools {
		rt, ok := tool.(*routes.Tool)
		if !ok {
			continue // 研究工具
		}
		method, pattern := rt.Route()
		perm, ok := app.RoutePermission(method, pattern)
		if !ok {
			t.Errorf("%s: route %s %s not in route table", rt.Spec().Name, method, pattern)
			continue
		}
		if string(perm) != rt.Spec().Permission {
			t.Errorf("%s: permission %q != route permission %q", rt.Spec().Name, rt.Spec().Permission, perm)
		}
		if rt.Spec().Risk == kernel.RiskRead && method != http.MethodGet && !readOnlyPOST[pattern] {
			t.Errorf("%s: %s %s is marked read but is not GET", rt.Spec().Name, method, pattern)
		}
		if forbiddenPerm[perm] {
			t.Errorf("%s: route %s %s requires %s, which must never be exposed to the agent", rt.Spec().Name, method, pattern, perm)
		}
		if strings.Contains(pattern, "batch") {
			t.Errorf("%s: batch routes must not be exposed (%s)", rt.Spec().Name, pattern)
		}
		var schema map[string]any
		if err := json.Unmarshal(rt.Spec().Parameters, &schema); err != nil || schema["type"] != "object" {
			t.Errorf("%s: invalid parameter schema %s", rt.Spec().Name, rt.Spec().Parameters)
		}
	}
}

type sseEvent struct {
	Event string
	Data  map[string]any
}

func readSSE(t *testing.T, body []byte) []sseEvent {
	t.Helper()
	var out []sseEvent
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var cur sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.Data)
		case line == "" && cur.Event != "":
			out = append(out, cur)
			cur = sseEvent{}
		}
	}
	return out
}

func eventNames(evs []sseEvent) []string {
	var out []string
	for _, e := range evs {
		if e.Event != "text_delta" && e.Event != "usage" {
			out = append(out, e.Event)
		}
	}
	return out
}

func findEvent(evs []sseEvent, name string) *sseEvent {
	for i := range evs {
		if evs[i].Event == name {
			return &evs[i]
		}
	}
	return nil
}

func newAgentTestServer(t *testing.T, model *fakemodel.Model, enabled bool) (*adminClient, *pgxpool.Pool, func()) {
	t.Helper()
	return newAdminTestServerWithDeps(t, false, func(d *app.AdminDeps) {
		cfg := config.Config{Agent: config.AgentConfig{Enabled: enabled, MaxTurns: 10, MaxToolCalls: 20, MaxTokens: 100000, RunTimeout: time.Minute}}
		svc, err := app.NewAgentService(app.AgentSetup{Config: cfg, Pool: testPoolFromDeps(t, d), Admin: d.Admin, Deps: *d, Logger: d.Logger})
		if err != nil {
			t.Fatalf("NewAgentService: %v", err)
		}
		if model != nil {
			svc.Model = model
		}
		d.Agent = svc
		d.AgentJobs = &jobs.Store{Pool: testPool(t)}
	})
}

// testPoolFromDeps 复用测试连接池（newAdminTestServerWithDeps 内部已建好的同一个库）。
func testPoolFromDeps(t *testing.T, _ *app.AdminDeps) *pgxpool.Pool { return testPool(t) }

func TestAgent_DisabledAndPermissions(t *testing.T) {
	ac, pool, done := newAgentTestServer(t, nil, false)
	defer done()
	ac.loginAs(pool, "智能体-运营", "operator")
	status, body := ac.do(http.MethodGet, "/agent/meta", nil, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("meta = %d %s, want 200 enabled=false", status, body)
	}
	if status, body := ac.do(http.MethodPost, "/agent/sessions", map[string]any{}, nil); status != http.StatusServiceUnavailable || !strings.Contains(string(body), "agent_disabled") {
		t.Errorf("create while disabled = %d %s, want 503 agent_disabled", status, body)
	}
	ac.loginAs(pool, "智能体-客服", "support")
	if status, _ := ac.do(http.MethodGet, "/agent/meta", nil, nil); status != http.StatusForbidden {
		t.Errorf("support /agent/meta = %d, want 403 (no agent:use)", status)
	}
}

func TestAgent_ProposeApproveExecuteAudit(t *testing.T) {
	appKey := fmt.Sprintf("name:agent-test-%d", time.Now().UnixNano())
	model := fakemodel.New(
		fakemodel.Call("list_public_apps", map[string]any{"days": 7}),
		fakemodel.Call("create_public_app_rule", map[string]any{
			"app_key": appKey, "action": "block", "note": "测试屏蔽", "rationale": "名称含广告引流", "confidence": 0.9,
		}),
		fakemodel.Text("已屏蔽该应用。"),
	)
	ac, pool, done := newAgentTestServer(t, model, true)
	defer done()

	// 应急令牌身份不能使用智能体。
	ac.token = testAdminToken
	if status, body := ac.do(http.MethodPost, "/agent/sessions", map[string]any{}, nil); status != http.StatusForbidden {
		t.Errorf("break-glass create = %d %s, want 403", status, body)
	}

	op := ac.loginAs(pool, "智能体-运营", "operator")
	var meta struct {
		Enabled bool `json:"enabled"`
		Tools   []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	ac.get("/agent/meta", &meta)
	var names []string
	for _, x := range meta.Tools {
		names = append(names, x.Name)
	}
	// operator 没有 pricing:write / price_change:approve：这些写工具不应出现在工具集里。
	if !meta.Enabled || !slices.Contains(names, "create_public_app_rule") || slices.Contains(names, "approve_price_change") {
		t.Fatalf("meta = %+v", meta)
	}

	var sess struct {
		ID int64 `json:"id"`
	}
	ac.post("/agent/sessions", map[string]any{"playbook": "public_app_governance"}, &sess)
	status, body := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/messages", sess.ID), map[string]any{"content": "检查应用榜"}, nil)
	if status != http.StatusOK {
		t.Fatalf("send = %d %s", status, body)
	}
	evs := readSSE(t, body)
	got := strings.Join(eventNames(evs), ",")
	want := "run_started,tool_call,tool_result,tool_call,approval_required,run_finished"
	if got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	fin := findEvent(evs, "run_finished")
	if fin.Data["status"] != kernel.StatusAwaitingApproval {
		t.Fatalf("run_finished = %+v", fin.Data)
	}
	// 只读工具必须真的执行成功（回归：外层 chi RouteContext 曾让内部 GET 被当成 POST，返回 405/400）。
	if tr := findEvent(evs, "tool_result"); tr == nil || tr.Data["status"] != kernel.CallDone || tr.Data["http_status"] != float64(200) {
		t.Fatalf("read tool result = %+v, want done/200", tr)
	}
	ap := findEvent(evs, "approval_required").Data
	callID, _ := ap["id"].(string)
	if ap["permission"] != "catalog:write" || ap["rationale"] != "名称含广告引流" {
		t.Errorf("approval_required = %+v", ap)
	}

	// 审批前没有任何写入发生。
	var rules struct {
		Data []admin.PublicAppRule `json:"data"`
	}
	ac.get("/public-app-rules", &rules)
	for _, r := range rules.Data {
		if r.AppKey == appKey {
			t.Fatal("rule created before approval")
		}
	}

	// 收件箱可见该提案。
	var inbox struct {
		Data []struct {
			ToolCallID string `json:"tool_call_id"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	ac.get("/agent/proposals?status=pending&target_type=public_app", &inbox)
	if !slices.ContainsFunc(inbox.Data, func(p struct {
		ToolCallID string `json:"tool_call_id"`
		Status     string `json:"status"`
	}) bool {
		return p.ToolCallID == callID
	}) {
		t.Errorf("proposal %s not in inbox: %+v", callID, inbox.Data)
	}

	// 审批（SSE）：执行 → 继续运行 → 完成。
	status, body = ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/tool-calls/%s/decision?stream=1", sess.ID, callID),
		map[string]any{"decision": "approve", "note": "同意"}, nil)
	if status != http.StatusOK {
		t.Fatalf("decision = %d %s", status, body)
	}
	evs = readSSE(t, body)
	tr := findEvent(evs, "tool_result")
	if tr == nil || tr.Data["status"] != kernel.CallExecuted {
		t.Fatalf("decision events = %+v", evs)
	}
	if fin := findEvent(evs, "run_finished"); fin == nil || fin.Data["status"] != kernel.StatusCompleted {
		t.Errorf("resumed run did not complete: %+v", evs)
	}

	// 规则已写入；审计记录关联到会话与工具调用，操作人是审批人。
	ac.get("/public-app-rules", &rules)
	if !slices.ContainsFunc(rules.Data, func(r admin.PublicAppRule) bool { return r.AppKey == appKey }) {
		t.Fatal("rule not created after approval")
	}
	var logs struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.token = testAdminToken
	ac.get("/audit-logs?action=public_app_rule.create&limit=20", &logs)
	found := false
	for _, e := range logs.Data {
		if e.AgentToolCallID != nil && *e.AgentToolCallID == callID {
			found = true
			if e.ActorID != op.ID || e.AgentSessionID == nil || *e.AgentSessionID != sess.ID {
				t.Errorf("audit entry = %+v", e)
			}
		}
	}
	if !found {
		t.Error("no audit log linked to the agent tool call")
	}
	ac.get(fmt.Sprintf("/audit-logs?action=agent.decision&target_id=%s", callID), &logs)
	if len(logs.Data) != 1 {
		t.Errorf("agent.decision audit = %+v", logs.Data)
	}

	// 重复审批只执行一次。
	ac.loginAs(pool, "智能体-运营2", "operator")
	status, body = ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/tool-calls/%s/decision", sess.ID, callID), map[string]any{"decision": "approve"}, nil)
	if status != http.StatusConflict || !strings.Contains(string(body), "already_decided") {
		t.Errorf("second decision = %d %s, want 409 already_decided", status, body)
	}
	// 他人会话：无 audit:read 时不可见（operator 有 audit:read，可只读查看）。
	var detail struct {
		ReadOnly bool `json:"read_only"`
		Messages []kernel.Message
	}
	ac.get(fmt.Sprintf("/agent/sessions/%d", sess.ID), &detail)
	if !detail.ReadOnly || len(detail.Messages) == 0 {
		t.Errorf("other admin view = %+v", detail)
	}
	if status, _ := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/messages", sess.ID), map[string]any{"content": "x"}, nil); status != http.StatusNotFound {
		t.Errorf("other admin send = %d, want 404", status)
	}
}

func TestAgent_RejectAndApproverPermission(t *testing.T) {
	appKey := fmt.Sprintf("name:agent-reject-%d", time.Now().UnixNano())
	model := fakemodel.New(
		fakemodel.Call("create_public_app_rule", map[string]any{"app_key": appKey, "action": "block"}),
		fakemodel.Text("好的，不屏蔽。"),
	)
	ac, pool, done := newAgentTestServer(t, model, true)
	defer done()
	ac.loginAs(pool, "智能体-运营", "operator")
	var sess struct {
		ID int64 `json:"id"`
	}
	ac.post("/agent/sessions", map[string]any{}, &sess)
	_, body := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/messages", sess.ID), map[string]any{"content": "屏蔽它"}, nil)
	ap := findEvent(readSSE(t, body), "approval_required")
	if ap == nil {
		t.Fatalf("no approval_required: %s", body)
	}
	callID := ap.Data["id"].(string)

	// pricing 角色没有 catalog:write，不能审批这条提案（即使能看到会话也不行）。
	ac.loginAs(pool, "智能体-定价", "pricing")
	if status, _ := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/tool-calls/%s/decision", sess.ID, callID), map[string]any{"decision": "approve"}, nil); status != http.StatusForbidden {
		t.Errorf("approve without permission = %d, want 403", status)
	}
	ac.loginAs(pool, "智能体-运营", "operator")
	var res agent.DecideResult
	status, body := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/tool-calls/%s/decision", sess.ID, callID), map[string]any{"decision": "reject", "note": "误报"}, nil)
	if status != http.StatusOK || json.Unmarshal(body, &res) != nil || res.Status != kernel.CallRejected {
		t.Fatalf("reject = %d %s", status, body)
	}
}

func testLogger() *slog.Logger {
	return observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
}
