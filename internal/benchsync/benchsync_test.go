package benchsync

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/parquet-go/parquet-go"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

// ---------- 纯函数：解析与换算 ----------

type arenaRow struct {
	ModelName string  `parquet:"model_name"`
	Rating    float64 `parquet:"rating"`
	Rank      int64   `parquet:"rank"`
	Category  string  `parquet:"category"`
	Date      string  `parquet:"leaderboard_publish_date"`
}

func TestParseRows_Formats(t *testing.T) {
	var pq bytes.Buffer
	if err := parquet.Write(&pq, []arenaRow{
		{"model-a-high", 1500.5, 1, "overall", "2026-09-30"},
		{"model-b", 1450, 2, "overall", "2026-09-30"},
		{"model-a-high", 1400, 5, "coding", "2026-09-30"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := parseRows(pq.Bytes(), "parquet", "", "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("parquet: %v rows=%d", err, len(rows))
	}
	b := boardConfig{Label: "model_name", Score: "rating", Filter: map[string]string{"category": "overall"}, RunAt: "leaderboard_publish_date",
		Extra: map[string]string{"rank": "rank"}}
	entries, runAt := BuildEntries(rows, b)
	if len(entries) != 2 || entries[0].Label != "model-a-high" || entries[0].Score != 1500.5 || entries[0].Extra["rank"] != int64(1) {
		t.Errorf("parquet entries: %+v", entries)
	}
	if runAt.Format(time.DateOnly) != "2026-09-30" {
		t.Errorf("runAt = %v", runAt)
	}

	// zip 里的 CSV：同一模型多行（多个 agent）取最好；0~1 换算成百分比；成本 / 耗时换算。
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("dir/bench.csv")
	_, _ = f.Write([]byte("\xef\xbb\xbfModel version,Score,Cost per task,Time (s),Notes\n" +
		"m1_max,0.5,0.12,3.5,\"multi\nline\"\n" +
		"m1_max,0.75,0.10,2,\n" +
		"m2,0.25,,,\n" +
		"bad,,1,1,\n"))
	_ = zw.Close()
	rows, err = parseRows(zbuf.Bytes(), "zip_csv", "dir/bench.csv", "")
	if err != nil {
		t.Fatal(err)
	}
	entries, _ = BuildEntries(rows, boardConfig{Label: "Model version", Score: "Score", Scale: 100, CostUSD: "Cost per task", DurationSeconds: "Time (s)"})
	if len(entries) != 2 || entries[0].Label != "m1_max" || entries[0].Score != 75 || *entries[0].CostMicro != 100000 || *entries[0].DurationMs != 2000 {
		t.Errorf("zip_csv entries: %+v", entries)
	}
	if entries[1].CostMicro != nil {
		t.Errorf("empty cost should be nil: %+v", entries[1])
	}
	if _, err := parseRows(zbuf.Bytes(), "zip_csv", "missing.csv", ""); err == nil {
		t.Error("missing zip member should fail")
	}

	// JSON：点分路径取嵌套字段；越低越好时取最小值并升序。
	js := []byte(`{"data":[{"slug":"x","evaluations":{"idx":12.5}},{"slug":"y","evaluations":{"idx":7}},{"slug":"x","evaluations":{"idx":9}}]}`)
	rows, err = parseRows(js, "json", "", "data")
	if err != nil {
		t.Fatal(err)
	}
	lower := false
	entries, _ = BuildEntries(rows, boardConfig{Label: "slug", Score: "evaluations.idx", Benchmark: benchmarkDef{HigherIsBetter: &lower}})
	if len(entries) != 2 || entries[0].Label != "y" || entries[1].Score != 9 {
		t.Errorf("json entries (lower is better): %+v", entries)
	}

	yml := []byte("- model: a\n  pass_rate_2: 61.5\n- model: b\n  pass_rate_2: 70\n")
	rows, err = parseRows(yml, "yaml", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ = BuildEntries(rows, boardConfig{Label: "model", Score: "pass_rate_2"}); len(entries) != 2 || entries[0].Label != "b" {
		t.Errorf("yaml entries: %+v", entries)
	}
}

func TestDecodeConfig_Validation(t *testing.T) {
	for _, bad := range []map[string]any{
		{},
		{"boards": []any{map[string]any{"key": "a", "label": "m"}}},
		{"boards": []any{map[string]any{"key": "a", "label": "m", "score": "s", "benchmark": map[string]any{"slug": "a"}}}},
	} {
		if _, err := decodeConfig(bad); err == nil {
			t.Errorf("expected error for %v", bad)
		}
	}
}

// ---------- 集成：导入、映射、发布、投影 ----------

const defaultTestDSN = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("UFT_TEST_PG_DSN")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: cannot create postgres pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping: postgres not reachable at %s: %v", dsn, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// projected 读 virtual_model_metadata.scores.gpqa_diamond；没有时返回 nil。
func projected(t *testing.T, svc *admin.Service, vmID int64) *float64 {
	t.Helper()
	md, err := svc.GetVirtualModelMetadata(context.Background(), vmID)
	if err != nil {
		t.Fatal(err)
	}
	if md == nil || len(md.Scores) == 0 {
		return nil
	}
	var scores map[string]any
	if err := json.Unmarshal(md.Scores, &scores); err != nil {
		t.Fatal(err)
	}
	v, ok := scores["gpqa_diamond"].(float64)
	if !ok {
		return nil
	}
	return &v
}

func TestJob_ImportPublishProjectAndRemap(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	svc := admin.New(pool, nil, nil, nil)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// 两个本测试专属的虚拟模型。
	vmA, err := svc.CreateVirtualModel(ctx, admin.CreateVirtualModelInput{Name: "acme/alpha-" + suffix, Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatal(err)
	}
	vmB, err := svc.CreateVirtualModel(ctx, admin.CreateVirtualModelInput{Name: "acme/beta-" + suffix, Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatal(err)
	}

	var csvBody atomic.Value
	setCSV := func(rows ...string) {
		csvBody.Store("model,score\n" + strings.Join(rows, "\n") + "\n")
	}
	setCSV(
		"alpha-"+suffix+"-high,80", "alpha-"+suffix+"-low,60", // 同一模型两个档位
		"Beta "+suffix+" Preview,70", // 模糊：只建议
		"other-1,50", "other-2,40", "other-3,30",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(csvBody.Load().(string)))
	}))
	defer srv.Close()

	key := "test:" + suffix
	ns := "test-ns-" + suffix
	slug := "test-bench-" + suffix
	cfg := map[string]any{
		"alias_namespace": ns, "format": "csv",
		"boards": []any{map[string]any{
			"key": key, "label": "model", "score": "score", "score_key": "gpqa_diamond",
			"benchmark": map[string]any{"slug": slug, "name": "Test Bench", "category": "reasoning", "metric_name": "Accuracy"},
		}},
	}
	cfgJSON, _ := json.Marshal(cfg)
	var srcID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_sources (domain, name, level, kind, fetcher, url, schedule, enabled, public_display, auto_publish, license, config)
		 VALUES ('benchmark', 'test source', 'L4', 'dataset', 'tabular', $1, '', true, true, true, 'CC-BY-4.0', $2) RETURNING id`,
		srv.URL, cfgJSON).Scan(&srcID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM benchmarks WHERE external_key = $1`, key)
		_, _ = pool.Exec(ctx, `DELETE FROM model_aliases WHERE namespace = $1`, ns)
		_, _ = pool.Exec(ctx, `DELETE FROM price_sources WHERE id = $1`, srcID)
	})

	// 同 slug 的手工基准已存在：导入不接管它，而是建一个带命名空间后缀的新基准。
	manual, err := svc.CreateBenchmark(ctx, admin.CreateBenchmarkInput{Slug: slug, Name: "manual", Category: "reasoning", MetricName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM benchmarks WHERE id = $1`, manual.ID) })

	job := &Job{Pool: pool, Publisher: svc}
	env := datasync.NewEnv(datasync.EnvOptions{AllowPrivateNetworks: true, MinHostInterval: -1})
	load := func() datasync.Source {
		src, err := datasync.LoadSource(ctx, pool, srcID)
		if err != nil {
			t.Fatal(err)
		}
		return src
	}

	res, err := job.Run(ctx, env, load())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.ItemsFetched != 6 || res.ItemsChanged != 1 {
		t.Errorf("first run result: %+v", res)
	}
	var benchID int64
	var status string
	if err := pool.QueryRow(ctx, `SELECT id, status FROM benchmarks WHERE external_key = $1`, key).Scan(&benchID, &status); err != nil {
		t.Fatal(err)
	}
	if status != "published" {
		t.Errorf("auto-publish source should create a published benchmark, got %s", status)
	}
	var gotSlug string
	_ = pool.QueryRow(ctx, `SELECT slug FROM benchmarks WHERE id = $1`, benchID).Scan(&gotSlug)
	if gotSlug != slug+"-"+slugPart(ns) {
		t.Errorf("imported benchmark slug = %q, want %q (manual one keeps %q)", gotSlug, slug+"-"+slugPart(ns), slug)
	}
	detail, err := svc.GetBenchmarkDetail(ctx, benchID)
	if err != nil || len(detail.Runs) != 1 || !detail.Runs[0].Published {
		t.Fatalf("expected one published run: %+v %v", detail, err)
	}
	// 两个档位都关联到 alpha，scores 取最好的 80。
	if got := projected(t, svc, vmA.ID); got == nil || *got != 80 {
		t.Errorf("projection for alpha = %v, want 80", got)
	}
	if got := projected(t, svc, vmB.ID); got != nil {
		t.Errorf("fuzzy suggestion must not be projected, got %v", *got)
	}
	var aliasStatus string
	var suggested *int64
	if err := pool.QueryRow(ctx, `SELECT status, virtual_model_id FROM model_aliases WHERE namespace = $1 AND external_label = $2`,
		ns, "Beta "+suffix+" Preview").Scan(&aliasStatus, &suggested); err != nil {
		t.Fatal(err)
	}
	if aliasStatus != "suggested" || suggested == nil || *suggested != vmB.ID {
		t.Errorf("beta alias = %s/%v, want suggested/%d", aliasStatus, suggested, vmB.ID)
	}

	// 内容没变：整体 unchanged。
	if _, err := pool.Exec(ctx, `UPDATE price_sources SET last_content_hash = $2 WHERE id = $1`, srcID, res.ContentHash); err != nil {
		t.Fatal(err)
	}
	// （其它包的测试可能同时在建虚拟模型，改变目录指纹；那样会重新解析，但每个榜单都应判为 unchanged。）
	if res, err := job.Run(ctx, env, load()); err != nil {
		t.Errorf("second run: %v", err)
	} else if res.Status != datasync.StatusUnchanged {
		if b := res.Detail["boards"].(map[string]any)[key].(map[string]any); b["status"] != "unchanged" {
			t.Errorf("second run should be unchanged: %+v", res)
		}
	}

	// 人工确认 beta：已导入结果重新关联，已发布 run 重新投影。
	out, err := svc.SetModelAlias(ctx, admin.SetModelAliasInput{Namespace: ns, ExternalLabel: "Beta " + suffix + " Preview", Status: "confirmed",
		VirtualModelID: &vmB.ID}, "tester")
	if err != nil || out.RelinkedResults != 1 || out.ReprojectedRuns != 1 {
		t.Fatalf("SetModelAlias: %+v %v", out, err)
	}
	if got := projected(t, svc, vmB.ID); got == nil || *got != 70 {
		t.Errorf("confirmed alias should project beta's score, got %v", got)
	}

	// 分数大面积跳变：新 run 留草稿，不自动发布。
	setCSV("alpha-"+suffix+"-high,20", "alpha-"+suffix+"-low,10", "Beta "+suffix+" Preview,5", "other-1,1", "other-2,2", "other-3,3")
	res, err = job.Run(ctx, env, load())
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	board := res.Detail["boards"].(map[string]any)[key].(map[string]any)
	if board["status"] != "draft" || board["hold_reason"] == nil {
		t.Errorf("anomalous run should be held as draft: %+v", board)
	}
	if got := projected(t, svc, vmA.ID); got == nil || *got != 80 {
		t.Errorf("held run must not change scores, got %v", got)
	}

	// 行数不足：整批 rejected。
	setCSV("alpha-" + suffix + "-high,80")
	if _, err := job.Run(ctx, env, load()); err == nil || !strings.Contains(err.Error(), "only 1 rows") {
		t.Errorf("expected rejection for too few rows, got %v", err)
	}
}
