// 端到端集成测试：真实启动 app.NewGatewayRouter（和 cmd/gateway/main.go 一样的装配方式），
// 打真实 HTTP 请求，上游用 httptest mock server 顶替，账户/钱包/价格/健康度数据分别
// 写入真实 PostgreSQL 和 Redis（tools/devdb + 本机 Redis/Memurai，无需 Docker）。
// 验证的是"鉴权 -> 预扣 -> 路由 -> 转发 -> (失败时换 Key/换渠道重试) -> 结算"这条
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/health"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/relay"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

const defaultTestDSN = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"
const defaultTestRedisAddr = "localhost:6379"
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

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("UFT_TEST_REDIS_ADDR")
	if addr == "" {
		addr = defaultTestRedisAddr
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("skipping: redis not reachable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
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

// --- 可组合的种子数据函数：每个测试按需拼出自己的 account/channel/key 拓扑 ---

type fixture struct {
	pool      *pgxpool.Pool
	accountID int64
	apiKey    string
}

func seedAccount(t *testing.T, pool *pgxpool.Pool, cashMicro int64) fixture {
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
	return fixture{pool: pool, accountID: accountID, apiKey: key.Raw}
}

// seedProviderAccount 建一个指向 upstreamURL 的 provider + provider_account，返回其 ID。
func seedProviderAccount(t *testing.T, pool *pgxpool.Pool, upstreamURL string) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

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
	return accID
}

// seedKey 给某个 provider_account 添加一个上游 Key，明文 secret 由调用方指定
// （用于在 mock 上游里按 Authorization 头区分"这是哪个 Key 发起的请求"）。返回 Key 的 DB ID。
func seedKey(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, accountID int64, secret string) int64 {
	t.Helper()
	sealed, err := box.Seal(secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	var keyID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO provider_keys (provider_account_id, secret_ciphertext, secret_dek_wrapped, secret_last4, weight, status)
		 VALUES ($1, $2, $3, 'cret', 100, 'active') RETURNING id`,
		accountID, sealed.Ciphertext, sealed.WrappedDEK,
	).Scan(&keyID); err != nil {
		t.Fatalf("insert provider_key: %v", err)
	}
	return keyID
}

// seedVirtualModel 建一个虚拟模型 + 对应的售价（1 元/百万 token，方便手算预期扣费）。
func seedVirtualModel(t *testing.T, pool *pgxpool.Pool) (vmID int64, vmName string) {
	t.Helper()
	ctx := context.Background()
	vmName = fmt.Sprintf("e2e-model-%d", time.Now().UnixNano())

	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, 'test', 'chat', 128000, 8192, '{stream}', '{free}', 'active') RETURNING id`,
		vmName,
	).Scan(&vmID); err != nil {
		t.Fatalf("insert virtual_model: %v", err)
	}

	var bookID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_books (kind, virtual_model_id, currency, effective_from) VALUES ('sell', $1, 'CNY', now() - interval '1 hour') RETURNING id`,
		vmID,
	).Scan(&bookID); err != nil {
		t.Fatalf("insert price_book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, unit_price)
		 VALUES ($1,'input','per_1m_tokens','default',0,1), ($1,'output','per_1m_tokens','default',0,1)`,
		bookID,
	); err != nil {
		t.Fatalf("insert price_components: %v", err)
	}
	return vmID, vmName
}

