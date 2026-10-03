package offers

import (
	"context"
	"errors"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

// ExtractPreview 支撑 POST /offer-pages/extract-preview（运营智能体「优惠分拣」剧本，实施方案 M2-B08）：
// 对单个页面跑一次与 offer_page 定时任务相同的抽取与原文证据校验，**不写库**。

// PreviewInput 是一次抽取预览的参数。
type PreviewInput struct {
	URL          string   `json:"url"`
	ProviderCode string   `json:"provider_code"`
	Keywords     []string `json:"keywords"`
	MaxChars     int      `json:"max_chars"`
}

// PreviewItem 是一条抽取结果及其是否通过校验（未通过的在真实运行中会被丢弃）。
type PreviewItem struct {
	ExtractedOffer
	Accepted bool `json:"accepted"`
}

// PreviewResult 是抽取预览结果。
type PreviewResult struct {
	URL       string        `json:"url"`
	TextChars int           `json:"text_chars"`
	Items     []PreviewItem `json:"items"`
	Accepted  int           `json:"accepted"`
	Dropped   int           `json:"dropped"`
}

// ErrLLMNotConfigured 表示未配置抽取用的 LLM。
var ErrLLMNotConfigured = errors.New("offers: LLM not configured")

// ExtractPreview 抓取页面 → 按关键词过滤正文 → LLM 抽取 → 证据校验，返回全部结果。
func ExtractPreview(ctx context.Context, env *datasync.Env, llm LLM, in PreviewInput) (*PreviewResult, error) {
	if llm == nil {
		return nil, ErrLLMNotConfigured
	}
	if in.URL == "" || strings.TrimSpace(in.ProviderCode) == "" {
		return nil, errors.New("url and provider_code are required")
	}
	keywords := in.Keywords
	if len(keywords) == 0 {
		keywords = DefaultKeywords
	}
	maxChars := in.MaxChars
	if maxChars <= 0 || maxChars > 30000 {
		maxChars = 12000
	}
	resp, err := env.Get(ctx, in.URL, datasync.GetOptions{MaxBytes: 8 << 20})
	if err != nil {
		return nil, err
	}
	text := FilterLines(PageText(resp.Body), keywords, maxChars)
	out := &PreviewResult{URL: in.URL, TextChars: len([]rune(text)), Items: []PreviewItem{}}
	if strings.TrimSpace(text) == "" {
		return out, nil
	}
	items, err := llm.ExtractOffers(ctx, in.ProviderCode, in.URL, text)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		_, ok := it.toCandidate(0, in.ProviderCode, in.URL, text)
		out.Items = append(out.Items, PreviewItem{ExtractedOffer: it, Accepted: ok})
		if ok {
			out.Accepted++
		} else {
			out.Dropped++
		}
	}
	return out, nil
}

// EvidenceInText 判断 quote 是否（按空白归一化后）逐字出现在 text 中——与抽取管道的证据校验同一规则，
// 智能体提案的证据校验也用它（设计 §13.4）。
func EvidenceInText(text, quote string) bool {
	q := normalizeSpace(quote)
	return len([]rune(q)) >= 4 && strings.Contains(normalizeSpace(text), q)
}
