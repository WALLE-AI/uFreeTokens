// Package schema 定义网关内部统一的请求/响应/用量表示（技术方案 §7.4）。
// 请求体本身不做严格的强类型建模——上游 OpenAI 兼容协议字段众多且各家有差异，
// 网关按 "解析出需要改写的少数字段 + 其余原样透传" 的方式处理（BuildRequest 时
// 只重写 model 字段并叠加 channel.param_overrides），这样新增一个 OpenAI 兼容
// 上游只需要配置，不需要改代码。
package schema

import "github.com/WALLE-AI/uFreeTokens/internal/pricing"

// UsageSource 标记用量数据的来源，决定计费时是否需要在 request_logs 里标注为
// "estimated"（技术方案 §7.9.4）。
type UsageSource string

const (
	UsageSourceUpstream  UsageSource = "upstream"
	UsageSourceEstimated UsageSource = "estimated"
)

// Usage 是从上游响应中提取（或兜底估算）出的用量，字段命名对齐各家适配器的映射目标：
//   - OpenAI: prompt_tokens_details.cached_tokens -> CacheReadTokens
//   - DeepSeek: prompt_cache_hit_tokens/miss_tokens -> CacheReadTokens / InputTokens
//   - Anthropic: cache_read_input_tokens / cache_creation_input_tokens
type Usage struct {
	InputTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	OutputTokens     int64
	ReasoningTokens  int64
	Source           UsageSource
}

// ToPricing 转换成计价层使用的 pricing.Usage（两者字段含义一致，分包是为了不让
// pricing 包依赖 HTTP/协议相关的概念）。
func (u Usage) ToPricing() pricing.Usage {
	return pricing.Usage{
		InputTokens:      u.InputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		OutputTokens:     u.OutputTokens,
		ReasoningTokens:  u.ReasoningTokens,
	}
}

// IsZero 判断是否完全没有提取到任何用量（用于判断上游是否根本没返回 usage 字段）。
func (u Usage) IsZero() bool {
	return u.InputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 &&
		u.OutputTokens == 0 && u.ReasoningTokens == 0
}
