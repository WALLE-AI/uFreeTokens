// Package agent 是运营后台智能体（Harness）的编排层：把 kernel（循环）、tools（路由工具与研究工具）、
// playbooks（剧本）与 pgstore（持久化）组装成会话、运行、审批与恢复（设计 §3、§15）。
// HTTP 层（internal/app/admin_agent.go）与后台作业（jobs）都只调用这里。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/playbooks"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/routes"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
)

// Config 是编排层的配置（来自 config.AgentConfig）。
type Config struct {
	Enabled    bool
	Model      string
	BatchModel string
	Budget     kernel.Budget
	RunTimeout time.Duration
	// CompactAfterTokens 是历史压缩阈值（粗估 Token）；0 = 60000。
	CompactAfterTokens int
	Location           *time.Location
}

// AuditEvent 是一次审批决定的审计内容（动作 agent.decision）。
type AuditEvent struct {
	Actor      *adminauth.Principal
	SessionID  int64
	ToolCallID string
	Decision   string
	Tool       string
	TargetType string
	TargetID   string
	Note       string
	Args       json.RawMessage
}

// Executor 是能以审批人身份执行的写工具（routes.Tool 实现它）。
type Executor interface {
	Execute(ctx context.Context, approver *adminauth.Principal, ref kernel.CallRef, args json.RawMessage, etag string) (routes.ExecOutcome, kernel.Result, error)
}

// Service 编排会话、运行与审批。
type Service struct {
	Cfg    Config
	Store  *pgstore.Store
	Model  kernel.Model // nil = 未配置 LLM，智能体不可用
	Tools  []kernel.Tool
	Audit  func(ctx context.Context, e AuditEvent) error
	Logger *slog.Logger
	// Missing 是未启用时缺失的配置项（/agent/meta 与启动日志展示）。
	Missing []string
	// Now 便于测试注入；nil = time.Now。
	Now func() time.Time
}

