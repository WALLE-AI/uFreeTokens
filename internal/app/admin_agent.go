package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/jobs"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/playbooks"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
)

// 运营智能体（Harness）的 HTTP 层：会话、SSE 运行、审批、提案收件箱（设计 §5，实施方案 M1-B07 / M2-B03 / M2-B10）。
// SSE 事件协议见 docs/admin-api.md「智能体」一节。

// ---------- DTO ----------

type agentToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
	Permission  string `json:"permission"`
}

type agentPlaybookInfo struct {
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	TargetType   string   `json:"target_type"`
	Starter      string   `json:"starter"`
	AllowedTools []string `json:"allowed_tools"`
	// Usable 为 false 表示当前管理员缺少该剧本全部工具的权限（剧本仍可用，但工具会被裁剪）。
	Usable bool `json:"usable"`
}

type agentMetaResponse struct {
	Enabled      bool                `json:"enabled"`
	JobsEnabled  bool                `json:"jobs_enabled"`
	Model        string              `json:"model"`
	Missing      []string            `json:"missing"`
	MaxTurns     int                 `json:"max_turns"`
	MaxToolCalls int                 `json:"max_tool_calls"`
	MaxTokens    int                 `json:"max_tokens"`
	Tools        []agentToolInfo     `json:"tools"`
	Playbooks    []agentPlaybookInfo `json:"playbooks"`
}

type agentSessionDetail struct {
	Session   pgstore.Session        `json:"session"`
	Messages  []kernel.Message       `json:"messages"`
	ToolCalls []pgstore.ToolCallView `json:"tool_calls"`
	// ReadOnly 为 true 表示当前管理员不是发起人（只能查看，不能继续对话）。
	ReadOnly bool `json:"read_only"`
}

type agentUpdateSessionRequest struct {
	Title    *string `json:"title"`
	Archived *bool   `json:"archived"`
}

type agentMessageRequest struct {
	Content string `json:"content"`
}

type agentProposalsResponse struct {
	Data       []pgstore.Proposal `json:"data"`
	NextCursor string             `json:"next_cursor"`
}

type agentPendingCountResponse struct {
	Pending int `json:"pending"`
}

// agentSSEEvent 仅用于文档：SSE 流中每个事件的 data 形状随 event 类型变化，见 docs/admin-api.md。
type agentSSEEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// ---------- 前置检查与错误映射 ----------

func (h *adminHandlers) requireAgent(w http.ResponseWriter, r *http.Request) bool {
	if !h.agent.Enabled() {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "agent_disabled", "The operations agent is disabled or its LLM is not configured.")
		return false
	}
	if actor(r).BreakGlass {
		httpx.WriteError(w, r, http.StatusForbidden, "break_glass_forbidden", "The emergency token identity cannot use the agent; sign in with an admin account.")
		return false
	}
	return true
}

func writeAgentError(w http.ResponseWriter, r *http.Request, h *adminHandlers, err error) {
	switch {
	case errors.Is(err, agent.ErrDisabled):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "agent_disabled", "The operations agent is disabled.")
	case errors.Is(err, agent.ErrBreakGlass):
		httpx.WriteError(w, r, http.StatusForbidden, "break_glass_forbidden", "The emergency token identity cannot use the agent.")
	case errors.Is(err, agent.ErrForbidden):
		httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", strings.TrimPrefix(err.Error(), "agent: forbidden: "))
	case errors.Is(err, agent.ErrAlreadyDecided):
		httpx.WriteError(w, r, http.StatusConflict, "already_decided", err.Error())
	case errors.Is(err, agent.ErrReadOnlySession):
		httpx.WriteError(w, r, http.StatusConflict, "read_only_session", "Background job sessions are read-only; start a new session to continue.")
	case errors.Is(err, pgstore.ErrBusy):
		httpx.WriteError(w, r, http.StatusConflict, "session_busy", "This session is already running; wait for it to finish or cancel it.")
	case errors.Is(err, pgstore.ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Agent session or tool call not found.")
	case errors.Is(err, agent.ErrInvalidInput):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", strings.TrimPrefix(err.Error(), "agent: invalid input: "))
	default:
		writeAdminError(w, r, h.log, err)
	}
}

