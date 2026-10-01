package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// 基准测试的运营录入（docs/基准测试与排行榜数据服务技术方案.md §3.5，阶段 2）：
// 基准定义的增改、评测批次（run）连同结果的录入/批量导入、发布与删除。对外只读接口
// 在 internal/benchmarks；这里不计算冠军、不过滤可见性。

var (
	ErrBenchmarkNotFound    = errors.New("admin: benchmark not found")
	ErrBenchmarkRunNotFound = errors.New("admin: benchmark run not found")
	// ErrBenchmarkRunPublished：发布过的 run（包括已退居历史的）不能删除，只有从未发布的草稿能删。
	ErrBenchmarkRunPublished = errors.New("admin: benchmark run has been published and cannot be deleted")
)

var (
	benchmarkCategories = []string{"general", "coding", "agents", "reasoning", "chinese", "search", "media", "artifacts", "embedding"}
	benchmarkStatuses   = []string{"draft", "published", "archived"}
	benchmarkOrigins    = []string{"manual", "import", "self_eval"}
	benchmarkCurrencies = []string{"USD", "CNY"}
	benchmarkSlugRe     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// maxBenchmarkResults 是单次 run 最多录入的结果行数（防止一次导入把请求体撑爆）。
const maxBenchmarkResults = 1000

type Benchmark struct {
	ID             int64   `json:"id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	Category       string  `json:"category"`
	Description    string  `json:"description"`
	MetricName     string  `json:"metric_name"`
	MetricUnit     string  `json:"metric_unit"`
	HigherIsBetter bool    `json:"higher_is_better"`
	SourceName     *string `json:"source_name"`
	SourceURL      *string `json:"source_url"`
	Status         string  `json:"status"`
	SortOrder      int     `json:"sort_order"`
	// 外部数据采集导入的基准（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §4.2）：
	// 来源、外部键、模型名映射命名空间；ScoreKey 非空时发布会把分数投影进 scores。
	DataSourceID   *int64    `json:"data_source_id"`
	DataSourceName *string   `json:"data_source_name"`
	ExternalKey    *string   `json:"external_key"`
	AliasNamespace *string   `json:"alias_namespace"`
	ScoreKey       *string   `json:"score_key"`
	PublicDisplay  bool      `json:"public_display"` // 来源许可是否允许对外展示（手工录入的基准恒为 true）
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// BenchmarkSummary 是列表行：基准定义 + run 统计。
type BenchmarkSummary struct {
	Benchmark
	RunCount             int        `json:"run_count"`
	PublishedRunID       *int64     `json:"published_run_id"`
	PublishedRunAt       *time.Time `json:"published_run_at"`
	PublishedResultCount int        `json:"published_result_count"`
}

type BenchmarkRun struct {
	ID           int64      `json:"id"`
	BenchmarkID  int64      `json:"benchmark_id"`
	Origin       string     `json:"origin"`
	RunAt        time.Time  `json:"run_at"`
	Notes        string     `json:"notes"`
	CostCurrency string     `json:"cost_currency"`
	Published    bool       `json:"published"`
	PublishedAt  *time.Time `json:"published_at"`
	CreatedBy    *int64     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ResultCount  int        `json:"result_count"`
}

type BenchmarkResult struct {
	ModelLabel       string          `json:"model_label"`
	VirtualModelID   *int64          `json:"virtual_model_id"`
	VirtualModel     *string         `json:"virtual_model"` // 关联模型的当前名称（只读）
	Score            float64         `json:"score"`
	CostPerTaskMicro *int64          `json:"cost_per_task_micro"`
	AvgDurationMs    *int            `json:"avg_duration_ms"`
	ErrorRate        *float64        `json:"error_rate"`
	SampleCount      *int            `json:"sample_count"`
	Extra            json.RawMessage `json:"extra"`
}

type BenchmarkDetail struct {
	Benchmark
	Runs []BenchmarkRun `json:"runs"`
}

type BenchmarkRunDetail struct {
	BenchmarkRun
	Results []BenchmarkResult `json:"results"`
}

// ---------- 基准定义 ----------

type CreateBenchmarkInput struct {
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	Category       string  `json:"category"`
	Description    string  `json:"description"`
	MetricName     string  `json:"metric_name"`
	MetricUnit     string  `json:"metric_unit"`      // 空 = percent
	HigherIsBetter *bool   `json:"higher_is_better"` // 空 = true
	SourceName     *string `json:"source_name"`
	SourceURL      *string `json:"source_url"`
	Status         string  `json:"status"` // 空 = draft
	SortOrder      int     `json:"sort_order"`
	ScoreKey       *string `json:"score_key"`
	// 以下三项只由外部数据采集（internal/benchsync）设置，不对运营接口开放。
	DataSourceID   *int64  `json:"-"`
	ExternalKey    *string `json:"-"`
	AliasNamespace *string `json:"-"`
}

func validSourceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return invalid("source_url must be an absolute http(s) URL without credentials")
	}
	return nil
}

// trimmedOrNil 把空白字符串归一成 NULL（来源名/链接是可选的）。
func trimmedOrNil(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

func (s *Service) CreateBenchmark(ctx context.Context, in CreateBenchmarkInput) (*Benchmark, error) {
	in.Slug, in.Name, in.MetricName = strings.TrimSpace(in.Slug), strings.TrimSpace(in.Name), strings.TrimSpace(in.MetricName)
	if !benchmarkSlugRe.MatchString(in.Slug) {
		return nil, invalid("slug must be lowercase letters/digits separated by single hyphens, e.g. gpqa-diamond")
	}
	if in.Name == "" || in.MetricName == "" {
		return nil, invalid("name and metric_name are required")
	}
	if err := oneOf("category", in.Category, benchmarkCategories...); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = "draft"
	}
	if err := oneOf("status", in.Status, benchmarkStatuses...); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.MetricUnit) == "" {
		in.MetricUnit = "percent"
	}
	higher := true
	if in.HigherIsBetter != nil {
		higher = *in.HigherIsBetter
	}
	sourceName, sourceURL := trimmedOrNil(in.SourceName), trimmedOrNil(in.SourceURL)
	if sourceURL != nil {
		if err := validSourceURL(*sourceURL); err != nil {
			return nil, err
		}
	}
	scoreKey, err := validScoreKey(in.ScoreKey)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO benchmarks (slug, name, category, description, metric_name, metric_unit, higher_is_better,
			source_name, source_url, status, sort_order, score_key, source_id, external_key, alias_namespace)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
		in.Slug, in.Name, in.Category, strings.TrimSpace(in.Description), in.MetricName, strings.TrimSpace(in.MetricUnit), higher,
		sourceName, sourceURL, in.Status, in.SortOrder, scoreKey, in.DataSourceID, trimmedOrNil(in.ExternalKey), trimmedOrNil(in.AliasNamespace),
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("admin: insert benchmark: %w", err)
	}
	return s.GetBenchmark(ctx, id)
}

type UpdateBenchmarkInput struct {
	Name           *string `json:"name"`
	Category       *string `json:"category"`
	Description    *string `json:"description"`
	MetricName     *string `json:"metric_name"`
	MetricUnit     *string `json:"metric_unit"`
	HigherIsBetter *bool   `json:"higher_is_better"`
	SourceName     *string `json:"source_name"` // 空字符串清除
	SourceURL      *string `json:"source_url"`  // 空字符串清除
	Status         *string `json:"status"`
	SortOrder      *int    `json:"sort_order"`
	ScoreKey       *string `json:"score_key"` // 空字符串清除
}

// validScoreKey 校验 scores 投影键：必须是 ScoreKeys 白名单里的顶层键。空 = 不投影。
func validScoreKey(p *string) (*string, error) {
	v := trimmedOrNil(p)
	if v != nil && !slices.Contains(ScoreKeys, *v) {
		return nil, invalid("score_key must be one of %s", strings.Join(ScoreKeys, ", "))
	}
	return v, nil
}

func (s *Service) UpdateBenchmark(ctx context.Context, id int64, in UpdateBenchmarkInput) (*Change, error) {
	var sets []setClause
	for _, f := range []struct {
		name string
		v    *string
	}{{"name", in.Name}, {"metric_name", in.MetricName}, {"metric_unit", in.MetricUnit}} {
		if err := nonEmpty(f.name, f.v); err != nil {
			return nil, err
		}
		if f.v != nil {
			sets = append(sets, setClause{f.name, strings.TrimSpace(*f.v)})
		}
	}
	if in.Category != nil {
		if err := oneOf("category", *in.Category, benchmarkCategories...); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"category", *in.Category})
	}
	if in.Description != nil {
		sets = append(sets, setClause{"description", strings.TrimSpace(*in.Description)})
	}
	if in.HigherIsBetter != nil {
		sets = append(sets, setClause{"higher_is_better", *in.HigherIsBetter})
	}
	if in.SourceName != nil {
		sets = append(sets, setClause{"source_name", trimmedOrNil(in.SourceName)})
	}
	if in.SourceURL != nil {
		v := trimmedOrNil(in.SourceURL)
		if v != nil {
			if err := validSourceURL(*v); err != nil {
				return nil, err
			}
		}
		sets = append(sets, setClause{"source_url", v})
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, benchmarkStatuses...); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
	}
	if in.SortOrder != nil {
		sets = append(sets, setClause{"sort_order", *in.SortOrder})
	}
	if in.ScoreKey != nil {
		v, err := validScoreKey(in.ScoreKey)
		if err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"score_key", v})
	}
	return s.patchRow(ctx, "benchmarks", id, sets, ErrBenchmarkNotFound, nil)
}

const benchmarkCols = `b.id, b.slug, b.name, b.category, b.description, b.metric_name, b.metric_unit, b.higher_is_better,
	b.source_name, b.source_url, b.status, b.sort_order, b.source_id,
	(SELECT ds.name FROM price_sources ds WHERE ds.id = b.source_id), b.external_key, b.alias_namespace, b.score_key,
	COALESCE((SELECT ds.public_display FROM price_sources ds WHERE ds.id = b.source_id), true),
	b.version, b.created_at, b.updated_at`

func scanBenchmark(row pgx.Row, extra ...any) (*Benchmark, error) {
	b := &Benchmark{}
	dest := append([]any{&b.ID, &b.Slug, &b.Name, &b.Category, &b.Description, &b.MetricName, &b.MetricUnit, &b.HigherIsBetter,
		&b.SourceName, &b.SourceURL, &b.Status, &b.SortOrder, &b.DataSourceID, &b.DataSourceName, &b.ExternalKey, &b.AliasNamespace,
		&b.ScoreKey, &b.PublicDisplay, &b.Version, &b.CreatedAt, &b.UpdatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) GetBenchmark(ctx context.Context, id int64) (*Benchmark, error) {
	b, err := scanBenchmark(s.db(ctx).QueryRow(ctx, `SELECT `+benchmarkCols+` FROM benchmarks b WHERE b.id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return nil, ErrBenchmarkNotFound
		}
		return nil, fmt.Errorf("admin: get benchmark: %w", err)
	}
	return b, nil
}

