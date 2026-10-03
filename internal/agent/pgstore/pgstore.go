// Package pgstore 是智能体会话、消息、工具调用与提案的 Postgres 存储（迁移 00032 / 00034），
// 实现 kernel.Store，并提供 HTTP 层需要的会话与提案查询。
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

// Store 实现 kernel.Store。
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) store.Querier { return store.Q(ctx, s.pool) }

// Pool 返回底层连接池（后台作业与提案失效检查共用）。
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// ErrNotFound 表示会话或工具调用不存在。
var ErrNotFound = errors.New("agent: not found")

// ErrBusy 表示会话已有运行在进行中。
var ErrBusy = errors.New("agent: session is running")

// Session 是一行 agent_sessions（含发起人名称）。
type Session struct {
	ID           int64           `json:"id"`
	AdminUserID  int64           `json:"admin_user_id"`
	AdminName    string          `json:"admin_name"`
	Title        string          `json:"title"`
	Playbook     *string         `json:"playbook"`
	ContextRef   json.RawMessage `json:"context_ref"`
	Mode         string          `json:"mode"`
	Status       string          `json:"status"`
	StatusReason string          `json:"status_reason"`
	Model        string          `json:"model"`
	TokensIn     int64           `json:"tokens_in"`
	TokensOut    int64           `json:"tokens_out"`
	Turns        int             `json:"turns"`
	Archived     bool            `json:"archived"`
	JobID        *int64          `json:"job_id"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

const sessionCols = `s.id, s.admin_user_id, COALESCE(u.name, ''), s.title, s.playbook, s.context_ref, s.mode, s.status, s.status_reason,
	s.model, s.tokens_in, s.tokens_out, s.turns, s.archived, s.job_id, s.created_at, s.updated_at`

func scanSession(row pgx.Row) (*Session, error) {
	var x Session
	err := row.Scan(&x.ID, &x.AdminUserID, &x.AdminName, &x.Title, &x.Playbook, &x.ContextRef, &x.Mode, &x.Status, &x.StatusReason,
		&x.Model, &x.TokensIn, &x.TokensOut, &x.Turns, &x.Archived, &x.JobID, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &x, err
}

// NewSession 是创建会话的参数。
type NewSession struct {
	AdminUserID int64
	Title       string
	Playbook    string
	ContextRef  json.RawMessage
	Mode        kernel.Mode
	Model       string
	JobID       *int64
}

// CreateSession 新建会话。
func (s *Store) CreateSession(ctx context.Context, in NewSession) (*Session, error) {
	var playbook *string
	if in.Playbook != "" {
		playbook = &in.Playbook
	}
	if in.Mode == "" {
		in.Mode = kernel.ModeInteractive
	}
	var ctxRef any
	if len(in.ContextRef) > 0 && string(in.ContextRef) != "null" {
		ctxRef = in.ContextRef
	}
	var id int64
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO agent_sessions (admin_user_id, title, playbook, context_ref, mode, model, job_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		in.AdminUserID, in.Title, playbook, ctxRef, string(in.Mode), in.Model, in.JobID).Scan(&id); err != nil {
		return nil, fmt.Errorf("agent: insert session: %w", err)
	}
	return s.GetSession(ctx, id)
}

// GetSession 读取会话。
func (s *Store) GetSession(ctx context.Context, id int64) (*Session, error) {
	return scanSession(s.db(ctx).QueryRow(ctx,
		`SELECT `+sessionCols+` FROM agent_sessions s LEFT JOIN admin_users u ON u.id = s.admin_user_id WHERE s.id = $1`, id))
}

// ListSessionsInput：AdminUserID=0 表示全部管理员；Before 是上一页最后一条的 updated_at|id 游标。
type ListSessionsInput struct {
	AdminUserID     int64
	IncludeArchived bool
	Mode            string
	Q               string
	Before          string
	Limit           int
}

// ListSessions 按 updated_at 倒序做 keyset 分页。
func (s *Store) ListSessions(ctx context.Context, in ListSessionsInput) ([]Session, string, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	var beforeAt *time.Time
	var beforeID int64
	if in.Before != "" {
		at, idStr, ok := strings.Cut(in.Before, "|")
		t, err1 := time.Parse(time.RFC3339Nano, at)
		id, err2 := strconv.ParseInt(idStr, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return nil, "", errors.New("invalid cursor")
		}
		beforeAt, beforeID = &t, id
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT `+sessionCols+` FROM agent_sessions s LEFT JOIN admin_users u ON u.id = s.admin_user_id
		 WHERE ($1 = 0 OR s.admin_user_id = $1) AND ($2 OR NOT s.archived) AND ($3 = '' OR s.mode = $3)
		   AND ($4 = '' OR s.title ILIKE '%' || $4 || '%')
		   AND ($5::timestamptz IS NULL OR (s.updated_at, s.id) < ($5, $6))
		 ORDER BY s.updated_at DESC, s.id DESC LIMIT $7`,
		in.AdminUserID, in.IncludeArchived, in.Mode, in.Q, beforeAt, beforeID, limit)
	if err != nil {
		return nil, "", fmt.Errorf("agent: list sessions: %w", err)
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *x)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = last.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(last.ID, 10)
	}
	return out, next, nil
}