// loadSession 读取会话并校验访问权：发起人可读写；有 audit:read 的管理员只读。
func (h *adminHandlers) loadSession(w http.ResponseWriter, r *http.Request, write bool) (*pgstore.Session, bool) {
	id, ok := pathID(w, r, "sessionID", "agent session")
	if !ok {
		return nil, false
	}
	sess, err := h.agent.Store.GetSession(r.Context(), id)
	if err != nil {
		writeAgentError(w, r, h, err)
		return nil, false
	}
	p := actor(r)
	if sess.AdminUserID != p.AdminID {
		if write || !p.Can(adminauth.PermAuditRead) {
			// 不暴露他人会话是否存在
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Agent session not found.")
			return nil, false
		}
	}
	return sess, true
}

// ---------- 元信息 ----------

// agentMeta 是 GET /agent/meta：未启用时也返回 200（enabled=false），前端据此决定是否渲染任何入口。
func (h *adminHandlers) agentMeta(w http.ResponseWriter, r *http.Request) {
	p := actor(r)
	resp := agentMetaResponse{Missing: []string{}, Tools: []agentToolInfo{}, Playbooks: []agentPlaybookInfo{}}
	if h.agent == nil {
		resp.Missing = append(resp.Missing, "agent service not configured")
		httpx.WriteJSON(w, http.StatusOK, resp)
		return
	}
	resp.Enabled = h.agent.Enabled() && !p.BreakGlass
	resp.JobsEnabled = h.agentInfo.JobsEnabled
	resp.Model = h.agent.Cfg.Model
	if h.agent.Missing != nil {
		resp.Missing = h.agent.Missing
	}
	resp.MaxTurns, resp.MaxToolCalls, resp.MaxTokens = h.agent.Cfg.Budget.MaxTurns, h.agent.Cfg.Budget.MaxToolCalls, h.agent.Cfg.Budget.MaxTokens
	for _, t := range h.agent.ToolsFor(p, nil, kernel.ModeInteractive) {
		s := t.Spec()
		resp.Tools = append(resp.Tools, agentToolInfo{Name: s.Name, Description: s.Description, Risk: string(s.Risk), Permission: s.Permission})
	}
	for _, pb := range playbooks.All() {
		resp.Playbooks = append(resp.Playbooks, agentPlaybookInfo{
			Name: pb.Name, Title: pb.Title, Description: pb.Description, TargetType: pb.TargetType, Starter: pb.Starter,
			AllowedTools: pb.AllowedTools, Usable: len(h.agent.ToolsFor(p, pb, kernel.ModeInteractive)) > 0,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// ---------- 会话 ----------

func (h *adminHandlers) listAgentSessions(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	p := actor(r)
	q := &queryParser{r: r}
	in := pgstore.ListSessionsInput{AdminUserID: p.AdminID, Q: q.str("q"), Before: q.str("before"), Limit: q.int("limit"),
		Mode: q.enum("mode", "interactive", "batch")}
	in.IncludeArchived = r.URL.Query().Get("include_archived") == "true"
	if !q.ok(w) {
		return
	}
	// 查看他人 / 全部会话（含后台作业产生的会话）需要 audit:read。
	scope := r.URL.Query().Get("scope")
	if v := r.URL.Query().Get("admin_user_id"); v != "" || scope == "all" {
		if !p.Can(adminauth.PermAuditRead) {
			forbid(w, r, adminauth.PermAuditRead)
			return
		}
		in.AdminUserID = 0
		if v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid admin_user_id")
				return
			}
			in.AdminUserID = id
		}
	}
	list, next, err := h.agent.Store.ListSessions(r.Context(), in)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, cursorPage[pgstore.Session]{Data: list, NextCursor: next})
}

func (h *adminHandlers) createAgentSession(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	var in agent.CreateSessionInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	sess, err := h.agent.Create(r.Context(), actor(r), in)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sess)
}

