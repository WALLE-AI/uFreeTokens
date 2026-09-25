// 集成测试：连真实 PostgreSQL（tools/devdb，无需 Docker）。促销的正确性核心在于
// "并发下额度/预算不会超发"，和 wallet 包一样必须用真实数据库的事务/行锁验证，
// mock 测不出竞态问题。
package promotion

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

func seedAccount(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO accounts (type, name, status, tier) VALUES ('personal', $1, 'active', 'free') RETURNING id`,
		fmt.Sprintf("promo-test-%d", time.Now().UnixNano()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_counters WHERE account_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	})
	return id
}

type seedPromoOpts struct {
	typ         Type
	priority    int
	discount    *float64
	amountMicro *int64
	period      string
	budgetTotal *int64
	models      []string
	tiers       []string
	starts      time.Time
	ends        *time.Time
	status      string
	stackable   bool
}

func seedPromotion(t *testing.T, pool *pgxpool.Pool, o seedPromoOpts) int64 {
	t.Helper()
	if o.status == "" {
		o.status = "active"
	}
	if o.starts.IsZero() {
		o.starts = time.Now().Add(-time.Hour)
	}

	params := map[string]any{}
	if o.discount != nil {
		params["discount"] = *o.discount
	}
	if o.amountMicro != nil {
		params["amount_micro"] = *o.amountMicro
		params["period"] = o.period
	}
	scope := map[string]any{}
	if o.models != nil {
		scope["models"] = o.models
	}
	if o.tiers != nil {
		scope["tiers"] = o.tiers
	}

	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO promotions (name, side, type, priority, scope, params, budget_total, starts_at, ends_at, status, stackable)
		 VALUES ($1, 'sell', $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		fmt.Sprintf("test-promo-%d", time.Now().UnixNano()), string(o.typ), o.priority, scope, params,
		o.budgetTotal, o.starts, o.ends, o.status, o.stackable,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed promotion: %v", err)
	}

	// 全局共享一个真实数据库，loadCandidates 按 (side, status, 生效时间) 查询整张表，
	// 不做任何测试专属的过滤——不清理就会让本次种下的促销"泄漏"进后面（甚至下次
	// 运行时）的测试用例，第一次写这批测试时就是这样栽的（见本文件的提交历史）。
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_counters WHERE promotion_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM promotions WHERE id = $1`, id)
	})
	return id
}

func floatp(v float64) *float64 { return &v }
func int64p(v int64) *int64     { return &v }

// singleID 断言 got 恰好是一个元素、且等于 want——大多数测试只命中一条促销，
// 这样写比每处都手动判断 len(got)==1 && got[0]==want 更简洁。
func singleID(got []int64, want int64) bool {
	return len(got) == 1 && got[0] == want
}

// uniqueModel 生成一个本次测试专属的虚拟模型名。`go test ./...` 会并发跑不同包的
// 测试二进制，internal/relay 的端到端测试也在同一个真实数据库里插促销数据；
// 如果测试之间共用像 "m" 这样的固定模型名、又不限定 scope.models，不同包的促销
// 会互相串扰（第一次写这批测试时就是这样栽的，见本文件对应的提交历史）。
// 让每个测试固定用自己独有的模型名、并把促销 scope 限定到这个模型名，
// 就不会跟任何其它测试或其它包冲突。
func uniqueModel(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

func TestQuote_NoMatchingPromotion_ChargesListAmount(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 10000 {
		t.Errorf("charged = %d, want 10000", charged)
	}
	if promoIDs != nil {
		t.Errorf("promoIDs = %v, want nil", promoIDs)
	}
}

func TestQuote_PriceDiscount_AppliesPercentage(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	promoID := seedPromotion(t, pool, seedPromoOpts{typ: TypePriceDiscount, priority: 0, discount: floatp(0.2), models: []string{vmName}})

	charged, got, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 8000 { // 20% off 10000 = 8000
		t.Errorf("charged = %d, want 8000", charged)
	}
	if !singleID(got, promoID) {
		t.Errorf("promoIDs = %v, want [%d]", got, promoID)
	}
}

func TestQuote_PriceDiscount_RespectsScope(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	inScope := uniqueModel(t) + "-in"
	outOfScope := uniqueModel(t) + "-out"
	seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.5),
		models: []string{inScope}, // 只对特定模型生效
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", outOfScope, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 10000 || promoIDs != nil {
		t.Errorf("out-of-scope model should not get the discount: charged=%d promoIDs=%v", charged, promoIDs)
	}

	charged, promoIDs, err = e.Quote(context.Background(), accountID, "free", inScope, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 5000 || promoIDs == nil {
		t.Errorf("in-scope model should get the discount: charged=%d promoIDs=%v", charged, promoIDs)
	}
}

func TestQuote_PriceDiscount_TierScope(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.5), models: []string{vmName}, tiers: []string{"enterprise"},
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 10000 || promoIDs != nil {
		t.Error("free tier should not match an enterprise-only promotion")
	}

	charged, promoIDs, err = e.Quote(context.Background(), accountID, "enterprise", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 5000 || promoIDs == nil {
		t.Error("enterprise tier should match")
	}
}

