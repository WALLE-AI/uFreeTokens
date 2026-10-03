package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
)

func TestFromConfig(t *testing.T) {
	t.Setenv("TEST_LLM_KEY", "sk-test")
	c, missing := FromConfig(config.DataSyncConfig{LLMBaseURL: "http://x/v1/", LLMModel: "m", LLMAPIKeyEnv: "TEST_LLM_KEY"})
	if c == nil || c.BaseURL != "http://x/v1" || c.APIKey != "sk-test" || len(missing) != 0 {
		t.Fatalf("got %+v, missing %v", c, missing)
	}

	// 把密钥本身误填进 llm_api_key_env：不可用，且提示里不能出现密钥。
	c, missing = FromConfig(config.DataSyncConfig{LLMBaseURL: "http://x/v1", LLMModel: "m", LLMAPIKeyEnv: "sk-secret-value"})
	if c != nil || len(missing) != 1 || strings.Contains(missing[0], "sk-secret-value") {
		t.Errorf("got %+v, missing %v", c, missing)
	}

	c, missing = FromConfig(config.DataSyncConfig{LLMAPIKeyEnv: "TEST_LLM_UNSET_KEY"})
	if c != nil || len(missing) != 3 {
		t.Errorf("got %+v, missing %v", c, missing)
	}
}

// 端点不支持 JSON 模式（如网关返回 503 no_available_channel）时，去掉 response_format 重试。
func TestChatJSONFallsBackWithoutJSONMode(t *testing.T) {
	var calls []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, jsonMode := body["response_format"]
		calls = append(calls, jsonMode)
		if jsonMode {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"no_available_channel"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"description\":\"ok\"}"}}]}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Model: "m"}
	got, err := c.ChatJSON(context.Background(), "sys", "user")
	if err != nil || got != `{"description":"ok"}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if len(calls) != 2 || !calls[0] || calls[1] {
		t.Errorf("calls (json mode) = %v, want [true false]", calls)
	}
}

func TestStripCodeFence(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`:                 `{"a":1}`,
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"<think>想一想 {x}</think>\n{\"a\":1}":      `{"a":1}`,
		"好的，结果如下：\n```json\n{\"a\":1}\n```\n以上。": `{"a":1}`,
	}
	for in, want := range cases {
		if got := StripCodeFence(in); got != want {
			t.Errorf("StripCodeFence(%q) = %q, want %q", in, got, want)
		}
	}
}
