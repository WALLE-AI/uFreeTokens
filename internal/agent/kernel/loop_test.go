package kernel_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel/fakemodel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel/memstore"
)

// fakeTool 是测试用工具：read 直接返回固定结果；write 实现 Proposer。
type fakeTool struct {
	spec  kernel.ToolSpec
	calls int
	err   error
}

func (t *fakeTool) Spec() kernel.ToolSpec { return t.spec }

func (t *fakeTool) Call(ctx context.Context, env *kernel.Env, args json.RawMessage) (kernel.Result, error) {
	t.calls++
	if t.err != nil {
		return kernel.Result{}, t.err
	}
	if _, ok := kernel.CallFrom(ctx); !ok {
		return kernel.Result{}, errors.New("missing call ref in ctx")
	}
	return kernel.Result{HTTPStatus: 200, Content: map[string]any{"count": 7, "args": json.RawMessage(args)}, Summary: "共 7 条"}, nil
}

func (t *fakeTool) Propose(ctx context.Context, env *kernel.Env, args json.RawMessage) (*kernel.Proposal, error) {
	var a struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(args, &a)
	if a.ID == 0 {
		return nil, errors.New("id is required")
	}
	return &kernel.Proposal{TargetType: "price_change_request", TargetID: "88", Summary: "批准调价 #88",
		Before: map[string]any{"status": "pending"}, After: map[string]any{"status": "approved"}, ETag: `"v1"`}, nil
}

func readTool(name string) *fakeTool {
	return &fakeTool{spec: kernel.ToolSpec{Name: name, Risk: kernel.RiskRead, Permission: "pricing:read", Trusted: true}}
}

func writeTool(name string) *fakeTool {
	return &fakeTool{spec: kernel.ToolSpec{Name: name, Risk: kernel.RiskWrite, Permission: "price_change:approve", Trusted: true}}
}

type harness struct {
	store  *memstore.Store
	events []kernel.Event
	loop   *kernel.Loop
	state  *kernel.RunState
}

func newHarness(model kernel.Model, perms []adminauth.Permission, tools ...kernel.Tool) *harness {
	h := &harness{store: memstore.New()}
	h.loop = &kernel.Loop{Model: model, Store: h.store, Sink: kernel.SinkFunc(func(e kernel.Event) { h.events = append(h.events, e) })}
	h.state = &kernel.RunState{
		Env:    &kernel.Env{Principal: &adminauth.Principal{AdminID: 5, Permissions: perms}, SessionID: 1, RunID: "r1", Mode: kernel.ModeInteractive},
		System: "sys", Tools: tools, Budget: kernel.Budget{MaxTurns: 5, MaxToolCalls: 10, MaxTokens: 100000},
		History: []kernel.Message{{Role: kernel.RoleUser, Content: "预审调价"}},
	}
	return h
}

func (h *harness) eventTypes() []string {
	var out []string
	for _, e := range h.events {
		if e.Type != "text_delta" && e.Type != "reasoning_delta" && e.Type != "usage" {
			out = append(out, e.Type)
		}
	}
	return out
}

func TestLoop_ReadThenAnswer(t *testing.T) {
	model := fakemodel.New(fakemodel.Call("list", map[string]any{"status": "pending"}), fakemodel.Text("共 7 条待审。"))
	tool := readTool("list")
	h := newHarness(model, []adminauth.Permission{"pricing:read"}, tool)
	out, err := h.loop.Run(context.Background(), h.state)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != kernel.StatusCompleted || out.Turns != 2 || out.Usage.In != 200 {
		t.Fatalf("outcome = %+v", out)
	}
	if tool.calls != 1 {
		t.Errorf("tool calls = %d", tool.calls)
	}
	hist := h.store.History(1)
	if len(hist) != 3 || hist[0].Role != "assistant" || hist[1].Role != "tool" || hist[2].Content != "共 7 条待审。" {
		t.Fatalf("history = %+v", hist)
	}
	if hist[1].ToolCallID != hist[0].ToolCalls[0].ID || !strings.HasPrefix(hist[0].ToolCalls[0].ID, "r1_1_") {
		t.Errorf("tool_call_id mismatch: %+v", hist)
	}
	if !strings.Contains(hist[1].Content, `<tool_result tool="list" status="200" trusted="true">`) {
		t.Errorf("tool content = %s", hist[1].Content)
	}
	// 第二轮请求必须带上工具结果。
	if n := len(model.Requests[1].Messages); n != 4 {
		t.Errorf("second request messages = %d, want 4 (system, user, assistant, tool)", n)
	}
	if got := strings.Join(h.eventTypes(), ","); got != "tool_call,tool_result" {
		t.Errorf("events = %s", got)
	}
}

