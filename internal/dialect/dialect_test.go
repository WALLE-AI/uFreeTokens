package dialect

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoad_NoDialect(t *testing.T) {
	for _, raw := range []string{"", "null", "{}"} {
		d, err := Load("", json.RawMessage(raw))
		if err != nil || d != nil {
			t.Errorf("Load(%q) = %v, %v; want nil, nil", raw, d, err)
		}
	}
	// nil 方言上的查询都安全
	var d *Dialect
	if c, s := d.CodecFor(EndpointImages, "x"); c != "" || s != nil {
		t.Errorf("nil CodecFor = %q %v", c, s)
	}
	if got := d.ResolveURL("https://a.example/v1/", EndpointRerank, "/rerank"); got != "https://a.example/v1/rerank" {
		t.Errorf("nil ResolveURL = %s", got)
	}
}

func TestLoad_PresetMergedWithOverride(t *testing.T) {
	d, err := Load("", json.RawMessage(`{"preset":"openrouter","endpoints":{"speech":{"defaults":{"speed":1.2}}},"transport":{"timeout_ms":5000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Preset != "openrouter" || d.Errors.BodyErrorField != "error" {
		t.Errorf("preset fields lost: %+v", d)
	}
	sp := d.Endpoint(EndpointSpeech)
	// 深合并：预设的 response_format 保留，覆盖新增 speed
	if sp.Defaults["response_format"] != "mp3" || sp.Defaults["speed"] != 1.2 {
		t.Errorf("speech defaults = %v", sp.Defaults)
	}
	if d.Transport.TimeoutMs != 5000 || d.Transport.ExtraHeaders["X-Title"] != "uFreeTokens" {
		t.Errorf("transport = %+v", d.Transport)
	}
	if got := d.ResolveURL("https://openrouter.ai/api/v1", EndpointImages, "/images/generations"); got != "https://openrouter.ai/api/v1/images" {
		t.Errorf("images url = %s", got)
	}
}

func TestResolveURL_OriginTemplate(t *testing.T) {
	d, err := Load("dashscope", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := d.ResolveURL("https://dashscope.aliyuncs.com/compatible-mode/v1", EndpointRerank, "/rerank")
	if got != "https://dashscope.aliyuncs.com/compatible-api/v1/reranks" {
		t.Errorf("rerank url = %s", got)
	}
}

func TestCodecFor_ByModel(t *testing.T) {
	d, err := Load("", json.RawMessage(`{"endpoints":{"images":{"codec":"openai.passthrough","by_model":[
		{"match":"^wan","codec":"dashscope.image"},{"match":"^legacy-","supported":false}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := d.CodecFor(EndpointImages, "qwen-image-3.0"); c != "openai.passthrough" {
		t.Errorf("default codec = %s", c)
	}
	if c, _ := d.CodecFor(EndpointImages, "wan2.7-image"); c != "dashscope.image" {
		t.Errorf("by_model codec = %s", c)
	}
	if _, s := d.CodecFor(EndpointImages, "legacy-x"); s == nil || *s {
		t.Errorf("by_model supported = %v", s)
	}
}

func TestLoad_Rejects(t *testing.T) {
	cases := map[string]string{
		"unknown preset":    `{"preset":"nope"}`,
		"unknown endpoint":  `{"endpoints":{"video":{}}}`,
		"unknown codec":     `{"endpoints":{"images":{"codec":"x.y"}}}`,
		"unknown transform": `{"endpoints":{"images":{"transforms":["boom"]}}}`,
		"bad regex":         `{"endpoints":{"images":{"by_model":[{"match":"("}]}}}`,
		"unknown field":     `{"endpoints":{"images":{"pathh":"/x"}}}`,
		"bad validation":    `{"auth":{"validation":{"method":"ping"}}}`,
		"url without url":   `{"auth":{"validation":{"method":"url"}}}`,
		"not an object":     `[1,2]`,
	}
	for name, raw := range cases {
		if _, err := Load("", json.RawMessage(raw)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestPresets_AllValidAndNoted(t *testing.T) {
	names := PresetNames()
	if len(names) < 4 {
		t.Fatalf("presets = %v", names)
	}
	for _, n := range names {
		d, err := Load(n, nil)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if !strings.Contains(d.Notes, "认证") {
			t.Errorf("%s: notes must say whether the preset is certified: %q", n, d.Notes)
		}
	}
}
