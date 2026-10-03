package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 展示元数据自动填充：按虚拟模型已有的数据给出 display_name / provider_display /
// description / tags 的建议值。数据来源按优先级：
//   - 价格同步抓到的外部目录参数（pending_model_listings.observed_meta，OpenRouter 的
//     name 形如 "DeepSeek: DeepSeek V3"，并带 description）；
//   - 模型名前缀（OpenRouter 的作者 slug，如 deepseek/、meta-llama/）或模型名首段
//     （gpt-、qwen-…）对应的厂商名；
//   - 模型名本身（人类可读化）与 type / capabilities。
// 只给建议、不落库；落库只有两处：运营在后台点「自动填充」后自行保存，以及
// EnsureVirtualModelMetadata（上架时元数据记录还不存在才插入，绝不覆盖）。
// 评分不在这里：由评测榜单发布时投影写入（ApplyScoreProjection）。

// MetadataSuggestionSource 是建议值的来源，供前端展示角标、审计追溯。
const (
	SuggestSourceExternal = "external" // 外部目录（OpenRouter / models.dev 等），Detail 里是抓取器名
	SuggestSourceVendor   = "vendor"   // 内置厂商表
	SuggestSourceDerived  = "derived"  // 由模型名 / 类型 / 能力推导
	SuggestSourceLLM      = "llm"      // LLM 生成（仅介绍文案），Detail 里是所用模型名
)

// SuggestedText 是一个文本字段的建议值；Value 为空表示没有可用建议。
type SuggestedText struct {
	Value  string `json:"value"`
	Source string `json:"source,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// SuggestedTags 是标签的建议值。
type SuggestedTags struct {
	Value  []string `json:"value"`
	Source string   `json:"source,omitempty"`
}

// MetadataSuggestion 是 GET /virtual-models/{id}/metadata/suggestion 的响应。
type MetadataSuggestion struct {
	VirtualModelID  int64         `json:"virtual_model_id"`
	DisplayName     SuggestedText `json:"display_name"`
	ProviderDisplay SuggestedText `json:"provider_display"`
	Description     SuggestedText `json:"description"`
	Tags            SuggestedTags `json:"tags"`
	// LLMAvailable：服务端配置了 LLM，可以用 ?llm=1 生成介绍文案。
	LLMAvailable bool `json:"llm_available"`
}

// SuggestOptions 控制建议值的生成方式。
type SuggestOptions struct {
	// UseLLM：用 LLM 生成介绍文案（结合外部目录原文与模型事实）。未配置 LLM 返回
	// ErrLLMNotConfigured，调用失败返回 ErrLLMUnavailable。
	UseLLM bool
}

// observedMeta 是 pricesync.ModelMeta 里这里用到的字段（admin 不能反向依赖 pricesync）。
type observedMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// SuggestVirtualModelMetadata 给出一个虚拟模型展示元数据的建议值（只读）。
func (s *Service) SuggestVirtualModelMetadata(ctx context.Context, vmID int64, opts SuggestOptions) (*MetadataSuggestion, error) {
	if opts.UseLLM && s.descGen == nil {
		return nil, ErrLLMNotConfigured
	}
	vm := &VirtualModel{ID: vmID}
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT name, family, type, context_window, max_output, capabilities FROM virtual_models WHERE id = $1`, vmID,
	).Scan(&vm.Name, &vm.Family, &vm.Type, &vm.ContextWindow, &vm.MaxOutput, &vm.Capabilities); err != nil {
		if isNoRows(err) {
			return nil, ErrVirtualModelNotFound
		}
		return nil, fmt.Errorf("admin: load virtual model for metadata suggestion: %w", err)
	}
	meta, err := s.observedMetaFor(ctx, vm)
	if err != nil {
		return nil, err
	}
	sug := BuildMetadataSuggestion(vm, meta)
	sug.LLMAvailable = s.descGen != nil
	if opts.UseLLM {
		in := ModelDescriptionInput{
			Name: vm.Name, DisplayName: sug.DisplayName.Value, Vendor: sug.ProviderDisplay.Value, Type: vm.Type,
			Capabilities: vm.Capabilities, ContextWindow: vm.ContextWindow, MaxOutput: vm.MaxOutput,
		}
		if meta != nil {
			in.ExternalDescription = meta.Description
		}
		d, err := s.descGen.GenerateModelDescription(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrLLMUnavailable, err)
		}
		sug.Description = SuggestedText{Value: d, Source: SuggestSourceLLM, Detail: s.descGen.ModelName()}
	}
	return &sug, nil
}