// UpdateSession 修改标题 / 归档。
func (s *Store) UpdateSession(ctx context.Context, id int64, title *string, archived *bool) error {
	tag, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_sessions SET title = COALESCE($2, title), archived = COALESCE($3, archived), updated_at = now() WHERE id = $1`,
		id, title, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimRun 原子地占用会话的运行位：只有当前不在运行（或上一次运行已超过截止时间、视为失联）时成功。
func (s *Store) ClaimRun(ctx context.Context, sessionID int64, runID string, deadline time.Time) error {
	tag, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_sessions SET status = 'running', status_reason = '', run_id = $2, run_deadline = $3,
		        cancel_requested = false, updated_at = now()
		 WHERE id = $1 AND (status <> 'running' OR run_deadline IS NULL OR run_deadline < now())`,
		sessionID, runID, deadline)
	if err != nil {
		return fmt.Errorf("agent: claim run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetSession(ctx, sessionID); err != nil {
			return err
		}
		return ErrBusy
	}
	return nil
}

// FinishRun 结束运行，写入最终状态；只有仍持有该运行位时生效（被接管的旧运行不覆盖新状态）。
func (s *Store) FinishRun(ctx context.Context, sessionID int64, runID, status, reason string) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_sessions SET status = $3, status_reason = $4, run_deadline = NULL, updated_at = now()
		 WHERE id = $1 AND run_id = $2`, sessionID, runID, status, reason)
	return err
}

// SetStatus 直接设置会话状态（不在运行中时使用，例如审批后全部处理完）。
func (s *Store) SetStatus(ctx context.Context, sessionID int64, status, reason string) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_sessions SET status = $2, status_reason = $3, updated_at = now() WHERE id = $1 AND status <> 'running'`,
		sessionID, status, reason)
	return err
}

// RequestCancel 置位取消标记；运行在下一个检查点停止。
func (s *Store) RequestCancel(ctx context.Context, sessionID int64) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE agent_sessions SET cancel_requested = true WHERE id = $1`, sessionID)
	return err
}

// ---------- kernel.Store ----------

func (s *Store) AppendMessage(ctx context.Context, sessionID int64, m *kernel.Message) error {
	var calls any
	if len(m.ToolCalls) > 0 {
		calls, _ = json.Marshal(m.ToolCalls)
	}
	var callID *string
	if m.ToolCallID != "" {
		callID = &m.ToolCallID
	}
	// 先锁会话行再取 seq = 当前最大值 + 1：同一会话的追加（运行与审批可能交错）完全串行化；
	// UNIQUE(session_id, seq) 只是兜底。
	return store.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		if _, err := s.db(ctx).Exec(ctx, `UPDATE agent_sessions SET updated_at = now() WHERE id = $1`, sessionID); err != nil {
			return fmt.Errorf("agent: lock session: %w", err)
		}
		if err := s.db(ctx).QueryRow(ctx,
			`INSERT INTO agent_messages (session_id, seq, role, content, tool_calls, tool_call_id, compacted)
			 SELECT $1, COALESCE(MAX(seq), 0) + 1, $2, $3, $4, $5, $6 FROM agent_messages WHERE session_id = $1
			 RETURNING seq`,
			sessionID, m.Role, m.Content, calls, callID, m.Compacted).Scan(&m.Seq); err != nil {
			return fmt.Errorf("agent: append message: %w", err)
		}
		return nil
	})
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func (s *Store) SaveToolCall(ctx context.Context, r *kernel.ToolCallRecord) error {
	_, err := s.db(ctx).Exec(ctx,
		`INSERT INTO agent_tool_calls (id, session_id, run_id, tool, risk, args, args_hash, status, summary, required_perm,
		                               etag, before, after, http_status, result, duration_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		r.ID, r.SessionID, r.RunID, r.Tool, string(r.Risk), r.Args, r.ArgsHash, r.Status, r.Summary, r.RequiredPerm,
		nullStr(r.ETag), nullJSON(r.Before), nullJSON(r.After), nullInt(r.HTTPStatus), nullJSON(r.Result), nullInt(r.DurationMS))
	if err != nil {
		return fmt.Errorf("agent: insert tool call: %w", err)
	}
	return nil
}

