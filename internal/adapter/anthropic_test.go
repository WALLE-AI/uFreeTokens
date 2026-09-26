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

func anthropicTestTarget(overrides map[string]any) Target {
	return Target{
		Channel: &catalog.Channel{
			ID:             1,
			UpstreamModel:  "claude-sonnet-4-5",
			ParamOverrides: overrides,
		},
		Account: &catalog.ProviderAccount{
			ID: 1, Protocol: "anthropic", BaseURL: "https://api.anthropic.com/v1", CostMultiplier: decimal.NewFromInt(1),
		},
		Key: &catalog.ProviderKey{ID: 1, Secret: "sk-ant-real-secret"},
	}
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode request body: %v (body: %s)", err, raw)
	}
	return m
}

func TestAnthropicBuildRequest_UsesMessagesEndpointAndAPIKeyHeader(t *testing.T) {
	a := &AnthropicAdapter{}
	body := map[string]any{"model": "claude-virtual", "messages": []any{
		map[string]any{"role": "user", "content": "hi"},
	}, "stream": false}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "irrelevant-for-this-adapter", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if req.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("URL = %s, want .../v1/messages", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "sk-ant-real-secret" {
		t.Errorf("x-api-key = %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != anthropicAPIVersion {
		t.Errorf("anthropic-version = %q, want %q", got, anthropicAPIVersion)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header should not be set for anthropic (uses x-api-key instead), got %q", got)
	}

	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	if m["model"] != "claude-sonnet-4-5" {
		t.Errorf("model = %v, want upstream model claude-sonnet-4-5 (must not leak virtual model name)", m["model"])
	}
}

func TestAnthropicBuildRequest_ExtractsSystemMessageOutOfMessagesArray(t *testing.T) {
	a := &AnthropicAdapter{}
	body := map[string]any{"model": "x", "messages": []any{
		map[string]any{"role": "system", "content": "You are terse."},
		map[string]any{"role": "user", "content": "hi"},
	}}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)

	if m["system"] != "You are terse." {
		t.Errorf("system = %v, want %q", m["system"], "You are terse.")
	}
	msgs, _ := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want exactly 1 (system message must be pulled out)", msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("remaining message role = %v, want user", first["role"])
	}
}

func TestAnthropicBuildRequest_JoinsMultipleSystemMessages(t *testing.T) {
	a := &AnthropicAdapter{}
	body := map[string]any{"model": "x", "messages": []any{
		map[string]any{"role": "system", "content": "First."},
		map[string]any{"role": "system", "content": "Second."},
		map[string]any{"role": "user", "content": "hi"},
	}}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	if m["system"] != "First.\n\nSecond." {
		t.Errorf("system = %v, want joined with blank line", m["system"])
	}
}

func TestAnthropicBuildRequest_DefaultsMaxTokensWhenMissing(t *testing.T) {
	a := &AnthropicAdapter{}
	body := map[string]any{"model": "x", "messages": []any{}}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	if got, ok := m["max_tokens"].(float64); !ok || int(got) != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens = %v, want default %d", m["max_tokens"], anthropicDefaultMaxTokens)
	}
}

func TestAnthropicBuildRequest_PassesThroughExplicitMaxTokens(t *testing.T) {
	a := &AnthropicAdapter{}
	body := map[string]any{"model": "x", "messages": []any{}, "max_tokens": float64(2048)}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	if got, ok := m["max_tokens"].(float64); !ok || int(got) != 2048 {
		t.Errorf("max_tokens = %v, want 2048 (must not be overridden by the default)", m["max_tokens"])
	}
}

func TestAnthropicBuildRequest_ConvertsStopToStopSequences(t *testing.T) {
	a := &AnthropicAdapter{}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", map[string]any{
		"model": "x", "messages": []any{}, "stop": "STOP",
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	m := decodeBody(t, raw)
	seqs, _ := m["stop_sequences"].([]any)
	if len(seqs) != 1 || seqs[0] != "STOP" {
		t.Errorf("stop_sequences = %v, want [STOP]", m["stop_sequences"])
	}

	req2, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", map[string]any{
		"model": "x", "messages": []any{}, "stop": []any{"A", "B"},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw2, _ := io.ReadAll(req2.Body)
	m2 := decodeBody(t, raw2)
	seqs2, _ := m2["stop_sequences"].([]any)
	if len(seqs2) != 2 || seqs2[0] != "A" || seqs2[1] != "B" {
		t.Errorf("stop_sequences = %v, want [A B]", m2["stop_sequences"])
	}
}

