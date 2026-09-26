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

// anthropicDefaultMaxTokens 是 max_tokens 缺省时的兜底值。Anthropic Messages API
// 要求 max_tokens 必填，但网关对外的 OpenAI 兼容协议里这个字段是可选的——BuildRequest
// 拿不到虚拟模型的 MaxOutput（Target 里没有这个信息，只有 Channel/Account/Key），
// 真正按虚拟模型配置裁剪上限的活儿由 relay 层的 reserveOutputTokens 做了，但那个值
// 只用于预扣估算，没有写回请求体。这里用一个保守的通用默认值，不是最优但足够安全。
const anthropicDefaultMaxTokens = 4096

// anthropicAPIVersion 是 Anthropic Messages API 要求的 anthropic-version 请求头。
const anthropicAPIVersion = "2023-06-01"

// AnthropicAdapter 把网关内部统一的 OpenAI 兼容请求/响应转换成 Anthropic Messages
// API（POST /v1/messages）的形态。
//
// 已知范围限制（明确未实现，不是遗漏）：
//   - 不支持 tool/function calling：请求体里出现 "tools" 字段，或消息里出现
//     role="tool"（工具调用结果），BuildRequest 直接返回错误，而不是做一个可能
//     错误的静默转换。
//   - 多模态内容（图片等）只做直通：content 是字符串或 {"type":"text","text":...}
//     数组时能正确转换，OpenAI 的 {"type":"image_url",...} 部分不会被翻译成
//     Anthropic 的 {"type":"image","source":{...}} 形态，直通后大概率被上游拒绝。
//   - "n"（多选一补全）等 Anthropic 不支持的字段会被静默丢弃。
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) Protocol() string { return "anthropic" }

func (a *AnthropicAdapter) BuildRequest(ctx context.Context, target Target, endpoint string, body map[string]any) (*http.Request, error) {
	if _, hasTools := body["tools"]; hasTools {
		return nil, errors.New("adapter/anthropic: tool/function calling is not supported yet")
	}

	rawMessages, _ := body["messages"].([]any)
	var system []string
	outMessages := make([]any, 0, len(rawMessages))
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "system":
			if s, ok := msg["content"].(string); ok && s != "" {
				system = append(system, s)
			}
		case "tool":
			return nil, errors.New("adapter/anthropic: tool-result messages are not supported yet")
		default:
			outMessages = append(outMessages, map[string]any{"role": role, "content": msg["content"]})
		}
	}

	payload := map[string]any{
		"model":      target.Channel.UpstreamModel,
		"messages":   outMessages,
		"max_tokens": anthropicDefaultMaxTokens,
	}
	if len(system) > 0 {
		payload["system"] = strings.Join(system, "\n\n")
	}
	if v, ok := body["max_tokens"]; ok {
		payload["max_tokens"] = v
	}
	if v, ok := body["temperature"]; ok {
		payload["temperature"] = v
	}
	if v, ok := body["top_p"]; ok {
		payload["top_p"] = v
	}
	if v, ok := body["stream"]; ok {
		payload["stream"] = v
	}
	switch stop := body["stop"].(type) {
	case string:
		payload["stop_sequences"] = []string{stop}
	case []any:
		payload["stop_sequences"] = stop
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
		return nil, fmt.Errorf("adapter/anthropic: marshal request: %w", err)
	}

	url := strings.TrimRight(target.Account.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("adapter/anthropic: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", target.Key.Secret)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	return req, nil
}

// anthropicUsage 是 Anthropic 响应/流事件里 usage 对象的形状。和 OpenAI 不同，
// input_tokens 本身就不含缓存部分（不需要像 openai 适配器那样做减法）。
type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func (u anthropicUsage) toSchema() schema.Usage {
	return schema.Usage{
		InputTokens:      u.InputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		OutputTokens:     u.OutputTokens,
		Source:           schema.UsageSourceUpstream,
	}
}

// mapStopReason 把 Anthropic 的 stop_reason 映射成 OpenAI 的 finish_reason。
func mapStopReason(r string) string {
	switch r {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default: // end_turn / stop_sequence / 其它未识别值
		return "stop"
	}
}

