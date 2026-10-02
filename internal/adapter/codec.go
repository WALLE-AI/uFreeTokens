package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// Codec 处理一个非对话端点（rerank / images / speech / transcriptions）的「构造上游
// 请求 → 解析上游响应」（多供应商接口统一技术实施方案 §2、§6）。对话与向量仍由
// Adapter（BuildRequest / DecodeResponse / StreamDecoder）处理。
//
// 选择规则见 CodecFor：方言（含 by_model）指定的 codec 优先，否则用 openai.passthrough。
type Codec interface {
	Name() string
	Build(ctx context.Context, t Target, c *Call) (*http.Request, error)
	// Decode 读取并解析上游 2xx 响应。返回 *InBandError 表示响应虽然是 2xx，内容却是错误。
	Decode(ctx context.Context, resp *http.Response, t Target, c *Call, env Env) (*Result, error)
}

// Call 是一次端点调用的输入（与具体上游无关）。
type Call struct {
	Endpoint string         // Endpoint* 常量
	JSON     map[string]any // JSON 请求体（multipart 时为 nil）
	Form     *Form          // 语音识别的 multipart 表单
	Stream   bool
}

// Form 是解析后的 multipart 表单：文件之外的字段按出现顺序保留。
type Form struct {
	Model    string
	Fields   [][2]string
	Filename string
	FileType string
	File     []byte
}

// Field 返回表单字段值。
func (f *Form) Field(name string) string {
	for _, kv := range f.Fields {
		if kv[0] == name {
			return kv[1]
		}
	}
	return ""
}

// ResultKind 是 Result 的形态。
type ResultKind int

const (
	ResultJSON   ResultKind = iota // JSON 响应（已归一化为 OpenAI 形状）
	ResultRaw                      // 原样字节（二进制音频、纯文本转写结果）
	ResultStream                   // 流式字节（TTS 的分块音频），调用方负责关闭
)

// Result 是 Decode 的输出。
type Result struct {
	Kind        ResultKind
	JSON        map[string]any
	Body        []byte
	Stream      io.ReadCloser
	ContentType string
	// RewriteIDModel 为 true 时 relay 把 JSON 的 id/model 改写成请求 ID / 虚拟模型名。
	RewriteIDModel bool
	// Usage 为零值时由 relay 按估算值兜底（usage_source=estimated）。
	Usage schema.Usage
}

// Env 是 Decode 需要的运行时依赖。
type Env struct {
	HTTP    *http.Client // 下载上游返回的媒体链接（如百炼 TTS 的音频 URL）
	MaxBody int64        // 响应体与下载内容的大小上限
}

// InBandError 表示上游返回了 2xx，但响应体里是错误（OpenRouter 的 HTTP 200 + error、
// 余额文本），或者结果不可用（图像一张都没生成）。
type InBandError struct {
	Class  ErrorClass
	Status int // 上游错误码（如果有），用于日志
	Detail string
}

func (e *InBandError) Error() string {
	return fmt.Sprintf("adapter: upstream in-band error (class=%s, status=%d): %s", e.Class, e.Status, e.Detail)
}

var codecs = map[string]Codec{}

func registerCodec(c Codec) { codecs[c.Name()] = c }

func init() {
	registerCodec(passthroughCodec{})
	registerCodec(dashscopeImageCodec{})
	registerCodec(dashscopeTTSCodec{})
	registerCodec(dashscopeASRCodec{})
}

// CodecNames 返回已注册的 codec 名（排序），供 /meta/codecs 与测试使用。
func CodecNames() []string {
	names := make([]string, 0, len(codecs))
	for n := range codecs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// CodecFor 返回渠道在某端点上使用的 codec。
func CodecFor(t Target, endpoint string) Codec {
	name, _ := accountDialect(t).CodecFor(dialectEndpoint(endpoint), t.Channel.UpstreamModel)
	if c, ok := codecs[name]; ok {
		return c
	}
	return codecs["openai.passthrough"]
}

// CodecName 返回方言为某端点、某上游模型指定的 codec 名（空 = 协议默认）。
func CodecName(acct *catalog.ProviderAccount, endpoint, upstreamModel string) string {
	if acct == nil {
		return ""
	}
	name, _ := acct.Dialect.CodecFor(dialectEndpoint(endpoint), upstreamModel)
	return name
}

// Serves 判断渠道能否服务某端点：方言显式声明的支持性优先；方言指定了 codec 时以
// codec 是否存在为准；否则按协议默认（anthropic / gemini 只支持对话）。
func Serves(adp Adapter, acct *catalog.ProviderAccount, endpoint, upstreamModel string) bool {
	if acct == nil {
		return false
	}
	name, supported := acct.Dialect.CodecFor(dialectEndpoint(endpoint), upstreamModel)
	if supported != nil {
		return *supported
	}
	if name != "" {
		_, ok := codecs[name]
		return ok
	}
	return SupportsEndpoint(adp, endpoint)
}

// PeekInBandError 检查 2xx 的 JSON 响应体里是否带错误（方言 errors.body_error_field /
// in_band_patterns）。有错误时返回该错误；没有时返回一个可以重新读取的响应体。
// 流式（text/event-stream）与二进制响应原样返回，不读取。
func PeekInBandError(t Target, resp *http.Response, limit int64) (*InBandError, error) {
	d := accountDialect(t)
	if d == nil || (d.Errors.BodyErrorField == "" && len(d.Errors.InBandPatterns) == 0) {
		return nil, nil
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	for _, p := range d.Errors.InBandPatterns {
		if strings.Contains(string(raw), p) {
			return &InBandError{Class: ErrClassKeyExhausted, Detail: p}, nil
		}
	}
	if f := d.Errors.BodyErrorField; f != "" {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			if e, ok := m[f]; ok && e != nil {
				return bodyError(e), nil
			}
		}
	}
	return nil, nil
}

// bodyError 把响应体里的 error 对象（OpenRouter 形状 {code:int, message}）映射成错误类别。
func bodyError(e any) *InBandError {
	ie := &InBandError{Class: ErrClassUpstreamUnavailable}
	switch v := e.(type) {
	case map[string]any:
		ie.Detail, _ = v["message"].(string)
		if c, ok := v["code"].(float64); ok {
			ie.Status = int(c)
		}
	case string:
		ie.Detail = v
	}
	switch {
	case isRegionRestricted(ie.Detail):
		ie.Class = ErrClassUpstreamUnavailable
	case ie.Status == http.StatusPaymentRequired:
		ie.Class = ErrClassKeyExhausted
	case ie.Status == http.StatusTooManyRequests:
		ie.Class = ErrClassRateLimited
	case ie.Status == http.StatusUnauthorized:
		ie.Class = ErrClassKeyInvalid
	case ie.Status >= 400 && ie.Status < 500:
		ie.Class = ErrClassBadRequest
	}
	return ie
}

// isRegionRestricted 识别「模型对所在地区不可用」——这是渠道级问题（换渠道），不是
// Key 失效（不能冻结 Key）。实测 OpenRouter 对 Google、OpenAI 模型返回这条 403。
func isRegionRestricted(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "not available in your region") || strings.Contains(m, "unsupported_country")
}

// ErrNoCodec 表示没有可用的 codec（理论上不会发生：Serves 已经过滤）。
var ErrNoCodec = errors.New("adapter: no codec for endpoint")
