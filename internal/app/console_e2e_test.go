// 端到端集成测试：internal/console 的注册/登录/自助 Key 管理挂在真实的
// internal/app.NewGatewayRouter 上（和 cmd/gateway/main.go 一样的装配方式），
// 用带 Cookie Jar 的 http.Client 模拟浏览器，验证"注册 → 登录 → 建 Key → 用
// 这把 Key 调 /v1/models → 吊销后失效"这条完整链路，以及 CSRF/越权/限流/
// 用户名枚举这几个安全相关的行为（技术方案迭代3）。
package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/console"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// consoleClient 是个带 Cookie Jar 的薄封装（模拟浏览器），专门给这些测试用：
// 和 adminClient 不同，这里的方法不在非 2xx 时直接 Fatal——很多用例本来就是
// 要断言 401/403/404/429，调用方自己检查状态码。
type consoleClient struct {
	t       *testing.T
	baseURL string
	http    *http.Client
}

func newConsoleClient(t *testing.T, baseURL string) *consoleClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &consoleClient{t: t, baseURL: baseURL, http: &http.Client{Jar: jar}}
}

func (c *consoleClient) do(method, path string, body any, csrf bool) (*http.Response, []byte) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal request body for %s: %v", path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		c.t.Fatalf("build request for %s: %v", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf {
		req.Header.Set("X-UFT-CSRF", "1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, respBody
}

func (c *consoleClient) register(email, password string) (*http.Response, []byte) {
	return c.do(http.MethodPost, "/console/register", map[string]any{"email": email, "password": password}, false)
}

func (c *consoleClient) login(email, password string) (*http.Response, []byte) {
	return c.do(http.MethodPost, "/console/login", map[string]any{"email": email, "password": password}, false)
}

// resetConsoleIPRateLimits 清掉按 IP 限流的 register/login 计数器。所有这些
// e2e 测试都通过 httptest.Server 从 127.0.0.1 发起请求，IP 维度的限流计数器
// 会在同一次 `go test` 进程里跨测试函数累积（技术方案的登录/注册限流本身就是
// 按 IP 算的，e2e 测试没有理由绕过它，但也不能让"测试 A 的注册请求"消耗掉
// "测试 B 期望能成功注册"的配额）——每个测试用例开始前重置一次，让每个用例
// 都能独立地验证自己关心的行为（正常流程 vs. 限流触发本身）。
func resetConsoleIPRateLimits(t *testing.T, rdb *redis.Client) {
	t.Helper()
	ctx := context.Background()
	// "rate:" 前缀是 github.com/go-redis/redis_rate 内部固定加在
	// ratelimit.Limiter.AllowRPMStrict 传入的 key 前面的（redisPrefix，v10 的
	// rate.go），不是 internal/ratelimit 自己拼的 "uft:rpm:" 前缀——两层前缀都
	// 要对上，删错键这个坑已经真实踩过一次。
	_ = rdb.Del(ctx, "rate:uft:rpm:console:register:ip:127.0.0.1", "rate:uft:rpm:console:login:ip:127.0.0.1").Err()
}

func buildConsoleGateway(t *testing.T) (gwURL string, consoleSvc *console.Service) {
	t.Helper()
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	resetConsoleIPRateLimits(t, rdb)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})

	walletSvc := wallet.New(pool)
	adminSvc := admin.New(pool, walletSvc, box, []byte(testPepper))
	sessions := console.NewSessionStore(rdb)
	rl := ratelimit.New(rdb, logger)
	consoleSvc = console.New(pool, adminSvc, sessions, rl, logger, console.Config{CookieSecure: false})

	gw := httptest.NewServer(app.NewGatewayRouter(app.GatewayDeps{
		Logger: logger, PG: pool, Redis: rdb, AuthStore: auth.NewPostgresStore(pool),
		Pepper: []byte(testPepper), Console: consoleSvc,
	}))
	t.Cleanup(gw.Close)
	return gw.URL, consoleSvc
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("console-e2e-%d@example.com", time.Now().UnixNano())
}

func TestConsole_RegisterLoginCreateKeyCallAndRevoke(t *testing.T) {
	gwURL, _ := buildConsoleGateway(t)
	c := newConsoleClient(t, gwURL)
	email := uniqueEmail(t)

	resp, body := c.register(email, "correct horse battery staple")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", resp.StatusCode, body)
	}

	resp, body = c.login(email, "correct horse battery staple")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", resp.StatusCode, body)
	}
	var loginOut struct {
		AccountID int64 `json:"account_id"`
	}
	if err := json.Unmarshal(body, &loginOut); err != nil {
		t.Fatalf("unmarshal login response %s: %v", body, err)
	}

	// GET /console/me 应该在登录后立刻可用（Cookie 已经由 Jar 自动带上）。
	resp, body = c.do(http.MethodGet, "/console/me", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", resp.StatusCode, body)
	}

	// 建 Key 是写操作，必须带 CSRF 头。
	resp, body = c.do(http.MethodPost, "/console/api-keys", map[string]any{"name": "e2e-key"}, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d, body = %s", resp.StatusCode, body)
	}
	var created struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create key response %s: %v", body, err)
	}
	if created.Key == "" {
		t.Fatal("create key response did not include the raw key")
	}

	// 用这把 Key 调 /v1/models 应该成功。
	req, _ := http.NewRequest(http.MethodGet, gwURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+created.Key)
	modelsResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	modelsBody, _ := io.ReadAll(modelsResp.Body)
	modelsResp.Body.Close()
	if modelsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models status = %d, body = %s", modelsResp.StatusCode, modelsBody)
	}

	// 吊销后，同一把 Key 立即失效（auth.PostgresStore 每次请求都查库、没有缓存）。
	resp, body = c.do(http.MethodPost, fmt.Sprintf("/console/api-keys/%d/revoke", created.ID), nil, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke key status = %d, body = %s", resp.StatusCode, body)
	}

	req2, _ := http.NewRequest(http.MethodGet, gwURL+"/v1/models", nil)
	req2.Header.Set("Authorization", "Bearer "+created.Key)
	afterRevoke, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /v1/models after revoke: %v", err)
	}
	afterRevokeBody, _ := io.ReadAll(afterRevoke.Body)
	afterRevoke.Body.Close()
	if afterRevoke.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status after revoke = %d, want 401, body = %s", afterRevoke.StatusCode, afterRevokeBody)
	}
}

