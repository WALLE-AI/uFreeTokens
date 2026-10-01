package datasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Scheduler 驱动所有来源的定时执行。
type Scheduler struct {
	pool     *pgxpool.Pool
	registry Registry
	env      *Env
	logger   *slog.Logger
	metrics  *Metrics
	// RunTimeout 是单个来源单次执行的超时；0 = DefaultRunTimeout。
	RunTimeout time.Duration
	// MaxPerTick 是一次 Tick 最多执行的来源数（其余留给下一轮）；0 = 20。
	MaxPerTick int
	now        func() time.Time
}

func NewScheduler(pool *pgxpool.Pool, registry Registry, env *Env, logger *slog.Logger, metrics *Metrics) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{pool: pool, registry: registry, env: env, logger: logger, metrics: metrics, now: time.Now}
}

// Metrics 是采集模块的 Prometheus 指标（技术方案 §5）。
type Metrics struct {
	RunsTotal   *prometheus.CounterVec
	LastSuccess *prometheus.GaugeVec
	ItemsChange *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "uft_datasync_runs_total", Help: "外部数据采集执行次数",
		}, []string{"source", "status"}),
		LastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "uft_datasync_last_success_timestamp_seconds", Help: "来源最近一次成功（含内容未变）的时间",
		}, []string{"source"}),
		ItemsChange: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "uft_datasync_items_changed_total", Help: "采集到的有变化的条目数",
		}, []string{"source"}),
	}
	if reg != nil {
		reg.MustRegister(m.RunsTotal, m.LastSuccess, m.ItemsChange)
	}
	return m
}

const sourceCols = `ps.id, ps.provider_id, COALESCE(p.code, ''), ps.domain, ps.name, ps.level, ps.kind, ps.fetcher, COALESCE(ps.url, ''),
	ps.schedule, ps.config, COALESCE(ps.license, ''), COALESCE(ps.attribution, ''), ps.public_display, ps.auto_publish,
	ps.last_content_hash, COALESCE(ps.http_etag, ''), COALESCE(ps.http_last_modified, '')`

func scanSource(row pgx.Row) (Source, error) {
	var s Source
	var cfg []byte
	if err := row.Scan(&s.ID, &s.ProviderID, &s.ProviderCode, &s.Domain, &s.Name, &s.Level, &s.Kind, &s.Fetcher, &s.URL,
		&s.Schedule, &cfg, &s.License, &s.Attribution, &s.PublicDisplay, &s.AutoPublish,
		&s.LastContentHash, &s.HTTPETag, &s.HTTPLastModified); err != nil {
		return s, err
	}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &s.Config); err != nil {
			return s, fmt.Errorf("datasync: source %d config: %w", s.ID, err)
		}
	}
	if s.Config == nil {
		s.Config = map[string]any{}
	}
	return s, nil
}

// LoadSource 读一个来源（不论是否启用）。
func LoadSource(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (Source, error) {
	return scanSource(q.QueryRow(ctx, `SELECT `+sourceCols+` FROM price_sources ps LEFT JOIN providers p ON p.id = ps.provider_id WHERE ps.id = $1`, id))
}

// TickResult 汇总一次 Tick。
type TickResult struct {
	Ran     int `json:"ran"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"` // 被其它 worker 副本抢到锁
}

// Tick 执行所有到期的来源。单个来源失败不影响其它来源；只有查询到期来源本身失败才返回 error。
func (s *Scheduler) Tick(ctx context.Context) (TickResult, error) {
	limit := s.MaxPerTick
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx,
		`SELECT ps.id FROM price_sources ps
		 WHERE ps.enabled AND ps.next_run_at IS NOT NULL AND ps.next_run_at <= now()
		 ORDER BY ps.next_run_at LIMIT $1`, limit)
	if err != nil {
		return TickResult{}, fmt.Errorf("datasync: query due sources: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return TickResult{}, fmt.Errorf("datasync: scan due sources: %w", err)
	}
	var res TickResult
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		ran, err := s.RunSource(ctx, id)
		switch {
		case !ran:
			res.Skipped++
		case err != nil:
			res.Ran++
			res.Failed++
		default:
			res.Ran++
		}
	}
	return res, nil
}

// RunSource 抢锁并执行一个来源；ran=false 表示锁被别的副本持有（或来源已被停用 / 不再到期）。
// 返回的 error 是来源本身执行失败的原因（已经记进 data_source_runs）。
func (s *Scheduler) RunSource(ctx context.Context, id int64) (ran bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("datasync: acquire conn: %w", err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('datasync'), $1::int)`, id).Scan(&locked); err != nil {
		return false, fmt.Errorf("datasync: advisory lock: %w", err)
	}
	if !locked {
		return false, nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('datasync'), $1::int)`, id)
	}()

	// 抢到锁后重新确认仍然到期（别的副本可能刚跑完并推后了 next_run_at）。
	var due bool
	if err := conn.QueryRow(ctx,
		`SELECT enabled AND next_run_at IS NOT NULL AND next_run_at <= now() FROM price_sources WHERE id = $1`, id).Scan(&due); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("datasync: recheck source: %w", err)
	}
	if !due {
		return false, nil
	}
	return true, s.execute(ctx, id)
}

