package reconcile

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mockBillingFetcher 是 BillingFetcher 的测试替身:返回预先编排好的账单明细,
// 不发任何网络请求。技术方案 §7.16 的账单级对账在这个阶段只搭框架(用户明确
// 选择的范围),真实上游账单 API 接入留给后续。
type mockBillingFetcher struct {
	items []BillingLineItem
	err   error
}

func (f *mockBillingFetcher) FetchDailyBilling(ctx context.Context, providerAccountID int64, day time.Time) ([]BillingLineItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

// seedBillingChannel 种一个 provider -> provider_account -> virtual_model ->
// channel 的最小链路,返回 (providerAccountID, channelID),供 seedBilledRequestLog
// 挂 request_logs.channel_id。
func seedBillingChannel(t *testing.T, pool *pgxpool.Pool, upstreamModel string) (providerAccountID, channelID int64) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol) VALUES ($1, $1, 'openai') RETURNING id`,
		"reconcile-billing-provider-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (provider_id, name, base_url) VALUES ($1, $2, 'https://example.invalid') RETURNING id`,
		providerID, "reconcile-billing-account-"+suffix,
	).Scan(&providerAccountID); err != nil {
		t.Fatalf("seed provider_account: %v", err)
	}
	var virtualModelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output) VALUES ($1, 'test', 'chat', 8192, 4096) RETURNING id`,
		"reconcile-billing-vm-"+suffix,
	).Scan(&virtualModelID); err != nil {
		t.Fatalf("seed virtual_model: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model) VALUES ($1, $2, $3) RETURNING id`,
		virtualModelID, providerAccountID, upstreamModel,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID)
		_, _ = pool.Exec(ctx, `DELETE FROM virtual_models WHERE id = $1`, virtualModelID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE id = $1`, providerAccountID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE id = $1`, providerID)
	})
	return providerAccountID, channelID
}

// seedBilledRequestLog 种一行 request_logs,带上 channel_id 和 cost_amount——
// CheckBillingDrift 靠 channel_id 关联 channels 拿 provider_account_id/
// upstream_model,靠 cost_amount(不是 charged_amount)算本地成本。
func seedBilledRequestLog(t *testing.T, pool *pgxpool.Pool, accountID, channelID int64, createdAt time.Time, costAmount int64) {
	t.Helper()
	requestID := fmt.Sprintf("reconcile-billing-log-%d", time.Now().UnixNano())
	_, err := pool.Exec(context.Background(),
		`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, channel_id, endpoint,
		                            is_stream, status, attempts, usage_source, cost_amount)
		 VALUES ($1, $2, $3, 1, 'm', $4, 'chat.completions', false, 'success', 1, 'upstream', $5)`,
		requestID, createdAt, accountID, channelID, costAmount,
	)
	if err != nil {
		t.Fatalf("seed billed request_log: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM request_logs WHERE request_id = $1`, requestID)
	})
}

func findBillingDrift(drifts []ChannelBillingDrift, providerAccountID int64, upstreamModel string) *ChannelBillingDrift {
	for i := range drifts {
		if drifts[i].ProviderAccountID == providerAccountID && drifts[i].UpstreamModel == upstreamModel {
			return &drifts[i]
		}
	}
	return nil
}

func TestCheckBillingDrift_MatchingTotals_NotFlagged(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)
	day := time.Now()
	providerAccountID, channelID := seedBillingChannel(t, pool, "gpt-4o-billing-test")
	seedBilledRequestLog(t, pool, accountID, channelID, day, 10_000)

	fetcher := &mockBillingFetcher{items: []BillingLineItem{
		{UpstreamModel: "gpt-4o-billing-test", AmountMicro: 10_000},
	}}
	drifts, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, day)
	if err != nil {
		t.Fatalf("CheckBillingDrift: %v", err)
	}
	d := findBillingDrift(drifts, providerAccountID, "gpt-4o-billing-test")
	if d == nil {
		t.Fatal("expected a drift entry for gpt-4o-billing-test")
	}
	if d.Diff() != 0 {
		t.Errorf("Diff() = %d, want 0", d.Diff())
	}
	if d.Flagged() {
		t.Error("Flagged() = true, want false (totals match exactly)")
	}
}

func TestCheckBillingDrift_SmallDrift_NotFlagged(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)
	day := time.Now()
	providerAccountID, channelID := seedBillingChannel(t, pool, "claude-billing-test")
	// 本地 10,000,上游 10,050:0.5% 漂移,低于 1% 阈值。
	seedBilledRequestLog(t, pool, accountID, channelID, day, 10_000)

	fetcher := &mockBillingFetcher{items: []BillingLineItem{
		{UpstreamModel: "claude-billing-test", AmountMicro: 10_050},
	}}
	drifts, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, day)
	if err != nil {
		t.Fatalf("CheckBillingDrift: %v", err)
	}
	d := findBillingDrift(drifts, providerAccountID, "claude-billing-test")
	if d == nil {
		t.Fatal("expected a drift entry for claude-billing-test")
	}
	if d.Flagged() {
		t.Errorf("Flagged() = true, want false (drift ratio %.4f is under the 1%% threshold)", *d.DriftRatio())
	}
}

