// Command providercheck 是供应商能力认证工具（多供应商接口统一技术实施方案 WP8 / §9）：
// 对一个上游账号的每种能力做一次最小真实调用，输出能力矩阵与报告。
//
// 与网关线上路径完全一致：请求经 internal/adapter 的同一套方言应用与 codec 构造、
// 解析，所以这里通过的能力在网关上也能用，这里失败的能力需要调整方言或补 codec。
//
// 用法：
//
//	go run ./cmd/providercheck -provider dashscope -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
//	    -key-env DASHSCOPE_API_KEY -preset dashscope \
//	    -models chat=qwen-flash,vlm=qwen3-vl-flash,embedding=text-embedding-v4,rerank=qwen3-rerank,image=qwen-image-3.0,tts=qwen3-tts-flash,asr=qwen3-asr-flash \
//	    -out reports/dashscope.json
//
//	go run ./cmd/providercheck -matrix reports -matrix-out docs/provider-capability-matrix.md
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
)

// Check 是一项能力的认证结果。
type Check struct {
	Capability string `json:"capability"`
	Model      string `json:"model"`
	Status     string `json:"status"` // pass / fail / skip / unsupported
	LatencyMs  int64  `json:"latency_ms"`
	Detail     string `json:"detail"`
	Usage      any    `json:"usage,omitempty"`
}

// Report 是一次认证的完整报告（-out 写成 JSON，-matrix 汇总）。
type Report struct {
	Provider  string            `json:"provider"`
	BaseURL   string            `json:"base_url"`
	Protocol  string            `json:"protocol,omitempty"`
	Preset    string            `json:"preset"`
	Override  json.RawMessage   `json:"dialect_override,omitempty"`
	KeyEnv    string            `json:"key_env,omitempty"` // 巡检时从这个环境变量取 Key（报告里从不存 Key）
	Voice     string            `json:"voice,omitempty"`
	ModelMap  map[string]string `json:"models,omitempty"`
	CheckedAt time.Time         `json:"checked_at"`
	KeyValid  *Check            `json:"key_valid"`
	Models    *ModelListCheck   `json:"model_list"`
	Checks    []Check           `json:"checks"`
}

// ModelListCheck 是模型列表探测结果：列表里找不到的被测模型单独列出（可能地区受限或未开通）。
type ModelListCheck struct {
	Total   int            `json:"total"`
	Paths   map[string]int `json:"paths"`
	Missing []string       `json:"missing"`
}

// capabilities 是认证的能力顺序（与 tests/gateway_py 的档案键一致）。
var capabilities = []string{"chat", "chat_stream", "tools", "vlm", "embedding", "rerank", "image", "tts", "asr"}

// options 是一次认证的输入（命令行或已有报告）。
type options struct {
	provider, baseURL, protocol, preset, keyEnv, voice string
	key                                                string
	override                                           json.RawMessage
	models                                             map[string]string
	only                                               map[string]bool // 非空时只测这些能力
	timeout                                            time.Duration
}

