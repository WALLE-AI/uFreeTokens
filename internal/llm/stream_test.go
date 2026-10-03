package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T, status int, lines []string, check func(map[string]any)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if check != nil {
			check(payload)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			_, _ = io.WriteString(w, l+"\n\n")
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, APIKey: "k", Model: "m"}
}

func TestChatTools_TextStream(t *testing.T) {
	c := sseServer(t, 200, []string{
		`: ping`,
		`data: {"choices":[{"delta":{"content":"<thi"}}]}`,
		`data: {"choices":[{"delta":{"content":"nk>hidden</think>你好"}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"，再想想","content":"，世界"},"finish_reason":"stop"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5}}`,
		`data: [DONE]`,
	}, func(p map[string]any) {
		if p["stream"] != true {
			t.Errorf("stream = %v, want true", p["stream"])
		}
		if so, _ := p["stream_options"].(map[string]any); so["include_usage"] != true {
			t.Errorf("stream_options = %v", p["stream_options"])
		}
	})
	var deltas, thoughts []string
	out, err := c.ChatTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{}, func(e StreamEvent) {
		deltas = append(deltas, e.TextDelta)
		thoughts = append(thoughts, e.ReasoningDelta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "你好，世界" {
		t.Errorf("Content = %q", out.Content)
	}
	if strings.Join(deltas, "") != out.Content {
		t.Errorf("deltas = %q", deltas)
	}
	if out.Reasoning != "hidden，再想想" || strings.Join(thoughts, "") != out.Reasoning {
		t.Errorf("Reasoning = %q, thoughts = %q", out.Reasoning, thoughts)
	}
	if out.Usage.PromptTokens != 12 || out.Usage.CompletionTokens != 5 || out.FinishReason != "stop" {
		t.Errorf("usage/finish = %+v %q", out.Usage, out.FinishReason)
	}
}

func TestChatTools_ToolCallFragments(t *testing.T) {
	c := sseServer(t, 200, []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_todo_counts","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"list_price_change_requests","arguments":"{\"sta"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"tus\":\"pending\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, func(p map[string]any) {
		tools, _ := p["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("tools = %v", p["tools"])
		}
		msgs := p["messages"].([]any)
		asst := msgs[1].(map[string]any)
		if asst["content"] != nil {
			t.Errorf("assistant content with only tool_calls should be null, got %v", asst["content"])
		}
	})
	msgs := []Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "x", Name: "t", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "x", Content: "{}"},
	}
	out, err := c.ChatTools(context.Background(), msgs, []ToolDef{{Name: "t", Description: "d"}}, ChatOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.ToolCalls[0].ID != "call_a" || out.ToolCalls[0].Arguments != "{}" {
		t.Errorf("call 0 = %+v", out.ToolCalls[0])
	}
	if out.ToolCalls[1].Name != "list_price_change_requests" || out.ToolCalls[1].Arguments != `{"status":"pending"}` {
		t.Errorf("call 1 = %+v", out.ToolCalls[1])
	}
}

func TestChatTools_UpstreamErrorAndInterrupted(t *testing.T) {
	c := sseServer(t, 503, nil, nil)
	_, err := c.ChatTools(context.Background(), []Message{{Role: "user", Content: "q"}}, nil, ChatOptions{}, nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 503 {
		t.Fatalf("err = %v, want HTTPError 503", err)
	}

	c = sseServer(t, 200, []string{`data: {"choices":[{"delta":{"content":"半"}}]}`}, nil)
	_, err = c.ChatTools(context.Background(), []Message{{Role: "user", Content: "q"}}, nil, ChatOptions{}, nil)
	if !errors.Is(err, ErrStreamInterrupted) {
		t.Fatalf("err = %v, want ErrStreamInterrupted", err)
	}
}

func TestChatTools_NonStreamFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Model: "m"}
	out, err := c.ChatTools(context.Background(), []Message{{Role: "user", Content: "q"}}, nil, ChatOptions{NoStream: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" || len(out.ToolCalls) != 1 || out.ToolCalls[0].Arguments != `{"a":1}` || out.Usage.PromptTokens != 3 {
		t.Errorf("out = %+v", out)
	}
}

func TestStripThink(t *testing.T) {
	cases := map[string]string{
		"<think>x</think>答案": "答案",
		"a < b":              "a < b",
		"无标签":                "无标签",
		"<think>未闭合":         "",
	}
	for in, want := range cases {
		if got := StripThink(in); got != want {
			t.Errorf("StripThink(%q) = %q, want %q", in, got, want)
		}
	}
}
