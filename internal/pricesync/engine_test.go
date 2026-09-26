// 集成测试：连真实 PostgreSQL（tools/devdb，无需 Docker），和本仓库其它包的
// 测试风格一致。Engine 是这个包里唯一真正碰数据库的部分（Validate/Diff/
// DecidePolicy 都是纯函数，测试见 validate_test.go/diff_test.go/policy_test.go）。
package pricesync

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
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

func uniqueCode(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

// testFixture 是一次测试专属的最小拓扑：1 provider + 1 provider_account + 1
// virtual_model + 1 channel，供 Engine.Ingest 挂成本价用。Model 是一个本次测试
// 专属的 upstream_model 名字——checkCrossSourceConflict 按 upstream_model 全局
// 查询（不限定 channel，因为现实里一个模型名在多个来源间比价本来就是跨渠道的），
// 如果测试之间共用像 "up" 这样的固定模型名，不同测试写的 price_observations
// 会互相当成"另一个来源的观测"触发假冲突（第一次写这批测试时就是这样栽的）。
type testFixture struct {
	channelID int64
	sourceID  int64
	model     string
}

func seedFixture(t *testing.T, pool *pgxpool.Pool, adminSvc *admin.Service, level Level) testFixture {
	t.Helper()
	ctx := context.Background()

	provider, err := adminSvc.CreateProvider(ctx, admin.CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	acc, err := adminSvc.CreateProviderAccount(ctx, admin.CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}
	vm, err := adminSvc.CreateVirtualModel(ctx, admin.CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	model := "upstream-" + uniqueCode(t)
	ch, err := adminSvc.CreateChannel(ctx, admin.CreateChannelInput{VirtualModelID: vm.ID, ProviderAccountID: acc.ID, UpstreamModel: model})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	var sourceID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1, $2, 'manual', 'test') RETURNING id`,
		provider.ID, string(level),
	).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}

	return testFixture{channelID: ch.ID, sourceID: sourceID, model: model}
}

func newEngine(t *testing.T, pool *pgxpool.Pool) (*Engine, *admin.Service) {
	t.Helper()
	adminSvc := admin.New(pool, wallet.New(pool), nil, []byte("pricesync-test-pepper"))
	return NewEngine(pool, adminSvc), adminSvc
}

func inputComponent(price float64) []Component {
	return []Component{{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, ServiceTier: "default", UnitPrice: decimal.NewFromFloat(price)}}
}

func TestEngine_CreateSource_ValidatesInput(t *testing.T) {
	pool := testPool(t)
	e, _ := newEngine(t, pool)
	ctx := context.Background()

	if _, err := e.CreateSource(ctx, CreateSourceInput{Level: "bogus", Kind: "manual", Fetcher: "test"}); err == nil {
		t.Error("expected error for invalid level")
	}
	if _, err := e.CreateSource(ctx, CreateSourceInput{Level: LevelL5, Kind: "bogus", Fetcher: "test"}); err == nil {
		t.Error("expected error for invalid kind")
	}
	if _, err := e.CreateSource(ctx, CreateSourceInput{Level: LevelL5, Kind: "manual", Fetcher: ""}); err == nil {
		t.Error("expected error for empty fetcher name")
	}

	id, err := e.CreateSource(ctx, CreateSourceInput{Level: LevelL5, Kind: "manual", Fetcher: "test"})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if id == 0 {
		t.Error("expected a non-zero source ID")
	}
}

func TestEngine_Ingest_NewChannel_DirectionNewPending(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)

	result, err := e.Ingest(context.Background(), IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(1)},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.ChangeRequestID == nil {
		t.Fatal("expected a change request for a brand-new channel with no current price")
	}
	if result.Decision != DecisionPending {
		t.Errorf("Decision = %q, want pending (direction=new always goes to review)", result.Decision)
	}
	if result.AppliedBookID != nil {
		t.Error("AppliedBookID should be nil, a pending change request must not be published yet")
	}

	var direction, status string
	if err := pool.QueryRow(context.Background(),
		`SELECT direction, status FROM price_change_requests WHERE id = $1`, *result.ChangeRequestID,
	).Scan(&direction, &status); err != nil {
		t.Fatalf("query change request: %v", err)
	}
	if direction != string(DirectionNew) || status != string(DecisionPending) {
		t.Errorf("direction=%q status=%q, want new/pending", direction, status)
	}
}

func TestEngine_Ingest_L2Decrease_AutoAppliesImmediately(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)
	ctx := context.Background()

	initialBookID, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID: fx.channelID, Currency: "USD",
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(10)}},
	})
	if err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	result, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(8)}, // 10 -> 8，降价
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Decision != DecisionAutoApproved {
		t.Fatalf("Decision = %q, want auto_approved", result.Decision)
	}
	if result.AppliedBookID == nil {
		t.Fatal("expected AppliedBookID to be set")
	}
	if *result.AppliedBookID == initialBookID {
		t.Error("a new price_book version should have been created, not reused the old one")
	}

	var currency string
	var unitPrice decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT pb.currency, pc.unit_price FROM price_books pb JOIN price_components pc ON pc.price_book_id = pb.id WHERE pb.id = $1`,
		*result.AppliedBookID,
	).Scan(&currency, &unitPrice); err != nil {
		t.Fatalf("query applied price_book: %v", err)
	}
	if currency != "USD" || !unitPrice.Equal(decimal.NewFromInt(8)) {
		t.Errorf("published price = %s %s, want USD 8", unitPrice, currency)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM price_change_requests WHERE id = $1`, *result.ChangeRequestID).Scan(&status); err != nil {
		t.Fatalf("query change request: %v", err)
	}
	if status != "applied" {
		t.Errorf("change request status = %q, want applied", status)
	}
}

func TestEngine_Ingest_L2LargeIncrease_GoesPendingWithoutApplying(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)
	ctx := context.Background()

	if _, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID:  fx.channelID,
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(10)}},
	}); err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	result, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(15)}, // +50%，超过 20% 阈值
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Decision != DecisionPending {
		t.Errorf("Decision = %q, want pending", result.Decision)
	}
	if result.AppliedBookID != nil {
		t.Error("a >20%% L2 increase must not auto-apply")
	}
}

func TestEngine_Ingest_MagnitudeAnomaly_Blocked(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)
	ctx := context.Background()

	if _, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID:  fx.channelID,
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(1)}},
	}); err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	result, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(50)}, // 50x，明显是解析错误
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Decision != DecisionBlocked {
		t.Errorf("Decision = %q, want blocked", result.Decision)
	}
	if result.AppliedBookID != nil {
		t.Error("a blocked change must never be published")
	}
}

func TestEngine_Ingest_L3RequiresTwoConsecutiveMatchingObservations(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL3)
	ctx := context.Background()

	spec := PriceSpec{Currency: "CNY", Components: inputComponent(5)}
	differentSpec := PriceSpec{Currency: "CNY", Components: inputComponent(6)}

	first, err := e.Ingest(ctx, IngestInput{ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL3, UpstreamModel: fx.model, Spec: spec})
	if err != nil {
		t.Fatalf("Ingest (first): %v", err)
	}
	if first.ChangeRequestID != nil {
		t.Error("first L3 observation should not produce a change request yet (no prior observation to confirm against)")
	}

	// "连续两次一致"检查的是紧邻的两次，不是"和历史上任意一次一致"：这一条和
	// first 不一样，所以既不确认它自己，也让下一条不能拿 first 来确认（它比较的
	// 对象永远是紧邻的前一条）。
	different, err := e.Ingest(ctx, IngestInput{ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL3, UpstreamModel: fx.model, Spec: differentSpec})
	if err != nil {
		t.Fatalf("Ingest (different): %v", err)
	}
	if different.ChangeRequestID != nil {
		t.Error("an observation that differs from the immediately preceding one should not confirm anything")
	}

	stillMismatched, err := e.Ingest(ctx, IngestInput{ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL3, UpstreamModel: fx.model, Spec: spec})
	if err != nil {
		t.Fatalf("Ingest (back to original, still mismatched vs 'different'): %v", err)
	}
	if stillMismatched.ChangeRequestID != nil {
		t.Error("this matches 'first' but not the immediately preceding 'different' observation, so it should not confirm yet")
	}

	confirmed, err := e.Ingest(ctx, IngestInput{ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL3, UpstreamModel: fx.model, Spec: spec})
	if err != nil {
		t.Fatalf("Ingest (matches the immediately preceding one): %v", err)
	}
	if confirmed.ChangeRequestID == nil {
		t.Fatal("two consecutive matching observations should confirm and produce a change request")
	}
}

func TestEngine_Ingest_IdenticalPrice_NoChangeRequest(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)
	ctx := context.Background()

	if _, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID:  fx.channelID,
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(5)}},
	}); err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	result, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(5)},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.ChangeRequestID != nil {
		t.Error("an observation identical to the current price should not produce a change request")
	}
}

func TestEngine_ApproveAndReject(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)
	ctx := context.Background()

	if _, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID:  fx.channelID,
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(10)}},
	}); err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	pending, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(15)}, // +50%, pending
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if pending.ChangeRequestID == nil || pending.Decision != DecisionPending {
		t.Fatalf("expected a pending change request, got %+v", pending)
	}

	t.Run("Reject", func(t *testing.T) {
		if err := e.Reject(ctx, *pending.ChangeRequestID, 1); err != nil {
			t.Fatalf("Reject: %v", err)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM price_change_requests WHERE id = $1`, *pending.ChangeRequestID).Scan(&status); err != nil {
			t.Fatalf("query: %v", err)
		}
		if status != "rejected" {
			t.Errorf("status = %q, want rejected", status)
		}
		// 拒绝之后不能再操作。
		if err := e.Reject(ctx, *pending.ChangeRequestID, 1); err != ErrChangeRequestNotPending {
			t.Errorf("second Reject error = %v, want ErrChangeRequestNotPending", err)
		}
	})

	pending2, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model + "-2",
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(20)}, // +100%，全新模型，仍然是 pending
	})
	if err != nil {
		t.Fatalf("Ingest (second model): %v", err)
	}

	t.Run("Approve", func(t *testing.T) {
		bookID, err := e.Approve(ctx, *pending2.ChangeRequestID, 1)
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if bookID == 0 {
			t.Fatal("expected a non-zero published book ID")
		}
		var status string
		var appliedBookID int64
		if err := pool.QueryRow(ctx, `SELECT status, applied_book_id FROM price_change_requests WHERE id = $1`, *pending2.ChangeRequestID).
			Scan(&status, &appliedBookID); err != nil {
			t.Fatalf("query: %v", err)
		}
		if status != "applied" || appliedBookID != bookID {
			t.Errorf("status=%q applied_book_id=%d, want applied/%d", status, appliedBookID, bookID)
		}

		if _, err := e.Approve(ctx, *pending2.ChangeRequestID, 1); err != ErrChangeRequestNotPending {
			t.Errorf("second Approve error = %v, want ErrChangeRequestNotPending", err)
		}
	})

	if _, err := e.Approve(ctx, -1, 1); err != ErrChangeRequestNotFound {
		t.Errorf("Approve(-1) error = %v, want ErrChangeRequestNotFound", err)
	}
}