// seedChannel 把虚拟模型接到某个 provider_account 上，priority 越小越优先。
func seedChannel(t *testing.T, pool *pgxpool.Pool, vmID, providerAccountID int64, priority int) int64 {
	t.Helper()
	var channelID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, status)
		 VALUES ($1, $2, 'mock-upstream-model', $3, 100, 'active') RETURNING id`,
		vmID, providerAccountID, priority,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return channelID
}

// seedSimple 是最常用拓扑的便捷封装：1 账户 + 1 上游账号(1 Key) + 1 虚拟模型 + 1 渠道，
// Key 的明文固定为 "sk-mock-upstream-secret"。
func seedSimple(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, upstreamURL string, cashMicro int64) (fx fixture, vmName string) {
	t.Helper()
	fx = seedAccount(t, pool, cashMicro)
	accID := seedProviderAccount(t, pool, upstreamURL)
	seedKey(t, pool, box, accID, "sk-mock-upstream-secret")
	vmID, name := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, accID, 0)
	return fx, name
}

// newTestGateway 用和 cmd/gateway/main.go 相同的装配方式组一个可用的网关 http.Handler，
// 包括真实 Redis 支撑的健康度/熔断注册表（每次调用都是全新的 Registry，测试间不会串状态）。
// newTestGateway 返回一个可用的网关 http.Handler，以及背后的 reqlog.Writer
// （测试可以调用它的 Close() 强制立即 flush，不用等 1 秒定时器）。
func newTestGateway(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, rdb *redis.Client) (http.Handler, *reqlog.Writer) {
	t.Helper()
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	reqLogWriter := reqlog.NewWriter(pool, logger)
	relaySvc := &relay.Service{
		Catalog:  catalog.NewStore(pool, box, 0), // TTL=0：每次 Get 都重新加载，测试里数据是即时写入的
		Wallet:   wallet.New(pool),
		Adapters: adapter.NewRegistry(),
		HTTP:     http.DefaultClient,
		Health:   health.NewRegistry(rdb, health.DefaultBreakerSettings()),
		ReqLog:   reqLogWriter,
		Logger:   logger,
		Cfg:      relay.DefaultConfig(),
	}
	h := app.NewGatewayRouter(app.GatewayDeps{
		Logger:    logger,
		PG:        pool,
		AuthStore: auth.NewPostgresStore(pool),
		Pepper:    []byte(testPepper),
		Relay:     relaySvc,
	})
	return h, reqLogWriter
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

func doChatCompletion(t *testing.T, gwURL, apiKey string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func TestChatCompletions_NonStream_ChargesActualUsage(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

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

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000) // 1 元
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{
		"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
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
	cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0 (reservation must be fully settled)", frozen)
	}
}

func TestChatCompletions_Stream_ChargesFromFinalUsageChunk(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

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

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "stream": true, "messages": []any{}})
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
	cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
}

func TestChatCompletions_InsufficientBalance_Returns402AndChargesNothing(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when balance is insufficient")
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 0) // 余额为 0
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{
		"model": vmName, "max_tokens": float64(1000),
		"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("x", 2000)}},
	})
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

func TestChatCompletions_ModelNotFound(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	fx, _ := seedSimple(t, pool, box, "http://unused.invalid", 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": "does-not-exist", "messages": []any{}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body = %s", resp.StatusCode, body)
	}
}

// TestChatCompletions_SingleKeyRateLimited_NoMoreOptionsReturns503 用单渠道单 Key，
// 上游总是 429。换 Key 重试时发现没有别的 Key 可换，router 报告"无可用渠道"——
// 这是技术方案 §7.7 里"重试耗尽 vs 彻底没有候选"两种情况中的后者，返回 503 而不是 502。
func TestChatCompletions_SingleKeyRateLimited_NoMoreOptionsReturns503(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, body)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("upstream call count = %d, want 1 (only one key exists, no point retrying it)", calls)
	}

	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 1_000_000 || frozen != 0 {
		t.Errorf("wallet mutated: cash=%d frozen=%d, want cash=1_000_000 frozen=0", cash, frozen)
	}
}

// TestChatCompletions_BadRequest_DoesNotRetry 验证 400 类错误不会触发任何重试——
// 换 Key/换渠道对"请求本身有问题"无济于事，还可能被误用于规避内容审核。
func TestChatCompletions_BadRequest_DoesNotRetry(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, body)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("upstream call count = %d, want exactly 1 (400 must not be retried)", got)
	}

	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 1_000_000 || frozen != 0 {
		t.Errorf("wallet mutated: cash=%d frozen=%d", cash, frozen)
	}
}

// TestChatCompletions_RetriesAcrossKeys_SucceedsWithSecondKey 同一渠道两个 Key，
// 一个总是 429，另一个总是成功——无论 router 先随机选到哪一个，最终都应该在
// MaxAttempts(3) 以内换到能用的 Key 并成功计费。
func TestChatCompletions_RetriesAcrossKeys_SucceedsWithSecondKey(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	const goodSecret = "sk-good-key"
	const badSecret = "sk-bad-key"
	var goodCalls, badCalls int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer " + goodSecret:
			atomic.AddInt32(&goodCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 100, "completion_tokens": 50}}`))
		case "Bearer " + badSecret:
			atomic.AddInt32(&badCalls, 1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
		default:
			t.Errorf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000)
	accID := seedProviderAccount(t, pool, upstream.URL)
	seedKey(t, pool, box, accID, goodSecret)
	seedKey(t, pool, box, accID, badSecret)
	vmID, vmName := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, accID, 0)

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (should eventually succeed via the good key), body = %s", resp.StatusCode, body)
	}
	if atomic.LoadInt32(&goodCalls) != 1 {
		t.Errorf("good key call count = %d, want 1", goodCalls)
	}

	// 150 token 合计（100 input + 50 output），单价 1 元/百万 -> 150 微元。
	cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-150 {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-150)
	}
}