// 错误：HTTP 层据此映射状态码。
var (
	ErrDisabled        = errors.New("agent: disabled")
	ErrForbidden       = errors.New("agent: forbidden")
	ErrAlreadyDecided  = errors.New("agent: tool call is not pending approval")
	ErrInvalidInput    = errors.New("agent: invalid input")
	ErrReadOnlySession = errors.New("agent: background job sessions are read-only")
	ErrBreakGlass      = routes.ErrBreakGlass
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Enabled 表示智能体可用（开关打开且 LLM 已配置）。
func (s *Service) Enabled() bool { return s != nil && s.Cfg.Enabled && s.Model != nil }

func (s *Service) toolByName(name string) kernel.Tool {
	for _, t := range s.Tools {
		if t.Spec().Name == name {
			return t
		}
	}
	return nil
}

// ToolsFor 返回某个管理员在某个剧本下可用的工具：剧本允许的工具 ∩ 有权限的工具（设计 §3.4：
// 只把有权限的工具发给模型）。playbook 为 nil 时为全部有权限的工具。批处理模式下写工具总是可见
// （只读服务主体只能产生提案，见 kernel.DefaultPolicy）。
func (s *Service) ToolsFor(p *adminauth.Principal, pb *playbooks.Playbook, mode kernel.Mode) []kernel.Tool {
	var allowed map[string]bool
	if pb != nil {
		allowed = map[string]bool{}
		for _, n := range pb.AllowedTools {
			allowed[n] = true
		}
	}
	var out []kernel.Tool
	for _, t := range s.Tools {
		spec := t.Spec()
		if allowed != nil && !allowed[spec.Name] {
			continue
		}
		if spec.Permission != "" && !p.Can(adminauth.Permission(spec.Permission)) && !(mode == kernel.ModeBatch && spec.Risk == kernel.RiskWrite) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ---------- 会话 ----------

// CreateSessionInput 是新建会话的参数。
type CreateSessionInput struct {
	Title      string          `json:"title"`
	Playbook   string          `json:"playbook"`
	ContextRef json.RawMessage `json:"context_ref"`
}

// Create 新建交互会话。
func (s *Service) Create(ctx context.Context, p *adminauth.Principal, in CreateSessionInput) (*pgstore.Session, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	if p.BreakGlass {
		return nil, ErrBreakGlass
	}
	if in.Playbook != "" {
		if _, ok := playbooks.Get(in.Playbook); !ok {
			return nil, fmt.Errorf("%w: unknown playbook %q", ErrInvalidInput, in.Playbook)
		}
	}
	refs, err := parseContextRefs(in.ContextRef)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	var refJSON json.RawMessage
	if len(refs) > 0 {
		refJSON, _ = json.Marshal(refs)
	}
	title := strings.TrimSpace(in.Title)
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	if title == "" && in.Playbook != "" {
		pb, _ := playbooks.Get(in.Playbook)
		title = pb.Title
	}
	return s.Store.CreateSession(ctx, pgstore.NewSession{
		AdminUserID: p.AdminID, Title: title, Playbook: in.Playbook, ContextRef: refJSON,
		Mode: kernel.ModeInteractive, Model: s.Cfg.Model,
	})
}

// Send 追加一条用户消息并运行（同步执行，事件经 sink 推出）。会话处于等待审批时，未处理的提案
// 作废（superseded），并在历史中注明“用户继续了对话”。
func (s *Service) Send(ctx context.Context, p *adminauth.Principal, sess *pgstore.Session, text string, sink kernel.Sink) (kernel.Outcome, error) {
	if !s.Enabled() {
		return kernel.Outcome{}, ErrDisabled
	}
	if p.BreakGlass {
		return kernel.Outcome{}, ErrBreakGlass
	}
	if sess.Mode == string(kernel.ModeBatch) {
		return kernel.Outcome{}, ErrReadOnlySession
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 20000 {
		return kernel.Outcome{}, fmt.Errorf("%w: message must be 1-20000 bytes", ErrInvalidInput)
	}
	return s.run(ctx, p, sess, sink, func(ctx context.Context) error {
		if err := s.supersedePending(ctx, sess.ID, "用户发送了新消息，该提案未审批即作废。"); err != nil {
			return err
		}
		if sess.Title == "" {
			title := truncRunes(strings.Join(strings.Fields(text), " "), 40)
			_ = s.Store.UpdateSession(ctx, sess.ID, &title, nil)
		}
		return s.Store.AppendMessage(ctx, sess.ID, &kernel.Message{Role: kernel.RoleUser, Content: text})
	})
}

// RunBatch 在后台作业会话中运行一次（批处理模式：写工具只落提案、不暂停），结束后写一条运行报告消息。
// p 是只读服务主体（agent-bot）；instruction 是本次待处理对象的说明。
func (s *Service) RunBatch(ctx context.Context, p *adminauth.Principal, sess *pgstore.Session, instruction string) (kernel.Outcome, error) {
	if !s.Enabled() {
		return kernel.Outcome{}, ErrDisabled
	}
	out, err := s.run(ctx, p, sess, kernel.NopSink, func(ctx context.Context) error {
		return s.Store.AppendMessage(ctx, sess.ID, &kernel.Message{Role: kernel.RoleUser, Content: instruction})
	})
	report := fmt.Sprintf("运行报告：状态 %s%s；轮数 %d；Token %d/%d；生成提案 %d 条（已进入提案收件箱，等待人工审批）。",
		out.Status, map[bool]string{true: "（" + out.Reason + "）", false: ""}[out.Reason != ""], out.Turns, out.Usage.In, out.Usage.Out, out.Proposals)
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if aerr := s.Store.AppendMessage(rctx, sess.ID, &kernel.Message{Role: kernel.RoleReport, Content: report}); aerr != nil && err == nil {
		err = aerr
	}
	return out, err
}

func (s *Service) supersedePending(ctx context.Context, sessionID int64, note string) error {
	pending, err := s.Store.PendingCalls(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, c := range pending {
		ok, err := s.Store.ClaimDecision(ctx, c.ID, kernel.CallSuperseded, 0, note)
		if err != nil {
			return err
		}
		if ok {
			content, _ := json.Marshal(map[string]any{"status": "superseded", "note": note})
			if err := s.Store.AppendMessage(ctx, sessionID, &kernel.Message{Role: kernel.RoleTool, ToolCallID: c.ID, Content: string(content)}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Cancel 请求取消会话当前的运行。
func (s *Service) Cancel(ctx context.Context, sessionID int64) error {
	return s.Store.RequestCancel(ctx, sessionID)
}

// run 占用运行位 → prepare（追加用户消息等）→ 循环 → 写回状态。
func (s *Service) run(ctx context.Context, p *adminauth.Principal, sess *pgstore.Session, sink kernel.Sink, prepare func(ctx context.Context) error) (kernel.Outcome, error) {
	runID := strings.ToLower(ulid.Make().String())
	timeout := s.Cfg.RunTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if err := s.Store.ClaimRun(ctx, sess.ID, runID, s.now().Add(timeout+time.Minute)); err != nil {
		return kernel.Outcome{}, err
	}
	start := s.now()
	sink.Emit(kernel.Event{Type: "run_started", Data: map[string]any{"run_id": runID, "session_id": sess.ID}})
	finish := func(out kernel.Outcome, err error) (kernel.Outcome, error) {
		if out.Status == "" {
			out.Status, out.Reason = kernel.StatusFailed, "internal_error"
		}
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if ferr := s.Store.FinishRun(fctx, sess.ID, runID, out.Status, out.Reason); ferr != nil {
			s.logger().Error("agent: finish run failed", "session_id", sess.ID, "error", ferr)
		}
		if err != nil && out.Reason != "llm_unavailable" {
			s.logger().Error("agent: run failed", "session_id", sess.ID, "run_id", runID, "error", err)
			sink.Emit(kernel.Event{Type: "error", Data: map[string]any{"code": "internal_error", "message": "运行出错，请稍后重试。"}})
		}
		sink.Emit(kernel.Event{Type: "run_finished", Data: map[string]any{
			"status": out.Status, "reason": out.Reason, "tokens_in": out.Usage.In, "tokens_out": out.Usage.Out,
			"turns": out.Turns, "pending": out.Pending, "duration_ms": s.now().Sub(start).Milliseconds(),
		}})
		playbook := ""
		if sess.Playbook != nil {
			playbook = *sess.Playbook
		}
		observability.ObserveAgentRun(playbook, out.Status, out.Usage.In, out.Usage.Out, s.now().Sub(start))
		s.logger().Info("agent run finished", "session_id", sess.ID, "run_id", runID, "status", out.Status, "reason", out.Reason,
			"turns", out.Turns, "tokens_in", out.Usage.In, "tokens_out", out.Usage.Out, "playbook", playbook)
		return out, err
	}
	if prepare != nil {
		if err := prepare(ctx); err != nil {
			return finish(kernel.Outcome{}, err)
		}
	}
	st, err := s.state(ctx, p, sess, runID)
	if err != nil {
		return finish(kernel.Outcome{}, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	loop := &kernel.Loop{Model: s.Model, Store: s.Store, Sink: sink, Hooks: kernel.Hooks{
		TransformContext: func(msgs []kernel.Message) []kernel.Message {
			limit := s.Cfg.CompactAfterTokens
			if limit <= 0 {
				limit = 60000
			}
			return compact(repairHistory(msgs), limit)
		},
		BeforePropose: CheckEvidence,
		AfterToolCall: func(_ context.Context, _ *kernel.Env, rec *kernel.ToolCallRecord) {
			observability.ObserveAgentToolCall(rec.Tool, rec.Status)
		},
	}}
	out, err := loop.Run(runCtx, st)
	return finish(out, err)
}

// state 由会话构造一次运行的输入。
func (s *Service) state(ctx context.Context, p *adminauth.Principal, sess *pgstore.Session, runID string) (*kernel.RunState, error) {
	var pb *playbooks.Playbook
	if sess.Playbook != nil && *sess.Playbook != "" {
		pb, _ = playbooks.Get(*sess.Playbook)
	}
	refs, _ := parseContextRefs(sess.ContextRef)
	history, err := s.Store.Messages(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	mode := kernel.Mode(sess.Mode)
	tools := s.ToolsFor(p, pb, mode)
	env := &kernel.Env{Principal: p, SessionID: sess.ID, RunID: runID, Mode: mode, JobID: sess.JobID, Pages: kernel.NewPageCache()}
	if pb != nil {
		env.Playbook = pb.Name
	}
	if err := s.preloadPages(ctx, sess.ID, env.Pages); err != nil {
		return nil, err
	}
	budget := s.Cfg.Budget
	if pb != nil {
		if pb.MaxTurns > 0 && (budget.MaxTurns == 0 || pb.MaxTurns < budget.MaxTurns) {
			budget.MaxTurns = pb.MaxTurns
		}
		if pb.MaxToolCalls > 0 && (budget.MaxToolCalls == 0 || pb.MaxToolCalls < budget.MaxToolCalls) {
			budget.MaxToolCalls = pb.MaxToolCalls
		}
	}
	model := sess.Model
	if model == "" {
		model = s.Cfg.Model
	}
	return &kernel.RunState{
		Env: env, Model: model, History: history, Tools: tools, Budget: budget,
		System: buildSystemPrompt(promptInput{Principal: p, Playbook: pb, Context: refs, Mode: mode, Tools: tools, Now: s.now(), Loc: s.Cfg.Location}),
	}, nil
}

// preloadPages 把本会话中 fetch_page 抓到的正文放回页面缓存：证据校验跨运行有效（审批后恢复、编辑参数）。
func (s *Service) preloadPages(ctx context.Context, sessionID int64, pages *kernel.PageCache) error {
	calls, err := s.Store.ToolCalls(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, c := range calls {
		if c.Tool != "fetch_page" || c.Status != kernel.CallDone {
			continue
		}
		var r struct {
			URL  string `json:"url"`
			Text string `json:"text"`
		}
		if json.Unmarshal(c.Result, &r) == nil && r.URL != "" && r.Text != "" {
			if prev, ok := pages.Get(r.URL); !ok || len(r.Text) > len(prev) {
				pages.Put(r.URL, r.Text)
			}
		}
	}
	return nil
}

// ---------- 证据校验（设计 §13.4，实施方案 M2-B07） ----------

// CheckEvidence 是 BeforePropose 钩子：提案带的每条 evidence，url 必须是本会话抓取过的页面，
// quote 必须逐字（空白归一化后）出现在该页正文中；确认优惠真实有效（set_offer_status=confirmed）
// 必须带证据。校验失败的提案不入库，错误回给模型重写。
func CheckEvidence(_ context.Context, env *kernel.Env, spec kernel.ToolSpec, meta kernel.ProposalMeta, args json.RawMessage) error {
	if spec.Name == "set_offer_status" {
		var a struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Status == "confirmed" && len(meta.Evidence) == 0 {
			return errors.New("确认优惠有效必须附 evidence（先用 fetch_page 抓取原页面，再摘录原文）")
		}
	}
	for i, e := range meta.Evidence {
		if e.URL == "" || strings.TrimSpace(e.Quote) == "" {
			return fmt.Errorf("evidence[%d] 缺少 url 或 quote", i)
		}
		text, ok := env.Pages.Get(e.URL)
		if !ok {
			return fmt.Errorf("evidence[%d] 的 url %s 未在本会话中用 fetch_page 抓取过", i, e.URL)
		}
		if !offers.EvidenceInText(text, e.Quote) {
			return fmt.Errorf("evidence[%d] 的 quote 没有逐字出现在 %s 的正文中（不要改写或拼接原文）", i, e.URL)
		}
	}
	return nil
}