func TestEngine_Ingest_CrossSourceConflict_ForcesPendingDespiteAutoApprovableDirection(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	ctx := context.Background()

	provider, err := adminSvc.CreateProvider(ctx, admin.CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	acc, err := adminSvc.CreateProviderAccount(ctx, admin.CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}
	vm, err := adminSvc.CreateVirtualModel(ctx, admin.CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	model := uniqueCode(t)
	ch, err := adminSvc.CreateChannel(ctx, admin.CreateChannelInput{VirtualModelID: vm.ID, ProviderAccountID: acc.ID, UpstreamModel: model})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	var sourceA, sourceB int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1, 'L2', 'manual', 'test-a') RETURNING id`, provider.ID).Scan(&sourceA); err != nil {
		t.Fatalf("insert source A: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1, 'L4', 'dataset', 'test-b') RETURNING id`, provider.ID).Scan(&sourceB); err != nil {
		t.Fatalf("insert source B: %v", err)
	}

	if _, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID:  ch.ID,
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(10)}},
	}); err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	// source B（L4）最近观测到 input=8；稍后 source A（L2）观测到 input=6——都是降价
	// （相对当前 10 都在合理范围内，本该自动通过），但两个来源彼此差了 25%，超过
	// 5% 冲突阈值，应该被强制转人工审批。
	if _, err := e.Ingest(ctx, IngestInput{
		ChannelID: ch.ID, SourceID: sourceB, Level: LevelL4, UpstreamModel: model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(8)},
	}); err != nil {
		t.Fatalf("Ingest (source B): %v", err)
	}

	result, err := e.Ingest(ctx, IngestInput{
		ChannelID: ch.ID, SourceID: sourceA, Level: LevelL2, UpstreamModel: model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(6)},
	})
	if err != nil {
		t.Fatalf("Ingest (source A): %v", err)
	}
	if result.Decision != DecisionPending {
		t.Errorf("Decision = %q, want pending (cross-source conflict must force review even though L2-down would normally auto-approve)", result.Decision)
	}
	if result.AppliedBookID != nil {
		t.Error("a cross-source conflict must not auto-apply")
	}
}

