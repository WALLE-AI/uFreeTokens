package offers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/net/html"

	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

// 定价页 / 公告页的优惠文案抽取（detection=llm_extract）。来源 config：
//
//	{"pages": [{"url": "https://api-docs.deepseek.com/zh-cn/quick_start/pricing", "provider_code": "deepseek"}],
//	 "keywords": ["限时", "免费", ...],   // 可选，默认 DefaultKeywords
//	 "max_chars": 12000}                  // 可选，单页送给 LLM 的最大字符数
//
// LLM 走 OpenAI 兼容接口（通常就是本平台网关），由 worker 的环境变量配置——密钥不能写进
// 明文的来源 config：UFT_DATASYNC_LLM_BASE_URL、UFT_DATASYNC_LLM_API_KEY、UFT_DATASYNC_LLM_MODEL。
// 未配置时这个来源跑空（记 detail.skipped），不算失败。
//
// LLM 的输出一律只作候选：必须带一句页面原文（evidence），原文在页面里找不到就丢弃；
// 入库状态都是 new，等运营确认。

// DefaultKeywords 是筛选段落用的关键词（命中任一即保留该行及前后各一行）。
var DefaultKeywords = []string{
	"限时", "免费", "折扣", "优惠", "打折", "折", "错峰", "夜间", "赠送", "试用", "新用户", "额度", "降价",
	"free", "discount", "off-peak", "promotion", "limited time", "trial", "credit", "% off", "price cut",
}

type pageConfig struct {
	Pages []struct {
		URL          string `json:"url"`
		ProviderCode string `json:"provider_code"`
	} `json:"pages"`
	Keywords []string `json:"keywords"`
	MaxChars int      `json:"max_chars"`
}

// LLM 是抽取用的最小接口（测试用假实现）。
type LLM interface {
	ExtractOffers(ctx context.Context, providerCode, pageURL, text string) ([]ExtractedOffer, error)
}

// ExtractedOffer 是 LLM 返回的一条优惠（JSON 字段即提示词里约定的输出格式）。
type ExtractedOffer struct {
	UpstreamModel *string        `json:"upstream_model"`
	OfferType     string         `json:"offer_type"`
	DiscountRatio *float64       `json:"discount_ratio"`
	StartsAt      *string        `json:"starts_at"`
	EndsAt        *string        `json:"ends_at"`
	Conditions    string         `json:"conditions"`
	Quota         map[string]any `json:"quota"`
	Evidence      string         `json:"evidence"`
}

// PageJob 是 fetcher=offer_page 的 datasync.Job。
type PageJob struct {
	Store *Store
	LLM   LLM // nil = 从环境变量构造；仍为 nil 时跳过
}

func (j PageJob) Run(ctx context.Context, env *datasync.Env, src datasync.Source) (datasync.Result, error) {
	raw, _ := json.Marshal(src.Config)
	var cfg pageConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return datasync.Result{}, fmt.Errorf("offers: decode offer_page config: %w", err)
	}
	if len(cfg.Pages) == 0 && src.URL != "" {
		cfg.Pages = append(cfg.Pages, struct {
			URL          string `json:"url"`
			ProviderCode string `json:"provider_code"`
		}{src.URL, src.ProviderCode})
	}
	if len(cfg.Pages) == 0 {
		return datasync.Result{}, errors.New("offers: offer_page source needs config.pages or url")
	}
	keywords := cfg.Keywords
	if len(keywords) == 0 {
		keywords = DefaultKeywords
	}
	maxChars := cfg.MaxChars
	if maxChars <= 0 {
		maxChars = 12000
	}

	type page struct {
		url, provider, text string
	}
	var pages []page
	var hashes [][]byte
	for _, p := range cfg.Pages {
		if p.URL == "" || p.ProviderCode == "" {
			return datasync.Result{}, errors.New("offers: each page needs url and provider_code")
		}
		resp, err := env.Get(ctx, p.URL, datasync.GetOptions{MaxBytes: 8 << 20})
		if err != nil {
			return datasync.Result{}, err
		}
		text := FilterLines(PageText(resp.Body), keywords, maxChars)
		hashes = append(hashes, []byte(text))
		pages = append(pages, page{p.URL, p.ProviderCode, text})
	}
	hash := datasync.HashParts(hashes...)
	if bytes.Equal(hash, src.LastContentHash) {
		return datasync.Result{Status: datasync.StatusUnchanged, ItemsFetched: len(pages), ContentHash: hash}, nil
	}

	llm := j.LLM
	if llm == nil {
		llm = LLMFromEnv()
	}
	if llm == nil {
		// 不写 ContentHash：配置好 LLM 之后下一次运行会真正抽取。
		return datasync.Result{ItemsFetched: len(pages), Detail: map[string]any{"skipped": "UFT_DATASYNC_LLM_* not configured"}}, nil
	}

	var created, dropped int
	for _, p := range pages {
		if strings.TrimSpace(p.text) == "" {
			continue
		}
		items, err := llm.ExtractOffers(ctx, p.provider, p.url, p.text)
		if err != nil {
			return datasync.Result{}, fmt.Errorf("offers: llm extract %s: %w", p.url, err)
		}
		for _, it := range items {
			c, ok := it.toCandidate(src.ID, p.provider, p.url, p.text)
			if !ok {
				dropped++
				continue
			}
			if _, isNew, err := j.Store.Upsert(ctx, c); err != nil {
				return datasync.Result{}, err
			} else if isNew {
				created++
			}
		}
	}
	return datasync.Result{ItemsFetched: len(pages), ItemsChanged: created, ContentHash: hash,
		Detail: map[string]any{"offers_created": created, "llm_items_dropped": dropped}}, nil
}

