// 集成测试：连真实 PostgreSQL（tools/devdb，无需 Docker）。这是一个跑了很久的
// 共享开发库，其它包（wallet/promotion/relay）的测试会直接用 SQL 种一个带初始
// cash_balance、却没有对应 ledger_entries 的钱包（纯粹为了测试方便，不走真实的
// 充值流程）——这意味着 CheckWallets 在这个库上几乎总会发现"别人的"不一致。
// 所以这里的断言全部按 accountID 过滤，只看自己种下的那个账户，不对全局结果的
// 数量做任何假设（和 wallet 包的 ReclaimExpired 测试是同一个教训）。
package reconcile

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
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

// seedBareWallet 直接用 SQL 种一个账户 + 钱包，不经过 wallet.Service，方便构造
// "余额和流水对不上"的场景。
func seedBareWallet(t *testing.T, pool *pgxpool.Pool, cash, frozen, bonus int64) int64 {
	t.Helper()
	ctx := context.Background()
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (type, name, status, tier) VALUES ('personal', $1, 'active', 'free') RETURNING id`,
		fmt.Sprintf("reconcile-test-%d", time.Now().UnixNano()),
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM reservations WHERE account_id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE account_id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE account_id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO wallets (account_id, cash_balance, frozen, bonus_balance) VALUES ($1, $2, $3, $4)`,
		accountID, cash, frozen, bonus,
	); err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	return accountID
}