func main() {
	var (
		provider  = flag.String("provider", "", "供应商 code（写进报告）")
		baseURL   = flag.String("base-url", "", "上游 base_url，如 https://api.siliconflow.cn/v1")
		key       = flag.String("key", "", "上游 Key（优先用 -key-env）")
		keyEnv    = flag.String("key-env", "", "从环境变量读取上游 Key")
		protocol  = flag.String("protocol", "openai", "协议：openai / anthropic / gemini")
		preset    = flag.String("preset", "", "方言预设名（见 internal/dialect/presets）")
		override  = flag.String("dialect", "", "方言覆盖（JSON），与预设深合并")
		models    = flag.String("models", "", "能力=模型，逗号分隔：chat=,vlm=,embedding=,rerank=,image=,tts=,asr=")
		voice     = flag.String("voice", "alloy", "语音合成用的音色")
		timeout   = flag.Duration("timeout", 120*time.Second, "单次调用超时")
		only      = flag.String("only", "", "只测这些能力（逗号分隔），巡检时用来控制成本，如 chat,chat_stream,embedding")
		out       = flag.String("out", "", "把报告写成 JSON 文件")
		matrix    = flag.String("matrix", "", "汇总目录下所有报告 JSON，输出能力矩阵（Markdown）")
		matrixOut = flag.String("matrix-out", "", "能力矩阵输出文件（默认打印到标准输出）")
		canary    = flag.String("canary", "", "巡检：按目录下每份报告记录的参数重测（Key 取报告的 key_env），回写报告；有能力由通过变为失败时退出码为 2")
	)
	flag.Parse()

	onlySet := map[string]bool{}
	for _, c := range strings.Split(*only, ",") {
		if c = strings.TrimSpace(c); c != "" {
			onlySet[c] = true
		}
	}

	if *canary != "" {
		regressions, err := runCanary(*canary, onlySet, *timeout)
		if err != nil {
			fail(err)
		}
		if *matrixOut != "" {
			if err := writeMatrix(*canary, *matrixOut); err != nil {
				fail(err)
			}
		}
		if regressions > 0 {
			fmt.Fprintf(os.Stderr, "providercheck: %d 项能力由通过变为失败\n", regressions)
			os.Exit(2)
		}
		return
	}
	if *matrix != "" {
		if err := writeMatrix(*matrix, *matrixOut); err != nil {
			fail(err)
		}
		return
	}
	if *keyEnv != "" {
		*key = os.Getenv(*keyEnv)
	}
	if *baseURL == "" || *key == "" {
		fail(errors.New("需要 -base-url 与 -key / -key-env"))
	}
	rep, err := certify(options{
		provider: *provider, baseURL: *baseURL, protocol: *protocol, preset: *preset, keyEnv: *keyEnv, voice: *voice,
		key: *key, override: json.RawMessage(*override), models: parseModels(*models), only: onlySet, timeout: *timeout,
	})
	if err != nil {
		fail(err)
	}
	printReport(rep)
	if *out != "" {
		if err := writeReport(*out, rep); err != nil {
			fail(err)
		}
		fmt.Println("报告已写入", *out)
	}
}

// certify 对一个上游账号做一次完整（或 -only 指定的）认证。
func certify(o options) (*Report, error) {
	if len(o.override) == 0 {
		o.override = nil
	}
	d, err := dialect.Load(o.preset, o.override)
	if err != nil {
		return nil, err
	}
	if o.protocol == "" {
		o.protocol = "openai"
	}
	if o.voice == "" {
		o.voice = "alloy"
	}
	c := &checker{
		http:   &http.Client{Timeout: o.timeout},
		acct:   &catalog.ProviderAccount{ProviderCode: o.provider, Protocol: o.protocol, BaseURL: o.baseURL, Dialect: d},
		key:    &catalog.ProviderKey{Secret: o.key},
		voice:  o.voice,
		models: o.models,
	}
	adp, ok := adapter.NewRegistry().For(o.protocol)
	if !ok {
		return nil, fmt.Errorf("未知协议 %q", o.protocol)
	}
	c.adp = adp

	rep := &Report{Provider: o.provider, BaseURL: o.baseURL, Protocol: o.protocol, Preset: o.preset, Override: o.override,
		KeyEnv: o.keyEnv, Voice: o.voice, ModelMap: o.models, CheckedAt: time.Now().UTC()}
	ctx := context.Background()
	rep.KeyValid = c.validateKey(ctx)
	rep.Models = c.listModels(ctx)
	for _, capName := range capabilities {
		// asr 用 tts 的产出作输入：只测 asr 时也要先跑 tts
		if len(o.only) > 0 && !o.only[capName] && !(capName == "tts" && o.only["asr"]) {
			continue
		}
		rep.Checks = append(rep.Checks, c.run(ctx, capName))
	}
	return rep, nil
}

