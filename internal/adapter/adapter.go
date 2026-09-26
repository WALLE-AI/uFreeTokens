// Package adapter 把网关内部统一表示转换成各上游协议的请求/响应（技术方案 §7.4）。
// 已实现 openai（直通，覆盖绝大多数国内外 OpenAI 兼容上游：DeepSeek、SiliconFlow、
// 火山方舟、阿里百炼、OpenRouter 等，差异通过 channel.param_overrides 配置消化，
// 不为每家写专门代码）和 anthropic（协议翻译，见 anthropic.go 的已知范围限制：
// 不支持 tool/function calling、多模态内容只做直通）两种协议。Gemini 留作后续。
package adapter

import (
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
