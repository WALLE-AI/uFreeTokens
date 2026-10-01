package reqlog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 用量小时汇总（迁移 00021 的 usage_hourly，B8）。运营后台的统计接口在时间窗
// 超过 48 小时时读汇总表，不再扫描原始 request_logs。

// LatencyBucketBounds 是延迟直方图各桶的上界（毫秒）；第 len(bounds) 个桶是 +inf。
// usage_hourly 的 lat_b0..lat_b13 / ttft_b0..ttft_b13 与之一一对应，改动需要同步迁移。
var LatencyBucketBounds = []int64{50, 100, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 10000, 30000}

// HistogramColumns 返回 prefix_b0..prefix_bN 列名。
func HistogramColumns(prefix string) []string {
	cols := make([]string, len(LatencyBucketBounds)+1)
	for i := range cols {
		cols[i] = fmt.Sprintf("%s_b%d", prefix, i)
	}
	return cols
}

// histogramExprs 生成按桶计数的聚合表达式（只统计 cond 为真的行）。
func histogramExprs(col, cond string) []string {
	out := make([]string, 0, len(LatencyBucketBounds)+1)
	lower := "-1"
	for _, b := range LatencyBucketBounds {
		out = append(out, fmt.Sprintf("count(*) FILTER (WHERE %s AND rl.%s > %s AND rl.%s <= %d)", cond, col, lower, col, b))
		lower = fmt.Sprint(b)
	}
	return append(out, fmt.Sprintf("count(*) FILTER (WHERE %s AND rl.%s > %s)", cond, col, lower))
}

// RollupUsage 重新计算 [from, to) 覆盖的每个整点小时的汇总（from 向下、to 向上取整到
// 小时）并写入 usage_hourly。按"重算覆盖"而不是"累加"，所以可以对同一时段反复
// 执行（worker 每 5 分钟重算最近 2 小时，迟到的日志也会被补进来）。返回写入行数。
func RollupUsage(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (int64, error) {
	from = from.UTC().Truncate(time.Hour)
	if t := to.UTC().Truncate(time.Hour); t.Before(to) {
		to = t.Add(time.Hour)
	} else {
		to = t
	}
	if !to.After(from) {
		return 0, nil
	}
	lat, ttft := HistogramColumns("lat"), HistogramColumns("ttft")
	cols := append([]string{"bucket", "account_id", "api_key_id", "virtual_model", "virtual_model_id", "channel_id", "provider_id",
		"requests", "success", "estimated", "input_tokens", "output_tokens", "cache_read_tokens", "reasoning_tokens",
		"charged_micro", "list_micro", "cost_micro"}, append(lat, ttft...)...)
	selects := append([]string{
		"date_trunc('hour', rl.created_at) AS bucket", "rl.account_id", "rl.api_key_id", "rl.virtual_model",
		"max(COALESCE(rl.virtual_model_id, (SELECT vm.id FROM virtual_models vm WHERE vm.name = rl.virtual_model)))", "rl.channel_id", "pa.provider_id",
		"count(*)", "count(*) FILTER (WHERE rl.status = 'success')", "count(*) FILTER (WHERE rl.usage_source = 'estimated')",
		"COALESCE(sum(rl.input_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.output_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.cache_read_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.reasoning_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.charged_amount), 0)", "COALESCE(sum(rl.list_amount), 0)", "COALESCE(sum(rl.cost_amount), 0)",
	}, append(histogramExprs("latency_ms", "rl.status = 'success'"), histogramExprs("ttft_ms", "rl.status = 'success' AND rl.is_stream")...)...)
	updates := make([]string, 0, len(cols))
	for _, c := range cols[7:] {
		updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", c, c))
	}
	updates = append(updates, "virtual_model_id = EXCLUDED.virtual_model_id", "provider_id = EXCLUDED.provider_id", "updated_at = now()")

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// 同一时段只允许一个 worker 在重算，避免两个实例交错写。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('usage_hourly_rollup'))`); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf(
		`INSERT INTO usage_hourly (%s)
		 SELECT %s
		 FROM request_logs rl
		 LEFT JOIN channels c ON c.id = rl.channel_id
		 LEFT JOIN provider_accounts pa ON pa.id = c.provider_account_id
		 WHERE rl.created_at >= $1 AND rl.created_at < $2
		 GROUP BY 1, rl.account_id, rl.api_key_id, rl.virtual_model, rl.channel_id, pa.provider_id
		 ON CONFLICT (bucket, account_id, api_key_id, virtual_model, channel_id) DO UPDATE SET %s`,
		strings.Join(cols, ", "), strings.Join(selects, ", "), strings.Join(updates, ", ")), from, to)
	if err != nil {
		return 0, fmt.Errorf("reqlog: rollup usage: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