func writeReport(path string, rep *Report) error {
	raw, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// runCanary 是每日巡检（实施方案 §9.4）：按目录下每份报告记录的参数重测，回写报告，
// 返回「上次通过、这次失败」的能力数。没有 Key（环境变量未设置）的供应商跳过。
// -only 只重测部分能力时，其余能力保留上次的结果。
func runCanary(dir string, only map[string]bool, timeout time.Duration) (int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return 0, err
	}
	regressions := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return regressions, err
		}
		var prev Report
		if err := json.Unmarshal(raw, &prev); err != nil {
			return regressions, fmt.Errorf("%s: %w", f, err)
		}
		keyEnv := firstNonEmpty(prev.KeyEnv, strings.ToUpper(prev.Provider)+"_API_KEY")
		key := os.Getenv(keyEnv)
		if key == "" {
			fmt.Printf("== %s：跳过（未设置 %s）\n", prev.Provider, keyEnv)
			continue
		}
		models := prev.ModelMap
		if len(models) == 0 { // 旧报告：从检查项还原能力 → 模型
			models = map[string]string{}
			for _, c := range prev.Checks {
				if c.Model != "" && c.Capability != "chat_stream" && c.Capability != "tools" {
					models[c.Capability] = c.Model
				}
			}
		}
		rep, err := certify(options{provider: prev.Provider, baseURL: prev.BaseURL, protocol: prev.Protocol, preset: prev.Preset,
			override: prev.Override, keyEnv: keyEnv, voice: prev.Voice, key: key, models: models, only: only, timeout: timeout})
		if err != nil {
			return regressions, fmt.Errorf("%s: %w", f, err)
		}
		before := map[string]Check{}
		for _, c := range prev.Checks {
			before[c.Capability] = c
		}
		if prev.KeyValid != nil && prev.KeyValid.Status == "pass" && rep.KeyValid.Status == "fail" {
			regressions++
			fmt.Printf("!! %s Key 校验由通过变为失败：%s\n", rep.Provider, rep.KeyValid.Detail)
		}
		ran := map[string]bool{}
		for _, c := range rep.Checks {
			ran[c.Capability] = true
			if before[c.Capability].Status == "pass" && c.Status == "fail" {
				regressions++
				fmt.Printf("!! %s %s（%s）由通过变为失败：%s\n", rep.Provider, c.Capability, c.Model, c.Detail)
			}
		}
		if len(only) > 0 { // 没重测的能力保留上次结果，按固定顺序排列
			var merged []Check
			for _, capName := range capabilities {
				if ran[capName] {
					for _, c := range rep.Checks {
						if c.Capability == capName {
							merged = append(merged, c)
						}
					}
				} else if c, ok := before[capName]; ok {
					merged = append(merged, c)
				}
			}
			rep.Checks = merged
		}
		printReport(rep)
		if err := writeReport(f, rep); err != nil {
			return regressions, err
		}
	}
	return regressions, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "providercheck:", err)
	os.Exit(1)
}

func parseModels(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if ok && v != "" {
			out[k] = v
		}
	}
	return out
}

type checker struct {
	http      *http.Client
	adp       adapter.Adapter
	acct      *catalog.ProviderAccount
	key       *catalog.ProviderKey
	voice     string
	models    map[string]string
	lastAudio []byte // tts 检查产出的音频，供 asr 检查作为输入（capabilities 中 tts 在 asr 之前）
}

func (c *checker) target(model string) adapter.Target {
	return adapter.Target{Channel: &catalog.Channel{UpstreamModel: model}, Account: c.acct, Key: c.key}
}

// validateKey 按方言 auth.validation 校验 Key：models（GET /models）/ url / chat_probe。
// 注意部分供应商的 /models 不鉴权（对无效 Key 也返回 200），必须在方言里改用 url 或 chat_probe。
func (c *checker) validateKey(ctx context.Context) *Check {
	var v dialect.Validation
	if c.acct.Dialect != nil {
		v = c.acct.Dialect.Auth.Validation
	}
	ch := &Check{Capability: "key", Status: "pass"}
	start := time.Now()
	var resp *http.Response
	var err error
	switch v.Method {
	case "chat_probe":
		model := v.Model
		if model == "" {
			model = c.models["chat"]
		}
		req, berr := c.adp.BuildRequest(ctx, c.target(model), adapter.EndpointChat,
			map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "max_tokens": 1.0})
		if berr != nil {
			return &Check{Capability: "key", Status: "fail", Detail: berr.Error()}
		}
		resp, err = c.http.Do(req)
	default:
		url := strings.TrimRight(c.acct.BaseURL, "/") + "/models"
		if v.Method == "url" {
			url = strings.ReplaceAll(v.URL, "{origin}", dialect.Origin(c.acct.BaseURL))
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+c.key.Secret)
		resp, err = c.http.Do(req)
	}
	ch.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		ch.Status, ch.Detail = "fail", err.Error()
		return ch
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		ch.Status, ch.Detail = "fail", fmt.Sprintf("HTTP %d %s", resp.StatusCode, oneLine(body))
	} else {
		ch.Detail = fmt.Sprintf("HTTP %d via %s", resp.StatusCode, firstNonEmpty(v.Method, "models"))
	}
	return ch
}

