package reqlog

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureFuturePartitions 确保 request_logs 从今天起 daysAhead 天内的分区都存在
// （技术方案 §6.8：按天分区）。migrations/00007 在建表时只预先建好了迁移执行当天
// 起 14 天的分区，此后必须有人持续往前补，否则插入超出已建分区范围的日期会直接
// 报错——这正是这个函数存在的原因，供 worker 定时调用（建议每天至少跑一次，
// 保留比 daysAhead 更宽的余量，避免因为 worker 短暂故障导致分区断档）。
//
// 用 CREATE TABLE IF NOT EXISTS 是幂等的，重复调用（或多个 worker 实例同时调用）
// 是安全的。
func EnsureFuturePartitions(ctx context.Context, pool *pgxpool.Pool, daysAhead int) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 0; i < daysAhead; i++ {
		day := today.AddDate(0, 0, i)
		next := day.AddDate(0, 0, 1)
		tableName := pgx.Identifier{fmt.Sprintf("request_logs_%s", day.Format("20060102"))}.Sanitize()

		stmt := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF request_logs FOR VALUES FROM ('%s') TO ('%s')`,
			tableName, day.Format("2006-01-02"), next.Format("2006-01-02"),
		)
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("reqlog: ensure partition for %s: %w", day.Format("2006-01-02"), err)
		}
	}
	return nil
}