// seedRecharge 模拟一笔"充值"：原子地既更新 wallets.cash_balance 又写一条
// ledger_entries——这是真实的 wallet.Credit/充值流程尚未实现前，测试里手工
// 保持两边一致的方式（技术方案 §7.11 的 payment_orders 充值流程会做同样的事）。
func seedRecharge(t *testing.T, pool *pgxpool.Pool, accountID int64, amount int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var cashAfter int64
	if err := tx.QueryRow(ctx,
		`UPDATE wallets SET cash_balance = cash_balance + $2 WHERE account_id = $1 RETURNING cash_balance`,
		accountID, amount,
	).Scan(&cashAfter); err != nil {
		t.Fatalf("update wallet cash_balance: %v", err)
	}
	refID := fmt.Sprintf("reconcile-recharge-%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx,
		`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id)
		 VALUES ($1, 'recharge', $2, 'cash', $3, 0, 'payment_order', $4)`,
		accountID, amount, cashAfter, refID,
	); err != nil {
		t.Fatalf("insert ledger_entries: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func findDiscrepancy(discs []WalletDiscrepancy, accountID int64, field string) *WalletDiscrepancy {
	for i := range discs {
		if discs[i].AccountID == accountID && discs[i].Field == field {
			return &discs[i]
		}
	}
	return nil
}

func TestCheckWallets_ZeroBalanceNoLedger_NoDiscrepancy(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	// 余额全为 0、没有任何流水/预扣/赠款——三个字段的"应有值"也都是 0，天然一致。
	accountID := seedBareWallet(t, pool, 0, 0, 0)

	discs, err := r.CheckWallets(context.Background())
	if err != nil {
		t.Fatalf("CheckWallets: %v", err)
	}
	if d := findDiscrepancy(discs, accountID, "cash_balance"); d != nil {
		t.Errorf("unexpected cash_balance discrepancy: %+v", d)
	}
	if d := findDiscrepancy(discs, accountID, "frozen"); d != nil {
		t.Errorf("unexpected frozen discrepancy: %+v", d)
	}
	if d := findDiscrepancy(discs, accountID, "bonus_balance"); d != nil {
		t.Errorf("unexpected bonus_balance discrepancy: %+v", d)
	}
}

func TestCheckWallets_DetectsCashBalanceMismatch(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	// cash_balance=5000 但没有任何 ledger_entries：应有值是 0，出现不一致。
	accountID := seedBareWallet(t, pool, 5000, 0, 0)

	discs, err := r.CheckWallets(context.Background())
	if err != nil {
		t.Fatalf("CheckWallets: %v", err)
	}
	d := findDiscrepancy(discs, accountID, "cash_balance")
	if d == nil {
		t.Fatal("expected a cash_balance discrepancy, found none")
	}
	if d.Recorded != 5000 || d.Expected != 0 {
		t.Errorf("discrepancy = %+v, want Recorded=5000 Expected=0", d)
	}
	if d.Diff() != 5000 {
		t.Errorf("Diff() = %d, want 5000", d.Diff())
	}
}

func TestCheckWallets_DetectsFrozenMismatch(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	// frozen=1000 但没有任何 held 的 reservation：应有值是 0。
	accountID := seedBareWallet(t, pool, 0, 1000, 0)

	discs, err := r.CheckWallets(context.Background())
	if err != nil {
		t.Fatalf("CheckWallets: %v", err)
	}
	d := findDiscrepancy(discs, accountID, "frozen")
	if d == nil {
		t.Fatal("expected a frozen discrepancy, found none")
	}
	if d.Recorded != 1000 || d.Expected != 0 {
		t.Errorf("discrepancy = %+v, want Recorded=1000 Expected=0", d)
	}
}

// TestCheckWallets_RealReserveSettleFlow_IsConsistent 是最重要的一个测试：
// 通过 wallet.Service 走一次真实的 Reserve -> Settle，产生的余额和账本理应
// 天然一致——如果这个测试失败，说明 wallet 包本身的记账逻辑和这里的对账逻辑
// 对"应该怎么算"的理解不一致，值得警惕。
func TestCheckWallets_RealReserveSettleFlow_IsConsistent(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	svc := wallet.New(pool)
	// 从 0 开始，而不是像其它测试那样直接把 cash_balance 种成一个非零值——
	// 那样做会绕开账本，制造出一个"起点本身就对不上"的账户（第一次写这个测试时
	// 就是这样栽的：种 1,000,000 现金却不写对应的 ledger_entries，Reserve+Settle
	// 之后 CheckWallets 正确地报出了不一致，只是这暴露的是测试自己的问题，不是
	// 对账逻辑的问题）。这里先用一笔"充值"流水把起点立好，才能验证"只要全程都走
	// wallet.Service 的方法，最终结果就应该是一致的"这件事。
	accountID := seedBareWallet(t, pool, 0, 0, 0)
	seedRecharge(t, pool, accountID, 1_000_000)

	reqID := fmt.Sprintf("reconcile-flow-%d", time.Now().UnixNano())
	if _, err := svc.Reserve(context.Background(), reqID, accountID, 300_000, time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := svc.Settle(context.Background(), reqID, 200_000, ""); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	discs, err := r.CheckWallets(context.Background())
	if err != nil {
		t.Fatalf("CheckWallets: %v", err)
	}
	if d := findDiscrepancy(discs, accountID, "cash_balance"); d != nil {
		t.Errorf("cash_balance should be consistent after a real Reserve+Settle flow: %+v", d)
	}
	if d := findDiscrepancy(discs, accountID, "frozen"); d != nil {
		t.Errorf("frozen should be consistent after Settle releases the hold: %+v", d)
	}
}

// seedRequestLog 直接插入一行 request_logs（表按天分区，created_at 必须落在
// 已存在的分区内——迁移里默认创建了"迁移执行当天起 14 天"的分区，本地开发环境
// 里用 now() 附近的时间通常是安全的）。
func seedRequestLog(t *testing.T, pool *pgxpool.Pool, accountID int64, createdAt time.Time, charged int64) {
	t.Helper()
	requestID := fmt.Sprintf("reconcile-log-%d", time.Now().UnixNano())
	_, err := pool.Exec(context.Background(),
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint,
		                            is_stream, status, attempts, usage_source, charged_amount)
		 VALUES ($1, $2, $3, 1, 'm', 'chat.completions', false, 'success', 1, 'upstream', $4)`,
		requestID, createdAt, accountID, charged,
	)
	if err != nil {
		t.Fatalf("seed request_log: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM request_logs WHERE request_id = $1`, requestID)
	})
}

