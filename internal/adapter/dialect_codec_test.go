package adapter

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
)

func mustDialect(t *testing.T, preset, override string) *dialect.Dialect {
	t.Helper()
	d, err := dialect.Load(preset, json.RawMessage(override))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func target(baseURL string, d *dialect.Dialect, upstream string) Target {
	return Target{
		Channel: &catalog.Channel{UpstreamModel: upstream},
		Account: &catalog.ProviderAccount{BaseURL: baseURL, Protocol: "openai", Dialect: d},
		Key:     &catalog.ProviderKey{Secret: "sk-up"},
	}
}

func bodyOf(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	raw, _ := io.ReadAll(req.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("body %s: %v", raw, err)
	}
	return m
}

func TestKnownNamesAreImplemented(t *testing.T) {
	for _, n := range dialect.KnownCodecs {
		if _, ok := codecs[n]; !ok {
			t.Errorf("codec %q is known to dialect but not registered", n)
		}
	}
	for _, n := range dialect.KnownTransforms {
		if _, ok := requestTransforms[n]; !ok {
			t.Errorf("transform %q is known to dialect but not implemented", n)
		}
	}
	if !slices.Equal(CodecNames(), []string{"dashscope.asr_chat", "dashscope.image", "dashscope.tts", "openai.passthrough"}) {
		t.Errorf("codecs = %v", CodecNames())
	}
}

func TestBuildRequest_NoDialectUnchanged(t *testing.T) {
	req, err := (&OpenAIAdapter{}).BuildRequest(context.Background(), target("https://up.example/v1/", nil, "m-up"),
		EndpointImages, map[string]any{"model": "vm", "prompt": "p", "n": 2.0})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://up.example/v1/images/generations" || req.Header.Get("Authorization") != "Bearer sk-up" {
		t.Errorf("url=%s auth=%s", req.URL, req.Header.Get("Authorization"))
	}
	if b := bodyOf(t, req); b["model"] != "m-up" || b["n"] != 2.0 {
		t.Errorf("body = %v", b)
	}
}

func TestBuildRequest_OpenRouterDialect(t *testing.T) {
	d := mustDialect(t, "openrouter", "")
	tg := target("https://openrouter.ai/api/v1", d, "qwen/qwen-image-3")
	req, _ := (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointImages, map[string]any{"prompt": "p"})
	if req.URL.String() != "https://openrouter.ai/api/v1/images" || req.Header.Get("X-Title") != "uFreeTokens" {
		t.Errorf("url=%s headers=%v", req.URL, req.Header)
	}
	// TTS：客户端没传 response_format 时补 mp3；传了不覆盖
	req, _ = (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointSpeech, map[string]any{"input": "hi"})
	if b := bodyOf(t, req); b["response_format"] != "mp3" {
		t.Errorf("speech body = %v", b)
	}
	req, _ = (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointSpeech, map[string]any{"input": "hi", "response_format": "pcm"})
	if b := bodyOf(t, req); b["response_format"] != "pcm" {
		t.Errorf("explicit response_format overridden: %v", b)
	}
}

func TestBuildRequest_DashscopeRerankURLAndEmbeddingBase64(t *testing.T) {
	d := mustDialect(t, "dashscope", "")
	tg := target("https://dashscope.aliyuncs.com/compatible-mode/v1", d, "qwen3-rerank")
	req, _ := (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointRerank, map[string]any{"query": "q"})
	if req.URL.String() != "https://dashscope.aliyuncs.com/compatible-api/v1/reranks" {
		t.Errorf("rerank url = %s", req.URL)
	}
	req, _ = (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointEmbeddings, map[string]any{"input": "x", "encoding_format": "base64"})
	if b := bodyOf(t, req); b["encoding_format"] != "float" {
		t.Errorf("embeddings body = %v", b)
	}
	resp := map[string]any{"data": []any{map[string]any{"embedding": []any{1.0, -2.5}}}}
	ApplyResponseTransforms(tg, EndpointEmbeddings, map[string]any{"encoding_format": "base64"}, resp)
	if got := resp["data"].([]any)[0].(map[string]any)["embedding"]; got != "AACAPwAAIMA=" {
		t.Errorf("base64 embedding = %v", got)
	}
}