// observedMetaFor 找与虚拟模型对应的外部目录参数：优先本模型上架时的那条候选，其次按
// 模型名 / 渠道上游模型名匹配的候选；带 description 的优先。没有返回 nil。
func (s *Service) observedMetaFor(ctx context.Context, vm *VirtualModel) (*observedMeta, error) {
	var raw []byte
	err := s.db(ctx).QueryRow(ctx,
		`SELECT l.observed_meta FROM pending_model_listings l
		 WHERE l.observed_meta IS NOT NULL
		   AND (l.published_virtual_model_id = $1 OR l.upstream_model = $2
		        OR l.upstream_model IN (SELECT upstream_model FROM channels WHERE virtual_model_id = $1))
		 ORDER BY (l.published_virtual_model_id IS NOT DISTINCT FROM $1) DESC,
		          (COALESCE(l.observed_meta->>'description', '') <> '') DESC,
		          l.last_observed_at DESC
		 LIMIT 1`,
		vm.ID, vm.Name,
	).Scan(&raw)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("admin: load observed meta for metadata suggestion: %w", err)
	}
	var m observedMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil // 脏数据不影响其他来源的建议
	}
	return &m, nil
}

// EnsureVirtualModelMetadata 在虚拟模型还没有展示元数据记录时，按建议值插入一条（评分留空）；
// 已有记录（哪怕是运营清空过的）一律不动。返回是否插入了。供上架流程调用，可加入调用方事务。
func (s *Service) EnsureVirtualModelMetadata(ctx context.Context, vmID int64) (bool, error) {
	sug, err := s.SuggestVirtualModelMetadata(ctx, vmID, SuggestOptions{})
	if err != nil {
		return false, err
	}
	tags := sug.Tags.Value
	if tags == nil {
		tags = []string{}
	}
	tag, err := s.db(ctx).Exec(ctx,
		`INSERT INTO virtual_model_metadata (virtual_model_id, display_name, description, provider_display, tags, updated_at)
		 VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, now())
		 ON CONFLICT (virtual_model_id) DO NOTHING`,
		vmID, sug.DisplayName.Value, sug.Description.Value, sug.ProviderDisplay.Value, tags,
	)
	if err != nil {
		return false, fmt.Errorf("admin: insert suggested virtual_model_metadata: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MetadataAutofillResult 是单个虚拟模型批量自动填充的结果。
type MetadataAutofillResult struct {
	VirtualModelID int64  `json:"virtual_model_id"`
	Name           string `json:"name"`
	// Changes 是将要（dry_run）或已经写入的字段：只含原本为空、且有建议值的字段。
	// 键为 display_name / provider_display / description / tags。
	Changes map[string]any `json:"changes"`
	Applied bool           `json:"applied"`
}

// AutofillVirtualModelMetadata 用建议值补齐一个虚拟模型展示元数据里的空字段（不用 LLM、
// 不碰评分、不覆盖已有内容）。dryRun 时只计算不写入。可加入调用方事务。
func (s *Service) AutofillVirtualModelMetadata(ctx context.Context, vmID int64, dryRun bool) (*MetadataAutofillResult, error) {
	sug, err := s.SuggestVirtualModelMetadata(ctx, vmID, SuggestOptions{})
	if err != nil {
		return nil, err
	}
	cur, err := s.GetVirtualModelMetadata(ctx, vmID)
	if err != nil {
		return nil, err
	}
	res := &MetadataAutofillResult{VirtualModelID: vmID, Changes: map[string]any{}}
	if err := s.db(ctx).QueryRow(ctx, `SELECT name FROM virtual_models WHERE id = $1`, vmID).Scan(&res.Name); err != nil {
		return nil, fmt.Errorf("admin: load virtual model name: %w", err)
	}
	empty := func(p *string) bool { return p == nil || strings.TrimSpace(*p) == "" }
	if cur == nil {
		cur = &VirtualModelMetadata{}
	}
	if empty(cur.DisplayName) && sug.DisplayName.Value != "" {
		res.Changes["display_name"] = sug.DisplayName.Value
	}
	if empty(cur.ProviderDisplay) && sug.ProviderDisplay.Value != "" {
		res.Changes["provider_display"] = sug.ProviderDisplay.Value
	}
	if empty(cur.Description) && sug.Description.Value != "" {
		res.Changes["description"] = sug.Description.Value
	}
	if len(cur.Tags) == 0 && len(sug.Tags.Value) > 0 {
		res.Changes["tags"] = sug.Tags.Value
	}
	if dryRun || len(res.Changes) == 0 {
		return res, nil
	}
	str := func(k string) string { v, _ := res.Changes[k].(string); return v }
	tags, _ := res.Changes["tags"].([]string)
	if tags == nil {
		tags = []string{}
	}
	// 只补空字段：已有值（非空）一律保留；评分列不出现在语句里，原样保留。
	if _, err := s.db(ctx).Exec(ctx,
		`INSERT INTO virtual_model_metadata (virtual_model_id, display_name, description, provider_display, tags, updated_at)
		 VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, now())
		 ON CONFLICT (virtual_model_id) DO UPDATE SET
		   display_name = COALESCE(NULLIF(btrim(virtual_model_metadata.display_name), ''), EXCLUDED.display_name),
		   description = COALESCE(NULLIF(btrim(virtual_model_metadata.description), ''), EXCLUDED.description),
		   provider_display = COALESCE(NULLIF(btrim(virtual_model_metadata.provider_display), ''), EXCLUDED.provider_display),
		   tags = CASE WHEN cardinality(virtual_model_metadata.tags) = 0 THEN EXCLUDED.tags ELSE virtual_model_metadata.tags END,
		   updated_at = now()`,
		vmID, str("display_name"), str("description"), str("provider_display"), tags,
	); err != nil {
		return nil, fmt.Errorf("admin: autofill virtual_model_metadata: %w", err)
	}
	res.Applied = true
	return res, nil
}

// BuildMetadataSuggestion 是建议值的纯计算部分（meta 可为 nil）。
func BuildMetadataSuggestion(vm *VirtualModel, meta *observedMeta) MetadataSuggestion {
	sug := MetadataSuggestion{VirtualModelID: vm.ID}
	extSource := ""
	extVendor, extName := "", ""
	if meta != nil {
		extSource = meta.Source
		extVendor, extName = splitVendorPrefix(cleanExternalName(meta.Name))
	}

	// 厂商展示名：内置厂商表 > 外部目录名称前缀 > 模型名前缀
	switch {
	case vendorFromModelName(vm.Name) != "":
		sug.ProviderDisplay = SuggestedText{Value: vendorFromModelName(vm.Name), Source: SuggestSourceVendor}
	case extVendor != "":
		sug.ProviderDisplay = SuggestedText{Value: extVendor, Source: SuggestSourceExternal, Detail: extSource}
	default:
		if prefix, _, ok := strings.Cut(vm.Name, "/"); ok && prefix != "" {
			sug.ProviderDisplay = SuggestedText{Value: humanizeModelID(prefix), Source: SuggestSourceDerived}
		}
	}

	// 展示名称：外部目录名称（去掉厂商前缀）> 模型名可读化
	if extName != "" {
		sug.DisplayName = SuggestedText{Value: extName, Source: SuggestSourceExternal, Detail: extSource}
	} else if v := humanizeModelID(modelBaseName(vm.Name)); v != "" {
		sug.DisplayName = SuggestedText{Value: v, Source: SuggestSourceDerived}
	}

	// 介绍文案：只有外部目录给了才有（不编造）
	if meta != nil {
		if d := cleanDescription(meta.Description); d != "" {
			sug.Description = SuggestedText{Value: d, Source: SuggestSourceExternal, Detail: extSource}
		}
	}

	sug.Tags = SuggestedTags{Value: suggestTags(vm), Source: SuggestSourceDerived}
	return sug
}

// suggestTags 由 type / capabilities / 模型名推导标签。非对话类型的标签与 web 模型库
// （frontend/web/src/data/models.ts catalogTypeTags）用同一套名字，前端会去重。
func suggestTags(vm *VirtualModel) []string {
	tags := []string{}
	add := func(t string) {
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	switch vm.Type {
	case "embedding":
		add("embedding")
	case "rerank":
		add("rerank")
	case "image":
		add("image-generation")
	case "audio":
		if slices.Contains(vm.Capabilities, "asr") {
			add("transcription")
		} else {
			add("voice")
		}
	}
	if slices.Contains(vm.Capabilities, "reasoning") {
		add("reasoning")
	}
	lower := strings.ToLower(modelBaseName(vm.Name))
	for _, kw := range []string{"coder", "codestral", "devstral", "codex", "-code"} {
		if strings.Contains(lower, kw) {
			add("coding")
			break
		}
	}
	if slices.Contains(vm.Capabilities, "tools") {
		add("tools")
	}
	if slices.Contains(vm.Capabilities, "vision") {
		add("vision")
	}
	return tags
}

// modelBaseName 去掉 "vendor/" 前缀与 ":free" 之类的变体后缀。
func modelBaseName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, ":"); i > 0 {
		name = name[:i]
	}
	return name
}

// vendorSlugs：OpenRouter 作者 slug / 常见模型名前缀 -> 厂商展示名。
var vendorSlugs = map[string]string{
	"openai": "OpenAI", "anthropic": "Anthropic", "google": "Google", "meta-llama": "Meta", "meta": "Meta",
	"mistralai": "Mistral AI", "mistral": "Mistral AI", "deepseek": "DeepSeek", "deepseek-ai": "DeepSeek",
	"qwen": "Qwen", "alibaba": "Qwen", "moonshotai": "Moonshot AI", "moonshot": "Moonshot AI",
	"z-ai": "Z.ai", "zai-org": "Z.ai", "zhipu": "Z.ai", "zhipuai": "Z.ai", "thudm": "Z.ai",
	"x-ai": "xAI", "xai": "xAI", "minimax": "MiniMax", "baidu": "Baidu", "tencent": "Tencent",
	"bytedance": "ByteDance", "bytedance-seed": "ByteDance", "cohere": "Cohere", "microsoft": "Microsoft",
	"nvidia": "NVIDIA", "amazon": "Amazon", "ai21": "AI21 Labs", "perplexity": "Perplexity",
	"stepfun": "StepFun", "stepfun-ai": "StepFun", "meituan": "Meituan", "meituan-longcat": "Meituan",
	"xiaomi": "Xiaomi", "inclusionai": "Ant Group", "liquid": "Liquid AI", "nousresearch": "Nous Research",
	"01-ai": "01.AI", "baai": "BAAI", "black-forest-labs": "Black Forest Labs", "stabilityai": "Stability AI",
	"ibm-granite": "IBM", "arcee-ai": "Arcee AI", "allenai": "Ai2", "inception": "Inception",
}

// vendorFamilies：无 vendor/ 前缀时按模型名首段识别厂商（如 deepseek-chat、gpt-4o）。
var vendorFamilies = map[string]string{
	"gpt": "OpenAI", "o1": "OpenAI", "o3": "OpenAI", "o4": "OpenAI", "chatgpt": "OpenAI", "dall": "OpenAI",
	"whisper": "OpenAI", "claude": "Anthropic", "gemini": "Google", "gemma": "Google",
	"llama": "Meta", "mistral": "Mistral AI", "codestral": "Mistral AI", "devstral": "Mistral AI",
	"magistral": "Mistral AI", "pixtral": "Mistral AI", "ministral": "Mistral AI", "deepseek": "DeepSeek",
	"qwen": "Qwen", "qwq": "Qwen", "qvq": "Qwen", "kimi": "Moonshot AI", "moonshot": "Moonshot AI",
	"glm": "Z.ai", "chatglm": "Z.ai", "cogview": "Z.ai", "grok": "xAI", "minimax": "MiniMax", "abab": "MiniMax",
	"ernie": "Baidu", "hunyuan": "Tencent", "doubao": "ByteDance", "seed": "ByteDance", "command": "Cohere",
	"phi": "Microsoft", "step": "StepFun", "longcat": "Meituan", "mimo": "Xiaomi", "ling": "Ant Group",
	"ring": "Ant Group", "yi": "01.AI", "spark": "iFLYTEK", "baichuan": "Baichuan",
}

var familyToken = regexp.MustCompile(`^[a-z]+[0-9]*`)

// vendorFromModelName 按内置表识别厂商，识别不出返回空。
func vendorFromModelName(name string) string {
	lower := strings.ToLower(name)
	if prefix, _, ok := strings.Cut(lower, "/"); ok {
		if v, ok := vendorSlugs[prefix]; ok {
			return v
		}
	}
	base := strings.ToLower(modelBaseName(name))
	first := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' })
	if len(first) == 0 {
		return ""
	}
	if v, ok := vendorFamilies[first[0]]; ok {
		return v
	}
	// "gpt4o"、"qwen2.5" 这类首段带数字的，取字母前缀再查
	if m := familyToken.FindString(first[0]); m != "" {
		letters := strings.TrimRightFunc(m, unicode.IsDigit)
		if v, ok := vendorFamilies[letters]; ok {
			return v
		}
	}
	return ""
}

// cleanExternalName 去掉外部目录名称里的 "(free)" 之类后缀。
func cleanExternalName(name string) string {
	name = strings.TrimSpace(name)
	for _, suf := range []string{"(free)", "(Free)", "(beta)", "(preview)"} {
		name = strings.TrimSpace(strings.TrimSuffix(name, suf))
	}
	return name
}

// splitVendorPrefix 拆 OpenRouter 风格的 "DeepSeek: DeepSeek V3" -> ("DeepSeek", "DeepSeek V3")。
// 没有前缀时 vendor 为空、name 原样返回。
func splitVendorPrefix(name string) (vendor, model string) {
	if v, m, ok := strings.Cut(name, ": "); ok && v != "" && strings.TrimSpace(m) != "" && utf8.RuneCountInString(v) <= 40 {
		return strings.TrimSpace(v), strings.TrimSpace(m)
	}
	return "", name
}

// wordCasing：模型名里常见词的规范大小写。
var wordCasing = map[string]string{
	"gpt": "GPT", "glm": "GLM", "ernie": "ERNIE", "deepseek": "DeepSeek", "minimax": "MiniMax", "qwq": "QwQ",
	"qvq": "QVQ", "mimo": "MiMo", "longcat": "LongCat", "vl": "VL", "ocr": "OCR", "tts": "TTS", "asr": "ASR",
	"api": "API", "ai": "AI", "moe": "MoE", "it": "IT", "oss": "OSS", "chatglm": "ChatGLM", "openai": "OpenAI",
	"xai": "xAI", "llm": "LLM", "hd": "HD", "4o": "4o", "kimi": "Kimi", "flux": "FLUX", "bge": "BGE",
}

var numberedWord = regexp.MustCompile(`^[a-z][0-9][0-9a-z.]*$`) // v3、r1、k2、o3…

// humanizeModelID 把模型 ID 片段转成可读名称：deepseek-v3.1-terminus -> DeepSeek V3.1 Terminus。
func humanizeModelID(id string) string {
	parts := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, p := range parts {
		lower := strings.ToLower(p)
		switch {
		case wordCasing[lower] != "":
			parts[i] = wordCasing[lower]
		case numberedWord.MatchString(lower):
			parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
		default:
			r, size := utf8.DecodeRuneInString(p)
			parts[i] = string(unicode.ToUpper(r)) + p[size:]
		}
	}
	// 习惯写法里 GPT / GLM 与版本号用连字符相连：GPT-4o、GLM-4.5
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			if (parts[i-1] == "GPT" || parts[i-1] == "GLM") && p[0] >= '0' && p[0] <= '9' {
				b.WriteByte('-')
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteString(p)
	}
	return b.String()
}

var mdLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)

// maxSuggestedDescription 是建议文案的长度上限（字符数）；模型库卡片只展示前 3 行，
// 详情页展示全文，外部目录的长文截到第一段、最多这么长。
const maxSuggestedDescription = 400

// cleanDescription 取外部描述的第一段，去掉 Markdown 链接/强调，按句子截断到上限。
func cleanDescription(d string) string {
	d = strings.ReplaceAll(d, "\r\n", "\n")
	if i := strings.Index(d, "\n\n"); i > 0 {
		d = d[:i]
	}
	d = mdLink.ReplaceAllString(d, "$1")
	d = strings.NewReplacer("**", "", "__", "", "`", "").Replace(d)
	d = strings.Join(strings.Fields(d), " ")
	if utf8.RuneCountInString(d) <= maxSuggestedDescription {
		return d
	}
	runes := []rune(d)[:maxSuggestedDescription]
	cut := string(runes)
	// 尽量在句末截断
	if i := strings.LastIndexAny(cut, ".。!！?？"); i > len(cut)/2 {
		_, size := utf8.DecodeRuneInString(cut[i:])
		return cut[:i+size]
	}
	return strings.TrimSpace(cut) + "…"
}
