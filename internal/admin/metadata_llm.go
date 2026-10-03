package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/WALLE-AI/uFreeTokens/internal/llm"
)

// 用 LLM 生成模型介绍文案（展示元数据「自动填充」的可选增强）：外部目录没有介绍，或只有
// 英文长文时，按模型的已知事实写一两句中文介绍。只在运营单个模型编辑时手动触发
// （GET .../metadata/suggestion?llm=1），批量填充与上架自动写入不调用，控制成本也避免
// 未经人工核对的生成内容直接上线。

// ErrLLMNotConfigured：没有配置共享 LLM（配置段 datasync.llm_*），无法生成介绍。
var ErrLLMNotConfigured = errors.New("admin: LLM is not configured (config datasync.llm_base_url / llm_model and env UFT_DATASYNC_LLM_API_KEY)")

// ErrLLMUnavailable：LLM 调用失败或输出不可用。
var ErrLLMUnavailable = errors.New("admin: LLM call failed")

// ModelDescriptionInput 是生成介绍时提供给 LLM 的模型事实。
type ModelDescriptionInput struct {
	Name                string
	DisplayName         string
	Vendor              string
	Type                string
	Capabilities        []string
	ContextWindow       int
	MaxOutput           int
	ExternalDescription string // 外部目录原文（可能是英文），可为空
}

// DescriptionGenerator 生成模型介绍文案（测试可替换为假实现）。
type DescriptionGenerator interface {
	GenerateModelDescription(ctx context.Context, in ModelDescriptionInput) (string, error)
	// ModelName 是生成所用的模型名（写进建议值的 detail，便于追溯）。
	ModelName() string
}

// SetDescriptionGenerator 配置介绍生成器；nil = 不可用。
func (s *Service) SetDescriptionGenerator(g DescriptionGenerator) { s.descGen = g }

// LLMDescriptionGenerator 用 OpenAI 兼容接口生成介绍。
type LLMDescriptionGenerator struct{ Client *llm.Client }

// NewLLMDescriptionGenerator 用共享的 LLM 客户端（llm.FromConfig）构造；c 为 nil 时返回 nil（未配置）。
func NewLLMDescriptionGenerator(c *llm.Client) DescriptionGenerator {
	if c == nil {
		return nil
	}
	return &LLMDescriptionGenerator{Client: c}
}

func (g *LLMDescriptionGenerator) ModelName() string { return g.Client.Model }

const describePrompt = `你是 AI 模型目录的编辑。根据用户给出的模型事实，为模型库卡片写一段简体中文介绍。
要求：1–2 句话，不超过 120 个汉字；说明模型的定位、主要特点与适用场景；
只使用给出的事实（外部介绍可翻译、概括），不要编造参数、评测成绩、发布时间或价格；不要使用 Markdown。
只输出 JSON：{"description":"..."}`

// maxGeneratedDescription 是生成文案的长度上限（字符数），超出视为不可用。
const maxGeneratedDescription = 300

func (g *LLMDescriptionGenerator) GenerateModelDescription(ctx context.Context, in ModelDescriptionInput) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "模型 ID：%s\n", in.Name)
	if in.DisplayName != "" {
		fmt.Fprintf(&b, "展示名称：%s\n", in.DisplayName)
	}
	if in.Vendor != "" {
		fmt.Fprintf(&b, "厂商：%s\n", in.Vendor)
	}
	fmt.Fprintf(&b, "类型：%s\n", in.Type)
	if len(in.Capabilities) > 0 {
		fmt.Fprintf(&b, "能力：%s\n", strings.Join(in.Capabilities, ", "))
	}
	if in.ContextWindow > 0 {
		fmt.Fprintf(&b, "上下文窗口：%d tokens\n", in.ContextWindow)
	}
	if in.MaxOutput > 0 {
		fmt.Fprintf(&b, "最大输出：%d tokens\n", in.MaxOutput)
	}
	if d := strings.TrimSpace(in.ExternalDescription); d != "" {
		fmt.Fprintf(&b, "外部介绍：%s\n", llm.Truncate(d, 4000))
	}
	content, err := g.Client.ChatJSON(ctx, describePrompt, b.String())
	if err != nil {
		return "", err
	}
	return ParseGeneratedDescription(content)
}

// ParseGeneratedDescription 解析 LLM 输出的 {"description": "..."}。
func ParseGeneratedDescription(content string) (string, error) {
	var out struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(llm.StripCodeFence(content)), &out); err != nil {
		return "", fmt.Errorf("llm: output is not the expected JSON: %w", err)
	}
	d := strings.Join(strings.Fields(out.Description), " ")
	if d == "" {
		return "", errors.New("llm: empty description")
	}
	if utf8.RuneCountInString(d) > maxGeneratedDescription {
		return "", fmt.Errorf("llm: description too long (%d chars)", utf8.RuneCountInString(d))
	}
	return d, nil
}
