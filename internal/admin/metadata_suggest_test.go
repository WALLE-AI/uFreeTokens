package admin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildMetadataSuggestion_External(t *testing.T) {
	vm := &VirtualModel{ID: 7, Name: "deepseek/deepseek-chat-v3.1:free", Type: "chat", Capabilities: []string{"stream", "tools", "reasoning"}}
	meta := &observedMeta{
		Name:        "DeepSeek: DeepSeek V3.1 (free)",
		Description: "DeepSeek-V3.1 is a **hybrid** reasoning model. See [docs](https://x.y).\n\nSecond paragraph.",
		Source:      "openrouter_models",
	}
	got := BuildMetadataSuggestion(vm, meta)
	if got.DisplayName != (SuggestedText{Value: "DeepSeek V3.1", Source: SuggestSourceExternal, Detail: "openrouter_models"}) {
		t.Errorf("display_name = %+v", got.DisplayName)
	}
	if got.ProviderDisplay != (SuggestedText{Value: "DeepSeek", Source: SuggestSourceVendor}) {
		t.Errorf("provider_display = %+v", got.ProviderDisplay)
	}
	if got.Description.Value != "DeepSeek-V3.1 is a hybrid reasoning model. See docs." {
		t.Errorf("description = %q", got.Description.Value)
	}
	if want := []string{"reasoning", "tools"}; !reflect.DeepEqual(got.Tags.Value, want) {
		t.Errorf("tags = %v, want %v", got.Tags.Value, want)
	}
}

func TestBuildMetadataSuggestion_Derived(t *testing.T) {
	cases := []struct {
		vm              VirtualModel
		display, vendor string
		vendorSource    string
		tags            []string
	}{
		{VirtualModel{Name: "qwen3-coder-plus", Type: "chat", Capabilities: []string{"tools", "vision"}}, "Qwen3 Coder Plus", "Qwen", SuggestSourceVendor, []string{"coding", "tools", "vision"}},
		{VirtualModel{Name: "gpt-4o-mini", Type: "chat"}, "GPT-4o Mini", "OpenAI", SuggestSourceVendor, []string{}},
		{VirtualModel{Name: "glm-4.5", Type: "chat"}, "GLM-4.5", "Z.ai", SuggestSourceVendor, []string{}},
		{VirtualModel{Name: "acme-labs/foo-r1", Type: "embedding"}, "Foo R1", "Acme Labs", SuggestSourceDerived, []string{"embedding"}},
		{VirtualModel{Name: "whatever-model", Type: "audio", Capabilities: []string{"asr"}}, "Whatever Model", "", "", []string{"transcription"}},
	}
	for _, c := range cases {
		got := BuildMetadataSuggestion(&c.vm, nil)
		if got.DisplayName.Value != c.display || got.DisplayName.Source != SuggestSourceDerived {
			t.Errorf("%s: display_name = %+v, want %q", c.vm.Name, got.DisplayName, c.display)
		}
		if got.ProviderDisplay.Value != c.vendor || got.ProviderDisplay.Source != c.vendorSource {
			t.Errorf("%s: provider_display = %+v, want %q/%q", c.vm.Name, got.ProviderDisplay, c.vendor, c.vendorSource)
		}
		if got.Description.Value != "" {
			t.Errorf("%s: description should be empty without external meta, got %q", c.vm.Name, got.Description.Value)
		}
		if !reflect.DeepEqual(got.Tags.Value, c.tags) {
			t.Errorf("%s: tags = %v, want %v", c.vm.Name, got.Tags.Value, c.tags)
		}
	}
}

func TestBuildMetadataSuggestion_ExternalVendorFallback(t *testing.T) {
	vm := &VirtualModel{Name: "some-unknown-model", Type: "chat"}
	got := BuildMetadataSuggestion(vm, &observedMeta{Name: "Acme: Some Model", Source: "models_dev"})
	if got.ProviderDisplay != (SuggestedText{Value: "Acme", Source: SuggestSourceExternal, Detail: "models_dev"}) {
		t.Errorf("provider_display = %+v", got.ProviderDisplay)
	}
	if got.DisplayName.Value != "Some Model" {
		t.Errorf("display_name = %+v", got.DisplayName)
	}
}

func TestCleanDescription_Truncates(t *testing.T) {
	long := strings.Repeat("This model is great. ", 40)
	got := cleanDescription(long)
	if n := len([]rune(got)); n > maxSuggestedDescription || !strings.HasSuffix(got, ".") {
		t.Errorf("len=%d suffix=%q", n, got[len(got)-5:])
	}
}

