package chsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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

// memorySink 是测试用的 Sink：把每次收到的批次原样存起来，供断言检查。
type memorySink struct {
	batches [][]UsageRow
}

func (s *memorySink) InsertBatch(ctx context.Context, rows []UsageRow) error {
	s.batches = append(s.batches, append([]UsageRow(nil), rows...))
	return nil
}

func (s *memorySink) allRows() []UsageRow {
	var out []UsageRow
	for _, b := range s.batches {
		out = append(out, b...)
	}
	return out
}

// failingSink 总是失败，用于验证 SyncOnce 在 Sink 写入失败时不会推进游标。
type failingSink struct {
	err error
}

func (s *failingSink) InsertBatch(ctx context.Context, rows []UsageRow) error { return s.err }

var seedCounter atomic.Int64

// seedRequestLog 种一行 request_logs。createdAt 之间必须保证严格递增
// （chsync 的游标是 (created_at, request_id) 元组，用同一时刻的多行做排序
// 断言会让测试结果依赖 request_id 的字典序，不直观），调用方自己控制好间隔。
func seedRequestLog(t *testing.T, pool *pgxpool.Pool, accountID int64, createdAt time.Time, channelID *int64) string {
	t.Helper()
	requestID := fmt.Sprintf("chsync-log-%d-%d", time.Now().UnixNano(), seedCounter.Add(1))
	_, err := pool.Exec(context.Background(),
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, channel_id, endpoint,
		                            is_stream, status, attempts, usage_source, charged_amount)
		 VALUES ($1, $2, $3, 1, 'test-model', $4, 'chat.completions', false, 'success', 1, 'upstream', 100)`,
		requestID, createdAt, accountID, channelID,
	)
	if err != nil {
		t.Fatalf("seed request_log: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM request_logs WHERE request_id = $1`, requestID)
	})
	return requestID
}

func newSyncerName(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("chsync-test-%s-%d", t.Name(), time.Now().UnixNano())
	return name
}

func cleanupSyncerState(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM analytics_sync_state WHERE name = $1`, name)
	})
}

// seedCursorAt 把游标初始化到 at 这个时间点——这是一个长期运行的共享开发库，
// request_logs 里积累了大量历史行，chsync 的游标默认值(年 0001)是"从有史
// 以来"，一个全新的 Syncer 第一次 SyncOnce 会把这些历史行全部当成"新数据"
// 拉出来。测试需要先把游标垫到"本测试自己的 anchor 之前一点"，才能只看到
// 本测试自己种下的行——这跟生产环境首次部署时也需要手工设置初始游标(避免
// 回填全部历史数据)是同一个道理，不是测试专用的作弊。
func seedCursorAt(t *testing.T, pool *pgxpool.Pool, name string, at time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO analytics_sync_state (name, last_created_at, last_request_id) VALUES ($1, $2, '')
		 ON CONFLICT (name) DO UPDATE SET last_created_at = $2, last_request_id = ''`,
		name, at,
	)
	if err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
}

// anchor 把测试用的时间锚点挪到未来——这是一个长期共享的真实开发库，其它包的
// 测试随时在往"现在"附近写 request_logs，chsync 的游标是全局的
// (created_at, request_id) 元组、不按 account_id 过滤，挪到未来才能保证一次
// SyncOnce 只看到本测试自己种下的行。offsetHours 在本文件的用例之间必须互不
// 相同。
func anchor(offsetHours int) time.Time {
	return time.Now().Add(time.Duration(offsetHours) * time.Hour)
}

func TestSyncOnce_PullsRowsInOrderAndAdvancesCursor(t *testing.T) {
	pool := testPool(t)
	name := newSyncerName(t)
	cleanupSyncerState(t, pool, name)
	base := anchor(20)
	seedCursorAt(t, pool, name, base.Add(-time.Minute))
	sink := &memorySink{}
	syncer := New(pool, sink, name)

	accountID := int64(1)
	id1 := seedRequestLog(t, pool, accountID, base, nil)
	id2 := seedRequestLog(t, pool, accountID, base.Add(time.Second), nil)
	id3 := seedRequestLog(t, pool, accountID, base.Add(2*time.Second), nil)

	n, err := syncer.SyncOnce(context.Background(), 10)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if n != 3 {
		t.Fatalf("SyncOnce returned %d, want 3", n)
	}
	got := sink.allRows()
	if len(got) != 3 || got[0].RequestID != id1 || got[1].RequestID != id2 || got[2].RequestID != id3 {
		t.Fatalf("rows out of order or wrong count: %+v", got)
	}

	n, err = syncer.SyncOnce(context.Background(), 10)
	if err != nil {
		t.Fatalf("SyncOnce (second call): %v", err)
	}
	if n != 0 {
		t.Errorf("second SyncOnce returned %d, want 0 (no new rows)", n)
	}
}

