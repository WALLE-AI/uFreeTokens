package pricesync

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

// DryRun 支撑 POST /price-sources/dry-run（运营智能体「数据源诊断与修复」剧本，实施方案 M2-B08）：
// 用给定的 fetcher 与配置抓取一次并解析，返回样本行与告警，**不写任何表**——
// 解析器（parsers）本来就与入库流程分离，这里只是不走 Job.Run 的入库部分。

// DryRunInput 是一次试运行的参数。
type DryRunInput struct {
	Fetcher string         `json:"fetcher"`
	URL     string         `json:"url"`
	Config  map[string]any `json:"config"`
	// SampleSize 是返回的样本行数，默认 10、最多 50。
	SampleSize int `json:"sample_size"`
}

// DryRunRow 是一条解析结果的摘要。
type DryRunRow struct {
	UpstreamModel string            `json:"upstream_model"`
	Currency      string            `json:"currency"`
	Prices        map[string]string `json:"prices"` // "meter/unit[@tier]" → 单价
}

// DryRunResult 是试运行结果。
type DryRunResult struct {
	Fetcher  string      `json:"fetcher"`
	Count    int         `json:"count"`
	Sample   []DryRunRow `json:"sample"`
	Warnings []string    `json:"warnings"`
	// Error 非空表示抓取成功但解析失败（真实运行时会被判为 rejected）。
	Error string `json:"error,omitempty"`
}

// ErrUnsupportedFetcher 表示该 fetcher 不支持试运行（不是价格解析器，如 offer_page / tabular）。
var ErrUnsupportedFetcher = errors.New("pricesync: fetcher does not support dry-run")

// DryRunFetchers 返回支持试运行的 fetcher 名。
func DryRunFetchers() []string {
	out := make([]string, 0, len(parsers))
	for k := range parsers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DryRun 抓取并解析，不写库。抓取失败（网络、状态码）返回 error；解析失败放在 Result.Error。
func DryRun(ctx context.Context, env *datasync.Env, in DryRunInput) (*DryRunResult, error) {
	parse, ok := parsers[in.Fetcher]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedFetcher, in.Fetcher)
	}
	if in.URL == "" {
		return nil, errors.New("url is required")
	}
	n := in.SampleSize
	if n <= 0 {
		n = 10
	}
	if n > 50 {
		n = 50
	}
	resp, err := env.Get(ctx, in.URL, datasync.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := &DryRunResult{Fetcher: in.Fetcher, Sample: []DryRunRow{}, Warnings: []string{}}
	obs, err := parse(resp.Body, datasync.Source{Fetcher: in.Fetcher, URL: in.URL, Config: in.Config})
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.Count = len(obs)
	if len(obs) == 0 {
		out.Warnings = append(out.Warnings, "parsed 0 models: a real run would be rejected")
	}
	zero, seen := 0, map[string]bool{}
	for _, o := range obs {
		if seen[o.UpstreamModel] {
			out.Warnings = append(out.Warnings, "duplicate upstream model: "+o.UpstreamModel)
		}
		seen[o.UpstreamModel] = true
		if o.Spec.isFree() {
			zero++
		}
		if len(out.Sample) < n {
			row := DryRunRow{UpstreamModel: o.UpstreamModel, Currency: o.Spec.Currency, Prices: map[string]string{}}
			for _, c := range o.Spec.Components {
				key := string(c.Meter) + "/" + string(c.Unit)
				if c.ServiceTier != "" && c.ServiceTier != "default" {
					key += "@" + c.ServiceTier
				}
				if c.TierMinInput > 0 {
					key += fmt.Sprintf(">%d", c.TierMinInput)
				}
				row.Prices[key] = c.UnitPrice.String()
			}
			out.Sample = append(out.Sample, row)
		}
	}
	if len(obs) > 0 && zero*2 > len(obs) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d of %d models parsed as free: check the price column selectors", zero, len(obs)))
	}
	return out, nil
}