func TestParseGeneratedDescription(t *testing.T) {
	got, err := ParseGeneratedDescription("```json\n{\"description\": \"  高性价比的  推理模型。 \"}\n```")
	if err != nil || got != "高性价比的 推理模型。" {
		t.Errorf("got %q, %v", got, err)
	}
	for _, bad := range []string{`not json`, `{"description":""}`, `{"description":"` + strings.Repeat("长", maxGeneratedDescription+1) + `"}`} {
		if _, err := ParseGeneratedDescription(bad); err == nil {
			t.Errorf("ParseGeneratedDescription(%.30q) should fail", bad)
		}
	}
}

func TestSuggestVirtualModelMetadata_LLMNotConfigured(t *testing.T) {
	s := &Service{}
	if _, err := s.SuggestVirtualModelMetadata(context.Background(), 1, SuggestOptions{UseLLM: true}); !errors.Is(err, ErrLLMNotConfigured) {
		t.Errorf("err = %v, want ErrLLMNotConfigured", err)
	}
}

type fakeDescGen struct{ got ModelDescriptionInput }

func (f *fakeDescGen) GenerateModelDescription(_ context.Context, in ModelDescriptionInput) (string, error) {
	f.got = in
	return "生成的介绍。", nil
}
func (f *fakeDescGen) ModelName() string { return "fake-llm" }

func TestAutofillAndLLMSuggestion(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()
	vm, err := s.CreateVirtualModel(ctx, CreateVirtualModelInput{
		Name: "deepseek/" + uniqueCode(t), Family: "test", Type: "chat", ContextWindow: 64000, MaxOutput: 8192,
		Capabilities: []string{"stream", "reasoning"},
	})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	// 运营已填了介绍和评分：自动填充只补其余空字段，介绍与评分保持原样。
	if err := s.SetVirtualModelMetadata(ctx, SetVirtualModelMetadataInput{
		VirtualModelID: vm.ID, Description: "运营写的介绍", Scores: map[string]any{"intelligence_index": 50.0},
	}); err != nil {
		t.Fatalf("SetVirtualModelMetadata: %v", err)
	}

	dry, err := s.AutofillVirtualModelMetadata(ctx, vm.ID, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Applied || dry.Changes["provider_display"] != "DeepSeek" || dry.Changes["description"] != nil {
		t.Errorf("dry run = %+v", dry)
	}
	if md, _ := s.GetVirtualModelMetadata(ctx, vm.ID); md.ProviderDisplay != nil {
		t.Errorf("dry run wrote provider_display")
	}

	res, err := s.AutofillVirtualModelMetadata(ctx, vm.ID, false)
	if err != nil || !res.Applied {
		t.Fatalf("autofill: %+v, %v", res, err)
	}
	md, err := s.GetVirtualModelMetadata(ctx, vm.ID)
	if err != nil {
		t.Fatalf("GetVirtualModelMetadata: %v", err)
	}
	if md.ProviderDisplay == nil || *md.ProviderDisplay != "DeepSeek" || md.DisplayName == nil || *md.Description != "运营写的介绍" ||
		!reflect.DeepEqual(md.Tags, []string{"reasoning"}) || !strings.Contains(string(md.Scores), "intelligence_index") {
		t.Errorf("metadata after autofill = %+v (scores %s)", md, md.Scores)
	}
	// 再跑一次没有可填的字段。
	if again, err := s.AutofillVirtualModelMetadata(ctx, vm.ID, false); err != nil || again.Applied || len(again.Changes) != 0 {
		t.Errorf("second autofill = %+v, %v", again, err)
	}

	gen := &fakeDescGen{}
	s.SetDescriptionGenerator(gen)
	sug, err := s.SuggestVirtualModelMetadata(ctx, vm.ID, SuggestOptions{UseLLM: true})
	if err != nil {
		t.Fatalf("suggest with LLM: %v", err)
	}
	if sug.Description != (SuggestedText{Value: "生成的介绍。", Source: SuggestSourceLLM, Detail: "fake-llm"}) || !sug.LLMAvailable {
		t.Errorf("suggestion = %+v", sug)
	}
	if gen.got.Vendor != "DeepSeek" || gen.got.ContextWindow != 64000 {
		t.Errorf("generator input = %+v", gen.got)
	}
}
