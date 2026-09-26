package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// GeminiAdapter 把网关内部统一的 OpenAI 兼容请求/响应转换成 Google Gemini
// generateContent / streamGenerateContent API 的形态。
//
// 已知范围限制（明确未实现，不是遗漏）：
//   - 不支持 tool/function calling：请求体里出现 "tools" 字段，或消息里出现
//     role="tool"，BuildRequest 直接返回错误，而不是做一个可能错误的静默转换
//     （和 AnthropicAdapter 的取舍一致）。
//   - 多模态内容只做直通：content 是纯字符串时能正确转换成 Gemini 的
//     {"parts":[{"text":...}]}；OpenAI 的 {"type":"image_url",...} 等复合内容部分
//     不会被翻译成 Gemini 的 inlineData/fileData 形态。
//   - 不上报 cache 写入量：Gemini 的显式上下文缓存（cachedContent）创建走单独的
//     API，不在每次请求的 usageMetadata 里报告"这次创建了多少缓存"，所以
//     CacheWriteTokens 恒为 0（cachedContentTokenCount 只表示"读了多少缓存"，
//     映射到 CacheReadTokens）。
type GeminiAdapter struct{}

func (a *GeminiAdapter) Protocol() string { return "gemini" }

func geminiContentPart(role string, content any) map[string]any {
	text, _ := content.(string)
	return map[string]any{"role": role, "parts": []any{map[string]any{"text": text}}}
}

func (a *GeminiAdapter) BuildRequest(ctx context.Context, target Target, endpoint string, body map[string]any) (*http.Request, error) {
	if _, hasTools := body["tools"]; hasTools {
		return nil, errors.New("adapter/gemini: tool/function calling is not supported yet")
	}

	rawMessages, _ := body["messages"].([]any)
	var systemParts []string
	contents := make([]any, 0, len(rawMessages))
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "system":
			if s, ok := msg["content"].(string); ok && s != "" {
				systemParts = append(systemParts, s)
			}
		case "tool":
			return nil, errors.New("adapter/gemini: tool-result messages are not supported yet")
		case "assistant":
			contents = append(contents, geminiContentPart("model", msg["content"]))
		default: // "user" 及其它未识别 role 一律当 user 处理
			contents = append(contents, geminiContentPart("user", msg["content"]))
		}
	}

	payload := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		payload["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": strings.Join(systemParts, "\n\n")}},
		}
	}

	genConfig := map[string]any{}
	if v, ok := body["temperature"]; ok {
		genConfig["temperature"] = v
	}
	if v, ok := body["top_p"]; ok {
		genConfig["topP"] = v
	}
	if v, ok := body["max_tokens"]; ok {
		genConfig["maxOutputTokens"] = v
	}
	switch stop := body["stop"].(type) {
	case string:
		genConfig["stopSequences"] = []string{stop}
	case []any:
		genConfig["stopSequences"] = stop
	}
	if len(genConfig) > 0 {
		payload["generationConfig"] = genConfig
	}

	for k, v := range target.Channel.ParamOverrides {
		if v == nil {
			delete(payload, k)
		} else {
			payload[k] = v
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("adapter/gemini: marshal request: %w", err)
	}

	stream, _ := body["stream"].(bool)
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	url := strings.TrimRight(target.Account.BaseURL, "/") + "/models/" + target.Channel.UpstreamModel + ":" + action
	if stream {
		url += "?alt=sse"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("adapter/gemini: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", target.Key.Secret) // 用请求头而不是 ?key= 查询参数，避免 Key 出现在访问日志/URL 里
	return req, nil
}

// mapGeminiFinishReason 把 Gemini 的 finishReason 映射成 OpenAI 的 finish_reason。
// 空字符串（流式中间 chunk 还没结束）由调用方自己判断是否要调用这个函数，这里
// 只处理"确实收到了 finishReason"的情况。
func mapGeminiFinishReason(r string) string {
	switch r {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION":
		return "content_filter"
	default: // STOP / OTHER / 未识别值
		return "stop"
	}
}

type geminiUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
}

func (u geminiUsageMetadata) toSchema() schema.Usage {
	input := u.PromptTokenCount - u.CachedContentTokenCount
	if input < 0 {
		input = 0
	}
	return schema.Usage{
		InputTokens:     input,
		CacheReadTokens: u.CachedContentTokenCount,
		OutputTokens:    u.CandidatesTokenCount,
		Source:          schema.UsageSourceUpstream,
	}
}