func TestBuildRequest_ChatDialectAndTransforms(t *testing.T) {
	d := mustDialect(t, "", `{"chat":{"strict_messages":true,"force_single_tool_call":true,"single_system_message":true,"min_max_tokens":256,"thinking_default":{"thinking":{"type":"disabled"}}},
		"endpoints":{"images":{"transforms":["ark_n_to_sequential"],"defaults":{"watermark":false}},"speech":{"transforms":["voice_prefix_upstream_model"],"voice_map":{"alloy":"anna"}}}}`)
	tg := target("https://up.example/v1", d, "up-model")
	msgs := []any{
		map[string]any{"role": "system", "content": "a"},
		map[string]any{"role": "assistant", "content": "x", "reasoning_content": "hidden", "partial": true},
		map[string]any{"role": "system", "content": "b"},
	}
	req, _ := (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointChat, map[string]any{
		"messages": msgs, "tools": []any{map[string]any{}}, "max_tokens": 16.0})
	b := bodyOf(t, req)
	out := b["messages"].([]any)
	if len(out) != 2 || out[0].(map[string]any)["content"] != "a\n\nb" {
		t.Errorf("messages = %v", out)
	}
	if _, ok := out[1].(map[string]any)["reasoning_content"]; ok {
		t.Error("strict_messages kept reasoning_content")
	}
	if b["parallel_tool_calls"] != false || b["max_tokens"] != 256.0 || b["thinking"] == nil {
		t.Errorf("chat body = %v", b)
	}
	// 原始请求的消息不被修改（重试会再用）
	if _, ok := msgs[1].(map[string]any)["reasoning_content"]; !ok {
		t.Error("client messages were mutated")
	}

	req, _ = (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointImages, map[string]any{"prompt": "p", "n": 3.0})
	b = bodyOf(t, req)
	if _, ok := b["n"]; ok || b["sequential_image_generation"] != "auto" || b["watermark"] != false {
		t.Errorf("ark images body = %v", b)
	}
	req, _ = (&OpenAIAdapter{}).BuildRequest(context.Background(), tg, EndpointSpeech, map[string]any{"voice": "alloy"})
	if b := bodyOf(t, req); b["voice"] != "up-model:anna" {
		t.Errorf("voice = %v", b["voice"])
	}
}

