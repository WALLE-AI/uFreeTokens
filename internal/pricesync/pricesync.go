// Package pricesync 实现技术方案 §7.16 上游价格同步流水线里"取到一条观测之后"
// 的确定性部分：归一化表示（PriceSpec）、校验（Validate）、比较（Diff）、生效
// 策略（DecidePolicy）、发布（Engine.Ingest 会在自动通过时直接调用
// internal/admin 发布新的成本价版本）、以及人工审批（Engine.Approve/Reject）。
//
// 已知范围限制（明确未实现，不是遗漏——§7.16 本身是一个足够大的独立子系统，
// 这里先把"来了一条观测该怎么处理"这条确定性流水线做对、做全，调度和外部集成
// 留给后续按需接入）：
//   - 没有真正对接外部的 Fetcher：openrouter.go 只实现了归一化函数
//     （normalizeOpenRouter），用录制的固定 JSON 作为 golden fixture 测试，
//     没有真正发 HTTP 请求去抓 OpenRouter 的公开接口。HTML 抓取
//     （goquery/chromedp）、LLM 辅助抽取、账单 API 对接（L1）都没有实现。
//   - 没有调度器：技术方案 §7.16.3/§7.16.5 设计的 cron + PG advisory lock 选主
//     没有实现——Engine.Ingest 是同步调用，由谁在什么时候调用它（真正的定时
//     抓取任务，或者运营手工通过 admin 接口提交一条观测）留给调用方决定。
//   - "模型消失"检测（§7.16.6）没有实现：这条规则本质上需要一个定期扫描
//     "哪些 (source, model) 组合最近没有新观测"的后台任务，不是单次 Ingest
//     调用能判断的，需要配合上面缺失的调度器一起做。
//   - 多来源冲突检测是简化版：技术方案原文是"L2 与 L4 差异 > 5%"，这里简化成
//     "同一 upstream_model 在过去 24 小时内、任意其它来源的最新观测，有任一
//     共同计量项差异 > 5%"，不区分具体是哪两个级别。
//   - impact_7d（按近 7 天实际用量估算变更影响）没有实现，price_change_requests
//     .impact_7d 恒为 NULL；这需要解析 request_logs 里的历史用量并按计量项
//     加权，属于独立工作量，留作后续。
//   - 毛利守护（Margin Guard，§7.16.7）没有实现：这里不产生"某渠道毛利转负，
//     自动把路由权重降到 0.1"这类联动动作。
//   - 汇率来源同步见 internal/catalog（FXRates）+ internal/admin（SetFXRate）；
//     本包只是发布价格时把 PriceSpec.Currency 原样写进 price_books.currency，
//     不做汇率相关的处理。
package pricesync

import (
	"context"
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
	RawObject     string // 原始内容/证据，供审计；本阶段直接存文本，不接对象存储
}

// Fetcher 每个来源一个实现，只负责"取数 + 归一化"，不做任何决策（校验/比较/
// 生效策略都在 Validate/Diff/DecidePolicy 里，和 Fetcher 解耦，方便独立测试）。
type Fetcher interface {
	Name() string
	Fetch(ctx context.Context, src Source) ([]Observation, error)
}
