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

// Adapter 把统一请求/响应转换成某个上游协议的形态（技术方案 §7.4）。目前有
// openai（直通，覆盖绝大多数 OpenAI 兼容上游）和 anthropic（协议翻译：请求/
// 响应/流式事件的形状都不一样，见 anthropic.go 的包级注释和已知范围限制）两种
// 实现。Gemini 协议留作后续。
type Adapter interface {
	Protocol() string
	// endpoint 是 relay 层对外的逻辑端点名（目前只有 chatEndpoint 一个取值），
	// 不是字面的上游 URL 路径——OpenAIAdapter 直接把它当路径后缀拼接（两者当前
	// 恰好相等），但协议差异更大的适配器（如 AnthropicAdapter）会忽略这个参数，
	// 自己决定真正的上游路径（比如 /messages）。
	BuildRequest(ctx context.Context, target Target, endpoint string, body map[string]any) (*http.Request, error)
	DecodeResponse(body []byte, vmName, requestID string) (rewritten map[string]any, usage schema.Usage, err error)
	NewStreamDecoder(body io.ReadCloser, vmName, requestID string) StreamDecoder
	ClassifyError(statusCode int, body []byte) ErrorClass
}

// StreamDecoder 逐块产出已改写好、可直接转发给客户端的 SSE payload。
type StreamDecoder interface {
	// Next 返回下一个完整的 "data: {...}\n\n" 字节序列；结束时返回 io.EOF。
	Next() ([]byte, error)
	// Usage 返回目前为止观测到的用量快照（可能来自最后一个带 usage 的 chunk）。
	Usage() schema.Usage
	Close() error
}

// Registry 按 provider 协议名分发到具体 Adapter 实现。
type Registry struct {
	byProtocol map[string]Adapter
}

func NewRegistry() *Registry {
	r := &Registry{byProtocol: map[string]Adapter{}}
	r.Register(&OpenAIAdapter{})
	r.Register(&AnthropicAdapter{})
	r.Register(&GeminiAdapter{})
	return r
}

func (r *Registry) Register(a Adapter) { r.byProtocol[a.Protocol()] = a }

func (r *Registry) For(protocol string) (Adapter, bool) {
	a, ok := r.byProtocol[protocol]
	return a, ok
}

// OpenAIAdapter 覆盖绝大多数 OpenAI 兼容上游（DeepSeek、SiliconFlow、火山方舟、
// 阿里百炼、OpenRouter 等）。各家差异通过 channel.param_overrides 配置消化。
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) Protocol() string { return "openai" }

func (a *OpenAIAdapter) BuildRequest(ctx context.Context, target Target, endpoint string, body map[string]any) (*http.Request, error) {
	payload := make(map[string]any, len(body)+1)
	for k, v := range body {
		payload[k] = v
	}
	payload["model"] = target.Channel.UpstreamModel

	// 流式请求总是向上游要 usage（即便客户端没主动带 stream_options），这样
	// relay 层才能按真实用量计费而不是保守估算兜底（技术方案 §7.9.4）；客户端
	// 自己看不看得到这个 usage-only chunk 由 relay.isUsageOnlyChunk 在转发时
	// 决定，不影响这里的上游请求。渠道如果不支持这个参数，用
	// channel.param_overrides: {"stream_options": null} 剔除（下面的循环会处理）。
	if stream, _ := payload["stream"].(bool); stream {
		merged := map[string]any{"include_usage": true}
		if existing, ok := payload["stream_options"].(map[string]any); ok {
			for k, v := range existing {
				merged[k] = v
			}
			merged["include_usage"] = true
		}
		payload["stream_options"] = merged
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
		return nil, fmt.Errorf("adapter/openai: marshal request: %w", err)
	}

	url := strings.TrimRight(target.Account.BaseURL, "/") + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("adapter/openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.Key.Secret)
	return req, nil
}

func (a *OpenAIAdapter) DecodeResponse(body []byte, vmName, requestID string) (map[string]any, schema.Usage, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, schema.Usage{}, fmt.Errorf("adapter/openai: decode response: %w", err)
	}
	usage := extractUsage(m)
	m["model"] = vmName
	m["id"] = requestID
	return m, usage, nil
}

func (a *OpenAIAdapter) NewStreamDecoder(body io.ReadCloser, vmName, requestID string) StreamDecoder {
	return &sseStreamDecoder{
		body:   body,
		reader: bufio.NewReaderSize(body, 64*1024), // 单行可能较长（如大段 tool_calls），加大缓冲避免截断
		vmName: vmName,
		reqID:  requestID,
	}
}

