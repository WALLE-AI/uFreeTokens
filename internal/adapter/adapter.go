// Package adapter 把网关内部统一表示转换成各上游协议的请求/响应（技术方案 §7.4）。
// 已实现三种协议：openai（直通，覆盖绝大多数国内外 OpenAI 兼容上游：DeepSeek、
// SiliconFlow、火山方舟、阿里百炼、OpenRouter 等，差异通过 channel.param_overrides
// 配置消化，不为每家写专门代码）、anthropic 和 gemini（都是协议翻译，各自的已知
// 范围限制见 anthropic.go / gemini.go 的包级注释：都不支持 tool/function calling、
// 多模态内容只做直通）。
package adapter

import (
	"context"
	"io"
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

// Target 是一次上游调用的具体目标：选中的渠道 + 上游账号 + 已解密的 Key。
type Target struct {
	Channel *catalog.Channel
	Account *catalog.ProviderAccount
	Key     *catalog.ProviderKey
}

// ErrorClass 决定 relay 层的处理动作（换 Key / 换渠道 / 直接返回用户），
// 对应技术方案 §7.6 的"上游错误 → 动作映射"表。
type ErrorClass string

const (
	ErrClassRateLimited         ErrorClass = "rate_limited"         // 429 -> 冷却该 Key，换 Key 重试
	ErrClassKeyExhausted        ErrorClass = "key_exhausted"        // 余额不足/配额耗尽 -> 标记 Key 失效
	ErrClassKeyInvalid          ErrorClass = "key_invalid"          // 401/403 -> 标记 Key 失效
	ErrClassUpstreamUnavailable ErrorClass = "upstream_unavailable" // 5xx/连接错误 -> 换渠道
	ErrClassBadRequest          ErrorClass = "bad_request"          // 400 等 -> 不重试，直接返回用户
	ErrClassContentFiltered     ErrorClass = "content_filtered"     // 内容安全拦截 -> 不重试
	ErrClassUnknown             ErrorClass = "unknown"
)

// Retryable 判断该错误类别是否值得换 Key/换渠道重试（技术方案 §7.7：
// 只在尚未向客户端写出任何字节前重试）。
func (c ErrorClass) Retryable() bool {
	switch c {
	case ErrClassRateLimited, ErrClassKeyExhausted, ErrClassKeyInvalid, ErrClassUpstreamUnavailable:
		return true
	default:
		return false
	}
}

// 逻辑端点（拼在 provider_accounts.base_url 后面的上游路径），与 relay 层的
// endpointSpec 一一对应。
const (
	EndpointChat           = "/chat/completions"
	EndpointEmbeddings     = "/embeddings"
	EndpointRerank         = "/rerank"
	EndpointImages         = "/images/generations"
	EndpointSpeech         = "/audio/speech"
	EndpointTranscriptions = "/audio/transcriptions"
)

// EndpointSupporter 是可选接口：声明适配器能处理哪些逻辑端点。没实现它的适配器
// （anthropic、gemini）只支持 EndpointChat——它们的 BuildRequest 会忽略 endpoint
// 参数、始终打到自己的对话端点，路由时必须把这类渠道排除在非对话端点之外，
// 否则请求会被发到错误的上游路径。
type EndpointSupporter interface {
	SupportsEndpoint(endpoint string) bool
}

// SupportsEndpoint 判断适配器 a 能否处理 endpoint。
func SupportsEndpoint(a Adapter, endpoint string) bool {
	if es, ok := a.(EndpointSupporter); ok {
		return es.SupportsEndpoint(endpoint)
	}
	return endpoint == EndpointChat
}

// RawRequestBuilder 是可选接口：请求体不是 JSON 时（语音识别的 multipart），
// 由 relay 组装好 body 与 Content-Type，适配器只负责拼 URL 和鉴权头。
type RawRequestBuilder interface {
	BuildRawRequest(ctx context.Context, target Target, endpoint, contentType string, body io.Reader) (*http.Request, error)
}
