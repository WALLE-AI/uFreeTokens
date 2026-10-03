package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 运行结果状态（agent_sessions.status）。
const (
	StatusCompleted        = "completed"
	StatusAwaitingApproval = "awaiting_approval"
	StatusStopped          = "stopped"
	StatusFailed           = "failed"
)

// Budget 是单次运行的上限（设计 §3.5）；零值表示不限。
type Budget struct {
	MaxTurns     int
	MaxToolCalls int
	MaxTokens    int
}

// RunState 是一次运行的输入：系统提示、已持久化的历史与可用工具。
type RunState struct {
	Env     *Env
	Model   string
	System  string
	History []Message // 不含 system；运行中追加的消息也会写回这里
	Tools   []Tool
	Budget  Budget
}

// Outcome 是一次运行的结论。
type Outcome struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Usage   Usage  `json:"usage"`
	Turns   int    `json:"turns"`
	Pending int    `json:"pending"` // 等待审批的提案数（交互模式）
	// Proposals 是本次运行生成的提案数（批处理模式的运行报告用）。
	Proposals int `json:"proposals"`
}

// Loop 是默认的 ReAct 循环：模型 → 工具 → 观察 → … → 结论 / 暂停。
type Loop struct {
	Model  Model
	Store  Store
	Sink   Sink
	Policy Policy
	Hooks  Hooks
	// MaxResultBytes 是单条工具结果交给模型的上限，超过截断；0 = 8 KiB。
	MaxResultBytes int
	// Now 便于测试注入时钟；nil = time.Now。
	Now func() time.Time
}

func (l *Loop) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Loop) emit(t string, data any) {
	if l.Sink != nil {
		l.Sink.Emit(Event{Type: t, Data: data})
	}
}

// Run 执行循环直到模型给出结论、需要审批、触达上限、被取消或出错。
// 暂停即返回，不挂起 goroutine：运行状态全部在 Store 里，审批后由调用方重新加载历史再次 Run。
func (l *Loop) Run(ctx context.Context, st *RunState) (Outcome, error) {
	if l.Policy == nil {
		l.Policy = DefaultPolicy{}
	}
	tools := make(map[string]Tool, len(st.Tools))
	specs := make([]ToolSpec, 0, len(st.Tools))
	for _, t := range st.Tools {
		s := t.Spec()
		tools[s.Name] = t
		specs = append(specs, s)
	}
	var out Outcome
	toolCalls := 0
	stop := func(status, reason string) (Outcome, error) {
		out.Status, out.Reason = status, reason
		return out, nil
	}
	for {
		if ctx.Err() != nil {
			return stop(StatusStopped, ctxReason(ctx))
		}
		if cancelled, err := l.Store.CancelRequested(ctx, st.Env.SessionID); err == nil && cancelled {
			return stop(StatusStopped, "cancelled")
		}
		if st.Budget.MaxTurns > 0 && out.Turns >= st.Budget.MaxTurns {
			return stop(StatusStopped, "max_turns")
		}
		if st.Budget.MaxTokens > 0 && out.Usage.In+out.Usage.Out >= st.Budget.MaxTokens {
			return stop(StatusStopped, "token_budget")
		}

		msgs := append([]Message{{Role: RoleSystem, Content: st.System}}, st.History...)
		if l.Hooks.TransformContext != nil {
			msgs = l.Hooks.TransformContext(msgs)
		}
		turn, err := l.Model.Stream(ctx, Request{Model: st.Model, Messages: msgs, Tools: specs}, func(s string) {
			l.emit("text_delta", map[string]any{"text": s})
		})
		if err != nil {
			if ctx.Err() != nil {
				return stop(StatusStopped, ctxReason(ctx))
			}
			l.emit("error", map[string]any{"code": "llm_unavailable", "message": err.Error()})
			out.Status, out.Reason = StatusFailed, "llm_unavailable"
			return out, err
		}
		out.Turns++
		out.Usage.In += turn.Usage.In
		out.Usage.Out += turn.Usage.Out
		if err := l.Store.AddUsage(ctx, st.Env.SessionID, turn.Usage, 1); err != nil {
			return out, err
		}
		l.emit("usage", map[string]any{"tokens_in": out.Usage.In, "tokens_out": out.Usage.Out, "turns": out.Turns})

		// 调用 ID 统一改写为运行内唯一（部分模型每轮都从 call_0 开始编号）。
		for i := range turn.ToolCalls {
			turn.ToolCalls[i].ID = fmt.Sprintf("%s_%d_%d", st.Env.RunID, out.Turns, i)
		}
		asst := Message{Role: RoleAssistant, Content: turn.Text, ToolCalls: turn.ToolCalls}
		if err := l.Store.AppendMessage(ctx, st.Env.SessionID, &asst); err != nil {
			return out, err
		}
		st.History = append(st.History, asst)
		if len(turn.ToolCalls) == 0 {
			return stop(StatusCompleted, "")
		}

		limitHit := false
		for _, call := range turn.ToolCalls {
			if st.Budget.MaxToolCalls > 0 && toolCalls >= st.Budget.MaxToolCalls {
				limitHit = true
				if err := l.appendTool(ctx, st, call.ID, errorContent("已达到单次运行的工具调用上限，未执行。")); err != nil {
					return out, err
				}
				continue
			}
			toolCalls++
			pending, proposed, err := l.handleCall(ctx, st, tools, call)
			if err != nil {
				return out, err
			}
			if pending {
				out.Pending++
			}
			if proposed {
				out.Proposals++
			}
		}
		if out.Pending > 0 {
			return stop(StatusAwaitingApproval, "")
		}
		if limitHit {
			return stop(StatusStopped, "max_tool_calls")
		}
	}
}

