// Package benchsync 从公开评测榜单抓取数据填充基准测试（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §4）。
//
// 一个来源（price_sources，domain=benchmark，fetcher=tabular）产出若干榜单（boards），每个
// 榜单对应一个 benchmarks 行（按 external_key 关联，第一次导入时自动创建）。一次运行：
//
//	下载（同一 URL 只下一次）-> 全部内容没变就结束 -> 按格式解析成行 -> 过滤 / 去重 / 换算 ->
//	模型名映射（model_aliases）-> 内容与上次导入相同就跳过 -> 新建 origin=import 的 run ->
//	来源允许自动发布且通过异常检查 -> 发布（发布时按 score_key 投影进 scores）；否则留草稿。
//
// 许可：来源 public_display=false 时数据照常导入（运营后台可见），但 /v1/benchmarks 不返回、
// 也不投影进 scores（见 internal/benchmarks 与 admin.ApplyScoreProjection）。
package benchsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

// maxResultsPerRun 与 admin 单次 run 的上限一致。
const maxResultsPerRun = 1000

// 异常检查阈值：与上次发布相比，共同模型里分数变化超过 20% 的占比超过 30%，或行数不到一半，就不自动发布。
const (
	scoreJumpRatio    = 0.2
	maxJumpedFraction = 0.3
	minRowKeepRatio   = 0.5
)

// Publisher 是导入需要的 admin 能力（*admin.Service 满足）。
type Publisher interface {
	CreateBenchmark(ctx context.Context, in admin.CreateBenchmarkInput) (*admin.Benchmark, error)
	CreateBenchmarkRun(ctx context.Context, benchmarkID int64, in admin.CreateBenchmarkRunInput) (*admin.BenchmarkRunDetail, error)
	PublishBenchmarkRun(ctx context.Context, runID int64) (*admin.BenchmarkRun, error)
}

// Job 是 fetcher=tabular 的 datasync.Job。
type Job struct {
	Pool      *pgxpool.Pool
	Publisher Publisher
}

func (j *Job) db(ctx context.Context) store.Querier { return store.Q(ctx, j.Pool) }

