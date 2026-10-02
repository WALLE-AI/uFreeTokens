// 供应商方言（多供应商接口统一技术实施方案 WP1–WP4）的端到端测试：方言写在
// provider_accounts.extra.dialect，经真实的 catalog 加载、路由、重试与结算。
package relay_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/relay"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
)

// setAccountDialect 给 vmName 唯一的那个渠道所在的上游账号写方言。
func setAccountDialect(t *testing.T, pool *pgxpool.Pool, vmName, dialectJSON string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE provider_accounts pa SET extra = jsonb_set(pa.extra, '{dialect}', $2::jsonb)
		 FROM channels c JOIN virtual_models vm ON vm.id = c.virtual_model_id
		 WHERE c.provider_account_id = pa.id AND vm.name = $1`, vmName, dialectJSON); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
}

// addChannel 给已有的虚拟模型再挂一个上游（新的账号 + Key），priority 越小越优先。
func addChannel(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, vmName, upstreamURL string, priority int) {
	t.Helper()
	accID := seedProviderAccount(t, pool, upstreamURL)
	seedKey(t, pool, box, accID, "sk-mock-upstream-secret")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, status)
		 SELECT id, $2, 'mock-upstream-model', $3, 100, 'active' FROM virtual_models WHERE name = $1`, vmName, accID, priority); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func TestDialect_ImagesPathOverrideAndB64Response(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aGk=","media_type":"image/png"}],"usage":{"cost":0.04}}`))
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "image",
		prices: []priceComp{{"image", "per_image", "0.1"}}})
	setAccountDialect(t, pool, vmName, `{"preset":"openrouter"}`)
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "cat"})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || gotPath != "/images" || !strings.Contains(string(body), `"b64_json":"aGk="`) {
		t.Fatalf("status = %d path = %s body = %s", resp.StatusCode, gotPath, body)
	}
	if cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000); cash != 900_000 {
		t.Errorf("cash = %d, want 900000", cash)
	}
}

func TestDialect_InBandErrorRetriesNextChannel(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	var bad atomic.Int32
	badUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bad.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"No provider available"}}`)) // HTTP 200 + error
	}))
	defer badUp.Close()
	goodUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
	}))
	defer goodUp.Close()

	fx, vmName := seedSimple(t, pool, box, badUp.URL, 1_000_000)
	setAccountDialect(t, pool, vmName, `{"preset":"openrouter"}`)
	addChannel(t, pool, box, vmName, goodUp.URL, 1)
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if bad.Load() != 1 {
		t.Errorf("bad upstream calls = %d, want 1", bad.Load())
	}
}

func TestDialect_InBandBadRequestNotRetried(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"prompt too long"}}`))
	}))
	defer upstream.Close()
	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	setAccountDialect(t, pool, vmName, `{"preset":"openrouter"}`)
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "prompt too long") {
		t.Errorf("status = %d body = %s", resp.StatusCode, body)
	}
	if cash, frozen := getWalletBalance(t, pool, fx.accountID); cash != 1_000_000 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want untouched", cash, frozen)
	}
}

func TestStream_MidStreamErrorAndUsageStripping(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 带内容的 chunk 上附带 usage（OpenRouter / SiliconFlow 风格），随后流中报错
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("data: {\"error\":{\"code\":502,\"message\":\"provider died\"},\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"error\"}]}\n\n"))
	}))
	defer upstream.Close()
	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "stream": true, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	defer resp.Body.Close()
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") {
			lines = append(lines, sc.Text())
		}
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %v", lines)
	}
	if strings.Contains(lines[0], `"usage"`) {
		t.Errorf("usage leaked to a client that did not ask for it: %s", lines[0])
	}
	var errChunk struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &errChunk)
	if errChunk.Error.Code != "upstream_error" || !strings.Contains(errChunk.Error.Message, "provider died") {
		t.Errorf("error chunk = %s", lines[1])
	}
	// 按已转发内容结算：上游报告了 usage（5 + 1 token × 1 元/百万 = 6 微元）
	if cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000); cash != 1_000_000-6 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d", cash, frozen)
	}
}