// TestChatCompletions_RetriesAcrossChannels_FallsBackOnUpstreamUnavailable 验证主渠道
// 5xx 时会切换到备用渠道（priority 更大），而不是直接失败——优先级分层是确定性的，
// 所以这个测试不依赖随机数，每次都应该先打到 primary 再打到 fallback。
func TestChatCompletions_RetriesAcrossChannels_FallsBackOnUpstreamUnavailable(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	var primaryCalls int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"internal error"}}`))
	}))
	defer primary.Close()

	var fallbackCalls int32
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 200, "completion_tokens": 300}}`))
	}))
	defer fallback.Close()

	fx := seedAccount(t, pool, 1_000_000)
	primaryAcc := seedProviderAccount(t, pool, primary.URL)
	seedKey(t, pool, box, primaryAcc, "sk-primary")
	fallbackAcc := seedProviderAccount(t, pool, fallback.URL)
	seedKey(t, pool, box, fallbackAcc, "sk-fallback")

	vmID, vmName := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, primaryAcc, 0)  // priority 0：主
	seedChannel(t, pool, vmID, fallbackAcc, 1) // priority 1：备

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (should fall back to the secondary channel), body = %s", resp.StatusCode, body)
	}
	if atomic.LoadInt32(&primaryCalls) != 1 {
		t.Errorf("primary call count = %d, want 1 (must have been tried first, deterministically)", primaryCalls)
	}
	if atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Errorf("fallback call count = %d, want 1", fallbackCalls)
	}

	// 500 token 合计（200 input + 300 output）-> 500 微元。
	cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-500 {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-500)
	}
}

// TestChatCompletions_ExhaustsMaxAttempts_Returns502 用 3 个 Key（>= 默认 MaxAttempts=3）
// 全部返回 429，验证重试预算耗尽后返回 502（而不是"无可用渠道"的 503——此时明明还有
// 没试过的 Key，只是攒够了尝试次数，语义上更接近"上游一直不给面子"）。
func TestChatCompletions_ExhaustsMaxAttempts_Returns502(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000)
	accID := seedProviderAccount(t, pool, upstream.URL)
	seedKey(t, pool, box, accID, "sk-1")
	seedKey(t, pool, box, accID, "sk-2")
	seedKey(t, pool, box, accID, "sk-3")
	vmID, vmName := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, accID, 0)

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, body)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("upstream call count = %d, want 3 (default MaxAttempts, 3 distinct keys available)", got)
	}

	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 1_000_000 || frozen != 0 {
		t.Errorf("wallet mutated: cash=%d frozen=%d, want cash=1_000_000 frozen=0", cash, frozen)
	}
}