func TestCheckBillingDrift_LargeDrift_Flagged(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)
	day := time.Now()
	providerAccountID, channelID := seedBillingChannel(t, pool, "drift-billing-test")
	// 本地 10,000,上游 8,000:25% 漂移,远超 1% 阈值。
	seedBilledRequestLog(t, pool, accountID, channelID, day, 10_000)

	fetcher := &mockBillingFetcher{items: []BillingLineItem{
		{UpstreamModel: "drift-billing-test", AmountMicro: 8_000},
	}}
	drifts, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, day)
	if err != nil {
		t.Fatalf("CheckBillingDrift: %v", err)
	}
	d := findBillingDrift(drifts, providerAccountID, "drift-billing-test")
	if d == nil {
		t.Fatal("expected a drift entry for drift-billing-test")
	}
	if d.Diff() != 2_000 {
		t.Errorf("Diff() = %d, want 2000", d.Diff())
	}
	if !d.Flagged() {
		t.Error("Flagged() = false, want true (25% drift must be flagged)")
	}
}

func TestCheckBillingDrift_UpstreamHasModelWeDidNotLog_Flagged(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	day := time.Now()
	providerAccountID, _ := seedBillingChannel(t, pool, "unused-channel-for-provider")

	// 上游账单里有一个我们本地完全没有记录的模型(local=0, upstream=5000)。
	fetcher := &mockBillingFetcher{items: []BillingLineItem{
		{UpstreamModel: "phantom-model", AmountMicro: 5_000},
	}}
	drifts, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, day)
	if err != nil {
		t.Fatalf("CheckBillingDrift: %v", err)
	}
	d := findBillingDrift(drifts, providerAccountID, "phantom-model")
	if d == nil {
		t.Fatal("expected a drift entry for phantom-model even though we have no local rows for it")
	}
	if d.LocalCostAmount != 0 {
		t.Errorf("LocalCostAmount = %d, want 0", d.LocalCostAmount)
	}
	if !d.Flagged() {
		t.Error("Flagged() = false, want true (upstream billed us for a model we never logged)")
	}
}

func TestCheckBillingDrift_LocalHasModelUpstreamDidNotBill_Flagged(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)
	day := time.Now()
	providerAccountID, channelID := seedBillingChannel(t, pool, "unbilled-model-test")
	seedBilledRequestLog(t, pool, accountID, channelID, day, 3_000)

	// 上游账单完全没有提到这个模型(比如免费额度、或者账单接口漏了一项)。
	fetcher := &mockBillingFetcher{items: nil}
	drifts, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, day)
	if err != nil {
		t.Fatalf("CheckBillingDrift: %v", err)
	}
	d := findBillingDrift(drifts, providerAccountID, "unbilled-model-test")
	if d == nil {
		t.Fatal("expected a drift entry for unbilled-model-test")
	}
	if d.UpstreamBilled != 0 {
		t.Errorf("UpstreamBilled = %d, want 0", d.UpstreamBilled)
	}
	if d.DriftRatio() != nil {
		t.Errorf("DriftRatio() = %v, want nil (division by zero upstream)", *d.DriftRatio())
	}
	if !d.Flagged() {
		t.Error("Flagged() = false, want true (we have local cost but upstream shows nothing)")
	}
}

func TestCheckBillingDrift_OutsideDayWindow_NotCounted(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	accountID := seedBareWallet(t, pool, 0, 0, 0)
	day := time.Now()
	providerAccountID, channelID := seedBillingChannel(t, pool, "outside-window-test")
	// 种在目标日期之前一天:不应该被计入。
	seedBilledRequestLog(t, pool, accountID, channelID, day.Add(-25*time.Hour), 7_000)

	fetcher := &mockBillingFetcher{items: nil}
	drifts, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, day)
	if err != nil {
		t.Fatalf("CheckBillingDrift: %v", err)
	}
	if d := findBillingDrift(drifts, providerAccountID, "outside-window-test"); d != nil {
		t.Errorf("unexpected drift entry for a row outside the day window: %+v", d)
	}
}

func TestCheckBillingDrift_FetcherError_Propagates(t *testing.T) {
	pool := testPool(t)
	r := New(pool)
	providerAccountID, _ := seedBillingChannel(t, pool, "error-propagation-test")

	fetcher := &mockBillingFetcher{err: fmt.Errorf("upstream billing API unavailable")}
	if _, err := r.CheckBillingDrift(context.Background(), fetcher, providerAccountID, time.Now()); err == nil {
		t.Error("expected CheckBillingDrift to propagate the fetcher error")
	}
}
