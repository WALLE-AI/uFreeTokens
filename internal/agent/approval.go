package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

// 审批与恢复（设计 §3.1、§3.3、§15.2，实施方案 M2-B03）。
//
// 核心安全不变式：写操作永远以“批准它的那个人”的身份、经该人对应路由的权限校验执行；
// 参数以库中提案为准（前端只传决定；“编辑后通过”的新参数重新做 Schema 与证据校验）；
// Idempotency-Key = agent:{callID}，重复审批只执行一次；对象在审批期间被修改 → 412/409 → stale。

// DecideInput 是一次审批决定。
type DecideInput struct {
	Decision string          `json:"decision"` // approve / reject
	Note     string          `json:"note"`
	Args     json.RawMessage `json:"args"` // 可选：编辑后的参数（仅 approve）
}

// DecideResult 是审批结果。
type DecideResult struct {
	ToolCallID string          `json:"tool_call_id"`
	Status     string          `json:"status"` // executed / stale / failed / rejected
	HTTPStatus int             `json:"http_status"`
	Summary    string          `json:"summary"`
	Result     json.RawMessage `json:"result"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	// Resumed 为 true 表示审批后智能体继续运行（交互模式，同一会话的提案都已处理）。
	Resumed bool            `json:"resumed"`
	Outcome *kernel.Outcome `json:"outcome,omitempty"`
}

// Decide 处理一次审批。交互会话在本轮全部提案处理完、且审批人就是会话发起人时继续运行：background=false 时
// 同步运行、事件经 sink 推出（SSE）；background=true 时在后台继续（JSON 请求不被整个运行阻塞，前端轮询会话）。
// 批处理会话只执行该工具，不再回到模型。
func (s *Service) Decide(ctx context.Context, approver *adminauth.Principal, sess *pgstore.Session, callID string, in DecideInput, sink kernel.Sink, background bool) (*DecideResult, error) {
	if approver.BreakGlass {
		return nil, ErrBreakGlass
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		return nil, fmt.Errorf("%w: decision must be approve or reject", ErrInvalidInput)
	}
	if len(in.Note) > 2000 {
		return nil, fmt.Errorf("%w: note too long", ErrInvalidInput)
	}
	call, err := s.Store.GetToolCall(ctx, callID)
	if err != nil {
		return nil, err
	}
	if call.SessionID != sess.ID {
		return nil, pgstore.ErrNotFound
	}
	if call.Status != kernel.CallPendingApproval {
		return nil, fmt.Errorf("%w (current status: %s)", ErrAlreadyDecided, call.Status)
	}
	if !approver.Can(adminauth.Permission(call.RequiredPerm)) {
		return nil, fmt.Errorf("%w: approving this proposal requires permission '%s'", ErrForbidden, call.RequiredPerm)
	}
	tool := s.toolByName(call.Tool)
	exec, ok := tool.(Executor)
	if tool == nil || !ok {
		return nil, fmt.Errorf("%w: tool %s is no longer available", ErrInvalidInput, call.Tool)
	}
	_, args, err := kernel.SplitProposalMeta(call.Args)
	if err != nil {
		return nil, err
	}
	etag := call.ETag
	if in.Decision == "approve" && len(in.Args) > 0 && string(in.Args) != "null" {
		args, etag, err = s.applyEdit(ctx, approver, sess, call, tool, in.Args)
		if err != nil {
			return nil, err
		}
	}

	to := kernel.CallApproved
	if in.Decision == "reject" {
		to = kernel.CallRejected
	}
	claimed, err := s.Store.ClaimDecision(ctx, call.ID, to, approver.AdminID, in.Note)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, ErrAlreadyDecided
	}
	observability.ObserveAgentDecision(in.Decision)
	if s.Audit != nil {
		if err := s.Audit(ctx, AuditEvent{Actor: approver, SessionID: sess.ID, ToolCallID: call.ID, Decision: in.Decision,
			Tool: call.Tool, TargetType: call.TargetType, TargetID: call.TargetID, Note: in.Note, Args: args}); err != nil {
			s.logger().Error("agent: audit decision failed", "tool_call_id", call.ID, "error", err)
		}
	}

	res := &DecideResult{ToolCallID: call.ID, TargetType: call.TargetType, TargetID: call.TargetID, Summary: call.Summary}
	var toolMsg any
	if in.Decision == "reject" {
		res.Status = kernel.CallRejected
		toolMsg = map[string]any{"status": "rejected", "decided_by": approver.Name, "note": in.Note,
			"message": "审批人拒绝了该操作，未执行。请参考拒绝原因调整建议，不要原样重复提出。"}
	} else {
		start := s.now()
		ref := kernel.CallRef{SessionID: sess.ID, RunID: call.RunID, ToolCallID: call.ID}
		outcome, r, err := exec.Execute(ctx, approver, ref, args, etag)
		if err != nil {
			_ = s.Store.FinishDecision(context.WithoutCancel(ctx), call.ID, kernel.CallFailed, 0, nil, 0)
			return nil, err
		}
		res.Status, res.HTTPStatus = string(outcome), r.HTTPStatus
		res.Result, _ = json.Marshal(r.Content)
		if r.Summary != "" {
			res.Summary = r.Summary
		}
		if err := s.Store.FinishDecision(ctx, call.ID, res.Status, r.HTTPStatus, res.Result, int(s.now().Sub(start)/time.Millisecond)); err != nil {
			return nil, err
		}
		switch res.Status {
		case kernel.CallExecuted:
			toolMsg = kernel.ToolResultContent(tool.Spec(), r, 0)
		case kernel.CallStale:
			toolMsg = map[string]any{"status": "stale", "http_status": r.HTTPStatus, "result": r.Content,
				"message": "对象在审批期间已被修改或状态已变化，操作未执行。请重新读取最新数据，再决定是否重新提案。"}
		default:
			toolMsg = map[string]any{"status": "failed", "http_status": r.HTTPStatus, "result": r.Content, "message": "执行失败，操作未生效。"}
		}
	}
	sink.Emit(kernel.Event{Type: "tool_result", Data: map[string]any{
		"id": call.ID, "status": res.Status, "http_status": res.HTTPStatus, "summary": res.Summary,
		"target_type": res.TargetType, "target_id": res.TargetID, "decided_by": approver.Name,
	}})

	if sess.Mode == string(kernel.ModeBatch) {
		// 批处理提案：模型早已收到“已提交提案”的结果，审批只执行工具，不回到模型。
		return res, nil
	}
	content, isStr := toolMsg.(string)
	if !isStr {
		b, _ := json.Marshal(toolMsg)
		content = string(b)
	}
	if err := s.Store.AppendMessage(ctx, sess.ID, &kernel.Message{Role: kernel.RoleTool, ToolCallID: call.ID, Content: content}); err != nil {
		return nil, err
	}
	// 同一轮的提案全部有了结果，才继续运行；只有会话发起人审批时才以其身份继续（智能体权限 ≤ 发起人）。
	done, err := s.turnResolved(ctx, sess.ID)
	if err != nil || !done {
		return res, err
	}
	if approver.AdminID != sess.AdminUserID {
		_ = s.Store.SetStatus(ctx, sess.ID, "idle", "decided_by_other")
		return res, nil
	}
	if background {
		res.Resumed = true
		go func() {
			if _, err := s.run(context.WithoutCancel(ctx), approver, sess, nil, kernel.NopSink, nil); err != nil && !errors.Is(err, pgstore.ErrBusy) {
				s.logger().Error("agent: background resume failed", "session_id", sess.ID, "error", err)
			}
		}()
		return res, nil
	}
	out, err := s.run(ctx, approver, sess, nil, sink, nil)
	if errors.Is(err, pgstore.ErrBusy) {
		return res, nil
	}
	res.Resumed, res.Outcome = true, &out
	return res, err
}

// applyEdit 校验“编辑后通过”的新参数：Schema / 必填项（由 Propose 重新构造）、证据，且不能改变操作对象。
func (s *Service) applyEdit(ctx context.Context, approver *adminauth.Principal, sess *pgstore.Session, call *pgstore.ToolCallView, tool kernel.Tool, raw json.RawMessage) (json.RawMessage, string, error) {
	meta, args, err := kernel.SplitProposalMeta(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: args must be a JSON object", ErrInvalidInput)
	}
	proposer, ok := tool.(kernel.Proposer)
	if !ok {
		return nil, "", fmt.Errorf("%w: tool cannot be edited", ErrInvalidInput)
	}
	env := &kernel.Env{Principal: approver, SessionID: sess.ID, RunID: call.RunID, Mode: kernel.Mode(sess.Mode), Pages: kernel.NewPageCache()}
	if err := s.preloadPages(ctx, sess.ID, env.Pages); err != nil {
		return nil, "", err
	}
	if err := CheckEvidence(ctx, env, tool.Spec(), meta, args); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	p, err := proposer.Propose(ctx, env, args)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if p.TargetType != call.TargetType || p.TargetID != call.TargetID {
		return nil, "", fmt.Errorf("%w: editing must not change the target object", ErrInvalidInput)
	}
	after, _ := json.Marshal(p.After)
	if err := s.Store.UpdateProposalArgs(ctx, call.ID, raw, kernel.HashArgs(raw), p.Summary, after); err != nil {
		return nil, "", err
	}
	// 编辑时重新读取了对象快照：用最新的 ETag（编辑者已看到最新状态）。
	etag := call.ETag
	if p.ETag != "" {
		etag = p.ETag
	}
	return args, etag, nil
}

// turnResolved 判断最近一条带工具调用的 assistant 消息的每个调用是否都已有结果。
func (s *Service) turnResolved(ctx context.Context, sessionID int64) (bool, error) {
	msgs, err := s.Store.Messages(ctx, sessionID)
	if err != nil {
		return false, err
	}
	last := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == kernel.RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			last = i
			break
		}
		if msgs[i].Role == kernel.RoleUser || msgs[i].Role == kernel.RoleAssistant {
			return false, nil // 之后已有新的对话，不需要恢复
		}
	}
	if last < 0 {
		return false, nil
	}
	have := map[string]bool{}
	for _, m := range msgs[last+1:] {
		if m.Role == kernel.RoleTool {
			have[m.ToolCallID] = true
		}
	}
	for _, c := range msgs[last].ToolCalls {
		if !have[c.ID] {
			return false, nil
		}
	}
	return true, nil
}
