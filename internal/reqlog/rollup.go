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

// SpeedBucketBounds 是输出吞吐直方图各桶的上界（tok/s，迁移 00028）；最后一个桶是 +inf。
// usage_hourly / public_model_usage_daily 的 spd_b0..spd_b13 与之一一对应。
var SpeedBucketBounds = []int64{10, 20, 30, 40, 50, 60, 80, 100, 125, 150, 200, 300, 500}

// HistogramColumns 返回 prefix_b0..prefix_bN 列名（延迟与吞吐直方图的桶数相同）。
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

const (
	speedCond = "rl.status = 'success' AND rl.is_stream AND rl.gen_ms > 0"
	toolCond  = "rl.status = 'success' AND rl.tool_calls > 0"
	imageCond = "rl.status = 'success' AND rl.image_inputs > 0"
)

// speedHistogramExprs 生成按请求吞吐（输出 token ÷ 生成秒数）分桶计数的聚合表达式。
func speedHistogramExprs() []string {
	tps := "(rl.output_tokens * 1000.0 / rl.gen_ms)"
	out := make([]string, 0, len(SpeedBucketBounds)+1)
	lower := "-1"
	for _, b := range SpeedBucketBounds {
		out = append(out, fmt.Sprintf("count(*) FILTER (WHERE %s AND %s > %s AND %s <= %d)", speedCond, tps, lower, tps, b))
		lower = fmt.Sprint(b)
	}
	return append(out, fmt.Sprintf("count(*) FILTER (WHERE %s AND %s > %s)", speedCond, tps, lower))
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
	lat, ttft, spd := HistogramColumns("lat"), HistogramColumns("ttft"), HistogramColumns("spd")
	cols := append([]string{"bucket", "account_id", "api_key_id", "virtual_model", "virtual_model_id", "channel_id", "provider_id",
		"requests", "success", "estimated", "input_tokens", "output_tokens", "cache_read_tokens", "reasoning_tokens",
		"charged_micro", "list_micro", "cost_micro",
		"speed_requests", "speed_output_tokens", "speed_gen_ms", "tool_requests", "tool_tokens", "image_requests", "image_tokens"}, append(append(lat, ttft...), spd...)...)
	selects := append([]string{
		"date_trunc('hour', rl.created_at) AS bucket", "rl.account_id", "rl.api_key_id", "rl.virtual_model",
		"max(COALESCE(rl.virtual_model_id, (SELECT vm.id FROM virtual_models vm WHERE vm.name = rl.virtual_model)))", "rl.channel_id", "pa.provider_id",
		"count(*)", "count(*) FILTER (WHERE rl.status = 'success')", "count(*) FILTER (WHERE rl.usage_source = 'estimated')",
		"COALESCE(sum(rl.input_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.output_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.cache_read_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.reasoning_tokens) FILTER (WHERE rl.status = 'success'), 0)",
		"COALESCE(sum(rl.charged_amount), 0)", "COALESCE(sum(rl.list_amount), 0)", "COALESCE(sum(rl.cost_amount), 0)",
		// 公开排行榜的速度 / 工具调用 / 多模态口径（迁移 00027）：速度只看成功的流式请求。
		"count(*) FILTER (WHERE " + speedCond + ")",
		"COALESCE(sum(rl.output_tokens) FILTER (WHERE " + speedCond + "), 0)",
		"COALESCE(sum(rl.gen_ms) FILTER (WHERE " + speedCond + "), 0)",
		"count(*) FILTER (WHERE " + toolCond + ")",
		"COALESCE(sum(COALESCE(rl.input_tokens, 0) + COALESCE(rl.output_tokens, 0)) FILTER (WHERE " + toolCond + "), 0)",
		"count(*) FILTER (WHERE " + imageCond + ")",
		"COALESCE(sum(COALESCE(rl.input_tokens, 0) + COALESCE(rl.output_tokens, 0)) FILTER (WHERE " + imageCond + "), 0)",
	}, append(append(histogramExprs("latency_ms", "rl.status = 'success'"), histogramExprs("ttft_ms", "rl.status = 'success' AND rl.is_stream")...),
		speedHistogramExprs()...)...)
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
