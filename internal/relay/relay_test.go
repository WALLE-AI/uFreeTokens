// 端到端集成测试：真实启动 app.NewGatewayRouter（和 cmd/gateway/main.go 一样的装配方式），
// 打真实 HTTP 请求，上游用 httptest mock server 顶替，账户/钱包/价格数据写入真实
// PostgreSQL（tools/devdb，无需 Docker）。验证的是"鉴权 -> 路由 -> 转发 -> 计费"这条
// 完整链路真的能跑通，而不只是各个包的单元测试凑在一起。
//
// 用 relay_test 这个外部测试包（而不是 relay 包内部测试），是因为需要引用
// internal/app（它反过来依赖 internal/relay），避免包循环依赖。
package relay_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/relay"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

const defaultTestDSN = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"
const testPepper = "relay-e2e-test-pepper"

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

type fixture struct {
	pool      *pgxpool.Pool
	accountID int64
	apiKey    string
}

// seed 建一个账户（带余额）+ API Key + 指向 upstreamURL 的完整 Provider/Channel/Price 链，
// 虚拟模型名带随机后缀避免测试之间互相干扰（不同测试并行/重复运行时不会撞名字）。
func seed(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, upstreamURL string, cashMicro int64) (fx fixture, vmName string) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (type, name, status, tier) VALUES ('personal', $1, 'active', 'free') RETURNING id`,
		"relay-e2e-"+suffix,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (account_id, cash_balance) VALUES ($1, $2)`, accountID, cashMicro); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	key, err := auth.GenerateAPIKey([]byte(testPepper))
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO api_keys (account_id, name, display_prefix, key_hmac, status) VALUES ($1, 'e2e', $2, $3, 'active')`,
		accountID, key.DisplayPrefix, key.HMAC,
	); err != nil {
		t.Fatalf("insert api_key: %v", err)
	}

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol, status) VALUES ($1, 'Mock', 'openai', 'active') RETURNING id`,
		"mock-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	var accID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (provider_id, name, base_url, status) VALUES ($1, 'mock-account', $2, 'active') RETURNING id`,
		providerID, upstreamURL,
	).Scan(&accID); err != nil {
		t.Fatalf("insert provider_account: %v", err)
	}

	sealed, err := box.Seal("sk-mock-upstream-secret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO provider_keys (provider_account_id, secret_ciphertext, secret_dek_wrapped, secret_last4, weight, status)
		 VALUES ($1, $2, $3, 'cret', 100, 'active')`,
		accID, sealed.Ciphertext, sealed.WrappedDEK,
	); err != nil {
		t.Fatalf("insert provider_key: %v", err)
	}

	vmName = "e2e-model-" + suffix
	var vmID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, 'test', 'chat', 128000, 8192, '{stream}', '{free}', 'active') RETURNING id`,
		vmName,
	).Scan(&vmID); err != nil {
		t.Fatalf("insert virtual_model: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, status)
		 VALUES ($1, $2, 'mock-upstream-model', 0, 100, 'active')`,
		vmID, accID,
	); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	var bookID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_books (kind, virtual_model_id, currency, effective_from) VALUES ('sell', $1, 'CNY', now() - interval '1 hour') RETURNING id`,
		vmID,
	).Scan(&bookID); err != nil {
		t.Fatalf("insert price_book: %v", err)
	}
	// 1 元/百万 token，方便手算预期扣费。
	if _, err := pool.Exec(ctx,
		`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, unit_price)
		 VALUES ($1,'input','per_1m_tokens','default',0,1), ($1,'output','per_1m_tokens','default',0,1)`,
		bookID,
	); err != nil {
		t.Fatalf("insert price_components: %v", err)
	}

	return fixture{pool: pool, accountID: accountID, apiKey: key.Raw}, vmName
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

// newTestGateway 用和 cmd/gateway/main.go 相同的装配方式组一个可用的网关 http.Handler。
func newTestGateway(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box) http.Handler {
	t.Helper()
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	relaySvc := &relay.Service{
		Catalog:  catalog.NewStore(pool, box, 0), // TTL=0：每次 Get 都重新加载，测试里数据是即时写入的
		Wallet:   wallet.New(pool),
		Adapters: adapter.NewRegistry(),
		HTTP:     http.DefaultClient,
		Logger:   logger,
		Cfg:      relay.DefaultConfig(),
	}
	return app.NewGatewayRouter(app.GatewayDeps{
		Logger:    logger,
		PG:        pool,
		AuthStore: auth.NewPostgresStore(pool),
		Pepper:    []byte(testPepper),
		Relay:     relaySvc,
	})
}

func getWalletBalance(t *testing.T, pool *pgxpool.Pool, accountID int64) (cash, frozen int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT cash_balance, frozen FROM wallets WHERE account_id = $1`, accountID,
	).Scan(&cash, &frozen); err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	return
}

func TestChatCompletions_NonStream_ChargesActualUsage(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("upstream got unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-mock-upstream-secret" {
			t.Errorf("upstream Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "up-1", "model": "mock-upstream-model",
			"choices": [{"message": {"role":"assistant","content":"hi"}}],
			"usage": {"prompt_tokens": 1000, "completion_tokens": 2000}
		}`))
	}))
	defer upstream.Close()

	fx, vmName := seed(t, pool, box, upstream.URL, 1_000_000) // 1 元
	gw := httptest.NewServer(newTestGateway(t, pool, box))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, respBody)
	}
	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, respBody)
	}
	if m["model"] != vmName {
		t.Errorf("model = %v, want %v (must not leak upstream model name)", m["model"], vmName)
	}
	if strings.Contains(fmt.Sprint(m["id"]), "up-1") {
		t.Errorf("response leaks upstream request id: %v", m["id"])
	}

	// 用量 1000 input + 2000 output，单价都是 1 元/百万 token：
	// (1000*1 + 2000*1) / 1e6 元 = 0.003 元 = 3000 微元。
	wantCharge := int64(3000)

	// 结算是 handler 返回响应之后、通过独立 context 异步完成的写入，但在 handler
	// 返回给 HTTP 客户端之前已经在同一个函数调用栈里同步执行完（settleQuietly 内部
	// 虽然用了 WithoutCancel，但没有另起 goroutine），所以这里直接读取应该已经生效；
	// 加一个小的重试规避极端调度延迟，避免测试偶发失败。
	var cash, frozen int64
	for i := 0; i < 20; i++ {
		cash, frozen = getWalletBalance(t, pool, fx.accountID)
		if cash != 1_000_000 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0 (reservation must be fully settled)", frozen)
	}
}

