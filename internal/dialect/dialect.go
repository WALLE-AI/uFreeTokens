// Package dialect 是「供应商方言」：同一协议（主要是 openai）下各家上游的声明式差异
// ——路径覆盖、默认参数、字段改名、用量字段位置、错误识别、传输参数等
// （多供应商接口统一技术实施方案 §3、§15.2）。
//
// 方言来自两处并做深合并：内置预设（presets/*.json，随代码发布，经过实测）←
// 上游账号的 provider_accounts.extra.dialect（运营覆盖）。没有配置方言的账号得到
// nil，行为与引入方言之前完全一致（纯透传）。
//
// 这个包不依赖网关的其他包，catalog（加载）、adapter（应用）、admin（编辑校验）
// 都依赖它。
package dialect

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// 逻辑端点名（方言 endpoints 的键）。
const (
	EndpointChat           = "chat"
	EndpointEmbeddings     = "embeddings"
	EndpointRerank         = "rerank"
	EndpointImages         = "images"
	EndpointSpeech         = "speech"
	EndpointTranscriptions = "transcriptions"
)

var endpointNames = []string{EndpointChat, EndpointEmbeddings, EndpointRerank, EndpointImages, EndpointSpeech, EndpointTranscriptions}

// KnownCodecs 是可以在方言里引用的 codec 名称；实现在 internal/adapter（那边的测试
// 保证每个名称都有实现）。空串 = 协议默认。
var KnownCodecs = []string{"openai.passthrough", "dashscope.image", "dashscope.tts", "dashscope.asr_chat"}

// KnownTransforms 是可以在方言里引用的具名请求 / 响应变换（实现在 internal/adapter）。
var KnownTransforms = []string{"voice_prefix_upstream_model", "ark_n_to_sequential", "embedding_base64_encode"}

// Dialect 是合并、校验后的方言。字段的含义见实施方案 §3.1 与 §15.2。
type Dialect struct {
	Preset    string               `json:"preset,omitempty"`
	Endpoints map[string]*Endpoint `json:"endpoints,omitempty"`
	Errors    Errors               `json:"errors,omitempty"`
	Chat      Chat                 `json:"chat,omitempty"`
	Transport Transport            `json:"transport,omitempty"`
	Auth      Auth                 `json:"auth,omitempty"`
	Catalog   Catalog              `json:"catalog,omitempty"`
	Region    string               `json:"region,omitempty"`
	// Notes 是给运营看的说明（来源、是否经过实测认证），不影响行为。
	Notes string `json:"notes,omitempty"`
}

// Endpoint 是某个逻辑端点上的差异。
type Endpoint struct {
	// Supported 为 false 表示该供应商不提供这个端点；nil = 按协议默认。
	Supported *bool `json:"supported,omitempty"`
	// Codec 为空 = 协议默认（openai 协议是 openai.passthrough）。
	Codec   string      `json:"codec,omitempty"`
	ByModel []ModelRule `json:"by_model,omitempty"`
	// URL 是绝对地址模板（{origin} = base_url 的 scheme://host），优先于 Path；
	// Path 是相对 base_url 的路径覆盖。
	URL        string              `json:"url,omitempty"`
	Path       string              `json:"path,omitempty"`
	Defaults   map[string]any      `json:"defaults,omitempty"` // 客户端没传才补
	Force      map[string]any      `json:"force,omitempty"`    // 总是覆盖
	Drop       []string            `json:"drop,omitempty"`
	Rename     map[string]string   `json:"rename,omitempty"`
	Transforms []string            `json:"transforms,omitempty"`
	VoiceMap   map[string]string   `json:"voice_map,omitempty"`
	SizeMap    map[string]string   `json:"size_map,omitempty"`
	Usage      map[string][]string `json:"usage,omitempty"` // 计量项 -> 响应里的 JSON 路径候选（按顺序取第一个非零值）
	Limits     Limits              `json:"limits,omitempty"`
}