// toCandidate 校验 LLM 输出：类型合法、折扣在 [0,1]、日期可解析、原文证据确实出现在页面里。
func (e ExtractedOffer) toCandidate(sourceID int64, provider, pageURL, pageText string) (Candidate, bool) {
	valid := false
	for _, t := range Types {
		if e.OfferType == t {
			valid = true
		}
	}
	if !valid || e.OfferType == TypePriceCut {
		return Candidate{}, false
	}
	ev := normalizeSpace(e.Evidence)
	if len([]rune(ev)) < 4 || !strings.Contains(normalizeSpace(pageText), ev) {
		return Candidate{}, false
	}
	c := Candidate{SourceID: &sourceID, ProviderCode: provider, OfferType: e.OfferType, Conditions: strings.TrimSpace(e.Conditions),
		Quota: e.Quota, EvidenceURL: pageURL, EvidenceExcerpt: e.Evidence, Detection: "llm_extract"}
	if e.UpstreamModel != nil {
		c.UpstreamModel = strings.TrimSpace(*e.UpstreamModel)
	}
	if e.DiscountRatio != nil {
		if *e.DiscountRatio < 0 || *e.DiscountRatio > 1 {
			return Candidate{}, false
		}
		d := decimal.NewFromFloat(*e.DiscountRatio).Round(5)
		c.DiscountRatio = &d
	} else if e.OfferType == TypeFreeModel {
		d := decimal.Zero
		c.DiscountRatio = &d
	}
	var ok bool
	if c.StartsAt, ok = parseDate(e.StartsAt); !ok {
		return Candidate{}, false
	}
	if c.EndsAt, ok = parseDate(e.EndsAt); !ok {
		return Candidate{}, false
	}
	return c, true
}

func parseDate(s *string) (*time.Time, bool) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, true
	}
	v := strings.TrimSpace(*s)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", time.DateOnly} {
		if t, err := time.ParseInLocation(layout, v, shanghai); err == nil {
			return &t, true
		}
	}
	return nil, false
}

var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// PageText 把 HTML 转成按块分行的纯文本（去掉 script/style 等）。
func PageText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return string(body)
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "template", "head":
				return
			}
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockElements[n.Data] {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		if l = normalizeSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

var blockElements = map[string]bool{
	"p": true, "div": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "table": true, "ul": true, "ol": true, "br": true, "dd": true, "dt": true, "blockquote": true,
	"header": true, "footer": true, "td": true, "th": true,
}

// FilterLines 保留命中关键词的行及其前后各一行，总长度不超过 maxChars。
func FilterLines(text string, keywords []string, maxChars int) string {
	lines := strings.Split(text, "\n")
	keep := make([]bool, len(lines))
	for i, l := range lines {
		ll := strings.ToLower(l)
		for _, k := range keywords {
			if strings.Contains(ll, strings.ToLower(k)) {
				for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
					keep[j] = true
				}
				break
			}
		}
	}
	var b strings.Builder
	for i, l := range lines {
		if !keep[i] {
			continue
		}
		if b.Len()+len(l)+1 > maxChars {
			break
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// ---------- OpenAI 兼容的 LLM 客户端 ----------

type openAILLM struct {
	baseURL, apiKey, model string
	client                 *http.Client
}

// LLMFromEnv 按 UFT_DATASYNC_LLM_* 构造客户端；未配置返回 nil。
func LLMFromEnv() LLM {
	base, key, model := os.Getenv("UFT_DATASYNC_LLM_BASE_URL"), os.Getenv("UFT_DATASYNC_LLM_API_KEY"), os.Getenv("UFT_DATASYNC_LLM_MODEL")
	if base == "" || key == "" || model == "" {
		return nil
	}
	return &openAILLM{baseURL: strings.TrimRight(base, "/"), apiKey: key, model: model, client: &http.Client{Timeout: 2 * time.Minute}}
}

const extractPrompt = `你是模型 API 价格情报分析员。下面是厂商 %s 的页面 %s 中与价格/优惠相关的文字片段。
请找出其中描述的【当前或即将生效的】免费模型、限时折扣、错峰优惠、免费额度、新用户赠送，忽略常规标价。
只输出 JSON：{"offers":[{"upstream_model":模型ID或null,"offer_type":"free_model|discount|off_peak|free_quota|new_user_credit",
"discount_ratio":价格乘数(0.5=五折,0=免费,无法量化为null),"starts_at":"YYYY-MM-DD"或null,"ends_at":"YYYY-MM-DD"或null,
"conditions":"一句话条件说明","quota":对象或null,"evidence":"逐字摘录页面原文中的一句"}]}
没有优惠时输出 {"offers":[]}。不要编造页面里没有的信息。`

func (l *openAILLM) ExtractOffers(ctx context.Context, providerCode, pageURL, text string) ([]ExtractedOffer, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           l.model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": fmt.Sprintf(extractPrompt, providerCode, pageURL)},
			{"role": "user", "content": text},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+l.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm status %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &cr); err != nil || len(cr.Choices) == 0 {
		return nil, fmt.Errorf("llm: unexpected response: %s", truncate(string(data), 300))
	}
	return ParseExtraction(cr.Choices[0].Message.Content)
}

// ParseExtraction 解析 LLM 输出（容忍 ```json 代码块包裹）。
func ParseExtraction(content string) ([]ExtractedOffer, error) {
	s := strings.TrimSpace(content)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	var out struct {
		Offers []ExtractedOffer `json:"offers"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("llm: output is not the expected JSON: %w", err)
	}
	return out.Offers, nil
}
