// 这些是针对真实 PostgreSQL 的集成测试（钱包的原子性/并发正确性是事务与行锁
// 语义决定的，纯内存 mock 测不出竞态问题）。默认连本机开发库
// postgres://uft:uft@localhost:5432/uft（见 tools/devdb，无需 Docker）。
// 也可用 UFT_TEST_PG_DSN 指定其它实例；连接失败时整批测试自动跳过，
// 保证在没有可用数据库的机器上 `go test ./...` 依然能跑通。
package wallet

import (
	"context"
	"fmt"
	"os"
	"sync"
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

// seedAccount 创建一个全新账户 + 钱包，cashMicro/bonusMicro/creditLimitMicro 单位为微元。
func seedAccount(t *testing.T, pool *pgxpool.Pool, cashMicro, bonusMicro, creditLimitMicro int64) int64 {
	t.Helper()
	ctx := context.Background()

	var accountID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO accounts (type, name, status, tier, credit_limit) VALUES ('personal', $1, 'active', 'free', $2) RETURNING id`,
		fmt.Sprintf("wallet-test-%d", time.Now().UnixNano()), creditLimitMicro,
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO wallets (account_id, cash_balance, bonus_balance) VALUES ($1, $2, $3)`,
		accountID, cashMicro, bonusMicro,
	); err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	return accountID
}

func getWallet(t *testing.T, pool *pgxpool.Pool, accountID int64) (cash, bonus, frozen int64) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT cash_balance, bonus_balance, frozen FROM wallets WHERE account_id = $1`, accountID,
	).Scan(&cash, &bonus, &frozen)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	return
}

func newRequestID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("req-%s-%d", t.Name(), time.Now().UnixNano())
}

func TestReserve_SucceedsWithinBalance(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0) // 1 元

	hold, err := svc.Reserve(context.Background(), newRequestID(t), acct, 300_000, time.Minute)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if hold.Amount != 300_000 || hold.Status != StatusHeld {
		t.Fatalf("unexpected hold: %+v", hold)
	}

	cash, _, frozen := getWallet(t, pool, acct)
	if cash != 1_000_000 {
		t.Errorf("cash_balance = %d, want unchanged 1_000_000 (Reserve must not touch cash)", cash)
	}
	if frozen != 300_000 {
		t.Errorf("frozen = %d, want 300_000", frozen)
	}
}

func TestReserve_RejectsWhenInsufficientBalance(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 100_000, 0, 0) // 0.1 元

	_, err := svc.Reserve(context.Background(), newRequestID(t), acct, 300_000, time.Minute)
	if err != ErrInsufficientBalance {
		t.Fatalf("Reserve error = %v, want ErrInsufficientBalance", err)
	}

	cash, _, frozen := getWallet(t, pool, acct)
	if cash != 100_000 || frozen != 0 {
		t.Errorf("wallet mutated on rejected reserve: cash=%d frozen=%d", cash, frozen)
	}
}

func TestReserve_UsesCreditLimit(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 0, 0, 500_000) // 现金为 0，但有 0.5 元授信额度

	_, err := svc.Reserve(context.Background(), newRequestID(t), acct, 500_000, time.Minute)
	if err != nil {
		t.Fatalf("Reserve should succeed using credit_limit: %v", err)
	}
}

func TestReserve_IsIdempotentPerRequestID(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	h1, err := svc.Reserve(context.Background(), reqID, acct, 400_000, time.Minute)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	h2, err := svc.Reserve(context.Background(), reqID, acct, 400_000, time.Minute)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if h1.Amount != h2.Amount {
		t.Fatalf("idempotent reserve returned different amounts: %d vs %d", h1.Amount, h2.Amount)
	}

	_, _, frozen := getWallet(t, pool, acct)
	if frozen != 400_000 {
		t.Errorf("frozen = %d, want 400_000 (should not double-freeze on retry)", frozen)
	}
}

// TestReserve_ConcurrentRequestsNeverOverdraw 是技术方案 §1.3 A1 描述的核心场景的回归测试：
// 余额只够 1 笔请求的情况下并发发起 50 笔，绝不能全部放行（V1 方案的"先放行后扣费"会在此失败）。
func TestReserve_ConcurrentRequestsNeverOverdraw(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	const balance = 1_000_000  // 1 元
	const perRequest = 600_000 // 每个请求预估 0.6 元，最多只能有 1 笔成功
	acct := seedAccount(t, pool, balance, 0, 0)

	const n = 50
	var wg sync.WaitGroup
	var succeeded int64
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Reserve(context.Background(), fmt.Sprintf("concurrent-%d-%d", time.Now().UnixNano(), i), acct, perRequest, time.Minute)
			errs[i] = err
			if err == nil {
				atomic.AddInt64(&succeeded, 1)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("succeeded reservations = %d, want exactly 1 (balance only covers one)", succeeded)
	}
	for i, err := range errs {
		if err != nil && err != ErrInsufficientBalance {
			t.Errorf("request %d: unexpected error %v", i, err)
		}
	}

	_, _, frozen := getWallet(t, pool, acct)
	if frozen != perRequest {
		t.Errorf("frozen = %d, want %d (must equal exactly the one successful reservation)", frozen, perRequest)
	}
	if frozen > balance {
		t.Fatalf("CRITICAL: frozen (%d) exceeds account balance (%d) — overdraft bug", frozen, balance)
	}
}

func TestSettle_DeductsActualAndReleasesExcessFreeze(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	if _, err := svc.Reserve(context.Background(), reqID, acct, 500_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	receipt, err := svc.Settle(context.Background(), reqID, 300_000) // 实际只花了 0.3 元
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if receipt.ChargedAmount != 300_000 {
		t.Errorf("ChargedAmount = %d, want 300_000", receipt.ChargedAmount)
	}

	cash, _, frozen := getWallet(t, pool, acct)
	if cash != 700_000 { // 1,000,000 - 300,000
		t.Errorf("cash_balance = %d, want 700_000", cash)
	}
	if frozen != 0 { // 500,000 冻结应全部释放，而不是只释放 300,000
		t.Errorf("frozen = %d, want 0 (excess of hold over actual must be released)", frozen)
	}
}

func TestSettle_AllowsActualExceedingHold(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	if _, err := svc.Reserve(context.Background(), reqID, acct, 200_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// 实际用量超过预估冻结（如响应比预期长），技术方案 §7.9.1 允许这种情况发生，
	// 由账户级并发上限兜底控制最坏情况下的透支幅度。
	if _, err := svc.Settle(context.Background(), reqID, 900_000); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	cash, _, frozen := getWallet(t, pool, acct)
	if cash != 100_000 { // 1,000,000 - 900,000
		t.Errorf("cash_balance = %d, want 100_000", cash)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0", frozen)
	}
}

func TestSettle_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	if _, err := svc.Reserve(context.Background(), reqID, acct, 500_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := svc.Settle(context.Background(), reqID, 300_000); err != nil {
		t.Fatalf("first Settle: %v", err)
	}

	// 重复结算（模拟上游重试/网络抖动导致的重复调用）不应再次扣费。
	receipt, err := svc.Settle(context.Background(), reqID, 300_000)
	if err != nil {
		t.Fatalf("second Settle returned error, want nil (idempotent no-op): %v", err)
	}
	if receipt != nil {
		t.Errorf("second Settle receipt = %+v, want nil", receipt)
	}

	cash, _, _ := getWallet(t, pool, acct)
	if cash != 700_000 {
		t.Errorf("cash_balance = %d, want 700_000 (must not be double-charged)", cash)
	}

	var ledgerCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ledger_entries WHERE ref_type='request' AND ref_id=$1 AND type='consume'`, reqID,
	).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("ledger_entries count = %d, want exactly 1", ledgerCount)
	}
}

