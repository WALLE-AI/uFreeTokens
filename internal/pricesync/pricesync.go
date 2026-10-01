// Package pricesync 实现技术方案 §7.16 上游价格同步流水线里"取到一条观测之后"
// 的确定性部分：归一化表示（PriceSpec）、校验（Validate）、比较（Diff）、生效
// 策略（DecidePolicy）、发布（Engine.Ingest 会在自动通过时直接调用
// internal/admin 发布新的成本价版本）、以及人工审批（Engine.Approve/Reject）。
// 还有 Mapper 阶段（listing.go）：Engine.IngestUnmapped 按 provider+upstream_model
// 找现有渠道，找不到就进"新模型发现"队列（pending_model_listings），运营用
// Engine.PublishListing 一键把候选变成真实的虚拟模型 + 渠道 + 成本价 + 售价
// （技术方案 Phase 3"新模型自动发现与一键上架"）。
//
// 已知范围限制（明确未实现，不是遗漏——§7.16 本身是一个足够大的独立子系统，
// 这里先把"来了一条观测该怎么处理"这条确定性流水线做对、做全，调度和外部集成
// 留给后续按需接入）：
//   - OpenRouter（openrouter.go 的 OpenRouterFetcher）已经接了真实 HTTP 抓取
//     （GET /api/v1/models，不需要 API Key），自动化测试仍然只用
//     httptest.Server 喂固定 fixture，不会真的请求 openrouter.ai。这个来源
//     比较特殊：它把每个模型的计费直接放进模型列表接口里，是这几个已实现的
//     来源里唯一能拿到"接口里自带价格"的（internal/app/pricelookup.go 的
//     test_web 联调接口用到了它）。
//   - HTML 抓取（html.go 的 HTMLFetcher + ParseHTMLPriceTable）实现了一个
//     配置驱动的表格解析框架——CSS 选择器 + 单位换算存在 price_sources.config
//     里，出问题改配置不用发版；但这不是"配一次适配所有网站"的万能方案，各
//     厂商定价页排版差异很大，接一个新来源大概率还是要调选择器。自动化测试
//     只喂手工构造的 HTML fixture，不会真的请求任何外部网站（抓取本身对
//     目标站点的服务条款/robots/频率都要谨慎，不适合在 CI 里跑）；只做了通用
//     表格布局，chromedp（需要 JS 渲染的页面）、LLM 辅助抽取、账单 API 对接
//     （L1）都没有实现。
//   - 没有调度器：技术方案 §7.16.3/§7.16.5 设计的 cron + PG advisory lock 选主
//     没有实现——Engine.Ingest/IngestUnmapped 都是同步调用，由谁在什么时候
//     调用它们（真正的定时抓取任务，或者运营手工通过 admin 接口提交一条观测）
//     留给调用方决定；HTMLFetcher/OpenRouter 的归一化函数写好了，但没有任何
//     后台循环会定期调用它们。
//   - "模型消失"检测（§7.16.6）没有实现：这条规则本质上需要一个定期扫描
//     "哪些 (source, model) 组合最近没有新观测"的后台任务，不是单次 Ingest
//     调用能判断的，需要配合上面缺失的调度器一起做。
//   - 多来源冲突检测是简化版：技术方案原文是"L2 与 L4 差异 > 5%"，这里简化成
//     "同一 upstream_model 在过去 24 小时内、任意其它来源的最新观测，有任一
//     共同计量项差异 > 5%"，不区分具体是哪两个级别。
//   - impact_7d（按近 7 天实际用量估算变更影响）没有实现，price_change_requests
//     .impact_7d 恒为 NULL；这需要解析 request_logs 里的历史用量并按计量项
//     加权，属于独立工作量，留作后续。
//   - 毛利守护的路由降权部分不在这个包里：挂牌价结构性负毛利的判定
//     （catalog.Channel.NegativeMargin）和降权（router.channelWeight 打 10%
//     折扣）在 internal/catalog + internal/router 里实现，随每次快照刷新
//     自动重算，不依赖这个包缺失的调度器。本包（Engine.Ingest 触发的价格变更）
//     和那条判定是各自独立生效的——一次价格同步会改变 price_books，下一次
//     快照刷新自然会重新评估毛利，两者通过数据库自然衔接，不需要显式调用。
//   - 汇率来源同步见 internal/catalog（FXRates）+ internal/admin（SetFXRate）；
//     本包只是发布价格时把 PriceSpec.Currency 原样写进 price_books.currency，
//     不做汇率相关的处理。
package pricesync