func TestServes(t *testing.T) {
	adp := &OpenAIAdapter{}
	vol := &catalog.ProviderAccount{Dialect: mustDialect(t, "volcengine", "")}
	if Serves(adp, vol, EndpointRerank, "x") || !Serves(adp, vol, EndpointImages, "x") {
		t.Error("volcengine: rerank must be unsupported, images supported")
	}
	if !Serves(adp, &catalog.ProviderAccount{}, EndpointRerank, "x") {
		t.Error("openai protocol without dialect serves every endpoint")
	}
	if Serves(&AnthropicAdapter{}, &catalog.ProviderAccount{}, EndpointImages, "x") {
		t.Error("anthropic protocol must not serve images")
	}
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestPeekInBandError(t *testing.T) {
	tg := target("https://openrouter.ai/api/v1", mustDialect(t, "openrouter", ""), "m")
	ie, err := PeekInBandError(tg, jsonResp(200, `{"error":{"code":402,"message":"Insufficient credits"}}`), 1<<20)
	if err != nil || ie == nil || ie.Class != ErrClassKeyExhausted {
		t.Errorf("402 in body: %v %v", ie, err)
	}
	ie, _ = PeekInBandError(tg, jsonResp(200, `{"error":{"code":403,"message":"This model is not available in your region."}}`), 1<<20)
	if ie == nil || ie.Class != ErrClassUpstreamUnavailable {
		t.Errorf("region in body: %v", ie)
	}
	resp := jsonResp(200, `{"data":[1]}`)
	if ie, _ := PeekInBandError(tg, resp, 1<<20); ie != nil {
		t.Errorf("clean body flagged: %v", ie)
	}
	if raw, _ := io.ReadAll(resp.Body); string(raw) != `{"data":[1]}` {
		t.Errorf("body not restored: %s", raw)
	}
	// 没有方言时不读响应体
	if ie, _ := PeekInBandError(target("https://x", nil, "m"), jsonResp(200, `{"error":"x"}`), 1<<20); ie != nil {
		t.Error("no dialect must not peek")
	}
	if c := (&OpenAIAdapter{}).ClassifyError(403, []byte(`{"error":{"message":"This model is not available in your region."}}`)); c != ErrClassUpstreamUnavailable {
		t.Errorf("region 403 class = %s, want upstream_unavailable (not key_invalid)", c)
	}
}

func TestStreamDecoder_ErrorChunk(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"error\":{\"code\":502,\"message\":\"provider died\"},\"choices\":[{\"finish_reason\":\"error\"}]}\n\n"
	dec := (&OpenAIAdapter{}).NewStreamDecoder(io.NopCloser(strings.NewReader(body)), "vm", "rid")
	if _, err := dec.Next(); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	_, err := dec.Next()
	se, ok := err.(*StreamError)
	if !ok || se.Message != "provider died" {
		t.Errorf("err = %v", err)
	}
}

func TestPassthroughDecode_RerankUsageAndCost(t *testing.T) {
	tg := target("https://openrouter.ai/api/v1", mustDialect(t, "openrouter", ""), "m")
	res, err := passthroughCodec{}.Decode(context.Background(), jsonResp(200,
		`{"results":[{"index":0,"relevance_score":0.9}],"usage":{"search_units":3,"cost":0.0012}}`), tg,
		&Call{Endpoint: EndpointRerank}, Env{MaxBody: 1 << 20})
	if err != nil || res.Usage.InputTokens != 3 || res.Usage.UpstreamCost != 0.0012 || !res.RewriteIDModel {
		t.Errorf("res = %+v err = %v", res, err)
	}
	cases := []struct {
		body string
		want int64
	}{
		{`{"meta":{"billed_units":{"input_tokens":30},"tokens":{"input_tokens":20}}}`, 30},
		{`{"meta":{"tokens":{"input_tokens":20}}}`, 20},
		{`{"usage":{"total_tokens":12}}`, 12},
		{`{"results":[]}`, 0},
	}
	for i, c := range cases {
		var m map[string]any
		_ = json.Unmarshal([]byte(c.body), &m)
		if got := rerankUsage(m, nil).InputTokens; got != c.want {
			t.Errorf("case %d: %d, want %d", i, got, c.want)
		}
	}
}

func TestDashscopeImageCodec(t *testing.T) {
	tg := target("https://dashscope.aliyuncs.com/compatible-mode/v1", mustDialect(t, "dashscope", ""), "qwen-image-3.0")
	c := CodecFor(tg, EndpointImages)
	if c.Name() != "dashscope.image" {
		t.Fatalf("codec = %s", c.Name())
	}
	req, err := c.Build(context.Background(), tg, &Call{Endpoint: EndpointImages, JSON: map[string]any{"prompt": "猫", "size": "1024x1024", "n": 1.0}})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation" {
		t.Errorf("url = %s", req.URL)
	}
	b := bodyOf(t, req)
	if b["model"] != "qwen-image-3.0" || b["parameters"].(map[string]any)["size"] != "1024*1024" {
		t.Errorf("body = %v", b)
	}
	res, err := c.Decode(context.Background(), jsonResp(200, `{"output":{"choices":[{"message":{"content":[{"image":"https://oss/1.png","type":"image"}]}}]},"usage":{"output_image_count":1}}`),
		tg, &Call{Endpoint: EndpointImages}, Env{MaxBody: 1 << 20})
	if err != nil || res.Usage.Images != 1 || res.JSON["data"].([]any)[0].(map[string]any)["url"] != "https://oss/1.png" {
		t.Errorf("res = %+v err = %v", res, err)
	}
	if _, err := c.Decode(context.Background(), jsonResp(200, `{"output":{"choices":[]}}`), tg, &Call{}, Env{MaxBody: 1 << 20}); err == nil {
		t.Error("no images must be an error")
	}
}

