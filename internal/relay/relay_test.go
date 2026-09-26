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
	"github.com/WALLE-AI/uFreeTokens/internal/promotion"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
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
	return seedProviderAccountWithProtocol(t, pool, upstreamURL, "openai")
}

// seedProviderAccountWithProtocol 和 seedProviderAccount 一样，但允许指定
// providers.protocol（比如 "anthropic"），用于测试非 openai 协议适配器的完整链路。
func seedProviderAccountWithProtocol(t *testing.T, pool *pgxpool.Pool, upstreamURL, protocol string) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol, status) VALUES ($1, 'Mock', $2, 'active') RETURNING id`,
		"mock-"+suffix, protocol,
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

// seedCostPrice 给渠道种一条成本价（1 元/百万 token，input/output 各一档），
// 用于验证 relay 结算时会把 request_logs.cost_amount 算出来。
func seedCostPrice(t *testing.T, pool *pgxpool.Pool, channelID int64) {
	t.Helper()
	seedCostPriceWithCurrency(t, pool, channelID, "CNY")
}

// seedCostPriceWithCurrency 和 seedCostPrice 一样，但允许指定币种——用于测试
// 非 CNY 成本价配合 fx_rates 折算（技术方案 §7.16.9）的场景。
func seedCostPriceWithCurrency(t *testing.T, pool *pgxpool.Pool, channelID int64, currency string) {
	t.Helper()
	ctx := context.Background()
	var bookID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_books (kind, channel_id, currency, effective_from) VALUES ('cost', $1, $2, now() - interval '1 hour') RETURNING id`,
		channelID, currency,
	).Scan(&bookID); err != nil {
		t.Fatalf("insert cost price_book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, unit_price)
		 VALUES ($1,'input','per_1m_tokens','default',0,1), ($1,'output','per_1m_tokens','default',0,1)`,
		bookID,
	); err != nil {
		t.Fatalf("insert cost price_components: %v", err)
	}
}

// seedPriceDiscountPromotion 种一条对全部模型/tier 生效的打折促销（见 internal/promotion）。
// seedPriceDiscountPromotion 种一条打折促销。scope 限定到 vmName——`go test ./...`
// 会并发跑不同包的测试二进制，internal/promotion 的测试也在同一个真实数据库里插
// 促销数据；如果这里种成"不限模型"，两边的促销会互相串扰（之前真的因为这个栽过，
// 见对应的提交历史），所以促销必须限定到本次测试专属、带随机后缀的虚拟模型名。
func seedPriceDiscountPromotion(t *testing.T, pool *pgxpool.Pool, vmName string, discount float64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO promotions (name, side, type, priority, scope, params, starts_at, status)
		 VALUES ($1, 'sell', 'price_discount', 0, $2, $3, now() - interval '1 hour', 'active') RETURNING id`,
		fmt.Sprintf("e2e-discount-%d", time.Now().UnixNano()), map[string]any{"models": []string{vmName}}, map[string]any{"discount": discount},
	).Scan(&id); err != nil {
		t.Fatalf("seed price_discount promotion: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM promotions WHERE id = $1`, id)
	})
	return id
}

// seedFreeQuotaPromotion 种一条每日免费额度促销，同样限定到 vmName（理由同上）。
func seedFreeQuotaPromotion(t *testing.T, pool *pgxpool.Pool, vmName string, dailyAmountMicro int64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO promotions (name, side, type, priority, scope, params, starts_at, status)
		 VALUES ($1, 'sell', 'free_quota', 0, $2, $3, now() - interval '1 hour', 'active') RETURNING id`,
		fmt.Sprintf("e2e-freequota-%d", time.Now().UnixNano()), map[string]any{"models": []string{vmName}},
		map[string]any{"period": "daily", "amount_micro": dailyAmountMicro},
	).Scan(&id); err != nil {
		t.Fatalf("seed free_quota promotion: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_counters WHERE promotion_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM promotions WHERE id = $1`, id)
	})
	return id
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
	return newTestGatewayWithBudget(t, pool, box, rdb, nil)
}

