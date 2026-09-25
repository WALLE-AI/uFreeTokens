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

// seedAccountOnly 只创建账户，不建钱包——用来测试 CreateWallet 本身。
func seedAccountOnly(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var accountID int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO accounts (type, name, status, tier) VALUES ('personal', $1, 'active', 'free') RETURNING id`,
		fmt.Sprintf("wallet-test-noaccount-%d", time.Now().UnixNano()),
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("seed account: %v", err)
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

func TestReclaimExpired_ReleasesOnlyExpiredHeldReservations(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)

	expiredID := newRequestID(t)
	if _, err := svc.Reserve(context.Background(), expiredID, acct, 300_000, 10*time.Millisecond); err != nil {
		t.Fatalf("Reserve (expired): %v", err)
	}
	stillLiveID := newRequestID(t)
	if _, err := svc.Reserve(context.Background(), stillLiveID, acct, 200_000, time.Hour); err != nil {
		t.Fatalf("Reserve (still live): %v", err)
	}

	time.Sleep(50 * time.Millisecond) // 等第一条真正过期

	// 不断言 ReclaimExpired 的返回值等于某个精确的全局数字：这是一个跑很久的开发库，
	// 其它测试（尤其是只测 Reserve、不调用 Settle/Release 的用例）会在库里留下别的
	// held 记录，ReclaimExpired 按设计会把它们也一起扫出来——这是对的行为，只是
	// 会让"全局恰好回收 N 条"这种断言变脆弱。用本账户的 frozen 余额和这两条记录
	// 各自的最终状态来断言，天然不受其它账户/其它测试遗留数据的影响。
	if _, err := svc.ReclaimExpired(context.Background(), 1000); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}

	_, _, frozen := getWallet(t, pool, acct)
	if frozen != 200_000 {
		t.Errorf("frozen = %d, want 200_000 (expired reservation released, live one untouched)", frozen)
	}

	var expiredStatus, liveStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM reservations WHERE request_id = $1`, expiredID).Scan(&expiredStatus); err != nil {
		t.Fatalf("query expired reservation status: %v", err)
	}
	if expiredStatus != string(StatusReleased) {
		t.Errorf("expired reservation status = %q, want released", expiredStatus)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status FROM reservations WHERE request_id = $1`, stillLiveID).Scan(&liveStatus); err != nil {
		t.Fatalf("query live reservation status: %v", err)
	}
	if liveStatus != string(StatusHeld) {
		t.Errorf("still-live reservation status = %q, want held (must not be touched)", liveStatus)
	}
}

func TestReclaimExpired_IsIdempotentAcrossCalls(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)

	reqID := newRequestID(t)
	if _, err := svc.Reserve(context.Background(), reqID, acct, 300_000, 10*time.Millisecond); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if _, err := svc.ReclaimExpired(context.Background(), 1000); err != nil {
		t.Fatalf("first ReclaimExpired: %v", err)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM reservations WHERE request_id = $1`, reqID).Scan(&status); err != nil {
		t.Fatalf("query reservation status: %v", err)
	}
	if status != string(StatusReleased) {
		t.Fatalf("status after first reclaim = %q, want released", status)
	}

	// 第二次扫描：这条记录已经是 released，不再满足 status='held'，
	// 重新调用不应该改变账户的冻结余额（即没有被"重复释放"）。
	if _, err := svc.ReclaimExpired(context.Background(), 1000); err != nil {
		t.Fatalf("second ReclaimExpired: %v", err)
	}

	_, _, frozen := getWallet(t, pool, acct)
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0 (must not be double-released)", frozen)
	}
}

func TestReclaimExpired_RespectsLimit(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)

	ids := make([]string, 3)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s-%d", newRequestID(t), i)
		if _, err := svc.Reserve(context.Background(), ids[i], acct, 100_000, 10*time.Millisecond); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
	}
	time.Sleep(50 * time.Millisecond)

	// limit=1：全局候选可能不止这 3 条（同一个开发库里其它测试也可能留下过期的
	// held 记录），所以不断言全局返回值，只断言"这次调用最多处理 1 条"这件事本身
	// 通过观察冻结余额的减少量来验证：本账户初始 frozen=300_000（3×100_000），
	// 调完一次 limit=1 后，减少量不应该超过 1 条的份额（100_000）。
	if _, err := svc.ReclaimExpired(context.Background(), 1); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	_, _, frozenAfterOne := getWallet(t, pool, acct)
	released := 300_000 - frozenAfterOne
	if released > 100_000 {
		t.Fatalf("frozen dropped by %d after limit=1, want at most 100_000 (one reservation's worth)", released)
	}

	// 用一个足够大的 limit 兜底处理完剩下的，最终这 3 条应该全部变成 released，
	// 账户 frozen 归零。
	if _, err := svc.ReclaimExpired(context.Background(), 1000); err != nil {
		t.Fatalf("ReclaimExpired (drain remaining): %v", err)
	}
	_, _, frozenFinal := getWallet(t, pool, acct)
	if frozenFinal != 0 {
		t.Errorf("frozen after draining all = %d, want 0", frozenFinal)
	}
	for _, id := range ids {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM reservations WHERE request_id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("query status for %s: %v", id, err)
		}
		if status != string(StatusReleased) {
			t.Errorf("reservation %s status = %q, want released", id, status)
		}
	}
}

