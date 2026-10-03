// Package jobs 是运营智能体的后台作业（设计 §15.2–15.3，实施方案 M3-B02 / M3-B05 / M4-B02 / M4-B06）：
// cmd/worker 每分钟 Tick 一次，按调度或事件（待处理查询）启动批处理运行；身份是只读服务主体 agent-bot，
// 写操作只进提案收件箱，审批时以审批人身份执行。
//
// 护栏：作业级每日 Token 预算、全局月度上限、每次最多处理 max_items_per_run 个对象、失败指数退避、
// PG advisory lock 防多实例重复运行、按剧本滚动拒绝率熔断（>40% 自动停用）。
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/playbooks"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

// BotEmail 是后台智能体服务主体的邮箱（迁移 00035）。
const BotEmail = "agent-bot@admin.local"

// 熔断参数（实施方案 M4-B02）。
const (
	breakerWindow   = 50
	breakerMinCount = 10
	breakerRatio    = 0.4
)

// Job 是一行 agent_jobs。
type Job struct {
	ID               int64           `json:"id"`
	Code             string          `json:"code"`
	Name             string          `json:"name"`
	Playbook         string          `json:"playbook"`
	Enabled          bool            `json:"enabled"`
	Schedule         string          `json:"schedule"`
	TriggerQuery     *string         `json:"trigger_query"`
	Cursor           json.RawMessage `json:"cursor"`
	Model            *string         `json:"model"`
	DailyTokenBudget int64           `json:"daily_token_budget"`
	MaxItemsPerRun   int             `json:"max_items_per_run"`
	NextRunAt        *time.Time      `json:"next_run_at"`
	RunRequested     bool            `json:"run_requested"`
	FailureCount     int             `json:"failure_count"`
	LastRunAt        *time.Time      `json:"last_run_at"`
	LastStatus       string          `json:"last_status"`
	LastError        string          `json:"last_error"`
	LastSessionID    *int64          `json:"last_session_id"`
	PausedReason     string          `json:"paused_reason"`
	UpdatedAt        time.Time       `json:"updated_at"`
	// 以下为统计（列表接口填充）。
	TokensToday    int64    `json:"tokens_today"`
	PendingCount   int      `json:"pending_count"`
	RejectedRatio  *float64 `json:"rejected_ratio"`
	DecidedInRatio int      `json:"decided_in_ratio"`
}

const jobCols = `id, code, name, playbook, enabled, schedule, trigger_query, cursor, model, daily_token_budget, max_items_per_run,
	next_run_at, run_requested, failure_count, last_run_at, last_status, last_error, last_session_id, paused_reason, updated_at`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Code, &j.Name, &j.Playbook, &j.Enabled, &j.Schedule, &j.TriggerQuery, &j.Cursor, &j.Model,
		&j.DailyTokenBudget, &j.MaxItemsPerRun, &j.NextRunAt, &j.RunRequested, &j.FailureCount, &j.LastRunAt, &j.LastStatus,
		&j.LastError, &j.LastSessionID, &j.PausedReason, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pgstore.ErrNotFound
	}
	return &j, err
}

// Store 是作业表的读写（cmd/admin 的作业管理接口与 worker 共用）。
type Store struct{ Pool *pgxpool.Pool }

// List 返回全部作业及今日 Token、待审提案数与拒绝率。
func (s *Store) List(ctx context.Context) ([]Job, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+jobCols+` FROM agent_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillStats(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []Job{}
	}
	return out, nil
}

// Get 读取一个作业（含统计）。
func (s *Store) Get(ctx context.Context, id int64) (*Job, error) {
	j, err := scanJob(s.Pool.QueryRow(ctx, `SELECT `+jobCols+` FROM agent_jobs WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return j, s.fillStats(ctx, j)
}

