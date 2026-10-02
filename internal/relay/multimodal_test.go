// 多模态端点（rerank / images / audio）与多模态技术方案 D1–D4 修复的端到端测试。
// 装配方式与 relay_test.go 相同：真实 app.NewGatewayRouter + PostgreSQL + Redis，
// 上游用 httptest 模拟。
package relay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
)

type priceComp struct {
	meter, unit string
	price       string
}

// mediaModel 描述一个测试用虚拟模型：类型、能力、售价分量、渠道 param_overrides。
type mediaModel struct {
	vmType    string
	caps      []string
	prices    []priceComp
	overrides map[string]any
	protocol  string // 空 = openai
}

// seedMediaModel 种 1 账户 + 1 上游账号(1 Key) + 1 虚拟模型（指定类型/能力/售价）+ 1 渠道。
func seedMediaModel(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, upstreamURL string, cashMicro int64, m mediaModel) (fixture, string) {
	t.Helper()
	ctx := context.Background()
	fx := seedAccount(t, pool, cashMicro)
	protocol := m.protocol
	if protocol == "" {
		protocol = "openai"
	}
	accID := seedProviderAccountWithProtocol(t, pool, upstreamURL, protocol)
	seedKey(t, pool, box, accID, "sk-mock-upstream-secret")

	vmName := fmt.Sprintf("e2e-media-%d", time.Now().UnixNano())
	caps := m.caps
	if caps == nil {
		caps = []string{}
	}
	var vmID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, 'test', $2, 128000, 8192, $3, '{free}', 'active') RETURNING id`,
		vmName, m.vmType, caps,
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
	for _, p := range m.prices {
		if _, err := pool.Exec(ctx,
			`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, unit_price) VALUES ($1, $2, $3, 'default', 0, $4)`,
			bookID, p.meter, p.unit, p.price,
		); err != nil {
			t.Fatalf("insert price_component: %v", err)
		}
	}
	overrides := m.overrides
	if overrides == nil {
		overrides = map[string]any{}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, status, param_overrides)
		 VALUES ($1, $2, 'mock-upstream-model', 0, 100, 'active', $3)`,
		vmID, accID, overrides,
	); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return fx, vmName
}

func startGateway(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box) *httptest.Server {
	t.Helper()
	handler, w := newTestGateway(t, pool, box, testRedis(t))
	gw := httptest.NewServer(handler)
	t.Cleanup(func() { gw.Close(); w.Close() })
	return gw
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func errorCode(body []byte) string {
	var m struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Error.Code
}

// ---------- D1：端点 × 模型类型 ----------

func TestChatCompletions_RejectsEmbeddingModelWithoutCallingUpstream(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	var called atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true) }))
	defer upstream.Close()

	fx, vmName := seedSimpleWithType(t, pool, box, upstream.URL, 1_000_000, "embedding")
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusNotFound || errorCode(body) != "model_not_found" {
		t.Errorf("status = %d body = %s, want 404 model_not_found", resp.StatusCode, body)
	}
	if called.Load() {
		t.Error("upstream must not be called for a type mismatch")
	}
}

func TestAudioSpeech_RejectsASRModel(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called")
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "audio", caps: []string{"asr"},
		prices: []priceComp{{"audio_second", "per_second", "0.01"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/audio/speech", fx.apiKey, map[string]any{"model": vmName, "input": "你好", "voice": "alex"})
	if body := readAll(t, resp); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d body = %s, want 404", resp.StatusCode, body)
	}
}

// ---------- D2：图片不按 base64 字节估算 token ----------

func TestChatCompletions_LargeBase64ImageFitsContextWindow(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"a cat"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	fx, vmName := seedSimple(t, pool, box, upstream.URL, 100_000_000)
	gw := startGateway(t, pool, box)

	// 3MB 的 data URL：按 4 字节/token 会被估成 ~78 万 token，超过 128k 上下文窗口。
	dataURL := "data:image/png;base64," + strings.Repeat("A", 3*1024*1024)
	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{
		"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			map[string]any{"type": "text", "text": "what is this?"},
		}}}})
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %.300s, want 200", resp.StatusCode, body)
	}
}

// ---------- D3：上游 400 的原因透传 ----------

