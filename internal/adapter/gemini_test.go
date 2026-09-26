package adapter

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

func geminiTestTarget(overrides map[string]any) Target {
	return Target{
		Channel: &catalog.Channel{
			ID:             1,
			UpstreamModel:  "gemini-2.5-pro",
			ParamOverrides: overrides,
		},
		Account: &catalog.ProviderAccount{
			ID: 1, Protocol: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta", CostMultiplier: decimal.NewFromInt(1),
		},
		Key: &catalog.ProviderKey{ID: 1, Secret: "gemini-real-secret"},
	}
}

func TestGeminiBuildRequest_UsesGenerateContentEndpointAndAPIKeyHeader(t *testing.T) {
	a := &GeminiAdapter{}
	body := map[string]any{"model": "gemini-virtual", "messages": []any{
		map[string]any{"role": "user", "content": "hi"},
	}, "stream": false}

	req, err := a.BuildRequest(context.Background(), geminiTestTarget(nil), "irrelevant", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent"
	if req.URL.String() != want {
		t.Errorf("URL = %s, want %s", req.URL.String(), want)
	}
	if got := req.Header.Get("x-goog-api-key"); got != "gemini-real-secret" {
		t.Errorf("x-goog-api-key = %q", got)
	}
	if strings.Contains(req.URL.String(), "gemini-real-secret") {
		t.Error("API key must not appear in the URL (use the header instead)")
	}

	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	contents, _ := m["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %v, want exactly 1", contents)
	}
	first, _ := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("role = %v, want user", first["role"])
	}
}

func TestGeminiBuildRequest_UsesStreamGenerateContentWithSSEWhenStreaming(t *testing.T) {
	a := &GeminiAdapter{}
	body := map[string]any{"model": "x", "messages": []any{}, "stream": true}

	req, err := a.BuildRequest(context.Background(), geminiTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse"
	if req.URL.String() != want {
		t.Errorf("URL = %s, want %s", req.URL.String(), want)
	}
}

func TestGeminiBuildRequest_ExtractsSystemInstructionAndMapsAssistantToModel(t *testing.T) {
	a := &GeminiAdapter{}
	body := map[string]any{"model": "x", "messages": []any{
		map[string]any{"role": "system", "content": "be terse"},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "hello"},
	}}

	req, err := a.BuildRequest(context.Background(), geminiTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)

	sysInstr, _ := m["systemInstruction"].(map[string]any)
	parts, _ := sysInstr["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("systemInstruction.parts = %v, want exactly 1", parts)
	}
	part, _ := parts[0].(map[string]any)
	if part["text"] != "be terse" {
		t.Errorf("systemInstruction text = %v, want %q", part["text"], "be terse")
	}

	contents, _ := m["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("contents = %v, want exactly 2 (system message must be pulled out)", contents)
	}
	second, _ := contents[1].(map[string]any)
	if second["role"] != "model" {
		t.Errorf("assistant role should map to 'model', got %v", second["role"])
	}
}

func TestGeminiBuildRequest_MapsGenerationConfigFields(t *testing.T) {
	a := &GeminiAdapter{}
	body := map[string]any{
		"model": "x", "messages": []any{},
		"temperature": float64(0.5), "top_p": float64(0.9), "max_tokens": float64(1024), "stop": "STOP",
	}

	req, err := a.BuildRequest(context.Background(), geminiTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	gc, _ := m["generationConfig"].(map[string]any)
	if gc["temperature"] != 0.5 {
		t.Errorf("temperature = %v, want 0.5", gc["temperature"])
	}
	if gc["topP"] != 0.9 {
		t.Errorf("topP = %v, want 0.9", gc["topP"])
	}
	if gc["maxOutputTokens"] != float64(1024) {
		t.Errorf("maxOutputTokens = %v, want 1024", gc["maxOutputTokens"])
	}
	seqs, _ := gc["stopSequences"].([]any)
	if len(seqs) != 1 || seqs[0] != "STOP" {
		t.Errorf("stopSequences = %v, want [STOP]", gc["stopSequences"])
	}
}

func TestGeminiBuildRequest_AppliesParamOverrides(t *testing.T) {
	a := &GeminiAdapter{}
	overrides := map[string]any{"extra_field": "injected"}
	req, err := a.BuildRequest(context.Background(), geminiTestTarget(overrides), "", map[string]any{"model": "x", "messages": []any{}})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(raw), "injected") {
		t.Errorf("extra_field should have been injected by override: %s", raw)
	}
}