func TestConsole_CannotRevokeAnotherAccountsKey(t *testing.T) {
	gwURL, _ := buildConsoleGateway(t)

	// 账户 A：注册、登录、建一把 Key。
	a := newConsoleClient(t, gwURL)
	emailA := uniqueEmail(t)
	if resp, body := a.register(emailA, "password-for-account-a"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register A status = %d, body = %s", resp.StatusCode, body)
	}
	if resp, body := a.login(emailA, "password-for-account-a"); resp.StatusCode != http.StatusOK {
		t.Fatalf("login A status = %d, body = %s", resp.StatusCode, body)
	}
	resp, body := a.do(http.MethodPost, "/console/api-keys", map[string]any{"name": "a-key"}, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key A status = %d, body = %s", resp.StatusCode, body)
	}
	var keyA struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &keyA); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 账户 B：独立的浏览器会话（独立 Cookie Jar），试图吊销 A 的 Key。
	b := newConsoleClient(t, gwURL)
	emailB := uniqueEmail(t)
	if resp, body := b.register(emailB, "password-for-account-b"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register B status = %d, body = %s", resp.StatusCode, body)
	}
	if resp, body := b.login(emailB, "password-for-account-b"); resp.StatusCode != http.StatusOK {
		t.Fatalf("login B status = %d, body = %s", resp.StatusCode, body)
	}

	resp, body = b.do(http.MethodPost, fmt.Sprintf("/console/api-keys/%d/revoke", keyA.ID), nil, true)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-account revoke status = %d, want 404, body = %s", resp.StatusCode, body)
	}
}

func TestConsole_WriteWithoutCSRFHeaderIsRejected(t *testing.T) {
	gwURL, _ := buildConsoleGateway(t)
	c := newConsoleClient(t, gwURL)
	email := uniqueEmail(t)
	if resp, body := c.register(email, "correct horse battery staple"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", resp.StatusCode, body)
	}
	if resp, body := c.login(email, "correct horse battery staple"); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", resp.StatusCode, body)
	}

	resp, body := c.do(http.MethodPost, "/console/api-keys", map[string]any{"name": "no-csrf"}, false)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status without CSRF header = %d, want 403, body = %s", resp.StatusCode, body)
	}
}

func TestConsole_LoginRateLimitedAfterTooManyAttempts(t *testing.T) {
	gwURL, _ := buildConsoleGateway(t)
	c := newConsoleClient(t, gwURL)
	email := uniqueEmail(t)
	if resp, body := c.register(email, "correct horse battery staple"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", resp.StatusCode, body)
	}

	// 邮箱维度限流是 5次/分钟（技术方案），用错误密码连续打，应该在耗尽配额后
	// 收到 429，而不是无限次 401。
	var sawRateLimited bool
	for i := 0; i < 8; i++ {
		resp, _ := c.login(email, "wrong-password")
		if resp.StatusCode == http.StatusTooManyRequests {
			sawRateLimited = true
			break
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 or 429", i, resp.StatusCode)
		}
	}
	if !sawRateLimited {
		t.Error("never saw 429 after repeated failed login attempts, want login rate limiting to trigger")
	}
}

func TestConsole_WrongPasswordAndUnknownEmailReturnIdenticalResponses(t *testing.T) {
	gwURL, _ := buildConsoleGateway(t)

	registered := newConsoleClient(t, gwURL)
	email := uniqueEmail(t)
	if resp, body := registered.register(email, "correct horse battery staple"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", resp.StatusCode, body)
	}

	wrongPassClient := newConsoleClient(t, gwURL)
	wrongPassResp, wrongPassBody := wrongPassClient.login(email, "totally-wrong-password")

	unknownEmailClient := newConsoleClient(t, gwURL)
	unknownResp, unknownBody := unknownEmailClient.login(uniqueEmail(t), "whatever-password")

	if wrongPassResp.StatusCode != unknownResp.StatusCode {
		t.Errorf("status codes differ: wrong password = %d, unknown email = %d, want identical", wrongPassResp.StatusCode, unknownResp.StatusCode)
	}
	if wrongPassResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", wrongPassResp.StatusCode)
	}

	var wrongPassErr, unknownErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(wrongPassBody, &wrongPassErr); err != nil {
		t.Fatalf("unmarshal wrong-password body %s: %v", wrongPassBody, err)
	}
	if err := json.Unmarshal(unknownBody, &unknownErr); err != nil {
		t.Fatalf("unmarshal unknown-email body %s: %v", unknownBody, err)
	}
	if wrongPassErr.Error.Code != unknownErr.Error.Code || wrongPassErr.Error.Message != unknownErr.Error.Message {
		t.Errorf("responses differ: wrong-password = %+v, unknown-email = %+v, want identical (avoid email enumeration)", wrongPassErr, unknownErr)
	}
}