// handleCall 处理一次工具调用；pending=true 表示生成了等待当场审批的提案（交互模式）。
func (l *Loop) handleCall(ctx context.Context, st *RunState, tools map[string]Tool, call ToolCall) (pending, proposed bool, err error) {
	env := st.Env
	tool, ok := tools[call.Name]
	if !ok {
		l.emit("tool_call", map[string]any{"id": call.ID, "tool": call.Name, "args": rawOrString(call.Arguments), "risk": ""})
		l.emit("tool_result", map[string]any{"id": call.ID, "status": CallError, "summary": "未知工具"})
		return false, false, l.appendTool(ctx, st, call.ID, errorContent("未知工具 "+call.Name+"；只能使用已提供的工具。"))
	}
	spec := tool.Spec()
	args := json.RawMessage(strings.TrimSpace(call.Arguments))
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	l.emit("tool_call", map[string]any{"id": call.ID, "tool": spec.Name, "args": rawOrString(string(args)), "risk": spec.Risk})
	var probe map[string]any
	if err := json.Unmarshal(args, &probe); err != nil {
		l.emit("tool_result", map[string]any{"id": call.ID, "status": CallError, "summary": "参数不是合法的 JSON 对象"})
		return false, false, l.appendTool(ctx, st, call.ID, errorContent("参数不是合法的 JSON 对象："+err.Error()))
	}
	rec := &ToolCallRecord{
		ID: call.ID, SessionID: env.SessionID, RunID: env.RunID, Tool: spec.Name, Risk: spec.Risk,
		Args: args, ArgsHash: HashArgs(args), RequiredPerm: spec.Permission,
	}
	switch l.Policy.Decide(env, spec) {
	case Deny:
		rec.Status, rec.Summary = CallDenied, "无权限"
		if err := l.Store.SaveToolCall(ctx, rec); err != nil {
			return false, false, err
		}
		l.emit("tool_result", map[string]any{"id": call.ID, "status": CallDenied, "summary": rec.Summary})
		return false, false, l.appendTool(ctx, st, call.ID, errorContent("当前管理员没有权限 "+spec.Permission+"，无法使用该工具。请如实告知用户。"))

	case Propose:
		return l.propose(ctx, st, tool, spec, rec)
	}

	rec.Status = CallRunning
	if err := l.Store.SaveToolCall(ctx, rec); err != nil {
		return false, false, err
	}
	start := l.now()
	res, callErr := tool.Call(WithCall(ctx, CallRef{SessionID: env.SessionID, RunID: env.RunID, ToolCallID: call.ID}), env, args)
	rec.DurationMS = int(l.now().Sub(start) / time.Millisecond)
	if callErr != nil {
		if ctx.Err() != nil {
			return false, false, ctx.Err()
		}
		res = Result{IsError: true, Content: map[string]any{"error": callErr.Error()}, Summary: "执行失败"}
	}
	rec.HTTPStatus, rec.Summary = res.HTTPStatus, res.Summary
	rec.Status = CallDone
	if res.IsError {
		rec.Status = CallError
	}
	rec.Result, _ = json.Marshal(res.Content)
	if err := l.Store.FinishToolCall(ctx, rec); err != nil {
		return false, false, err
	}
	if l.Hooks.AfterToolCall != nil {
		l.Hooks.AfterToolCall(ctx, env, rec)
	}
	l.emit("tool_result", map[string]any{"id": call.ID, "status": rec.Status, "http_status": rec.HTTPStatus, "summary": rec.Summary, "duration_ms": rec.DurationMS})
	return false, false, l.appendTool(ctx, st, call.ID, ToolResultContent(spec, res, l.MaxResultBytes))
}

