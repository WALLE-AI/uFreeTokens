package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// passthroughCodec 是 OpenAI 兼容上游的默认 codec：请求按 OpenAI 形状原样转发（只改写
// model，并应用方言），响应归一化为 OpenAI 形状并提取用量。
type passthroughCodec struct{}

func (passthroughCodec) Name() string { return "openai.passthrough" }

func (passthroughCodec) Build(ctx context.Context, t Target, c *Call) (*http.Request, error) {
	if c.Form != nil {
		body, ct, err := encodeMultipart(c.Form, t.Channel.UpstreamModel)
		if err != nil {
			return nil, err
		}
		return (&OpenAIAdapter{}).BuildRawRequest(ctx, t, c.Endpoint, ct, body)
	}
	return (&OpenAIAdapter{}).BuildRequest(ctx, t, c.Endpoint, c.JSON)
}

// encodeMultipart 重新组装语音识别的 multipart：model 改写为上游模型名，其余字段与文件原样。
func encodeMultipart(f *Form, upstreamModel string) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", upstreamModel)
	for _, kv := range f.Fields {
		_ = mw.WriteField(kv[0], kv[1])
	}
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, f.Filename)}
	ct := f.FileType
	if ct == "" {
		ct = "application/octet-stream"
	}
	h["Content-Type"] = []string{ct}
	fw, err := mw.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(f.File); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

func (passthroughCodec) Decode(_ context.Context, resp *http.Response, t Target, c *Call, env Env) (*Result, error) {
	switch c.Endpoint {
	case EndpointSpeech:
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "audio/mpeg"
		}
		if c.Stream {
			return &Result{Kind: ResultStream, Stream: resp.Body, ContentType: ct}, nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, env.MaxBody))
		if err != nil {
			return nil, err
		}
		return &Result{Kind: ResultRaw, Body: raw, ContentType: ct}, nil

	case EndpointTranscriptions:
		raw, err := io.ReadAll(io.LimitReader(resp.Body, env.MaxBody))
		if err != nil {
			return nil, err
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		res := &Result{Kind: ResultRaw, Body: raw, ContentType: ct, Usage: schema.Usage{Requests: 1, Source: schema.UsageSourceUpstream}}
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			res.Usage.AudioMillis = TranscriptionMillis(m)
			res.Usage.UpstreamCost = usageCost(m)
		}
		return res, nil
	}

	m, err := readJSONBody(resp, env.MaxBody)
	if err != nil {
		return nil, err
	}
	res := &Result{Kind: ResultJSON, JSON: m, RewriteIDModel: true}
	switch c.Endpoint {
	case EndpointRerank:
		res.Usage = rerankUsage(m, accountDialect(t).Endpoint(dialectEndpoint(EndpointRerank)).UsagePaths("input_tokens"))
	case EndpointImages:
		u, err := NormalizeImages(m)
		if err != nil {
			return nil, err
		}
		res.Usage = u
	}
	res.Usage.UpstreamCost = usageCost(m)
	return res, nil
}

func readJSONBody(resp *http.Response, limit int64) (map[string]any, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("adapter: decode upstream response: %w", err)
	}
	return m, nil
}

// defaultRerankUsagePaths 兼容 Cohere / SiliconFlow（meta.billed_units / meta.tokens）、
// Jina / 百炼 / OpenRouter（usage.total_tokens）等常见格式。
var defaultRerankUsagePaths = []string{
	"meta.billed_units.input_tokens", "meta.tokens.input_tokens", "usage.total_tokens", "usage.prompt_tokens", "usage.search_units",
}

func rerankUsage(m map[string]any, paths []string) schema.Usage {
	if len(paths) == 0 {
		paths = defaultRerankUsagePaths
	} else {
		paths = append(append([]string(nil), paths...), defaultRerankUsagePaths...)
	}
	for _, p := range paths {
		if v := jsonNumber(m, p); v > 0 {
			return schema.Usage{InputTokens: int64(v), Source: schema.UsageSourceUpstream}
		}
	}
	return schema.Usage{}
}

// jsonNumber 按点分路径取数值（取不到返回 0）。
func jsonNumber(m map[string]any, path string) float64 {
	var cur any = m
	for _, k := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = mm[k]
	}
	f, _ := cur.(float64)
	return f
}

// usageCost 读取上游报告的本次成本（OpenRouter 的 usage.cost，单位是上游币种）。
func usageCost(m map[string]any) float64 {
	return jsonNumber(m, "usage.cost")
}

// NormalizeImages 把图像响应统一成 OpenAI 形状：只有 images[]（SiliconFlow 旧格式）时补
// data[]；补 created。一张都没生成时返回 *InBandError（不计费）。
func NormalizeImages(m map[string]any) (schema.Usage, error) {
	data, _ := m["data"].([]any)
	if len(data) == 0 {
		if images, ok := m["images"].([]any); ok && len(images) > 0 {
			data = images
			m["data"] = images
		}
	}
	if len(data) == 0 {
		return schema.Usage{}, &InBandError{Class: ErrClassUpstreamUnavailable, Detail: "upstream returned no images"}
	}
	if _, ok := m["created"]; !ok {
		m["created"] = time.Now().Unix()
	}
	return schema.Usage{Images: int64(len(data)), Source: schema.UsageSourceUpstream}, nil
}

// TranscriptionMillis 取上游返回的音频时长：usage.seconds（OpenAI gpt-4o-transcribe、
// SiliconFlow、OpenRouter、百炼）或 duration（verbose_json）。
func TranscriptionMillis(m map[string]any) int64 {
	if sec := jsonNumber(m, "usage.seconds"); sec > 0 {
		return int64(sec * 1000)
	}
	if sec := jsonNumber(m, "duration"); sec > 0 {
		return int64(sec * 1000)
	}
	return 0
}

// IsInBand 判断错误是否是 *InBandError。
func IsInBand(err error) (*InBandError, bool) {
	var ie *InBandError
	ok := errors.As(err, &ie)
	return ie, ok
}