func TestChatCompletions_UpstreamBadRequestMessageIsEchoed(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":20015,"message":"max_tokens is too large","data":null}`))
	}))
	defer upstream.Close()
	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "max_tokens is too large") {
		t.Errorf("status = %d body = %s, want 400 echoing the upstream reason", resp.StatusCode, body)
	}
}

// ---------- D4：请求体超限返回 413 ----------

func TestChatCompletions_BodyTooLargeReturns413(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called")
	}))
	defer upstream.Close()
	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	gw := startGateway(t, pool, box)

	resp := doChatCompletion(t, gw.URL, fx.apiKey, map[string]any{"model": vmName, "padding": strings.Repeat("x", 21*1024*1024)})
	if body := readAll(t, resp); resp.StatusCode != http.StatusRequestEntityTooLarge || errorCode(body) != "request_too_large" {
		t.Errorf("status = %d body = %.200s, want 413 request_too_large", resp.StatusCode, body)
	}
}

// ---------- rerank ----------

func TestRerank_ChargesBilledInputTokens(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("path = %s, want /rerank", r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "mock-upstream-model" {
			t.Errorf("upstream model = %v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"up-1","results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}],
			"meta":{"tokens":{"input_tokens":280},"billed_units":{"input_tokens":300}}}`))
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "rerank",
		prices: []priceComp{{"input", "per_1m_tokens", "1"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/rerank", fx.apiKey, map[string]any{"model": vmName, "query": "q", "documents": []any{"a", "b"}})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m["model"] != vmName || m["id"] == "up-1" {
		t.Errorf("model/id not rewritten: %s", body)
	}
	// billed_units 优先：300 token × 1 元/百万 = 300 微元
	if cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000); cash != 1_000_000-300 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want %d / 0", cash, frozen, 1_000_000-300)
	}
}

func TestRerank_RequiresQueryAndDocuments(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("upstream should not be called") }))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "rerank",
		prices: []priceComp{{"input", "per_1m_tokens", "1"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/rerank", fx.apiKey, map[string]any{"model": vmName, "query": "q", "documents": []any{}})
	if body := readAll(t, resp); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d body = %s, want 400", resp.StatusCode, body)
	}
}

// ---------- images ----------

func TestImages_NormalizesResponseAndChargesPerImage(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// SiliconFlow 旧格式：只有 images[]
		_, _ = w.Write([]byte(`{"images":[{"url":"https://img/1.png"},{"url":"https://img/2.png"}],"seed":1}`))
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "image",
		prices: []priceComp{{"image", "per_image", "0.1"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "a cat", "n": 2})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var m struct {
		Data    []map[string]any `json:"data"`
		Created int64            `json:"created"`
		Model   string           `json:"model"`
	}
	_ = json.Unmarshal(body, &m)
	if len(m.Data) != 2 || m.Created == 0 || m.Model != vmName {
		t.Errorf("response not normalized: %s", body)
	}
	// 2 张 × 0.1 元 = 200000 微元
	if cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000); cash != 800_000 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want 800000 / 0", cash, frozen)
	}
}

func TestImages_NoImagesReturnedIsNotCharged(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "image",
		prices: []priceComp{{"image", "per_image", "0.1"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "a cat"})
	if body := readAll(t, resp); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d body = %s, want 502", resp.StatusCode, body)
	}
	cash, frozen := getWalletBalance(t, pool, fx.accountID)
	if cash != 1_000_000 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want untouched", cash, frozen)
	}
}

// ---------- audio.speech ----------

func TestAudioSpeech_PassesBinaryAndChargesPerChar(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	audio := append([]byte("ID3"), bytes.Repeat([]byte{0xFF}, 4096)...)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["voice"] != "mock-upstream-model:alex" {
			t.Errorf("voice = %v, want prefixed with upstream model", req["voice"])
		}
		for k := range req {
			if strings.HasPrefix(k, "$") {
				t.Errorf("directive key %q leaked to upstream", k)
			}
		}
		if _, ok := req["stream_options"]; ok {
			t.Error("stream_options must only be injected for chat")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audio)
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "audio", caps: []string{"tts"},
		prices:    []priceComp{{"input_char", "per_1m_chars", "100"}},
		overrides: map[string]any{"$voice_prefix_upstream_model": true}})
	gw := startGateway(t, pool, box)

	for _, stream := range []bool{false, true} {
		resp := doPost(t, gw.URL+"/v1/audio/speech", fx.apiKey, map[string]any{"model": vmName, "input": "今天天气很好，适合散步", "voice": "alex", "stream": stream})
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "audio/mpeg" || !bytes.Equal(body, audio) {
			t.Fatalf("stream=%v: status = %d ct = %s len = %d", stream, resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
		}
	}
	// 每次 11 个字符 × 100 元/百万字符 = 1100 微元，共两次
	time.Sleep(100 * time.Millisecond)
	if cash, frozen := getWalletBalance(t, pool, fx.accountID); cash != 1_000_000-2200 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want %d / 0", cash, frozen, 1_000_000-2200)
	}
}

// ---------- audio.transcriptions ----------

func multipartBody(t *testing.T, fields map[string]string, filename string, file []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(file)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestAudioTranscriptions_RewritesModelAndChargesUpstreamDuration(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	file := []byte("fake-mp3-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("upstream parse multipart: %v", err)
		}
		if r.FormValue("model") != "mock-upstream-model" || r.FormValue("language") != "zh" {
			t.Errorf("fields = %v", r.MultipartForm.Value)
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		got, _ := io.ReadAll(f)
		if hdr.Filename != "a.mp3" || !bytes.Equal(got, file) {
			t.Errorf("file = %s %q", hdr.Filename, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"你好","usage":{"type":"duration","seconds":3}}`))
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "audio", caps: []string{"asr"},
		prices: []priceComp{{"audio_second", "per_second", "0.01"}}})
	gw := startGateway(t, pool, box)

	body, ct := multipartBody(t, map[string]string{"model": vmName, "language": "zh"}, "a.mp3", file)
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/audio/transcriptions", body)
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if b := readAll(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "你好") {
		t.Fatalf("status = %d body = %s", resp.StatusCode, b)
	}
	// 3 秒 × 0.01 元/秒 = 30000 微元
	if cash, frozen := awaitSettled(t, pool, fx.accountID, 1_000_000); cash != 970_000 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want 970000 / 0", cash, frozen)
	}
}

