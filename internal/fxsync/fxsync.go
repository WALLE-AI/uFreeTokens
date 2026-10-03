// Package fxsync 是确定性的汇率采集（fetcher=fx_rate，实施方案 M1-B09、设计 §13.2）：
// 汇率直接进入售价换算，不走智能体。接一个免密钥的结构化源（默认 open.er-api.com，
// 形如 {"base_code":"USD","rates":{"CNY":7.1}}；frankfurter 的 {"base":"USD","rates":{...}} 同样支持），
// 可选第二个源做交叉核对；与库中最新汇率偏离超过阈值或两源分歧过大时整批拒收（rejected，计入告警），
// 不写入——人工核实后在后台手工录入。
//
// 来源 config：
//
//	base             string    原币种，默认 USD
//	quotes           []string  目标币种，默认 ["CNY"]
//	max_deviation    number    允许的相对偏离（与上次汇率 / 两源之间），默认 0.02
//	cross_check_url  string    可选：交叉核对源（同样的 JSON 形状）
package fxsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

// Writer 是写汇率需要的能力（admin.Service 实现）。
type Writer interface {
	ListFXRates(ctx context.Context, base, quote string, latest bool, limit int) ([]admin.FXRateInfo, error)
	SetFXRate(ctx context.Context, in admin.SetFXRateInput) (prev, cur *admin.FXRateInfo, err error)
	RecordAudit(ctx context.Context, in admin.AuditLogInput) (int64, error)
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Job 实现 datasync.Job。
type Job struct {
	Admin Writer
	// Now 便于测试；nil = time.Now。
	Now func() time.Time
}

type config struct {
	Base          string   `json:"base"`
	Quotes        []string `json:"quotes"`
	MaxDeviation  float64  `json:"max_deviation"`
	CrossCheckURL string   `json:"cross_check_url"`
}

var shanghai = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// ParseRates 解析 {"base_code"|"base": "...", "rates": {...}}；base 字段存在时必须与期望一致。
func ParseRates(body []byte, base string) (map[string]decimal.Decimal, error) {
	var r struct {
		Result   string                     `json:"result"`
		BaseCode string                     `json:"base_code"`
		Base     string                     `json:"base"`
		Rates    map[string]decimal.Decimal `json:"rates"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode fx response: %w", err)
	}
	if r.Result != "" && r.Result != "success" {
		return nil, fmt.Errorf("fx source returned result=%q", r.Result)
	}
	got := strings.ToUpper(r.BaseCode + r.Base)
	if got != "" && got != base {
		return nil, fmt.Errorf("fx source base is %s, want %s", got, base)
	}
	if len(r.Rates) == 0 {
		return nil, fmt.Errorf("fx source has no rates")
	}
	return r.Rates, nil
}

func deviation(a, b decimal.Decimal) float64 {
	if b.IsZero() {
		return 1
	}
	d, _ := a.Sub(b).Abs().Div(b).Float64()
	return d
}

func (j *Job) Run(ctx context.Context, env *datasync.Env, src datasync.Source) (datasync.Result, error) {
	raw, _ := json.Marshal(src.Config)
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return datasync.Result{}, fmt.Errorf("fxsync: decode config: %w", err)
	}
	cfg.Base = strings.ToUpper(strings.TrimSpace(cfg.Base))
	if cfg.Base == "" {
		cfg.Base = "USD"
	}
	if len(cfg.Quotes) == 0 {
		cfg.Quotes = []string{"CNY"}
	}
	if cfg.MaxDeviation <= 0 {
		cfg.MaxDeviation = 0.02
	}
	if src.URL == "" {
		return datasync.Result{}, fmt.Errorf("fxsync: source %d has no url", src.ID)
	}
	resp, err := env.Get(ctx, src.URL, datasync.GetOptions{MaxBytes: 1 << 20})
	if err != nil {
		return datasync.Result{}, err
	}
	rates, err := ParseRates(resp.Body, cfg.Base)
	if err != nil {
		return datasync.Result{}, datasync.Rejectf("%v", err)
	}
	var cross map[string]decimal.Decimal
	if cfg.CrossCheckURL != "" {
		cr, err := env.Get(ctx, cfg.CrossCheckURL, datasync.GetOptions{MaxBytes: 1 << 20})
		if err != nil {
			return datasync.Result{}, fmt.Errorf("fxsync: cross-check source: %w", err)
		}
		if cross, err = ParseRates(cr.Body, cfg.Base); err != nil {
			return datasync.Result{}, datasync.Rejectf("cross-check source: %v", err)
		}
	}

	// 先全部校验，任一币种不通过整批拒收（不写半批）。
	type item struct {
		quote string
		rate  decimal.Decimal
		prev  *admin.FXRateInfo
	}
	var items []item
	for _, q := range cfg.Quotes {
		q = strings.ToUpper(strings.TrimSpace(q))
		rate, ok := rates[q]
		if !ok || !rate.IsPositive() {
			return datasync.Result{}, datasync.Rejectf("fx source has no positive rate for %s/%s", cfg.Base, q)
		}
		if cross != nil {
			c, ok := cross[q]
			if !ok {
				return datasync.Result{}, datasync.Rejectf("cross-check source has no rate for %s/%s", cfg.Base, q)
			}
			if d := deviation(rate, c); d > cfg.MaxDeviation {
				return datasync.Result{}, datasync.Rejectf("%s/%s sources disagree: %s vs %s (%.2f%%)", cfg.Base, q, rate, c, d*100)
			}
		}
		latest, err := j.Admin.ListFXRates(ctx, cfg.Base, q, true, 1)
		if err != nil {
			return datasync.Result{}, err
		}
		it := item{quote: q, rate: rate.Round(6)}
		if len(latest) > 0 {
			it.prev = &latest[0]
			if d := deviation(it.rate, latest[0].Rate); d > cfg.MaxDeviation {
				return datasync.Result{}, datasync.Rejectf("%s/%s moved %.2f%% (%s -> %s), above max_deviation %.2f%%: verify and enter manually",
					cfg.Base, q, d*100, latest[0].Rate, it.rate, cfg.MaxDeviation*100)
			}
		}
		items = append(items, it)
	}

	now := time.Now
	if j.Now != nil {
		now = j.Now
	}
	today := now().In(shanghai)
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	changed := 0
	source := "fx_rate"
	if src.Name != "" {
		source = "fx_rate:" + src.Name
	}
	for _, it := range items {
		if it.prev != nil && it.prev.Rate.Equal(it.rate) && it.prev.EffectiveDate.Equal(day) {
			continue
		}
		err := j.Admin.RunInTx(ctx, func(ctx context.Context) error {
			prev, cur, err := j.Admin.SetFXRate(ctx, admin.SetFXRateInput{Base: cfg.Base, Quote: it.quote, Rate: it.rate, Source: source, EffectiveDate: day})
			if err != nil {
				return err
			}
			_, err = j.Admin.RecordAudit(ctx, admin.AuditLogInput{
				ActorID: 0, ActorName: "system", Action: "fx_rate.set", TargetType: "fx_rate",
				TargetID: cur.Base + "/" + cur.Quote + "@" + cur.EffectiveDate.Format(time.DateOnly), Before: prev, After: cur,
			})
			return err
		})
		if err != nil {
			return datasync.Result{}, err
		}
		changed++
	}
	status := datasync.StatusOK
	if changed == 0 {
		status = datasync.StatusUnchanged
	}
	return datasync.Result{Status: status, ItemsFetched: len(items), ItemsChanged: changed, ContentHash: resp.Hash}, nil
}