// newTestGatewayWithBudget 和 newTestGateway 一样，但允许测试装一个自定义的
// RetryBudget（默认 nil = 不限制重试），用来测全局重试预算真的会在耗尽后
// 提前放弃重试（技术方案 §7.7）。
func newTestGatewayWithBudget(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, rdb *redis.Client, budget *relay.RetryBudget) (http.Handler, *reqlog.Writer) {
	t.Helper()
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	reqLogWriter := reqlog.NewWriter(pool, logger)
	relaySvc := &relay.Service{
		Catalog:     catalog.NewStore(pool, box, 0), // TTL=0：每次 Get 都重新加载，测试里数据是即时写入的
		Wallet:      wallet.New(pool),
		Adapters:    adapter.NewRegistry(),
		HTTP:        http.DefaultClient,
		Health:      health.NewRegistry(rdb, health.DefaultBreakerSettings()),
		RateLimit:   ratelimit.New(rdb, logger),
		Promotion:   promotion.New(pool),
		ReqLog:      reqLogWriter,
		Logger:      logger,
		Cfg:         relay.DefaultConfig(),
		RetryBudget: budget,
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

func intp(v int) *int { return &v }

// setAPIKeyLimits 给已经种好的 API Key 设置 RPM/TPM/并发限制（默认种子数据不限制任何一项）。
func setAPIKeyLimits(t *testing.T, pool *pgxpool.Pool, apiKey string, rpm, tpm, concurrency *int) {
	t.Helper()
	mac, err := auth.ComputeHMAC([]byte(testPepper), apiKey)
	if err != nil {
		t.Fatalf("ComputeHMAC: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE api_keys SET rpm_limit = $1, tpm_limit = $2, concurrency_limit = $3 WHERE key_hmac = $4`,
		rpm, tpm, concurrency, mac,
	); err != nil {
		t.Fatalf("update api_key limits: %v", err)
	}
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

// TestChatCompletions_AnthropicProtocol_NonStream_TranslatesAndCharges 验证一个
// providers.protocol='anthropic' 的渠道能走完整链路：网关收到 OpenAI 兼容请求，
// AnthropicAdapter 把它翻译成 Anthropic Messages API 请求打给上游，上游返回
// Anthropic 形状的响应，网关再翻译回 OpenAI 兼容响应给客户端，并按 Anthropic 的
// usage 字段正确计费——这是唯一一条真正验证"多协议路由"跑通的测试，其它测试
// 都只覆盖 openai 协议。
func TestChatCompletions_AnthropicProtocol_NonStream_TranslatesAndCharges(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Errorf("upstream got unexpected path %q, want /messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-mock-anthropic-secret" {
			t.Errorf("upstream x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Error("upstream did not receive anthropic-version header")
		}
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"role":"system"`) {
			t.Errorf("system message should have been pulled out of messages[] into a top-level system field: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_upstream_1",
			"content": [{"type":"text","text":"hi there"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1000, "output_tokens": 2000}
		}`))
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000) // 1 元
	accID := seedProviderAccountWithProtocol(t, pool, upstream.URL, "anthropic")
	seedKey(t, pool, box, accID, "sk-mock-anthropic-secret")
	vmID, vmName := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, accID, 0)

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{
		"model": vmName,
		"messages": []any{
			map[string]any{"role": "system", "content": "be terse"},
			map[string]any{"role": "user", "content": "hi"},
		},
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
	choices, _ := m["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v, want exactly 1 (translated from Anthropic's content blocks)", choices)
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "hi there" {
		t.Errorf("message.content = %v, want %q", message["content"], "hi there")
	}

	// 1000 input + 2000 output，单价都是 1 元/百万 token -> 3000 微元。
	wantCharge := int64(3000)
	cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0 (reservation must be fully settled)", frozen)
	}
}

// TestChatCompletions_AnthropicProtocol_Stream_TranslatesAndCharges 和上面的非流式
// 版本一样，但走流式路径：验证 AnthropicAdapter 的 SSE 事件翻译在真实网关链路里
// 也能正确转发内容、正确停止、正确计费。
func TestChatCompletions_AnthropicProtocol_Stream_TranslatesAndCharges(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for _, line := range []string{
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"usage":{"input_tokens":500}}}` + "\n\n",
			"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n",
			"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":100}}` + "\n\n",
			"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		} {
			_, _ = w.Write([]byte(line))
			fl.Flush()
		}
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000)
	accID := seedProviderAccountWithProtocol(t, pool, upstream.URL, "anthropic")
	seedKey(t, pool, box, accID, "sk-mock-anthropic-secret")
	vmID, vmName := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, accID, 0)

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
	if !strings.Contains(string(body), "Hello") {
		t.Errorf("stream body should contain the forwarded text: %s", body)
	}
	if !strings.Contains(string(body), vmName) {
		t.Errorf("stream body should contain rewritten model name %q: %s", vmName, body)
	}

	// 500 input + 100 output, 单价 1 元/百万 -> 600 微元。
	wantCharge := int64(600)
	cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
}