func (h *adminHandlers) getAgentSession(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	sess, ok := h.loadSession(w, r, false)
	if !ok {
		return
	}
	msgs, err := h.agent.Store.Messages(r.Context(), sess.ID)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	calls, err := h.agent.Store.ToolCalls(r.Context(), sess.ID)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, agentSessionDetail{Session: *sess, Messages: msgs, ToolCalls: calls,
		ReadOnly: sess.AdminUserID != actor(r).AdminID || sess.Mode == string(kernel.ModeBatch)})
}

func (h *adminHandlers) updateAgentSession(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	sess, ok := h.loadSession(w, r, true)
	if !ok {
		return
	}
	var in agentUpdateSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if len([]rune(t)) > 100 {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "title must be at most 100 characters")
			return
		}
		in.Title = &t
	}
	if err := h.agent.Store.UpdateSession(r.Context(), sess.ID, in.Title, in.Archived); err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	out, err := h.agent.Store.GetSession(r.Context(), sess.ID)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *adminHandlers) cancelAgentSession(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	sess, ok := h.loadSession(w, r, true)
	if !ok {
		return
	}
	if err := h.agent.Cancel(r.Context(), sess.ID); err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, statusResponse{Status: "cancel_requested"})
}

// sendAgentMessage 是 POST /agent/sessions/{id}/messages：响应为 text/event-stream。
// 运行与请求解耦：客户端断开不中断运行（ctx 用 WithoutCancel，时长由运行超时控制），重连后
// GET 会话详情拉全量。
func (h *adminHandlers) sendAgentMessage(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	sess, ok := h.loadSession(w, r, true)
	if !ok {
		return
	}
	var in agentMessageRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	sse := newSSEWriter(w, r)
	defer sse.close()
	_, err := h.agent.Send(context.WithoutCancel(r.Context()), actor(r), sess, in.Content, sse)
	if err != nil && !sse.started() {
		writeAgentError(w, r, h, err)
	}
}

// decideAgentToolCall 是 POST /agent/sessions/{id}/tool-calls/{callID}/decision。
// 路由层只要求 agent:use；审批人必须拥有该提案绑定路由的权限（handler 内校验）。
// 发起人之外、有对应权限的管理员也可以处理（提案收件箱）。?stream=1 或 Accept: text/event-stream 时
// 以 SSE 返回（交互会话审批后继续运行），否则返回 JSON。
func (h *adminHandlers) decideAgentToolCall(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	id, ok := pathID(w, r, "sessionID", "agent session")
	if !ok {
		return
	}
	sess, err := h.agent.Store.GetSession(r.Context(), id)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	var in agent.DecideInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	callID := chi.URLParam(r, "callID")
	stream := r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	ctx := context.WithoutCancel(r.Context())
	if !stream {
		res, err := h.agent.Decide(ctx, actor(r), sess, callID, in, kernel.NopSink, true)
		if err != nil && res == nil {
			writeAgentError(w, r, h, err)
			return
		}
		invalidateTodoCache()
		httpx.WriteJSON(w, http.StatusOK, res)
		return
	}
	sse := newSSEWriter(w, r)
	defer sse.close()
	res, err := h.agent.Decide(ctx, actor(r), sess, callID, in, sse, false)
	if err != nil && res == nil && !sse.started() {
		writeAgentError(w, r, h, err)
		return
	}
	invalidateTodoCache()
	h.afterDecision()
	if res != nil {
		sse.Emit(kernel.Event{Type: "decision", Data: res})
	}
}

// afterDecision 在审批后检查后台作业的拒绝率熔断（拒绝发生在这里，不必等下一次作业运行）。
func (h *adminHandlers) afterDecision() {
	if h.agentJobs == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r := &jobs.Runner{Store: h.agentJobs, Logger: h.log}
		if err := r.CheckBreakers(ctx); err != nil {
			h.log.Warn("agent job breaker check failed", "error", err)
		}
	}()
}

// ---------- 后台智能作业（agent:admin） ----------

func (h *adminHandlers) requireAgentJobs(w http.ResponseWriter, r *http.Request) bool {
	if h.agentJobs == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "agent_disabled", "Agent jobs are not configured on this server.")
		return false
	}
	return true
}

