package relay

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeAppName(t *testing.T) {
	for in, want := range map[string]string{
		"  Cline  ":              "Cline",
		"My\t\x01 App\n":         "My App",
		"\x00\x07":               "",
		strings.Repeat("长", 100): strings.Repeat("长", maxAppNameRunes),
		"bad\xffutf8":            "badutf8",
	} {
		if got := normalizeAppName(in); got != want {
			t.Errorf("normalizeAppName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAppURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://Example.com/path?q=1":  "https://example.com",
		"https://u:p@example.com:443/x": "https://example.com",
		"http://localhost:8080/":        "http://localhost:8080",
		"ftp://example.com":             "",
		"javascript:alert(1)":           "",
		"not a url":                     "",
		"":                              "",
	} {
		if got := normalizeAppURL(in); got != want {
			t.Errorf("normalizeAppURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStreamStats(t *testing.T) {
	var st streamStats
	if st.genMillis() != nil {
		t.Error("no chunks: gen_ms must be nil")
	}
	t0 := time.Now()
	st.observe([]byte(`data: {"choices":[{"index":0,"delta":{"content":"a"}}]}`), t0)
	if st.genMillis() != nil {
		t.Error("single chunk: gen_ms must be nil")
	}
	st.observe([]byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0},{"index":1}]}},{"index":1,"delta":{"tool_calls":[{"index":0}]}}]}`), t0.Add(100*time.Millisecond))
	st.observe([]byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1}]}}]}`), t0.Add(250*time.Millisecond))
	st.observe([]byte(`data: not json "tool_calls"`), t0.Add(300*time.Millisecond))
	if ms := st.genMillis(); ms == nil || *ms != 300 {
		t.Errorf("gen_ms = %v, want 300", ms)
	}
	if len(st.toolCalls) != 3 {
		t.Errorf("tool calls = %d, want 3 distinct (choice, index) pairs", len(st.toolCalls))
	}
}

func TestCountImageInputsAndToolCalls(t *testing.T) {
	req := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "plain"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url"}, map[string]any{"type": "text"}, map[string]any{"type": "image_url"},
		}},
	}}
	if n := countImageInputs(req); n != 2 {
		t.Errorf("countImageInputs = %d, want 2", n)
	}
	resp := map[string]any{"choices": []any{
		map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{}, map[string]any{}}}},
		map[string]any{"message": map[string]any{"content": "x"}},
	}}
	if n := countToolCalls(resp); n != 2 {
		t.Errorf("countToolCalls = %d, want 2", n)
	}
}
