package pricesync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// models.dev（https://models.dev/api.json，开源、MIT）按"提供商 -> 模型"组织，同一个模型在
// 不同提供商下各有报价，cost 单位是美元 / 百万 token。L4 社区数据集：只做交叉校验和情报，
// 永不单独生效（DecidePolicy 对 L4 永远给 pending）。
//
// 来源 config：
//
//	{"providers": ["deepseek", "moonshotai", ...]}  // 只取这些提供商；空 = 全部（不建议，约 8000 条）
//
// upstream_model 的写法：来源绑定了本平台的某个 provider（provider_id 非空）且只取一个提供商时，
// 用 models.dev 里的模型 ID 原样（与该 provider 的渠道 upstream_model 对得上，可以走正常的
// Ingest 比价流程）；否则写成 "提供商/模型ID"，只作市场情报。

var modelsDevMeters = map[string]pricing.Meter{
	"input":       pricing.MeterInput,
	"output":      pricing.MeterOutput,
	"cache_read":  pricing.MeterInputCacheRead,
	"cache_write": pricing.MeterInputCacheWrite,
	"reasoning":   pricing.MeterOutputReasoning,
}

type modelsDevConfig struct {
	Providers []string `json:"providers"`
}

type modelsDevModel struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Status           string                     `json:"status"`
	Cost             map[string]json.RawMessage `json:"cost"`
	Attachment       bool                       `json:"attachment"`
	Reasoning        bool                       `json:"reasoning"`
	ToolCall         bool                       `json:"tool_call"`
	StructuredOutput bool                       `json:"structured_output"`
	Modalities       struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

func (m modelsDevModel) meta() *ModelMeta {
	return metaOrNil(ModelMeta{
		Name: m.Name, Type: typeFromModalities(m.Modalities.Output), ContextWindow: m.Limit.Context, MaxOutput: m.Limit.Output,
		Capabilities:    capabilitiesFrom(m.ToolCall, slices.Contains(m.Modalities.Input, "image"), m.StructuredOutput, m.Reasoning),
		InputModalities: m.Modalities.Input, OutputModalities: m.Modalities.Output, Source: "modelsdev",
	})
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

// normalizeModelsDevResponse 解析整个 api.json。plainIDs=true 时 upstream_model 不带提供商前缀。
func normalizeModelsDevResponse(body []byte, cfg modelsDevConfig, plainIDs bool) ([]Observation, map[string]error, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var top map[string]modelsDevProvider
	if err := dec.Decode(&top); err != nil {
		return nil, nil, fmt.Errorf("pricesync/modelsdev: decode response: %w", err)
	}
	skipped := map[string]error{}
	var out []Observation
	providers := cfg.Providers
	if len(providers) == 0 {
		for p := range top {
			providers = append(providers, p)
		}
		slices.Sort(providers)
	}
	for _, p := range providers {
		prov, ok := top[p]
		if !ok {
			continue
		}
		ids := make([]string, 0, len(prov.Models))
		for id := range prov.Models {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			m := prov.Models[id]
			if m.Status == "deprecated" || len(m.Cost) == 0 {
				continue
			}
			components, err := normalizeModelsDevCost(m.Cost)
			name := id
			if !plainIDs {
				name = p + "/" + id
			}
			if err != nil {
				skipped[name] = err
				continue
			}
			if len(components) == 0 {
				continue
			}
			out = append(out, Observation{UpstreamModel: name, Spec: PriceSpec{Currency: "USD", Components: components}, Meta: m.meta()})
		}
	}
	return out, skipped, nil
}

func normalizeModelsDevCost(cost map[string]json.RawMessage) ([]Component, error) {
	var out []Component
	for field, meter := range modelsDevMeters {
		raw, ok := cost[field]
		if !ok || string(raw) == "null" {
			continue
		}
		v, err := decimal.NewFromString(string(raw))
		if err != nil {
			return nil, fmt.Errorf("pricesync/modelsdev: cost.%s=%s: %w", field, raw, err)
		}
		out = append(out, Component{Meter: meter, Unit: pricing.UnitPer1MTokens, ServiceTier: "default", UnitPrice: v})
	}
	// 固定顺序，保证同样的价格每次序列化出同样的 spec_hash。
	slices.SortFunc(out, func(a, b Component) int { return compareStrings(string(a.Meter), string(b.Meter)) })
	return out, nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