func TestRelease_UnfreezesFullAmount(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	if _, err := svc.Reserve(context.Background(), reqID, acct, 400_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := svc.Release(context.Background(), reqID); err != nil {
		t.Fatalf("Release: %v", err)
	}

	cash, _, frozen := getWallet(t, pool, acct)
	if cash != 1_000_000 || frozen != 0 {
		t.Errorf("wallet after release: cash=%d frozen=%d, want cash=1_000_000 frozen=0", cash, frozen)
	}
}

func TestRelease_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	if _, err := svc.Reserve(context.Background(), reqID, acct, 400_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := svc.Release(context.Background(), reqID); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := svc.Release(context.Background(), reqID); err != nil {
		t.Fatalf("second Release should be a no-op, got error: %v", err)
	}
}

func TestRelease_AfterSettleReturnsReleasedNotError(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)
	reqID := newRequestID(t)

	if _, err := svc.Reserve(context.Background(), reqID, acct, 400_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := svc.Settle(context.Background(), reqID, 400_000); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// 对已结算的 reservation 调用 Release 应是无害的幂等 no-op，不能把已扣的钱又退回去。
	if err := svc.Release(context.Background(), reqID); err != nil {
		t.Fatalf("Release after Settle should be a no-op, got error: %v", err)
	}

	cash, _, frozen := getWallet(t, pool, acct)
	if cash != 600_000 || frozen != 0 {
		t.Errorf("wallet mutated by Release-after-Settle: cash=%d frozen=%d", cash, frozen)
	}
}

func TestSettleAndRelease_UnknownRequestIDReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)

	if _, err := svc.Settle(context.Background(), "never-reserved", 1); err != ErrReservationNotFound {
		t.Errorf("Settle error = %v, want ErrReservationNotFound", err)
	}
	if err := svc.Release(context.Background(), "never-reserved-either"); err != ErrReservationNotFound {
		t.Errorf("Release error = %v, want ErrReservationNotFound", err)
	}
}
