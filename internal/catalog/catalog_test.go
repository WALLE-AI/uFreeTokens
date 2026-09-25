// 集成测试：连真实 PostgreSQL 验证 TEXT[]/JSONB 等字段的 pgx 扫描确实按预期工作
// （这类问题在纯 mock 测试里发现不了）。默认连 tools/devdb 起的本机实例，
// 连不上时自动跳过，见 internal/wallet 的测试注释。
package catalog

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
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

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	box, err := secretbox.NewBox(base64.StdEncoding.EncodeToString(b))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

// seedFullChain 建一条完整的 Provider -> ProviderAccount -> ProviderKey -> VirtualModel
// -> Channel -> sell PriceBook -> PriceComponents，覆盖所有需要验证的字段类型
// （TEXT[]、JSONB、NUMERIC、可空的 SMALLINT 时段字段）。
func seedFullChain(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box) (vmName string, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol, currency, status) VALUES ($1, 'Test Provider', 'openai', 'CNY', 'active') RETURNING id`,
		"test-provider-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (provider_id, name, base_url, cost_multiplier, status)
		 VALUES ($1, 'test-account', 'https://api.example.com/v1', 0.9, 'active') RETURNING id`,
		providerID,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert provider_account: %v", err)
	}

	sealed, err := box.Seal("sk-upstream-real-secret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO provider_keys (provider_account_id, secret_ciphertext, secret_dek_wrapped, secret_last4, weight, status)
		 VALUES ($1, $2, $3, 'cret', 100, 'active')`,
		accountID, sealed.Ciphertext, sealed.WrappedDEK,
	); err != nil {
		t.Fatalf("insert provider_key: %v", err)
	}

	vmName = "test-vm-" + suffix
	var vmID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, 'test', 'chat', 128000, 8192, '{stream,tools}', '{free,pro}', 'active') RETURNING id`,
		vmName,
	).Scan(&vmID); err != nil {
		t.Fatalf("insert virtual_model: %v", err)
	}

	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, param_overrides, allowed_tiers, status)
		 VALUES ($1, $2, 'upstream-model-name', 0, 100, '{"top_p": null}'::jsonb, '{free,pro}', 'active') RETURNING id`,
		vmID, accountID,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	var bookID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_books (kind, virtual_model_id, currency, effective_from)
		 VALUES ('sell', $1, 'CNY', now() - interval '1 hour') RETURNING id`,
		vmID,
	).Scan(&bookID); err != nil {
		t.Fatalf("insert price_book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, unit_price)
		 VALUES ($1, 'input', 'per_1m_tokens', 'default', 0, 4.635), ($1, 'output', 'per_1m_tokens', 'default', 0, 18.54)`,
		bookID,
	); err != nil {
		t.Fatalf("insert price_components: %v", err)
	}

	cleanup = func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM price_components WHERE price_book_id = $1`, bookID)
		_, _ = pool.Exec(ctx, `DELETE FROM price_books WHERE id = $1`, bookID)
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID)
		_, _ = pool.Exec(ctx, `DELETE FROM virtual_models WHERE id = $1`, vmID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_keys WHERE provider_account_id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE id = $1`, providerID)
	}
	return vmName, cleanup
}

func TestStore_LoadFullChainFromRealPostgres(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)
	vmName, cleanup := seedFullChain(t, pool, box)
	defer cleanup()

	store := NewStore(pool, box, time.Hour)
	snap, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	vm, ok := snap.Models[vmName]
	if !ok {
		t.Fatalf("virtual model %q not found in snapshot", vmName)
	}
	if !vm.HasCapability("tools") {
		t.Errorf("expected capabilities to include 'tools', got %v (TEXT[] scan bug?)", vm.Capabilities)
	}
	if len(vm.VisibleTiers) != 2 {
		t.Errorf("visible_tiers = %v, want 2 elements", vm.VisibleTiers)
	}

	channels := snap.ChannelsByVM[vm.ID]
	if len(channels) != 1 {
		t.Fatalf("got %d channels, want 1", len(channels))
	}
	ch := channels[0]
	if ch.UpstreamModel != "upstream-model-name" {
		t.Errorf("UpstreamModel = %q", ch.UpstreamModel)
	}
	if v, ok := ch.ParamOverrides["top_p"]; !ok || v != nil {
		t.Errorf("ParamOverrides = %v, want {top_p: nil} (JSONB scan bug?)", ch.ParamOverrides)
	}
	if !ch.AllowsTier("free") || ch.AllowsTier("enterprise") {
		t.Errorf("AllowedTiers = %v (TEXT[] scan bug?)", ch.AllowedTiers)
	}

	keys := snap.KeysByAccount[ch.ProviderAccountID]
	if len(keys) != 1 {
		t.Fatalf("got %d provider keys, want 1", len(keys))
	}
	if keys[0].Secret != "sk-upstream-real-secret" {
		t.Errorf("decrypted secret = %q, want sk-upstream-real-secret (envelope decryption via loaded snapshot failed)", keys[0].Secret)
	}

	account := snap.ProviderAccounts[ch.ProviderAccountID]
	if account == nil {
		t.Fatal("provider account not found in snapshot")
	}
	if !account.CostMultiplier.Equal(decimal.NewFromFloat(0.9)) {
		t.Errorf("CostMultiplier = %s, want 0.9 (NUMERIC scan)", account.CostMultiplier.String())
	}

	book, ok := snap.SellPriceBooks[vm.ID]
	if !ok {
		t.Fatal("sell price book not found in snapshot")
	}
	if len(book.Components) != 2 {
		t.Fatalf("got %d price components, want 2", len(book.Components))
	}
}

func TestStore_CachesWithinTTL(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)
	vmName, cleanup := seedFullChain(t, pool, box)
	defer cleanup()

	store := NewStore(pool, box, time.Hour) // 长 TTL：第二次 Get 应该走缓存
	first, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}

	// 缓存生效期间往数据库里加一个新模型，不应该出现在第二次 Get 的结果里。
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, status) VALUES ($1, 'x', 'chat', 1000, 100, 'active')`,
		"should-not-appear-"+vmName,
	); err != nil {
		t.Fatalf("insert extra model: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM virtual_models WHERE name = $1`, "should-not-appear-"+vmName) //nolint:errcheck

	second, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if second != first {
		t.Error("expected cached snapshot pointer to be reused within TTL")
	}
	if _, ok := second.Models["should-not-appear-"+vmName]; ok {
		t.Error("newly inserted model should not appear before TTL expiry")
	}
}

// TestStore_LoadCostPrices 验证成本价（挂渠道，非 CNY 币种也会被加载——是否可用
// 由调用方按 Currency 字段自行判断，见 internal/relay.computeCostAmount）。
func TestStore_LoadCostPrices(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol, status) VALUES ($1, 'Cost Test', 'openai', 'active') RETURNING id`,
		"cost-provider-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (provider_id, name, base_url, cost_multiplier, status)
		 VALUES ($1, 'cost-account', 'https://api.example.com/v1', 0.85, 'active') RETURNING id`,
		providerID,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert provider_account: %v", err)
	}
	vmName := "cost-test-vm-" + suffix
	var vmID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, status)
		 VALUES ($1, 'test', 'chat', 128000, 8192, 'active') RETURNING id`,
		vmName,
	).Scan(&vmID); err != nil {
		t.Fatalf("insert virtual_model: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, status)
		 VALUES ($1, $2, 'upstream-model', 0, 100, 'active') RETURNING id`,
		vmID, accountID,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	var bookID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_books (kind, channel_id, currency, effective_from)
		 VALUES ('cost', $1, 'CNY', now() - interval '1 hour') RETURNING id`,
		channelID,
	).Scan(&bookID); err != nil {
		t.Fatalf("insert cost price_book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, unit_price)
		 VALUES ($1, 'input', 'per_1m_tokens', 'default', 0, 4.5), ($1, 'output', 'per_1m_tokens', 'default', 0, 18)`,
		bookID,
	); err != nil {
		t.Fatalf("insert cost price_components: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM price_components WHERE price_book_id = $1`, bookID)
		_, _ = pool.Exec(ctx, `DELETE FROM price_books WHERE id = $1`, bookID)
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID)
		_, _ = pool.Exec(ctx, `DELETE FROM virtual_models WHERE id = $1`, vmID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE id = $1`, providerID)
	})

	store := NewStore(pool, box, time.Hour)
	snap, err := store.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	book, ok := snap.CostPriceBooks[channelID]
	if !ok {
		t.Fatal("expected a cost price book for this channel, found none")
	}
	if book.Currency != "CNY" {
		t.Errorf("Currency = %q, want CNY", book.Currency)
	}
	if len(book.Components) != 2 {
		t.Fatalf("got %d cost price components, want 2", len(book.Components))
	}

	// 注意：这里不去检查 snap.SellPriceBooks[channelID] 是否为空来验证"成本价没有
	// 混进售价里"——SellPriceBooks 按 virtual_model_id 索引、CostPriceBooks 按
	// channel_id 索引，是两个独立的 Go map 字段，这件事从类型和赋值路径上就已经
	// 成立，不需要再用运行时断言验证；而两者的主键都来自各自独立的序列，长期运行
	// 的开发库里 channelID 和某个不相关的 vmID 数值刚好相等完全可能发生，用它做
	// 断言只会产生误报（第一次这么写就真的报错了，原因和这条 channel 本身毫无关系）。
	if book.ID != bookID {
		t.Errorf("book.ID = %d, want %d", book.ID, bookID)
	}
}
