// 运营后台 B6（数据一致性）的端到端测试：并发审批、最后渠道守卫、赠金幂等、
// 乐观锁、价格版本链、审计与业务同事务。见
// docs/运营后台接口与数据库设计问题分析及执行方案.md §3 B6。
package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

// parallel 并发发出 n 个相同请求，返回各自的状态码。
func parallel(ac *adminClient, n int, method, path string, body any) []int {
	statuses := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			statuses[i], _ = ac.do(method, path, body, nil)
		}()
	}
	close(start)
	wg.Wait()
	return statuses
}

func countStatus(statuses []int, want int) int {
	n := 0
	for _, s := range statuses {
		if s == want {
			n++
		}
	}
	return n
}

func TestB6_ConcurrentApprovePublishesOnce(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	var res ingestResultJSON
	ac.post(fmt.Sprintf("/channels/%d/price-observations", f.channelID), map[string]any{
		"source_id": f.sourceID, "level": "L2", "upstream_model": "b2-up-" + f.suffix, "currency": f.currency,
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "1.5"}, {"meter": "output", "unit": "per_1m_tokens", "unit_price": "3"}},
	}, &res)
	if res.ChangeRequestID == nil || res.Decision != "pending" {
		t.Fatalf("ingest = %+v, want pending", res)
	}

	statuses := parallel(ac, 8, http.MethodPost, fmt.Sprintf("/price-change-requests/%d/approve", *res.ChangeRequestID), map[string]any{"reason": "并发"})
	if ok := countStatus(statuses, http.StatusOK); ok != 1 {
		t.Fatalf("approve statuses = %v, want exactly one 200", statuses)
	}
	if conflicts := countStatus(statuses, http.StatusConflict); conflicts != 7 {
		t.Errorf("approve statuses = %v, want seven 409", statuses)
	}
	var books int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM price_books WHERE kind = 'cost' AND channel_id = $1`, f.channelID).Scan(&books); err != nil {
		t.Fatalf("count books: %v", err)
	}
	if books != 2 { // 初始成本价 + 这一次批准
		t.Errorf("cost books = %d, want 2", books)
	}
	var audits int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM admin_audit_logs WHERE action = 'price_change.approve' AND target_id = $1`, fmt.Sprint(*res.ChangeRequestID)).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 1 {
		t.Errorf("approve audits = %d, want 1 (audit is written in the same transaction as the approval)", audits)
	}
}

func TestB6_ConcurrentDisableKeepsOneActiveChannel(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	var ch2 idResp
	ac.post("/channels", map[string]any{"virtual_model_id": f.vmID, "provider_account_id": f.accountID, "upstream_model": "b2-up2-" + f.suffix, "priority": 2}, &ch2)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, id := range []int64{f.channelID, ch2.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], _ = ac.do(http.MethodPatch, fmt.Sprintf("/channels/%d", id), map[string]any{"status": "disabled"}, nil)
		}()
	}
	wg.Wait()
	if countStatus(statuses, http.StatusOK) != 1 || countStatus(statuses, http.StatusConflict) != 1 {
		t.Fatalf("disable statuses = %v, want one 200 and one 409 (last active channel)", statuses)
	}
	var vm admin.VirtualModelDetail
	ac.get(fmt.Sprintf("/virtual-models/%d", f.vmID), &vm)
	if vm.ActiveChannelCount != 1 {
		t.Errorf("active channels = %d, want 1", vm.ActiveChannelCount)
	}
}

func TestB6_CreditGrantIsIdempotentByRefID(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	var acct idResp
	ac.post("/accounts", map[string]any{"type": "personal", "name": fmt.Sprint("b6-grant-", time.Now().UnixNano())}, &acct)
	path := fmt.Sprintf("/accounts/%d/credit-grants", acct.ID)
	body := map[string]any{"source": "compensation", "amount_micro": 1_000_000, "ref_id": fmt.Sprint("b6-", time.Now().UnixNano()), "reason": "补偿"}

	statuses := parallel(ac, 5, http.MethodPost, path, body)
	if countStatus(statuses, http.StatusOK) != 1 || countStatus(statuses, http.StatusConflict) != 4 {
		t.Fatalf("grant statuses = %v, want one 200 and four 409", statuses)
	}
	var detail struct {
		Wallet admin.WalletSummary `json:"wallet"`
	}
	ac.get(fmt.Sprintf("/accounts/%d", acct.ID), &detail)
	if detail.Wallet.BonusBalance != 1_000_000 {
		t.Errorf("bonus = %d, want 1000000 (granted once)", detail.Wallet.BonusBalance)
	}
	// 不带 ref_id 被拒绝（自动生成的 ref_id 会让重试重复发放）。
	if status, _ := ac.do(http.MethodPost, path, map[string]any{"source": "compensation", "amount_micro": 1, "reason": "x"}, nil); status != http.StatusBadRequest {
		t.Errorf("grant without ref_id: status = %d, want 400", status)
	}
}