func TestSyncOnce_BatchSizeSplitsAcrossMultipleCalls(t *testing.T) {
	pool := testPool(t)
	name := newSyncerName(t)
	cleanupSyncerState(t, pool, name)
	base := anchor(21)
	seedCursorAt(t, pool, name, base.Add(-time.Minute))
	sink := &memorySink{}
	syncer := New(pool, sink, name)

	accountID := int64(2)
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, seedRequestLog(t, pool, accountID, base.Add(time.Duration(i)*time.Second), nil))
	}

	var totalSynced int
	for {
		n, err := syncer.SyncOnce(context.Background(), 2)
		if err != nil {
			t.Fatalf("SyncOnce: %v", err)
		}
		if n == 0 {
			break
		}
		totalSynced += n
	}
	if totalSynced != 5 {
		t.Fatalf("total synced = %d, want 5", totalSynced)
	}
	got := sink.allRows()
	if len(got) != 5 {
		t.Fatalf("sink received %d rows, want 5", len(got))
	}
	for i, row := range got {
		if row.RequestID != ids[i] {
			t.Errorf("row %d = %s, want %s (order must be preserved across batched calls)", i, row.RequestID, ids[i])
		}
	}
}

func TestSyncOnce_SinkFailure_DoesNotAdvanceCursor(t *testing.T) {
	pool := testPool(t)
	name := newSyncerName(t)
	cleanupSyncerState(t, pool, name)
	base := anchor(22)
	seedCursorAt(t, pool, name, base.Add(-time.Minute))

	accountID := int64(3)
	id1 := seedRequestLog(t, pool, accountID, base, nil)

	failing := &failingSink{err: errors.New("clickhouse unreachable")}
	syncer := New(pool, failing, name)
	if _, err := syncer.SyncOnce(context.Background(), 10); err == nil {
		t.Fatal("expected SyncOnce to propagate the sink error")
	}

	// 换一个真的能成功的 sink 重试，游标应该还停在原地，这一行应该被重新拉到。
	sink := &memorySink{}
	syncer2 := New(pool, sink, name)
	n, err := syncer2.SyncOnce(context.Background(), 10)
	if err != nil {
		t.Fatalf("SyncOnce (retry): %v", err)
	}
	if n != 1 {
		t.Fatalf("retry SyncOnce returned %d, want 1 (the row from the failed attempt must not be skipped)", n)
	}
	if got := sink.allRows(); len(got) != 1 || got[0].RequestID != id1 {
		t.Errorf("retry did not re-deliver the same row: %+v", got)
	}
}

func TestSyncOnce_NoNewRows_ReturnsZero(t *testing.T) {
	pool := testPool(t)
	name := newSyncerName(t)
	cleanupSyncerState(t, pool, name)
	// 垫到很远的未来:这是一个长期共享的开发库,request_logs 有大量历史行,
	// 游标默认值(年 0001)会把它们全部当成"新数据"——这里要验证的是"游标之后
	// 确实没有新行"这件事本身,所以要把游标垫到比任何人都可能种的时间更晚。
	seedCursorAt(t, pool, name, anchor(1000))
	sink := &memorySink{}
	syncer := New(pool, sink, name)

	n, err := syncer.SyncOnce(context.Background(), 10)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("SyncOnce = %d, want 0 for a cursor with no rows after it", n)
	}
}

func TestSyncOnce_NullableFieldsSurviveScan(t *testing.T) {
	pool := testPool(t)
	name := newSyncerName(t)
	cleanupSyncerState(t, pool, name)
	base := anchor(23)
	seedCursorAt(t, pool, name, base.Add(-time.Minute))
	sink := &memorySink{}
	syncer := New(pool, sink, name)

	accountID := int64(4)
	// channel_id 留空(nil):request_logs.channel_id 是可空列,鉴权/预检失败但
	// 仍然被记了日志的请求可能没有 channel_id。
	seedRequestLog(t, pool, accountID, base, nil)

	if _, err := syncer.SyncOnce(context.Background(), 10); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	got := sink.allRows()
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].ChannelID != nil {
		t.Errorf("ChannelID = %v, want nil", got[0].ChannelID)
	}
	if got[0].AccountID != accountID {
		t.Errorf("AccountID = %d, want %d", got[0].AccountID, accountID)
	}
}
