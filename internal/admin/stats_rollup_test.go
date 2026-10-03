package admin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
)

func TestHistogramPercentile(t *testing.T) {
	counts := make([]int64, len(reqlog.LatencyBucketBounds)+1)
	if histogramPercentile(counts, 0.5) != nil {
		t.Fatal("empty histogram must return nil")
	}
	counts[4] = 10 // 全部落在 (300, 500]
	if p := histogramPercentile(counts, 0.5); p == nil || *p != 400 {
		t.Errorf("p50 = %v, want 400 (linear interpolation within the bucket)", p)
	}
	counts[len(counts)-1] = 90 // 90% 超过最大上界
	if p := histogramPercentile(counts, 0.95); p == nil || *p != 30000 {
		t.Errorf("p95 = %v, want 30000 (lower bound of the open-ended bucket)", p)
	}
}

// TestUsage_RollupMatchesRaw：同一批请求日志，短时间窗走原始表、长时间窗走小时汇总，
// 计数与金额必须一致，延迟分位数在直方图精度内接近。
func TestUsage_RollupMatchesRaw(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()
	model := uniqueCode(t)
	now := time.Now().UTC()
	base := now.Truncate(time.Hour).Add(-2 * time.Hour)
	for i, l := range []struct {
		status          string
		latency, charge int
	}{{"success", 120, 1000}, {"success", 480, 2000}, {"success", 900, 3000}, {"upstream_error", 0, 0}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream,
			   status, http_status, attempts, latency_ms, input_tokens, output_tokens, usage_source, charged_amount, list_amount, cost_amount)
			 VALUES ($1, $2, 7, 7, $3, '/v1/chat/completions', false, $4, 200, 1, $5, 100, 50, 'upstream', $6::bigint, $6::bigint, $6::bigint / 2)`,
			fmt.Sprintf("%s-%d", model, i), base.Add(time.Duration(i)*time.Minute), model, l.status, l.latency, l.charge); err != nil {
			t.Fatalf("seed request_log: %v", err)
		}
	}
	if _, err := reqlog.RollupUsage(ctx, pool, base, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupUsage: %v", err)
	}

	raw, err := s.Usage(ctx, UsageInput{StatsFilter: StatsFilter{From: base.Add(-time.Hour), To: now, VirtualModel: model}, Interval: "none"})
	if err != nil {
		t.Fatalf("raw usage: %v", err)
	}
	roll, err := s.Usage(ctx, UsageInput{StatsFilter: StatsFilter{From: now.Add(-72 * time.Hour), To: now, VirtualModel: model}, Interval: "day"})
	if err != nil {
		t.Fatalf("rollup usage: %v", err)
	}
	if raw.Source != "raw" || roll.Source != "rollup" {
		t.Fatalf("sources = %s / %s, want raw / rollup", raw.Source, roll.Source)
	}
	a, b := raw.Totals, roll.Totals
	if a.Requests != 4 || a.Requests != b.Requests || a.Success != b.Success || a.RevenueMicro != b.RevenueMicro ||
		a.CostMicro != b.CostMicro || a.InputTokens != b.InputTokens || a.ActiveAccounts != b.ActiveAccounts {
		t.Errorf("raw totals %+v != rollup totals %+v", a, b)
	}
	if b.P50LatencyMs == nil || *b.P50LatencyMs < 300 || *b.P50LatencyMs > 500 {
		t.Errorf("rollup p50 = %v, want within the (300,500] bucket (raw p50 = %v)", b.P50LatencyMs, a.P50LatencyMs)
	}
	if len(roll.Series) == 0 {
		t.Error("rollup series is empty")
	}
	// 回归：按天的序列每天只有一行（汇总表的 bucket 列曾让 GROUP BY 按小时分组）。
	var dayTotal int64
	seen := map[string]bool{}
	for _, p := range roll.Series {
		if seen[p.Bucket+"|"+p.Group] {
			t.Errorf("duplicate series point for bucket %s", p.Bucket)
		}
		seen[p.Bucket+"|"+p.Group] = true
		dayTotal += p.Requests
	}
	if dayTotal != b.Requests {
		t.Errorf("daily series sums to %d requests, totals say %d", dayTotal, b.Requests)
	}
}

// TestUsage_RollupDailySeriesOnePointPerDay：同一天不同小时的请求，读小时汇总表按天分桶时必须合并成一个点
// （回归：GROUP BY bucket 曾被解析为汇总表自身的 bucket 列，按小时分组）。
func TestUsage_RollupDailySeriesOnePointPerDay(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()
	model := uniqueCode(t)
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-3 * 24 * time.Hour)
	for i, at := range []time.Time{day.Add(30 * time.Minute), day.Add(5*time.Hour + 10*time.Minute)} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream,
			   status, http_status, attempts, latency_ms, input_tokens, output_tokens, usage_source, charged_amount, list_amount, cost_amount)
			 VALUES ($1, $2, 7, 7, $3, '/v1/chat/completions', false, 'success', 200, 1, 100, 10, 5, 'upstream', 1000, 1000, 500)`,
			fmt.Sprintf("%s-%d", model, i), at, model); err != nil {
			t.Fatalf("seed request_log: %v", err)
		}
	}
	if _, err := reqlog.RollupUsage(ctx, pool, day, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupUsage: %v", err)
	}
	res, err := s.Usage(ctx, UsageInput{StatsFilter: StatsFilter{From: day.Add(-24 * time.Hour), To: day.Add(48 * time.Hour), VirtualModel: model}, Interval: "day"})
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if res.Source != "rollup" || len(res.Series) != 1 || res.Series[0].Requests != 2 || res.Series[0].Bucket != day.Format("2006-01-02") {
		t.Errorf("source %s series %+v, want one rollup point with 2 requests on %s", res.Source, res.Series, day.Format("2006-01-02"))
	}
}
