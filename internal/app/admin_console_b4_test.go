// 运营后台 B4 批次接口（用量统计、全局调用日志）的端到端测试，见
// docs/cmd-admin 运营后台接口补全技术方案.md §3、§6。统计口径（§3.1）在这里固化。
package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

func TestAdminConsole_StatsAndRequestLogs(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	pool := testPool(t)
	f := newB2Fixture(t, ac)

	var acct, key struct {
		ID int64 `json:"id"`
	}
	ac.post("/accounts", map[string]any{"type": "personal", "name": "b4-" + f.suffix}, &acct)
	ac.post(fmt.Sprintf("/accounts/%d/api-keys", acct.ID), map[string]any{"name": "b4-key"}, &key)

	// 3 条成功 + 1 条上游失败；其中一条成功请求是 estimated 用量。
	now := time.Now().UTC().Truncate(time.Second)
	logs := []struct {
		status        string
		latency       int
		charged, cost int64
		source        string
	}{
		{"success", 100, 1000, 600, "upstream"},
		{"success", 200, 1000, 600, "upstream"},
		{"success", 300, 1000, 600, "estimated"},
		{"upstream_error", 50, 0, 0, "upstream"},
	}
	for i, l := range logs {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, channel_id, endpoint, is_stream,
			   status, http_status, attempts, latency_ms, ttft_ms, input_tokens, output_tokens, usage_source, charged_amount, list_amount, cost_amount)
			 VALUES ($1, $2, $3, $4, $5, $6, '/v1/chat/completions', true, $7, 200, 1, $8, 50, 1000, 500, $9, $10, $10, $11)`,
			fmt.Sprintf("b4-%s-%d", f.suffix, i), now.Add(-time.Duration(len(logs)-i)*time.Minute), acct.ID, key.ID, f.vmName, f.channelID,
			l.status, l.latency, l.source, l.charged, l.cost); err != nil {
			t.Fatalf("insert request_log: %v", err)
		}
	}

	window := "&from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Add(time.Minute).Format(time.RFC3339))
	var usage admin.UsageResult
	ac.get(fmt.Sprintf("/stats/usage?channel_id=%d&group_by=virtual_model&interval=hour", f.channelID)+window, &usage)
	tot := usage.Totals
	if tot.Requests != 4 || tot.Success != 3 || tot.ErrorRate.String() != "0.25" || tot.RevenueMicro != 3000 || tot.CostMicro != 1800 ||
		tot.GrossProfitMicro != 1200 || tot.GrossMargin.String() != "0.4" || tot.InputTokens != 3000 || tot.EstimatedRatio.String() != "0.25" ||
		tot.P50LatencyMs == nil || *tot.P50LatencyMs != 200 || tot.ActiveAccounts != 1 {
		t.Fatalf("totals = %+v", tot)
	}
	if len(usage.Groups) != 1 || usage.Groups[0].Key != f.vmName || usage.Groups[0].Label != f.vmName || len(usage.Series) == 0 {
		t.Fatalf("groups = %+v, series = %d", usage.Groups, len(usage.Series))
	}

	var prefix string
	if err := pool.QueryRow(t.Context(), `SELECT display_prefix FROM api_keys WHERE id = $1`, key.ID).Scan(&prefix); err != nil {
		t.Fatalf("query key prefix: %v", err)
	}
	var acctUsage admin.UsageResult
	ac.get(fmt.Sprintf("/accounts/%d/usage?interval=none&group_by=api_key", acct.ID)+window, &acctUsage)
	if acctUsage.Totals.Requests != 4 || len(acctUsage.Groups) != 1 || acctUsage.Groups[0].Label != "b4-key ("+prefix+"…)" {
		t.Fatalf("account usage = %+v", acctUsage.Groups)
	}

	var overview admin.StatsOverview
	ac.get("/stats/overview?from="+url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)), &overview)
	if overview.Current.Requests < 4 {
		t.Errorf("overview current = %+v, want >= 4 requests", overview.Current)
	}

	if status, _ := ac.do(http.MethodGet, "/stats/usage?interval=hour&from=2026-01-01&to=2026-02-01", nil, nil); status != http.StatusBadRequest {
		t.Errorf("hourly range over 7 days: %d, want 400", status)
	}

	// 调用日志：游标分页 + 过滤 + 详情。
	var p1, p2 struct {
		Data       []admin.RequestLogItem `json:"data"`
		NextCursor string                 `json:"next_cursor"`
	}
	base := fmt.Sprintf("/request-logs?channel_id=%d&limit=3", f.channelID) + window
	ac.get(base, &p1)
	if len(p1.Data) != 3 || p1.NextCursor == "" || p1.Data[0].Status != "upstream_error" {
		t.Fatalf("logs page1 = %+v", p1)
	}
	ac.get(base+"&before="+url.QueryEscape(p1.NextCursor), &p2)
	if len(p2.Data) != 1 || p2.NextCursor != "" {
		t.Fatalf("logs page2 = %+v", p2)
	}
	var errs struct {
		Data []admin.RequestLogItem `json:"data"`
	}
	ac.get(fmt.Sprintf("/request-logs?channel_id=%d&status=upstream_error", f.channelID)+window, &errs)
	if len(errs.Data) != 1 {
		t.Fatalf("status filter = %+v", errs.Data)
	}

	var d admin.RequestLogDetail
	ac.get("/request-logs/"+url.PathEscape(p1.Data[0].RequestID)+"?created_at="+url.QueryEscape(p1.Data[0].CreatedAt.Format(time.RFC3339Nano)), &d)
	if d.AccountName == nil || *d.AccountName != "b4-"+f.suffix || d.ChannelLabel == nil || d.ProviderCode == nil || *d.ProviderCode != "b2-"+f.suffix {
		t.Fatalf("detail = %+v", d)
	}
	if status, _ := ac.do(http.MethodGet, "/request-logs/does-not-exist", nil, nil); status != http.StatusNotFound {
		t.Errorf("missing log: %d, want 404", status)
	}
	if status, _ := ac.do(http.MethodGet, "/request-logs?from=2026-01-01&to=2026-02-01", nil, nil); status != http.StatusBadRequest {
		t.Errorf("30-day window without account: %d, want 400", status)
	}
}