func TestLoop_ReasoningStreamedAndPersisted(t *testing.T) {
	step := fakemodel.Text("答案")
	step.Reasoning = "先想一想"
	h := newHarness(fakemodel.New(step), nil)
	if _, err := h.loop.Run(context.Background(), h.state); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, e := range h.events {
		if e.Type == "reasoning_delta" || e.Type == "text_delta" {
			seq = append(seq, e.Type+":"+e.Data.(map[string]any)["text"].(string))
		}
	}
	if got := strings.Join(seq, ","); got != "reasoning_delta:先想一想,text_delta:答案" {
		t.Errorf("deltas = %s", got)
	}
	hist := h.store.History(1)
	if len(hist) != 1 || hist[0].Reasoning != "先想一想" || hist[0].Content != "答案" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestLoop_WriteProposesAndPauses(t *testing.T) {
	model := fakemodel.New(fakemodel.Calls(
		fakemodel.TC("list", map[string]any{}),
		fakemodel.TC("approve", map[string]any{"id": 88, "rationale": "参考价未变", "confidence": 0.9}),
		fakemodel.TC("approve", map[string]any{"id": 89}),
	))
	h := newHarness(model, []adminauth.Permission{"*"}, readTool("list"), writeTool("approve"))
	out, err := h.loop.Run(context.Background(), h.state)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != kernel.StatusAwaitingApproval || out.Pending != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if len(h.store.Proposals) != 1 {
		// 两次调用目标相同（假工具固定返回 #88），第二条被去重退回。
		t.Fatalf("proposals = %d, want 1 (duplicate rejected)", len(h.store.Proposals))
	}
	p := h.store.Proposals[0]
	if p.Rationale != "参考价未变" || p.Confidence == nil || *p.Confidence != 0.9 || p.RequiredPerm != "price_change:approve" {
		t.Errorf("proposal = %+v", p)
	}
	rec := h.store.Calls[p.ToolCallID]
	if rec.Status != kernel.CallPendingApproval || rec.ETag != `"v1"` || rec.Summary != "批准调价 #88" {
		t.Errorf("record = %+v", rec)
	}
	// 读工具与被退回的写工具都有 tool 消息；待审批的写工具没有。
	tools := 0
	for _, m := range h.store.History(1) {
		if m.Role == "tool" {
			tools++
		}
	}
	if tools != 2 {
		t.Errorf("tool messages = %d, want 2", tools)
	}
}

func TestLoop_BatchModeDoesNotPause(t *testing.T) {
	model := fakemodel.New(fakemodel.Call("approve", map[string]any{"id": 88}), fakemodel.Text("已提交 1 条提案"))
	h := newHarness(model, []adminauth.Permission{"*"}, writeTool("approve"))
	h.state.Env.Mode = kernel.ModeBatch
	out, err := h.loop.Run(context.Background(), h.state)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != kernel.StatusCompleted || out.Proposals != 1 || out.Pending != 0 {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestLoop_DeniedUnknownAndBadArgs(t *testing.T) {
	model := fakemodel.New(fakemodel.Calls(
		fakemodel.TC("approve", map[string]any{"id": 1}),
		fakemodel.TC("nope", map[string]any{}),
		fakemodel.TC("list", "{not json"),
	), fakemodel.Text("无权限"))
	list := readTool("list")
	h := newHarness(model, []adminauth.Permission{"pricing:read"}, list, writeTool("approve"))
	out, err := h.loop.Run(context.Background(), h.state)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != kernel.StatusCompleted || list.calls != 0 || len(h.store.Proposals) != 0 {
		t.Fatalf("outcome = %+v calls=%d proposals=%d", out, list.calls, len(h.store.Proposals))
	}
	hist := h.store.History(1)
	if !strings.Contains(hist[1].Content, "没有权限") || !strings.Contains(hist[2].Content, "未知工具") || !strings.Contains(hist[3].Content, "JSON") {
		t.Errorf("history = %+v", hist)
	}
}

func TestLoop_ToolErrorIsReturnedToModel(t *testing.T) {
	model := fakemodel.New(fakemodel.Call("list", map[string]any{}), fakemodel.Text("接口出错"))
	tool := readTool("list")
	tool.err = errors.New("boom")
	h := newHarness(model, []adminauth.Permission{"*"}, tool)
	out, err := h.loop.Run(context.Background(), h.state)
	if err != nil || out.Status != kernel.StatusCompleted {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if !strings.Contains(h.store.History(1)[1].Content, "boom") {
		t.Errorf("tool error not passed to model")
	}
}

func TestLoop_Budgets(t *testing.T) {
	loopForever := func(n int) []fakemodel.Step {
		var s []fakemodel.Step
		for range n {
			s = append(s, fakemodel.Call("list", map[string]any{}))
		}
		return s
	}
	h := newHarness(fakemodel.New(loopForever(10)...), []adminauth.Permission{"*"}, readTool("list"))
	h.state.Budget = kernel.Budget{MaxTurns: 3}
	out, _ := h.loop.Run(context.Background(), h.state)
	if out.Status != kernel.StatusStopped || out.Reason != "max_turns" || out.Turns != 3 {
		t.Errorf("max turns: %+v", out)
	}

	h = newHarness(fakemodel.New(loopForever(10)...), []adminauth.Permission{"*"}, readTool("list"))
	h.state.Budget = kernel.Budget{MaxTokens: 250}
	out, _ = h.loop.Run(context.Background(), h.state)
	if out.Status != kernel.StatusStopped || out.Reason != "token_budget" || out.Turns != 3 {
		t.Errorf("token budget: %+v", out)
	}

	h = newHarness(fakemodel.New(loopForever(10)...), []adminauth.Permission{"*"}, readTool("list"))
	h.state.Budget = kernel.Budget{MaxToolCalls: 2}
	out, _ = h.loop.Run(context.Background(), h.state)
	if out.Status != kernel.StatusStopped || out.Reason != "max_tool_calls" {
		t.Errorf("tool budget: %+v", out)
	}
}

func TestLoop_CancelAndTimeout(t *testing.T) {
	h := newHarness(fakemodel.New(fakemodel.Text("x")), nil)
	h.store.Cancelled[1] = true
	out, _ := h.loop.Run(context.Background(), h.state)
	if out.Status != kernel.StatusStopped || out.Reason != "cancelled" {
		t.Errorf("cancel: %+v", out)
	}

	h = newHarness(fakemodel.New(fakemodel.Step{Text: "slow", Delay: time.Second}), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	out, _ = h.loop.Run(ctx, h.state)
	if out.Status != kernel.StatusStopped || out.Reason != "timeout" {
		t.Errorf("timeout: %+v", out)
	}
}

func TestLoop_LLMFailure(t *testing.T) {
	h := newHarness(fakemodel.New(fakemodel.Step{Err: errors.New("503")}), nil)
	out, err := h.loop.Run(context.Background(), h.state)
	if err == nil || out.Status != kernel.StatusFailed {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got := strings.Join(h.eventTypes(), ","); got != "error" {
		t.Errorf("events = %s", got)
	}
}

func TestToolResultContent_TruncatesAndMarksUntrusted(t *testing.T) {
	spec := kernel.ToolSpec{Name: "get_offer", Source: "upstream_offer"}
	s := kernel.ToolResultContent(spec, kernel.Result{HTTPStatus: 200, Content: strings.Repeat("优", 5000)}, 1024)
	if !strings.Contains(s, `trusted="false" source="upstream_offer" truncated="true"`) || len(s) > 1400 {
		t.Errorf("content = %.200s… (len %d)", s, len(s))
	}
}

func TestSplitProposalMeta(t *testing.T) {
	meta, rest, err := kernel.SplitProposalMeta(json.RawMessage(`{"reason":"x","rationale":"r","confidence":1.5,"evidence":[{"url":"u","quote":"q"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Rationale != "r" || meta.Confidence != nil || len(meta.Evidence) != 1 || string(rest) != `{"reason":"x"}` {
		t.Errorf("meta=%+v rest=%s", meta, rest)
	}
}
