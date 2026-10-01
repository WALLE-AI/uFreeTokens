package pricesync

import (
	"reflect"
	"testing"

	"github.com/shopspring/decimal"
)

// 模型参数提取（预填待上架表单）：三个带参数的来源各取一条按其公开字段手工构造的样例。

func TestNormalizeOpenRouter_ExtractsMeta(t *testing.T) {
	body := []byte(`{"data": [{
		"id": "deepseek/deepseek-r1:free",
		"name": "DeepSeek: R1 (free)",
		"context_length": 163840,
		"architecture": {"input_modalities": ["text", "image"], "output_modalities": ["text"]},
		"top_provider": {"context_length": 128000, "max_completion_tokens": 32768},
		"supported_parameters": ["max_tokens", "tools", "reasoning", "include_reasoning", "response_format"],
		"pricing": {"prompt": "0", "completion": "0"}
	}]}`)
	obs, _, err := normalizeOpenRouterResponse(body)
	if err != nil || len(obs) != 1 {
		t.Fatalf("normalize: obs=%v err=%v", obs, err)
	}
	want := &ModelMeta{
		Name: "DeepSeek: R1 (free)", Type: "chat", ContextWindow: 128000, MaxOutput: 32768,
		Capabilities:    []string{"stream", "tools", "vision", "json_mode", "reasoning"},
		InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, Source: "openrouter_models",
	}
	if !reflect.DeepEqual(obs[0].Meta, want) {
		t.Errorf("meta = %+v, want %+v", obs[0].Meta, want)
	}
	if !obs[0].Spec.isFree() {
		t.Errorf("spec should be free: %+v", obs[0].Spec)
	}
}

func TestNormalizeOpenRouter_NoMetaFields(t *testing.T) {
	obs, _, err := normalizeOpenRouterResponse([]byte(`{"data": [{"id": "x/y", "pricing": {"prompt": "0.000001", "completion": "0.000002"}}]}`))
	if err != nil || len(obs) != 1 {
		t.Fatalf("normalize: obs=%v err=%v", obs, err)
	}
	if obs[0].Meta != nil {
		t.Errorf("meta = %+v, want nil", obs[0].Meta)
	}
	if obs[0].Spec.isFree() {
		t.Error("paid spec reported as free")
	}
}

func TestNormalizeModelsDev_ExtractsMeta(t *testing.T) {
	body := []byte(`{"siliconflow": {"models": {"Qwen/Qwen3-8B": {
		"id": "Qwen/Qwen3-8B", "name": "Qwen3 8B", "reasoning": true, "tool_call": true,
		"modalities": {"input": ["text"], "output": ["text"]},
		"limit": {"context": 131072, "output": 8192},
		"cost": {"input": 0, "output": 0}
	}}}}`)
	obs, _, err := normalizeModelsDevResponse(body, modelsDevConfig{Providers: []string{"siliconflow"}}, false)
	if err != nil || len(obs) != 1 {
		t.Fatalf("normalize: obs=%v err=%v", obs, err)
	}
	if obs[0].UpstreamModel != "siliconflow/Qwen/Qwen3-8B" {
		t.Errorf("upstream_model = %q", obs[0].UpstreamModel)
	}
	m := obs[0].Meta
	if m == nil || m.Type != "chat" || m.ContextWindow != 131072 || m.MaxOutput != 8192 ||
		!reflect.DeepEqual(m.Capabilities, []string{"stream", "tools", "reasoning"}) {
		t.Errorf("meta = %+v", m)
	}
}

func TestNormalizeLiteLLM_ExtractsMeta(t *testing.T) {
	body := []byte(`{
		"gpt-x": {"input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002, "max_input_tokens": 200000,
		          "max_output_tokens": 16384, "mode": "chat", "supports_function_calling": true, "supports_vision": true},
		"embed-x": {"input_cost_per_token": 0.0000001, "output_cost_per_token": 0, "max_input_tokens": 8192, "mode": "embedding"}
	}`)
	obs, _, err := normalizeLiteLLMResponse(body)
	if err != nil || len(obs) != 2 {
		t.Fatalf("normalize: obs=%v err=%v", obs, err)
	}
	byModel := map[string]*ModelMeta{}
	for _, o := range obs {
		byModel[o.UpstreamModel] = o.Meta
	}
	if m := byModel["gpt-x"]; m == nil || m.Type != "chat" || m.ContextWindow != 200000 || m.MaxOutput != 16384 ||
		!reflect.DeepEqual(m.Capabilities, []string{"stream", "tools", "vision"}) {
		t.Errorf("gpt-x meta = %+v", m)
	}
	if m := byModel["embed-x"]; m == nil || m.Type != "embedding" || m.Capabilities != nil {
		t.Errorf("embed-x meta = %+v", m)
	}
}

func TestTypeFromModalities(t *testing.T) {
	for _, c := range []struct {
		out  []string
		want string
	}{
		{nil, ""},
		{[]string{"text"}, "chat"},
		{[]string{"text", "image"}, "chat"},
		{[]string{"image"}, "image"},
		{[]string{"audio"}, "audio"},
		{[]string{"embeddings"}, "embedding"},
	} {
		if got := typeFromModalities(c.out); got != c.want {
			t.Errorf("typeFromModalities(%v) = %q, want %q", c.out, got, c.want)
		}
	}
}

func TestPriceSpecIsFree(t *testing.T) {
	zero := Component{UnitPrice: decimal.Zero}
	paid := Component{UnitPrice: decimal.NewFromInt(1)}
	if (PriceSpec{}).isFree() {
		t.Error("empty spec must not count as free")
	}
	if !(PriceSpec{Components: []Component{zero, zero}}).isFree() {
		t.Error("all-zero spec should be free")
	}
	if (PriceSpec{Components: []Component{zero, paid}}).isFree() {
		t.Error("partially paid spec must not be free")
	}
}