func TestDashscopeTTSCodec_DownloadsAudio(t *testing.T) {
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-wav")
		_, _ = w.Write([]byte("RIFF....WAVE"))
	}))
	defer audio.Close()
	tg := target("https://dashscope.aliyuncs.com/compatible-mode/v1", mustDialect(t, "dashscope", ""), "qwen3-tts-flash")
	c := CodecFor(tg, EndpointSpeech)
	req, _ := c.Build(context.Background(), tg, &Call{Endpoint: EndpointSpeech, JSON: map[string]any{"input": "你好", "voice": "alloy"}})
	if b := bodyOf(t, req); b["input"].(map[string]any)["voice"] != "Cherry" {
		t.Errorf("voice map not applied: %v", b)
	}
	// 只允许阿里云 OSS 的地址（防 SSRF）
	if _, err := c.Decode(context.Background(), jsonResp(200, `{"output":{"audio":{"url":"`+audio.URL+`/a.wav"}}}`), tg, &Call{}, Env{MaxBody: 1 << 20}); err == nil {
		t.Error("download from a non-OSS host must be refused")
	}
	if _, _, err := downloadMedia(context.Background(), Env{HTTP: audio.Client(), MaxBody: 1 << 20}, audio.URL+"/a.wav", "127.0.0.1"); err != nil {
		t.Errorf("allowed host download failed: %v", err)
	}
	res, err := c.Decode(context.Background(), jsonResp(200, `{"output":{"audio":{"data":"UklGRg=="}}}`), tg, &Call{}, Env{MaxBody: 1 << 20})
	if err != nil || string(res.Body) != "RIFF" || res.ContentType != "audio/wav" {
		t.Errorf("inline audio res = %+v err = %v", res, err)
	}
}

func TestDashscopeASRCodec(t *testing.T) {
	tg := target("https://dashscope.aliyuncs.com/compatible-mode/v1", mustDialect(t, "dashscope", ""), "qwen3-asr-flash")
	c := CodecFor(tg, EndpointTranscriptions)
	form := &Form{Filename: "a.mp3", File: []byte("ID3data"), Fields: [][2]string{{"language", "zh"}}}
	req, err := c.Build(context.Background(), tg, &Call{Endpoint: EndpointTranscriptions, Form: form})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions" {
		t.Errorf("url = %s", req.URL)
	}
	b := bodyOf(t, req)
	part := b["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(part["input_audio"].(map[string]any)["data"].(string), "data:audio/mpeg;base64,") || b["asr_options"].(map[string]any)["language"] != "zh" {
		t.Errorf("body = %v", b)
	}
	res, err := c.Decode(context.Background(), jsonResp(200, `{"choices":[{"message":{"content":"你好"}}],"usage":{"seconds":4}}`), tg, &Call{}, Env{MaxBody: 1 << 20})
	if err != nil || res.Usage.AudioMillis != 4000 || !strings.Contains(string(res.Body), `"text":"你好"`) {
		t.Errorf("res = %+v err = %v", res, err)
	}
	big := &Form{Filename: "a.wav", File: make([]byte, 8<<20)}
	if _, err := c.Build(context.Background(), tg, &Call{Endpoint: EndpointTranscriptions, Form: big}); err == nil {
		t.Error("oversized file must be rejected")
	}
}

func TestPassthroughBuild_Multipart(t *testing.T) {
	tg := target("https://up.example/v1", nil, "whisper-up")
	req, err := passthroughCodec{}.Build(context.Background(), tg, &Call{Endpoint: EndpointTranscriptions,
		Form: &Form{Filename: "a.mp3", File: []byte("x"), Fields: [][2]string{{"language", "zh"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	mr := multipart.NewReader(req.Body, params["boundary"])
	got := map[string]string{}
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		v, _ := io.ReadAll(p)
		got[p.FormName()] = string(v)
	}
	if got["model"] != "whisper-up" || got["language"] != "zh" || got["file"] != "x" {
		t.Errorf("multipart = %v", got)
	}
}
