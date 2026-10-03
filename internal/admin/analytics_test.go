package admin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBucketPoints_MergesDaysIntoWeeksAndMonths(t *testing.T) {
	day := func(d string, req, ok, rev, cost int64) UsagePoint {
		return UsagePoint{Bucket: d, Metrics: Metrics{Requests: req, Success: ok, RevenueMicro: rev, CostMicro: cost}}
	}
	// 2026-09-28 是周一；09-27（周日）属于上一周。
	series := []UsagePoint{day("2026-09-27", 1, 1, 100, 50), day("2026-09-28", 4, 3, 1000, 400), day("2026-10-01", 6, 6, 3000, 600)}
	weeks := bucketPoints(series, "week", time.UTC)
	if len(weeks) != 2 || weeks[0].Bucket != "2026-09-21" || weeks[1].Bucket != "2026-09-28" {
		t.Fatalf("weeks = %+v", weeks)
	}
	w := weeks[1].Metrics
	if w.Requests != 10 || w.Success != 9 || w.RevenueMicro != 4000 || w.GrossProfitMicro != 3000 {
		t.Errorf("merged week = %+v", w)
	}
	if w.ErrorRate == nil || w.ErrorRate.String() != "0.1" || w.GrossMargin == nil || w.GrossMargin.String() != "0.75" {
		t.Errorf("derived rates = %v / %v, want 0.1 / 0.75", w.ErrorRate, w.GrossMargin)
	}
	months := bucketPoints(series, "month", time.UTC)
	if len(months) != 2 || months[0].Bucket != "2026-09" || months[0].Requests != 5 || months[1].Bucket != "2026-10" {
		t.Errorf("months = %+v", months)
	}
}

func TestChange(t *testing.T) {
	if got := change(float64(3), float64(2), "money"); got != 0.5 {
		t.Errorf("relative change = %v, want 0.5", got)
	}
	if got := change(int64(5), int64(0), "integer"); got != nil {
		t.Errorf("change from zero = %v, want nil", got)
	}
	if got := change(0.12, 0.1, "percent"); got != 0.02 {
		t.Errorf("percent change = %v, want 0.02 (percentage points)", got)
	}
	if got := change(nil, int64(1), "integer"); got != nil {
		t.Errorf("change with nil current = %v", got)
	}
}

func TestAnalytics_ValidatesQuery(t *testing.T) {
	s := &Service{}
	r := AnalyticsRange{From: time.Now().Add(-time.Hour), To: time.Now()}
	for name, q := range map[string]AnalyticsQuery{
		"subject":        {Subject: "orders"},
		"compare":        {Compare: "yoy"},
		"usage metric":   {Metrics: []string{"revenue", "nope"}},
		"usage group":    {GroupBy: "tier"},
		"usage interval": {Interval: "quarter"},
		"wallet hour":    {Subject: "wallet", Interval: "hour"},
		"wallet group":   {Subject: "wallet", GroupBy: "account", Interval: "day"},
		"balance cmp":    {Subject: "balance", Compare: "previous_period"},
		"balance range":  {Subject: "balance", Interval: "day"},
	} {
		if _, err := s.Analytics(context.Background(), q, r); err == nil {
			t.Errorf("%s: invalid query accepted", name)
		} else if !errors.Is(err, ErrInvalidFilterOrValue) {
			t.Errorf("%s: err = %v, want ErrInvalidFilterOrValue", name, err)
		}
	}
}

