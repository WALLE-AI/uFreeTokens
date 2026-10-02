// messagesResponseWriter/anthropicRequestToOpenAI/openAIResponseToAnthropic 都是
// 未导出的，测试必须放在 package relay 内部（同样的理由见 helpers_internal_test.go
// 顶部注释）。
package relay

import (
	"testing"
)

func TestAnthropicRequestToOpenAI_BasicFields(t *testing.T) {
	in := map[string]any{
		"model": "claude-virtual", "max_tokens": float64(1024), "temperature": float64(0.7),
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	out, err := anthropicRequestToOpenAI(in)
	if err != nil {
		t.Fatalf("anthropicRequestToOpenAI: %v", err)
	}
	if out["model"] != "claude-virtual" {
		t.Errorf("model = %v, want claude-virtual", out["model"])
	}
	if out["max_tokens"] != float64(1024) {
		t.Errorf("max_tokens = %v, want 1024", out["max_tokens"])
	}
	if out["temperature"] != float64(0.7) {
		t.Errorf("temperature = %v, want 0.7", out["temperature"])
	}
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want exactly 1", msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hi" {
		t.Errorf("first message = %+v, want role=user content=hi", first)
	}
}

func TestAnthropicRequestToOpenAI_ExtractsSystemAsMessage(t *testing.T) {
	in := map[string]any{
		"model": "x", "system": "be terse",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	out, err := anthropicRequestToOpenAI(in)
	if err != nil {
		t.Fatalf("anthropicRequestToOpenAI: %v", err)
	}
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v, want 2 (system + user)", msgs)
	}
	sysMsg, _ := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != "be terse" {
		t.Errorf("system message = %+v, want role=system content='be terse'", sysMsg)
	}
}

func TestAnthropicRequestToOpenAI_JoinsTextContentBlocks(t *testing.T) {
	in := map[string]any{
		"model": "x",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "Hello, "},
				map[string]any{"type": "text", "text": "world."},
			},
		}},
	}
	out, err := anthropicRequestToOpenAI(in)
	if err != nil {
		t.Fatalf("anthropicRequestToOpenAI: %v", err)
	}
	msgs, _ := out["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["content"] != "Hello, world." {
		t.Errorf("content = %v, want joined text blocks", first["content"])
	}
}

func TestAnthropicRequestToOpenAI_MapsStopSequencesAndStream(t *testing.T) {
	in := map[string]any{
		"model": "x", "messages": []any{}, "stream": true, "stop_sequences": []any{"STOP"},
	}
	out, err := anthropicRequestToOpenAI(in)
	if err != nil {
		t.Fatalf("anthropicRequestToOpenAI: %v", err)
	}
	if out["stream"] != true {
		t.Errorf("stream = %v, want true", out["stream"])
	}
	stop, _ := out["stop"].([]any)
	if len(stop) != 1 || stop[0] != "STOP" {
		t.Errorf("stop = %v, want [STOP]", out["stop"])
	}
}

func TestAnthropicRequestToOpenAI_RejectsToolsAndToolMessages(t *testing.T) {
	if _, err := anthropicRequestToOpenAI(map[string]any{
		"model": "x", "messages": []any{}, "tools": []any{map[string]any{"name": "f"}},
	}); err == nil {
		t.Error("expected an error when tools is present")
	}
	if _, err := anthropicRequestToOpenAI(map[string]any{
		"model": "x", "messages": []any{map[string]any{"role": "tool", "content": "result"}},
	}); err == nil {
		t.Error("expected an error for a tool-result message")
	}
}

func TestExtractAnthropicText(t *testing.T) {
	if text, ok := extractAnthropicText("hi"); !ok || text != "hi" {
		t.Errorf("string content: text=%q ok=%v, want hi/true", text, ok)
	}
	if _, ok := extractAnthropicText(""); ok {
		t.Error("empty string should report ok=false")
	}
	if _, ok := extractAnthropicText(nil); ok {
		t.Error("nil content should report ok=false")
	}
	if text, ok := extractAnthropicText([]any{
		map[string]any{"type": "text", "text": "a"},
		map[string]any{"type": "image", "text": "ignored"},
		map[string]any{"type": "text", "text": "b"},
	}); !ok || text != "ab" {
		t.Errorf("mixed blocks: text=%q ok=%v, want ab/true", text, ok)
	}
}

func TestOpenAIResponseToAnthropic_TranslatesContentAndUsage(t *testing.T) {
	m := map[string]any{
		"id": "req-123", "model": "claude-virtual",
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "hello"},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": float64(10), "completion_tokens": float64(5)},
	}
	out := openAIResponseToAnthropic(m)
	if out["id"] != "req-123" || out["type"] != "message" || out["role"] != "assistant" {
		t.Errorf("out = %+v", out)
	}
	content, _ := out["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v, want exactly 1 block", content)
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "hello" {
		t.Errorf("content block = %+v", block)
	}
	if out["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", out["stop_reason"])
	}
	usage, _ := out["usage"].(map[string]any)
	if usage["input_tokens"] != int64(10) || usage["output_tokens"] != int64(5) {
		t.Errorf("usage = %+v, want input=10 output=5", usage)
	}
}

func TestOpenAIFinishReasonToAnthropic(t *testing.T) {
	cases := map[string]string{
		"stop": "end_turn", "length": "max_tokens", "tool_calls": "tool_use", "": "end_turn", "weird": "end_turn",
	}
	for in, want := range cases {
		if got := openAIFinishReasonToAnthropic(in); got != want {
			t.Errorf("openAIFinishReasonToAnthropic(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAnthropicRequestToOpenAI_StreamRequestsUsage：流式请求必须带上 include_usage，
// 否则只在 usage-only chunk 里返回用量的上游（百炼、方舟）会让 message_delta 的
// output_tokens 变成 0。
func TestAnthropicRequestToOpenAI_StreamRequestsUsage(t *testing.T) {
	out, err := anthropicRequestToOpenAI(map[string]any{"model": "m", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	so, _ := out["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Errorf("stream_options = %#v, want include_usage=true", out["stream_options"])
	}
	out, _ = anthropicRequestToOpenAI(map[string]any{"model": "m", "messages": []any{}})
	if _, ok := out["stream_options"]; ok {
		t.Error("non-stream request must not carry stream_options")
	}
}
