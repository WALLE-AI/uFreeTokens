package app

import (
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// defaultLiteLLMDatasetURL 是 LiteLLM 维护的社区价格数据集在 GitHub 上的
// 规范地址（litellm 包自己的 model_cost_map 默认值用的就是这个 URL）。
const defaultLiteLLMDatasetURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

type litellmLookupRequest struct {
	DatasetURL     string   `json:"dataset_url"` // 留空用 defaultLiteLLMDatasetURL
	UpstreamModels []string `json:"upstream_models"`
}

type litellmLookupResult struct {
	Matched bool   `json:"matched"`
	Input   string `json:"input,omitempty"`  // 每 1M token 美元；未匹配时留空
	Output  string `json:"output,omitempty"` // 每 1M token 美元；未匹配时留空
}

// litellmPriceLookup 是"给一批上游模型 ID，去 LiteLLM 社区价格数据集里查一遍
// 有没有现成的价格"这个只读查询的 HTTP 入口——只是 pricesync.LiteLLMFetcher
// 结果按 UpstreamModel 精确匹配一遍，不写数据库，也不经过
// pricesync.Engine 的校验/审批流水线（那套是给"已经生效的价格发生变化"设计
// 的，L4 来源按策略永不单独生效，见 pricesync.DecidePolicy）。这里单纯给
// test_web 的导入界面提供一个可编辑的参考默认值，不是自动生效的价格来源，
// 匹配不到的模型仍然需要管理员自己判断填多少。
func (h *adminHandlers) litellmPriceLookup(w http.ResponseWriter, r *http.Request) {
	var in litellmLookupRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if len(in.UpstreamModels) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "upstream_models must not be empty")
		return
	}
	datasetURL := in.DatasetURL
	if datasetURL == "" {
		datasetURL = defaultLiteLLMDatasetURL
	}

	fetcher := &pricesync.LiteLLMFetcher{}
	observations, err := fetcher.Fetch(r.Context(), pricesync.Source{URL: datasetURL})
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_unavailable", "fetch LiteLLM dataset: "+err.Error())
		return
	}

	byModel := make(map[string]pricesync.Observation, len(observations))
	for _, obs := range observations {
		byModel[obs.UpstreamModel] = obs
	}

	out := make(map[string]litellmLookupResult, len(in.UpstreamModels))
	for _, model := range in.UpstreamModels {
		obs, ok := byModel[model]
		if !ok {
			out[model] = litellmLookupResult{Matched: false}
			continue
		}
		res := litellmLookupResult{Matched: true}
		for _, c := range obs.Spec.Components {
			switch c.Meter {
			case pricing.MeterInput:
				res.Input = c.UnitPrice.String()
			case pricing.MeterOutput:
				res.Output = c.UnitPrice.String()
			}
		}
		out[model] = res
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out, "dataset_url": datasetURL, "currency": "USD"})
}
