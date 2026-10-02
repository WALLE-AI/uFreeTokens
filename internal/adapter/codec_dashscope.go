package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// 阿里云百炼的专用 codec（多供应商接口统一技术实施方案 §6）。请求与响应形状均来自
// 2026-10-02 的实测：
//   - 图像：compatible-mode 的 /images/generations 在 dashscope.aliyuncs.com 上 404，
//     只能用原生同步接口 /api/v1/services/aigc/multimodal-generation/generation；
//   - TTS：同一个原生接口，返回 output.audio.url（wav，OSS 下载地址）；
//   - ASR：compatible-mode 的 chat/completions + input_audio，usage.seconds 是时长。

const dashscopeGenerationPath = "/api/v1/services/aigc/multimodal-generation/generation"

func dashscopeJSONRequest(ctx context.Context, t Target, url string, body map[string]any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthAndTransportHeaders(req, t)
	return req, nil
}

// ---------- 图像 ----------

type dashscopeImageCodec struct{}

func (dashscopeImageCodec) Name() string { return "dashscope.image" }

func (dashscopeImageCodec) Build(ctx context.Context, t Target, c *Call) (*http.Request, error) {
	prompt, _ := c.JSON["prompt"].(string)
	params := map[string]any{}
	if size, _ := c.JSON["size"].(string); size != "" && size != "auto" {
		params["size"] = strings.Replace(size, "x", "*", 1) // 1024x1024 -> 1024*1024
	} else if size, _ := c.JSON["image_size"].(string); size != "" {
		params["size"] = strings.Replace(size, "x", "*", 1)
	}
	for _, k := range []string{"n", "negative_prompt", "seed", "watermark", "prompt_extend"} {
		if v, ok := c.JSON[k]; ok {
			params[k] = v
		}
	}
	if v, ok := c.JSON["batch_size"]; ok {
		if _, has := params["n"]; !has {
			params["n"] = v
		}
	}
	body := map[string]any{
		"model": t.Channel.UpstreamModel,
		"input": map[string]any{"messages": []any{map[string]any{
			"role": "user", "content": []any{map[string]any{"text": prompt}},
		}}},
	}
	if len(params) > 0 {
		body["parameters"] = params
	}
	return dashscopeJSONRequest(ctx, t, dialect.Origin(t.Account.BaseURL)+dashscopeGenerationPath, body)
}

func (dashscopeImageCodec) Decode(_ context.Context, resp *http.Response, _ Target, _ *Call, env Env) (*Result, error) {
	m, err := readJSONBody(resp, env.MaxBody)
	if err != nil {
		return nil, err
	}
	var data []any
	choices, _ := jsonPath(m, "output.choices").([]any)
	for _, ch := range choices {
		content, _ := jsonPath(ch, "message.content").([]any)
		for _, part := range content {
			if u, _ := jsonPath(part, "image").(string); u != "" {
				data = append(data, map[string]any{"url": u})
			}
		}
	}
	out := map[string]any{"data": data}
	u, err := NormalizeImages(out)
	if err != nil {
		return nil, err
	}
	if n := jsonNumber(m, "usage.output_image_count"); n > 0 {
		u.Images = int64(n)
	}
	return &Result{Kind: ResultJSON, JSON: out, RewriteIDModel: true, Usage: u}, nil
}

// ---------- 语音合成 ----------

type dashscopeTTSCodec struct{}

func (dashscopeTTSCodec) Name() string { return "dashscope.tts" }

func (dashscopeTTSCodec) Build(ctx context.Context, t Target, c *Call) (*http.Request, error) {
	text, _ := c.JSON["input"].(string)
	voice, _ := c.JSON["voice"].(string)
	if ep := accountDialect(t).Endpoint(dialect.EndpointSpeech); ep != nil {
		if m, ok := ep.VoiceMap[voice]; ok {
			voice = m
		}
	}
	if voice == "" {
		voice = "Cherry"
	}
	input := map[string]any{"text": text, "voice": voice}
	if lang, _ := c.JSON["language"].(string); lang != "" {
		input["language_type"] = lang
	}
	body := map[string]any{"model": t.Channel.UpstreamModel, "input": input}
	return dashscopeJSONRequest(ctx, t, dialect.Origin(t.Account.BaseURL)+dashscopeGenerationPath, body)
}

