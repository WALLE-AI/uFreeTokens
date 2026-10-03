package app_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/jobs"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel/fakemodel"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// TestAgentJobs_BatchProposeApproveBreaker：后台作业以只读服务主体运行，只产生提案（不暂停、不执行）；
// 运营在收件箱审批后以审批人身份执行；拒绝率超过阈值自动熔断；agent-bot 无法登录。
func TestAgentJobs_BatchProposeApproveBreaker(t *testing.T) {
	appKey := fmt.Sprintf("name:agent-job-%d", time.Now().UnixNano())
	model := fakemodel.New(
		fakemodel.Call("list_public_apps", map[string]any{"days": 7}),
		fakemodel.Call("create_public_app_rule", map[string]any{"app_key": appKey, "action": "block", "rationale": "刷榜", "confidence": 0.7}),
		fakemodel.Text("处理完毕：1 条提案。"),
	)
	ac, pool, done := newAgentTestServer(t, model, true)
	defer done()
	ctx := context.Background()

	// 用与 cmd/worker 相同的方式组装 Runner（批处理模式）。
	adminSvc := admin.New(pool, wallet.New(pool), nil, nil)
	deps := app.AdminDeps{Logger: testLogger(), Admin: adminSvc}
	cfg := config.Config{Agent: config.AgentConfig{Enabled: true, JobsEnabled: true, MaxTurns: 10, MaxToolCalls: 20, RunTimeout: time.Minute}}
	svc, err := app.NewAgentService(app.AgentSetup{Config: cfg, Pool: pool, Deps: deps, Batch: true, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	svc.Model = model
	runner := &jobs.Runner{Store: &jobs.Store{Pool: pool}, Agent: svc, Logger: testLogger()}

	bot, err := runner.Bot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range bot.Permissions {
		if !strings.HasSuffix(string(p), ":read") && p != "agent:use" {
			t.Fatalf("agent-bot has non-read permission %s", p)
		}
	}
	if status, _ := ac.do(http.MethodPost, "/auth/login", map[string]any{"email": jobs.BotEmail, "password": "!"}, nil); status == http.StatusOK {
		t.Fatal("agent-bot must not be able to log in")
	}

	var jobID int64
	if err := pool.QueryRow(ctx, `UPDATE agent_jobs SET run_requested = true, enabled = false, paused_reason = '' WHERE code = 'public_app_governance' RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE agent_jobs SET enabled = false, run_requested = false WHERE id = $1`, jobID)
	})
	if _, err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var sessionID int64
	var status string
	if err := pool.QueryRow(ctx, `SELECT last_session_id, last_status FROM agent_jobs WHERE id = $1`, jobID).Scan(&sessionID, &status); err != nil || status != kernel.StatusCompleted {
		t.Fatalf("job status = %q session=%d err=%v", status, sessionID, err)
	}

	// 提案进入收件箱，带 job_id；规则尚未写入。
	ac.loginAs(pool, "作业-运营", "operator")
	var inbox struct {
		Data []struct {
			ToolCallID string `json:"tool_call_id"`
			SessionID  int64  `json:"session_id"`
			JobID      *int64 `json:"job_id"`
			TargetID   string `json:"target_id"`
		} `json:"data"`
	}
	ac.get("/agent/proposals?status=pending&target_type=public_app&target_ids="+appKey, &inbox)
	if len(inbox.Data) != 1 || inbox.Data[0].JobID == nil || *inbox.Data[0].JobID != jobID {
		t.Fatalf("inbox = %+v", inbox.Data)
	}
	var counts struct {
		AgentPendingApprovals int `json:"agent_pending_approvals"`
	}
	ac.get("/todo-counts", &counts)
	if counts.AgentPendingApprovals < 1 {
		t.Errorf("todo-counts agent_pending_approvals = %d", counts.AgentPendingApprovals)
	}
	// 批处理会话对人只读。
	if st, _ := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/messages", sessionID), map[string]any{"content": "x"}, nil); st != http.StatusNotFound {
		t.Errorf("send to job session = %d, want 404", st)
	}

	p := inbox.Data[0]
	st, body := ac.do(http.MethodPost, fmt.Sprintf("/agent/sessions/%d/tool-calls/%s/decision", p.SessionID, p.ToolCallID), map[string]any{"decision": "approve"}, nil)
	if st != http.StatusOK || !strings.Contains(string(body), `"status":"executed"`) || strings.Contains(string(body), `"resumed":true`) {
		t.Fatalf("approve batch proposal = %d %s", st, body)
	}

	// 熔断：注入 10 条被拒绝的提案后自动停用。
	if _, err := pool.Exec(ctx, `UPDATE agent_jobs SET enabled = true WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		id := fmt.Sprintf("breaker_%d_%d", time.Now().UnixNano(), i)
		if _, err := pool.Exec(ctx, `INSERT INTO agent_tool_calls (id, session_id, tool, risk, args, status) VALUES ($1, $2, 'x', 'write', '{}', 'rejected')`, id, sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO agent_proposals (tool_call_id, session_id, job_id, tool, target_type, target_id, summary, required_perm, status)
			VALUES ($1, $2, $3, 'x', 'test', $1, 's', 'catalog:write', 'rejected')`, id, sessionID, jobID); err != nil {
			t.Fatal(err)
		}
	}
	if err := runner.CheckBreakers(ctx); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	var paused string
	_ = pool.QueryRow(ctx, `SELECT enabled, paused_reason FROM agent_jobs WHERE id = $1`, jobID).Scan(&enabled, &paused)
	if enabled || paused != "circuit_breaker" {
		t.Errorf("breaker: enabled=%v paused=%q", enabled, paused)
	}

	// 作业管理接口需要 agent:admin（operator 没有）。
	if st, _ := ac.do(http.MethodGet, "/agent/jobs", nil, nil); st != http.StatusForbidden {
		t.Errorf("operator GET /agent/jobs = %d, want 403", st)
	}
	ac.loginAs(pool, "作业-超管", "super_admin")
	var list struct {
		Data []jobs.Job `json:"data"`
	}
	ac.get("/agent/jobs", &list)
	if len(list.Data) < 8 {
		t.Errorf("jobs = %d", len(list.Data))
	}
	st, body = ac.do(http.MethodPatch, fmt.Sprintf("/agent/jobs/%d", jobID), map[string]any{"enabled": true}, nil)
	if st != http.StatusOK || !strings.Contains(string(body), `"paused_reason":""`) {
		t.Errorf("re-enable = %d %s", st, body)
	}
	if st, _ := ac.do(http.MethodPatch, fmt.Sprintf("/agent/jobs/%d", jobID), map[string]any{"schedule": "bogus"}, nil); st != http.StatusBadRequest {
		t.Errorf("bad schedule = %d, want 400", st)
	}
}