// ClassifyError 按 HTTP 状态码把上游错误映射到内部错误类别，用于决定重试策略
// （技术方案 §7.6）。
func (a *OpenAIAdapter) ClassifyError(statusCode int, body []byte) ErrorClass {
	if bytes.Contains(body, []byte("content_filter")) || bytes.Contains(body, []byte("content_policy")) {
		return ErrClassContentFiltered
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return ErrClassRateLimited
	case statusCode == http.StatusPaymentRequired:
		return ErrClassKeyExhausted
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

// extractUsage 兼容 OpenAI 原生格式（prompt_tokens_details.cached_tokens、
// completion_tokens_details.reasoning_tokens）与 DeepSeek 的
// prompt_cache_hit_tokens/prompt_cache_miss_tokens 两种常见变体。
func extractUsage(m map[string]any) schema.Usage {
	raw, ok := m["usage"].(map[string]any)
	if !ok {
		return schema.Usage{}
	}

	prompt := getFloat(raw, "prompt_tokens")
	completion := getFloat(raw, "completion_tokens")

	var cacheRead, cacheWrite float64
	if details, ok := raw["prompt_tokens_details"].(map[string]any); ok {
		cacheRead = getFloat(details, "cached_tokens")
	}
	if hit, ok2 := raw["prompt_cache_hit_tokens"]; ok2 {
		cacheRead = toFloat(hit)
	}

	var reasoning float64
	if details, ok := raw["completion_tokens_details"].(map[string]any); ok {
		reasoning = getFloat(details, "reasoning_tokens")
	}

	input := prompt - cacheRead
	if input < 0 {
		input = 0
	}

	return schema.Usage{
		InputTokens:      int64(input),
		CacheReadTokens:  int64(cacheRead),
		CacheWriteTokens: int64(cacheWrite),
		OutputTokens:     int64(completion),
		ReasoningTokens:  int64(reasoning),
		Source:           schema.UsageSourceUpstream,
	}
}

func getFloat(m map[string]any, key string) float64 {
	return toFloat(m[key])
}

func toFloat(v any) float64 {
	f, _ := v.(float64) // JSON 数字统一解码为 float64
	return f
}

// sseStreamDecoder 解析 "data: {...}\n\n" 格式的 SSE 流，逐条改写 model/id 字段
// 后原样转发；[DONE] 标志映射为 io.EOF。
type sseStreamDecoder struct {
	body   io.ReadCloser
	reader *bufio.Reader
	vmName string
	reqID  string
	usage  schema.Usage
}

func (d *sseStreamDecoder) Next() ([]byte, error) {
	for {
		// 先处理已经读到的内容（哪怕这次 ReadString 同时带回了 err，比如流在最后一行
		// 没有换行符就结束了），再判断要不要因为 err 结束循环——这样最后一条不带换行的
		// 事件不会被直接丢弃。
		line, readErr := d.reader.ReadString('\n')

		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			if payload, ok := cutSSEDataPrefix(trimmed); ok {
				if payload == "[DONE]" {
					return nil, io.EOF
				}
				if out, ok := d.decodeAndRewrite(payload); ok {
					return out, nil
				}
				// 非法 JSON（个别厂商会在流里夹杂非标准行）：忽略，继续读下一行。
			}
			// 非 "data:" 行（event:/id:/注释等 SSE 元数据）：忽略。
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("adapter/openai: read stream: %w", readErr)
		}
	}
}

func cutSSEDataPrefix(line string) (string, bool) {
	if payload, ok := strings.CutPrefix(line, "data: "); ok {
		return payload, true
	}
	if payload, ok := strings.CutPrefix(line, "data:"); ok {
		return strings.TrimSpace(payload), true
	}
	return "", false
}

// decodeAndRewrite 解析一条 chunk JSON，累计用量并改写 model/id，返回可直接转发的
// SSE payload 字节。ok=false 表示 JSON 解析失败，调用方应跳过该行。
func (d *sseStreamDecoder) decodeAndRewrite(payload string) ([]byte, bool) {
	var chunk map[string]any
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return nil, false
	}

	if u := extractUsage(chunk); !u.IsZero() {
		d.usage = u
	}
	chunk["model"] = d.vmName
	chunk["id"] = d.reqID

	out, err := json.Marshal(chunk)
	if err != nil {
		return nil, false
	}
	return append(append([]byte("data: "), out...), []byte("\n\n")...), true
}

func (d *sseStreamDecoder) Usage() schema.Usage { return d.usage }

func (d *sseStreamDecoder) Close() error { return d.body.Close() }