func TestDialect_DashscopeNativeImageCodec(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	var gotPath string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"image":"https://oss.example/1.png","type":"image"}]}}]},"usage":{"output_image_count":1}}`))
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL+"/compatible-mode/v1", 1_000_000, mediaModel{vmType: "image",
		prices: []priceComp{{"image", "per_image", "0.2"}}})
	setAccountDialect(t, pool, vmName, `{"preset":"dashscope"}`)
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "cat", "size": "1024x1024"})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || gotPath != "/api/v1/services/aigc/multimodal-generation/generation" ||
		!strings.Contains(string(body), `"url":"https://oss.example/1.png"`) {
		t.Fatalf("status = %d path = %s body = %s", resp.StatusCode, gotPath, body)
	}
	if p, _ := gotBody["parameters"].(map[string]any); p["size"] != "1024*1024" {
		t.Errorf("upstream body = %v", gotBody)
	}
	if cash, _ := awaitSettled(t, pool, fx.accountID, 1_000_000); cash != 800_000 {
		t.Errorf("cash = %d, want 800000", cash)
	}
}

func TestDialect_UnsupportedEndpointExcludesChannel(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called")
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "rerank",
		prices: []priceComp{{"input", "per_1m_tokens", "1"}}})
	setAccountDialect(t, pool, vmName, `{"preset":"volcengine"}`) // 方舟没有 rerank
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/rerank", fx.apiKey, map[string]any{"model": vmName, "query": "q", "documents": []any{"a"}})
	if body := readAll(t, resp); resp.StatusCode != http.StatusServiceUnavailable || errorCode(body) != "no_available_channel" {
		t.Errorf("status = %d body = %s", resp.StatusCode, body)
	}
}

func TestDialect_InvalidDialectTakesAccountOffline(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called with a broken dialect")
	}))
	defer upstream.Close()
	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	setAccountDialect(t, pool, vmName, `{"preset":"no-such-preset"}`)
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	if body := readAll(t, resp); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d body = %s, want 503", resp.StatusCode, body)
	}
}

// okChat 是一个最小的成功对话响应。
const okChat = `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`

func chatHi(t *testing.T, gwURL, apiKey, vmName string) (*http.Response, []byte) {
	t.Helper()
	resp := doChatCompletion(t, gwURL, apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	return resp, readAll(t, resp)
}

// transport.timeout_ms：非流式请求超时后换下一个渠道。
func TestDialect_TimeoutFailsOverToNextChannel(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okChat))
	}))
	defer fast.Close()

	fx, vmName := seedSimple(t, pool, box, slow.URL, 1_000_000)
	setAccountDialect(t, pool, vmName, `{"transport":{"timeout_ms":200}}`)
	addChannel(t, pool, box, vmName, fast.URL, 1)
	gw := startGateway(t, pool, box)

	start := time.Now()
	resp, body := chatHi(t, gw.URL, fx.apiKey, vmName)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("took %v, timeout_ms not applied", d)
	}
}

// auth.alternate_hosts：主域名 401 后改用备用域名，并记住它。
func TestDialect_AlternateHostOnKeyRejected(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	var primary, alt atomic.Int32
	altUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		alt.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okChat))
	}))
	defer altUp.Close()
	primaryUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primary.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer primaryUp.Close()

	fx, vmName := seedSimple(t, pool, box, primaryUp.URL, 1_000_000)
	setAccountDialect(t, pool, vmName, `{"auth":{"alternate_hosts":["`+altUp.URL+`"]}}`)
	gw := startGateway(t, pool, box)

	for i := 0; i < 2; i++ {
		if resp, body := chatHi(t, gw.URL, fx.apiKey, vmName); resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: status = %d body = %s", i, resp.StatusCode, body)
		}
	}
	if primary.Load() != 1 || alt.Load() != 2 {
		t.Errorf("primary = %d alt = %d, want 1 / 2 (alternate host remembered)", primary.Load(), alt.Load())
	}
}

// errors.challenge_is_transient：CF 质询的 403 换渠道，不当作 Key 失效。
func TestDialect_ChallengeIsTransient(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	challenge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer challenge.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okChat))
	}))
	defer good.Close()

	fx, vmName := seedSimple(t, pool, box, challenge.URL, 1_000_000)
	setAccountDialect(t, pool, vmName, `{"errors":{"challenge_is_transient":true}}`)
	addChannel(t, pool, box, vmName, good.URL, 1)
	gw := startGateway(t, pool, box)

	if resp, body := chatHi(t, gw.URL, fx.apiKey, vmName); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var keyStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT pk.status FROM provider_keys pk JOIN provider_accounts pa ON pa.id = pk.provider_account_id
		 JOIN channels c ON c.provider_account_id = pa.id JOIN virtual_models vm ON vm.id = c.virtual_model_id
		 WHERE vm.name = $1 AND pa.base_url = $2`, vmName, challenge.URL).Scan(&keyStatus); err != nil {
		t.Fatal(err)
	}
	if keyStatus != "active" {
		t.Errorf("key status = %s, want active", keyStatus)
	}
}