func TestQuote_PriceDiscount_PriorityOrder(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	// priority 数值越小越优先：低优先级（数值大）的 80% off 不应该生效，
	// 应该用高优先级（数值小）的 10% off。
	seedPromotion(t, pool, seedPromoOpts{typ: TypePriceDiscount, priority: 10, discount: floatp(0.8), models: []string{vmName}})
	highPriorityID := seedPromotion(t, pool, seedPromoOpts{typ: TypePriceDiscount, priority: 0, discount: floatp(0.1), models: []string{vmName}})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 9000 {
		t.Errorf("charged = %d, want 9000 (higher-priority 10%% discount should win)", charged)
	}
	if !singleID(promoIDs, highPriorityID) {
		t.Errorf("promoIDs = %v, want [%d] (neither promotion is stackable, so only the top-priority one applies)", promoIDs, highPriorityID)
	}
}

func TestQuote_PriceDiscount_ExpiredPromotionNotApplied(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	past := time.Now().Add(-time.Hour)
	seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.5), models: []string{vmName},
		starts: time.Now().Add(-2 * time.Hour), ends: &past,
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 10000 || promoIDs != nil {
		t.Error("expired promotion should not be applied")
	}
}

func TestQuote_PriceDiscount_BudgetExhaustionFallsBackToListPrice(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	// 50% off，预算只够省 3000 微元。
	seedPromotion(t, pool, seedPromoOpts{typ: TypePriceDiscount, priority: 0, discount: floatp(0.5), models: []string{vmName}, budgetTotal: int64p(3000)})

	// 第一次：10000 -> 5000，省 5000，超过预算 3000 -> 不应用，按原价收取。
	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 10000 || promoIDs != nil {
		t.Errorf("saving 5000 exceeds budget 3000, should not apply: charged=%d promoIDs=%v", charged, promoIDs)
	}

	// 换一个刚好在预算内的场景：省 3000 (6000->3000)。
	charged2, promoIDs2, err := e.Quote(context.Background(), accountID, "free", vmName, 6000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged2 != 3000 || promoIDs2 == nil {
		t.Errorf("saving exactly 3000 should fit the budget: charged=%d promoIDs=%v", charged2, promoIDs2)
	}
}

func TestQuote_FreeQuota_CoversUpToCapThenChargesRemainder(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	seedPromotion(t, pool, seedPromoOpts{typ: TypeFreeQuota, priority: 0, amountMicro: int64p(5000), period: "daily", models: []string{vmName}})
	ctx := context.Background()

	// 第一次消费 3000，全免。
	charged, promoIDs, err := e.Quote(ctx, accountID, "free", vmName, 3000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 0 || promoIDs == nil {
		t.Errorf("first 3000 should be fully covered: charged=%d promoIDs=%v", charged, promoIDs)
	}

	// 第二次消费 4000：剩余额度只有 2000，覆盖 2000，实收 2000。
	charged2, promoIDs2, err := e.Quote(ctx, accountID, "free", vmName, 4000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged2 != 2000 || promoIDs2 == nil {
		t.Errorf("second request should be partially covered: charged=%d, want 2000", charged2)
	}

	// 第三次：额度已耗尽，全额收取。
	charged3, promoIDs3, err := e.Quote(ctx, accountID, "free", vmName, 1000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged3 != 1000 || promoIDs3 != nil {
		t.Errorf("quota exhausted, should charge full amount: charged=%d promoIDs=%v", charged3, promoIDs3)
	}
}

func TestQuote_FreeQuota_PerAccountIsolation(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountA, accountB := seedAccount(t, pool), seedAccount(t, pool)
	vmName := uniqueModel(t)
	seedPromotion(t, pool, seedPromoOpts{typ: TypeFreeQuota, priority: 0, amountMicro: int64p(1000), period: "daily", models: []string{vmName}})
	ctx := context.Background()

	if charged, _, err := e.Quote(ctx, accountA, "free", vmName, 1000); err != nil || charged != 0 {
		t.Fatalf("account A should get full coverage: charged=%d err=%v", charged, err)
	}
	if charged, _, err := e.Quote(ctx, accountB, "free", vmName, 1000); err != nil || charged != 0 {
		t.Errorf("account B's quota should be independent of account A's: charged=%d err=%v", charged, err)
	}
}

// TestQuote_Stacking_ChainsStackablePromotionsInPriorityOrder 验证 stackable=true
// 的促销真的会叠加：高优先级的 20% off（stackable）先把 10000 打到 8000，
// 低优先级的 10% off（不管是否 stackable，链条只看"前一条是否 stackable"）
// 接着在 8000 的基础上再打 10% -> 7200。两条促销 ID 都应该出现在结果里。
func TestQuote_Stacking_ChainsStackablePromotionsInPriorityOrder(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	firstID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.2), models: []string{vmName}, stackable: true,
	})
	secondID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 10, discount: floatp(0.1), models: []string{vmName},
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 7200 { // 10000 * 0.8 = 8000; 8000 * 0.9 = 7200
		t.Errorf("charged = %d, want 7200 (20%% then 10%% chained)", charged)
	}
	if len(promoIDs) != 2 || promoIDs[0] != firstID || promoIDs[1] != secondID {
		t.Errorf("promoIDs = %v, want [%d %d] in priority order", promoIDs, firstID, secondID)
	}
}

// TestQuote_Stacking_NonStackableTopPromotionStopsTheChain 验证不叠加是默认行为：
// 顶部命中的促销没有标 stackable，即便还有更低优先级的候选，也不会继续叠加——
// 和只有一条促销时完全一样（技术方案 §7.10 的默认语义）。
func TestQuote_Stacking_NonStackableTopPromotionStopsTheChain(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	topID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.2), models: []string{vmName}, // stackable 默认 false
	})
	seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 10, discount: floatp(0.1), models: []string{vmName}, stackable: true,
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 8000 {
		t.Errorf("charged = %d, want 8000 (only the non-stackable top promotion should apply)", charged)
	}
	if !singleID(promoIDs, topID) {
		t.Errorf("promoIDs = %v, want [%d]", promoIDs, topID)
	}
}