// TestAnalytics_UsageGroupedWithCompare：分组排名 + 上一周期对比，金额换算为元、环比由服务端计算。
func TestAnalytics_UsageGroupedWithCompare(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()
	model := uniqueCode(t)
	now := time.Now().UTC()
	seed := func(i int, at time.Time, status string, charge int64) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream,
			   status, http_status, attempts, latency_ms, input_tokens, output_tokens, usage_source, charged_amount, list_amount, cost_amount)
			 VALUES ($1, $2, 7, 7, $3, '/v1/chat/completions', false, $4, 200, 1, 300, 100, 50, 'upstream', $5::bigint, $5::bigint, $5::bigint / 4)`,
			fmt.Sprintf("%s-%d", model, i), at, model, status, charge); err != nil {
			t.Fatalf("seed request_log: %v", err)
		}
	}
	seed(0, now.Add(-2*time.Hour), "success", 1_000_000)
	seed(1, now.Add(-time.Hour), "success", 3_000_000)
	seed(2, now.Add(-90*time.Minute), "upstream_error", 0)
	seed(3, now.Add(-26*time.Hour), "success", 2_000_000) // 上一周期

	ds, err := s.Analytics(ctx, AnalyticsQuery{
		Metrics: []string{"revenue", "requests", "error_rate", "gross_margin"}, GroupBy: "virtual_model",
		Filters: AnalyticsFilters{VirtualModel: model}, Compare: "previous_period",
	}, AnalyticsRange{From: now.Add(-24 * time.Hour), To: now, Loc: time.UTC})
	if err != nil {
		t.Fatalf("Analytics: %v", err)
	}
	if ds.Source != "raw" || ds.Interval != "none" || len(ds.Rows) != 1 {
		t.Fatalf("dataset = %+v", ds)
	}
	row := ds.Rows[0]
	if row["key"] != model || row["revenue"] != 4.0 || row["requests"] != int64(3) {
		t.Errorf("row = %+v", row)
	}
	if row["revenue_prev"] != 2.0 || row["revenue_change"] != 1.0 || row["requests_change"] != 2.0 {
		t.Errorf("compare columns = prev %v change %v / requests change %v", row["revenue_prev"], row["revenue_change"], row["requests_change"])
	}
	if er, _ := row["error_rate"].(float64); er < 0.333 || er > 0.334 {
		t.Errorf("error_rate = %v, want 1/3", row["error_rate"])
	}
	if row["gross_margin"] != 0.75 || row["gross_margin_change"] != 0.0 {
		t.Errorf("gross margin %v (change %v), want 0.75 (0 pp)", row["gross_margin"], row["gross_margin_change"])
	}
	if ds.Totals["revenue"] != 4.0 || ds.Previous["revenue"] != 2.0 {
		t.Errorf("totals = %v / previous = %v", ds.Totals, ds.Previous)
	}
	var hasChangeCol bool
	for _, c := range ds.Columns {
		if c.Key == "gross_margin_change" && c.Type == "pp" {
			hasChangeCol = true
		}
	}
	if !hasChangeCol {
		t.Errorf("columns = %+v, want gross_margin_change typed pp", ds.Columns)
	}
	if ds.Title == "" || ds.Currency != "CNY" {
		t.Errorf("title %q currency %q", ds.Title, ds.Currency)
	}

	// 不分组：默认按天的时间序列
	ts, err := s.Analytics(ctx, AnalyticsQuery{Metrics: []string{"requests"}, Filters: AnalyticsFilters{VirtualModel: model}},
		AnalyticsRange{From: now.Add(-24 * time.Hour), To: now, Loc: time.UTC})
	if err != nil {
		t.Fatalf("Analytics series: %v", err)
	}
	var total int64
	for _, r := range ts.Rows {
		if r["bucket"] == "" {
			t.Errorf("series row without bucket: %+v", r)
		}
		total += r["requests"].(int64)
	}
	if ts.Interval != "day" || total != 3 {
		t.Errorf("series interval %s total %d, want day / 3", ts.Interval, total)
	}
}

func TestAnalytics_WalletAndBalance(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, CreateAccountInput{Type: "personal", Name: uniqueCode(t)})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	now := time.Now().UTC()
	for i, e := range []struct {
		typ    string
		amount int64
	}{{"recharge", 10_000_000}, {"recharge", 5_000_000}, {"adjust", -1_000_000}, {"consume", -300_000}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id, created_at)
			 VALUES ($1, $2, $3, 'cash', 0, 0, 'admin', $4, $5)`,
			acct.ID, e.typ, e.amount, fmt.Sprintf("%s-%d", uniqueCode(t), i), now.Add(-time.Duration(i+1)*time.Hour)); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}
	r := AnalyticsRange{From: now.Add(-24 * time.Hour), To: now, Loc: time.UTC}
	ds, err := s.Analytics(ctx, AnalyticsQuery{Subject: "wallet", Interval: "none", Filters: AnalyticsFilters{AccountID: acct.ID},
		Metrics: []string{"recharge_amount", "recharge_count", "paying_accounts", "adjust_amount"}}, r)
	if err != nil {
		t.Fatalf("wallet: %v", err)
	}
	tot := ds.Totals
	if tot["recharge_amount"] != 15.0 || tot["recharge_count"] != int64(2) || tot["paying_accounts"] != int64(1) || tot["adjust_amount"] != -1.0 {
		t.Errorf("wallet totals = %+v", tot)
	}
	if ds.Source != "ledger" || len(ds.Rows) != 1 {
		t.Errorf("wallet dataset = %+v", ds)
	}

	byAcct, err := s.Analytics(ctx, AnalyticsQuery{Subject: "wallet", GroupBy: "account", Filters: AnalyticsFilters{AccountID: acct.ID}}, r)
	if err != nil {
		t.Fatalf("wallet by account: %v", err)
	}
	if len(byAcct.Rows) != 1 || byAcct.Rows[0]["key"] != fmt.Sprint(acct.ID) || byAcct.Rows[0]["label"] != acct.Name {
		t.Errorf("wallet by account rows = %+v", byAcct.Rows)
	}

	if _, err := pool.Exec(ctx, `UPDATE wallets SET cash_balance = 5000000, bonus_balance = 1500000 WHERE account_id = $1`, acct.ID); err != nil {
		t.Fatalf("set wallet: %v", err)
	}
	bal, err := s.Analytics(ctx, AnalyticsQuery{Subject: "balance", GroupBy: "account", Filters: AnalyticsFilters{AccountID: acct.ID}}, r)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if len(bal.Rows) != 1 || bal.Rows[0]["total_balance"] != 6.5 || bal.Rows[0]["label"] != acct.Name || bal.From != nil {
		t.Errorf("balance rows = %+v (from %v)", bal.Rows, bal.From)
	}
}
