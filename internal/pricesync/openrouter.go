package pricesync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// orMeters 把 OpenRouter `pricing` 对象里的字段名映射到本平台的计量项命名
// （技术方案 §7.16.5）。OpenRouter 按次计费的字段（如 web_search）没有对应的
// pricing.Meter 常量——本平台的价格表 schema 目前只有 6 个 meter（见
// internal/pricing.Meter），image/web_search 这类附加计量项暂时丢弃，不归一化
// 成一个假冒的 meter；一旦 pricing.Meter 扩展了对应枚举，这里可以直接加一行。
var orMeters = map[string]pricing.Meter{
	"prompt":             pricing.MeterInput,
	"completion":         pricing.MeterOutput,
	"input_cache_read":   pricing.MeterInputCacheRead,
	"input_cache_write":  pricing.MeterInputCacheWrite,
	"internal_reasoning": pricing.MeterOutputReasoning,
}

// OpenRouterFetcher.Name 标识这个来源插件；Fetch 尚未实现真正的 HTTP 抓取
// （见包级注释的范围限制），只暴露 normalizeOpenRouter 这个纯函数供以后接线。
type OpenRouterFetcher struct{}

func (OpenRouterFetcher) Name() string { return "openrouter_models" }

func (OpenRouterFetcher) Fetch(ctx context.Context, src Source) ([]Observation, error) {
	return nil, fmt.Errorf("adapter/openrouter: live fetch not implemented, see internal/pricesync package doc")
}

// openRouterModel 是 GET /api/v1/models 响应里我们关心的字段子集。
type openRouterModel struct {
	ID      string                     `json:"id"`
	Pricing map[string]json.RawMessage `json:"pricing"`
}

type openRouterModelsResponse struct {
	Data []openRouterModel `json:"data"`
}

// normalizeOpenRouterResponse 解析整个 GET /api/v1/models 响应体，对每个模型
// 调用 normalizeOpenRouter。单个模型解析失败不影响其它模型（用 skipped 收集
// 失败的模型 ID + 原因，调用方决定是否告警，而不是让一个模型的脏数据拖垮整批）。
func normalizeOpenRouterResponse(body []byte) (observations []Observation, skipped map[string]error, err error) {
	var resp openRouterModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("pricesync/openrouter: decode response: %w", err)
	}
	skipped = map[string]error{}
	for _, m := range resp.Data {
		components, err := normalizeOpenRouter(m.Pricing)
		if err != nil {
			skipped[m.ID] = err
			continue
		}
		if len(components) == 0 {
			continue // 这个模型没有任何我们认识的计量项，不产生一条空观测
		}
		observations = append(observations, Observation{
			UpstreamModel: m.ID,
			Spec:          PriceSpec{Currency: "USD", Components: components},
		})
	}
	return observations, skipped, nil
}

// normalizeOpenRouter 把 OpenRouter 的 pricing 对象（USD/token 字符串）归一化成
// 本平台的 []Component（USD/百万 token，decimal 全程不用 float，技术方案
// §7.16.5 的示例代码）。
func normalizeOpenRouter(p map[string]json.RawMessage) ([]Component, error) {
	var out []Component
	for field, meter := range orMeters {
		raw, ok := p[field]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("pricesync/openrouter: pricing.%s: %w", field, err)
		}
		if s == "" {
			continue // OpenRouter 用空字符串表示"这个模型不支持该计量项"，不是 0 价
		}
		v, err := decimal.NewFromString(s)
		if err != nil {
			return nil, fmt.Errorf("pricesync/openrouter: pricing.%s=%q: %w", field, s, err)
		}
		out = append(out, Component{
			Meter: meter, Unit: pricing.UnitPer1MTokens, ServiceTier: "default",
			UnitPrice: v.Mul(decimal.NewFromInt(1_000_000)),
		})
	}
	return out, nil
}