// listModels 探测模型列表接口（方言 catalog.list_paths，默认 /models），并找出被测模型
// 中不在列表里的（可能是地区受限、未开通，或供应商不列出这类模型）。
func (c *checker) listModels(ctx context.Context) *ModelListCheck {
	paths := []string{"/models"}
	if c.acct.Dialect != nil && len(c.acct.Dialect.Catalog.ListPaths) > 0 {
		paths = c.acct.Dialect.Catalog.ListPaths
	}
	out := &ModelListCheck{Paths: map[string]int{}, Missing: []string{}}
	ids := map[string]bool{}
	for _, p := range paths {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.acct.BaseURL, "/")+p, nil)
		req.Header.Set("Authorization", "Bearer "+c.key.Secret)
		resp, err := c.http.Do(req)
		if err != nil {
			out.Paths[p] = -1
			continue
		}
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&body)
		resp.Body.Close()
		out.Paths[p] = len(body.Data)
		for _, m := range body.Data {
			ids[m.ID] = true
		}
	}
	out.Total = len(ids)
	seen := map[string]bool{}
	for _, m := range c.models {
		if !ids[m] && !seen[m] {
			out.Missing = append(out.Missing, m)
			seen[m] = true
		}
	}
	sort.Strings(out.Missing)
	return out
}

// run 执行一项能力的认证。
func (c *checker) run(ctx context.Context, capName string) Check {
	modelKey := capName
	switch capName {
	case "chat_stream", "tools":
		modelKey = "chat"
	}
	model := c.models[modelKey]
	ch := Check{Capability: capName, Model: model}
	if model == "" {
		ch.Status, ch.Detail = "skip", "未指定模型"
		return ch
	}
	endpoint := map[string]string{
		"chat": adapter.EndpointChat, "chat_stream": adapter.EndpointChat, "tools": adapter.EndpointChat, "vlm": adapter.EndpointChat,
		"embedding": adapter.EndpointEmbeddings, "rerank": adapter.EndpointRerank, "image": adapter.EndpointImages,
		"tts": adapter.EndpointSpeech, "asr": adapter.EndpointTranscriptions,
	}[capName]
	if !adapter.Serves(c.adp, c.acct, endpoint, model) {
		ch.Status, ch.Detail = "unsupported", "协议或方言声明不支持该端点"
		return ch
	}
	start := time.Now()
	detail, usage, err := c.call(ctx, capName, endpoint, model)
	ch.LatencyMs = time.Since(start).Milliseconds()
	ch.Usage = usage
	if err != nil {
		ch.Status, ch.Detail = "fail", err.Error()
	} else {
		ch.Status, ch.Detail = "pass", detail
	}
	return ch
}

