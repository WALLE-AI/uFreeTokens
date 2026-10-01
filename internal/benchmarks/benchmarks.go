// Package benchmarks 是基准测试的公开只读数据层（docs/基准测试与排行榜数据服务技术方案.md
// §3.4，阶段 2）：GET /v1/benchmarks[/{slug}] 只返回 status=published 的基准、每个基准
// 最新一次已发布的 run。录入/发布在 internal/admin/benchmarks.go。
//
// 三项冠军在这里由结果实时计算，不单独存：
//   - quality：分数最好（按 higher_is_better）；
//   - value（性价比）：分数不低于中位数的模型里，单题成本最低；
//   - speed：分数不低于中位数的模型里，平均耗时最短。
//
// 后两项先用"中位数质量线"过滤，避免又便宜又快但几乎答不对的模型拿到冠军。
//
// 外部数据采集导入的基准（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §4.3）：
// 来源许可不允许对外展示（price_sources.public_display=false）的基准一律不返回；
// 返回的基准带上来源许可证，前端据此展示署名。
package benchmarks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound：slug 不存在、基准未发布，都按不存在处理（不泄露草稿是否存在）。
var ErrNotFound = errors.New("benchmarks: not found")

// BenchmarkRun 是对外展示的那次评测批次。Origin 为 self_eval 时是平台实测，否则是外部来源数据。
type BenchmarkRun struct {
	ID           int64     `json:"id"`
	Origin       string    `json:"origin"`
	RunAt        time.Time `json:"run_at"`
	CostCurrency string    `json:"cost_currency"`
	Notes        string    `json:"notes"`
}

// BenchmarkChampion 是某一项冠军。Model 是关联的公开模型名（未上架或非公开时为 null）。
type BenchmarkChampion struct {
	ModelLabel       string  `json:"model_label"`
	Model            *string `json:"model"`
	Score            float64 `json:"score"`
	CostPerTaskMicro *int64  `json:"cost_per_task_micro"`
	AvgDurationMs    *int    `json:"avg_duration_ms"`
}

type BenchmarkChampions struct {
	Quality *BenchmarkChampion `json:"quality"`
	Value   *BenchmarkChampion `json:"value"`
	Speed   *BenchmarkChampion `json:"speed"`
}

// Benchmark 是 GET /v1/benchmarks 的一项。
type Benchmark struct {
	Slug           string             `json:"slug"`
	Name           string             `json:"name"`
	Category       string             `json:"category"`
	Description    string             `json:"description"`
	MetricName     string             `json:"metric_name"`
	MetricUnit     string             `json:"metric_unit"`
	HigherIsBetter bool               `json:"higher_is_better"`
	SourceName     *string            `json:"source_name"`
	SourceURL      *string            `json:"source_url"`
	License        *string            `json:"license"` // 外部来源的数据许可（如 CC-BY-4.0）；手工录入为 null
	Run            *BenchmarkRun      `json:"run"`
	ModelsCount    int                `json:"models_count"`
	Champions      BenchmarkChampions `json:"champions"`
}

// BenchmarkResult 是排行榜的一行。
type BenchmarkResult struct {
	Rank             int             `json:"rank"`
	ModelLabel       string          `json:"model_label"`
	Model            *string         `json:"model"`
	Score            float64         `json:"score"`
	CostPerTaskMicro *int64          `json:"cost_per_task_micro"`
	AvgDurationMs    *int            `json:"avg_duration_ms"`
	ErrorRate        *float64        `json:"error_rate"`
	SampleCount      *int            `json:"sample_count"`
	Extra            json.RawMessage `json:"extra"`
}

// BenchmarkDetail 是 GET /v1/benchmarks/{slug} 的响应。
type BenchmarkDetail struct {
	Benchmark
	Results []BenchmarkResult `json:"results"`
}

// Categories 是合法的分类过滤值（与 benchmarks.category 的 CHECK 约束一致）。
var Categories = []string{"general", "coding", "agents", "reasoning", "chinese", "search", "media", "artifacts", "embedding"}

// publicSource 是"来源许可允许对外展示"的过滤条件（手工录入的基准没有来源，总是可见）。
const publicSource = `(b.source_id IS NULL OR EXISTS (SELECT 1 FROM price_sources ds WHERE ds.id = b.source_id AND ds.public_display))`

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const summaryCols = `b.id, b.slug, b.name, b.category, b.description, b.metric_name, b.metric_unit, b.higher_is_better,
	b.source_name, b.source_url, (SELECT ds.license FROM price_sources ds WHERE ds.id = b.source_id),
	r.id, r.origin, r.run_at, r.cost_currency, r.notes`