func (a *GeminiAdapter) DecodeResponse(body []byte, vmName, requestID string) (map[string]any, schema.Usage, error) {
	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, schema.Usage{}, fmt.Errorf("adapter/gemini: decode response: %w", err)
	}

	var text strings.Builder
	finishReason := "stop"
	if len(resp.Candidates) > 0 {
		c := resp.Candidates[0]
		for _, p := range c.Content.Parts {
			text.WriteString(p.Text)
		}
		if c.FinishReason != "" {
			finishReason = mapGeminiFinishReason(c.FinishReason)
		}
	} else if resp.PromptFeedback.BlockReason != "" {
		finishReason = "content_filter" // 整个 prompt 被安全策略拦截，压根没有 candidates
	}

	usage := resp.UsageMetadata.toSchema()
	m := map[string]any{
		"id":     requestID,
		"object": "chat.completion",
		"model":  vmName,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": text.String()},
				"finish_reason": finishReason,
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     usage.InputTokens,
			"completion_tokens": usage.OutputTokens,
			"total_tokens":      usage.InputTokens + usage.OutputTokens,
		},
	}
	return m, usage, nil
}

func (a *GeminiAdapter) NewStreamDecoder(body io.ReadCloser, vmName, requestID string) StreamDecoder {
	return &geminiStreamDecoder{
		body:   body,
		reader: bufio.NewReaderSize(body, 64*1024),
		vmName: vmName,
		reqID:  requestID,
	}
}

// ClassifyError 优先按 Gemini 错误体里显式的 error.status 字段分类，识别不了时
// 退化到和其它适配器一致的状态码兜底。
func (a *GeminiAdapter) ClassifyError(statusCode int, body []byte) ErrorClass {
	var envelope struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)

	switch envelope.Error.Status {
	case "RESOURCE_EXHAUSTED":
		return ErrClassRateLimited
	case "UNAUTHENTICATED", "PERMISSION_DENIED":
		return ErrClassKeyInvalid
	case "INVALID_ARGUMENT", "NOT_FOUND", "FAILED_PRECONDITION":
		return ErrClassBadRequest
	case "INTERNAL", "UNAVAILABLE":
		return ErrClassUpstreamUnavailable
	}

	switch {
	case statusCode == http.StatusTooManyRequests:
		return ErrClassRateLimited
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrClassKeyInvalid
	case statusCode >= 400 && statusCode < 500:
		return ErrClassBadRequest
	case statusCode >= 500:
		return ErrClassUpstreamUnavailable
	default:
		return ErrClassUnknown
	}
}

// geminiStreamDecoder 解析 streamGenerateContent(alt=sse) 返回的 "data: {...}\n\n"
// 流。和 Anthropic 不同，Gemini 没有具名事件、也没有显式的结束哨兵（[DONE] 或
// message_stop）——流结束就是连接关闭（读到 io.EOF）。
type geminiStreamDecoder struct {
	body   io.ReadCloser
	reader *bufio.Reader
	vmName string
	reqID  string
	usage  schema.Usage
}

func (d *geminiStreamDecoder) Next() ([]byte, error) {
	for {
		line, readErr := d.reader.ReadString('\n')

		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			if payload, ok := cutSSEDataPrefix(trimmed); ok {
				if out, ok := d.decodeAndTranslate(payload); ok {
					return out, nil
				}
				// 畸形 JSON 或者既无内容增量也无结束原因的 chunk：跳过，继续读下一行。
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("adapter/gemini: read stream: %w", readErr)
		}
	}
}

func (d *geminiStreamDecoder) decodeAndTranslate(payload string) ([]byte, bool) {
	var chunk struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return nil, false
	}

	if chunk.UsageMetadata.PromptTokenCount > 0 || chunk.UsageMetadata.CandidatesTokenCount > 0 {
		d.usage = chunk.UsageMetadata.toSchema()
	}

	var text strings.Builder
	var finishReason string
	if len(chunk.Candidates) > 0 {
		c := chunk.Candidates[0]
		for _, p := range c.Content.Parts {
			text.WriteString(p.Text)
		}
		if c.FinishReason != "" {
			finishReason = mapGeminiFinishReason(c.FinishReason)
		}
	}
	if text.Len() == 0 && finishReason == "" {
		return nil, false // 纯 usageMetadata 收尾包或空 chunk：没有客户端需要看到的增量
	}

	delta := map[string]any{}
	if text.Len() > 0 {
		delta["content"] = text.String()
	}
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": nil}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}
	out, _ := json.Marshal(map[string]any{
		"id": d.reqID, "object": "chat.completion.chunk", "model": d.vmName,
		"choices": []any{choice},
	})
	return append(append([]byte("data: "), out...), []byte("\n\n")...), true
}

func (d *geminiStreamDecoder) Usage() schema.Usage { return d.usage }

func (d *geminiStreamDecoder) Close() error { return d.body.Close() }
