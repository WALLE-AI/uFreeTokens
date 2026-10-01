package reqlog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureFuturePartitions 确保 request_logs 从今天起 daysAhead 天内的分区都存在
// （技术方案 §6.8：按天分区）。migrations/00007 在建表时只预先建好了迁移执行当天
// 起 14 天的分区，此后必须有人持续往前补——供 worker 定时调用（建议每天至少跑一次，
// 保留比 daysAhead 更宽的余量，避免因为 worker 短暂故障导致分区断档）。
//
// 分区边界显式写成 UTC（'…+00'）：不带时区的字面量会按数据库会话时区解释，
// 在 TimeZone=Asia/Shanghai 的库上分区会整体偏移 8 小时，按 UTC 日期命名的
// 分区与它实际覆盖的时间对不上。
//
// 与已有分区重叠（例如迁移 00007 按会话时区建的启动分区）时跳过该天：那段时间
// 已经有分区覆盖，缝隙里的数据落进兜底分区（迁移 00021）。兜底分区里如果已经有
// 这一天的数据，先把它们挪进新分区再挂载，否则挂载会失败。
//
// 幂等：重复调用（或多个 worker 实例同时调用）是安全的。
func EnsureFuturePartitions(ctx context.Context, pool *pgxpool.Pool, daysAhead int) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 0; i < daysAhead; i++ {
		day := today.AddDate(0, 0, i)
		if err := ensurePartition(ctx, pool, day); err != nil {
			return err
		}
	}
	return nil
}

func ensurePartition(ctx context.Context, pool *pgxpool.Pool, day time.Time) error {
	next := day.AddDate(0, 0, 1)
	name := fmt.Sprintf("request_logs_%s", day.Format("20060102"))
	table := pgx.Identifier{name}.Sanitize()
	from, to := day.Format("2006-01-02")+" 00:00:00+00", next.Format("2006-01-02")+" 00:00:00+00"

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		return fmt.Errorf("reqlog: check partition %s: %w", name, err)
	}
	if exists {
		return nil
	}
	var inDefault bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('request_logs_default') IS NOT NULL
		   AND EXISTS (SELECT 1 FROM request_logs_default WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz)`,
		from, to).Scan(&inDefault); err != nil {
		return fmt.Errorf("reqlog: check default partition for %s: %w", name, err)
	}

	var err error
	if !inDefault {
		_, err = pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF request_logs FOR VALUES FROM ('%s') TO ('%s')`, table, from, to))
	} else {
		err = moveFromDefaultAndAttach(ctx, pool, table, from, to)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P17" { // 与已有分区重叠：该时间段已被覆盖
		return nil
	}
	if err != nil {
		return fmt.Errorf("reqlog: ensure partition for %s: %w", day.Format("2006-01-02"), err)
	}
	return nil
}

// moveFromDefaultAndAttach 在一个事务里：建一张结构相同的独立表，把兜底分区里
// 属于这个时间段的行挪过去，再把它挂载为正式分区。
func moveFromDefaultAndAttach(ctx context.Context, pool *pgxpool.Pool, table, from, to string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (LIKE request_logs INCLUDING DEFAULTS INCLUDING CONSTRAINTS)`, table)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`WITH moved AS (DELETE FROM request_logs_default WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz RETURNING *)
		 INSERT INTO %s SELECT * FROM moved`, table), from, to); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE request_logs ATTACH PARTITION %s FOR VALUES FROM ('%s') TO ('%s')`, table, from, to)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var partitionUpperBound = regexp.MustCompile(`TO \('([^']+)'\)`)

// DropExpiredPartitions 删除上界早于 now-retention 的日分区，返回删除的分区名。
// request_logs 是计费相关的明细，删除前应确认已归档/同步到分析库；retention<=0
// 时什么也不做（默认不删除，见 cmd/worker 的 UFT_REQLOG_RETENTION_DAYS）。
// 存在兜底分区时 Postgres 不允许 DETACH ... CONCURRENTLY，这里用普通 DETACH
// （短暂持有父表锁，每天一次、在低峰期执行即可），再 DROP。
func DropExpiredPartitions(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) ([]string, error) {
	if retention <= 0 {
		return nil, nil
	}
	cutoff := time.Now().Add(-retention)
	rows, err := pool.Query(ctx,
		`SELECT c.relname, pg_get_expr(c.relpartbound, c.oid)
		 FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		 WHERE i.inhparent = 'request_logs'::regclass AND c.relname <> 'request_logs_default'`)
	if err != nil {
		return nil, fmt.Errorf("reqlog: list partitions: %w", err)
	}
	var expired []string
	for rows.Next() {
		var name, bound string
		if err := rows.Scan(&name, &bound); err != nil {
			rows.Close()
			return nil, err
		}
		m := partitionUpperBound.FindStringSubmatch(bound)
		if m == nil {
			continue
		}
		upper, err := parsePartitionBound(m[1])
		if err != nil {
			continue
		}
		if !upper.After(cutoff) {
			expired = append(expired, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var dropped []string
	for _, name := range expired {
		table := pgx.Identifier{name}.Sanitize()
		if _, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE request_logs DETACH PARTITION %s`, table)); err != nil {
			return dropped, fmt.Errorf("reqlog: detach %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP TABLE %s`, table)); err != nil {
			return dropped, fmt.Errorf("reqlog: drop %s: %w", name, err)
		}
		dropped = append(dropped, name)
	}
	return dropped, nil
}

// parsePartitionBound 解析 pg_get_expr 输出的边界值，如 "2026-10-01 00:00:00+08"。
func parsePartitionBound(v string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05-07", "2006-01-02 15:04:05-07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("reqlog: unrecognized partition bound %q", v)
}

// DefaultPartitionRows 返回兜底分区里的行数（>0 时应告警：分区维护没跟上）。
func DefaultPartitionRows(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `SELECT CASE WHEN to_regclass('request_logs_default') IS NULL THEN 0
		ELSE (SELECT count(*) FROM request_logs_default) END`).Scan(&n)
	return n, err
}