func (l *Loop) propose(ctx context.Context, st *RunState, tool Tool, spec ToolSpec, rec *ToolCallRecord) (pending, proposed bool, err error) {
	env := st.Env
	reject := func(msg string) (bool, bool, error) {
		l.emit("tool_result", map[string]any{"id": rec.ID, "status": CallError, "summary": "提案未通过校验"})
		return false, false, l.appendTool(ctx, st, rec.ID, errorContent(msg))
	}
	proposer, ok := tool.(Proposer)
	if !ok {
		return reject("该写工具不支持提案，未执行。")
	}
	meta, rest, err := SplitProposalMeta(rec.Args)
	if err != nil {
		return reject("参数不是合法的 JSON 对象。")
	}
	if l.Hooks.BeforePropose != nil {
		if err := l.Hooks.BeforePropose(ctx, env, spec, meta, rest); err != nil {
			return reject("提案被退回：" + err.Error() + "。请修正后重新提出。")
		}
	}
	p, err := proposer.Propose(ctx, env, rest)
	if err != nil {
		if ctx.Err() != nil {
			return false, false, ctx.Err()
		}
		return reject("无法生成提案：" + err.Error())
	}
	rec.Status, rec.Summary, rec.ETag = CallPendingApproval, p.Summary, p.ETag
	rec.Before, _ = json.Marshal(p.Before)
	rec.After, _ = json.Marshal(p.After)
	var evidence json.RawMessage
	if len(meta.Evidence) > 0 {
		evidence, _ = json.Marshal(meta.Evidence)
	}
	pr := &ProposalRecord{
		ToolCallID: rec.ID, SessionID: env.SessionID, JobID: env.JobID, Playbook: env.Playbook, Tool: spec.Name,
		TargetType: p.TargetType, TargetID: p.TargetID, Summary: p.Summary, Rationale: meta.Rationale,
		Evidence: evidence, Confidence: meta.Confidence, RequiredPerm: spec.Permission,
	}
	if err := l.Store.SaveProposal(ctx, rec, pr); err != nil {
		if errors.Is(err, ErrDuplicateProposal) {
			return reject("该对象已有一条相同操作的待审批提案，无需重复提出。")
		}
		return false, false, err
	}
	if env.Mode == ModeBatch {
		l.emit("tool_result", map[string]any{"id": rec.ID, "status": CallPendingApproval, "summary": p.Summary})
		return false, true, l.appendTool(ctx, st, rec.ID, map[string]any{
			"status": "proposed", "proposal_id": pr.ID,
			"note": "已生成提案，等待人工审批（不会立即执行）。继续处理下一项。",
		})
	}
	l.emit("approval_required", map[string]any{
		"id": rec.ID, "proposal_id": pr.ID, "tool": spec.Name, "args": json.RawMessage(rest), "summary": p.Summary,
		"before": json.RawMessage(rec.Before), "after": json.RawMessage(rec.After), "permission": spec.Permission,
		"rationale": meta.Rationale, "confidence": meta.Confidence, "evidence": meta.Evidence,
		"target_type": p.TargetType, "target_id": p.TargetID,
	})
	return true, true, nil
}

func (l *Loop) appendTool(ctx context.Context, st *RunState, callID string, content any) error {
	m := Message{Role: RoleTool, ToolCallID: callID, Content: contentString(content)}
	if err := l.Store.AppendMessage(ctx, st.Env.SessionID, &m); err != nil {
		return err
	}
	st.History = append(st.History, m)
	return nil
}

// ToolResultContent 把工具结果包装成交给模型的文本：结果超过上限时截断；
// 外部来源的结果用 <tool_result trusted="false"> 包裹，提示模型其中的文字是数据而非指令（§3.4）。
func ToolResultContent(spec ToolSpec, res Result, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 8 << 10
	}
	body, err := json.Marshal(res.Content)
	if err != nil {
		body = []byte(`{"error":"unencodable result"}`)
	}
	s := string(body)
	truncated := false
	if len(s) > maxBytes {
		s = truncateUTF8(s, maxBytes)
		truncated = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<tool_result tool=%q status="%d" trusted="%t"`, spec.Name, res.HTTPStatus, spec.Trusted)
	if spec.Source != "" {
		fmt.Fprintf(&b, ` source=%q`, spec.Source)
	}
	if truncated {
		b.WriteString(` truncated="true"`)
	}
	b.WriteString(">\n")
	b.WriteString(s)
	if truncated {
		b.WriteString("\n…（结果过长已截断，请用更窄的筛选条件或分页参数重新查询）")
	}
	b.WriteString("\n</tool_result>")
	return b.String()
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

func errorContent(msg string) map[string]any { return map[string]any{"error": msg} }

func contentString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func rawOrString(s string) any {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return s
}

func ctxReason(ctx context.Context) string {
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "cancelled"
}
