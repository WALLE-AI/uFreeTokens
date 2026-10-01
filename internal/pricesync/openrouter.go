package pricesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

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

// OpenRouterFetcher 抓取 OpenRouter 的公开模型列表接口（GET /api/v1/models，
// 不需要 API Key）——和 LiteLLMFetcher 同类，是 §7.16.2 表格里的 L4 社区/
// 聚合来源，只做交叉校验，不单独生效（DecidePolicy 对 L4 永不自动通过）。
// OpenRouter 是这几个来源里比较特殊的一个：它把每个模型的实时计费直接放在
// 模型列表接口里（技术方案原文举例的 §7.16.5 就是这个接口），不像大多数
// 上游把价格只发布在给人看的文档页——这也是选它接第二个真实数据源的原因。
type OpenRouterFetcher struct {
	HTTPClient *http.Client
}

func (OpenRouterFetcher) Name() string { return "openrouter_models" }

func (f OpenRouterFetcher) httpClient() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Fetch 需要 src.URL 显式配置接口地址（不内置一个默认地址，同 LiteLLMFetcher
// 的取舍——具体调哪个地址属于运维配置，不属于代码；调用方一般会填
// "https://openrouter.ai/api/v1/models"）。
func (f OpenRouterFetcher) Fetch(ctx context.Context, src Source) ([]Observation, error) {
	if src.URL == "" {
		return nil, errors.New("pricesync: openrouter_models source requires a URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("pricesync/openrouter: build request: %w", err)
	}
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("pricesync/openrouter: fetch %s: %w", src.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricesync/openrouter: fetch %s: status %d", src.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("pricesync/openrouter: read response body: %w", err)
	}
	observations, _, err := normalizeOpenRouterResponse(body)
	return observations, err
}

// openRouterModel 是 GET /api/v1/models 响应里我们关心的字段子集。
type openRouterModel struct {
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	ContextLength int                        `json:"context_length"`
	Pricing       map[string]json.RawMessage `json:"pricing"`
	Architecture  struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	TopProvider struct {
		ContextLength       int `json:"context_length"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	SupportedParameters []string `json:"supported_parameters"`
}

// meta 提取模型参数（预填待上架表单用）：上下文优先取 top_provider（实际路由到的那家），
// 能力由 supported_parameters 与输入模态推断。
func (m openRouterModel) meta() *ModelMeta {
	ctx := m.TopProvider.ContextLength
	if ctx == 0 {
		ctx = m.ContextLength
	}
	params := m.SupportedParameters
	has := func(v string) bool { return slices.Contains(params, v) }
	var caps []string
	if len(params) > 0 || len(m.Architecture.InputModalities) > 0 {
		caps = capabilitiesFrom(has("tools"), slices.Contains(m.Architecture.InputModalities, "image"),
			has("response_format") || has("structured_outputs"), has("reasoning") || has("include_reasoning"))
	}
	return metaOrNil(ModelMeta{
		Name: m.Name, Type: typeFromModalities(m.Architecture.OutputModalities), ContextWindow: ctx,
		MaxOutput: m.TopProvider.MaxCompletionTokens, Capabilities: caps,
		InputModalities: m.Architecture.InputModalities, OutputModalities: m.Architecture.OutputModalities,
		Source: "openrouter_models",
	})
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
			Meta:          m.meta(),
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