// TestQuote_Stacking_MixesDiscountAndFreeQuota 验证叠加不限于同类型促销：
// stackable 的 50% off 先把 10000 打到 5000，再叠加 free_quota（当日剩余额度 3000）
// 覆盖掉其中 3000，实收 2000。
func TestQuote_Stacking_MixesDiscountAndFreeQuota(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	discountID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.5), models: []string{vmName}, stackable: true,
	})
	quotaID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypeFreeQuota, priority: 10, amountMicro: int64p(3000), period: "daily", models: []string{vmName},
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 2000 { // 10000 * 0.5 = 5000; 5000 - 3000（免费额度覆盖）= 2000
		t.Errorf("charged = %d, want 2000", charged)
	}
	if len(promoIDs) != 2 || promoIDs[0] != discountID || promoIDs[1] != quotaID {
		t.Errorf("promoIDs = %v, want [%d %d]", promoIDs, discountID, quotaID)
	}
}

// TestQuote_Stacking_FailedChainedPromotionStopsWithoutFallback 验证链中间一条
// 应用失败（预算耗尽）时：链停在这里，保留已经成功应用的部分，不去尝试更低
// 优先级的第三条候选（技术方案 §7.10：不做失败降级链）。
func TestQuote_Stacking_FailedChainedPromotionStopsWithoutFallback(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	firstID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 0, discount: floatp(0.2), models: []string{vmName}, stackable: true,
	})
	// 第二条：50% off 但预算只够省 100 微元，8000 的 50% 是 4000，远超预算 -> 应用失败。
	seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 10, discount: floatp(0.5), models: []string{vmName},
		budgetTotal: int64p(100), stackable: true,
	})
	// 第三条：即便第二条失败了，也不应该跳过它去尝试这条更低优先级的促销。
	thirdID := seedPromotion(t, pool, seedPromoOpts{
		typ: TypePriceDiscount, priority: 20, discount: floatp(0.05), models: []string{vmName},
	})

	charged, promoIDs, err := e.Quote(context.Background(), accountID, "free", vmName, 10000)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if charged != 8000 { // 只应用第一条 20% off；第二条失败后链条停止，第三条不会被尝试。
		t.Errorf("charged = %d, want 8000", charged)
	}
	if !singleID(promoIDs, firstID) {
		t.Errorf("promoIDs = %v, want [%d] (chain must stop at the failed budget-exhausted promotion, not fall through to id %d)", promoIDs, firstID, thirdID)
	}
}

// TestQuote_FreeQuota_ConcurrentRequestsNeverExceedCap 是并发正确性回归测试
// （同样的思路：wallet 的透支测试、ratelimit 的并发测试）：quota=10000，
// 20 个并发请求各申请 1000，最多只能有 10 个被全额覆盖，总覆盖量不能超过 10000。
func TestQuote_FreeQuota_ConcurrentRequestsNeverExceedCap(t *testing.T) {
	pool := testPool(t)
	e := New(pool)
	accountID := seedAccount(t, pool)
	vmName := uniqueModel(t)
	seedPromotion(t, pool, seedPromoOpts{typ: TypeFreeQuota, priority: 0, amountMicro: int64p(10000), period: "daily", models: []string{vmName}})

	const n = 20
	const perRequest = int64(1000)
	var wg sync.WaitGroup
	var totalCovered int64

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			charged, _, err := e.Quote(context.Background(), accountID, "free", vmName, perRequest)
			if err != nil {
				t.Errorf("Quote: %v", err)
				return
			}
			covered := perRequest - charged
			atomic.AddInt64(&totalCovered, covered)
		}()
	}
	wg.Wait()

	if totalCovered != 10000 {
		t.Errorf("totalCovered = %d, want exactly 10000 (quota cap)", totalCovered)
	}
	if totalCovered > 10000 {
		t.Fatalf("CRITICAL: totalCovered (%d) exceeds quota cap (10000) — overspend bug", totalCovered)
	}
}