// Entry 是榜单的一行（已换算、已去重）。
type Entry struct {
	Label      string         `json:"label"`
	Score      float64        `json:"score"`
	CostMicro  *int64         `json:"cost_micro,omitempty"`
	DurationMs *int           `json:"duration_ms,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

func (j *Job) Run(ctx context.Context, env *datasync.Env, src datasync.Source) (datasync.Result, error) {
	cfg, err := decodeConfig(src.Config)
	if err != nil {
		return datasync.Result{}, err
	}
	headers := map[string]string{}
	if cfg.AuthHeader != "" {
		v := os.Getenv(cfg.AuthHeaderEnv)
		if cfg.AuthHeaderEnv == "" || v == "" {
			return datasync.Result{}, fmt.Errorf("benchsync: source needs env %s for header %s", cfg.AuthHeaderEnv, cfg.AuthHeader)
		}
		headers[cfg.AuthHeader] = v
	}

	bodies := map[string][]byte{}
	var urls []string
	for _, b := range cfg.Boards {
		u := b.URL
		if u == "" {
			u = src.URL
		}
		if u == "" {
			return datasync.Result{}, fmt.Errorf("benchsync: board %s has no url", b.Key)
		}
		if _, ok := bodies[u]; ok {
			continue
		}
		resp, err := env.Get(ctx, u, datasync.GetOptions{Headers: headers})
		if err != nil {
			return datasync.Result{}, err
		}
		bodies[u] = resp.Body
		urls = append(urls, u)
	}
	slices.Sort(urls)
	var parts [][]byte
	for _, u := range urls {
		sum := sha256.Sum256(bodies[u])
		parts = append(parts, []byte(u), sum[:])
	}
	// 配置也算进指纹：改了榜单配置（比如新增一个 board）要重新导入。虚拟模型目录也算进去：
	// 新上架 / 改名的模型要能自动关联到没变的榜单数据上。
	cfgJSON, _ := json.Marshal(cfg)
	var catalogFP string
	if err := j.db(ctx).QueryRow(ctx,
		`SELECT COALESCE(md5(string_agg(name || ':' || array_to_string(aliases, ','), '|' ORDER BY id)), '') FROM virtual_models`,
	).Scan(&catalogFP); err != nil {
		return datasync.Result{}, fmt.Errorf("benchsync: catalog fingerprint: %w", err)
	}
	parts = append(parts, cfgJSON, []byte(catalogFP))
	hash := datasync.HashParts(parts...)
	if bytes.Equal(hash, src.LastContentHash) {
		return datasync.Result{Status: datasync.StatusUnchanged, ContentHash: hash}, nil
	}

	namespace := cfg.AliasNamespace
	if namespace == "" {
		namespace = fmt.Sprintf("source-%d", src.ID)
	}
	resolver, err := j.newResolver(ctx, namespace)
	if err != nil {
		return datasync.Result{}, err
	}

	boardsDetail := map[string]any{}
	var fetched, changed int
	var failures []string
	rejected := false
	for _, b := range cfg.Boards {
		u := b.URL
		if u == "" {
			u = src.URL
		}
		format := b.Format
		if format == "" {
			format = cfg.Format
		}
		rowsPath := b.RowsPath
		if rowsPath == "" {
			rowsPath = cfg.RowsPath
		}
		rows, err := parseRows(bodies[u], format, b.File, rowsPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", b.Key, err))
			rejected = true
			continue
		}
		entries, runAt := BuildEntries(rows, b)
		minRows := b.MinRows
		if minRows <= 0 {
			minRows = 5
		}
		if len(entries) < minRows {
			failures = append(failures, fmt.Sprintf("%s: only %d rows (min %d)", b.Key, len(entries), minRows))
			rejected = true
			continue
		}
		fetched += len(entries)
		out, err := j.importBoard(ctx, src, namespace, b, entries, runAt, resolver)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", b.Key, err))
			continue
		}
		boardsDetail[b.Key] = out
		if out["status"] != "unchanged" {
			changed++
		}
	}
	detail := map[string]any{"boards": boardsDetail}
	if len(failures) > 0 {
		detail["failures"] = failures
		// 不写 ContentHash：成功的榜单各自有 run 级指纹，下次重跑时会被跳过，只重试失败的。
		err := fmt.Errorf("benchsync: %d board(s) failed: %s", len(failures), strings.Join(failures, "; "))
		if rejected {
			err = fmt.Errorf("%w: %s", datasync.ErrRejected, strings.Join(failures, "; "))
		}
		return datasync.Result{ItemsFetched: fetched, ItemsChanged: changed, Detail: detail}, err
	}
	return datasync.Result{ItemsFetched: fetched, ItemsChanged: changed, ContentHash: hash, Detail: detail}, nil
}

// BuildEntries 把原始行按榜单配置转换成 Entry：过滤、取分、换算、同名取最好、按分数排序、截断。
// 返回数据里最新的评测日期（没有日期列时为零值）。
func BuildEntries(rows []Row, b boardConfig) ([]Entry, time.Time) {
	higher := b.higherIsBetter()
	best := map[string]Entry{}
	var runAt time.Time
	for _, r := range rows {
		skip := false
		for k, v := range b.Filter {
			if toString(lookup(map[string]any(r), k)) != v {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		label := toString(lookup(map[string]any(r), b.Label))
		score, ok := toFloat(lookup(map[string]any(r), b.Score))
		if label == "" || !ok {
			continue
		}
		if b.Scale != 0 {
			score *= b.Scale
		}
		e := Entry{Label: label, Score: math.Round(score*10000) / 10000}
		if b.CostUSD != "" {
			if v, ok := toFloat(lookup(map[string]any(r), b.CostUSD)); ok && v >= 0 {
				micro := int64(math.Round(v * 1e6))
				e.CostMicro = &micro
			}
		}
		if b.DurationSeconds != "" {
			if v, ok := toFloat(lookup(map[string]any(r), b.DurationSeconds)); ok && v >= 0 {
				ms := int(math.Round(v * 1000))
				e.DurationMs = &ms
			}
		}
		if len(b.Extra) > 0 {
			e.Extra = map[string]any{}
			for k, col := range b.Extra {
				v := lookup(map[string]any(r), col)
				if f, ok := v.(json.Number); ok {
					if x, err := f.Float64(); err == nil {
						v = x
					}
				}
				if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
					continue
				}
				if v != nil {
					e.Extra[k] = v
				}
			}
			if len(e.Extra) == 0 {
				e.Extra = nil
			}
		}
		if b.RunAt != "" {
			if t, ok := parseDate(toString(lookup(map[string]any(r), b.RunAt))); ok && t.After(runAt) {
				runAt = t
			}
		}
		if prev, ok := best[label]; !ok || (higher && e.Score > prev.Score) || (!higher && e.Score < prev.Score) {
			best[label] = e
		}
	}
	out := make([]Entry, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, c Entry) int {
		if a.Score != c.Score {
			if (a.Score > c.Score) == higher {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Label, c.Label)
	})
	if len(out) > maxResultsPerRun {
		out = out[:maxResultsPerRun]
	}
	return out, runAt
}

// importBoard 导入一个榜单，返回运行明细（写进 data_source_runs.detail）。
func (j *Job) importBoard(ctx context.Context, src datasync.Source, namespace string, b boardConfig, entries []Entry, runAt time.Time,
	resolver *resolver) (map[string]any, error) {
	bm, err := j.ensureBenchmark(ctx, src, namespace, b)
	if err != nil {
		return nil, err
	}

	labels := make([]string, len(entries))
	for i, e := range entries {
		labels[i] = e.Label
	}
	links, err := resolver.resolve(ctx, labels)
	if err != nil {
		return nil, err
	}

	canonical, _ := json.Marshal(struct {
		Entries []Entry           `json:"entries"`
		Links   map[string]*int64 `json:"links"`
	}{entries, links})
	sum := sha256.Sum256(canonical)
	var lastHash []byte
	err = j.db(ctx).QueryRow(ctx,
		`SELECT content_hash FROM benchmark_runs WHERE benchmark_id = $1 AND origin = 'import' ORDER BY created_at DESC, id DESC LIMIT 1`,
		bm.id).Scan(&lastHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("benchsync: load last import: %w", err)
	}
	if bytes.Equal(lastHash, sum[:]) {
		return map[string]any{"status": "unchanged", "benchmark_id": bm.id, "rows": len(entries)}, nil
	}

	results := make([]admin.BenchmarkResultInput, 0, len(entries))
	linked := 0
	for _, e := range entries {
		score := e.Score
		in := admin.BenchmarkResultInput{ModelLabel: e.Label, Score: &score, CostPerTaskMicro: e.CostMicro, AvgDurationMs: e.DurationMs}
		if id := links[e.Label]; id != nil {
			in.VirtualModelID = id
			linked++
		}
		if e.Extra != nil {
			raw, _ := json.Marshal(e.Extra)
			in.Extra = raw
		}
		results = append(results, in)
	}
	if runAt.IsZero() {
		runAt = time.Now()
	}
	sourceURL := b.URL
	if sourceURL == "" {
		sourceURL = src.URL
	}
	run, err := j.Publisher.CreateBenchmarkRun(ctx, bm.id, admin.CreateBenchmarkRunInput{
		Origin: "import", RunAt: runAt, CostCurrency: "USD", Results: results, ContentHash: sum[:],
		Notes: fmt.Sprintf("自动导入：%s（%s）", src.Name, sourceURL),
	})
	if err != nil {
		return nil, fmt.Errorf("benchsync: create run: %w", err)
	}
	out := map[string]any{"status": "draft", "benchmark_id": bm.id, "run_id": run.ID, "rows": len(entries), "linked": linked}

	if !src.AutoPublish || bm.status != "published" {
		out["hold_reason"] = "auto_publish disabled or benchmark not published"
		return out, nil
	}
	if reason, err := j.sanityCheck(ctx, bm.id, entries); err != nil {
		return nil, err
	} else if reason != "" {
		out["hold_reason"] = reason
		if _, err := j.db(ctx).Exec(ctx, `UPDATE benchmark_runs SET notes = notes || $2 WHERE id = $1`, run.ID, "\n未自动发布："+reason); err != nil {
			return nil, fmt.Errorf("benchsync: annotate run: %w", err)
		}
		return out, nil
	}
	if _, err := j.Publisher.PublishBenchmarkRun(ctx, run.ID); err != nil {
		return nil, fmt.Errorf("benchsync: publish run: %w", err)
	}
	out["status"] = "published"
	return out, nil
}

type benchmarkRef struct {
	id     int64
	status string
}

// ensureBenchmark 按 external_key 找基准，没有就按榜单配置创建（自动发布的来源直接建成 published）。
func (j *Job) ensureBenchmark(ctx context.Context, src datasync.Source, namespace string, b boardConfig) (benchmarkRef, error) {
	var ref benchmarkRef
	err := j.db(ctx).QueryRow(ctx, `SELECT id, status FROM benchmarks WHERE external_key = $1`, b.Key).Scan(&ref.id, &ref.status)
	if err == nil {
		// 运营可能改过来源 / 命名空间配置：以最新配置为准（名称、分类等展示字段尊重运营的修改）。
		if _, err := j.db(ctx).Exec(ctx,
			`UPDATE benchmarks SET source_id = $2, alias_namespace = $3, updated_at = now()
			 WHERE id = $1 AND (source_id IS DISTINCT FROM $2 OR alias_namespace IS DISTINCT FROM $3)`, ref.id, src.ID, namespace); err != nil {
			return ref, fmt.Errorf("benchsync: update benchmark source: %w", err)
		}
		return ref, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ref, fmt.Errorf("benchsync: find benchmark: %w", err)
	}
	status := "draft"
	if src.AutoPublish {
		status = "published"
	}
	def := b.Benchmark
	// slug 已被运营手工建的基准占用时不接管它（避免自动导入覆盖人工维护的数据），改用"slug-命名空间"。
	var taken bool
	if err := j.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM benchmarks WHERE slug = $1)`, def.Slug).Scan(&taken); err != nil {
		return ref, fmt.Errorf("benchsync: check slug: %w", err)
	}
	if taken {
		def.Slug = def.Slug + "-" + slugPart(namespace)
	}
	sourceName := src.Name
	if src.Attribution != "" {
		sourceName = src.Attribution
	}
	var sourceURL *string
	if def.SourceURL != "" {
		sourceURL = &def.SourceURL
	}
	unit := def.MetricUnit
	sid, key, ns := src.ID, b.Key, namespace
	in := admin.CreateBenchmarkInput{
		Slug: def.Slug, Name: def.Name, Category: def.Category, Description: def.Description, MetricName: def.MetricName,
		MetricUnit: unit, HigherIsBetter: def.HigherIsBetter, SourceName: &sourceName, SourceURL: sourceURL,
		Status: status, SortOrder: def.SortOrder, DataSourceID: &sid, ExternalKey: &key, AliasNamespace: &ns,
	}
	if b.ScoreKey != "" {
		sk := b.ScoreKey
		in.ScoreKey = &sk
	}
	created, err := j.Publisher.CreateBenchmark(ctx, in)
	if err != nil {
		return ref, fmt.Errorf("benchsync: create benchmark %s: %w", def.Slug, err)
	}
	return benchmarkRef{id: created.ID, status: created.Status}, nil
}