func (a *AnthropicAdapter) DecodeResponse(body []byte, vmName, requestID string) (map[string]any, schema.Usage, error) {
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string         `json:"stop_reason"`
		Usage      anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, schema.Usage{}, fmt.Errorf("adapter/anthropic: decode response: %w", err)
	}

	var text strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	usage := resp.Usage.toSchema()

	m := map[string]any{
		"id":     requestID,
		"object": "chat.completion",
		"model":  vmName,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": text.String()},
				"finish_reason": mapStopReason(resp.StopReason),
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

func (a *AnthropicAdapter) NewStreamDecoder(body io.ReadCloser, vmName, requestID string) StreamDecoder {
	return &anthropicStreamDecoder{
		body:   body,
		reader: bufio.NewReaderSize(body, 64*1024),
		vmName: vmName,
		reqID:  requestID,
	}
}

// ClassifyError 优先按 Anthropic 错误体里显式的 error.type 字段分类（比按状态码
// 猜测更准确），识别不了时退化到和 openai 适配器一致的状态码兜底。
func (a *AnthropicAdapter) ClassifyError(statusCode int, body []byte) ErrorClass {
	var envelope struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)

	switch envelope.Error.Type {
	case "rate_limit_error":
		return ErrClassRateLimited
	case "authentication_error", "permission_error":
		return ErrClassKeyInvalid
	case "invalid_request_error", "not_found_error", "request_too_large":
		return ErrClassBadRequest
	case "api_error", "overloaded_error":
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

// anthropicStreamDecoder 解析 Anthropic 的具名 SSE 事件流（message_start /
// content_block_delta / message_delta / message_stop 等），翻译成 OpenAI 风格的
// "chat.completion.chunk" payload。事件类型从每条 data 载荷自带的 "type" 字段判断，
// 不依赖单独的 "event:" 行（有些代理会把它strip 掉，而 data 里的 type 总是存在）。
type anthropicStreamDecoder struct {
	body   io.ReadCloser
	reader *bufio.Reader
	vmName string
	reqID  string
	usage  schema.Usage
}

func (d *anthropicStreamDecoder) Next() ([]byte, error) {
	for {
		// 和 openai 适配器的 sseStreamDecoder 一样：先处理已经读到的内容（哪怕这次
		// ReadString 同时带回了 err），再判断要不要因为 err 结束循环，这样最后一条
		// 不带换行的事件不会被直接丢弃。
		line, readErr := d.reader.ReadString('\n')

		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			if payload, ok := cutSSEDataPrefix(trimmed); ok {
				out, done, err := d.handlePayload(payload)
				if err != nil {
					return nil, err
				}
				if done {
					return nil, io.EOF
				}
				if out != nil {
					return out, nil
				}
			}
			// 非 "data:" 行（event:/id:/注释等 SSE 元数据）：忽略。
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("adapter/anthropic: read stream: %w", readErr)
		}
	}
}

func (d *anthropicStreamDecoder) handlePayload(payload string) (chunk []byte, done bool, err error) {
	var envelope struct {
		Type    string `json:"type"`
		Message struct {
			Usage anthropicUsage `json:"usage"`
		} `json:"message"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage anthropicUsage `json:"usage"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal([]byte(payload), &envelope); jsonErr != nil {
		return nil, false, nil // 畸形数据跳过，呼应 openai 解码器同样的容错策略
	}

	switch envelope.Type {
	case "message_start":
		d.usage = envelope.Message.Usage.toSchema()
		return nil, false, nil
	case "content_block_delta":
		if envelope.Delta.Type != "text_delta" || envelope.Delta.Text == "" {
			return nil, false, nil // input_json_delta（工具调用）/thinking_delta 等暂不转发
		}
		return d.chunk(map[string]any{"content": envelope.Delta.Text}, ""), false, nil
	case "message_delta":
		if envelope.Usage.OutputTokens > 0 {
			d.usage.OutputTokens = envelope.Usage.OutputTokens
			d.usage.Source = schema.UsageSourceUpstream
		}
		return d.chunk(map[string]any{}, mapStopReason(envelope.Delta.StopReason)), false, nil
	case "message_stop":
		return nil, true, nil
	case "error":
		return nil, false, fmt.Errorf("adapter/anthropic: stream error: %s: %s", envelope.Error.Type, envelope.Error.Message)
	default: // ping / content_block_start / content_block_stop 等：不需要转发
		return nil, false, nil
	}
}

func (d *anthropicStreamDecoder) chunk(delta map[string]any, finishReason string) []byte {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": nil}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}
	out, _ := json.Marshal(map[string]any{
		"id": d.reqID, "object": "chat.completion.chunk", "model": d.vmName,
		"choices": []any{choice},
	})
	return append(append([]byte("data: "), out...), []byte("\n\n")...)
}

func (d *anthropicStreamDecoder) Usage() schema.Usage { return d.usage }

func (d *anthropicStreamDecoder) Close() error { return d.body.Close() }
