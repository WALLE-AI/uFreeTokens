package relay

import "sync"

// RetryBudget 实现技术方案 §7.7 的"全局重试预算"：每实例每秒重试数 ≤ 正常
// 请求数的 20%，防止上游整体故障时，重试把请求量放大到把故障拖垮的渠道/整个
// 上游打得更死。
//
// 用令牌桶而不是严格的"每秒计数窗口"来近似这个比例：每来一个正常请求（无论
// 最终是否需要重试）攒 tokenRatio 个令牌，每次重试消耗 1 个令牌，令牌数封顶在
// maxTokens。稳态下（持续有重试发生）这就收敛到"重试数 = 请求数 × tokenRatio"，
// 但不像固定秒窗口那样在窗口边界有令人惊讶的突发/清零行为，也不需要后台协程
// 做整点重置。桶从满值开始，允许冷启动时的一小段突发重试（比如实例刚起来、
// 还没积累"正常请求"历史时，不应该直接把重试全部堵死）。
type RetryBudget struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	tokenRatio float64 // 每个正常请求增加的令牌数；默认 0.2 对应"重试 ≤ 请求数的 20%"
}

// NewRetryBudget 创建一个重试预算。maxTokens<=0 或 tokenRatio<=0 时返回 nil，
// 调用方应把 nil 当作"不限制重试"处理（Service.RetryBudget 为 nil 时同样如此）。
func NewRetryBudget(maxTokens float64, tokenRatio float64) *RetryBudget {
	if maxTokens <= 0 || tokenRatio <= 0 {
		return nil
	}
	return &RetryBudget{tokens: maxTokens, maxTokens: maxTokens, tokenRatio: tokenRatio}
}

// RecordRequest 应该在每个进入网关的"正常请求"（不区分首次尝试还是重试）开始
// 处理时调用一次，给预算记一次账。对 nil 接收者安全（no-op）。
func (b *RetryBudget) RecordRequest() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens += b.tokenRatio
	if b.tokens > b.maxTokens {
		b.tokens = b.maxTokens
	}
}

// TryRetry 在每次真正发起一次重试（即第二次及以后的上游尝试）之前调用；
// 返回 false 表示预算已耗尽，调用方应放弃重试、直接把最后一次错误返回给客户端。
// 对 nil 接收者安全，永远返回 true（不限制）。
func (b *RetryBudget) TryRetry() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// DefaultRetryBudget 是技术方案 §7.7 默认参数的实现：maxTokens=10（允许一次
// 冷启动突发），tokenRatio=0.2（稳态下重试 ≤ 请求数的 20%）。
func DefaultRetryBudget() *RetryBudget {
	return NewRetryBudget(10, 0.2)
}
