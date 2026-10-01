// 端到端集成测试：整套配置——账户、API Key、Provider、渠道、售价——全部通过
// internal/app.NewAdminRouter 的真实 HTTP 接口创建，不写一行手工 SQL；然后用这个
// API Key 打一个真实的 /v1/chat/completions 请求到 internal/app.NewGatewayRouter，
// 验证请求成功、按正确价格扣费。这是全仓库里唯一一个"从控制面到数据面全打通"的
// 测试——它专门用来捕捉 admin 包写数据的字段/格式和 catalog/relay 包读数据的
// 期望对不上的问题，这类问题在两边各自的单元测试里都测不出来（各自的测试都是
// 自己写什么就自己读什么，字段刚好对齐是必然的；只有真正换一边写、另一边读，
// 才能验证两边对"一条 channel/一条 price_component 应该长什么样"的理解是一致的）。
package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/admin"
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
const defaultTestRedisAddr = "localhost:6379"
const testPepper = "app-e2e-test-pepper"
const testAdminToken = "app-e2e-test-admin-token"

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

// adminClient 是个薄薄的 HTTP 封装，专门为了让这个测试读起来像"调用管理接口"
// 而不是被 JSON 编解码的样板代码淹没。token 默认用 testAdminToken——所有测试
// 构造 app.AdminDeps 时都必须传 AdminToken: testAdminToken，否则每个请求都会
// 被 httpx.RequireBearerToken 拒成 401（这不是测试基础设施的意外行为，是
// cmd/admin 鉴权中间件按设计工作，见 internal/app.NewAdminRouter 的注释）。
type adminClient struct {
	t       *testing.T
	baseURL string
	token   string
}

func (c *adminClient) authToken() string {
	if c.token == "" {
		return testAdminToken
	}
	return c.token
}

func (c *adminClient) post(path string, body any, out any) {
	c.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		c.t.Fatalf("marshal request body for %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		c.t.Fatalf("build request for %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		c.t.Fatalf("POST %s: status = %d, body = %s", path, resp.StatusCode, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			c.t.Fatalf("POST %s: unmarshal response %s: %v", path, respBody, err)
		}
	}
}

func (c *adminClient) get(path string, out any) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		c.t.Fatalf("build request for %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.authToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		c.t.Fatalf("GET %s: status = %d, body = %s", path, resp.StatusCode, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			c.t.Fatalf("GET %s: unmarshal response %s: %v", path, respBody, err)
		}
	}
}

func TestAdminCreatedConfig_WorksThroughGateway(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	walletSvc := wallet.New(pool)
	adminSvc := admin.New(pool, walletSvc, box, []byte(testPepper))
	adminSvc.SetUpstreamURLPolicy(admin.PermissiveUpstreamURLPolicy())
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	// mock 上游：验证收到的 Authorization 头就是管理接口生成、加密落库、
	// 又在路由时被解密出来的那个 Key。
	const upstreamSecret = "sk-e2e-upstream-secret"
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage": {"prompt_tokens": 1000, "completion_tokens": 500}}`))
	}))
	defer upstream.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// 1. 建账户 + 充值 1 元（Adjust 是当前唯一的入账手段，真正的支付流程还没做）。
	var account struct {
		ID int64 `json:"id"`
	}
	ac.post("/accounts", map[string]any{"type": "personal", "name": "e2e-" + suffix, "tier": "free"}, &account)
	ac.post(fmt.Sprintf("/accounts/%d/wallet/adjust", account.ID),
		map[string]any{"amount": 1_000_000, "ref_id": "e2e-recharge-" + suffix, "reason": "e2e test top-up"}, nil)

	// 2. 建 API Key。
	var apiKey struct {
		RawKey string `json:"raw_key"`
	}
	ac.post(fmt.Sprintf("/accounts/%d/api-keys", account.ID), map[string]any{"name": "e2e-key"}, &apiKey)
	if apiKey.RawKey == "" {
		t.Fatal("admin did not return a raw API key")
	}

	// 3. 建 Provider -> ProviderAccount(指向 mock 上游) -> ProviderKey。
	var provider struct{ ID int64 }
	ac.post("/providers", map[string]any{"code": "e2e-provider-" + suffix, "name": "E2E", "protocol": "openai"}, &provider)

	var providerAccount struct{ ID int64 }
	ac.post("/provider-accounts", map[string]any{
		"provider_id": provider.ID, "name": "e2e-account", "base_url": upstream.URL,
	}, &providerAccount)

	ac.post(fmt.Sprintf("/provider-accounts/%d/keys", providerAccount.ID),
		map[string]any{"secret": upstreamSecret, "weight": 100}, nil)

	// 4. 建虚拟模型 + 渠道。
	var vm struct{ ID int64 }
	ac.post("/virtual-models", map[string]any{
		"name": "e2e-model-" + suffix, "family": "test", "type": "chat",
		"context_window": 128000, "max_output": 8192, "capabilities": []string{"stream"},
	}, &vm)

	ac.post("/channels", map[string]any{
		"virtual_model_id": vm.ID, "provider_account_id": providerAccount.ID,
		"upstream_model": "upstream-model-name", "priority": 0, "weight": 100,
	}, nil)

	// 5. 发布售价：1 元/百万 token，方便手算预期扣费。
	var priceResp struct {
		PriceBookID int64 `json:"price_book_id"`
	}
	ac.post(fmt.Sprintf("/virtual-models/%d/sell-price", vm.ID), map[string]any{
		"components": []map[string]any{
			{"meter": "input", "unit": "per_1m_tokens", "unit_price": "1"},
			{"meter": "output", "unit": "per_1m_tokens", "unit_price": "1"},
		},
	}, &priceResp)

	// --- 到这里，全部配置都通过 HTTP 管理接口建好了，一行 SQL 都没写 ---

	// 6. 起一个真实网关，用刚刚创建的 API Key 打一个真实的 chat completion 请求。
	relaySvc := &relay.Service{
		Catalog:  catalog.NewStore(pool, box, 0),
		Wallet:   walletSvc,
		Adapters: adapter.NewRegistry(),
		HTTP:     http.DefaultClient,
		Logger:   logger,
		Cfg:      relay.DefaultConfig(),
	}
	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{
		Logger: logger, PG: pool, AuthStore: auth.NewPostgresStore(pool),
		Pepper: []byte(testPepper), Relay: relaySvc,
	}))
	defer gw.Close()

	reqBody, _ := json.Marshal(map[string]any{"model": "e2e-model-" + suffix, "messages": []any{}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey.RawKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat completion request: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, respBody)
	}
	if gotAuth != "Bearer "+upstreamSecret {
		t.Errorf("upstream saw Authorization = %q, want the key created via AddProviderKey (encryption round-trip through catalog failed)", gotAuth)
	}

	// 1500 token 合计，单价 1 元/百万 -> 1500 微元。
	var cash int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT cash_balance FROM wallets WHERE account_id = $1`, account.ID).Scan(&cash); err != nil {
			t.Fatalf("query wallet: %v", err)
		}
		if cash != 1_000_000 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cash != 1_000_000-1500 {
		t.Errorf("cash_balance = %d, want %d (1500 micro-CNY charged for 1500 tokens at 1 CNY/1M)", cash, 1_000_000-1500)
	}
	_ = rdb // 保留依赖，方便未来这个测试需要用到限流/熔断时直接接上 Health/RateLimit
}