func (h *adminHandlers) listAgentJobs(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgentJobs(w, r) {
		return
	}
	list, err := h.agentJobs.List(r.Context())
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, listData[jobs.Job]{Data: list})
}

func (h *adminHandlers) updateAgentJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgentJobs(w, r) {
		return
	}
	id, ok := pathID(w, r, "jobID", "agent job")
	if !ok {
		return
	}
	var in jobs.UpdateInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	before, err := h.agentJobs.Get(r.Context(), id)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	job, err := audited(h, r, func(ctx context.Context) (*jobs.Job, auditEntry, error) {
		job, err := h.agentJobs.Update(ctx, id, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return job, auditEntry{"agent_job.update", "agent_job", idStr(id), before, in}, nil
	})
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, job)
}

func (h *adminHandlers) runAgentJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgentJobs(w, r) {
		return
	}
	id, ok := pathID(w, r, "jobID", "agent job")
	if !ok {
		return
	}
	job, err := audited(h, r, func(ctx context.Context) (*jobs.Job, auditEntry, error) {
		if err := h.agentJobs.RequestRun(ctx, id); err != nil {
			return nil, auditEntry{}, err
		}
		job, err := h.agentJobs.Get(ctx, id)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return job, auditEntry{"agent_job.run", "agent_job", idStr(id), nil, map[string]any{"run_requested": true}}, nil
	})
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, job)
}

// ---------- 提案 ----------

// listAgentProposals 是 GET /agent/proposals：收件箱与页面行内建议共用。
//   - target_type + target_ids=1,2,3：列表页一次请求取回所有行的建议；
//   - mine=1（默认）：只返回我有权限处理的提案。
func (h *adminHandlers) listAgentProposals(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	qv := r.URL.Query()
	q := &queryParser{r: r}
	f := pgstore.ProposalFilter{
		Status:     q.enum("status", "pending", "approved", "rejected", "executed", "failed", "stale", "superseded"),
		TargetType: q.str("target_type"), Playbook: q.str("playbook"), SessionID: q.int64("session_id"),
		BeforeID: q.int64("before"), Limit: q.int("limit"),
	}
	if !q.ok(w) {
		return
	}
	if v := qv.Get("target_ids"); v != "" {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				f.TargetIDs = append(f.TargetIDs, id)
			}
		}
		if len(f.TargetIDs) > 500 {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "at most 500 target_ids")
			return
		}
		if f.Limit == 0 {
			f.Limit = 200
		}
	}
	if qv.Get("mine") != "0" {
		f.Perms = permsOf(actor(r))
	}
	// 目标对象已被人工处理的提案惰性标记为 superseded（worker 每次 tick 也会做）。
	if f.Status == "" || f.Status == "pending" {
		if _, err := jobs.SupersedeHandled(r.Context(), h.agent.Store.Pool()); err != nil {
			h.log.Warn("supersede handled proposals failed", "error", err)
		}
	}
	list, err := h.agent.Store.ListProposals(r.Context(), f)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	next := ""
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(list) == limit {
		next = strconv.FormatInt(list[len(list)-1].ID, 10)
	}
	httpx.WriteJSON(w, http.StatusOK, agentProposalsResponse{Data: list, NextCursor: next})
}

// agentPendingCount 是 GET /agent/proposals/pending-count：侧栏徽标 = 我有权限处理的待审提案数。
func (h *adminHandlers) agentPendingCount(w http.ResponseWriter, r *http.Request) {
	if !h.agent.Enabled() {
		httpx.WriteJSON(w, http.StatusOK, agentPendingCountResponse{})
		return
	}
	n, err := h.agent.Store.CountPending(r.Context(), permsOf(actor(r)))
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, agentPendingCountResponse{Pending: n})
}

// permsOf 返回管理员的权限点列表；拥有 * 时返回 nil（不过滤）。
func permsOf(p *adminauth.Principal) []string {
	if p.Can(adminauth.PermAll) {
		return nil
	}
	out := make([]string, 0, len(p.Permissions))
	for _, x := range p.Permissions {
		out = append(out, string(x))
	}
	return out
}