// relay.disabled_codecs：用到被停用 codec 的渠道不参与路由；attempt_trace 记录 codec。
func TestDialect_DisabledCodecAndTraceCodec(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	var native atomic.Int32
	nativeUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		native.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"image":"https://oss.example/1.png"}]}}]},"usage":{"output_image_count":1}}`))
	}))
	defer nativeUp.Close()
	fx, vmName := seedMediaModel(t, pool, box, nativeUp.URL+"/compatible-mode/v1", 1_000_000, mediaModel{vmType: "image",
		prices: []priceComp{{"image", "per_image", "0.2"}}})
	setAccountDialect(t, pool, vmName, `{"preset":"dashscope"}`)

	// 停用 dashscope.image：唯一渠道被排除
	cfg := relay.DefaultConfig()
	cfg.DisabledCodecs = []string{"dashscope.image"}
	h, w := newTestGatewayWithConfig(t, pool, box, rdb, nil, cfg)
	gw := httptest.NewServer(h)
	resp := doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "cat"})
	if body := readAll(t, resp); resp.StatusCode != http.StatusServiceUnavailable || native.Load() != 0 {
		t.Errorf("disabled codec: status = %d calls = %d body = %s", resp.StatusCode, native.Load(), body)
	}
	gw.Close()
	w.Close()

	// 正常配置：成功，attempt_trace 带 codec
	h, w = newTestGateway(t, pool, box, rdb)
	gw = httptest.NewServer(h)
	defer gw.Close()
	resp = doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "cat"})
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	reqID := resp.Header.Get("X-Request-Id")
	w.Close()
	var traceJSON []byte
	if err := pool.QueryRow(context.Background(), `SELECT attempt_trace FROM request_logs WHERE request_id = $1`, reqID).Scan(&traceJSON); err != nil {
		t.Fatal(err)
	}
	var trace []map[string]any
	_ = json.Unmarshal(traceJSON, &trace)
	if len(trace) != 1 || trace[0]["codec"] != "dashscope.image" {
		t.Errorf("attempt_trace = %s", traceJSON)
	}
}

// relay.Config.UpstreamHeaderTimeout：等响应头超时换渠道（图像端点用更长的 ImagesHeaderTimeout）。
func TestRelay_UpstreamHeaderTimeoutFailsOver(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okChat))
	}))
	defer fast.Close()

	fx, vmName := seedSimple(t, pool, box, slow.URL, 1_000_000)
	addChannel(t, pool, box, vmName, fast.URL, 1)
	cfg := relay.DefaultConfig()
	cfg.UpstreamHeaderTimeout = 200 * time.Millisecond
	h, w := newTestGatewayWithConfig(t, pool, box, rdb, nil, cfg)
	gw := httptest.NewServer(h)
	defer func() { gw.Close(); w.Close() }()

	start := time.Now()
	if resp, body := chatHi(t, gw.URL, fx.apiKey, vmName); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("took %v, header timeout not applied", d)
	}
}
