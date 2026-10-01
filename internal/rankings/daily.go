// Package rankings 是公开排行榜的数据层（docs/基准测试与排行榜数据服务技术方案.md
// 阶段 1）：worker 把 usage_hourly 物化成按 Asia/Shanghai 切日的
// public_model_usage_daily / public_model_account_daily（RefreshDaily），网关的
// 免鉴权接口 GET /v1/rankings/* 只读这两张小表（Service）。
//
// 公开口径（与 web 端"榜单统计方法与规则"文案一致）：
//   - 只统计成功请求的 input + output token；output_tokens 取自上游
//     completion_tokens，已经包含 reasoning token，不再重复相加；
//   - 标记了 accounts.exclude_from_public_stats 的账户（内部测试、压测、评测）不计入；
//   - 统计期内独立账户数 < MinDistinctAccounts 的模型不单独上榜，归入"其他"；
//   - 单个账户在某模型上的计入量不超过该模型当期总量的 MaxAccountShare（防自刷），
//     超出部分不计入排名，原始数据不删；
//   - 只展示对 free 等级可见的模型（与 GET /v1/catalog 一致），其余归入"其他"。
package rankings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
)

// Location 是公开榜单切日的时区。usage_hourly 按 UTC 整点分桶，东八区没有半小时
// 偏移，按天切分不损失精度。
var Location = mustLoad("Asia/Shanghai")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// 极简容器镜像可能没有 tzdata；固定 +08:00 与 Asia/Shanghai 等价（无夏令时）。
		return time.FixedZone(name, 8*3600)
	}
	return loc
}

// Day 把时间截到 Location 下的当天 0 点。
func Day(t time.Time) time.Time {
	y, m, d := t.In(Location).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, Location)
}

// modelKeyExpr 是物化表的模型归并键：有 virtual_model_id 时按 ID 归并（模型改名后
// 趋势线不断开），历史上没记 ID 的行回落到名称。
const modelKeyExpr = `CASE WHEN u.virtual_model_id IS NOT NULL THEN 'id:' || u.virtual_model_id ELSE 'name:' || u.virtual_model END`

// authorExpr 与 catalog.AuthorOf 同一规则（'/' 之前的部分），由 TestAuthorExprMatchesGo 保证一致。
const authorExpr = `CASE WHEN strpos(%[1]s, '/') > 1 THEN split_part(%[1]s, '/', 1) ELSE %[1]s END`

