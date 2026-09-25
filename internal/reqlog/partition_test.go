package reqlog

import (
	"context"
	"testing"
	"time"
)

func TestEnsureFuturePartitions_CreatesExpectedTables(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if err := EnsureFuturePartitions(ctx, pool, 3); err != nil {
		t.Fatalf("EnsureFuturePartitions: %v", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 0; i < 3; i++ {
		day := today.AddDate(0, 0, i)
		tableName := "request_logs_" + day.Format("20060102")

		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, tableName,
		).Scan(&exists); err != nil {
			t.Fatalf("check table existence for %s: %v", tableName, err)
		}
		if !exists {
			t.Errorf("expected partition table %q to exist, it does not", tableName)
		}
	}
}

func TestEnsureFuturePartitions_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if err := EnsureFuturePartitions(ctx, pool, 2); err != nil {
		t.Fatalf("first EnsureFuturePartitions: %v", err)
	}
	// 重复调用（模拟多个 worker 实例、或同一实例的下一次定时任务）不应该报错。
	if err := EnsureFuturePartitions(ctx, pool, 2); err != nil {
		t.Fatalf("second EnsureFuturePartitions: %v", err)
	}
}

func TestEnsureFuturePartitions_AllowsInsertOnFarFutureDate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// 迁移只建了执行当天起 14 天的分区；这里往前多建几天，验证一个原本会因为
	// "没有匹配的分区"而报错的插入，在调用之后能成功。
	const daysAhead = 20
	if err := EnsureFuturePartitions(ctx, pool, daysAhead); err != nil {
		t.Fatalf("EnsureFuturePartitions: %v", err)
	}

	farFuture := time.Now().UTC().AddDate(0, 0, daysAhead-1)
	requestID := "partition-test-" + farFuture.Format("20060102150405")
	_, err := pool.Exec(ctx,
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint,
		                            is_stream, status, attempts, usage_source)
		 VALUES ($1, $2, 1, 1, 'm', 'chat.completions', false, 'success', 1, 'upstream')`,
		requestID, farFuture,
	)
	if err != nil {
		t.Fatalf("insert into far-future partition failed (partition not created?): %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM request_logs WHERE request_id = $1`, requestID)
	})
}
