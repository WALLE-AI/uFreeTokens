package relay

import (
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

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
// 提取到）时使用：按预扣时的估算上限计费，而不是按 0 计费——宁可少数情况下
// 对用户略微多算，也不能让平台在计费信息缺失时系统性地少收钱甚至倒贴
// （技术方案 §7.9.4 的兜底原则；基于 tokenizer 的精确估算留作后续）。
func fallbackUsage(estInput, reserveOutput int) schema.Usage {
	return schema.Usage{
		InputTokens:  int64(estInput),
		OutputTokens: int64(reserveOutput),
		Source:       schema.UsageSourceEstimated,
	}
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
