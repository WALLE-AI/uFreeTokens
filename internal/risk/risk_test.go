package risk

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedCounter 保证同一个测试内快速连续调用 seedSuccessLog 也能拿到互不相同的
// request_id——Windows 上 time.Now().UnixNano() 的实际分辨率比 1ns 粗得多，
// 紧凑的循环里连续几次调用经常拿到完全相同的时间戳，撞上 request_logs 的
// (request_id, created_at) 主键唯一约束。
var seedCounter atomic.Int64

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

func seedAccount(t *testing.T, pool *pgxpool.Pool, tier string) int64 {
	t.Helper()
	ctx := context.Background()
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (type, name, status, tier) VALUES ('personal', $1, 'active', $2) RETURNING id`,
		fmt.Sprintf("risk-test-%d", time.Now().UnixNano()), tier,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM admin_audit_logs WHERE target_type = 'account' AND target_id = $1`, fmt.Sprintf("%d", accountID))
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	return accountID
}

func seedSuccessLog(t *testing.T, pool *pgxpool.Pool, accountID int64, createdAt time.Time, charged int64, clientIP string) {
	t.Helper()
	requestID := fmt.Sprintf("risk-log-%d-%d", time.Now().UnixNano(), seedCounter.Add(1))
	_, err := pool.Exec(context.Background(),
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint,
		                            is_stream, status, attempts, usage_source, charged_amount, client_ip)
		 VALUES ($1, $2, $3, 1, 'm', 'chat.completions', false, 'success', 1, 'upstream', $4, $5)`,
		requestID, createdAt, accountID, charged, clientIP,
	)
	if err != nil {
		t.Fatalf("seed request_log: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM request_logs WHERE request_id = $1`, requestID)
	})
}

func accountTier(t *testing.T, pool *pgxpool.Pool, accountID int64) string {
	t.Helper()
	var tier string
	if err := pool.QueryRow(context.Background(), `SELECT tier FROM accounts WHERE id = $1`, accountID).Scan(&tier); err != nil {
		t.Fatalf("query tier: %v", err)
	}
	return tier
}

func findFinding(findings []Finding, rule string, accountID int64) *Finding {
	for i := range findings {
		if findings[i].Rule == rule && findings[i].AccountID == accountID {
			return &findings[i]
		}
	}
	return nil
}

// anchor 和 internal/reconcile 里的 futureAnchor 同一个道理：这是一个共享的
// 真实开发库，其它包的测试随时在往"现在"附近写 request_logs（真实的
// Reserve/Settle 流程），把检测窗口挪到未来，结构上就不会被撞车。offsetHours
// 在本文件的用例之间必须互不相同（窗口宽度和基线窗口都以它为锚点，相邻用例
// 共享同一个 offsetHours 会导致基线窗口和别的用例的当前窗口重叠）。
func anchor(offsetHours int) time.Time {
	return time.Now().Add(time.Duration(offsetHours) * time.Hour)
}

func TestCheckConsumptionSpike_DetectsAndDowngradesTier(t *testing.T) {
	pool := testPool(t)
	now := anchor(10)
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 3, SpikeRatio: 5.0, MinIPFanout: 1000}
	e := New(pool, th)

	accountID := seedAccount(t, pool, "pro")
	baselineStart := now.Add(-th.Window).Add(-th.BaselineWindow)
	// 基线窗口：过去 1 小时内均匀花了 600(转换到 1 分钟窗口期望值约为 10)。
	seedSuccessLog(t, pool, accountID, baselineStart.Add(time.Minute), 600, "203.0.113.1")
	// 当前窗口：1 分钟内花了 1000，远超基线期望值的 5 倍阈值。
	for i := 0; i < 3; i++ {
		seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 1000, "203.0.113.1")
	}

	findings, err := e.Scan(context.Background(), now)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	f := findFinding(findings, "consumption_spike", accountID)
	if f == nil {
		t.Fatal("expected a consumption_spike finding")
	}
	if f.Action != ActionDowngradeTier || !f.Applied {
		t.Errorf("finding = %+v, want Action=downgrade_tier Applied=true", f)
	}
	if got := accountTier(t, pool, accountID); got != "free" {
		t.Errorf("account tier = %q, want free", got)
	}
}

func TestCheckConsumptionSpike_NoBaseline_NotFlagged(t *testing.T) {
	pool := testPool(t)
	now := anchor(11)
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 3, SpikeRatio: 5.0, MinIPFanout: 1000}
	e := New(pool, th)

	accountID := seedAccount(t, pool, "pro")
	// 没有任何基线窗口内的历史数据，只有当前窗口一笔很大的消费。
	for i := 0; i < 5; i++ {
		seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 100_000, "203.0.113.2")
	}

	findings, err := e.Scan(context.Background(), now)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if f := findFinding(findings, "consumption_spike", accountID); f != nil {
		t.Errorf("unexpected consumption_spike finding without a baseline: %+v", f)
	}
	if got := accountTier(t, pool, accountID); got != "pro" {
		t.Errorf("account tier = %q, want unchanged pro (no action should have been taken)", got)
	}
}