func TestB6_IfMatchPreventsLostUpdates(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	path := fmt.Sprintf("/channels/%d", f.channelID)

	req, _ := http.NewRequest(http.MethodGet, ac.baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+ac.authToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("channel detail has no ETag")
	}

	// 第一个运营基于 etag 改权重：成功，拿到新 ETag。
	status, body := ac.do(http.MethodPatch, path, map[string]any{"weight": 70}, map[string]string{"If-Match": etag})
	if status != http.StatusOK {
		t.Fatalf("first patch: %d %s", status, body)
	}
	// 第二个运营还拿着旧 etag：412，不会覆盖。
	status, body = ac.do(http.MethodPatch, path, map[string]any{"weight": 30}, map[string]string{"If-Match": etag})
	if status != http.StatusPreconditionFailed {
		t.Fatalf("stale patch: %d %s, want 412", status, body)
	}
	var ch admin.ChannelDetail
	ac.get(path, &ch)
	if ch.Weight != 70 {
		t.Errorf("weight = %d, want 70 (stale update must not win)", ch.Weight)
	}
	if status, _ := ac.do(http.MethodPatch, path, map[string]any{"weight": 30}, map[string]string{"If-Match": "garbage"}); status != http.StatusBadRequest {
		t.Errorf("malformed If-Match: status = %d, want 400", status)
	}
}

func TestB6_PriceBooksFormAChain(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	sell := func(price string) int64 {
		var out struct {
			PriceBookID int64 `json:"price_book_id"`
		}
		ac.post(fmt.Sprintf("/virtual-models/%d/sell-price", f.vmID), map[string]any{"components": []map[string]any{
			{"meter": "input", "unit": "per_1m_tokens", "unit_price": price},
		}}, &out)
		return out.PriceBookID
	}
	second := sell("11")
	third := sell("12")

	rows, err := pool.Query(t.Context(),
		`SELECT id, effective_from, effective_to FROM price_books WHERE kind = 'sell' AND virtual_model_id = $1 ORDER BY effective_from, id`, f.vmID)
	if err != nil {
		t.Fatalf("query books: %v", err)
	}
	type book struct {
		id   int64
		from time.Time
		to   *time.Time
	}
	var books []book
	for rows.Next() {
		var b book
		if err := rows.Scan(&b.id, &b.from, &b.to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		books = append(books, b)
	}
	if len(books) != 3 || books[1].id != second || books[2].id != third {
		t.Fatalf("books = %+v, want fixture book + %d + %d", books, second, third)
	}
	for i := 0; i < 2; i++ {
		if books[i].to == nil || !books[i].to.Equal(books[i+1].from) {
			t.Errorf("book %d effective_to = %v, want next book's effective_from %v", books[i].id, books[i].to, books[i+1].from)
		}
	}
	if books[2].to != nil {
		t.Errorf("latest book effective_to = %v, want nil (open-ended)", books[2].to)
	}

	// 审计的 before 是被取代的那一本。
	var audit struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.get(fmt.Sprintf("/audit-logs?target_type=virtual_model&target_id=%d&action=sell_price.set&limit=1", f.vmID), &audit)
	var before struct {
		ID int64 `json:"id"`
	}
	if len(audit.Data) != 1 || json.Unmarshal(audit.Data[0].Before, &before) != nil || before.ID != second {
		t.Errorf("sell_price.set audit before = %s, want the superseded book %d", audit.Data[0].Before, second)
	}

	// 数据库兜底：直接插入与当前版本重叠的价格本会被排他约束拒绝。
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO price_books (kind, virtual_model_id, currency, effective_from) VALUES ('sell', $1, 'CNY', now() - interval '1 hour')`, f.vmID); err == nil {
		t.Error("overlapping price book insert succeeded, want exclusion violation")
	}
}

func TestB6_AppendOnlyTablesRejectMutation(t *testing.T) {
	pool := testPool(t)
	for _, stmt := range []string{
		`UPDATE admin_audit_logs SET action = 'tampered' WHERE id = (SELECT max(id) FROM admin_audit_logs)`,
		`DELETE FROM admin_audit_logs WHERE id = (SELECT max(id) FROM admin_audit_logs)`,
		`TRUNCATE ledger_entries`,
	} {
		if _, err := pool.Exec(t.Context(), stmt); err == nil {
			t.Errorf("%s succeeded, want it to be rejected", stmt)
		}
	}
}