func TestChatCompletions_Stream_ChargesFromFinalUsageChunk(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for _, line := range []string{
			`data: {"id":"up-1","choices":[{"delta":{"content":"He"}}]}` + "\n\n",
			`data: {"id":"up-1","choices":[{"delta":{"content":"llo"}}]}` + "\n\n",
			`data: {"id":"up-1","choices":[],"usage":{"prompt_tokens":500,"completion_tokens":100}}` + "\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = w.Write([]byte(line))
			fl.Flush()
		}
	}))
	defer upstream.Close()

	fx, vmName := seed(t, pool, box, upstream.URL, 1_000_000)
	gw := httptest.NewServer(newTestGateway(t, pool, box))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{"model": vmName, "stream": true, "messages": []any{}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), vmName) {
		t.Errorf("stream body should contain rewritten model name %q: %s", vmName, body)
	}
	if strings.Contains(string(body), "up-1") {
		t.Errorf("stream body leaks upstream id: %s", body)
	}

	// 500 input + 100 output, 单价 1 元/百万 -> (500+100)/1e6 元 = 600 微元。
	wantCharge := int64(600)
	var cash int64
	for i := 0; i < 20; i++ {
		cash, _ = getWalletBalance(t, pool, fx.accountID)
		if cash != 1_000_000 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
}

func TestChatCompletions_InsufficientBalance_Returns402AndChargesNothing(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when balance is insufficient")
	}))
	defer upstream.Close()

	fx, vmName := seed(t, pool, box, upstream.URL, 0) // 余额为 0
	gw := httptest.NewServer(newTestGateway(t, pool, box))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model": vmName, "max_tokens": float64(1000),
		"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("x", 2000)}},
	})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 402, body = %s", resp.StatusCode, body)
	}

	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 0 || frozen != 0 {
		t.Errorf("wallet mutated on rejected request: cash=%d frozen=%d", cash, frozen)
	}
}

func TestChatCompletions_UpstreamError_ReleasesReservation(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer upstream.Close()

	fx, vmName := seed(t, pool, box, upstream.URL, 1_000_000)
	gw := httptest.NewServer(newTestGateway(t, pool, box))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{"model": vmName, "messages": []any{}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, body)
	}

	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 1_000_000 {
		t.Errorf("cash_balance = %d, want unchanged 1_000_000 (failed upstream call must not charge)", cash)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0 (reservation must be released on upstream failure)", frozen)
	}
}

func TestChatCompletions_ModelNotFound(t *testing.T) {
	pool := testPool(t)
	box := testBox(t)
	fx, _ := seed(t, pool, box, "http://unused.invalid", 1_000_000)
	gw := httptest.NewServer(newTestGateway(t, pool, box))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{"model": "does-not-exist", "messages": []any{}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body = %s", resp.StatusCode, body)
	}
}