// slugPart 把命名空间转成可拼进 slug 的形式（小写字母数字，单个连字符）。
func slugPart(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// sanityCheck 与当前发布的 run 比较；返回非空字符串表示不宜自动发布的原因。
func (j *Job) sanityCheck(ctx context.Context, benchmarkID int64, entries []Entry) (string, error) {
	rows, err := j.db(ctx).Query(ctx,
		`SELECT x.model_label, x.score::float8 FROM benchmark_results x JOIN benchmark_runs r ON r.id = x.run_id
		 WHERE r.benchmark_id = $1 AND r.published`, benchmarkID)
	if err != nil {
		return "", fmt.Errorf("benchsync: load published results: %w", err)
	}
	prev := map[string]float64{}
	for rows.Next() {
		var label string
		var score float64
		if err := rows.Scan(&label, &score); err != nil {
			rows.Close()
			return "", err
		}
		prev[label] = score
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(prev) == 0 {
		return "", nil
	}
	if float64(len(entries)) < float64(len(prev))*minRowKeepRatio {
		return fmt.Sprintf("row count dropped from %d to %d", len(prev), len(entries)), nil
	}
	var common, jumped int
	for _, e := range entries {
		p, ok := prev[e.Label]
		if !ok {
			continue
		}
		common++
		if math.Abs(e.Score-p) > math.Max(math.Abs(p), 1e-9)*scoreJumpRatio {
			jumped++
		}
	}
	if common >= 5 && float64(jumped) > float64(common)*maxJumpedFraction {
		return fmt.Sprintf("%d of %d models changed score by more than %.0f%%", jumped, common, scoreJumpRatio*100), nil
	}
	return "", nil
}