func TestAudioTranscriptions_RejectsNonMultipartAndBadFormat(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("upstream should not be called") }))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "audio", caps: []string{"asr"},
		prices: []priceComp{{"audio_second", "per_second", "0.01"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/audio/transcriptions", fx.apiKey, map[string]any{"model": vmName})
	if b := readAll(t, resp); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("json body: status = %d body = %s, want 415", resp.StatusCode, b)
	}

	body, ct := multipartBody(t, map[string]string{"model": vmName}, "a.exe", []byte("x"))
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/audio/transcriptions", body)
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if b := readAll(t, resp); resp.StatusCode != http.StatusUnsupportedMediaType || errorCode(b) != "unsupported_media_type" {
		t.Errorf("bad format: status = %d body = %s, want 415", resp.StatusCode, b)
	}
}

// ---------- 适配器不支持的端点：排除渠道 ----------

func TestImages_ExcludesChannelsWhoseProtocolCannotServeEndpoint(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must not be called, got %s", r.URL.Path)
	}))
	defer upstream.Close()
	fx, vmName := seedMediaModel(t, pool, box, upstream.URL, 1_000_000, mediaModel{vmType: "image", protocol: "anthropic",
		prices: []priceComp{{"image", "per_image", "0.1"}}})
	gw := startGateway(t, pool, box)

	resp := doPost(t, gw.URL+"/v1/images/generations", fx.apiKey, map[string]any{"model": vmName, "prompt": "a cat"})
	if b := readAll(t, resp); resp.StatusCode != http.StatusServiceUnavailable || errorCode(b) != "no_available_channel" {
		t.Errorf("status = %d body = %s, want 503 no_available_channel", resp.StatusCode, b)
	}
	if cash, frozen := getWalletBalance(t, pool, fx.accountID); cash != 1_000_000 || frozen != 0 {
		t.Errorf("cash = %d frozen = %d, want untouched", cash, frozen)
	}
}