func seedLedgerConsume(t *testing.T, pool *pgxpool.Pool, accountID int64, createdAt time.Time, amount int64) {
	t.Helper()
	refID := fmt.Sprintf("reconcile-ledger-%d", time.Now().UnixNano())
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id, created_at)
		 VALUES ($1, 'consume', $2, 'cash', 0, 0, 'request', $3, $4)`,
		accountID, -amount, refID, createdAt,
	)
	if err != nil {
		t.Fatalf("seed ledger_entries: %v", err)
	}
}

// futureAnchor 返回一个"保证没有别的测试会往这里写数据"的时间点，用作
// CheckLedgerVsRequestLogs 窗口测试的锚点。这几个测试最初直接用 time.Now()
// 附近的窗口，结果被同一个真实数据库上并发跑着的其它包的测试撞了个正着——
// 那些测试里真实的 Reserve/Settle 调用同样会在"现在"这个时间点写 ledger_entries，
// 落进了本该只属于这个测试自己的窗口里（`go test ./...` 并发跑不同包，见本文件
// 顶部注释和其它包已经踩过的同类坑）。整个测试套件里没有任何代码会写入未来时间戳
// 的记录，所以把窗口挪到几小时后，就不可能再有别的测试凑巧落进来。
//
// hoursAhead 必须在用到 futureAnchor 的几个测试之间互不相同：它们的窗口宽度是
// ±1 分钟，而同一个测试文件里的用例执行间隔通常只有几百毫秒，如果都用同一个
// "+2 小时"，相邻两个测试的窗口会互相重叠，同样会把对方种下的数据算进来
// （这不是猜测——第一次这么写就是这样炸的）。每个测试给一个不同的 hoursAhead，
// 结构上就不可能重叠。
func futureAnchor(t *testing.T, hoursAhead int) time.Time {
	t.Helper()
	return time.Now().Add(time.Duration(hoursAhead) * time.Hour)
}

// ledger_entries 是真正意义上的仅追加表（有数据库触发器硬性禁止 UPDATE/DELETE，
// 见 migrations/00006），测试里插进去的行在这个开发库中永远删不掉。这意味着任何
// "固定未来窗口"在多次运行（不只是并发运行，是同一个窗口今天种一条、明天又种一条）
// 之间都会不断累积，不能断言窗口内的绝对总量。正确的做法是在同一次测试执行内，
// 用完全相同的 [from,to) 先测一次"之前"、插入数据后再测一次"之后"，只断言这个
// delta——不管窗口里历史上已经堆了多少来自之前运行的旧数据，这次新插入造成的
// 变化量总是准确的。

func TestCheckLedgerVsRequestLogs_MatchingTotals(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)

	anchor := futureAnchor(t, 2)
	from, to := anchor.Add(-time.Minute), anchor.Add(time.Minute)

	before, err := r.CheckLedgerVsRequestLogs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("CheckLedgerVsRequestLogs (before): %v", err)
	}

	seedLedgerConsume(t, pool, accountID, anchor, 500)
	seedRequestLog(t, pool, accountID, anchor, 500)

	after, err := r.CheckLedgerVsRequestLogs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("CheckLedgerVsRequestLogs (after): %v", err)
	}
	if after.Diff() != before.Diff() {
		t.Errorf("Diff() changed by %d after adding a matching pair, want unchanged (0 net effect)", after.Diff()-before.Diff())
	}
	if after.LedgerTotal-before.LedgerTotal != 500 {
		t.Errorf("LedgerTotal increased by %d, want 500", after.LedgerTotal-before.LedgerTotal)
	}
	if after.RequestLogsTotal-before.RequestLogsTotal != 500 {
		t.Errorf("RequestLogsTotal increased by %d, want 500", after.RequestLogsTotal-before.RequestLogsTotal)
	}
}

func TestCheckLedgerVsRequestLogs_DetectsMismatch(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)

	anchor := futureAnchor(t, 3)
	from, to := anchor.Add(-time.Minute), anchor.Add(time.Minute)

	before, err := r.CheckLedgerVsRequestLogs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("CheckLedgerVsRequestLogs (before): %v", err)
	}

	// 只种账本消费，不种对应的 request_logs：ledger 总额应该多 700，
	// request_logs 总额不变，Diff() 应该相应多 700。
	seedLedgerConsume(t, pool, accountID, anchor, 700)

	after, err := r.CheckLedgerVsRequestLogs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("CheckLedgerVsRequestLogs (after): %v", err)
	}
	if after.Diff()-before.Diff() != 700 {
		t.Errorf("Diff() increased by %d, want 700 (unmatched ledger consume)", after.Diff()-before.Diff())
	}
}

func TestCheckLedgerVsRequestLogs_WindowExcludesOutOfRangeRows(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)

	anchor := futureAnchor(t, 4)
	windowFrom, windowTo := anchor, anchor.Add(time.Minute)

	// 种在窗口之外（早于 windowFrom）的记录不应该被计入。
	seedLedgerConsume(t, pool, accountID, anchor.Add(-time.Hour), 999)

	result, err := r.CheckLedgerVsRequestLogs(context.Background(), windowFrom, windowTo)
	if err != nil {
		t.Fatalf("CheckLedgerVsRequestLogs: %v", err)
	}
	if result.Diff() != 0 {
		t.Errorf("Diff() = %d, want 0 (the seeded record is outside the window and must not be counted)", result.Diff())
	}
}

func TestRun_CombinesBothChecks(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 2500, 0, 0) // 制造一个 cash_balance 不一致

	report, err := r.Run(context.Background(), time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := findDiscrepancy(report.WalletDiscrepancies, accountID, "cash_balance"); d == nil {
		t.Error("Run() should surface the wallet discrepancy seeded by this test")
	}
	if report.LedgerVsLogs == nil {
		t.Error("Run() should always populate LedgerVsLogs")
	}
	if report.Clean() {
		t.Error("Clean() should be false when a discrepancy was seeded")
	}
}

// TestCheckUpstreamCost_FlagsDriftingChannel：同一个渠道上「按成本价计算的成本」与「上游
// 报告的成本 × 汇率」偏差超过 20% 时被标出，偏差小的渠道不报。用本测试专属的假币种和
// 未来时间窗口，避免与并发运行的其他测试数据互相干扰（见 futureAnchor 的注释）。
func TestCheckUpstreamCost_FlagsDriftingChannel(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	cur := "U" + suffix[len(suffix)-6:]
	if _, err := pool.Exec(ctx, `INSERT INTO fx_rates (base, quote, rate, source, effective_date) VALUES ($1, 'CNY', 7, 'test', CURRENT_DATE)`, cur); err != nil {
		t.Fatal(err)
	}
	channel := func(name string) int64 {
		var providerID, accountID, vmID, channelID int64
		must := func(err error) {
			if err != nil {
				t.Fatal(err)
			}
		}
		must(pool.QueryRow(ctx, `INSERT INTO providers (code, name, protocol, status, currency) VALUES ($1, 'p', 'openai', 'active', $2) RETURNING id`, name+suffix, cur).Scan(&providerID))
		must(pool.QueryRow(ctx, `INSERT INTO provider_accounts (provider_id, name, base_url, status) VALUES ($1, 'a', 'https://x.example', 'active') RETURNING id`, providerID).Scan(&accountID))
		must(pool.QueryRow(ctx, `INSERT INTO virtual_models (name, family, type, context_window, max_output, status) VALUES ($1, 'f', 'chat', 1, 1, 'active') RETURNING id`, name+suffix).Scan(&vmID))
		must(pool.QueryRow(ctx, `INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, status) VALUES ($1, $2, 'u', 'active') RETURNING id`, vmID, accountID).Scan(&channelID))
		return channelID
	}
	drifting, fine := channel("drift-"), channel("fine-")
	at := futureAnchor(t, 9)
	seed := func(channelID int64, costMicro int64, upstream float64) {
		id := fmt.Sprintf("upcost-%d", time.Now().UnixNano())
		if _, err := pool.Exec(ctx,
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream, status, attempts,
			                           usage_source, channel_id, cost_amount, upstream_cost)
			 VALUES ($1, $2, 1, 1, 'm', 'chat.completions', false, 'success', 1, 'upstream', $3, $4, $5)`,
			id, at, channelID, costMicro, upstream); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM request_logs WHERE request_id = $1`, id) })
	}
	seed(drifting, 1_000_000, 0.1) // 成本 ¥1，上游 0.1×7 = ¥0.7 → 偏差 43%
	seed(fine, 700_000, 0.1)       // 成本 ¥0.7，上游 ¥0.7 → 无偏差

	drifts, err := New(pool).CheckUpstreamCost(ctx, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var found, foundFine bool
	for _, d := range drifts {
		if d.ChannelID == drifting {
			found = d.Ratio() > 0.4 && d.Ratio() < 0.45 && d.Currency == cur
		}
		if d.ChannelID == fine {
			foundFine = true
		}
	}
	if !found || foundFine {
		t.Errorf("drifts = %+v", drifts)
	}
}