// ModelRule 按上游模型名（正则）覆盖 codec / 支持性。
type ModelRule struct {
	Match     string `json:"match"`
	Codec     string `json:"codec,omitempty"`
	Supported *bool  `json:"supported,omitempty"`
	re        *regexp.Regexp
}

// Limits 是对用户可见的约束，会汇总到 GET /v1/catalog 的 limits。
type Limits struct {
	B64Only         bool     `json:"b64_only,omitempty"`         // 图像只返回 b64_json
	MaxFileBytes    int64    `json:"max_file_bytes,omitempty"`   // 上传文件大小上限
	ResponseFormats []string `json:"response_formats,omitempty"` // 支持的 response_format
	AudioFormats    []string `json:"audio_formats,omitempty"`    // TTS 实际输出的音频格式
}

// Errors 描述上游的错误表达方式。
type Errors struct {
	// BodyErrorField：HTTP 200 但响应体（或 SSE chunk）里带此顶层字段时视为失败（OpenRouter）。
	BodyErrorField string `json:"body_error_field,omitempty"`
	// InBandPatterns：200 响应文本中出现这些子串时视为配额 / 余额耗尽。
	InBandPatterns []string `json:"in_band_patterns,omitempty"`
	// ChallengeIsTransient：带 cf-mitigated: challenge 的 403 不当作 Key 失效。
	ChallengeIsTransient bool `json:"challenge_is_transient,omitempty"`
}

// Chat 是对话端点的行为差异。
type Chat struct {
	ThinkingDefault     map[string]any `json:"thinking_default,omitempty"`       // 客户端没传对应字段时补
	ForceSingleToolCall bool           `json:"force_single_tool_call,omitempty"` // 有 tools 时 parallel_tool_calls=false
	StrictMessages      bool           `json:"strict_messages,omitempty"`        // 消息只保留标准字段
	SingleSystemMessage bool           `json:"single_system_message,omitempty"`  // 多条 system 合并到首位
	MinMaxTokens        int            `json:"min_max_tokens,omitempty"`         // max_tokens 下限
}

// Transport 是传输层参数。
type Transport struct {
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
	TimeoutMs    int               `json:"timeout_ms,omitempty"` // 非流式请求的整体超时（含读响应体）
	Keyless      bool              `json:"keyless,omitempty"`    // 不发 Authorization
}

// Auth 是鉴权与 Key 校验方式。
type Auth struct {
	// Validation：Key 校验方式。models（默认，GET /models）/ url（GET 指定地址）/
	// chat_probe（1 token 的对话调用；适用于 /models 不鉴权的供应商）。
	Validation Validation `json:"validation,omitempty"`
	// AlternateHosts：主域名对 Key 返回 401/403 时依次尝试的备用 base_url（智谱国内站 / 国际站）。
	AlternateHosts []string `json:"alternate_hosts,omitempty"`
}