// TestChatCompletions_LogsSuccessToRequestLogs 验证一次成功请求会在 request_logs
// 里留下完整的审计记录：渠道/Key、计费快照、用量、尝试次数（技术方案 §6.8/§7.13）。
func TestChatCompletions_LogsSuccessToRequestLogs(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 100, "completion_tokens": 50}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	requestID := resp.Header.Get("X-Request-Id")
	if requestID == "" {
		t.Fatal("response missing X-Request-Id header")
	}

	reqLogW.Close() // 强制 flush，不用等 1 秒定时器

	var (
		status                       string
		httpStatus, attempts         int
		channelID, keyID, sellBookID *int64
		inputTokens, outputTokens    int64
		chargedAmount                *int64
		usageSource                  string
	)
	err := pool.QueryRow(context.Background(),
		`SELECT status, http_status, attempts, channel_id, provider_key_id, sell_price_book_id,
		        input_tokens, output_tokens, charged_amount, usage_source
		 FROM request_logs WHERE request_id = $1`, requestID,
	).Scan(&status, &httpStatus, &attempts, &channelID, &keyID, &sellBookID,
		&inputTokens, &outputTokens, &chargedAmount, &usageSource)
	if err != nil {
		t.Fatalf("query request_logs: %v", err)
	}

	if status != "success" {
		t.Errorf("status = %q, want success", status)
	}
	if httpStatus != http.StatusOK {
		t.Errorf("http_status = %d, want 200", httpStatus)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if channelID == nil || keyID == nil {
		t.Error("channel_id/provider_key_id should be recorded for a successful request")
	}
	if inputTokens != 100 || outputTokens != 50 {
		t.Errorf("tokens = %d/%d, want 100/50", inputTokens, outputTokens)
	}
	// 150 token 合计，单价 1 元/百万 -> 150 微元。
	if chargedAmount == nil || *chargedAmount != 150 {
		t.Errorf("charged_amount = %v, want 150", chargedAmount)
	}
	if sellBookID == nil {
		t.Error("sell_price_book_id should be recorded")
	}
	if usageSource != "upstream" {
		t.Errorf("usage_source = %q, want upstream", usageSource)
	}
}

// TestChatCompletions_LogsFailureToRequestLogs 验证重试耗尽后的失败也会留下审计记录，
// 且 attempt_trace 里包含每一次尝试的渠道/Key/结果。
func TestChatCompletions_LogsFailureToRequestLogs(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	resp.Body.Close()
	requestID := resp.Header.Get("X-Request-Id")
	if requestID == "" {
		t.Fatal("response missing X-Request-Id header")
	}

	reqLogW.Close()

	var (
		status               string
		httpStatus, attempts int
		errorCode            *string
		traceJSON            []byte
	)
	err := pool.QueryRow(context.Background(),
		`SELECT status, http_status, error_code, attempts, attempt_trace FROM request_logs WHERE request_id = $1`,
		requestID,
	).Scan(&status, &httpStatus, &errorCode, &attempts, &traceJSON)
	if err != nil {
		t.Fatalf("query request_logs: %v", err)
	}

	if status != "upstream_error" {
		t.Errorf("status = %q, want upstream_error", status)
	}
	if errorCode == nil || *errorCode != "no_available_channel" {
		// 只有 1 个渠道/1 个 Key：第一次尝试失败后，重试时发现无候选可换，
		// 归类为"无可用渠道"而不是"重试耗尽"，与 §7.7 的两种失败语义一致。
		t.Errorf("error_code = %v, want no_available_channel", errorCode)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}

	var trace []map[string]any
	if err := json.Unmarshal(traceJSON, &trace); err != nil {
		t.Fatalf("unmarshal attempt_trace: %v", err)
	}
	if len(trace) != 1 {
		t.Fatalf("attempt_trace length = %d, want 1", len(trace))
	}
	if trace[0]["status"] != "upstream_unavailable" {
		t.Errorf("attempt_trace[0].status = %v, want upstream_unavailable", trace[0]["status"])
	}
}

// awaitSettled 轮询等待结算完成（settleQuietly 在 handler 返回响应前同步执行，
// 但给一点余量规避极端调度延迟），返回最终余额。
func awaitSettled(t *testing.T, pool *pgxpool.Pool, accountID, unsettledCash int64) (cash, frozen int64) {
	t.Helper()
	for i := 0; i < 20; i++ {
		cash, frozen = getWalletBalance(t, pool, accountID)
		if cash != unsettledCash {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	return
}