// TestChatCompletions_GeminiProtocol_NonStream_TranslatesAndCharges 是
// GeminiAdapter 的网关级验证，和上面 Anthropic 的两条测试是同一个思路：
// 一个 providers.protocol='gemini' 的渠道要能走完整链路，而不只是适配器单测通过。
func TestChatCompletions_GeminiProtocol_NonStream_TranslatesAndCharges(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("upstream got unexpected path %q, want suffix :generateContent", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "sk-mock-gemini-secret" {
			t.Errorf("upstream x-goog-api-key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{"content":{"parts":[{"text":"hi there"}]},"finishReason":"STOP"}],
			"usageMetadata": {"promptTokenCount": 1000, "candidatesTokenCount": 2000}
		}`))
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000) // 1 元
	accID := seedProviderAccountWithProtocol(t, pool, upstream.URL, "gemini")
	seedKey(t, pool, box, accID, "sk-mock-gemini-secret")
	vmID, vmName := seedVirtualModel(t, pool)
	seedChannel(t, pool, vmID, accID, 0)

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{
		"model":    vmName,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
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
	choices, _ := m["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "hi there" {
		t.Errorf("message.content = %v, want %q", message["content"], "hi there")
	}

	// 1000 input + 2000 output，单价都是 1 元/百万 token -> 3000 微元。
	wantCharge := int64(3000)
	cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-wantCharge {
		t.Errorf("cash_balance = %d, want %d", cash, 1_000_000-wantCharge)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0 (reservation must be fully settled)", frozen)
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

// TestChatCompletions_RetryBudgetExhausted_StopsRetryingEarly 验证全局重试预算
// （技术方案 §7.7）真的会在耗尽后提前放弃重试，而不是一直重试到 MaxAttempts。
// 用 3 个 Key（都返回 429，够 MaxAttempts=3 用）+ maxTokens=1 的预算：第一次尝试
// 不消耗预算，第一次重试消耗掉唯一的令牌，第二次重试应该在真正发请求之前就被
// 预算拦下——上游只应该被打 2 次，不是 3 次。
func TestChatCompletions_RetryBudgetExhausted_StopsRetryingEarly(t *testing.T) {
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

	budget := relay.NewRetryBudget(1, 0.2)
	handler, reqLogW := newTestGatewayWithBudget(t, pool, box, rdb, budget)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, body)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("upstream call count = %d, want 2 (1 first attempt + 1 retry the budget allows; the 3rd must be blocked by the exhausted retry budget)", got)
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

// TestChatCompletions_RecordsCostAmountWhenCostPriceConfigured 验证渠道配了成本价时，
// request_logs.cost_amount 会被算出来并按 provider_accounts.cost_multiplier 打折；
// 没配成本价的渠道（前面几乎所有测试用的都是这种）不应该记出一个具有欺骗性的 0。
func TestChatCompletions_RecordsCostAmountWhenCostPriceConfigured(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1000, "completion_tokens": 1000}}`))
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000)
	accID := seedProviderAccount(t, pool, upstream.URL)
	seedKey(t, pool, box, accID, "sk-mock-upstream-secret")
	// 渠道的合同折扣是 0.5（provider_accounts.cost_multiplier 默认 1，这里手工改一下）。
	if _, err := pool.Exec(context.Background(), `UPDATE provider_accounts SET cost_multiplier = 0.5 WHERE id = $1`, accID); err != nil {
		t.Fatalf("set cost_multiplier: %v", err)
	}
	vmID, vmName := seedVirtualModel(t, pool)
	channelID := seedChannel(t, pool, vmID, accID, 0)
	seedCostPrice(t, pool, channelID) // 1 元/百万 token

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
	reqLogW.Close()

	var costAmount *int64
	if err := pool.QueryRow(context.Background(),
		`SELECT cost_amount FROM request_logs WHERE request_id = $1`, requestID,
	).Scan(&costAmount); err != nil {
		t.Fatalf("query request_logs: %v", err)
	}
	// 2000 token 合计，成本单价 1 元/百万 -> 原始成本 2000 微元，× 0.5 折扣 -> 1000。
	if costAmount == nil || *costAmount != 1000 {
		t.Errorf("cost_amount = %v, want 1000", costAmount)
	}
}