func TestGeminiBuildRequest_RejectsToolsAndToolMessages(t *testing.T) {
	a := &GeminiAdapter{}

	if _, err := a.BuildRequest(context.Background(), geminiTestTarget(nil), "", map[string]any{
		"model": "x", "messages": []any{}, "tools": []any{map[string]any{"type": "function"}},
	}); err == nil {
		t.Error("expected an error when the request declares tools (not supported yet)")
	}

	if _, err := a.BuildRequest(context.Background(), geminiTestTarget(nil), "", map[string]any{
		"model": "x", "messages": []any{map[string]any{"role": "tool", "content": "result"}},
	}); err == nil {
		t.Error("expected an error for a tool-result message (not supported yet)")
	}
}

func TestGeminiDecodeResponse_ExtractsUsageAndMapsFinishReason(t *testing.T) {
	a := &GeminiAdapter{}
	body := []byte(`{
		"candidates": [{"content":{"parts":[{"text":"Hello, "},{"text":"world."}]},"finishReason":"STOP"}],
		"usageMetadata": {"promptTokenCount": 30, "candidatesTokenCount": 12, "cachedContentTokenCount": 10}
	}`)

	m, usage, err := a.DecodeResponse(body, "gemini-virtual", "req-123")
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if m["model"] != "gemini-virtual" {
		t.Errorf("model = %v, want gemini-virtual", m["model"])
	}
	if m["id"] != "req-123" {
		t.Errorf("id = %v, want req-123", m["id"])
	}
	choices, _ := m["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	if msg["content"] != "Hello, world." {
		t.Errorf("message.content = %v, want concatenated parts", msg["content"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}

	if usage.InputTokens != 20 { // 30 prompt - 10 cached
		t.Errorf("InputTokens = %d, want 20", usage.InputTokens)
	}
	if usage.CacheReadTokens != 10 {
		t.Errorf("CacheReadTokens = %d, want 10", usage.CacheReadTokens)
	}
	if usage.OutputTokens != 12 {
		t.Errorf("OutputTokens = %d, want 12", usage.OutputTokens)
	}
}

func TestGeminiDecodeResponse_BlockedPromptHasNoCandidatesMapsToContentFilter(t *testing.T) {
	a := &GeminiAdapter{}
	body := []byte(`{"promptFeedback": {"blockReason": "SAFETY"}, "usageMetadata": {"promptTokenCount": 10}}`)

	m, usage, err := a.DecodeResponse(body, "vm", "req")
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	choices, _ := m["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	if choice["finish_reason"] != "content_filter" {
		t.Errorf("finish_reason = %v, want content_filter", choice["finish_reason"])
	}
	msg, _ := choice["message"].(map[string]any)
	if msg["content"] != "" {
		t.Errorf("message.content = %v, want empty (prompt was blocked, no candidates)", msg["content"])
	}
	if !usage.IsZero() && usage.InputTokens != 10 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestGeminiDecodeResponse_MapsAllFinishReasons(t *testing.T) {
	a := &GeminiAdapter{}
	cases := []struct {
		finishReason string
		want         string
	}{
		{"STOP", "stop"},
		{"MAX_TOKENS", "length"},
		{"SAFETY", "content_filter"},
		{"RECITATION", "content_filter"},
		{"OTHER", "stop"},
	}
	for _, c := range cases {
		body := []byte(`{"candidates":[{"content":{"parts":[]},"finishReason":"` + c.finishReason + `"}]}`)
		m, _, err := a.DecodeResponse(body, "vm", "req")
		if err != nil {
			t.Fatalf("DecodeResponse(%s): %v", c.finishReason, err)
		}
		choices, _ := m["choices"].([]any)
		choice, _ := choices[0].(map[string]any)
		if choice["finish_reason"] != c.want {
			t.Errorf("finishReason %q -> finish_reason %v, want %q", c.finishReason, choice["finish_reason"], c.want)
		}
	}
}

func TestGeminiClassifyError_ByErrorStatusField(t *testing.T) {
	a := &GeminiAdapter{}
	cases := []struct {
		status int
		body   string
		want   ErrorClass
	}{
		{429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, ErrClassRateLimited},
		{401, `{"error":{"status":"UNAUTHENTICATED"}}`, ErrClassKeyInvalid},
		{403, `{"error":{"status":"PERMISSION_DENIED"}}`, ErrClassKeyInvalid},
		{400, `{"error":{"status":"INVALID_ARGUMENT"}}`, ErrClassBadRequest},
		{404, `{"error":{"status":"NOT_FOUND"}}`, ErrClassBadRequest},
		{500, `{"error":{"status":"INTERNAL"}}`, ErrClassUpstreamUnavailable},
		{503, `{"error":{"status":"UNAVAILABLE"}}`, ErrClassUpstreamUnavailable},
	}
	for _, c := range cases {
		got := a.ClassifyError(c.status, []byte(c.body))
		if got != c.want {
			t.Errorf("ClassifyError(%d, %q) = %q, want %q", c.status, c.body, got, c.want)
		}
	}
}

func TestGeminiClassifyError_FallsBackToStatusCodeWhenStatusFieldMissing(t *testing.T) {
	a := &GeminiAdapter{}
	cases := []struct {
		status int
		want   ErrorClass
	}{
		{429, ErrClassRateLimited},
		{401, ErrClassKeyInvalid},
		{400, ErrClassBadRequest},
		{502, ErrClassUpstreamUnavailable},
	}
	for _, c := range cases {
		got := a.ClassifyError(c.status, []byte(`not even json`))
		if got != c.want {
			t.Errorf("ClassifyError(%d, malformed body) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestGeminiStreamDecoder_ForwardsDeltasAndCapturesUsageThenEOFOnConnectionClose(t *testing.T) {
	a := &GeminiAdapter{}
	sse := "" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}` + "\n\n"

	dec := a.NewStreamDecoder(fakeReadCloser{strings.NewReader(sse)}, "gemini-virtual", "req-abc")
	defer dec.Close()

	var texts []string
	var sawFinishReason string
	for {
		out, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		s := string(out)
		if !strings.Contains(s, `"id":"req-abc"`) || !strings.Contains(s, `"model":"gemini-virtual"`) {
			t.Errorf("chunk missing rewritten id/model: %s", s)
		}
		var chunk map[string]any
		payload := strings.TrimSuffix(strings.TrimPrefix(s, "data: "), "\n\n")
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk is not valid JSON: %s", s)
		}
		choices, _ := chunk["choices"].([]any)
		choice, _ := choices[0].(map[string]any)
		if delta, ok := choice["delta"].(map[string]any); ok {
			if c, ok := delta["content"].(string); ok && c != "" {
				texts = append(texts, c)
			}
		}
		if fr, ok := choice["finish_reason"].(string); ok {
			sawFinishReason = fr
		}
	}

	if got := strings.Join(texts, ""); got != "Hello" {
		t.Errorf("forwarded text = %q, want Hello", got)
	}
	if sawFinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", sawFinishReason)
	}
	usage := dec.Usage()
	if usage.InputTokens != 10 || usage.OutputTokens != 2 {
		t.Errorf("Usage() = %+v, want input=10 output=2", usage)
	}
}
