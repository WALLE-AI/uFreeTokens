package adapter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

func testTarget(overrides map[string]any) Target {
	return Target{
		Channel: &catalog.Channel{
			ID:             1,
			UpstreamModel:  "deepseek-ai/DeepSeek-V3",
			ParamOverrides: overrides,
		},
		Account: &catalog.ProviderAccount{
			ID: 1, Protocol: "openai", BaseURL: "https://api.example.com/v1", CostMultiplier: decimal.NewFromInt(1),
		},
		Key: &catalog.ProviderKey{ID: 1, Secret: "sk-real-upstream-secret"},
	}
}

func TestBuildRequest_RewritesModelAndAuth(t *testing.T) {
	a := &OpenAIAdapter{}
	body := map[string]any{"model": "deepseek-v4-flash", "messages": []any{}, "stream": false}

	req, err := a.BuildRequest(context.Background(), testTarget(nil), "/chat/completions", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if req.URL.String() != "https://api.example.com/v1/chat/completions" {
		t.Errorf("URL = %s, want https://api.example.com/v1/chat/completions", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-real-upstream-secret" {
		t.Errorf("Authorization header = %q", got)
	}

	raw, _ := io.ReadAll(req.Body)
	if strings.Contains(string(raw), "deepseek-v4-flash") {
		t.Errorf("request body still contains the virtual model name (upstream key leak risk): %s", raw)
	}
	if !strings.Contains(string(raw), "deepseek-ai/DeepSeek-V3") {
		t.Errorf("request body does not contain upstream model name: %s", raw)
	}
}

func TestBuildRequest_AppliesParamOverrides(t *testing.T) {
	a := &OpenAIAdapter{}
	body := map[string]any{"model": "x", "max_tokens": float64(100), "top_p": float64(0.9)}
	overrides := map[string]any{
		"top_p":      nil,                          // 该上游不支持 top_p，应被删除
		"max_tokens": float64(4096),                // 覆盖为渠道限定的上限
		"extra_body": map[string]any{"foo": "bar"}, // 新增字段
	}

	req, err := a.BuildRequest(context.Background(), testTarget(overrides), "/chat/completions", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	s := string(raw)
	if strings.Contains(s, "top_p") {
		t.Errorf("top_p should have been removed by override: %s", s)
	}
	if !strings.Contains(s, `"max_tokens":4096`) {
		t.Errorf("max_tokens should be overridden to 4096: %s", s)
	}
	if !strings.Contains(s, "extra_body") {
		t.Errorf("extra_body should have been injected: %s", s)
	}
}

func TestDecodeResponse_ExtractsOpenAIUsageAndRewritesFields(t *testing.T) {
	a := &OpenAIAdapter{}
	body := []byte(`{
		"id": "chatcmpl-upstream-xyz",
		"model": "deepseek-ai/DeepSeek-V3",
		"usage": {
			"prompt_tokens": 1250,
			"completion_tokens": 870,
			"prompt_tokens_details": {"cached_tokens": 200},
			"completion_tokens_details": {"reasoning_tokens": 50}
		}
	}`)

	m, usage, err := a.DecodeResponse(body, "deepseek-v4-flash", "req-123")
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if m["model"] != "deepseek-v4-flash" {
		t.Errorf("model = %v, want deepseek-v4-flash (must not leak upstream model name)", m["model"])
	}
	if m["id"] != "req-123" {
		t.Errorf("id = %v, want req-123 (must not leak upstream request id)", m["id"])
	}
	if usage.InputTokens != 1050 { // 1250 - 200 cached
		t.Errorf("InputTokens = %d, want 1050", usage.InputTokens)
	}
	if usage.CacheReadTokens != 200 {
		t.Errorf("CacheReadTokens = %d, want 200", usage.CacheReadTokens)
	}
	if usage.OutputTokens != 870 {
		t.Errorf("OutputTokens = %d, want 870", usage.OutputTokens)
	}
	if usage.ReasoningTokens != 50 {
		t.Errorf("ReasoningTokens = %d, want 50", usage.ReasoningTokens)
	}
	if usage.Source != "upstream" {
		t.Errorf("Source = %q, want upstream", usage.Source)
	}
}

func TestDecodeResponse_DeepSeekCacheHitFormat(t *testing.T) {
	a := &OpenAIAdapter{}
	body := []byte(`{"usage": {"prompt_tokens": 1000, "completion_tokens": 500, "prompt_cache_hit_tokens": 300}}`)

	_, usage, err := a.DecodeResponse(body, "vm", "req")
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if usage.CacheReadTokens != 300 {
		t.Errorf("CacheReadTokens = %d, want 300", usage.CacheReadTokens)
	}
	if usage.InputTokens != 700 {
		t.Errorf("InputTokens = %d, want 700", usage.InputTokens)
	}
}

func TestDecodeResponse_MissingUsageIsZeroNotError(t *testing.T) {
	a := &OpenAIAdapter{}
	m, usage, err := a.DecodeResponse([]byte(`{"choices":[{"message":{"content":"hi"}}]}`), "vm", "req")
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if !usage.IsZero() {
		t.Errorf("usage = %+v, want zero", usage)
	}
	if m["model"] != "vm" {
		t.Errorf("model = %v, want vm", m["model"])
	}
}

func TestClassifyError_StatusCodeMapping(t *testing.T) {
	a := &OpenAIAdapter{}
	cases := []struct {
		status int
		body   string
		want   ErrorClass
	}{
		{429, `{}`, ErrClassRateLimited},
		{402, `{}`, ErrClassKeyExhausted},
		{401, `{}`, ErrClassKeyInvalid},
		{403, `{}`, ErrClassKeyInvalid},
		{400, `{}`, ErrClassBadRequest},
		{404, `{}`, ErrClassBadRequest},
		{500, `{}`, ErrClassUpstreamUnavailable},
		{503, `{}`, ErrClassUpstreamUnavailable},
		{400, `{"error":{"code":"content_filter"}}`, ErrClassContentFiltered},
	}
	for _, c := range cases {
		got := a.ClassifyError(c.status, []byte(c.body))
		if got != c.want {
			t.Errorf("ClassifyError(%d, %q) = %q, want %q", c.status, c.body, got, c.want)
		}
	}
}

func TestErrorClass_Retryable(t *testing.T) {
	retryable := []ErrorClass{ErrClassRateLimited, ErrClassKeyExhausted, ErrClassKeyInvalid, ErrClassUpstreamUnavailable}
	for _, c := range retryable {
		if !c.Retryable() {
			t.Errorf("%q should be retryable", c)
		}
	}
	notRetryable := []ErrorClass{ErrClassBadRequest, ErrClassContentFiltered, ErrClassUnknown}
	for _, c := range notRetryable {
		if c.Retryable() {
			t.Errorf("%q should not be retryable", c)
		}
	}
}

// fakeReadCloser 包一个 io.Reader 成 io.ReadCloser，供流式解码测试使用。
type fakeReadCloser struct{ io.Reader }

func (f fakeReadCloser) Close() error { return nil }

func TestStreamDecoder_ForwardsChunksAndCapturesFinalUsage(t *testing.T) {
	a := &OpenAIAdapter{}
	sse := "" +
		"data: {\"id\":\"up-1\",\"model\":\"deepseek-ai/DeepSeek-V3\",\"choices\":[{\"delta\":{\"content\":\"He\"}}]}\n\n" +
		"data: {\"id\":\"up-1\",\"model\":\"deepseek-ai/DeepSeek-V3\",\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n" +
		"data: {\"id\":\"up-1\",\"model\":\"deepseek-ai/DeepSeek-V3\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"

	dec := a.NewStreamDecoder(fakeReadCloser{strings.NewReader(sse)}, "deepseek-v4-flash", "req-abc")
	defer dec.Close()

	var chunks [][]byte
	for {
		out, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		chunks = append(chunks, out)
	}

	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	for _, c := range chunks {
		s := string(c)
		if !strings.Contains(s, `"model":"deepseek-v4-flash"`) {
			t.Errorf("chunk did not have model rewritten: %s", s)
		}
		if !strings.Contains(s, `"id":"req-abc"`) {
			t.Errorf("chunk did not have id rewritten: %s", s)
		}
		if strings.Contains(s, "DeepSeek-V3") {
			t.Errorf("chunk leaks upstream model name: %s", s)
		}
	}

	usage := dec.Usage()
	if usage.InputTokens != 10 || usage.OutputTokens != 2 {
		t.Errorf("Usage() = %+v, want input=10 output=2", usage)
	}
}

func TestStreamDecoder_HandlesNoTrailingNewlineBeforeEOF(t *testing.T) {
	a := &OpenAIAdapter{}
	// 最后一个事件后面没有再跟换行符就直接 EOF（模拟连接被截断但已收到完整 JSON 的情况）。
	sse := "data: {\"id\":\"up-1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}"

	dec := a.NewStreamDecoder(fakeReadCloser{strings.NewReader(sse)}, "vm", "req")
	defer dec.Close()

	out, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !strings.Contains(string(out), `"id":"req"`) {
		t.Errorf("expected the trailing chunk without newline to still be processed, got: %s", out)
	}

	_, err = dec.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
}

// TestBuildRequest_AgainstHTTPTestServer 端到端验证 BuildRequest 产出的请求能被
// 一个真实的 http.Server 正确接收（而不仅仅是断言字符串内容）。
func TestBuildRequest_AgainstHTTPTestServer(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	target := testTarget(nil)
	target.Account.BaseURL = srv.URL

	a := &OpenAIAdapter{}
	req, err := a.BuildRequest(context.Background(), target, "/chat/completions", map[string]any{"model": "x"})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if gotPath != "/chat/completions" {
		t.Errorf("server saw path %q", gotPath)
	}
	if gotAuth != "Bearer sk-real-upstream-secret" {
		t.Errorf("server saw Authorization %q", gotAuth)
	}
}