// Decode 下载 output.audio.url 后以二进制返回。只下载阿里云 OSS 上的地址（防 SSRF）。
// 百炼只能输出 wav，客户端的 response_format 无法满足时仍返回 wav（Content-Type 如实标注，
// 目录 limits.audio_formats 已声明）。流式请求同样整体返回。
func (dashscopeTTSCodec) Decode(ctx context.Context, resp *http.Response, _ Target, _ *Call, env Env) (*Result, error) {
	m, err := readJSONBody(resp, env.MaxBody)
	if err != nil {
		return nil, err
	}
	if b64, _ := jsonPath(m, "output.audio.data").(string); b64 != "" {
		audio, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("adapter/dashscope: decode audio: %w", err)
		}
		return &Result{Kind: ResultRaw, Body: audio, ContentType: "audio/wav"}, nil
	}
	audioURL, _ := jsonPath(m, "output.audio.url").(string)
	if audioURL == "" {
		return nil, &InBandError{Class: ErrClassUpstreamUnavailable, Detail: "upstream returned no audio"}
	}
	audio, ct, err := downloadMedia(ctx, env, audioURL, ".aliyuncs.com")
	if err != nil {
		return nil, err
	}
	if ct == "" || ct == "application/octet-stream" || ct == "audio/x-wav" {
		ct = "audio/wav"
	}
	return &Result{Kind: ResultRaw, Body: audio, ContentType: ct}, nil
}

// downloadMedia 下载上游返回的媒体链接：只允许 http(s) 与指定后缀的主机（防 SSRF），
// 大小受 env.MaxBody 限制。
func downloadMedia(ctx context.Context, env Env, raw, hostSuffix string) ([]byte, string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.HasSuffix(u.Hostname(), hostSuffix) {
		return nil, "", fmt.Errorf("adapter: refusing to download media from %q", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	client := env.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("adapter: download media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("adapter: download media: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, env.MaxBody+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > env.MaxBody {
		return nil, "", fmt.Errorf("adapter: downloaded media exceeds %d bytes", env.MaxBody)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// ---------- 语音识别 ----------

type dashscopeASRCodec struct{}

func (dashscopeASRCodec) Name() string { return "dashscope.asr_chat" }

func (dashscopeASRCodec) Build(ctx context.Context, t Target, c *Call) (*http.Request, error) {
	if c.Form == nil {
		return nil, fmt.Errorf("adapter/dashscope: transcription needs a multipart form")
	}
	if ep := accountDialect(t).Endpoint(dialect.EndpointTranscriptions); ep != nil && ep.Limits.MaxFileBytes > 0 && int64(len(c.Form.File)) > ep.Limits.MaxFileBytes {
		return nil, &InBandError{Class: ErrClassBadRequest, Status: http.StatusRequestEntityTooLarge,
			Detail: fmt.Sprintf("audio file exceeds %d bytes for this model", ep.Limits.MaxFileBytes)}
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(c.Form.Filename)), ".")
	if ext == "" {
		ext = "mpeg"
	}
	if ext == "mp3" {
		ext = "mpeg"
	}
	dataURL := "data:audio/" + ext + ";base64," + base64.StdEncoding.EncodeToString(c.Form.File)
	body := map[string]any{
		"model":    t.Channel.UpstreamModel,
		"stream":   false,
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": dataURL}}}}},
	}
	if lang := c.Form.Field("language"); lang != "" {
		body["asr_options"] = map[string]any{"language": lang}
	}
	return dashscopeJSONRequest(ctx, t, strings.TrimRight(t.Account.BaseURL, "/")+EndpointChat, body)
}

func (dashscopeASRCodec) Decode(_ context.Context, resp *http.Response, _ Target, _ *Call, env Env) (*Result, error) {
	m, err := readJSONBody(resp, env.MaxBody)
	if err != nil {
		return nil, err
	}
	text, _ := jsonPath(m, "choices").([]any)
	var content string
	if len(text) > 0 {
		content, _ = jsonPath(text[0], "message.content").(string)
	}
	out := map[string]any{"text": content}
	ms := TranscriptionMillis(m)
	if ms > 0 {
		out["usage"] = map[string]any{"type": "duration", "seconds": float64(ms) / 1000}
	}
	raw, _ := json.Marshal(out)
	return &Result{Kind: ResultRaw, Body: raw, ContentType: "application/json",
		Usage: schema.Usage{AudioMillis: ms, Requests: 1, Source: schema.UsageSourceUpstream}}, nil
}

// jsonPath 按点分路径取任意值。
func jsonPath(v any, p string) any {
	cur := v
	for _, k := range strings.Split(p, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}
