// 集成测试：连真实 PostgreSQL（tools/devdb），和本仓库其它包的测试风格一致；连不上时跳过。
package datasync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// newSource 插入一个只属于本测试的来源（fetcher 名唯一，不会被其它来源的 Job 接手）。
func newSource(t *testing.T, pool *pgxpool.Pool, schedule string) (id int64, fetcher string) {
	t.Helper()
	fetcher = fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO price_sources (domain, name, level, kind, fetcher, schedule, enabled, next_run_at, config)
		 VALUES ('benchmark', $1, 'L4', 'dataset', $1, $2, true, now() - interval '1 minute', '{"k": 1}') RETURNING id`,
		fetcher, schedule).Scan(&id); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM price_sources WHERE id = $1`, id)
	})
	return id, fetcher
}

type sourceState struct {
	nextRunAt   *time.Time
	failures    int
	lastError   *string
	lastSuccess *time.Time
	hash        []byte
}

func loadState(t *testing.T, pool *pgxpool.Pool, id int64) sourceState {
	t.Helper()
	var s sourceState
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at, consecutive_failures, last_error, last_success_at, last_content_hash FROM price_sources WHERE id = $1`, id,
	).Scan(&s.nextRunAt, &s.failures, &s.lastError, &s.lastSuccess, &s.hash); err != nil {
		t.Fatalf("load source: %v", err)
	}
	return s
}

func lastRun(t *testing.T, pool *pgxpool.Pool, id int64) (status string, fetched int, errText *string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(items_fetched, 0), error FROM data_source_runs WHERE source_id = $1 ORDER BY id DESC LIMIT 1`, id,
	).Scan(&status, &fetched, &errText); err != nil {
		t.Fatalf("load run: %v", err)
	}
	return
}

func TestScheduler_RunSuccessReschedules(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id, fetcher := newSource(t, pool, "@every 2h")
	var calls atomic.Int32
	reg := Registry{fetcher: JobFunc(func(ctx context.Context, _ *Env, src Source) (Result, error) {
		calls.Add(1)
		if src.Config["k"] != float64(1) {
			t.Errorf("config not passed through: %v", src.Config)
		}
		return Result{ItemsFetched: 7, ItemsChanged: 2, ContentHash: []byte{1, 2, 3}}, nil
	})}
	s := NewScheduler(pool, reg, NewEnv(EnvOptions{MinHostInterval: -1}), nil, NewMetrics(nil))

	ran, err := s.RunSource(ctx, id)
	if !ran || err != nil {
		t.Fatalf("RunSource: ran=%v err=%v", ran, err)
	}
	st := loadState(t, pool, id)
	if st.failures != 0 || st.lastSuccess == nil || string(st.hash) != "\x01\x02\x03" {
		t.Errorf("unexpected state after success: %+v", st)
	}
	if st.nextRunAt == nil || time.Until(*st.nextRunAt) < 110*time.Minute {
		t.Errorf("next_run_at should be ~2h later, got %v", st.nextRunAt)
	}
	if status, fetched, _ := lastRun(t, pool, id); status != StatusOK || fetched != 7 {
		t.Errorf("run = %s/%d, want ok/7", status, fetched)
	}
	// 已经不到期：再跑一次应当跳过。
	if ran, _ := s.RunSource(ctx, id); ran {
		t.Error("source not due anymore, RunSource should skip")
	}
	if calls.Load() != 1 {
		t.Errorf("job called %d times, want 1", calls.Load())
	}
}