// TestChatCompletions_RecordsCostAmountForNonCNYCostPriceUsingFXRate 验证一个
// 以非 CNY 币种计价的成本价（技术方案 §7.16.9），配合 fx_rates 表里的汇率，
// 也能正确算出 request_logs.cost_amount——用一个本次测试专属的假币种代码而不是
// 真的 "USD"，理由同 catalog 包里 TestStore_LoadFXRates 的注释：fx_rates 是
// (base, quote, effective_date) 为主键的共享表，不能用真实币种代码，会跟其它
// 测试/其它次运行撞车。
func TestChatCompletions_RecordsCostAmountForNonCNYCostPriceUsingFXRate(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1000, "completion_tokens": 1000}}`))
	}))
	defer upstream.Close()

	fx := seedAccount(t, pool, 1_000_000)
	accID := seedProviderAccount(t, pool, upstream.URL)
	seedKey(t, pool, box, accID, "sk-mock-upstream-secret")
	vmID, vmName := seedVirtualModel(t, pool)
	channelID := seedChannel(t, pool, vmID, accID, 0)

	fakeCurrency := fmt.Sprintf("T%d", time.Now().UnixNano())
	seedCostPriceWithCurrency(t, pool, channelID, fakeCurrency) // 1 单位/百万 token
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO fx_rates (base, quote, rate, source, effective_date) VALUES ($1, 'CNY', 7.5, 'test', CURRENT_DATE)`,
		fakeCurrency,
	); err != nil {
		t.Fatalf("insert fx_rate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM fx_rates WHERE base = $1`, fakeCurrency)
	})

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
	reqLogW.Close()

	var costAmount *int64
	if err := pool.QueryRow(context.Background(),
		`SELECT cost_amount FROM request_logs WHERE request_id = $1`, requestID,
	).Scan(&costAmount); err != nil {
		t.Fatalf("query request_logs: %v", err)
	}
	// 2000 token 合计，单价 1/百万 -> 原始成本 2000 微单位，汇率 7.5 -> 15000 微元。
	if costAmount == nil || *costAmount != 15000 {
		t.Errorf("cost_amount = %v, want 15000", costAmount)
	}
}

// TestChatCompletions_RecordsExperimentLabelFromChannel 验证 A/B 路由分组标签
// （Phase 3）会从命中的渠道原样记进 request_logs，供事后按 experiment_key/
// variant_label 聚合对比。
func TestChatCompletions_RecordsExperimentLabelFromChannel(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 10, "completion_tokens": 10}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	var channelID int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM channels WHERE virtual_model_id = (SELECT id FROM virtual_models WHERE name = $1)`, vmName).Scan(&channelID); err != nil {
		t.Fatalf("find seeded channel: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE channels SET experiment_key = 'ab-cost-test', variant_label = 'treatment' WHERE id = $1`, channelID,
	); err != nil {
		t.Fatalf("tag channel with experiment label: %v", err)
	}

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
	reqLogW.Close()

	var experimentKey, variantLabel *string
	if err := pool.QueryRow(context.Background(),
		`SELECT experiment_key, variant_label FROM request_logs WHERE request_id = $1`, requestID,
	).Scan(&experimentKey, &variantLabel); err != nil {
		t.Fatalf("query request_logs: %v", err)
	}
	if experimentKey == nil || *experimentKey != "ab-cost-test" {
		t.Errorf("experiment_key = %v, want ab-cost-test", experimentKey)
	}
	if variantLabel == nil || *variantLabel != "treatment" {
		t.Errorf("variant_label = %v, want treatment", variantLabel)
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

// TestChatCompletions_RPMLimitReturns429 验证按 API Key 的每分钟请求数限制生效，
// 且带 Retry-After 头（技术方案 §7.12）。
func TestChatCompletions_RPMLimitReturns429(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1, "completion_tokens": 1}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	setAPIKeyLimits(t, pool, fx.apiKey, intp(1), nil, nil) // 每分钟只允许 1 次请求

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	body := map[string]any{"model": vmName, "messages": []any{}}

	first := doChatCompletion(t, gw.URL, fx.apiKey, body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.StatusCode)
	}

	second := doChatCompletion(t, gw.URL, fx.apiKey, body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(second.Body)
		t.Fatalf("second request status = %d, want 429, body = %s", second.StatusCode, respBody)
	}
	if ra := second.Header.Get("Retry-After"); ra == "" {
		t.Error("429 response should include a Retry-After header")
	}
}