// RefreshDaily 重算 [from, to] 这些自然日（Location 时区，含两端）的物化数据：先删后插，
// 在一个事务里完成，可以对同一天反复执行（迟到的汇总、账户排除标记的变更都会被反映）。
// 返回写入的模型日行数。
func RefreshDaily(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (int64, error) {
	fromDay, toDay := Day(from), Day(to)
	if toDay.Before(fromDay) {
		return 0, nil
	}
	start, end := fromDay, toDay.AddDate(0, 0, 1)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('public_usage_daily'))`); err != nil {
		return 0, err
	}
	fromDate, toDate := fromDay.Format(time.DateOnly), toDay.Format(time.DateOnly)
	if _, err := tx.Exec(ctx, `DELETE FROM public_model_usage_daily WHERE day BETWEEN $1 AND $2`, fromDate, toDate); err != nil {
		return 0, fmt.Errorf("rankings: clear model daily: %w", err)
	}
	for _, table := range []string{"public_model_account_daily", "public_app_usage_daily", "public_app_account_daily"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE day BETWEEN $1 AND $2`, fromDate, toDate); err != nil {
			return 0, fmt.Errorf("rankings: clear %s: %w", table, err)
		}
	}

	spd := reqlog.HistogramColumns("spd")
	spdCols := strings.Join(spd, ", ")
	sums := make([]string, len(spd))
	for i, c := range spd {
		sums[i] = fmt.Sprintf("sum(u.%s) AS %s", c, c)
	}
	spdSums := strings.Join(sums, ", ")
	src := `FROM usage_hourly u
		JOIN accounts a ON a.id = u.account_id AND NOT a.exclude_from_public_stats
		WHERE u.bucket >= $1 AND u.bucket < $2`
	tag, err := tx.Exec(ctx, `
		INSERT INTO public_model_usage_daily (day, model_key, virtual_model, virtual_model_id, author,
			requests, success, input_tokens, output_tokens, reasoning_tokens, distinct_accounts,
			speed_requests, speed_output_tokens, speed_gen_ms, tool_requests, tool_tokens, image_requests, image_tokens, `+spdCols+`)
		SELECT day, model_key, name, vm_id, `+fmt.Sprintf(authorExpr, "name")+`,
			requests, success, input_tokens, output_tokens, reasoning_tokens, distinct_accounts,
			speed_requests, speed_output_tokens, speed_gen_ms, tool_requests, tool_tokens, image_requests, image_tokens, `+spdCols+`
		FROM (
			SELECT (u.bucket AT TIME ZONE '`+Location.String()+`')::date AS day, `+modelKeyExpr+` AS model_key,
				(array_agg(u.virtual_model ORDER BY u.bucket DESC))[1] AS name, max(u.virtual_model_id) AS vm_id,
				sum(u.requests) AS requests, sum(u.success) AS success,
				sum(u.input_tokens) AS input_tokens, sum(u.output_tokens) AS output_tokens, sum(u.reasoning_tokens) AS reasoning_tokens,
				count(DISTINCT u.account_id) FILTER (WHERE u.input_tokens + u.output_tokens > 0) AS distinct_accounts,
				sum(u.speed_requests) AS speed_requests, sum(u.speed_output_tokens) AS speed_output_tokens,
				sum(u.speed_gen_ms) AS speed_gen_ms, sum(u.tool_requests) AS tool_requests, sum(u.tool_tokens) AS tool_tokens,
				sum(u.image_requests) AS image_requests, sum(u.image_tokens) AS image_tokens, `+spdSums+`
			`+src+`
			GROUP BY 1, 2
		) d`, start, end)
	if err != nil {
		return 0, fmt.Errorf("rankings: insert model daily: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO public_model_account_daily (day, model_key, account_id, tokens, tool_tokens, image_tokens)
		SELECT (u.bucket AT TIME ZONE '`+Location.String()+`')::date, `+modelKeyExpr+`, u.account_id,
			sum(u.input_tokens + u.output_tokens), sum(u.tool_tokens), sum(u.image_tokens)
		`+src+`
		GROUP BY 1, 2, 3
		HAVING sum(u.input_tokens + u.output_tokens) > 0`, start, end); err != nil {
		return 0, fmt.Errorf("rankings: insert account daily: %w", err)
	}
	if err := refreshApps(ctx, tx, start, end); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// appKeyExpr：有 app_url 时按域名归并（同一域名下不同的 X-Title 视为同一应用），否则按小写应用名。
const appKeyExpr = `CASE WHEN rl.app_url IS NOT NULL THEN 'url:' || rl.app_url ELSE 'name:' || lower(rl.app_name) END`

// refreshApps 物化应用榜的日表。usage_hourly 没有应用维度（加上会让汇总表行数随应用数膨胀），
// 这里直接读 request_logs 里声明了 X-Title 的成功请求（部分索引 idx_request_logs_app_time）。
func refreshApps(ctx context.Context, tx pgx.Tx, start, end time.Time) error {
	src := `FROM request_logs rl
		JOIN accounts a ON a.id = rl.account_id AND NOT a.exclude_from_public_stats
		WHERE rl.created_at >= $1 AND rl.created_at < $2 AND rl.app_name IS NOT NULL AND rl.status = 'success'`
	day := `(rl.created_at AT TIME ZONE '` + Location.String() + `')::date`
	tokens := `COALESCE(rl.input_tokens, 0) + COALESCE(rl.output_tokens, 0)`
	if _, err := tx.Exec(ctx, `
		INSERT INTO public_app_usage_daily (day, app_key, app_name, app_url, requests, tokens, distinct_accounts)
		SELECT `+day+`, `+appKeyExpr+`, mode() WITHIN GROUP (ORDER BY rl.app_name), COALESCE(max(rl.app_url), ''),
			count(*), sum(`+tokens+`), count(DISTINCT rl.account_id)
		`+src+`
		GROUP BY 1, 2`, start, end); err != nil {
		return fmt.Errorf("rankings: insert app daily: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO public_app_account_daily (day, app_key, account_id, tokens)
		SELECT `+day+`, `+appKeyExpr+`, rl.account_id, sum(`+tokens+`)
		`+src+`
		GROUP BY 1, 2, 3
		HAVING sum(`+tokens+`) > 0`, start, end); err != nil {
		return fmt.Errorf("rankings: insert app account daily: %w", err)
	}
	return nil
}