func (s *Scheduler) execute(ctx context.Context, id int64) error {
	src, err := LoadSource(ctx, s.pool, id)
	if err != nil {
		return fmt.Errorf("datasync: load source %d: %w", id, err)
	}
	var runID int64
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO data_source_runs (source_id, status) VALUES ($1, 'running') RETURNING id`, id).Scan(&runID); err != nil {
		return fmt.Errorf("datasync: record run start: %w", err)
	}

	timeout := s.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	start := s.now()
	res, runErr := s.runJob(runCtx, src)
	cancel()

	status := res.Status
	if status == "" {
		status = StatusOK
	}
	var errText *string
	if runErr != nil {
		status = StatusFailed
		if errors.Is(runErr, ErrRejected) {
			status = StatusRejected
		}
		msg := runErr.Error()
		errText = &msg
	}
	detail := res.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	detail["duration_ms"] = s.now().Sub(start).Milliseconds()

	// 记录执行结果与下一次调度时间。用不带取消的 ctx：worker 收到退出信号时也要把这次记完。
	wctx := context.WithoutCancel(ctx)
	if _, err := s.pool.Exec(wctx,
		`UPDATE data_source_runs SET finished_at = now(), status = $2, items_fetched = $3, items_changed = $4,
		   content_hash = $5, error = $6, detail = $7 WHERE id = $1`,
		runID, status, res.ItemsFetched, res.ItemsChanged, res.ContentHash, errText, detail); err != nil {
		s.logger.Warn("datasync: record run finish failed", "source_id", id, "error", err)
	}

	now := s.now()
	if runErr != nil {
		var failures int
		if err := s.pool.QueryRow(wctx,
			`UPDATE price_sources SET consecutive_failures = consecutive_failures + 1, last_error = $2, last_run_at = now()
			 WHERE id = $1 RETURNING consecutive_failures`, id, runErr.Error()).Scan(&failures); err != nil {
			s.logger.Warn("datasync: record failure failed", "source_id", id, "error", err)
		}
		next := now.Add(Backoff(failures))
		if sched, ok, _ := NextRun(src.Schedule, now); ok && sched.Before(next) {
			next = sched
		}
		s.setNextRun(wctx, id, next)
		s.logger.Error("datasync: source run failed", "source_id", id, "source", src.Name, "status", status, "error", runErr)
		if failures >= AlertAfterFailures {
			s.logger.Error("datasync: source failing repeatedly", "source_id", id, "source", src.Name,
				"consecutive_failures", failures, "alert", true)
		}
	} else {
		if _, err := s.pool.Exec(wctx,
			`UPDATE price_sources SET consecutive_failures = 0, last_error = NULL, last_run_at = now(), last_success_at = now(),
			   last_content_hash = COALESCE($2, last_content_hash),
			   http_etag = COALESCE(NULLIF($3, ''), http_etag), http_last_modified = COALESCE(NULLIF($4, ''), http_last_modified)
			 WHERE id = $1`, id, res.ContentHash, res.HTTPETag, res.HTTPLastModified); err != nil {
			s.logger.Warn("datasync: record success failed", "source_id", id, "error", err)
		}
		if next, ok, err := NextRun(src.Schedule, now); err != nil || !ok {
			s.setNextRun(wctx, id, time.Time{}) // 无调度或调度写错：只能手工触发
		} else {
			s.setNextRun(wctx, id, next)
		}
		s.logger.Info("datasync: source run finished", "source_id", id, "source", src.Name, "status", status,
			"fetched", res.ItemsFetched, "changed", res.ItemsChanged)
	}

	if s.metrics != nil {
		label := fmt.Sprintf("%d:%s", src.ID, src.Fetcher)
		s.metrics.RunsTotal.WithLabelValues(label, status).Inc()
		if runErr == nil {
			s.metrics.LastSuccess.WithLabelValues(label).Set(float64(now.Unix()))
			s.metrics.ItemsChange.WithLabelValues(label).Add(float64(res.ItemsChanged))
		}
	}
	return runErr
}

func (s *Scheduler) runJob(ctx context.Context, src Source) (res Result, err error) {
	job, ok := s.registry[src.Fetcher]
	if !ok {
		return Result{}, fmt.Errorf("datasync: no job registered for fetcher %q", src.Fetcher)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("datasync: job %s panicked: %v", src.Fetcher, r)
		}
	}()
	return job.Run(ctx, s.env, src)
}

func (s *Scheduler) setNextRun(ctx context.Context, id int64, next time.Time) {
	var v any
	if !next.IsZero() {
		v = next
	}
	if _, err := s.pool.Exec(ctx, `UPDATE price_sources SET next_run_at = $2 WHERE id = $1`, id, v); err != nil {
		s.logger.Warn("datasync: set next_run_at failed", "source_id", id, "error", err)
	}
}

// PurgeRuns 删除 retention 之前的执行记录。
func PurgeRuns(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM data_source_runs WHERE started_at < $1`, time.Now().Add(-retention))
	if err != nil {
		return 0, fmt.Errorf("datasync: purge runs: %w", err)
	}
	return tag.RowsAffected(), nil
}
