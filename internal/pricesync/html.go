package pricesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// HTMLTableConfig 是抓一个"定价表页面"所需的全部解析规则，存在
// price_sources.config（JSONB）里，随 source 配置、不用改代码就能接入新网页
// （前提是页面确实是表格布局——各厂商定价页排版差异很大，这不是一个"配一次
// 适配所有网站"的万能方案，只是把"CSS 选择器 + 单位换算"这些容易变的东西
// 参数化，出问题时改配置就行，不用发版）。
type HTMLTableConfig struct {
	// RowSelector 定位每一行"一个模型的价格"，比如 "table.pricing tbody tr"。
	RowSelector string `json:"row_selector"`
	// ModelNameSelector 相对于行的选择器，取模型展示名文本。
	ModelNameSelector string `json:"model_name_selector"`
	// ModelNameMap 把页面上的展示名映射成 upstream_model（有些厂商页面上写的
	// 名字和 API 实际用的模型 ID 不是一回事）；映射不到的名字原样当
	// upstream_model 用。
	ModelNameMap map[string]string `json:"model_name_map"`
	// Currency 是整页统一使用的原始币种（如 "USD"）。
	Currency string `json:"currency"`
	// Columns 每一列对应一个计量项。
	Columns []HTMLPriceColumn `json:"columns"`
	// MinModels 是"这次解析出的模型数至少要有多少个，少于这个数就认为页面
	// 改版/解析失败，整批丢弃"的下限（技术方案 §7.16.6）。0 = 不做这个检查。
	MinModels int `json:"min_models"`
}

// HTMLPriceColumn 描述表格里的一列价格。
type HTMLPriceColumn struct {
	// Selector 相对于行的选择器，取这一列的价格文本。
	Selector string `json:"selector"`
	Meter    string `json:"meter"` // pricing.Meter 的取值之一
	Unit     string `json:"unit"`  // pricing.Unit 的取值之一
	// Multiplier 把页面上的单价换算成 Unit 声明的计量单位下的价格——比如页面
	// 写的是"每千 token"但 Unit 是 per_1m_tokens，Multiplier 就该是 1000。
	// 零值当 1 处理。
	Multiplier float64 `json:"multiplier"`
}

var (
	// ErrHTMLPageStructureChanged 表示解析出的模型数低于 MinModels——大概率是
	// 页面改版导致选择器失效，不是"这家真的只有这么少模型"。技术方案 §7.16.6：
	// 这种情况要整批丢弃，不产生任何观测，而不是把"0 个模型"当成一条有效结果
	// 往下传。
	ErrHTMLPageStructureChanged = errors.New("pricesync: parsed model count below min_models, page structure may have changed")
	// ErrHTMLPriceUnparsable 表示某个价格单元格的文本解析不出数字。
	ErrHTMLPriceUnparsable = errors.New("pricesync: could not parse a price cell as a number")
)

