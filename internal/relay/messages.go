package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// Messages 是 POST /v1/messages 的 http.HandlerFunc（技术方案 Phase 2 的
// "/v1/messages 入口"）：接受 Anthropic Messages API 原生请求形状，翻译成内部
// 通用的 OpenAI 兼容形状后直接复用 ChatCompletions 的完整管线（鉴权/限流/预扣/
// 路由/重试/结算全部一样，包括上游选的是 openai/anthropic/gemini 协议的渠道都
// 行），再把结果（非流式 JSON、流式 SSE）翻译回 Anthropic 形状写给客户端。
// 这不是一套平行实现，只是 ChatCompletions 外面的一层协议转换壳——好处是
// bug 修复、新功能只需要改一处；代价是 request_logs.endpoint 记的仍然是
// "chat.completions"，不区分客户端用的是哪种协议入口进来的（已知限制）。
//
// 已知范围限制（和 internal/adapter.AnthropicAdapter 保持对称，理由见那边的
// 包注释）：不支持 tool/function calling；content 数组只识别 text 与 image 块；非 200
// 的错误响应直接透传 OpenAI 形状的错误体，不翻译成 Anthropic 的
// {"type":"error",...} 形状。
func (s *Service) Messages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID := httpx.RequestIDFromContext(ctx)

	_, anthropicBody, ok := s.readJSON(w, r)
	if !ok {
		return
	}
	openAIBody, err := anthropicRequestToOpenAI(anthropicBody)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	modelName, _ := anthropicBody["model"].(string)

	translated, err := json.Marshal(openAIBody)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to translate request.")
		return
	}
	innerReq, err := http.NewRequestWithContext(ctx, r.Method, r.URL.String(), bytes.NewReader(translated))
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to build internal request.")
		return
	}
	innerReq.Header = r.Header.Clone()
	innerReq.Header.Set("Content-Type", "application/json")
	innerReq.RemoteAddr = r.RemoteAddr

	wrapped := &messagesResponseWriter{ResponseWriter: w, reqID: requestID, vmName: modelName}
	s.ChatCompletions(wrapped, innerReq)
	wrapped.finish()
}

