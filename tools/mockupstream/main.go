// Command mockupstream 是本地联调用的 OpenAI 兼容上游：不需要真实的模型厂商账号，
// 就能把 网关 → 渠道 → 上游 的整条链路跑通（计费、请求日志、统计、渠道健康）。
// 仅用于本地开发与手工测试，不要部署到任何共享环境。
//
// 支持：
//
//	GET  /v1/models                 返回固定的模型列表（接入向导"测试连接"用）
//	POST /v1/chat/completions       非流式 / 流式（stream=true，SSE，末尾带 usage）
//	POST /v1/embeddings             返回固定维度的向量
//
// 可以用特殊的模型名触发异常，方便测试重试、熔断与渠道健康：
//
//	模型名包含 "fail"  → 返回 500
//	模型名包含 "limit" → 返回 429（Retry-After: 30）
//	模型名包含 "slow"  → 延迟 3 秒再返回
//
// 用法：go run ./tools/mockupstream -addr 127.0.0.1:18099
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

var models = []string{"mock-chat", "mock-chat-pro", "mock-reasoner", "mock-embedding", "mock-fail", "mock-limit", "mock-slow"}

func main() {
	addr := flag.String("addr", "127.0.0.1:18099", "监听地址")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]any, len(models))
		for i, m := range models {
			data[i] = map[string]any{"id": m, "object": "model", "owned_by": "mockai"}
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	})
	mux.HandleFunc("POST /v1/chat/completions", chat)
	mux.HandleFunc("POST /v1/embeddings", embeddings)

	log.Printf("mockupstream listening on http://%s/v1 (models: %s)", *addr, strings.Join(models, ", "))
	log.Fatal(http.ListenAndServe(*addr, logRequests(mux)))
}

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

// misbehave 按模型名模拟上游异常；返回 true 表示已经写出了错误响应。
func misbehave(w http.ResponseWriter, model string) bool {
	switch {
	case strings.Contains(model, "fail"):
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "mock upstream failure", "type": "server_error"}})
		return true
	case strings.Contains(model, "limit"):
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": "mock rate limit", "type": "rate_limit_error"}})
		return true
	case strings.Contains(model, "slow"):
		time.Sleep(3 * time.Second)
	}
	return false
}

func chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid json", "type": "invalid_request_error"}})
		return
	}
	if misbehave(w, req.Model) {
		return
	}
	last := ""
	if n := len(req.Messages); n > 0 {
		last = fmt.Sprint(req.Messages[n-1].Content)
	}
	reply := fmt.Sprintf("这是来自 mockupstream 的回复（模型 %s）。你说的是：%s", req.Model, last)
	promptTokens := 10 + len([]rune(last))/2
	completionTokens := len([]rune(reply)) / 2
	usage := map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens}
	id := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"}},
			"usage":   usage,
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for _, piece := range chunks(reply, 6) {
		send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": piece}, "finish_reason": nil}}})
		time.Sleep(40 * time.Millisecond)
	}
	send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": usage})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func embeddings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
		Input any    `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid json"}})
		return
	}
	if misbehave(w, req.Model) {
		return
	}
	inputs := []any{req.Input}
	if arr, ok := req.Input.([]any); ok {
		inputs = arr
	}
	data := make([]any, len(inputs))
	tokens := 0
	for i, in := range inputs {
		vec := make([]float64, 8)
		for j := range vec {
			vec[j] = float64((i+1)*(j+1)) / 100
		}
		data[i] = map[string]any{"object": "embedding", "index": i, "embedding": vec}
		tokens += 1 + len([]rune(fmt.Sprint(in)))/2
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "model": req.Model, "data": data,
		"usage": map[string]any{"prompt_tokens": tokens, "total_tokens": tokens}})
}

func chunks(s string, n int) []string {
	rs := []rune(s)
	var out []string
	for i := 0; i < len(rs); i += n {
		out = append(out, string(rs[i:min(i+n, len(rs))]))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s) auth=%t", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond), r.Header.Get("Authorization") != "")
	})
}