import (
	"context"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// Level 是价格来源的可信度分级（技术方案 §7.16.2）。
type Level string

const (
	LevelL1 Level = "L1" // 实际结算回传：最高可信度，但只用于漂移检测，不直接生成价格
	LevelL2 Level = "L2" // 官方机器可读 API：可自动生效（受 Policy 阈值约束）
	LevelL3 Level = "L3" // 官方定价网页/公告：默认需人工审批，且要求连续两次观测一致
	LevelL4 Level = "L4" // 社区数据集：只做交叉校验，永不单独生效
	LevelL5 Level = "L5" // 人工录入：走审批流生效
)

// Source 对应一条 price_sources。
type Source struct {
	ID      int64
	Level   Level
	Kind    string // api / html / dataset / billing / manual
	Fetcher string // 插件名
	URL     string
	Config  map[string]any
}

// PriceSpec 是所有来源归一化后的统一表示（技术方案 §7.16.5）。
type PriceSpec struct {
	Currency      string
	Components    []Component
	EffectiveFrom *time.Time // 预约生效；nil = 立即
	ExpiresAt     *time.Time // 模型下线时间 / 限时价格结束时间
}

// isFree：有计量项且全部单价为 0。
func (s PriceSpec) isFree() bool {
	for _, c := range s.Components {
		if !c.UnitPrice.IsZero() {
			return false
		}
	}
	return len(s.Components) > 0
}

// Component 对应 PriceSpec 里的一个计量项，字段含义和 internal/pricing.Component
// 一致（这里独立定义一份，而不是直接复用 pricing.Component，是因为二者的生命
// 周期不同：pricing.Component 是"已经生效、用于实时计价"的表示，Component
// 是"一条尚待校验/审批的候选"，刻意保持两者可以独立演化）。
type Component struct {
	Meter          pricing.Meter
	Unit           pricing.Unit
	ServiceTier    string // "" 视为 "default"
	TierMinInput   int
	TierMaxInput   *int
	WindowStartMin *int16
	WindowEndMin   *int16
	UnitPrice      decimal.Decimal
}

// Observation 是 Fetcher 抓到并归一化后的一条"某上游模型在某时刻的价格"。
type Observation struct {
	UpstreamModel string
	Spec          PriceSpec
	RawObject     string     // 原始内容/证据，供审计；本阶段直接存文本，不接对象存储
	Meta          *ModelMeta // 来源顺带给出的模型参数；不参与 spec_hash，只用于预填"待上架"表单
}

// ModelMeta 是来源接口里与价格一起给出的模型基础参数（OpenRouter / models.dev / LiteLLM 都有）。
// 零值字段表示来源没给。Type / Capabilities 已映射到本平台的枚举（admin.EnumValues）。
type ModelMeta struct {
	Name             string   `json:"name,omitempty"` // 来源里的展示名
	Type             string   `json:"type,omitempty"` // chat / embedding / image / audio / rerank
	ContextWindow    int      `json:"context_window,omitempty"`
	MaxOutput        int      `json:"max_output,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Source           string   `json:"source,omitempty"` // 给出这些参数的抓取器名
}

// metaOrNil：一个字段都没有时返回 nil，免得存一条空对象。
func metaOrNil(m ModelMeta) *ModelMeta {
	if m.Name == "" && m.Type == "" && m.ContextWindow == 0 && m.MaxOutput == 0 && len(m.Capabilities) == 0 &&
		len(m.InputModalities) == 0 && len(m.OutputModalities) == 0 {
		return nil
	}
	return &m
}

// capabilitiesFrom 按固定顺序（与 admin.EnumValues().Capabilities 一致）收集能力，stream 默认都有。
func capabilitiesFrom(tools, vision, jsonMode, reasoning bool) []string {
	caps := []string{"stream"}
	for _, c := range []struct {
		on   bool
		name string
	}{{tools, "tools"}, {vision, "vision"}, {jsonMode, "json_mode"}, {reasoning, "reasoning"}} {
		if c.on {
			caps = append(caps, c.name)
		}
	}
	return caps
}

// typeFromModalities 由输入 / 输出模态推断模型类型：输出含 image 的算 image，输出只有 audio 的算 audio，
// 输出含 embedding 的算 embedding，其余（输出 text）算 chat。
func typeFromModalities(out []string) string {
	has := func(v string) bool { return slices.Contains(out, v) }
	switch {
	case len(out) == 0:
		return ""
	case has("embedding") || has("embeddings"):
		return "embedding"
	case has("image") && !has("text"):
		return "image"
	case has("audio") && !has("text"):
		return "audio"
	}
	return "chat"
}

// Fetcher 每个来源一个实现，只负责"取数 + 归一化"，不做任何决策（校验/比较/
// 生效策略都在 Validate/Diff/DecidePolicy 里，和 Fetcher 解耦，方便独立测试）。
type Fetcher interface {
	Name() string
	Fetch(ctx context.Context, src Source) ([]Observation, error)
}
