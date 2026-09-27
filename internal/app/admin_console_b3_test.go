// 运营后台 B3 批次接口（账户检索、资金流水、赠送余额、API Key 检索、带防护的
// 人工调账）的端到端测试，见 docs/cmd-admin 运营后台接口补全技术方案.md §4。
package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

func TestAdminConsole_AccountsLedgerAndGuardedAdjust(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	pool := testPool(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	var acct struct {
		ID int64 `json:"id"`
	}
	ac.post("/accounts", map[string]any{"type": "organization", "name": "b3-org-" + suffix, "tier": "pro"}, &acct)

	// 挂一个 owner 用户，验证按邮箱检索与详情里的成员列表。
	email := "b3-" + suffix + "@example.com"
	var userID int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO account_members (account_id, user_id, role) VALUES ($1, $2, 'owner')`, acct.ID, userID); err != nil {
		t.Fatalf("insert member: %v", err)
	}

	for _, q := range []string{fmt.Sprint(acct.ID), email, "b3-org-" + suffix} {
		var page admin.Page[admin.AccountSummary]
		ac.get("/accounts?q="+url.QueryEscape(q), &page)
		if page.Total != 1 || page.Data[0].ID != acct.ID || page.Data[0].OwnerEmail == nil || *page.Data[0].OwnerEmail != email {
			t.Fatalf("search %q = %+v, want exactly the fixture account with owner email", q, page)
		}
	}

	adjustPath := fmt.Sprintf("/accounts/%d/wallet/adjust", acct.ID)
	if status, _ := ac.do(http.MethodPost, adjustPath, map[string]any{"amount": 100, "ref_id": "no-reason-" + suffix}, nil); status != http.StatusBadRequest {
		t.Errorf("adjust without reason: %d, want 400", status)
	}
	ac.post(adjustPath, map[string]any{"amount": 5_000_000, "ref_id": "b3-topup-" + suffix, "reason": "充值", "expected_cash_balance_micro": 0}, nil)

	// 页面上看到的余额已过时（实际是 5 元）：必须 409 balance_changed，且不入账。
	status, body := ac.do(http.MethodPost, adjustPath, map[string]any{"amount": 1, "ref_id": "b3-stale-" + suffix, "reason": "x", "expected_cash_balance_micro": 0}, nil)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errBody)
	if status != http.StatusConflict || errBody.Error.Code != "balance_changed" {
		t.Errorf("stale expected balance: %d %s, want 409 balance_changed", status, body)
	}
	// 扣成负数必须拒绝。
	if status, body := ac.do(http.MethodPost, adjustPath, map[string]any{"amount": -6_000_000, "ref_id": "b3-neg-" + suffix, "reason": "扣减"}, nil); status != http.StatusBadRequest {
		t.Errorf("adjust to negative: %d %s, want 400", status, body)
	}
	ac.post(adjustPath, map[string]any{"amount": -1_000_000, "ref_id": "b3-deduct-" + suffix, "reason": "扣减", "expected_cash_balance_micro": 5_000_000}, nil)
	ac.post(fmt.Sprintf("/accounts/%d/credit-grants", acct.ID), map[string]any{
		"source": "compensation", "amount": 2_000_000, "ref_id": "b3-grant-" + suffix, "reason": "故障补偿",
		"expires_at": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
	}, nil)

	var detail struct {
		Wallet  admin.WalletSummary   `json:"wallet"`
		Members []admin.AccountMember `json:"members"`
		Grants  admin.GrantsSummary   `json:"active_grants_summary"`
	}
	ac.get(fmt.Sprintf("/accounts/%d", acct.ID), &detail)
	if detail.Wallet.CashBalance != 4_000_000 || detail.Wallet.BonusBalance != 2_000_000 || len(detail.Members) != 1 ||
		detail.Grants.Count != 1 || detail.Grants.RemainingMicro != 2_000_000 || detail.Grants.NearestExpiresAt == nil {
		t.Fatalf("detail = %+v", detail)
	}

	// 流水游标分页：3 条（充值、扣减、赠送），limit=2 分两页，倒序。
	var p1, p2 admin.Page[admin.LedgerEntry]
	var c1 struct {
		NextCursor string `json:"next_cursor"`
	}
	ledgerPath := fmt.Sprintf("/accounts/%d/ledger?limit=2", acct.ID)
	ac.get(ledgerPath, &p1)
	ac.get(ledgerPath, &c1)
	if len(p1.Data) != 2 || p1.Data[0].Type != "grant" || c1.NextCursor == "" {
		t.Fatalf("ledger page1 = %+v cursor=%q", p1.Data, c1.NextCursor)
	}
	ac.get(ledgerPath+"&before="+url.QueryEscape(c1.NextCursor), &p2)
	if len(p2.Data) != 1 || p2.Data[0].AmountMicro != 5_000_000 || p2.Data[0].RefID != "b3-topup-"+suffix {
		t.Fatalf("ledger page2 = %+v", p2.Data)
	}

	var grants struct {
		Data []admin.CreditGrantInfo `json:"data"`
	}
	ac.get(fmt.Sprintf("/accounts/%d/credit-grants?active=true", acct.ID), &grants)
	if len(grants.Data) != 1 || grants.Data[0].Source != "compensation" {
		t.Fatalf("grants = %+v", grants.Data)
	}

	// 审计：调账记录 before/after 现金余额与原因。
	var audit struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.get(fmt.Sprintf("/audit-logs?target_type=account&target_id=%d&action=wallet.adjust", acct.ID), &audit)
	if len(audit.Data) != 2 {
		t.Fatalf("adjust audit entries = %d, want 2", len(audit.Data))
	}
	var before, after map[string]any
	_ = json.Unmarshal(audit.Data[0].Before, &before)
	_ = json.Unmarshal(audit.Data[0].After, &after)
	if fmt.Sprint(before["cash_balance_micro"]) != "5e+06" || fmt.Sprint(after["cash_balance_micro"]) != "4e+06" || after["reason"] != "扣减" {
		t.Errorf("deduct audit before/after = %v / %v", before, after)
	}

	// API Key 全局检索：按用户发来的完整 Key 的前缀定位。
	var key struct {
		ID            int64  `json:"id"`
		DisplayPrefix string `json:"display_prefix"`
		RawKey        string `json:"raw_key"`
	}
	ac.post(fmt.Sprintf("/accounts/%d/api-keys", acct.ID), map[string]any{"name": "b3-key"}, &key)
	var keys admin.Page[admin.APIKeyListItem]
	ac.get("/api-keys?q="+url.QueryEscape(key.RawKey), &keys)
	if keys.Total != 1 || keys.Data[0].ID != key.ID || keys.Data[0].AccountName != "b3-org-"+suffix {
		t.Fatalf("api key search by raw key = %+v", keys)
	}
}