// GetBenchmarkDetail 返回基准定义与它的全部 run（新的在前，不含结果明细）。
func (s *Service) GetBenchmarkDetail(ctx context.Context, id int64) (*BenchmarkDetail, error) {
	b, err := s.GetBenchmark(ctx, id)
	if err != nil {
		return nil, err
	}
	runs, err := s.listRuns(ctx, `WHERE r.benchmark_id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &BenchmarkDetail{Benchmark: *b, Runs: runs}, nil
}

type ListBenchmarksInput struct {
	Category, Status string
}

// ListBenchmarks 返回全部基准（含 draft/archived），按 sort_order、名称排序。基准数量是
// 运营手工维护的量级（几十个），不分页。
func (s *Service) ListBenchmarks(ctx context.Context, in ListBenchmarksInput) ([]BenchmarkSummary, error) {
	if in.Category != "" {
		if err := oneOf("category", in.Category, benchmarkCategories...); err != nil {
			return nil, err
		}
	}
	if in.Status != "" {
		if err := oneOf("status", in.Status, benchmarkStatuses...); err != nil {
			return nil, err
		}
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT `+benchmarkCols+`,
		   (SELECT count(*) FROM benchmark_runs r WHERE r.benchmark_id = b.id),
		   pr.id, pr.run_at,
		   COALESCE((SELECT count(*) FROM benchmark_results x WHERE x.run_id = pr.id), 0)
		 FROM benchmarks b
		 LEFT JOIN benchmark_runs pr ON pr.benchmark_id = b.id AND pr.published
		 WHERE ($1 = '' OR b.category = $1) AND ($2 = '' OR b.status = $2)
		 ORDER BY b.sort_order, b.name, b.id`, in.Category, in.Status)
	if err != nil {
		return nil, fmt.Errorf("admin: list benchmarks: %w", err)
	}
	defer rows.Close()
	out := []BenchmarkSummary{}
	for rows.Next() {
		var sum BenchmarkSummary
		b, err := scanBenchmark(rows, &sum.RunCount, &sum.PublishedRunID, &sum.PublishedRunAt, &sum.PublishedResultCount)
		if err != nil {
			return nil, fmt.Errorf("admin: scan benchmark: %w", err)
		}
		sum.Benchmark = *b
		out = append(out, sum)
	}
	return out, rows.Err()
}