func (c *checker) call(ctx context.Context, capName, endpoint, model string) (string, any, error) {
	t := c.target(model)
	user := func(content any) []any { return []any{map[string]any{"role": "user", "content": content}} }
	switch capName {
	case "chat", "tools", "vlm":
		body := map[string]any{"messages": user("用一句话介绍你自己。"), "max_tokens": 256.0}
		if capName == "tools" {
			body["messages"] = user("北京现在天气怎么样？请调用工具查询。")
			body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{
				"name": "get_weather", "parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []any{"city"}}}}}
		}
		if capName == "vlm" {
			body["messages"] = user([]any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://sf-maas-uat-prod.oss-cn-shanghai.aliyuncs.com/dog.png"}},
				map[string]any{"type": "text", "text": "图片里是什么动物？"},
			})
		}
		raw, err := c.do(ctx, t, endpoint, body)
		if err != nil {
			return "", nil, err
		}
		m, usage, err := c.adp.DecodeResponse(raw, model, "providercheck")
		if err != nil {
			return "", nil, err
		}
		msg, _ := pick(m, "choices.0.message").(map[string]any)
		if capName == "tools" {
			calls, _ := msg["tool_calls"].([]any)
			if len(calls) == 0 {
				return "", usage, errors.New("模型没有发起工具调用")
			}
			return fmt.Sprintf("tool_calls=%d", len(calls)), usage, nil
		}
		content, _ := msg["content"].(string)
		if strings.TrimSpace(content) == "" {
			return "", usage, errors.New("content 为空（推理模型可能把 max_tokens 全部用于推理）")
		}
		return trim(content, 60), usage, nil

	case "chat_stream":
		req, err := c.adp.BuildRequest(ctx, t, endpoint, map[string]any{"messages": user("你好"), "max_tokens": 64.0, "stream": true})
		if err != nil {
			return "", nil, err
		}
		resp, err := c.send(req)
		if err != nil {
			return "", nil, err
		}
		defer resp.Body.Close()
		dec := c.adp.NewStreamDecoder(resp.Body, model, "providercheck")
		chunks := 0
		for {
			_, err := dec.Next()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					return "", nil, err
				}
				break
			}
			chunks++
		}
		u := dec.Usage()
		if u.IsZero() {
			return "", nil, fmt.Errorf("%d 个 chunk，但流式响应没有返回 usage（计费会退化为估算）", chunks)
		}
		return fmt.Sprintf("%d chunks, usage ok", chunks), u, nil

	case "embedding":
		raw, err := c.do(ctx, t, endpoint, map[string]any{"input": []any{"猫", "汽车"}})
		if err != nil {
			return "", nil, err
		}
		m, usage, err := c.adp.DecodeResponse(raw, model, "providercheck")
		if err != nil {
			return "", nil, err
		}
		vec, _ := pick(m, "data.0.embedding").([]any)
		if len(vec) == 0 {
			return "", usage, errors.New("没有返回向量")
		}
		return fmt.Sprintf("dim=%d", len(vec)), usage, nil
	}

	// 非对话端点：走 codec（与网关一致）
	call := &adapter.Call{Endpoint: endpoint}
	switch capName {
	case "rerank":
		call.JSON = map[string]any{"query": "苹果手机", "documents": []any{"iPhone 发布会", "苹果富含维生素"}, "top_n": 2.0}
	case "image":
		call.JSON = map[string]any{"prompt": "一只橘猫，水彩风格", "n": 1.0, "size": "1024x1024"}
	case "tts":
		call.JSON = map[string]any{"input": "今天天气很好，我们一起去公园散步吧。", "voice": c.voice, "response_format": "mp3"}
	case "asr":
		audio, err := c.ttsSample(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("需要先合成一段语音作为输入：%w", err)
		}
		call.Form = &adapter.Form{Filename: "sample.mp3", FileType: "audio/mpeg", File: audio, Fields: [][2]string{{"language", "zh"}}}
	}
	codec := adapter.CodecFor(t, endpoint)
	req, err := codec.Build(ctx, t, call)
	if err != nil {
		return "", nil, err
	}
	resp, err := c.send(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if ie, perr := adapter.PeekInBandError(t, resp, 32<<20); perr == nil && ie != nil {
		return "", nil, ie
	}
	res, err := codec.Decode(ctx, resp, t, call, adapter.Env{HTTP: c.http, MaxBody: 32 << 20})
	if err != nil {
		return "", nil, err
	}
	switch res.Kind {
	case adapter.ResultJSON:
		return fmt.Sprintf("codec=%s %s", codec.Name(), trim(compactJSON(res.JSON), 80)), res.Usage, nil
	case adapter.ResultStream:
		n, _ := io.Copy(io.Discard, res.Stream)
		res.Stream.Close()
		return fmt.Sprintf("codec=%s %s %d bytes", codec.Name(), res.ContentType, n), res.Usage, nil
	default:
		if strings.HasPrefix(res.ContentType, "audio/") {
			if len(res.Body) < 1024 {
				return "", nil, fmt.Errorf("音频过短（%d 字节）", len(res.Body))
			}
			c.lastAudio = res.Body
			return fmt.Sprintf("codec=%s %s %d bytes", codec.Name(), res.ContentType, len(res.Body)), res.Usage, nil
		}
		return fmt.Sprintf("codec=%s %s", codec.Name(), trim(string(res.Body), 80)), res.Usage, nil
	}
}