func (s *Store) FinishToolCall(ctx context.Context, r *kernel.ToolCallRecord) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_tool_calls SET status = $2, summary = $3, http_status = $4, result = $5, duration_ms = $6 WHERE id = $1`,
		r.ID, r.Status, r.Summary, nullInt(r.HTTPStatus), nullJSON(r.Result), nullInt(r.DurationMS))
	return err
}

func (s *Store) SaveProposal(ctx context.Context, r *kernel.ToolCallRecord, p *kernel.ProposalRecord) error {
	return store.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		if err := s.SaveToolCall(ctx, r); err != nil {
			return err
		}
		err := s.db(ctx).QueryRow(ctx,
			`INSERT INTO agent_proposals (tool_call_id, session_id, job_id, playbook, tool, target_type, target_id, summary,
			                              rationale, evidence, confidence, required_perm)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
			p.ToolCallID, p.SessionID, p.JobID, p.Playbook, p.Tool, p.TargetType, p.TargetID, p.Summary,
			p.Rationale, nullJSON(p.Evidence), p.Confidence, p.RequiredPerm).Scan(&p.ID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return kernel.ErrDuplicateProposal
		}
		return err
	})
}

func (s *Store) AddUsage(ctx context.Context, sessionID int64, u kernel.Usage, turns int) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_sessions SET tokens_in = tokens_in + $2, tokens_out = tokens_out + $3, turns = turns + $4, updated_at = now() WHERE id = $1`,
		sessionID, u.In, u.Out, turns)
	return err
}

func (s *Store) CancelRequested(ctx context.Context, sessionID int64) (bool, error) {
	var c bool
	err := s.db(ctx).QueryRow(ctx, `SELECT cancel_requested FROM agent_sessions WHERE id = $1`, sessionID).Scan(&c)
	return c, err
}

// ---------- 消息与工具调用的读取 ----------

// Messages 返回会话的全部消息（按 seq）。
func (s *Store) Messages(ctx context.Context, sessionID int64) ([]kernel.Message, error) {
	rows, err := s.db(ctx).Query(ctx,
		`SELECT seq, role, content, tool_calls, COALESCE(tool_call_id, ''), compacted FROM agent_messages WHERE session_id = $1 ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []kernel.Message{}
	for rows.Next() {
		var m kernel.Message
		var calls []byte
		if err := rows.Scan(&m.Seq, &m.Role, &m.Content, &calls, &m.ToolCallID, &m.Compacted); err != nil {
			return nil, err
		}
		if len(calls) > 0 {
			_ = json.Unmarshal(calls, &m.ToolCalls)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ToolCallView 是 HTTP 层展示用的工具调用（含审批信息与提案）。
type ToolCallView struct {
	kernel.ToolCallRecord
	DecidedBy     *int64          `json:"decided_by"`
	DecidedByName string          `json:"decided_by_name"`
	DecidedAt     *time.Time      `json:"decided_at"`
	DecisionNote  string          `json:"decision_note"`
	CreatedAt     time.Time       `json:"created_at"`
	ProposalID    *int64          `json:"proposal_id"`
	Rationale     string          `json:"rationale"`
	Evidence      json.RawMessage `json:"evidence"`
	Confidence    *float64        `json:"confidence"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
}

const toolCallCols = `c.id, c.session_id, c.run_id, c.tool, c.risk, c.args, c.args_hash, c.status, c.summary, c.required_perm,
	COALESCE(c.etag, ''), c.before, c.after, COALESCE(c.http_status, 0), c.result, COALESCE(c.duration_ms, 0),
	c.decided_by, COALESCE(u.name, ''), c.decided_at, COALESCE(c.decision_note, ''), c.created_at,
	p.id, COALESCE(p.rationale, ''), p.evidence, p.confidence::float8, COALESCE(p.target_type, ''), COALESCE(p.target_id, '')`

const toolCallFrom = ` FROM agent_tool_calls c LEFT JOIN admin_users u ON u.id = c.decided_by
	LEFT JOIN agent_proposals p ON p.tool_call_id = c.id `

func scanToolCall(row pgx.Row) (*ToolCallView, error) {
	var x ToolCallView
	var risk string
	err := row.Scan(&x.ID, &x.SessionID, &x.RunID, &x.Tool, &risk, &x.Args, &x.ArgsHash, &x.Status, &x.Summary, &x.RequiredPerm,
		&x.ETag, &x.Before, &x.After, &x.HTTPStatus, &x.Result, &x.DurationMS,
		&x.DecidedBy, &x.DecidedByName, &x.DecidedAt, &x.DecisionNote, &x.CreatedAt,
		&x.ProposalID, &x.Rationale, &x.Evidence, &x.Confidence, &x.TargetType, &x.TargetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	x.Risk = kernel.Risk(risk)
	return &x, err
}

// ToolCalls 返回会话的全部工具调用（按时间）。
func (s *Store) ToolCalls(ctx context.Context, sessionID int64) ([]ToolCallView, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+toolCallCols+toolCallFrom+` WHERE c.session_id = $1 ORDER BY c.created_at, c.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolCallView{}
	for rows.Next() {
		x, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// GetToolCall 读取一次工具调用。
func (s *Store) GetToolCall(ctx context.Context, id string) (*ToolCallView, error) {
	return scanToolCall(s.db(ctx).QueryRow(ctx, `SELECT `+toolCallCols+toolCallFrom+` WHERE c.id = $1`, id))
}

// PendingCalls 返回会话中等待审批的调用。
func (s *Store) PendingCalls(ctx context.Context, sessionID int64) ([]ToolCallView, error) {
	all, err := s.ToolCalls(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, c := range all {
		if c.Status == kernel.CallPendingApproval {
			out = append(out, c)
		}
	}
	return out, nil
}

// ClaimDecision 原子地把一条待审批调用从 pending_approval 转到 to（approved / rejected / superseded），
// 同时更新提案状态；已被处理过返回 ok=false（重复审批只生效一次）。
func (s *Store) ClaimDecision(ctx context.Context, id, to string, decidedBy int64, note string) (bool, error) {
	ok := false
	err := store.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		var by any
		if decidedBy > 0 {
			by = decidedBy
		}
		tag, err := s.db(ctx).Exec(ctx,
			`UPDATE agent_tool_calls SET status = $2, decided_by = $3, decided_at = now(), decision_note = NULLIF($4, '')
			 WHERE id = $1 AND status = 'pending_approval'`, id, to, by, note)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		ok = true
		_, err = s.db(ctx).Exec(ctx,
			`UPDATE agent_proposals SET status = $2, decided_by = $3, decided_at = now() WHERE tool_call_id = $1 AND status = 'pending'`,
			id, proposalStatus(to), by)
		return err
	})
	return ok, err
}

// FinishDecision 写入审批后执行的结果（executed / stale / failed）。
func (s *Store) FinishDecision(ctx context.Context, id, status string, httpStatus int, result json.RawMessage, durationMS int) error {
	return store.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		if _, err := s.db(ctx).Exec(ctx,
			`UPDATE agent_tool_calls SET status = $2, http_status = $3, result = $4, duration_ms = $5 WHERE id = $1`,
			id, status, nullInt(httpStatus), nullJSON(result), nullInt(durationMS)); err != nil {
			return err
		}
		_, err := s.db(ctx).Exec(ctx, `UPDATE agent_proposals SET status = $2 WHERE tool_call_id = $1`, id, proposalStatus(status))
		return err
	})
}

func proposalStatus(callStatus string) string {
	switch callStatus {
	case kernel.CallPendingApproval:
		return "pending"
	case kernel.CallApproved:
		return "approved"
	case kernel.CallRejected:
		return "rejected"
	case kernel.CallExecuted:
		return "executed"
	case kernel.CallStale:
		return "stale"
	case kernel.CallSuperseded:
		return "superseded"
	default:
		return "failed"
	}
}

// UpdateProposalArgs 写入“编辑后通过”的新参数（仍处于 pending_approval 时）。
func (s *Store) UpdateProposalArgs(ctx context.Context, id string, args json.RawMessage, hash, summary string, after json.RawMessage) error {
	return store.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		tag, err := s.db(ctx).Exec(ctx,
			`UPDATE agent_tool_calls SET args = $2, args_hash = $3, summary = $4, after = $5 WHERE id = $1 AND status = 'pending_approval'`,
			id, args, hash, summary, nullJSON(after))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = s.db(ctx).Exec(ctx, `UPDATE agent_proposals SET summary = $2 WHERE tool_call_id = $1`, id, summary)
		return err
	})
}