// ParseHTMLPriceTable 是纯函数：给定页面 HTML 和解析规则，产出观测列表。
// 不发任何网络请求，方便用保存下来的 HTML fixture 测试（技术方案要求 L3
// 网页抓取只用录制的 fixture 测，不在自动化测试里真的请求外部网站）。
func ParseHTMLPriceTable(html []byte, cfg HTMLTableConfig) ([]Observation, error) {
	if cfg.RowSelector == "" || cfg.ModelNameSelector == "" || len(cfg.Columns) == 0 {
		return nil, errors.New("pricesync: row_selector, model_name_selector and at least one column are required")
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		return nil, fmt.Errorf("pricesync: parse HTML: %w", err)
	}

	var observations []Observation
	var parseErr error
	doc.Find(cfg.RowSelector).EachWithBreak(func(_ int, row *goquery.Selection) bool {
		name := strings.TrimSpace(row.Find(cfg.ModelNameSelector).First().Text())
		if name == "" {
			return true // 这一行没取到模型名，跳过（表头行、合并单元格等常见原因），继续下一行
		}
		upstreamModel := name
		if mapped, ok := cfg.ModelNameMap[name]; ok {
			upstreamModel = mapped
		}

		components := make([]Component, 0, len(cfg.Columns))
		for _, col := range cfg.Columns {
			text := strings.TrimSpace(row.Find(col.Selector).First().Text())
			if text == "" {
				continue // 这一列这一行没有值（比如这个模型不支持这个计量项），跳过，不当成 0 价
			}
			price, err := parsePriceText(text)
			if err != nil {
				parseErr = fmt.Errorf("%w: model=%q column=%q text=%q: %v", ErrHTMLPriceUnparsable, name, col.Meter, text, err)
				return false
			}
			multiplier := col.Multiplier
			if multiplier == 0 {
				multiplier = 1
			}
			components = append(components, Component{
				Meter: pricing.Meter(col.Meter), Unit: pricing.Unit(col.Unit), ServiceTier: "default",
				UnitPrice: price.Mul(decimal.NewFromFloat(multiplier)),
			})
		}
		if len(components) == 0 {
			return true
		}
		observations = append(observations, Observation{
			UpstreamModel: upstreamModel,
			Spec:          PriceSpec{Currency: cfg.Currency, Components: components},
		})
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	if cfg.MinModels > 0 && len(observations) < cfg.MinModels {
		return nil, fmt.Errorf("%w: got %d, want at least %d", ErrHTMLPageStructureChanged, len(observations), cfg.MinModels)
	}
	return observations, nil
}

// parsePriceText 从价格单元格文本里剥掉常见的货币符号/千分位逗号/空白后解析成
// decimal——"$0.003"、"¥1,234.5"、"0.003 " 都能处理，不能识别的字符原样保留
// 交给 decimal.NewFromString 报错（宁可报错也不要猜错——静默吃掉一个不认识的
// 符号可能把数量级看错）。
func parsePriceText(s string) (decimal.Decimal, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r == '.', r == '-':
			return r
		case r == ',':
			return -1 // 千分位分隔符，直接去掉
		default:
			return -1 // 货币符号、空白等非数字字符，去掉
		}
	}, s)
	if cleaned == "" {
		return decimal.Decimal{}, fmt.Errorf("no numeric characters in %q", s)
	}
	return decimal.NewFromString(cleaned)
}

// HTMLFetcher 实现 Fetcher 接口：GET 一个定价页面 URL，用 price_sources.config
// 里存的 HTMLTableConfig 解析成观测列表。真正的网络请求只在这里发生——
// ParseHTMLPriceTable 本身不碰网络，单测直接喂 fixture。
type HTMLFetcher struct {
	HTTPClient *http.Client
}

func (f *HTMLFetcher) Name() string { return "html_table" }

func (f *HTMLFetcher) httpClient() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (f *HTMLFetcher) Fetch(ctx context.Context, src Source) ([]Observation, error) {
	if src.URL == "" {
		return nil, errors.New("pricesync: html_table source requires a URL")
	}
	cfg, err := decodeHTMLTableConfig(src.Config)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("pricesync: build request: %w", err)
	}
	// 明确标识自己是价格同步机器人，礻貌起见——技术方案 §7.16.5："抓取网页须
	// 遵守对方的服务条款与 robots 规则，控制频率"，这至少让对方看日志时知道
	// 是谁在访问，不是伪装成普通浏览器。
	req.Header.Set("User-Agent", "uFreeTokens-PriceSync/1.0 (+html_table fetcher)")

	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("pricesync: fetch %s: %w", src.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricesync: fetch %s: status %d", src.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("pricesync: read response body: %w", err)
	}
	return ParseHTMLPriceTable(body, cfg)
}

// decodeHTMLTableConfig 把 price_sources.config（map[string]any，来自 JSONB 列
// 反序列化）转换成 HTMLTableConfig。走一次 json 编解码往返而不是手写字段提取，
// 图省事——config 不是热路径。
func decodeHTMLTableConfig(config map[string]any) (HTMLTableConfig, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return HTMLTableConfig{}, fmt.Errorf("pricesync: marshal source config: %w", err)
	}
	var cfg HTMLTableConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return HTMLTableConfig{}, fmt.Errorf("pricesync: decode html_table config: %w", err)
	}
	return cfg, nil
}