// ---------- 评测批次 ----------

type BenchmarkResultInput struct {
	ModelLabel string `json:"model_label"`
	// VirtualModelID / VirtualModel 二选一（都可省略）：关联到已上架的虚拟模型，前端据此跳转
	// 模型详情。只给名称时按 virtual_models.name 精确匹配，匹配不到返回 400。
	VirtualModelID   *int64          `json:"virtual_model_id"`
	VirtualModel     *string         `json:"virtual_model"`
	Score            *float64        `json:"score"`
	CostPerTaskMicro *int64          `json:"cost_per_task_micro"`
	AvgDurationMs    *int            `json:"avg_duration_ms"`
	ErrorRate        *float64        `json:"error_rate"`
	SampleCount      *int            `json:"sample_count"`
	Extra            json.RawMessage `json:"extra"`
}

type CreateBenchmarkRunInput struct {
	Origin       string                 `json:"origin"` // manual / import（self_eval 由评测任务写入）
	RunAt        time.Time              `json:"run_at"`
	Notes        string                 `json:"notes"`
	CostCurrency string                 `json:"cost_currency"` // 空 = USD
	Results      []BenchmarkResultInput `json:"results"`
	// Publish 为 true 时创建后立即发布（同基准的旧 run 退居历史）。
	Publish bool `json:"publish"`
	// ContentHash 只由外部数据采集设置：导入内容的指纹，下次内容没变就不再新建 run。
	ContentHash []byte `json:"-"`
}

