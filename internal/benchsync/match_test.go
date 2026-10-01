package benchsync

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct{ in, base, variant string }{
		{"claude-opus-4-6-high", "claude-opus-4-6", "high"},
		{"gpt-4o-2024-05-13", "gpt-4o", ""},
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet", ""},
		{"deepseek-v4-pro_max", "deepseek-v4-pro", "max"},
		{"gpt-6.1-sol_max", "gpt-6-1-sol", "max"},
		{"Claude Opus 5.5", "claude-opus-5-5", ""},
		{"GPT-5 (high)", "gpt-5", "high"},
		{"anthropic/claude-sonnet-4.5", "claude-sonnet-4-5", ""},
		{"claude-4-sonnet-thinking-16k", "claude-4-sonnet", "thinking-16k"},
		{"qwen3.8-max-0902", "qwen3-8-max-0902", ""},
		{"gemini-2.5-pro-latest", "gemini-2-5-pro", ""},
		{"glm-5.2_unknown", "glm-5-2", "unknown"},
	}
	for _, c := range cases {
		base, variant := Normalize(c.in)
		if base != c.base || variant != c.variant {
			t.Errorf("Normalize(%q) = (%q, %q), want (%q, %q)", c.in, base, variant, c.base, c.variant)
		}
	}
}

func TestMatcher(t *testing.T) {
	m := NewMatcher([]ModelCandidate{
		{ID: 1, Names: []string{"anthropic/claude-opus-4.6", "Claude Opus 4.6"}},
		{ID: 2, Names: []string{"deepseek-ai/DeepSeek-V4-Pro"}},
		{ID: 3, Names: []string{"gpt-5.5"}},
		// 4 和 5 归一化后同名：有歧义，不能自动关联。
		{ID: 4, Names: []string{"qwen3-max"}},
		{ID: 5, Names: []string{"qwen3-max-2025-09-23"}},
		{ID: 6, Names: []string{"kimi-k3"}},
		{ID: 7, Names: []string{"qwen/qwen3.7-max"}},
		{ID: 8, Names: []string{"openai/gpt-5.1-codex-max"}},
	})
	cases := []struct {
		label  string
		id     int64
		method string
	}{
		{"Claude Opus 4.6", 1, "exact"},
		{"claude-opus-4-6-high", 1, "normalized"},
		{"claude-opus-4-6-thinking-32k", 1, "normalized"},
		{"deepseek-v4-pro_max", 2, "normalized"},
		{"GPT-5.5 (high)", 3, "normalized"},
		{"gpt-5", 0, "none"},            // 版本号不同，不能模糊到 gpt-5.5
		{"kimi-k3-preview", 6, "fuzzy"}, // 只是建议
		{"llama-4-maverick", 0, "none"},
		{"kimi-k3-max", 6, "normalized"},          // max 是档位
		{"qwen3.7-max-20260517", 7, "normalized"}, // max 是型号的一部分
		{"qwen3.7-plus", 0, "none"},               // 不能被当成 qwen3.7-max 的建议
		{"gpt-5.1-codex-max-high", 8, "normalized"},
		{"claude-sonnet-4-6", 0, "none"}, // 不能被建议成 opus
	}
	for _, c := range cases {
		got := m.Match(c.label)
		if got.VirtualModelID != c.id || got.Method != c.method {
			t.Errorf("Match(%q) = %d/%s, want %d/%s", c.label, got.VirtualModelID, got.Method, c.id, c.method)
		}
	}
	if got := m.Match("qwen3-max"); got.Method != "exact" || got.VirtualModelID != 4 {
		t.Errorf("exact name wins over normalized ambiguity, got %+v", got)
	}
	if got := m.Match("Qwen3 Max"); got.Method == "normalized" {
		t.Errorf("ambiguous normalized base must not auto-link, got %+v", got)
	}
}
