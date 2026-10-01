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

// TestEnsurePartition_MovesRowsOutOfDefault：超出已建分区范围的行落进兜底分区
// 而不是写入失败；之后补建这一天的分区时，行被挪进新分区。
func TestEnsurePartition_MovesRowsOutOfDefault(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(1, 0, int(time.Now().UnixNano()%300))
	at := day.Add(3 * time.Hour)
	requestID := "default-partition-" + day.Format("20060102") + "-" + time.Now().Format("150405.000000")
	if _, err := pool.Exec(ctx,
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint,
		                            is_stream, status, attempts, usage_source)
		 VALUES ($1, $2, 1, 1, 'm', 'chat.completions', false, 'success', 1, 'upstream')`, requestID, at); err != nil {
		t.Fatalf("insert beyond partitions should land in the default partition: %v", err)
	}
	if err := ensurePartition(ctx, pool, day); err != nil {
		t.Fatalf("ensurePartition: %v", err)
	}
	var where string
	if err := pool.QueryRow(ctx, `SELECT tableoid::regclass::text FROM request_logs WHERE request_id = $1`, requestID).Scan(&where); err != nil {
		t.Fatalf("find row: %v", err)
	}
	if want := "request_logs_" + day.Format("20060102"); where != want {
		t.Errorf("row lives in %s, want %s", where, want)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS request_logs_`+day.Format("20060102"))
	})
}

func TestDropExpiredPartitions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := ensurePartition(ctx, pool, old); err != nil {
		t.Fatalf("ensurePartition: %v", err)
	}
	if dropped, err := DropExpiredPartitions(ctx, pool, 0); err != nil || len(dropped) != 0 {
		t.Fatalf("retention<=0 must be a no-op, got %v %v", dropped, err)
	}
	dropped, err := DropExpiredPartitions(ctx, pool, 20*365*24*time.Hour)
	if err != nil {
		t.Fatalf("DropExpiredPartitions: %v", err)
	}
	found := false
	for _, n := range dropped {
		found = found || n == "request_logs_20010101"
	}
	if !found {
		t.Errorf("dropped = %v, want request_logs_20010101", dropped)
	}
}