func scanBenchmark(row pgx.Row) (int64, *Benchmark, error) {
	var id int64
	s := &Benchmark{}
	var runID *int64
	var origin, currency, notes *string
	var runAt *time.Time
	if err := row.Scan(&id, &s.Slug, &s.Name, &s.Category, &s.Description, &s.MetricName, &s.MetricUnit, &s.HigherIsBetter,
		&s.SourceName, &s.SourceURL, &s.License, &runID, &origin, &runAt, &currency, &notes); err != nil {
		return 0, nil, err
	}
	if runID != nil {
		s.Run = &BenchmarkRun{ID: *runID, Origin: *origin, RunAt: *runAt, CostCurrency: *currency, Notes: *notes}
	}
	return id, s, nil
}

// List 返回已发布的基准（category 为空不过滤），按 sort_order、名称排序。
func (st *Store) List(ctx context.Context, category string) ([]Benchmark, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT `+summaryCols+`
		 FROM benchmarks b
		 LEFT JOIN benchmark_runs r ON r.benchmark_id = b.id AND r.published
		 WHERE b.status = 'published' AND ($1 = '' OR b.category = $1) AND `+publicSource+`
		 ORDER BY b.sort_order, b.name, b.id`, category)
	if err != nil {
		return nil, fmt.Errorf("benchmarks: list: %w", err)
	}
	var out []Benchmark
	for rows.Next() {
		_, s, err := scanBenchmark(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("benchmarks: scan: %w", err)
		}
		out = append(out, *s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Run == nil {
			continue
		}
		results, err := st.results(ctx, out[i].Run.ID, out[i].HigherIsBetter)
		if err != nil {
			return nil, err
		}
		out[i].ModelsCount = len(results)
		out[i].Champions = champions(results, out[i].HigherIsBetter)
	}
	if out == nil {
		out = []Benchmark{}
	}
	return out, nil
}

// Get 返回一个已发布基准与它最新已发布 run 的完整排行榜。
func (st *Store) Get(ctx context.Context, slug string) (*BenchmarkDetail, error) {
	_, s, err := scanBenchmark(st.pool.QueryRow(ctx,
		`SELECT `+summaryCols+`
		 FROM benchmarks b
		 LEFT JOIN benchmark_runs r ON r.benchmark_id = b.id AND r.published
		 WHERE b.status = 'published' AND b.slug = $1 AND `+publicSource, slug))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("benchmarks: get: %w", err)
	}
	d := &BenchmarkDetail{Benchmark: *s, Results: []BenchmarkResult{}}
	if s.Run != nil {
		if d.Results, err = st.results(ctx, s.Run.ID, s.HigherIsBetter); err != nil {
			return nil, err
		}
		d.ModelsCount = len(d.Results)
		d.Champions = champions(d.Results, s.HigherIsBetter)
	}
	return d, nil
}

// results 读一次 run 的全部结果并排名（同分并列，按标签排序）。关联模型只有在公开目录
// 可见（未隐藏、对 free 等级可见）时才返回名称，避免通过基准页暴露非公开模型。
func (st *Store) results(ctx context.Context, runID int64, higherIsBetter bool) ([]BenchmarkResult, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT x.model_label,
			CASE WHEN vm.status IN ('active','deprecated') AND 'free' = ANY(vm.visible_tiers) THEN vm.name END,
			x.score::float8, x.cost_per_task_micro, x.avg_duration_ms, x.error_rate::float8, x.sample_count, x.extra
		 FROM benchmark_results x
		 LEFT JOIN virtual_models vm ON vm.id = x.virtual_model_id
		 WHERE x.run_id = $1`, runID)
	if err != nil {
		return nil, fmt.Errorf("benchmarks: query results: %w", err)
	}
	defer rows.Close()
	out := []BenchmarkResult{}
	for rows.Next() {
		var r BenchmarkResult
		var extra []byte
		if err := rows.Scan(&r.ModelLabel, &r.Model, &r.Score, &r.CostPerTaskMicro, &r.AvgDurationMs, &r.ErrorRate, &r.SampleCount, &extra); err != nil {
			return nil, fmt.Errorf("benchmarks: scan result: %w", err)
		}
		if len(extra) > 0 {
			r.Extra = extra
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rank(out, higherIsBetter)
	return out, nil
}

// ModelBenchmark 是某个模型在一个已发布基准上的成绩（GET /v1/model-benchmarks）。
type ModelBenchmark struct {
	Slug           string          `json:"slug"`
	Name           string          `json:"name"`
	Category       string          `json:"category"`
	MetricName     string          `json:"metric_name"`
	MetricUnit     string          `json:"metric_unit"`
	HigherIsBetter bool            `json:"higher_is_better"`
	SourceName     *string         `json:"source_name"`
	SourceURL      *string         `json:"source_url"`
	License        *string         `json:"license"`
	RunAt          time.Time       `json:"run_at"`
	ModelLabel     string          `json:"model_label"` // 榜单上的原始名（同一模型多个推理档位时取成绩最好的那个）
	Score          float64         `json:"score"`
	Rank           int             `json:"rank"`
	ModelsCount    int             `json:"models_count"`
	Extra          json.RawMessage `json:"extra"`
}

// ForModel 返回某个公开模型在全部已发布基准上的成绩（每个基准取该模型最好的一个档位），
// 按分类、基准排序。模型不存在或不公开时返回空列表。
func (st *Store) ForModel(ctx context.Context, model string) ([]ModelBenchmark, error) {
	rows, err := st.pool.Query(ctx,
		`WITH ranked AS (
		   SELECT x.run_id, x.model_label, x.virtual_model_id, x.score, x.extra,
		          rank() OVER (PARTITION BY x.run_id ORDER BY CASE WHEN b.higher_is_better THEN -x.score ELSE x.score END) AS rnk,
		          count(*) OVER (PARTITION BY x.run_id) AS n
		   FROM benchmark_results x
		   JOIN benchmark_runs r ON r.id = x.run_id AND r.published
		   JOIN benchmarks b ON b.id = r.benchmark_id AND b.status = 'published' AND `+publicSource+`
		 )
		 SELECT DISTINCT ON (b.id) b.slug, b.name, b.category, b.metric_name, b.metric_unit, b.higher_is_better,
		        b.source_name, b.source_url, (SELECT ds.license FROM price_sources ds WHERE ds.id = b.source_id),
		        r.run_at, k.model_label, k.score::float8, k.rnk, k.n, k.extra
		 FROM ranked k
		 JOIN benchmark_runs r ON r.id = k.run_id
		 JOIN benchmarks b ON b.id = r.benchmark_id
		 JOIN virtual_models vm ON vm.id = k.virtual_model_id
		 WHERE vm.name = $1 AND vm.status IN ('active','deprecated') AND 'free' = ANY(vm.visible_tiers)
		 ORDER BY b.id, k.rnk, k.model_label`, model)
	if err != nil {
		return nil, fmt.Errorf("benchmarks: query model benchmarks: %w", err)
	}
	defer rows.Close()
	out := []ModelBenchmark{}
	for rows.Next() {
		var m ModelBenchmark
		var extra []byte
		if err := rows.Scan(&m.Slug, &m.Name, &m.Category, &m.MetricName, &m.MetricUnit, &m.HigherIsBetter, &m.SourceName, &m.SourceURL,
			&m.License, &m.RunAt, &m.ModelLabel, &m.Score, &m.Rank, &m.ModelsCount, &extra); err != nil {
			return nil, fmt.Errorf("benchmarks: scan model benchmark: %w", err)
		}
		if len(extra) > 0 {
			m.Extra = extra
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	catOrder := map[string]int{}
	for i, c := range Categories {
		catOrder[c] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return catOrder[out[i].Category] < catOrder[out[j].Category]
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func better(a, b float64, higherIsBetter bool) bool {
	if higherIsBetter {
		return a > b
	}
	return a < b
}

// rank 按分数排序并写入名次；同分名次相同（1, 1, 3）。
func rank(rs []BenchmarkResult, higherIsBetter bool) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Score != rs[j].Score {
			return better(rs[i].Score, rs[j].Score, higherIsBetter)
		}
		return rs[i].ModelLabel < rs[j].ModelLabel
	})
	for i := range rs {
		if i > 0 && rs[i].Score == rs[i-1].Score {
			rs[i].Rank = rs[i-1].Rank
		} else {
			rs[i].Rank = i + 1
		}
	}
}

func toChampion(r BenchmarkResult) *BenchmarkChampion {
	return &BenchmarkChampion{ModelLabel: r.ModelLabel, Model: r.Model, Score: r.Score, CostPerTaskMicro: r.CostPerTaskMicro, AvgDurationMs: r.AvgDurationMs}
}

// champions 计算三项冠军；rs 已按分数从好到差排好序。
func champions(rs []BenchmarkResult, higherIsBetter bool) BenchmarkChampions {
	if len(rs) == 0 {
		return BenchmarkChampions{}
	}
	c := BenchmarkChampions{Quality: toChampion(rs[0])}
	// 中位数质量线取排序后第 ⌈n/2⌉ 名的分数；分数不差于它的模型参与性价比/速度评选。
	median := rs[(len(rs)-1)/2].Score
	var value, speed *BenchmarkResult
	for i := range rs {
		r := &rs[i]
		if better(median, r.Score, higherIsBetter) {
			continue
		}
		if r.CostPerTaskMicro != nil && (value == nil || *r.CostPerTaskMicro < *value.CostPerTaskMicro) {
			value = r
		}
		if r.AvgDurationMs != nil && (speed == nil || *r.AvgDurationMs < *speed.AvgDurationMs) {
			speed = r
		}
	}
	if value != nil {
		c.Value = toChampion(*value)
	}
	if speed != nil {
		c.Speed = toChampion(*speed)
	}
	return c
}