func TestAnthropicBuildRequest_AppliesParamOverrides(t *testing.T) {
	a := &AnthropicAdapter{}
	overrides := map[string]any{
		"temperature": nil,           // 该渠道不支持自定义温度，应被删除
		"max_tokens":  float64(1000), // 覆盖为渠道限定的上限
	}
	body := map[string]any{"model": "x", "messages": []any{}, "temperature": float64(0.7), "max_tokens": float64(4096)}

	req, err := a.BuildRequest(context.Background(), anthropicTestTarget(overrides), "", body)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	s := string(raw)
	if strings.Contains(s, "temperature") {
		t.Errorf("temperature should have been removed by override: %s", s)
	}
	if !strings.Contains(s, `"max_tokens":1000`) {
		t.Errorf("max_tokens should be overridden to 1000: %s", s)
	}
}

func TestAnthropicBuildRequest_RejectsToolsAndToolMessages(t *testing.T) {
	a := &AnthropicAdapter{}

	if _, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", map[string]any{
		"model": "x", "messages": []any{}, "tools": []any{map[string]any{"type": "function"}},
	}); err == nil {
		t.Error("expected an error when the request declares tools (not supported yet)")
	}

	if _, err := a.BuildRequest(context.Background(), anthropicTestTarget(nil), "", map[string]any{
		"model": "x", "messages": []any{map[string]any{"role": "tool", "content": "result"}},
	}); err == nil {
		t.Error("expected an error for a tool-result message (not supported yet)")
	}
}

