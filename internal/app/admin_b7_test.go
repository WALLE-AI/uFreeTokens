// 运营后台 B7（接口规范化与缺失接口）的端到端测试：批量导入、价格预览、目录计数、
// 枚举字典、API Key 编辑、成员管理、Idempotency-Key、参数校验、时区分桶。
package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

func TestB7_ImportModelsDryRunThenImport(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	f := newB2Fixture(t, ac) // 假币种 f.currency → CNY 汇率 7，上游账号倍率 1.2
	up := "b7-import-" + f.suffix
	body := map[string]any{
		"currency": f.currency, "markup_percent": "25",
		"items": []map[string]any{
			{"upstream_model": up, "family": "b7", "context_window": 32000, "max_output": 4096, "cost_input": "1", "cost_output": "2"},
			// 已存在的虚拟模型 + 已存在的渠道（fixture），保留现有售价，只刷新成本价。
			{"upstream_model": "b2-up-" + f.suffix, "name": f.vmName, "cost_input": "1", "cost_output": "2", "keep_existing_sell": true},
			// 手工售价低于成本 → 负毛利，逐条报错，不影响其他条目。
			{"upstream_model": up + "-neg", "family": "b7", "context_window": 32000, "max_output": 4096, "cost_input": "1", "cost_output": "2", "sell_input": "1", "sell_output": "1"},
		},
	}
	type item struct {
		admin.ImportModelPlan
		OK     bool                      `json:"ok"`
		Result *admin.ImportModelResult  `json:"result"`
		Error  *struct{ Message string } `json:"error"`
	}
	var dry struct {
		DryRun bool   `json:"dry_run"`
		Items  []item `json:"items"`
	}
	body["dry_run"] = true
	ac.post(fmt.Sprintf("/provider-accounts/%d/import-models", f.accountID), body, &dry)
	if len(dry.Items) != 3 {
		t.Fatalf("dry run items = %+v", dry.Items)
	}
	// 1 × 7 × 1.2 = 8.4 → ×1.25 = 10.5（decimal，无浮点误差）
	if it := dry.Items[0]; it.Status != admin.ImportStatusNew || !it.OK || it.SellInput == nil || it.SellInput.String() != "10.5" || it.SellOutput.String() != "21" {
		t.Errorf("item 0 plan = %+v, want new, sell 10.5/21", it)
	}
	if it := dry.Items[1]; it.Status != admin.ImportStatusListed || it.PublishSellPrice || !it.OK {
		t.Errorf("item 1 plan = %+v, want listed and keep existing sell price", it)
	}
	if it := dry.Items[2]; it.OK || !strings.Contains(strings.Join(it.Errors, ","), "负毛利") {
		t.Errorf("item 2 plan = %+v, want negative-margin error", it)
	}
	var vmCount int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM virtual_models WHERE name = $1`, up).Scan(&vmCount)
	if vmCount != 0 {
		t.Fatalf("dry run created %d virtual models", vmCount)
	}

	body["dry_run"] = false
	var real struct {
		Items []item `json:"items"`
	}
	ac.post(fmt.Sprintf("/provider-accounts/%d/import-models", f.accountID), body, &real)
	if it := real.Items[0]; !it.OK || it.Result == nil || !it.Result.CreatedVM || !it.Result.CreatedChannel || it.Result.SellBookID == nil {
		t.Errorf("item 0 result = %+v", it)
	}
	if it := real.Items[1]; !it.OK || it.Result == nil || it.Result.CreatedVM || it.Result.CreatedChannel || it.Result.SellBookID != nil {
		t.Errorf("item 1 result = %+v, want reused vm/channel and no new sell price", it)
	}
	if it := real.Items[2]; it.OK || it.Error == nil {
		t.Errorf("item 2 result = %+v, want error", it)
	}
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM virtual_models WHERE name = $1`, up+"-neg").Scan(&vmCount)
	if vmCount != 0 {
		t.Errorf("failed item left %d virtual models behind", vmCount)
	}
	var audits int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_audit_logs WHERE action = 'model.import' AND target_id = $1`, fmt.Sprint(f.accountID)).Scan(&audits)
	if audits != 2 {
		t.Errorf("model.import audits = %d, want 2 (one per imported model)", audits)
	}
}

func TestB7_MetaCountsPreview(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)

	var enums struct {
		Tiers       []string `json:"tiers"`
		Meters      []string `json:"meters"`
		Permissions []string `json:"permissions"`
	}
	ac.get("/meta/enums", &enums)
	if len(enums.Tiers) != 3 || len(enums.Meters) == 0 || len(enums.Permissions) == 0 {
		t.Errorf("enums = %+v", enums)
	}
	var counts admin.CatalogCounts
	ac.get("/catalog/counts", &counts)
	if counts.Models.Total == 0 || counts.Channels.Total == 0 || counts.Channels.Active == 0 {
		t.Errorf("counts = %+v", counts)
	}
	var preview admin.PricingPreviewResult
	ac.post("/pricing/preview", map[string]any{"currency": f.currency, "cost_multiplier": "1.2", "markup_percent": "20",
		"items": []map[string]any{{"key": "a", "cost_input": "1", "cost_output": "2"}}}, &preview)
	if preview.FXMissing || len(preview.Items) != 1 || preview.Items[0].SellInput.String() != "10.08" || preview.Items[0].MarginRatio.String() != "0.1667" {
		t.Errorf("preview = %+v, want sell 10.08 and margin 0.1667", preview.Items)
	}
}

func TestB7_APIKeyEditAndMembers(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, false)
	defer done()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var acct idResp
	ac.post("/accounts", map[string]any{"type": "organization", "name": "b7-org-" + suffix}, &acct)
	var key struct {
		ID int64 `json:"id"`
	}
	ac.post(fmt.Sprintf("/accounts/%d/api-keys", acct.ID), map[string]any{"name": "k1", "budget_limit_micro": 5_000_000, "budget_period": "monthly"}, &key)

	status, body := ac.do(http.MethodPatch, fmt.Sprintf("/api-keys/%d", key.ID), map[string]any{"status": "disabled", "rpm_limit": 60, "name": "k1-renamed"}, nil)
	var updated admin.APIKeyListItem
	_ = json.Unmarshal(body, &updated)
	if status != http.StatusOK || updated.Status != "disabled" || updated.RPMLimit == nil || *updated.RPMLimit != 60 || updated.Name != "k1-renamed" ||
		updated.BudgetLimitMicro == nil || *updated.BudgetLimitMicro != 5_000_000 {
		t.Fatalf("patch api key: %d %s", status, body)
	}
	// 账户下的 Key 列表现在分页返回。
	var page admin.Page[admin.APIKeyListItem]
	ac.get(fmt.Sprintf("/accounts/%d/api-keys?page_size=1", acct.ID), &page)
	if page.Total != 1 || len(page.Data) != 1 {
		t.Errorf("account api keys page = %+v", page)
	}
	ac.post(fmt.Sprintf("/api-keys/%d/revoke", key.ID), nil, nil)
	if status, _ := ac.do(http.MethodPatch, fmt.Sprintf("/api-keys/%d", key.ID), map[string]any{"status": "active"}, nil); status != http.StatusConflict {
		t.Errorf("patch revoked key: status = %d, want 409", status)
	}

	// 成员：加一个 owner、一个 viewer；不能移除/降级最后一个 owner。
	var uid1, uid2 int64
	for i, dst := range []*int64{&uid1, &uid2} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ($1, '!') RETURNING id`,
			fmt.Sprintf("b7-%s-%d@test.local", suffix, i)).Scan(dst); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	ac.post(fmt.Sprintf("/accounts/%d/members", acct.ID), map[string]any{"email": fmt.Sprintf("b7-%s-0@test.local", suffix), "role": "owner"}, nil)
	ac.post(fmt.Sprintf("/accounts/%d/members", acct.ID), map[string]any{"email": fmt.Sprintf("b7-%s-1@test.local", suffix), "role": "viewer"}, nil)
	if status, _ := ac.do(http.MethodPost, fmt.Sprintf("/accounts/%d/members", acct.ID), map[string]any{"email": "nobody-" + suffix + "@test.local", "role": "viewer"}, nil); status != http.StatusNotFound {
		t.Errorf("add unknown user: status = %d, want 404", status)
	}
	if status, _ := ac.do(http.MethodDelete, fmt.Sprintf("/accounts/%d/members/%d", acct.ID, uid1), nil, nil); status != http.StatusConflict {
		t.Errorf("remove last owner: status = %d, want 409", status)
	}
	if status, _ := ac.do(http.MethodPatch, fmt.Sprintf("/accounts/%d/members/%d", acct.ID, uid2), map[string]any{"role": "owner"}, nil); status != http.StatusOK {
		t.Errorf("promote member: status = %d", status)
	}
	if status, _ := ac.do(http.MethodDelete, fmt.Sprintf("/accounts/%d/members/%d", acct.ID, uid1), nil, nil); status != http.StatusNoContent {
		t.Errorf("remove owner when another owner exists: status = %d, want 204", status)
	}
}