func TestScheduler_FailureBackoffAndRejected(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id, fetcher := newSource(t, pool, "@every 24h")
	var reject atomic.Bool
	reg := Registry{fetcher: JobFunc(func(context.Context, *Env, Source) (Result, error) {
		if reject.Load() {
			return Result{}, Rejectf("only %d rows", 1)
		}
		return Result{}, errors.New("boom")
	})}
	s := NewScheduler(pool, reg, NewEnv(EnvOptions{MinHostInterval: -1}), nil, nil)

	if ran, err := s.RunSource(ctx, id); !ran || err == nil {
		t.Fatalf("expected a failed run, got ran=%v err=%v", ran, err)
	}
	st := loadState(t, pool, id)
	if st.failures != 1 || st.lastError == nil || *st.lastError != "boom" {
		t.Errorf("unexpected state after failure: %+v", st)
	}
	// 第一次失败退避 5 分钟，早于 24 小时的正常调度。
	if st.nextRunAt == nil || time.Until(*st.nextRunAt) > 6*time.Minute || time.Until(*st.nextRunAt) < 4*time.Minute {
		t.Errorf("next_run_at should back off ~5m, got %v", st.nextRunAt)
	}
	if status, _, _ := lastRun(t, pool, id); status != StatusFailed {
		t.Errorf("status = %s, want failed", status)
	}

	reject.Store(true)
	if _, err := pool.Exec(ctx, `UPDATE price_sources SET next_run_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunSource(ctx, id); !errors.Is(err, ErrRejected) {
		t.Fatalf("expected ErrRejected, got %v", err)
	}
	if status, _, _ := lastRun(t, pool, id); status != StatusRejected {
		t.Errorf("status = %s, want rejected", status)
	}
	if st := loadState(t, pool, id); st.failures != 2 {
		t.Errorf("failures = %d, want 2", st.failures)
	}
}

func TestScheduler_UnknownFetcherAndLock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id, _ := newSource(t, pool, "")
	s := NewScheduler(pool, Registry{}, NewEnv(EnvOptions{MinHostInterval: -1}), nil, nil)

	// 另一个"副本"持有锁时跳过。
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('datasync'), $1::int)`, id); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.RunSource(ctx, id); ran || err != nil {
		t.Errorf("locked source: ran=%v err=%v, want skipped", ran, err)
	}
	_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('datasync'), $1::int)`, id)
	conn.Release()

	if ran, err := s.RunSource(ctx, id); !ran || err == nil {
		t.Fatalf("unknown fetcher should fail: ran=%v err=%v", ran, err)
	}
}

func TestEnv_ConditionalGetAndPrivateNetworks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent {
			t.Errorf("missing bot user agent: %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	ctx := context.Background()

	env := NewEnv(EnvOptions{AllowPrivateNetworks: true, MinHostInterval: -1})
	resp, err := env.Get(ctx, srv.URL, GetOptions{})
	if err != nil || string(resp.Body) != "hello" || resp.ETag != `"v1"` || len(resp.Hash) != 32 {
		t.Fatalf("first GET: %+v, %v", resp, err)
	}
	resp, err = env.Get(ctx, srv.URL, GetOptions{ETag: resp.ETag})
	if err != nil || !resp.NotModified {
		t.Fatalf("conditional GET should be 304: %+v, %v", resp, err)
	}
	if _, err := env.Get(ctx, srv.URL, GetOptions{MaxBytes: 2}); err == nil {
		t.Error("expected size limit error")
	}

	strict := NewEnv(EnvOptions{MinHostInterval: -1})
	if _, err := strict.Get(ctx, srv.URL, GetOptions{}); err == nil || !errors.Is(err, errUnsafeAddr) {
		t.Errorf("loopback must be refused by default, got %v", err)
	}
	if _, err := strict.Get(ctx, "file:///etc/passwd", GetOptions{}); err == nil {
		t.Error("non-http scheme must be refused")
	}
}

func TestEnv_HostRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	env := NewEnv(EnvOptions{AllowPrivateNetworks: true, MinHostInterval: 150 * time.Millisecond})
	start := time.Now()
	for range 3 {
		if _, err := env.Get(context.Background(), srv.URL, GetOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("3 requests took %v, want >= 300ms with a 150ms host interval", d)
	}
}