func TestCreateWallet_InitializesZeroBalance(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccountOnly(t, pool)

	if err := svc.CreateWallet(context.Background(), acct); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}

	cash, bonus, frozen := getWallet(t, pool, acct)
	if cash != 0 || bonus != 0 || frozen != 0 {
		t.Errorf("wallet = cash=%d bonus=%d frozen=%d, want all 0", cash, bonus, frozen)
	}
}

func TestCreateWallet_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccountOnly(t, pool)

	if err := svc.CreateWallet(context.Background(), acct); err != nil {
		t.Fatalf("first CreateWallet: %v", err)
	}
	// 手工把余额改成非零，验证第二次调用不会把它压回 0（ON CONFLICT DO NOTHING）。
	if _, err := pool.Exec(context.Background(), `UPDATE wallets SET cash_balance = 500 WHERE account_id = $1`, acct); err != nil {
		t.Fatalf("manual balance bump: %v", err)
	}
	if err := svc.CreateWallet(context.Background(), acct); err != nil {
		t.Fatalf("second CreateWallet: %v", err)
	}

	cash, _, _ := getWallet(t, pool, acct)
	if cash != 500 {
		t.Errorf("cash_balance = %d, want 500 (second CreateWallet must not reset an existing wallet)", cash)
	}
}

func TestAdjust_PositiveAmountCreditsBalance(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 0, 0, 0)

	receipt, err := svc.Adjust(context.Background(), acct, 500_000, "admin-op-"+newRequestID(t))
	if err != nil {
		t.Fatalf("Adjust: %v", err)
	}
	if receipt.CashAfter != 500_000 {
		t.Errorf("CashAfter = %d, want 500_000", receipt.CashAfter)
	}

	cash, _, _ := getWallet(t, pool, acct)
	if cash != 500_000 {
		t.Errorf("cash_balance = %d, want 500_000", cash)
	}

	var ledgerType, refType string
	var ledgerAmount int64
	if err := pool.QueryRow(context.Background(),
		`SELECT type, amount, ref_type FROM ledger_entries WHERE account_id = $1 AND type = 'adjust'`, acct,
	).Scan(&ledgerType, &ledgerAmount, &refType); err != nil {
		t.Fatalf("query ledger entry: %v", err)
	}
	if ledgerType != "adjust" || ledgerAmount != 500_000 || refType != "admin" {
		t.Errorf("ledger entry = type=%s amount=%d ref_type=%s, want adjust/500000/admin", ledgerType, ledgerAmount, refType)
	}
}

func TestAdjust_NegativeAmountDebitsBalance(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 1_000_000, 0, 0)

	if _, err := svc.Adjust(context.Background(), acct, -300_000, "admin-op-"+newRequestID(t)); err != nil {
		t.Fatalf("Adjust: %v", err)
	}

	cash, _, _ := getWallet(t, pool, acct)
	if cash != 700_000 {
		t.Errorf("cash_balance = %d, want 700_000", cash)
	}
}

func TestAdjust_RejectsZeroAmountAndEmptyRefID(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)
	acct := seedAccount(t, pool, 0, 0, 0)

	if _, err := svc.Adjust(context.Background(), acct, 0, "ref"); err == nil {
		t.Error("expected error for zero amount")
	}
	if _, err := svc.Adjust(context.Background(), acct, 100, ""); err == nil {
		t.Error("expected error for empty refID")
	}
}

func TestAdjust_UnknownAccountReturnsError(t *testing.T) {
	pool := testPool(t)
	svc := New(pool)

	if _, err := svc.Adjust(context.Background(), -1, 100, "ref"); err == nil {
		t.Error("expected error for an account with no wallet")
	}
}