func (c *checker) ttsSample(ctx context.Context) ([]byte, error) {
	if len(c.lastAudio) > 0 {
		return c.lastAudio, nil
	}
	return nil, errors.New("tts 检查没有产出音频（未指定 tts 模型或 tts 失败）")
}

func (c *checker) do(ctx context.Context, t adapter.Target, endpoint string, body map[string]any) ([]byte, error) {
	req, err := c.adp.BuildRequest(ctx, t, endpoint, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if ie, perr := adapter.PeekInBandError(t, resp, 32<<20); perr == nil && ie != nil {
		return nil, ie
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// send 发请求；非 2xx 时读出错误体作为错误返回。
func (c *checker) send(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d %s (%s)", resp.StatusCode, oneLine(body), req.URL.Path)
	}
	return resp, nil
}

func pick(v any, path string) any {
	cur := v
	for _, k := range strings.Split(path, ".") {
		switch x := cur.(type) {
		case map[string]any:
			cur = x[k]
		case []any:
			var i int
			if _, err := fmt.Sscanf(k, "%d", &i); err != nil || i >= len(x) {
				return nil
			}
			cur = x[i]
		default:
			return nil
		}
	}
	return cur
}

func compactJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func oneLine(b []byte) string { return trim(strings.Join(strings.Fields(string(b)), " "), 200) }

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var statusMark = map[string]string{"pass": "✅", "fail": "❌", "skip": "—", "unsupported": "⛔"}

func printReport(r *Report) {
	fmt.Printf("供应商 %s（%s，预设 %s）\n", r.Provider, r.BaseURL, firstNonEmpty(r.Preset, "无"))
	fmt.Printf("  Key 校验: %s %s\n", statusMark[r.KeyValid.Status], r.KeyValid.Detail)
	fmt.Printf("  模型列表: 共 %d 个 %v；被测模型不在列表中: %v\n", r.Models.Total, r.Models.Paths, r.Models.Missing)
	for _, ch := range r.Checks {
		fmt.Printf("  %-12s %s %-40s %6dms  %s\n", ch.Capability, statusMark[ch.Status], ch.Model, ch.LatencyMs, ch.Detail)
	}
}

// writeMatrix 汇总目录下所有报告，输出「供应商 × 能力」矩阵（Markdown）。
func writeMatrix(dir, outPath string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	var reports []Report
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var r Report
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		reports = append(reports, r)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Provider < reports[j].Provider })
	var b bytes.Buffer
	b.WriteString("# 供应商能力矩阵\n\n> 由 `go run ./cmd/providercheck -matrix` 根据认证报告自动生成，勿手改。\n")
	b.WriteString("> ✅ 通过　❌ 失败　⛔ 方言 / 协议声明不支持　— 未测\n\n")
	b.WriteString("| 供应商 | 认证时间 | Key | " + strings.Join(capabilities, " | ") + " |\n|---|---|---|" + strings.Repeat("---|", len(capabilities)) + "\n")
	for _, r := range reports {
		row := []string{r.Provider, r.CheckedAt.Format("2006-01-02"), statusMark[r.KeyValid.Status]}
		byCap := map[string]string{}
		for _, ch := range r.Checks {
			byCap[ch.Capability] = statusMark[ch.Status]
		}
		for _, c := range capabilities {
			row = append(row, firstNonEmpty(byCap[c], "—"))
		}
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	b.WriteString("\n## 失败明细\n\n")
	for _, r := range reports {
		for _, ch := range r.Checks {
			if ch.Status == "fail" {
				fmt.Fprintf(&b, "- **%s / %s**（%s）：%s\n", r.Provider, ch.Capability, ch.Model, ch.Detail)
			}
		}
	}
	if outPath == "" {
		fmt.Print(b.String())
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, b.Bytes(), 0o644)
}
