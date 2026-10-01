// Package datasync 是外部数据采集的调度层（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §2）：
// 价格、上游优惠、评测榜单三类来源共用一张 price_sources 表（domain 区分）、一套
// 调度 / 运行记录 / 失败退避 / 告警。它只负责"什么时候跑哪个来源、跑完记账"，
// 具体抓什么、怎么入库由各 domain 注册的 Job 决定：
//   - price：internal/pricesync（OpenRouter / models.dev / LiteLLM / HTML 定价表），
//     顺带识别免费模型与降价（internal/offers）；
//   - offer：internal/offers 的定价页 / 公告页文案抽取；
//   - benchmark：internal/benchsync 的榜单抓取、模型名映射、导入与发布。
//
// 调度方式：worker 每分钟调用一次 Scheduler.Tick，挑出 enabled 且 next_run_at 已到
// 的来源，逐个用 PG advisory lock 抢占后执行——worker 多副本时同一来源不会被并发跑。
// schedule 为空的来源只能在运营后台"立即运行"（把 next_run_at 置为 now）。
package datasync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Source 是一行 price_sources（调度视角）。
type Source struct {
	ID            int64
	ProviderID    *int64
	ProviderCode  string // providers.code；ProviderID 为空时为 ""
	Domain        string // price / offer / benchmark
	Name          string
	Level         string
	Kind          string
	Fetcher       string
	URL           string
	Schedule      string
	Config        map[string]any
	License       string
	Attribution   string
	PublicDisplay bool
	AutoPublish   bool

	LastContentHash  []byte
	HTTPETag         string
	HTTPLastModified string
}

// Run 状态，与 data_source_runs.status 一致。
const (
	StatusOK        = "ok"
	StatusUnchanged = "unchanged"
	StatusFailed    = "failed"
	StatusRejected  = "rejected"
)

// Result 是一次 Job 执行的结果。Status 为空按 ok 处理。
type Result struct {
	Status       string
	ItemsFetched int
	ItemsChanged int
	// ContentHash 非空时写回 price_sources.last_content_hash，供下次判断"内容没变"。
	ContentHash      []byte
	HTTPETag         string
	HTTPLastModified string
	Detail           map[string]any
}

// ErrRejected 包装"抓到了，但数据形状不对"的错误（页面改版、行数骤降、分数异常）：
// 本次不入库，记 rejected，按失败计入退避与告警。
var ErrRejected = errors.New("datasync: fetched data rejected by sanity check")

// Rejectf 构造一个 ErrRejected。
func Rejectf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRejected, fmt.Sprintf(format, a...))
}

// Job 执行一个来源的一次抓取 + 入库。实现方不应吞掉错误：返回 error 即本次失败。
type Job interface {
	Run(ctx context.Context, env *Env, src Source) (Result, error)
}

// JobFunc 让普通函数满足 Job。
type JobFunc func(ctx context.Context, env *Env, src Source) (Result, error)

func (f JobFunc) Run(ctx context.Context, env *Env, src Source) (Result, error) {
	return f(ctx, env, src)
}

// Registry 按 price_sources.fetcher 查找 Job。
type Registry map[string]Job

// Fetchers 返回已注册的抓取器名（运营后台下拉框用）。
func (r Registry) Fetchers() []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	return out
}

// 默认的单次运行超时与退避上限。
const (
	DefaultRunTimeout = 10 * time.Minute
	maxBackoff        = 6 * time.Hour
	baseBackoff       = 5 * time.Minute
	// AlertAfterFailures：连续失败达到这个次数时打 alert=true 的 ERROR 日志。
	AlertAfterFailures = 3
)

// Backoff 返回连续第 failures 次失败后的重试间隔：5m、10m、20m ... 封顶 6h。
func Backoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := baseBackoff
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}