func (s *Store) fillStats(ctx context.Context, j *Job) error {
	var err error
	if j.TokensToday, err = s.tokensToday(ctx, j.ID); err != nil {
		return err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_proposals WHERE job_id = $1 AND status = 'pending'`, j.ID).Scan(&j.PendingCount); err != nil {
		return err
	}
	ratio, n, err := s.rejectedRatio(ctx, j.ID)
	if err != nil {
		return err
	}
	j.DecidedInRatio = n
	if n > 0 {
		j.RejectedRatio = &ratio
	}
	return nil
}

func (s *Store) tokensToday(ctx context.Context, jobID int64) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(sum(tokens_in + tokens_out), 0) FROM agent_sessions WHERE job_id = $1 AND created_at >= date_trunc('day', now())`, jobID).Scan(&n)
	return n, err
}

func (s *Store) tokensThisMonth(ctx context.Context) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(sum(tokens_in + tokens_out), 0) FROM agent_sessions WHERE job_id IS NOT NULL AND created_at >= date_trunc('month', now())`).Scan(&n)
	return n, err
}

// rejectedRatio 是最近 breakerWindow 条已处理提案的拒绝率。
func (s *Store) rejectedRatio(ctx context.Context, jobID int64) (float64, int, error) {
	var rejected, total int
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'rejected'), count(*) FROM (
		   SELECT status FROM agent_proposals WHERE job_id = $1 AND status IN ('approved','executed','rejected','stale','failed')
		   ORDER BY id DESC LIMIT $2) t`, jobID, breakerWindow).Scan(&rejected, &total)
	if err != nil || total == 0 {
		return 0, total, err
	}
	return float64(rejected) / float64(total), total, nil
}

// UpdateInput 是作业管理接口可修改的字段。
type UpdateInput struct {
	Enabled          *bool   `json:"enabled"`
	Schedule         *string `json:"schedule"`
	Model            *string `json:"model"`
	DailyTokenBudget *int64  `json:"daily_token_budget"`
	MaxItemsPerRun   *int    `json:"max_items_per_run"`
}