func TestCheckConsumptionSpike_BelowMinSamples_NotFlagged(t *testing.T) {
	pool := testPool(t)
	now := anchor(12)
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 10, SpikeRatio: 2.0, MinIPFanout: 1000}
	e := New(pool, th)

	accountID := seedAccount(t, pool, "pro")
	baselineStart := now.Add(-th.Window).Add(-th.BaselineWindow)
	seedSuccessLog(t, pool, accountID, baselineStart.Add(time.Minute), 100, "203.0.113.3")
	// 当前窗口只有 1 个请求(远低于 MinSamples=10)，即便比率很高也不该被判定。
	seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 100_000, "203.0.113.3")

	findings, err := e.Scan(context.Background(), now)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if f := findFinding(findings, "consumption_spike", accountID); f != nil {
		t.Errorf("unexpected consumption_spike finding below MinSamples: %+v", f)
	}
}

func TestCheckConsumptionSpike_AlreadyFreeTier_NotApplied(t *testing.T) {
	pool := testPool(t)
	now := anchor(13)
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 3, SpikeRatio: 5.0, MinIPFanout: 1000}
	e := New(pool, th)

	accountID := seedAccount(t, pool, "free")
	baselineStart := now.Add(-th.Window).Add(-th.BaselineWindow)
	seedSuccessLog(t, pool, accountID, baselineStart.Add(time.Minute), 600, "203.0.113.4")
	for i := 0; i < 3; i++ {
		seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 1000, "203.0.113.4")
	}

	findings, err := e.Scan(context.Background(), now)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	f := findFinding(findings, "consumption_spike", accountID)
	if f == nil {
		t.Fatal("expected a consumption_spike finding even though no downgrade was applied")
	}
	if f.Applied {
		t.Error("Applied = true, want false (account is already on the free tier)")
	}
}

func TestCheckSharedIPFanout_DetectsButDoesNotMutateAccounts(t *testing.T) {
	pool := testPool(t)
	now := anchor(14)
	sharedIP := "198.51.100.42"
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 1000, SpikeRatio: 1000, MinIPFanout: 3}
	e := New(pool, th)

	var accountIDs []int64
	for i := 0; i < 3; i++ {
		accountID := seedAccount(t, pool, "pro")
		seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 10, sharedIP)
		accountIDs = append(accountIDs, accountID)
	}

	findings, err := e.Scan(context.Background(), now)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var f *Finding
	for i := range findings {
		if findings[i].Rule == "shared_ip_fanout" && findings[i].IP == sharedIP {
			f = &findings[i]
		}
	}
	if f == nil {
		t.Fatal("expected a shared_ip_fanout finding")
	}
	if f.Action != ActionNotifyOperator || f.Applied {
		t.Errorf("finding = %+v, want Action=notify_operator Applied=false", f)
	}
	for _, accountID := range accountIDs {
		if got := accountTier(t, pool, accountID); got != "pro" {
			t.Errorf("account %d tier = %q, want unchanged pro (shared_ip_fanout must never mutate accounts)", accountID, got)
		}
	}
}

func TestCheckSharedIPFanout_BelowThreshold_NotFlagged(t *testing.T) {
	pool := testPool(t)
	now := anchor(15)
	sharedIP := "198.51.100.99"
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 1000, SpikeRatio: 1000, MinIPFanout: 5}
	e := New(pool, th)

	for i := 0; i < 2; i++ {
		accountID := seedAccount(t, pool, "pro")
		seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 10, sharedIP)
	}

	findings, err := e.Scan(context.Background(), now)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for i := range findings {
		if findings[i].Rule == "shared_ip_fanout" && findings[i].IP == sharedIP {
			t.Errorf("unexpected shared_ip_fanout finding below MinIPFanout: %+v", findings[i])
		}
	}
}

func TestScan_RecordsAuditLogOnDowngrade(t *testing.T) {
	pool := testPool(t)
	now := anchor(16)
	th := Thresholds{Window: time.Minute, BaselineWindow: time.Hour, MinSamples: 3, SpikeRatio: 5.0, MinIPFanout: 1000}
	e := New(pool, th)

	accountID := seedAccount(t, pool, "enterprise")
	baselineStart := now.Add(-th.Window).Add(-th.BaselineWindow)
	seedSuccessLog(t, pool, accountID, baselineStart.Add(time.Minute), 600, "203.0.113.5")
	for i := 0; i < 3; i++ {
		seedSuccessLog(t, pool, accountID, now.Add(-10*time.Second), 1000, "203.0.113.5")
	}

	if _, err := e.Scan(context.Background(), now); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM admin_audit_logs WHERE action = 'risk.auto_downgrade_tier' AND target_type = 'account' AND target_id = $1`,
		fmt.Sprintf("%d", accountID),
	).Scan(&count); err != nil {
		t.Fatalf("query admin_audit_logs: %v", err)
	}
	if count != 1 {
		t.Errorf("admin_audit_logs rows for this downgrade = %d, want 1", count)
	}
}