// TestChatCompletions_ConcurrencyLimitReturns429 验证并发上限生效：第一个请求
// 还没结束时，第二个并发请求应该被拒绝；第一个结束、释放名额后，后续请求恢复正常。
func TestChatCompletions_ConcurrencyLimitReturns429(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // 卡住，直到测试主动放行，模拟一个仍在处理中的慢请求
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1, "completion_tokens": 1}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	setAPIKeyLimits(t, pool, fx.apiKey, nil, nil, intp(1)) // 并发上限 1

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	body := map[string]any{"model": vmName, "messages": []any{}}

	firstDone := make(chan *http.Response, 1)
	go func() { firstDone <- doChatCompletion(t, gw.URL, fx.apiKey, body) }()

	// 等第一个请求真正占用了并发名额（已经打到 upstream，卡在 <-release 上）再发第二个，
	// 避免竞态导致第二个请求先到。
	time.Sleep(500 * time.Millisecond)

	second := doChatCompletion(t, gw.URL, fx.apiKey, body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(second.Body)
		t.Fatalf("second (concurrent) request status = %d, want 429, body = %s", second.StatusCode, respBody)
	}

	close(release) // 放行第一个请求
	first := <-firstDone
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first request status = %d, want 200, body = %s", first.StatusCode, body)
	}

	// 第一个请求结束、名额释放后，新请求应该恢复正常。
	third := doChatCompletion(t, gw.URL, fx.apiKey, body)
	defer third.Body.Close()
	if third.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(third.Body)
		t.Errorf("third request status = %d, want 200 (concurrency slot should be released), body = %s", third.StatusCode, body)
	}
}

// TestChatCompletions_PriceDiscountPromotion_ReducesCharge 验证命中打折促销时，
// 实扣金额按折扣计算，且 request_logs 里 list_amount(原价) != charged_amount(实扣)，
// promotion_ids 包含命中的促销 ID（端到端贯穿 relay -> promotion -> wallet -> reqlog）。
func TestChatCompletions_PriceDiscountPromotion_ReducesCharge(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1000, "completion_tokens": 1000}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	seedPriceDiscountPromotion(t, pool, vmName, 0.5) // 5 折

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
	reqLogW.Close()

	// 2000 token 合计，单价 1 元/百万 -> 原价 2000 微元，5 折后实扣 1000 微元。
	cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000)
	if cash != 1_000_000-1000 {
		t.Errorf("cash_balance = %d, want %d (list 2000 discounted 50%% to 1000)", cash, 1_000_000-1000)
	}

	var listAmount, chargedAmount *int64
	var promotionIDs []int64
	if err := pool.QueryRow(context.Background(),
		`SELECT list_amount, charged_amount, promotion_ids FROM request_logs WHERE request_id = $1`, requestID,
	).Scan(&listAmount, &chargedAmount, &promotionIDs); err != nil {
		t.Fatalf("query request_logs: %v", err)
	}
	if listAmount == nil || *listAmount != 2000 {
		t.Errorf("list_amount = %v, want 2000", listAmount)
	}
	if chargedAmount == nil || *chargedAmount != 1000 {
		t.Errorf("charged_amount = %v, want 1000", chargedAmount)
	}
	if len(promotionIDs) != 1 {
		t.Errorf("promotion_ids = %v, want exactly 1 entry", promotionIDs)
	}
}

// TestChatCompletions_FreeQuotaPromotion_CoversUsage 验证免费额度促销命中时不扣费。
func TestChatCompletions_FreeQuotaPromotion_CoversUsage(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 500, "completion_tokens": 500}}`))
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	seedFreeQuotaPromotion(t, pool, vmName, 1_000_000) // 每天 1 元免费额度，远超本次用量

	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	// 结算发生在 handler 写响应之前（settleQuietly 是同步调用，见 relay.go），
	// 客户端已经收到响应，说明结算必然已完成，不需要像其它用例那样轮询等待变化
	// ——这里恰恰是要断言余额"没有变化"，轮询等待变化只会白白等满整个超时。
	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 1_000_000 {
		t.Errorf("cash_balance = %d, want unchanged 1_000_000 (usage fully covered by free quota)", cash)
	}
	if frozen != 0 {
		t.Errorf("frozen = %d, want 0", frozen)
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
