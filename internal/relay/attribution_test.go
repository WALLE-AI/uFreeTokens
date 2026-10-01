package relay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 公开排行榜的采集字段（迁移 00027）在流式 / 非流式两条路径上的取值。

func waitRequestLog(t *testing.T, query func() error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := query()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("query request_logs: %v (row never appeared)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestChatCompletions_Stream_RecordsAttribution(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for _, c := range []string{
			`{"id":"u","choices":[{"index":0,"delta":{"content":"Hi"}}]}`,
			// 同一个工具调用（index 0）分两个 chunk 下发，另有一个 index 1：共 2 个调用。
			`{"id":"u","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"f","arguments":""}}]}}]}`,
			`{"id":"u","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
			`{"id":"u","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c2","function":{"name":"g","arguments":"{}"}}]}}]}`,
			`{"id":"u","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
		} {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			fl.Flush()
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	raw, _ := json.Marshal(map[string]any{"model": vmName, "stream": true, "messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "what is this"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/a.png"}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/b.png"}},
		}},
	}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "  My \t App  ")
	req.Header.Set("HTTP-Referer", "https://User:pw@Example.COM:443/some/path?q=secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	requestID := resp.Header.Get("X-Request-Id")

	var genMs *int64
	var toolCalls, imageInputs int
	var appName, appURL string
	waitRequestLog(t, func() error {
		return pool.QueryRow(context.Background(),
			`SELECT gen_ms, tool_calls, image_inputs, app_name, app_url FROM request_logs WHERE request_id = $1`, requestID,
		).Scan(&genMs, &toolCalls, &imageInputs, &appName, &appURL)
	})
	if genMs == nil || *genMs <= 0 {
		t.Errorf("gen_ms = %v, want > 0 for a multi-chunk stream", genMs)
	}
	if toolCalls != 2 || imageInputs != 2 {
		t.Errorf("tool_calls/image_inputs = %d/%d, want 2/2", toolCalls, imageInputs)
	}
	if appName != "My App" || appURL != "https://example.com" {
		t.Errorf("app = %q / %q, want \"My App\" / https://example.com", appName, appURL)
	}
}

func TestChatCompletions_NonStream_RecordsToolCallsWithoutGenMs(t *testing.T) {
	pool, rdb, box := testPool(t), testRedis(t), testBox(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"u","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`)
	}))
	defer upstream.Close()

	fx, vmName := seedSimple(t, pool, box, upstream.URL, 1_000_000)
	handler, reqLogW := newTestGateway(t, pool, box, rdb)
	defer reqLogW.Close()
	gw := httptest.NewServer(handler)
	defer gw.Close()

	// 只有 HTTP-Referer、没有 X-Title：不算声明了应用。
	raw, _ := json.Marshal(map[string]any{"model": vmName, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+fx.apiKey)
	req.Header.Set("HTTP-Referer", "https://example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	requestID := resp.Header.Get("X-Request-Id")

	var genMs *int64
	var toolCalls, imageInputs int
	var appName, appURL *string
	waitRequestLog(t, func() error {
		return pool.QueryRow(context.Background(),
			`SELECT gen_ms, tool_calls, image_inputs, app_name, app_url FROM request_logs WHERE request_id = $1`, requestID,
		).Scan(&genMs, &toolCalls, &imageInputs, &appName, &appURL)
	})
	if genMs != nil {
		t.Errorf("gen_ms = %d, want NULL for non-streaming requests", *genMs)
	}
	if toolCalls != 1 || imageInputs != 0 {
		t.Errorf("tool_calls/image_inputs = %d/%d, want 1/0", toolCalls, imageInputs)
	}
	if appName != nil || appURL != nil {
		t.Errorf("app = %v / %v, want NULL without X-Title", appName, appURL)
	}
}
