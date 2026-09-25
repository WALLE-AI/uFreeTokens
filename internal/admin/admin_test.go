// 集成测试：连真实 PostgreSQL（tools/devdb，无需 Docker）。
package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// uniqueCode 生成一个本次测试专属的 provider code——providers.code 有 UNIQUE
// 约束，这个开发库是长期共享、反复运行同一批测试的，固定字面量的 code 在第二次
// 运行时就会撞上第一次运行留下的行（provider 不是仅追加表，本可以在测试里删掉，
// 但用不会撞车的名字更省事）。
func uniqueCode(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

const defaultTestDSN = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"
const testPepper = "admin-test-pepper"

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

func newService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	return New(pool, wallet.New(pool), testBox(t), []byte(testPepper))
}

func TestCreateAccount_InitializesEmptyWallet(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	acct, err := s.CreateAccount(context.Background(), CreateAccountInput{Type: "personal", Name: "acme"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if acct.ID == 0 {
		t.Fatal("expected a non-zero account ID")
	}
	if acct.Tier != "free" {
		t.Errorf("Tier = %q, want default 'free'", acct.Tier)
	}

	got, w, err := s.GetAccount(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Name != "acme" || got.Status != "active" {
		t.Errorf("account = %+v", got)
	}
	if w.CashBalance != 0 || w.BonusBalance != 0 || w.Frozen != 0 {
		t.Errorf("wallet = %+v, want all zero", w)
	}
}

func TestCreateAccount_ValidatesInput(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, CreateAccountInput{Type: "bogus", Name: "x"}); err == nil {
		t.Error("expected error for invalid type")
	}
	if _, err := s.CreateAccount(ctx, CreateAccountInput{Type: "personal", Name: ""}); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := s.CreateAccount(ctx, CreateAccountInput{Type: "personal", Name: "x", CreditLimit: -1}); err == nil {
		t.Error("expected error for negative credit_limit")
	}
}

func TestGetAccount_NotFound(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	_, _, err := s.GetAccount(context.Background(), -1)
	if err != ErrAccountNotFound {
		t.Errorf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestAPIKeyLifecycle_CreateListRevoke(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	acct, err := s.CreateAccount(ctx, CreateAccountInput{Type: "personal", Name: "key-owner"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	created, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{AccountID: acct.ID, Name: "primary"})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !strings.HasPrefix(created.RawKey, auth.KeyPrefix) {
		t.Errorf("RawKey = %q, want prefix %q", created.RawKey, auth.KeyPrefix)
	}

	// 用真实的 auth.ComputeHMAC 验证生成的 Key 确实能通过网关鉴权路径校验——
	// 这条测试连的是 admin 包和 auth 包之间的契约，不只是 admin 自己内部的逻辑。
	mac, err := auth.ComputeHMAC([]byte(testPepper), created.RawKey)
	if err != nil {
		t.Fatalf("ComputeHMAC: %v", err)
	}
	authStore := auth.NewPostgresStore(pool)
	principal, err := authStore.FindByHMAC(ctx, mac)
	if err != nil {
		t.Fatalf("FindByHMAC: %v (the key created by admin.CreateAPIKey must authenticate)", err)
	}
	if principal.AccountID != acct.ID {
		t.Errorf("principal.AccountID = %d, want %d", principal.AccountID, acct.ID)
	}

	keys, err := s.ListAPIKeys(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != created.ID {
		t.Fatalf("ListAPIKeys = %+v, want exactly the created key", keys)
	}

	if err := s.RevokeAPIKey(ctx, created.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := authStore.FindByHMAC(ctx, mac); err != auth.ErrKeyDisabled {
		t.Errorf("FindByHMAC after revoke = %v, want ErrKeyDisabled", err)
	}

	// 幂等：再吊销一次不应该报错。
	if err := s.RevokeAPIKey(ctx, created.ID); err != nil {
		t.Errorf("RevokeAPIKey (idempotent second call): %v", err)
	}
}

func TestRevokeAPIKey_NotFound(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	if err := s.RevokeAPIKey(context.Background(), -1); err != ErrAPIKeyNotFound {
		t.Errorf("err = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestAddProviderKey_EncryptsAndRoundTrips(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)
	s := New(pool, wallet.New(pool), box, []byte(testPepper))
	ctx := context.Background()

	provider, err := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "Test", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	acc, err := s.CreateProviderAccount(ctx, CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}

	key, err := s.AddProviderKey(ctx, AddProviderKeyInput{ProviderAccountID: acc.ID, Secret: "sk-real-upstream-secret-abc123"})
	if err != nil {
		t.Fatalf("AddProviderKey: %v", err)
	}
	if key.Last4 != "c123" {
		t.Errorf("Last4 = %q, want c123", key.Last4)
	}

	var ciphertext, dek []byte
	if err := pool.QueryRow(ctx, `SELECT secret_ciphertext, secret_dek_wrapped FROM provider_keys WHERE id = $1`, key.ID).Scan(&ciphertext, &dek); err != nil {
		t.Fatalf("query stored key: %v", err)
	}
	if string(ciphertext) == "sk-real-upstream-secret-abc123" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	plaintext, err := box.Open(&secretbox.Sealed{Ciphertext: ciphertext, WrappedDEK: dek})
	if err != nil {
		t.Fatalf("decrypt stored key: %v", err)
	}
	if plaintext != "sk-real-upstream-secret-abc123" {
		t.Errorf("decrypted secret = %q, want the original plaintext", plaintext)
	}
}

func TestAddProviderKey_RefusesWithoutKEK(t *testing.T) {
	pool := testPool(t)
	s := New(pool, wallet.New(pool), nil, []byte(testPepper)) // 没有配置 box
	ctx := context.Background()

	provider, err := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	acc, err := s.CreateProviderAccount(ctx, CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}

	if _, err := s.AddProviderKey(ctx, AddProviderKeyInput{ProviderAccountID: acc.ID, Secret: "sk-x"}); err == nil {
		t.Fatal("expected an error when no KEK is configured, got nil (this would store a plaintext-adjacent secret)")
	}
}

func TestCreateVirtualModelAndChannel(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	provider, _ := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	acc, _ := s.CreateProviderAccount(ctx, CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://x"})

	vm, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{
		Name: uniqueCode(t), Family: "test", Type: "chat", ContextWindow: 128000, MaxOutput: 8192,
		Capabilities: []string{"stream"},
	})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	if len(vm.VisibleTiers) != 3 {
		t.Errorf("VisibleTiers = %v, want default 3-tier list", vm.VisibleTiers)
	}

	ch, err := s.CreateChannel(ctx, CreateChannelInput{
		VirtualModelID: vm.ID, ProviderAccountID: acc.ID, UpstreamModel: "upstream-name", Priority: 0,
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if ch.Weight != 100 {
		t.Errorf("Weight = %d, want default 100", ch.Weight)
	}
}

func TestCreateVirtualModel_ValidatesInput(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	if _, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{Name: "x", Type: "bogus", ContextWindow: 1, MaxOutput: 1}); err == nil {
		t.Error("expected error for invalid type")
	}
	if _, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{Name: "x", Type: "chat", ContextWindow: 0, MaxOutput: 1}); err == nil {
		t.Error("expected error for zero context_window")
	}
}

func TestSetSellPrice_CreatesNewVersionEachCall(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	vm, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}

	book1, err := s.SetSellPrice(ctx, SetSellPriceInput{
		VirtualModelID: vm.ID,
		Components: []PriceComponentInput{
			{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromFloat(4.635)},
			{Meter: "output", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromFloat(18.54)},
		},
	})
	if err != nil {
		t.Fatalf("SetSellPrice (first): %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_components WHERE price_book_id = $1`, book1).Scan(&count); err != nil {
		t.Fatalf("count price_components: %v", err)
	}
	if count != 2 {
		t.Errorf("price_components count = %d, want 2", count)
	}

	// 再发布一次：应该是一个新的 price_book，旧的那条不受影响、不会被覆盖或删除
	// （技术方案 §6.4：价格版本化，历史不可篡改）。
	book2, err := s.SetSellPrice(ctx, SetSellPriceInput{
		VirtualModelID: vm.ID,
		Components:     []PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromFloat(1)}},
	})
	if err != nil {
		t.Fatalf("SetSellPrice (second): %v", err)
	}
	if book2 == book1 {
		t.Fatal("second SetSellPrice must create a new price_book, not reuse the first")
	}

	var stillThere int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_components WHERE price_book_id = $1`, book1).Scan(&stillThere); err != nil {
		t.Fatalf("count price_components (old book): %v", err)
	}
	if stillThere != 2 {
		t.Errorf("old price_book's components = %d, want still 2 (must not be touched by the new version)", stillThere)
	}
}

func TestSetSellPrice_RejectsInvalidMeterOrUnit(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	vm, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}

	if _, err := s.SetSellPrice(ctx, SetSellPriceInput{
		VirtualModelID: vm.ID,
		Components:     []PriceComponentInput{{Meter: "not-a-meter", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(1)}},
	}); err == nil {
		t.Error("expected error for invalid meter")
	}
	if _, err := s.SetSellPrice(ctx, SetSellPriceInput{
		VirtualModelID: vm.ID,
		Components:     []PriceComponentInput{{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromInt(-1)}},
	}); err == nil {
		t.Error("expected error for negative unit_price")
	}
}

func TestSetCostPrice_CreatesVersionAndDefaultsToCNY(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	provider, err := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	acc, err := s.CreateProviderAccount(ctx, CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}
	vm, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	ch, err := s.CreateChannel(ctx, CreateChannelInput{VirtualModelID: vm.ID, ProviderAccountID: acc.ID, UpstreamModel: "up"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	bookID, err := s.SetCostPrice(ctx, SetCostPriceInput{
		ChannelID: ch.ID,
		Components: []PriceComponentInput{
			{Meter: "input", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromFloat(4.5)},
			{Meter: "output", Unit: "per_1m_tokens", UnitPrice: decimal.NewFromFloat(18)},
		},
	})
	if err != nil {
		t.Fatalf("SetCostPrice: %v", err)
	}

	var kind, currency string
	var channelID int64
	if err := pool.QueryRow(ctx, `SELECT kind, currency, channel_id FROM price_books WHERE id = $1`, bookID).
		Scan(&kind, &currency, &channelID); err != nil {
		t.Fatalf("query price_book: %v", err)
	}
	if kind != "cost" {
		t.Errorf("kind = %q, want cost", kind)
	}
	if currency != "CNY" {
		t.Errorf("currency = %q, want default CNY", currency)
	}
	if channelID != ch.ID {
		t.Errorf("channel_id = %d, want %d", channelID, ch.ID)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_components WHERE price_book_id = $1`, bookID).Scan(&count); err != nil {
		t.Fatalf("count price_components: %v", err)
	}
	if count != 2 {
		t.Errorf("price_components count = %d, want 2", count)
	}
}