// ---------- 数据源试运行 / 优惠抽取预览（M2-B08，非智能体专用） ----------

type priceSourceDryRunRequest = pricesync.DryRunInput
type offerExtractPreviewRequest = offers.PreviewInput

// dryRunPriceSource 是 POST /price-sources/dry-run：抓取并解析一次，不写库。会发起出站请求，按 pricing:write 收口。
func (h *adminHandlers) dryRunPriceSource(w http.ResponseWriter, r *http.Request) {
	if h.dsEnv == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Outbound fetching is not configured on this server.")
		return
	}
	var in priceSourceDryRunRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := pricesync.DryRun(ctx, h.dsEnv, in)
	switch {
	case errors.Is(err, pricesync.ErrUnsupportedFetcher):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%v; supported: %s", err, strings.Join(pricesync.DryRunFetchers(), ", ")))
	case err != nil && strings.HasPrefix(err.Error(), "datasync:"):
		httpx.WriteError(w, r, http.StatusBadGateway, "fetch_failed", err.Error())
	case err != nil:
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		httpx.WriteJSON(w, http.StatusOK, res)
	}
}

// extractOfferPreview 是 POST /offer-pages/extract-preview：对单个页面跑一次优惠抽取与证据校验，不写库。
func (h *adminHandlers) extractOfferPreview(w http.ResponseWriter, r *http.Request) {
	if h.dsEnv == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Outbound fetching is not configured on this server.")
		return
	}
	if h.offerLLM == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "llm_not_configured", "Server has no LLM configured (config datasync.llm_*).")
		return
	}
	var in offerExtractPreviewRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 80*time.Second)
	defer cancel()
	res, err := offers.ExtractPreview(ctx, h.dsEnv, h.offerLLM, in)
	switch {
	case err != nil && strings.HasPrefix(err.Error(), "datasync:"):
		httpx.WriteError(w, r, http.StatusBadGateway, "fetch_failed", err.Error())
	case err != nil && strings.Contains(err.Error(), "required"):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
	case err != nil:
		h.log.Warn("offer extract preview failed", "request_id", httpx.RequestIDFromContext(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, "llm_unavailable", "The LLM call failed or returned unusable output.")
	default:
		httpx.WriteJSON(w, http.StatusOK, res)
	}
}

// ---------- SSE ----------

// sseWriter 把内核事件写成 text/event-stream：首个事件到来时才写响应头（之前出错仍可返回 JSON 错误），
// 每 15 秒发一次 ": ping" 保活，解除 cmd/admin 的 WriteTimeout。客户端断开后写入失败被忽略，运行继续。
type sseWriter struct {
	w    http.ResponseWriter
	rc   *http.ResponseController
	mu   sync.Mutex
	open bool
	dead bool
	stop chan struct{}
	once sync.Once
}

func newSSEWriter(w http.ResponseWriter, _ *http.Request) *sseWriter {
	return &sseWriter{w: w, rc: http.NewResponseController(w), stop: make(chan struct{})}
}

func (s *sseWriter) started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

func (s *sseWriter) begin() {
	if s.open {
		return
	}
	s.open = true
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // Nginx 不缓冲
	_ = s.rc.SetWriteDeadline(time.Time{})
	s.w.WriteHeader(http.StatusOK)
	_ = s.rc.Flush()
	go s.ping()
}

func (s *sseWriter) ping() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.mu.Lock()
			if !s.dead {
				if _, err := s.w.Write([]byte(": ping\n\n")); err != nil {
					s.dead = true
				} else {
					_ = s.rc.Flush()
				}
			}
			s.mu.Unlock()
		}
	}
}

// Emit 实现 kernel.Sink。
func (s *sseWriter) Emit(e kernel.Event) {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.begin()
	if s.dead {
		return
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", e.Type, data); err != nil {
		s.dead = true
		return
	}
	if err := s.rc.Flush(); err != nil {
		s.dead = true
	}
}

func (s *sseWriter) close() { s.once.Do(func() { close(s.stop) }) }