// ---------- 提案收件箱 / 行内建议 ----------

// Proposal 是收件箱与行内建议展示的提案（含工具调用的参数与前后快照）。
type Proposal struct {
	ID            int64           `json:"id"`
	ToolCallID    string          `json:"tool_call_id"`
	SessionID     int64           `json:"session_id"`
	SessionTitle  string          `json:"session_title"`
	SessionMode   string          `json:"session_mode"`
	JobID         *int64          `json:"job_id"`
	Playbook      string          `json:"playbook"`
	Tool          string          `json:"tool"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Summary       string          `json:"summary"`
	Rationale     string          `json:"rationale"`
	Evidence      json.RawMessage `json:"evidence"`
	Confidence    *float64        `json:"confidence"`
	RequiredPerm  string          `json:"required_perm"`
	Status        string          `json:"status"`
	Args          json.RawMessage `json:"args"`
	Before        json.RawMessage `json:"before"`
	After         json.RawMessage `json:"after"`
	Result        json.RawMessage `json:"result"`
	DecidedBy     *int64          `json:"decided_by"`
	DecidedByName string          `json:"decided_by_name"`
	DecidedAt     *time.Time      `json:"decided_at"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ProposalFilter 是提案查询条件；Perms 非 nil 时只返回 required_perm 在其中的提案（“我能处理的”）。
type ProposalFilter struct {
	Status     string
	TargetType string
	TargetIDs  []string
	Playbook   string
	SessionID  int64
	Perms      []string
	BeforeID   int64
	Limit      int
}

// ListProposals 按 id 倒序返回提案。
func (s *Store) ListProposals(ctx context.Context, f ProposalFilter) ([]Proposal, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var ids any
	if len(f.TargetIDs) > 0 {
		ids = f.TargetIDs
	}
	var perms any
	if f.Perms != nil {
		perms = f.Perms
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT p.id, p.tool_call_id, p.session_id, s.title, s.mode, p.job_id, p.playbook, p.tool, p.target_type, p.target_id,
		        p.summary, p.rationale, p.evidence, p.confidence::float8, p.required_perm, p.status,
		        c.args, c.before, c.after, c.result, p.decided_by, COALESCE(u.name, ''), p.decided_at, p.created_at
		 FROM agent_proposals p
		 JOIN agent_tool_calls c ON c.id = p.tool_call_id
		 JOIN agent_sessions s ON s.id = p.session_id
		 LEFT JOIN admin_users u ON u.id = p.decided_by
		 WHERE ($1 = '' OR p.status = $1) AND ($2 = '' OR p.target_type = $2)
		   AND ($3::text[] IS NULL OR p.target_id = ANY($3)) AND ($4 = '' OR p.playbook = $4)
		   AND ($5 = 0 OR p.session_id = $5) AND ($6::text[] IS NULL OR p.required_perm = ANY($6))
		   AND ($7 = 0 OR p.id < $7)
		 ORDER BY p.id DESC LIMIT $8`,
		f.Status, f.TargetType, ids, f.Playbook, f.SessionID, perms, f.BeforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("agent: list proposals: %w", err)
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		var p Proposal
		if err := rows.Scan(&p.ID, &p.ToolCallID, &p.SessionID, &p.SessionTitle, &p.SessionMode, &p.JobID, &p.Playbook, &p.Tool,
			&p.TargetType, &p.TargetID, &p.Summary, &p.Rationale, &p.Evidence, &p.Confidence, &p.RequiredPerm, &p.Status,
			&p.Args, &p.Before, &p.After, &p.Result, &p.DecidedBy, &p.DecidedByName, &p.DecidedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountPending 返回待处理提案数；perms 为 nil 表示全部（super_admin）。
func (s *Store) CountPending(ctx context.Context, perms []string) (int, error) {
	var p any
	if perms != nil {
		p = perms
	}
	var n int
	err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM agent_proposals WHERE status = 'pending' AND ($1::text[] IS NULL OR required_perm = ANY($1))`, p).Scan(&n)
	return n, err
}