// CreateBenchmarkRun 新建一次评测批次并写入全部结果（一次导入要么全部成功、要么全部回滚）。
func (s *Service) CreateBenchmarkRun(ctx context.Context, benchmarkID int64, in CreateBenchmarkRunInput) (*BenchmarkRunDetail, error) {
	if in.Origin == "" {
		in.Origin = "manual"
	}
	if err := oneOf("origin", in.Origin, benchmarkOrigins...); err != nil {
		return nil, err
	}
	if in.CostCurrency == "" {
		in.CostCurrency = "USD"
	}
	if err := oneOf("cost_currency", in.CostCurrency, benchmarkCurrencies...); err != nil {
		return nil, err
	}
	if in.RunAt.IsZero() {
		return nil, invalid("run_at is required")
	}
	if len(in.Results) == 0 {
		return nil, invalid("results must not be empty")
	}
	if len(in.Results) > maxBenchmarkResults {
		return nil, invalid("at most %d results per run", maxBenchmarkResults)
	}

	var runID int64
	err := s.RunInTx(ctx, func(ctx context.Context) error {
		if _, err := s.GetBenchmark(ctx, benchmarkID); err != nil {
			return err
		}
		if err := s.db(ctx).QueryRow(ctx,
			`INSERT INTO benchmark_runs (benchmark_id, origin, run_at, notes, cost_currency, created_by, content_hash)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			benchmarkID, in.Origin, in.RunAt, strings.TrimSpace(in.Notes), in.CostCurrency, actorFrom(ctx), in.ContentHash,
		).Scan(&runID); err != nil {
			return fmt.Errorf("admin: insert benchmark run: %w", err)
		}
		seen := map[string]bool{}
		for i, res := range in.Results {
			if err := s.insertBenchmarkResult(ctx, runID, i, res, seen); err != nil {
				return err
			}
		}
		if in.Publish {
			return s.publishRun(ctx, runID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetBenchmarkRun(ctx, runID)
}

func (s *Service) insertBenchmarkResult(ctx context.Context, runID int64, i int, res BenchmarkResultInput, seen map[string]bool) error {
	label := strings.TrimSpace(res.ModelLabel)
	switch {
	case label == "":
		return invalid("results[%d].model_label is required", i)
	case seen[label]:
		return invalid("results[%d].model_label %q is duplicated", i, label)
	case res.Score == nil:
		return invalid("results[%d].score is required", i)
	case res.CostPerTaskMicro != nil && *res.CostPerTaskMicro < 0,
		res.AvgDurationMs != nil && *res.AvgDurationMs < 0,
		res.SampleCount != nil && *res.SampleCount < 0:
		return invalid("results[%d]: cost/duration/sample_count must be >= 0", i)
	case res.ErrorRate != nil && (*res.ErrorRate < 0 || *res.ErrorRate > 1):
		return invalid("results[%d].error_rate must be between 0 and 1", i)
	}
	seen[label] = true
	var extra any
	if len(res.Extra) > 0 && string(res.Extra) != "null" {
		var obj map[string]any
		if err := json.Unmarshal(res.Extra, &obj); err != nil || obj == nil {
			return invalid("results[%d].extra must be a JSON object", i)
		}
		extra = obj
	}
	vmID := res.VirtualModelID
	if vmID == nil && res.VirtualModel != nil && strings.TrimSpace(*res.VirtualModel) != "" {
		vm, err := s.GetVirtualModelByName(ctx, strings.TrimSpace(*res.VirtualModel))
		if err != nil {
			if errors.Is(err, ErrVirtualModelNotFound) {
				return invalid("results[%d].virtual_model %q does not exist", i, *res.VirtualModel)
			}
			return err
		}
		vmID = &vm.ID
	}
	if _, err := s.db(ctx).Exec(ctx,
		`INSERT INTO benchmark_results (run_id, model_label, virtual_model_id, score, cost_per_task_micro,
			avg_duration_ms, error_rate, sample_count, extra)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		runID, label, vmID, *res.Score, res.CostPerTaskMicro, res.AvgDurationMs, res.ErrorRate, res.SampleCount, extra,
	); err != nil {
		return fmt.Errorf("admin: insert benchmark result %q: %w", label, err)
	}
	return nil
}

const runCols = `r.id, r.benchmark_id, r.origin, r.run_at, r.notes, r.cost_currency, r.published, r.published_at,
	r.created_by, r.created_at, (SELECT count(*) FROM benchmark_results x WHERE x.run_id = r.id)`

func (s *Service) listRuns(ctx context.Context, where string, args ...any) ([]BenchmarkRun, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+runCols+` FROM benchmark_runs r `+where+` ORDER BY r.run_at DESC, r.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: list benchmark runs: %w", err)
	}
	defer rows.Close()
	out := []BenchmarkRun{}
	for rows.Next() {
		var r BenchmarkRun
		if err := rows.Scan(&r.ID, &r.BenchmarkID, &r.Origin, &r.RunAt, &r.Notes, &r.CostCurrency, &r.Published, &r.PublishedAt,
			&r.CreatedBy, &r.CreatedAt, &r.ResultCount); err != nil {
			return nil, fmt.Errorf("admin: scan benchmark run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetBenchmarkRun 返回 run 与全部结果（按分数从好到差）。
func (s *Service) GetBenchmarkRun(ctx context.Context, runID int64) (*BenchmarkRunDetail, error) {
	runs, err := s.listRuns(ctx, `WHERE r.id = $1`, runID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrBenchmarkRunNotFound
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT x.model_label, x.virtual_model_id, vm.name, x.score::float8, x.cost_per_task_micro, x.avg_duration_ms,
			x.error_rate::float8, x.sample_count, x.extra
		 FROM benchmark_results x
		 JOIN benchmark_runs r ON r.id = x.run_id
		 JOIN benchmarks b ON b.id = r.benchmark_id
		 LEFT JOIN virtual_models vm ON vm.id = x.virtual_model_id
		 WHERE x.run_id = $1
		 ORDER BY CASE WHEN b.higher_is_better THEN -x.score ELSE x.score END, x.model_label`, runID)
	if err != nil {
		return nil, fmt.Errorf("admin: list benchmark results: %w", err)
	}
	defer rows.Close()
	d := &BenchmarkRunDetail{BenchmarkRun: runs[0], Results: []BenchmarkResult{}}
	for rows.Next() {
		var r BenchmarkResult
		var extra []byte
		if err := rows.Scan(&r.ModelLabel, &r.VirtualModelID, &r.VirtualModel, &r.Score, &r.CostPerTaskMicro, &r.AvgDurationMs,
			&r.ErrorRate, &r.SampleCount, &extra); err != nil {
			return nil, fmt.Errorf("admin: scan benchmark result: %w", err)
		}
		if len(extra) > 0 {
			r.Extra = extra
		}
		d.Results = append(d.Results, r)
	}
	return d, rows.Err()
}

// PublishBenchmarkRun 发布一次 run：同一基准此前发布的 run 自动取消发布（退居历史，数据保留）。
// 返回发布前后的状态供审计。
func (s *Service) PublishBenchmarkRun(ctx context.Context, runID int64) (before *BenchmarkRun, err error) {
	runs, err := s.listRuns(ctx, `WHERE r.benchmark_id = (SELECT benchmark_id FROM benchmark_runs WHERE id = $1) AND r.published`, runID)
	if err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		before = &runs[0]
	}
	return before, s.publishRun(ctx, runID)
}

