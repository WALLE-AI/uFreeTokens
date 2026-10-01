package pricesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// litellmMeters 把 LiteLLM `model_prices_and_context_window.json`（技术方案
// §7.16.2 L4 来源）里的价格字段映射到本平台的计量项命名。这个数据集单价单位
// 是"每 token 的美元"（不是每百万 token），归一化时要乘 1,000,000。
var litellmMeters = map[string]pricing.Meter{
	"input_cost_per_token":            pricing.MeterInput,
	"output_cost_per_token":           pricing.MeterOutput,
	"cache_read_input_token_cost":     pricing.MeterInputCacheRead,
	"cache_creation_input_token_cost": pricing.MeterInputCacheWrite,
}

// litellmSampleSpecKey 是这个数据集里一条固定存在的占位说明条目，不是真实
// 模型，必须跳过（拿它当模型解析会产生一条名字很怪、价格也不对的假观测）。
const litellmSampleSpecKey = "sample_spec"

// LiteLLMFetcher 实现 Fetcher 接口：拉取 LiteLLM 维护的社区价格数据集，仅用于
// §7.16.2 表格里 L4 级别定义的"交叉校验"——DecidePolicy 对 L4 来源在任何方向
// 下都只会给出 pending（它不匹配 DecidePolicy 里 L2/L5 的自动通过条件），
// 不需要在这里额外加限制，策略层已经保证了"永不单独生效"。
type LiteLLMFetcher struct {
	HTTPClient *http.Client
}

func (f *LiteLLMFetcher) Name() string { return "litellm_dataset" }

func (f *LiteLLMFetcher) httpClient() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Fetch 需要 src.URL 显式配置数据集地址（不内置一个默认地址——数据集的托管
// 位置属于运维配置，不属于代码；配置错了/地址过期了应该在配置里改，不应该
// 依赖一个可能已经失效的硬编码默认值）。
func (f *LiteLLMFetcher) Fetch(ctx context.Context, src Source) ([]Observation, error) {
	if src.URL == "" {
		return nil, errors.New("pricesync: litellm_dataset source requires a URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("pricesync: build request: %w", err)
	}
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("pricesync: fetch %s: %w", src.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricesync: fetch %s: status %d", src.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("pricesync: read response body: %w", err)
	}
	observations, _, err := normalizeLiteLLMResponse(body)
	return observations, err
}

// normalizeLiteLLMResponse 解析整个数据集（顶层是一个 "模型名 -> 价格信息" 的
// 大 JSON 对象，不是数组）。单个模型解析失败不影响其它模型，见 openrouter.go
// 的 normalizeOpenRouterResponse 同款设计。
func normalizeLiteLLMResponse(body []byte) (observations []Observation, skipped map[string]error, err error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, nil, fmt.Errorf("pricesync/litellm: decode response: %w", err)
	}
	skipped = map[string]error{}
	for modelName, raw := range top {
		if modelName == litellmSampleSpecKey {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			skipped[modelName] = err
			continue
		}
		components, err := normalizeLiteLLM(entry)
		if err != nil {
			skipped[modelName] = err
			continue
		}
		if len(components) == 0 {
			continue // 这个模型没有任何我们认识的价格字段（比如只有 max_tokens 之类的元数据），不产生一条空观测
		}
		observations = append(observations, Observation{
			UpstreamModel: modelName,
			Spec:          PriceSpec{Currency: "USD", Components: components},
			Meta:          litellmMeta(raw),
		})
	}
	return observations, skipped, nil
}

// normalizeLiteLLM 归一化单个模型条目的价格字段。全程用 json.RawMessage +
// decimal.NewFromString，不经过 float64——LiteLLM 的数据集里价格是 JSON 数字
// 字面量（不像 OpenRouter 那样是字符串），直接用原始文本喂给 decimal 解析，
// 避免先解成 float64 再转 decimal 引入的精度损失。
func normalizeLiteLLM(entry map[string]json.RawMessage) ([]Component, error) {
	var out []Component
	for field, meter := range litellmMeters {
		raw, ok := entry[field]
		if !ok || string(raw) == "null" {
			continue
		}
		v, err := decimal.NewFromString(string(raw))
		if err != nil {
			return nil, fmt.Errorf("pricesync/litellm: %s=%s: %w", field, raw, err)
		}
		out = append(out, Component{
			Meter: meter, Unit: pricing.UnitPer1MTokens, ServiceTier: "default",
			UnitPrice: v.Mul(decimal.NewFromInt(1_000_000)),
		})
	}
	return out, nil
}

// litellmModeTypes 把 LiteLLM 的 mode 映射到本平台的模型类型。
var litellmModeTypes = map[string]string{
	"chat": "chat", "completion": "chat", "responses": "chat", "embedding": "embedding", "rerank": "rerank",
	"image_generation": "image", "audio_transcription": "audio", "audio_speech": "audio",
}

// litellmMeta 提取模型参数；条目字段类型不规整时（社区数据集偶有字符串数字）只丢掉参数，不影响价格。
func litellmMeta(raw json.RawMessage) *ModelMeta {
	var e struct {
		MaxInputTokens  int    `json:"max_input_tokens"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		Mode            string `json:"mode"`
		FunctionCalling bool   `json:"supports_function_calling"`
		Vision          bool   `json:"supports_vision"`
		ResponseSchema  bool   `json:"supports_response_schema"`
		Reasoning       bool   `json:"supports_reasoning"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil
	}
	m := ModelMeta{Type: litellmModeTypes[e.Mode], ContextWindow: e.MaxInputTokens, MaxOutput: e.MaxOutputTokens, Source: "litellm_dataset"}
	if m.Type == "chat" {
		m.Capabilities = capabilitiesFrom(e.FunctionCalling, e.Vision, e.ResponseSchema, e.Reasoning)
	}
	return metaOrNil(m)
}