// Update 修改作业；重新启用时清除熔断标记与退避。
func (s *Store) Update(ctx context.Context, id int64, in UpdateInput) (*Job, error) {
	if in.Schedule != nil && *in.Schedule != "" {
		if _, _, err := datasync.NextRun(*in.Schedule, time.Now()); err != nil {
			return nil, fmt.Errorf("%w: invalid schedule: %v", agent.ErrInvalidInput, err)
		}
	}
	if in.DailyTokenBudget != nil && *in.DailyTokenBudget < 0 || in.MaxItemsPerRun != nil && (*in.MaxItemsPerRun < 1 || *in.MaxItemsPerRun > 200) {
		return nil, fmt.Errorf("%w: daily_token_budget must be >= 0 and max_items_per_run 1-200", agent.ErrInvalidInput)
	}
	var model any
	if in.Model != nil {
		if m := strings.TrimSpace(*in.Model); m != "" {
			model = m
		}
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE agent_jobs SET enabled = COALESCE($2, enabled), schedule = COALESCE($3, schedule),
		        model = CASE WHEN $4 THEN $5 ELSE model END,
		        daily_token_budget = COALESCE($6, daily_token_budget), max_items_per_run = COALESCE($7, max_items_per_run),
		        paused_reason = CASE WHEN $2 IS TRUE THEN '' ELSE paused_reason END,
		        failure_count = CASE WHEN $2 IS TRUE THEN 0 ELSE failure_count END,
		        next_run_at = CASE WHEN $2 IS TRUE OR $3 IS NOT NULL THEN NULL ELSE next_run_at END,
		        updated_at = now()
		 WHERE id = $1`, id, in.Enabled, in.Schedule, in.Model != nil, model, in.DailyTokenBudget, in.MaxItemsPerRun)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgstore.ErrNotFound
	}
	return s.Get(ctx, id)
}

// RequestRun 标记“立即运行”（worker 下一次 tick 执行，不受调度与触发条件限制）。
func (s *Store) RequestRun(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE agent_jobs SET run_requested = true, updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgstore.ErrNotFound
	}
	return nil
}

// ---------- 运行 ----------

// Runner 由 cmd/worker 驱动。
type Runner struct {
	Store  *Store
	Agent  *agent.Service
	Logger *slog.Logger
	// MonthlyTokenCap 是全部作业的月度 Token 上限；0 = 不限。
	MonthlyTokenCap int64
	// Now 便于测试；nil = time.Now。
	Now func() time.Time

	bot *adminauth.Principal
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Bot 加载服务主体 agent-bot 的身份（角色与权限来自库，保证它永远只有只读权限）。
func (r *Runner) Bot(ctx context.Context) (*adminauth.Principal, error) {
	if r.bot != nil {
		return r.bot, nil
	}
	p := &adminauth.Principal{}
	var perms []string
	err := r.Store.Pool.QueryRow(ctx,
		`SELECT u.id, u.name, COALESCE(array_agg(DISTINCT ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL), '{}'),
		        COALESCE(array_agg(DISTINCT perm) FILTER (WHERE perm IS NOT NULL), '{}')
		 FROM admin_users u
		 LEFT JOIN admin_user_roles ur ON ur.admin_user_id = u.id
		 LEFT JOIN admin_roles r ON r.code = ur.role_code
		 LEFT JOIN LATERAL unnest(r.permissions) AS perm ON true
		 WHERE u.email = $1 GROUP BY u.id, u.name`, BotEmail).Scan(&p.AdminID, &p.Name, &p.Roles, &perms)
	if err != nil {
		return nil, fmt.Errorf("agent jobs: load %s (migration 00035): %w", BotEmail, err)
	}
	for _, x := range perms {
		perm := adminauth.Permission(x)
		// 不变式：服务主体不允许拥有任何写权限，配置错误时拒绝运行。
		if perm == adminauth.PermAll || !strings.HasSuffix(x, ":read") && perm != adminauth.PermAgentUse {
			return nil, fmt.Errorf("agent jobs: %s must only have read permissions, found %q", BotEmail, x)
		}
		p.Permissions = append(p.Permissions, perm)
	}
	r.bot = p
	return p, nil
}

// Tick 检查到期 / 有新对象 / 被手动触发的作业并逐个运行。返回本轮运行概况（记入 job_runs.detail）。
func (r *Runner) Tick(ctx context.Context) (map[string]any, error) {
	detail := map[string]any{}
	if !r.Agent.Enabled() {
		detail["skipped"] = "agent disabled"
		return detail, nil
	}
	if n, err := r.Supersede(ctx); err != nil {
		return detail, err
	} else if n > 0 {
		detail["superseded"] = n
	}
	rows, err := r.Store.Pool.Query(ctx, `SELECT `+jobCols+` FROM agent_jobs WHERE (enabled AND paused_reason = '') OR run_requested ORDER BY id`)
	if err != nil {
		return detail, err
	}
	var due []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return detail, err
		}
		due = append(due, *j)
	}
	rows.Close()
	ran := map[string]string{}
	for _, j := range due {
		status, err := r.runJob(ctx, j)
		if err != nil {
			r.Logger.Error("agent job failed", "job", j.Code, "error", err)
			status = "error: " + err.Error()
		}
		if status != "" {
			ran[j.Code] = status
		}
	}
	detail["jobs"] = ran
	return detail, nil
}

func lockKey(code string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("agent_job:" + code))
	return int64(h.Sum64() >> 1)
}

// runJob 运行一个作业（如到期）。返回空字符串表示本轮不需要运行。
func (r *Runner) runJob(ctx context.Context, j Job) (string, error) {
	now := r.now()
	scheduled := j.Schedule != "" && (j.NextRunAt == nil || !now.Before(*j.NextRunAt))
	if !j.RunRequested && !scheduled && j.TriggerQuery == nil {
		return "", nil
	}
	conn, err := r.Store.Pool.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, lockKey(j.Code)).Scan(&locked); err != nil || !locked {
		return "", err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockKey(j.Code)) //nolint:errcheck

	pb, ok := playbooks.Get(j.Playbook)
	if !ok {
		return "", r.finish(ctx, j, "failed", fmt.Sprintf("unknown playbook %q", j.Playbook), nil, nil, false)
	}
	// 预算：作业每日上限 + 全局月度上限。
	if used, err := r.Store.tokensToday(ctx, j.ID); err != nil {
		return "", err
	} else if j.DailyTokenBudget > 0 && used >= j.DailyTokenBudget {
		return "budget_exhausted", r.finish(ctx, j, "budget_exhausted", "", nil, nil, true)
	}
	if r.MonthlyTokenCap > 0 {
		if used, err := r.Store.tokensThisMonth(ctx); err != nil {
			return "", err
		} else if used >= r.MonthlyTokenCap {
			return "monthly_cap", r.finish(ctx, j, "monthly_cap", "", nil, nil, true)
		}
	}

	limit := j.MaxItemsPerRun
	if limit <= 0 {
		limit = 20
	}
	cur := decodeCursor(j.Cursor)
	var items []Item
	next := cur
	if j.TriggerQuery != nil {
		trig, ok := Triggers[*j.TriggerQuery]
		if !ok {
			return "", r.finish(ctx, j, "failed", "unknown trigger "+*j.TriggerQuery, nil, nil, false)
		}
		if items, next, err = trig(ctx, r.Store.Pool, j.Playbook, cur, limit); err != nil {
			return "", r.finish(ctx, j, "failed", err.Error(), nil, nil, false)
		}
		// 事件作业：没有新对象就不启动运行（调度只是兜底检查频率）。
		if len(items) == 0 {
			if scheduled || j.RunRequested {
				return "no_items", r.finish(ctx, j, "no_items", "", &next, nil, true)
			}
			return "", nil
		}
	} else if !scheduled && !j.RunRequested {
		return "", nil
	}

	bot, err := r.Bot(ctx)
	if err != nil {
		return "", r.finish(ctx, j, "failed", err.Error(), nil, nil, false)
	}
	refJSON, _ := json.Marshal(items)
	model := r.Agent.Cfg.BatchModel
	if model == "" {
		model = r.Agent.Cfg.Model
	}
	if j.Model != nil && *j.Model != "" {
		model = *j.Model
	}
	jobID := j.ID
	sess, err := r.Agent.Store.CreateSession(ctx, pgstore.NewSession{
		AdminUserID: bot.AdminID, Title: fmt.Sprintf("⏱ %s %s", pb.Title, now.Format("01-02 15:04")), Playbook: j.Playbook,
		ContextRef: refJSON, Mode: kernel.ModeBatch, Model: model, JobID: &jobID,
	})
	if err != nil {
		return "", err
	}
	out, runErr := r.Agent.RunBatch(ctx, bot, sess, instruction(j, pb, items))
	status := out.Status
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
		status = kernel.StatusFailed
	}
	if err := r.finish(ctx, j, status, errMsg, &next, &sess.ID, runErr == nil); err != nil {
		return status, err
	}
	r.checkBreaker(ctx, j)
	return fmt.Sprintf("%s (%d items, %d proposals)", status, len(items), out.Proposals), nil
}

func instruction(j Job, pb *playbooks.Playbook, items []Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "后台作业「%s」：%s\n", j.Name, pb.Starter)
	if len(items) > 0 {
		fmt.Fprintf(&b, "本次只处理以下 %d 个对象（逐个处理，逐个给出提案或说明为何不提案）：\n", len(items))
		for _, it := range items {
			fmt.Fprintf(&b, "- %s（type=%s, id=%s）\n", it.Label, it.Type, it.ID)
		}
	}
	b.WriteString("处理完后输出运行报告：每个对象的结论与是否已提出提案。")
	return b.String()
}

// finish 写回运行结果与下次运行时间；失败时指数退避（最多 24 小时）。
func (r *Runner) finish(ctx context.Context, j Job, status, errMsg string, cur *Cursor, sessionID *int64, ok bool) error {
	now := r.now()
	failures := 0
	if !ok {
		failures = j.FailureCount + 1
	}
	var next *time.Time
	if !ok {
		backoff := time.Duration(1<<min(failures, 8)) * time.Minute
		if backoff > 24*time.Hour {
			backoff = 24 * time.Hour
		}
		t := now.Add(backoff)
		next = &t
	} else if j.Schedule != "" {
		if t, found, err := datasync.NextRun(j.Schedule, now); err == nil && found {
			next = &t
		}
	}
	var cursor any
	if cur != nil {
		cursor, _ = json.Marshal(cur)
	}
	_, err := r.Store.Pool.Exec(context.WithoutCancel(ctx),
		`UPDATE agent_jobs SET last_run_at = $2, last_status = $3, last_error = $4, failure_count = $5, next_run_at = $6,
		        cursor = COALESCE($7, cursor), last_session_id = COALESCE($8, last_session_id), run_requested = false, updated_at = now()
		 WHERE id = $1`, j.ID, now, status, errMsg, failures, next, cursor, sessionID)
	return err
}

// checkBreaker：最近 50 条已处理提案拒绝率 > 40%（至少 10 条）时自动停用作业并告警。
func (r *Runner) checkBreaker(ctx context.Context, j Job) {
	ratio, n, err := r.Store.rejectedRatio(ctx, j.ID)
	if err != nil {
		r.Logger.Warn("agent job breaker check failed", "job", j.Code, "error", err)
		return
	}
	observability.SetAgentRejectedRatio(j.Code, ratio)
	if n < breakerMinCount || ratio <= breakerRatio {
		return
	}
	if _, err := r.Store.Pool.Exec(ctx,
		`UPDATE agent_jobs SET enabled = false, paused_reason = 'circuit_breaker', updated_at = now() WHERE id = $1`, j.ID); err != nil {
		r.Logger.Error("agent job breaker trip failed", "job", j.Code, "error", err)
		return
	}
	r.Logger.Error("agent job circuit breaker tripped", "job", j.Code, "rejected_ratio", ratio, "decided", n, "alert", true)
}

// CheckBreakers 对全部作业检查熔断（审批后调用：拒绝发生在 cmd/admin，不必等下一次运行）。
func (r *Runner) CheckBreakers(ctx context.Context) error {
	list, err := r.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, j := range list {
		if j.Enabled {
			r.checkBreaker(ctx, j)
		}
	}
	return nil
}

// Supersede 把目标对象已被人工处理的待审提案标记为 superseded（实施方案 M3-B05）。
func (r *Runner) Supersede(ctx context.Context) (int64, error) {
	return SupersedeHandled(ctx, r.Store.Pool)
}

// SupersedeHandled 是 Supersede 的实现（列表查询时也可以惰性调用）。
func SupersedeHandled(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
WITH handled AS (
  SELECT p.id, p.tool_call_id FROM agent_proposals p
  WHERE p.status = 'pending' AND (
       (p.target_type = 'price_change_request' AND EXISTS (SELECT 1 FROM price_change_requests c WHERE c.id::text = p.target_id AND c.status NOT IN ('pending','blocked')))
    OR (p.target_type = 'pending_listing'      AND EXISTS (SELECT 1 FROM pending_model_listings l WHERE l.id::text = p.target_id AND l.status <> 'pending'))
    OR (p.target_type = 'upstream_offer' AND p.tool = 'set_offer_status' AND EXISTS (SELECT 1 FROM upstream_offers o WHERE o.id::text = p.target_id AND o.status <> 'new'))
    OR (p.target_type = 'model_alias'          AND EXISTS (SELECT 1 FROM model_aliases a WHERE a.namespace || ':' || a.external_label = p.target_id AND a.status NOT IN ('suggested','unmatched')))
    OR (p.target_type = 'benchmark_run'        AND EXISTS (SELECT 1 FROM benchmark_runs b WHERE b.id::text = p.target_id AND b.published))
  )
), up AS (
  UPDATE agent_proposals p SET status = 'superseded' FROM handled h WHERE p.id = h.id RETURNING p.tool_call_id
)
UPDATE agent_tool_calls c SET status = 'superseded', decision_note = '目标对象已被人工处理' FROM up WHERE c.id = up.tool_call_id AND c.status = 'pending_approval'`)
	if err != nil {
		return 0, fmt.Errorf("agent jobs: supersede: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeSessions 删除超过保留期的会话（实施方案 M4-B06，默认 180 天）；审计日志不受影响。
// 仍有待审提案的会话不删。
func PurgeSessions(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	if retention <= 0 {
		retention = 180 * 24 * time.Hour
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM agent_sessions s WHERE s.updated_at < now() - $1::interval
		   AND NOT EXISTS (SELECT 1 FROM agent_proposals p WHERE p.session_id = s.id AND p.status = 'pending')`,
		fmt.Sprintf("%d seconds", int64(retention.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("agent jobs: purge sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