func (s *Service) publishRun(ctx context.Context, runID int64) error {
	return s.RunInTx(ctx, func(ctx context.Context) error {
		var benchmarkID int64
		if err := s.db(ctx).QueryRow(ctx, `SELECT benchmark_id FROM benchmark_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&benchmarkID); err != nil {
			if isNoRows(err) {
				return ErrBenchmarkRunNotFound
			}
			return fmt.Errorf("admin: lock benchmark run: %w", err)
		}
		// 先锁基准行，串行化同一基准的并发发布（部分唯一索引兜底）。
		if _, err := s.db(ctx).Exec(ctx, `SELECT 1 FROM benchmarks WHERE id = $1 FOR UPDATE`, benchmarkID); err != nil {
			return fmt.Errorf("admin: lock benchmark: %w", err)
		}
		if _, err := s.db(ctx).Exec(ctx,
			`UPDATE benchmark_runs SET published = false WHERE benchmark_id = $1 AND published AND id <> $2`, benchmarkID, runID); err != nil {
			return fmt.Errorf("admin: unpublish previous runs: %w", err)
		}
		if _, err := s.db(ctx).Exec(ctx,
			`UPDATE benchmark_runs SET published = true, published_at = COALESCE(published_at, now()) WHERE id = $1`, runID); err != nil {
			return fmt.Errorf("admin: publish benchmark run: %w", err)
		}
		_, err := s.ApplyScoreProjection(ctx, runID)
		return err
	})
}

// ApplyScoreProjection 把一次 run 的分数写进关联模型的 virtual_model_metadata.scores[score_key]
// （同一模型多个推理档位取最好的分数）。基准没有 score_key、或来源许可不允许对外展示时什么都不做。
// 只合并这一个键，不动 scores 里的其它键。返回更新的模型数。
func (s *Service) ApplyScoreProjection(ctx context.Context, runID int64) (int64, error) {
	tag, err := s.db(ctx).Exec(ctx,
		`WITH b AS (
		   SELECT bm.score_key, bm.higher_is_better FROM benchmark_runs r JOIN benchmarks bm ON bm.id = r.benchmark_id
		   LEFT JOIN price_sources ds ON ds.id = bm.source_id
		   WHERE r.id = $1 AND bm.score_key IS NOT NULL AND (bm.source_id IS NULL OR ds.public_display)
		 ), best AS (
		   SELECT x.virtual_model_id AS vm,
		          CASE WHEN (SELECT higher_is_better FROM b) THEN max(x.score) ELSE min(x.score) END AS score
		   FROM benchmark_results x WHERE x.run_id = $1 AND x.virtual_model_id IS NOT NULL AND EXISTS (SELECT 1 FROM b)
		   GROUP BY x.virtual_model_id
		 )
		 INSERT INTO virtual_model_metadata AS m (virtual_model_id, scores)
		 SELECT best.vm, jsonb_build_object((SELECT score_key FROM b), round(best.score::numeric, 2)) FROM best
		 ON CONFLICT (virtual_model_id) DO UPDATE
		   SET scores = COALESCE(m.scores, '{}'::jsonb) || EXCLUDED.scores, updated_at = now()`, runID)
	if err != nil {
		return 0, fmt.Errorf("admin: apply score projection: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteBenchmarkRun 删除一次从未发布过的 run（连同结果）。发布过的 run 是对外展示过的
// 数据，只能被新 run 替代，不能删除。
func (s *Service) DeleteBenchmarkRun(ctx context.Context, runID int64) (*BenchmarkRunDetail, error) {
	d, err := s.GetBenchmarkRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if d.Published || d.PublishedAt != nil {
		return nil, ErrBenchmarkRunPublished
	}
	tag, err := s.db(ctx).Exec(ctx, `DELETE FROM benchmark_runs WHERE id = $1 AND published_at IS NULL`, runID)
	if err != nil {
		return nil, fmt.Errorf("admin: delete benchmark run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrBenchmarkRunPublished
	}
	return d, nil
}
