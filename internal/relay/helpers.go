package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// intOrZero 把 *int 形式的限流配置（nil = "继承账户"，尚未实现账户级默认值，
// 见技术方案 §6.2）解成 0（= 不限制），避免到处写 nil 判断。
func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// writeRateLimited 统一处理限流拒绝：带上 Retry-After 头（有明确等待时长时），
// 返回 429（技术方案附录 A：rate_limit_exceeded / concurrency_limit_exceeded）。
func writeRateLimited(w http.ResponseWriter, r *http.Request, res ratelimit.Result, code, message string) {
	if res.RetryAfter > 0 {
		secs := int(res.RetryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	httpx.WriteError(w, r, http.StatusTooManyRequests, code, message)
}

// estimateTokens 是请求体大小到 token 数的粗略估算（约 4 字节/token 的经验值），
// 只用于路由的上下文窗口过滤和预扣费用的估算上限——不是最终计费依据。
// 真正的计费以上游返回的 usage 为准（技术方案 §7.9.4）。
func estimateTokens(bodyBytes int) int {
	t := bodyBytes/4 + 1
	if t < 1 {
		t = 1
	}
	return t
}

// reserveOutputTokens 决定预扣费用时按多少输出 token 计算上限：取请求里声明的
// max_tokens/max_completion_tokens、虚拟模型的 max_output、以及运营配置的
// reserve_output_cap 三者中最小值（技术方案 §7.9.1：cap 避免低余额用户在
// 支持超长输出的模型上直接被拒绝所有请求）。
func reserveOutputTokens(reqMap map[string]any, vmMaxOutput, cap int) int {
	limit := vmMaxOutput
	if v, ok := numberField(reqMap, "max_tokens"); ok && int(v) < limit {
		limit = int(v)
	}
	if v, ok := numberField(reqMap, "max_completion_tokens"); ok && int(v) < limit {
		limit = int(v)
	}
	if cap > 0 && cap < limit {
		limit = cap
	}
	if limit <= 0 {
		limit = 1
	}
	return limit
}

func numberField(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

func hasKey(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	arr, ok := v.([]any)
	return ok && len(arr) > 0
}

func hasResponseFormatJSONSchema(m map[string]any) bool {
	rf, ok := m["response_format"].(map[string]any)
	if !ok {
		return false
	}
	t, _ := rf["type"].(string)
	return t == "json_schema" || t == "json_object"
}

func modelAllowed(allowed []string, model string) bool {
	if allowed == nil {
		return true
	}
	for _, m := range allowed {
		if m == model {
			return true
		}
	}
	return false
}

func tierCanSee(visibleTiers []string, tier string) bool {
	for _, t := range visibleTiers {
		if t == tier {
			return true
		}
	}
	return false
}

// fallbackUsage 在上游完全没有返回 usage（且流式场景下也没能从任何 chunk 里
// 提取到）时使用，宁可略微多算也不能系统性地少收钱甚至倒贴（技术方案 §7.9.4
// 的兜底原则；基于 tokenizer 的精确估算留作后续）。output 参数由调用方决定：
// 非流式路径、以及流式但完全没转发出任何内容的场景，用预扣时的估算上限
// reserveOutput；流式场景下客户端已经收到了部分内容后断开，用
// estimateTokens(转发字节数) 而不是 reserveOutput——那是"最多可能用掉多少"，
// 而不是"实际输出了多少"，见 handleStream。
func fallbackUsage(estInput, output int) schema.Usage {
	return schema.Usage{
		InputTokens:  int64(estInput),
		OutputTokens: int64(output),
		Source:       schema.UsageSourceEstimated,
	}
}

// clientRequestedStreamUsage 判断客户端自己的请求体里是不是主动带了
// stream_options.include_usage=true。relay 会无条件让上游带上这个参数
// （adapter/openai.go 的 BuildRequest）以便准确计费，但客户端看不看得到那个
// usage-only chunk 应该完全取决于客户端自己有没有要——这个函数就是那个判断
// 依据，供 handleStream 决定要不要转发（isUsageOnlyChunk）。
func clientRequestedStreamUsage(reqMap map[string]any) bool {
	so, ok := reqMap["stream_options"].(map[string]any)
	if !ok {
		return false
	}
	v, _ := so["include_usage"].(bool)
	return v
}

// isUsageOnlyChunk 判断一个 StreamDecoder.Next() 返回的 SSE chunk
// （"data: {...}\n\n" 形状）是不是只携带 usage、不携带任何内容增量——也就是
// BuildRequest 为了计费注入 include_usage 换来的那个额外 chunk（OpenAI 兼容
// 上游的约定是这类 chunk 的 choices 为空数组）。解析失败时保守地返回 false，
// 原样转发，绝不能因为解析失败误吞掉真实内容。
func isUsageOnlyChunk(chunk []byte) bool {
	payload := bytes.TrimSpace(chunk)
	payload = bytes.TrimSpace(bytes.TrimPrefix(payload, []byte("data:")))
	if len(payload) == 0 || payload[0] != '{' {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return false
	}
	usage, hasUsage := m["usage"]
	if !hasUsage || usage == nil {
		return false
	}
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		return false
	}
	return true
}

// computeCostAmount 用渠道的成本价（costBook，可能是零值——没配置成本价）算出
// 这次请求的平台成本（微元，CNY），乘上渠道的合同折扣系数（costMultiplier，
// 技术方案 §6.4：渠道成本 = 挂牌价 × cost_multiplier）。非 CNY 计价的成本价会先
// 按 fxRates（来自 catalog.Snapshot.FXRates，技术方案 §7.16.9）折算成 CNY。
// 返回 nil 表示"这次算不出成本"（没配成本价，或成本价币种没有对应的汇率数据），
// 而不是返回一个具有欺骗性的 0：0 成本会让毛利报表显得"每一分钱都是利润"，
// 比"缺这条数据"更容易误导人。
func computeCostAmount(costBook pricing.Book, costMultiplier decimal.Decimal, usage schema.Usage, fxRates map[string]decimal.Decimal) *int64 {
	if len(costBook.Components) == 0 {
		return nil
	}
	fxRate := decimal.NewFromInt(1)
	if costBook.Currency != "" && costBook.Currency != "CNY" {
		rate, ok := fxRates[costBook.Currency]
		if !ok {
			return nil
		}
		fxRate = rate
	}
	amount, matched := pricing.Charge(costBook, usage.ToPricing(), "default", time.Now(), pricing.RoundCeil)
	if !matched {
		return nil
	}
	// costMultiplier 不做"零值当作 1"的兜底：provider_accounts.cost_multiplier
	// 在数据库里 NOT NULL DEFAULT 1，catalog 加载的永远是管理员实际配置的值——
	// 如果那个值就是 0（比如上游整月免费的渠道），这里就应该算出成本为 0，
	// 而不是悄悄当成"没配置"改回 1 倍，那样会让一个真实的零成本渠道在毛利
	// 报表上显得像是照单全价支付。
	adjusted := decimal.NewFromInt(amount).Mul(fxRate).Mul(costMultiplier).Ceil().IntPart()
	return &adjusted
}

// clientFacingError 把上游错误类别映射为返回给客户端的状态码/错误码。
// 技术方案 §7.6 里 Key 级别的问题（限流、余额耗尽、Key 失效）本应触发换 Key/换渠道
// 重试，但本阶段是单次尝试，这些都会直接暴露成 502——语义上这是"我们这次没能完成
// 你的请求"，而不是"你的请求有问题"，所以统一映射到 502 而不是 429（429 会让用户
// 误以为是自己的限流触发了）。
func clientFacingError(class adapter.ErrorClass) (status int, code string) {
	switch class {
	case adapter.ErrClassBadRequest:
		return http.StatusBadRequest, "invalid_request"
	case adapter.ErrClassContentFiltered:
		return http.StatusBadRequest, "content_filtered"
	default:
		return http.StatusBadGateway, "upstream_error"
	}
}
