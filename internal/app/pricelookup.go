package app

import (
	"context"
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// 两个已实现真实 HTTP 抓取的 L4/聚合来源（见 internal/pricesync 包文档）的
// 规范地址——都是调用方一般会用的默认值，请求体里都可以覆盖。
const (
	defaultLiteLLMDatasetURL   = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	defaultOpenRouterModelsURL = "https://openrouter.ai/api/v1/models"
)

type referencePriceLookupRequest struct {
	LiteLLMDatasetURL   string   `json:"litellm_dataset_url"`   // 留空用 defaultLiteLLMDatasetURL
	OpenRouterModelsURL string   `json:"openrouter_models_url"` // 留空用 defaultOpenRouterModelsURL
	UpstreamModels      []string `json:"upstream_models"`
}

type referencePriceLookupResult struct {
	Matched bool   `json:"matched"`
	Source  string `json:"source,omitempty"` // "openrouter" / "litellm"；未匹配时留空
	Input   string `json:"input,omitempty"`  // 每 1M token 美元；未匹配时留空
	Output  string `json:"output,omitempty"` // 每 1M token 美元；未匹配时留空
}

// referencePriceLookup 是"给一批上游模型 ID，去几个真实的社区/聚合价格来源
// 里查一遍有没有现成的价格"这个只读查询的 HTTP 入口——不写数据库，也不经过
// pricesync.Engine 的校验/审批流水线（那套是给"已经生效的价格发生变化"设计
// 的，L4 来源按策略永不单独生效，见 pricesync.DecidePolicy）。单纯给 test_web
// 的导入界面提供一个可编辑的参考默认值，不是自动生效的价格来源，两个来源都
// 匹配不到的模型仍然需要管理员自己判断填多少。
//
// 同时查 OpenRouter 和 LiteLLM 两个来源，OpenRouter 优先——它把每个模型的
// 实时计费直接放进模型列表接口本身（不是社区维护、可能滞后的静态数据集），
// 见 internal/pricesync/openrouter.go 包注释。两个来源用不同的 key 命名习惯
// （比如同一个模型在 OpenRouter 可能叫 "deepseek/deepseek-v3"，在 LiteLLM
// 可能叫别的），这里只做精确匹配，不做模糊猜测——匹配不上比给一个可能对应
// 错了模型的价格更安全。
//
// 两个来源里只要有一个抓取失败就把那个来源当作"全员未匹配"处理、不整体
// 报错——只有两个都失败才返回 502，这样某一个来源临时抽风不会拖累另一个
// 还能用的来源。
func (h *adminHandlers) referencePriceLookup(w http.ResponseWriter, r *http.Request) {
	var in referencePriceLookupRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if len(in.UpstreamModels) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "upstream_models must not be empty")
		return
	}
	litellmURL := in.LiteLLMDatasetURL
	if litellmURL == "" {
		litellmURL = defaultLiteLLMDatasetURL
	}
	openRouterURL := in.OpenRouterModelsURL
	if openRouterURL == "" {
		openRouterURL = defaultOpenRouterModelsURL
	}

	openRouterByModel, openRouterErr := fetchObservationsByModel(r.Context(), pricesync.OpenRouterFetcher{}, openRouterURL)
	litellmByModel, litellmErr := fetchObservationsByModel(r.Context(), &pricesync.LiteLLMFetcher{}, litellmURL)
	if openRouterErr != nil && litellmErr != nil {
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_unavailable",
			"both reference price sources failed: openrouter: "+openRouterErr.Error()+"; litellm: "+litellmErr.Error())
		return
	}

	out := make(map[string]referencePriceLookupResult, len(in.UpstreamModels))
	for _, model := range in.UpstreamModels {
		if obs, ok := openRouterByModel[model]; ok {
			out[model] = observationToResult(obs, "openrouter")
			continue
		}
		if obs, ok := litellmByModel[model]; ok {
			out[model] = observationToResult(obs, "litellm")
			continue
		}
		out[model] = referencePriceLookupResult{Matched: false}
	}

	resp := map[string]any{
		"data":                  out,
		"currency":              "USD",
		"litellm_dataset_url":   litellmURL,
		"openrouter_models_url": openRouterURL,
	}
	if openRouterErr != nil {
		resp["openrouter_error"] = openRouterErr.Error()
	}
	if litellmErr != nil {
		resp["litellm_error"] = litellmErr.Error()
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func fetchObservationsByModel(ctx context.Context, fetcher pricesync.Fetcher, url string) (map[string]pricesync.Observation, error) {
	observations, err := fetcher.Fetch(ctx, pricesync.Source{URL: url})
	if err != nil {
		return nil, err
	}
	byModel := make(map[string]pricesync.Observation, len(observations))
	for _, obs := range observations {
		byModel[obs.UpstreamModel] = obs
	}
	return byModel, nil
}

func observationToResult(obs pricesync.Observation, source string) referencePriceLookupResult {
	res := referencePriceLookupResult{Matched: true, Source: source}
	for _, c := range obs.Spec.Components {
		switch c.Meter {
		case pricing.MeterInput:
			res.Input = c.UnitPrice.String()
		case pricing.MeterOutput:
			res.Output = c.UnitPrice.String()
		}
	}
	return res
}