// anthropicRequestToOpenAI 把 Anthropic Messages API 请求体翻译成 OpenAI 兼容
// 形状——这是 internal/adapter.AnthropicAdapter.BuildRequest 反方向的翻译
// （那边是"内部 OpenAI 形状 -> 发给 Anthropic 上游"，这里是"客户端发来的
// Anthropic 形状 -> 内部 OpenAI 形状"）。
func anthropicRequestToOpenAI(body map[string]any) (map[string]any, error) {
	if _, hasTools := body["tools"]; hasTools {
		return nil, errors.New("relay: tool/function calling is not supported on /v1/messages yet")
	}

	rawMessages, _ := body["messages"].([]any)
	outMessages := make([]any, 0, len(rawMessages)+1)

	if sysText, ok := extractAnthropicText(body["system"]); ok && sysText != "" {
		outMessages = append(outMessages, map[string]any{"role": "system", "content": sysText})
	}

	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "" {
			continue
		}
		if role == "tool" {
			return nil, errors.New("relay: tool-result messages are not supported on /v1/messages yet")
		}
		outMessages = append(outMessages, map[string]any{"role": role, "content": anthropicContentToOpenAI(msg["content"])})
	}

	out := map[string]any{"model": body["model"], "messages": outMessages}
	if v, ok := body["max_tokens"]; ok {
		out["max_tokens"] = v
	}
	if v, ok := body["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := body["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := body["stream"]; ok {
		out["stream"] = v
		// 流式时显式要 usage：有的上游（百炼、方舟）只在最后一个 choices 为空的
		// usage-only chunk 里返回用量，而 ChatCompletions 只有在客户端自己要了
		// include_usage 时才转发这个 chunk。这里的"客户端"是 messagesResponseWriter，
		// 它把 usage 折进 message_delta，不会把原始 chunk 透给 Anthropic 客户端。
		if stream, _ := v.(bool); stream {
			out["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if v, ok := body["stop_sequences"]; ok {
		out["stop"] = v
	}
	return out, nil
}

// anthropicContentToOpenAI 翻译一条消息的 content：没有图片时拼成纯文本字符串
// （与之前的行为一致）；有图片时保留为 OpenAI 的 content 数组——text 块转
// {"type":"text"}，image 块（source.type 为 base64 或 url）转 {"type":"image_url"}。
// 其他类型的块（tool_use、document 等）忽略。
func anthropicContentToOpenAI(content any) any {
	blocks, ok := content.([]any)
	if !ok {
		text, _ := extractAnthropicText(content)
		return text
	}
	hasImage := false
	for _, b := range blocks {
		if m, _ := b.(map[string]any); m != nil && m["type"] == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		text, _ := extractAnthropicText(content)
		return text
	}
	parts := make([]any, 0, len(blocks))
	for _, b := range blocks {
		m, _ := b.(map[string]any)
		if m == nil {
			continue
		}
		switch m["type"] {
		case "text":
			if text, _ := m["text"].(string); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "image":
			if url := anthropicImageURL(m["source"]); url != "" {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
		}
	}
	return parts
}

// anthropicImageURL 把 Anthropic 的 image source 转成 OpenAI image_url.url：
// {"type":"base64","media_type","data"} -> data URL；{"type":"url","url"} -> 原 URL。
func anthropicImageURL(source any) string {
	src, _ := source.(map[string]any)
	switch src["type"] {
	case "base64":
		mediaType, _ := src["media_type"].(string)
		data, _ := src["data"].(string)
		if mediaType == "" || data == "" {
			return ""
		}
		return "data:" + mediaType + ";base64," + data
	case "url":
		url, _ := src["url"].(string)
		return url
	}
	return ""
}

// extractAnthropicText 从 Anthropic 的 content 字段（字符串，或
// [{"type":"text","text":...}, ...] 数组）里提取纯文本。ok=false 表示这个字段
// 缺失或者不是可识别的文本形态（比如全是非文本块）。
func extractAnthropicText(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return c, c != ""
	case []any:
		var sb strings.Builder
		found := false
		for _, block := range c {
			m, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t != "text" {
				continue
			}
			text, _ := m["text"].(string)
			sb.WriteString(text)
			found = true
		}
		return sb.String(), found
	default:
		return "", false
	}
}

// openAIResponseToAnthropic 把 ChatCompletions 产出的 OpenAI 形状非流式响应
// 翻译成 Anthropic Messages API 的响应形状（上面翻译的反方向）。
func openAIResponseToAnthropic(m map[string]any) map[string]any {
	id, _ := m["id"].(string)
	model, _ := m["model"].(string)
	var text, finishReason string
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				text, _ = message["content"].(string)
			}
			finishReason, _ = choice["finish_reason"].(string)
		}
	}

	var inputTokens, outputTokens float64
	if usage, ok := m["usage"].(map[string]any); ok {
		inputTokens, _ = usage["prompt_tokens"].(float64)
		outputTokens, _ = usage["completion_tokens"].(float64)
	}

	return map[string]any{
		"id": id, "type": "message", "role": "assistant",
		"content":       []any{map[string]any{"type": "text", "text": text}},
		"model":         model,
		"stop_reason":   openAIFinishReasonToAnthropic(finishReason),
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": int64(inputTokens), "output_tokens": int64(outputTokens)},
	}
}

// openAIFinishReasonToAnthropic 是 internal/adapter.mapStopReason 的反方向映射。
func openAIFinishReasonToAnthropic(r string) string {
	switch r {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default: // "stop" 和其它未识别值
		return "end_turn"
	}
}

// messagesResponseWriter 包装真实的 http.ResponseWriter：200 的成功响应会被
// 翻译成 Anthropic 形状再写出去（非流式整体翻译；流式逐块翻译 + 在
// ChatCompletions 返回后补发收尾事件）；非 200 的错误响应直接透传，不翻译
// （见 Messages 的已知范围限制）。
type messagesResponseWriter struct {
	http.ResponseWriter
	statusCode int
	headerDone bool
	isStream   bool

	msgStarted   bool
	blockStarted bool
	finishReason string
	inputTokens  int64
	outputTokens int64
	reqID        string
	vmName       string

	buf bytes.Buffer // 非流式响应攢在这里，等 finish() 时一次性翻译
}