type Validation struct {
	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`
	Model  string `json:"model,omitempty"` // chat_probe 用的模型
}

// Catalog 描述模型列表接口。
type Catalog struct {
	ListPaths []string `json:"list_paths,omitempty"` // 默认 ["/models"]
}

//go:embed presets/*.json
var presetFS embed.FS

// presets 是内置预设的原始 JSON（按名称索引），init 时加载并校验。
var presets = map[string]json.RawMessage{}

func init() {
	entries, err := presetFS.ReadDir("presets")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		raw, err := presetFS.ReadFile("presets/" + e.Name())
		if err != nil {
			panic(err)
		}
		name := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		presets[name] = raw
	}
	for name := range presets {
		if _, err := Load(name, nil); err != nil {
			panic(fmt.Sprintf("dialect: built-in preset %s is invalid: %v", name, err))
		}
	}
}

// PresetNames 返回内置预设名（排序）。
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PresetJSON 返回内置预设的原始 JSON。
func PresetJSON(name string) (json.RawMessage, bool) {
	raw, ok := presets[name]
	return raw, ok
}

// Load 合并预设与覆盖并校验。preset 为空时取 override 里的 "preset" 字段；两者都没有
// 且 override 为空时返回 (nil, nil)，即「没有方言」。
func Load(preset string, override json.RawMessage) (*Dialect, error) {
	var ov map[string]any
	if len(bytes.TrimSpace(override)) > 0 && string(bytes.TrimSpace(override)) != "null" {
		if err := json.Unmarshal(override, &ov); err != nil {
			return nil, fmt.Errorf("dialect: override is not a JSON object: %w", err)
		}
	}
	if p, _ := ov["preset"].(string); p != "" {
		preset = p
	}
	if preset == "" && len(ov) == 0 {
		return nil, nil
	}
	merged := map[string]any{}
	if preset != "" {
		raw, ok := presets[preset]
		if !ok {
			return nil, fmt.Errorf("dialect: unknown preset %q", preset)
		}
		if err := json.Unmarshal(raw, &merged); err != nil {
			return nil, fmt.Errorf("dialect: preset %s: %w", preset, err)
		}
	}
	merged = deepMerge(merged, ov)
	merged["preset"] = preset

	buf, _ := json.Marshal(merged)
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	d := &Dialect{}
	if err := dec.Decode(d); err != nil {
		return nil, fmt.Errorf("dialect: %w", err)
	}
	if err := d.validate(); err != nil {
		return nil, err
	}
	return d, nil
}

// deepMerge 把 b 合并进 a：两边都是对象时递归，否则 b 覆盖 a（切片整体替换）。
func deepMerge(a, b map[string]any) map[string]any {
	if a == nil {
		a = map[string]any{}
	}
	for k, bv := range b {
		if bm, ok := bv.(map[string]any); ok {
			if am, ok := a[k].(map[string]any); ok {
				a[k] = deepMerge(am, bm)
				continue
			}
		}
		a[k] = bv
	}
	return a
}

func (d *Dialect) validate() error {
	for name, ep := range d.Endpoints {
		if !slices.Contains(endpointNames, name) {
			return fmt.Errorf("dialect: unknown endpoint %q (want one of %s)", name, strings.Join(endpointNames, ", "))
		}
		if ep == nil {
			continue
		}
		if ep.Codec != "" && !slices.Contains(KnownCodecs, ep.Codec) {
			return fmt.Errorf("dialect: endpoints.%s.codec %q is not a known codec", name, ep.Codec)
		}
		for _, t := range ep.Transforms {
			if !slices.Contains(KnownTransforms, t) {
				return fmt.Errorf("dialect: endpoints.%s.transforms: unknown transform %q", name, t)
			}
		}
		for i := range ep.ByModel {
			r := &ep.ByModel[i]
			re, err := regexp.Compile(r.Match)
			if err != nil {
				return fmt.Errorf("dialect: endpoints.%s.by_model[%d].match: %w", name, i, err)
			}
			r.re = re
			if r.Codec != "" && !slices.Contains(KnownCodecs, r.Codec) {
				return fmt.Errorf("dialect: endpoints.%s.by_model[%d].codec %q is not a known codec", name, i, r.Codec)
			}
		}
		if ep.URL != "" {
			if _, err := url.Parse(strings.ReplaceAll(ep.URL, "{origin}", "https://x")); err != nil {
				return fmt.Errorf("dialect: endpoints.%s.url: %w", name, err)
			}
		}
	}
	switch d.Auth.Validation.Method {
	case "", "models", "url", "chat_probe":
	default:
		return fmt.Errorf("dialect: auth.validation.method %q must be models, url or chat_probe", d.Auth.Validation.Method)
	}
	if d.Auth.Validation.Method == "url" && d.Auth.Validation.URL == "" {
		return fmt.Errorf("dialect: auth.validation.url is required when method is url")
	}
	return nil
}

// Endpoint 返回某端点的方言（可能为 nil）。对 nil 的 Dialect 安全。
func (d *Dialect) Endpoint(name string) *Endpoint {
	if d == nil {
		return nil
	}
	return d.Endpoints[name]
}

// CodecFor 返回某端点、某上游模型使用的 codec 名（空 = 协议默认）与显式的支持性
// （nil = 按协议默认）。by_model 规则按顺序匹配，先匹配者生效。
func (d *Dialect) CodecFor(endpoint, upstreamModel string) (codec string, supported *bool) {
	ep := d.Endpoint(endpoint)
	if ep == nil {
		return "", nil
	}
	codec, supported = ep.Codec, ep.Supported
	for _, r := range ep.ByModel {
		if r.re != nil && r.re.MatchString(upstreamModel) {
			if r.Codec != "" {
				codec = r.Codec
			}
			if r.Supported != nil {
				supported = r.Supported
			}
			break
		}
	}
	return codec, supported
}

// HasTransform 判断某端点是否声明了具名变换。
func (d *Dialect) HasTransform(endpoint, name string) bool {
	ep := d.Endpoint(endpoint)
	return ep != nil && slices.Contains(ep.Transforms, name)
}

// Origin 返回 base_url 的 scheme://host（用于 {origin} 模板）。
func Origin(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" {
		return strings.TrimRight(baseURL, "/")
	}
	return u.Scheme + "://" + u.Host
}

// ResolveURL 计算某端点的上游地址：方言 url（绝对模板）> path（相对 base_url）> 默认 path。
func (d *Dialect) ResolveURL(baseURL, endpoint, defaultPath string) string {
	base := strings.TrimRight(baseURL, "/")
	ep := d.Endpoint(endpoint)
	switch {
	case ep != nil && ep.URL != "":
		return strings.ReplaceAll(ep.URL, "{origin}", Origin(baseURL))
	case ep != nil && ep.Path != "":
		return base + ep.Path
	default:
		return base + defaultPath
	}
}

// UsagePaths 返回某计量项的用量字段路径候选（对 nil 安全）。
func (e *Endpoint) UsagePaths(meter string) []string {
	if e == nil {
		return nil
	}
	return e.Usage[meter]
}

// EndpointForModel 是模型类型 / 能力对应的方言端点名（与网关 relay 的端点 × 类型矩阵一致）。
func EndpointForModel(typ string, caps []string) string {
	switch typ {
	case "embedding":
		return EndpointEmbeddings
	case "rerank":
		return EndpointRerank
	case "image":
		return EndpointImages
	case "audio":
		if slices.Contains(caps, "asr") {
			return EndpointTranscriptions
		}
		return EndpointSpeech
	}
	return EndpointChat
}

// IsZero 判断限制是否为空。
func (l Limits) IsZero() bool {
	return !l.B64Only && l.MaxFileBytes == 0 && len(l.ResponseFormats) == 0 && len(l.AudioFormats) == 0
}

// MergeLimits 汇总一个模型所有渠道的限制，给出用户可能遇到的最严约束：布尔约束取或、
// 大小上限取最小、可选值列表取交集（只有声明了列表的渠道参与交集）。全部为空时返回 nil。
func MergeLimits(list []Limits) *Limits {
	var out Limits
	var formats, audio []string
	formatsSet, audioSet := false, false
	for _, l := range list {
		out.B64Only = out.B64Only || l.B64Only
		if l.MaxFileBytes > 0 && (out.MaxFileBytes == 0 || l.MaxFileBytes < out.MaxFileBytes) {
			out.MaxFileBytes = l.MaxFileBytes
		}
		if len(l.ResponseFormats) > 0 {
			formats, formatsSet = intersect(formats, l.ResponseFormats, formatsSet), true
		}
		if len(l.AudioFormats) > 0 {
			audio, audioSet = intersect(audio, l.AudioFormats, audioSet), true
		}
	}
	out.ResponseFormats, out.AudioFormats = formats, audio
	if out.IsZero() {
		return nil
	}
	return &out
}

func intersect(acc, next []string, started bool) []string {
	if !started {
		return append([]string(nil), next...)
	}
	var out []string
	for _, v := range acc {
		if slices.Contains(next, v) {
			out = append(out, v)
		}
	}
	return out
}