func TestAnthropicDecodeResponse_ExtractsUsageConcatenatesTextAndMapsStopReason(t *testing.T) {
	a := &AnthropicAdapter{}
	body := []byte(`{
		"id": "msg_upstream_xyz",
		"model": "claude-sonnet-4-5",
		"content": [{"type":"text","text":"Hello, "},{"type":"text","text":"world."}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 25, "output_tokens": 12, "cache_read_input_tokens": 5, "cache_creation_input_tokens": 3}
	}`)

	m, usage, err := a.DecodeResponse(body, "claude-virtual", "req-123")
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if m["model"] != "claude-virtual" {
		t.Errorf("model = %v, want claude-virtual (must not leak upstream model name)", m["model"])
	}
	if m["id"] != "req-123" {
		t.Errorf("id = %v, want req-123 (must not leak upstream message id)", m["id"])
	}
	choices, _ := m["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v, want exactly 1", choices)
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	if msg["content"] != "Hello, world." {
		t.Errorf("message.content = %v, want concatenated text blocks", msg["content"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}

	if usage.InputTokens != 25 {
		t.Errorf("InputTokens = %d, want 25 (Anthropic input_tokens excludes cache already, no subtraction needed)", usage.InputTokens)
	}
	if usage.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens = %d, want 5", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 3 {
		t.Errorf("CacheWriteTokens = %d, want 3", usage.CacheWriteTokens)
	}
	if usage.OutputTokens != 12 {
		t.Errorf("OutputTokens = %d, want 12", usage.OutputTokens)
	}
	if usage.Source != "upstream" {
		t.Errorf("Source = %q, want upstream", usage.Source)
	}
}

func TestAnthropicDecodeResponse_MapsMaxTokensAndToolUseStopReasons(t *testing.T) {
	a := &AnthropicAdapter{}
	cases := []struct {
		stopReason string
		want       string
	}{
		{"end_turn", "stop"},
		{"stop_sequence", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "tool_calls"},
		{"something_unrecognized", "stop"},
	}
	for _, c := range cases {
		body := []byte(`{"content":[],"stop_reason":"` + c.stopReason + `","usage":{}}`)
		m, _, err := a.DecodeResponse(body, "vm", "req")
		if err != nil {
			t.Fatalf("DecodeResponse(%s): %v", c.stopReason, err)
		}
		choices, _ := m["choices"].([]any)
		choice, _ := choices[0].(map[string]any)
		if choice["finish_reason"] != c.want {
			t.Errorf("stop_reason %q -> finish_reason %v, want %q", c.stopReason, choice["finish_reason"], c.want)
		}
	}
}

func TestAnthropicClassifyError_ByErrorTypeField(t *testing.T) {
	a := &AnthropicAdapter{}
	cases := []struct {
		status int
		body   string
		want   ErrorClass
	}{
		{429, `{"type":"error","error":{"type":"rate_limit_error"}}`, ErrClassRateLimited},
		{401, `{"type":"error","error":{"type":"authentication_error"}}`, ErrClassKeyInvalid},
		{403, `{"type":"error","error":{"type":"permission_error"}}`, ErrClassKeyInvalid},
		{400, `{"type":"error","error":{"type":"invalid_request_error"}}`, ErrClassBadRequest},
		{404, `{"type":"error","error":{"type":"not_found_error"}}`, ErrClassBadRequest},
		{413, `{"type":"error","error":{"type":"request_too_large"}}`, ErrClassBadRequest},
		{500, `{"type":"error","error":{"type":"api_error"}}`, ErrClassUpstreamUnavailable},
		{529, `{"type":"error","error":{"type":"overloaded_error"}}`, ErrClassUpstreamUnavailable},
	}
	for _, c := range cases {
		got := a.ClassifyError(c.status, []byte(c.body))
		if got != c.want {
			t.Errorf("ClassifyError(%d, %q) = %q, want %q", c.status, c.body, got, c.want)
		}
	}
}

func TestAnthropicClassifyError_FallsBackToStatusCodeWhenErrorTypeMissing(t *testing.T) {
	a := &AnthropicAdapter{}
	cases := []struct {
		status int
		want   ErrorClass
	}{
		{429, ErrClassRateLimited},
		{401, ErrClassKeyInvalid},
		{403, ErrClassKeyInvalid},
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

func TestAnthropicStreamDecoder_TranslatesEventsToOpenAIChunksAndCapturesUsage(t *testing.T) {
	a := &AnthropicAdapter{}
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":30,\"cache_read_input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	dec := a.NewStreamDecoder(fakeReadCloser{strings.NewReader(sse)}, "claude-virtual", "req-abc")
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
		if !strings.HasPrefix(s, "data: ") {
			t.Fatalf("chunk is not an SSE data line: %s", s)
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSuffix(s, "\n\n"), "data: ")), &chunk); err != nil {
			t.Fatalf("chunk is not valid JSON: %s", s)
		}
		if chunk["model"] != "claude-virtual" || chunk["id"] != "req-abc" {
			t.Errorf("chunk missing rewritten id/model: %v", chunk)
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
		t.Errorf("forwarded text = %q, want %q", got, "Hello")
	}
	if sawFinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", sawFinishReason)
	}

	usage := dec.Usage()
	if usage.InputTokens != 30 || usage.CacheReadTokens != 10 || usage.OutputTokens != 2 {
		t.Errorf("Usage() = %+v, want input=30 cacheRead=10 output=2", usage)
	}
}

func TestAnthropicStreamDecoder_PropagatesMidStreamErrorEvent(t *testing.T) {
	a := &AnthropicAdapter{}
	sse := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"

	dec := a.NewStreamDecoder(fakeReadCloser{strings.NewReader(sse)}, "vm", "req")
	defer dec.Close()

	_, err := dec.Next()
	if err == nil {
		t.Fatal("expected an error for a mid-stream error event")
	}
}

func TestAnthropicStreamDecoder_SkipsNonTextDeltasWithoutCrashing(t *testing.T) {
	a := &AnthropicAdapter{}
	sse := "" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"x\\\":1}\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	dec := a.NewStreamDecoder(fakeReadCloser{strings.NewReader(sse)}, "vm", "req")
	defer dec.Close()

	_, err := dec.Next()
	if err != io.EOF {
		t.Fatalf("Next() error = %v, want io.EOF (input_json_delta should be skipped, not forwarded or errored)", err)
	}
}