func TestEngine_ListPending_ReturnsOnlyPendingAndBlocked(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL2)
	ctx := context.Background()

	if _, err := adminSvc.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID:  fx.channelID,
		Components: []admin.PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(10)}},
	}); err != nil {
		t.Fatalf("seed initial cost price: %v", err)
	}

	// L2 降价：auto_approved，不应该出现在 ListPending 里。
	auto, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(8)},
	})
	if err != nil {
		t.Fatalf("Ingest (auto): %v", err)
	}
	if auto.Decision != DecisionAutoApproved {
		t.Fatalf("Decision = %q, want auto_approved", auto.Decision)
	}

	// 新增一个之前没配置过的计量项（output）：direction=mixed，一律 pending，
	// 应该出现在 ListPending 里。
	pending, err := e.Ingest(ctx, IngestInput{
		ChannelID: fx.channelID, SourceID: fx.sourceID, Level: LevelL2, UpstreamModel: fx.model + "-2",
		Spec: PriceSpec{Currency: "CNY", Components: []Component{{Meter: pricing.MeterOutput, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromInt(20)}}},
	})
	if err != nil {
		t.Fatalf("Ingest (pending): %v", err)
	}
	if pending.Decision != DecisionPending {
		t.Fatalf("Decision = %q, want pending", pending.Decision)
	}

	all, err := e.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	var sawPending bool
	for _, cr := range all {
		if cr.ID == *pending.ChangeRequestID {
			sawPending = true
		}
		if cr.ID == *auto.ChangeRequestID {
			t.Errorf("auto_approved change request %d should not appear in ListPending", cr.ID)
		}
	}
	if !sawPending {
		t.Errorf("expected pending change request %d in ListPending results %+v", *pending.ChangeRequestID, all)
	}
}