func TestB7_IdempotencyKeyReplays(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	key := fmt.Sprint("idem-", time.Now().UnixNano())
	body := map[string]any{"type": "personal", "name": key}
	h := map[string]string{"Idempotency-Key": key}
	s1, b1 := ac.do(http.MethodPost, "/accounts", body, h)
	s2, b2 := ac.do(http.MethodPost, "/accounts", body, h)
	if s1 != http.StatusCreated || s2 != http.StatusCreated || string(b1) != string(b2) {
		t.Fatalf("replay: %d %s / %d %s, want identical 201 responses", s1, b1, s2, b2)
	}
	var list admin.Page[admin.AccountSummary]
	ac.get("/accounts?q="+key, &list)
	if list.Total != 1 {
		t.Errorf("accounts created = %d, want 1", list.Total)
	}
	if s, _ := ac.do(http.MethodPost, "/accounts", map[string]any{"type": "personal", "name": key + "-other"}, h); s != http.StatusUnprocessableEntity {
		t.Errorf("same key, different body: status = %d, want 422", s)
	}
}

func TestB7_ParamValidation(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	for _, path := range []string{
		"/accounts?status=bogus", "/channels?dedicated=maybe", "/virtual-models?status=active,bogus",
		"/price-change-requests?direction=sideways", "/stats/usage?tz=Mars/Base",
	} {
		if s, body := ac.do(http.MethodGet, path, nil, nil); s != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d body = %s, want 400", path, s, body)
		}
	}
	// limit 超上限截到上限而不是回落到默认值。
	var logs struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.get("/audit-logs?limit=100000", &logs)
	if len(logs.Data) > 500 {
		t.Errorf("audit logs = %d, want at most 500", len(logs.Data))
	}
}