func (w *messagesResponseWriter) WriteHeader(code int) {
	if w.headerDone {
		return
	}
	w.headerDone = true
	w.statusCode = code
	if code == http.StatusOK {
		w.isStream = strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *messagesResponseWriter) Write(p []byte) (int, error) {
	if !w.headerDone {
		w.WriteHeader(http.StatusOK)
	}
	if w.statusCode != http.StatusOK {
		return w.ResponseWriter.Write(p)
	}
	if w.isStream {
		return w.writeStreamChunk(p)
	}
	return w.buf.Write(p)
}

func (w *messagesResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish 在被包装的 ChatCompletions 处理完、返回之后调用：非流式响应到这里才
// 真正翻译并写出去；流式响应补发收尾事件——OpenAI 的流本身没有一个"结束"的
// 显式信号会经过 Write()（handleStream 的循环遇到 io.EOF 直接 break），只能靠
// "被包装的 handler 返回了"这件事本身来判断流已经正常结束。
func (w *messagesResponseWriter) finish() {
	if w.statusCode != http.StatusOK {
		return
	}
	if w.isStream {
		if w.msgStarted {
			w.emitStreamClose()
		}
		return
	}
	var m map[string]any
	if err := json.Unmarshal(w.buf.Bytes(), &m); err != nil {
		// 翻译不了：把原始 OpenAI 形状原样吐出去，好过什么都不返回。
		_, _ = w.ResponseWriter.Write(w.buf.Bytes())
		return
	}
	out, _ := json.Marshal(openAIResponseToAnthropic(m))
	_, _ = w.ResponseWriter.Write(out)
}

func (w *messagesResponseWriter) writeStreamChunk(p []byte) (int, error) {
	s := strings.TrimSuffix(strings.TrimPrefix(string(p), "data: "), "\n\n")
	if s == "" || s == "[DONE]" {
		return len(p), nil
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(s), &chunk); err != nil {
		return len(p), nil
	}

	if !w.msgStarted {
		w.msgStarted = true
		w.emitMessageStart()
	}

	w.captureUsage(chunk)
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return len(p), nil
	}
	choice, _ := choices[0].(map[string]any)
	if delta, ok := choice["delta"].(map[string]any); ok {
		if content, ok := delta["content"].(string); ok && content != "" {
			if !w.blockStarted {
				w.blockStarted = true
				w.emitContentBlockStart()
			}
			w.emitContentBlockDelta(content)
		}
	}
	if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
		w.finishReason = fr
	}
	return len(p), nil
}

func (w *messagesResponseWriter) captureUsage(chunk map[string]any) {
	usage, ok := chunk["usage"].(map[string]any)
	if !ok {
		return
	}
	if v, ok := usage["prompt_tokens"].(float64); ok {
		w.inputTokens = int64(v)
	}
	if v, ok := usage["completion_tokens"].(float64); ok {
		w.outputTokens = int64(v)
	}
}

func (w *messagesResponseWriter) writeEvent(eventType string, data map[string]any) {
	out, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w.ResponseWriter, "event: %s\ndata: %s\n\n", eventType, out)
	w.Flush()
}

func (w *messagesResponseWriter) emitMessageStart() {
	w.writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": w.reqID, "type": "message", "role": "assistant", "content": []any{}, "model": w.vmName,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (w *messagesResponseWriter) emitContentBlockStart() {
	w.writeEvent("content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
}

func (w *messagesResponseWriter) emitContentBlockDelta(text string) {
	w.writeEvent("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (w *messagesResponseWriter) emitStreamClose() {
	if w.blockStarted {
		w.writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	}
	w.writeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": openAIFinishReasonToAnthropic(w.finishReason)},
		"usage": map[string]any{"output_tokens": w.outputTokens},
	})
	w.writeEvent("message_stop", map[string]any{"type": "message_stop"})
}
