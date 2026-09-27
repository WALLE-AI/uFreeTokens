// 运营后台 B2 批次接口（列表 / 详情 / 编辑 / 调价详情 / 批量审批 / 待办计数）的
// 端到端测试，见 docs/cmd-admin 运营后台接口补全技术方案.md §1、§2、§5、§7。
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

// b2Fixture 通过 HTTP 建一套最小可用配置：供应商 → 账号（倍率 1.2）→ 密钥 →
// 虚拟模型 → 渠道（USD 成本价）→ CNY 售价，外加一条 USD→CNY 汇率。
type b2Fixture struct {
	suffix                                 string
	providerID, accountID, vmID, channelID int64
	sourceID                               int64
	vmName, currency                       string
}

type idResp struct {
	ID int64 `json:"id"`
}

func newB2Fixture(t *testing.T, ac *adminClient) b2Fixture {
	t.Helper()
	f := b2Fixture{suffix: fmt.Sprintf("%d", time.Now().UnixNano())}
	f.vmName = "b2-vm-" + f.suffix
	var p, a, vm, ch, src idResp
	ac.post("/providers", map[string]any{"code": "b2-" + f.suffix, "name": "B2 Provider", "protocol": "openai"}, &p)
	ac.post("/provider-accounts", map[string]any{"provider_id": p.ID, "name": "b2-acc", "base_url": "https://b2.example", "cost_multiplier": "1.2"}, &a)
	ac.post(fmt.Sprintf("/provider-accounts/%d/keys", a.ID), map[string]any{"secret": "sk-b2-secret-9f2a", "weight": 100}, nil)
	ac.post("/virtual-models", map[string]any{"name": f.vmName, "family": "b2", "type": "chat", "context_window": 64000, "max_output": 4096}, &vm)
	ac.post("/channels", map[string]any{"virtual_model_id": vm.ID, "provider_account_id": a.ID, "upstream_model": "b2-up-" + f.suffix, "priority": 1}, &ch)
	// 用本次测试专属的假币种（同 internal/catalog 的测试做法）：fx_rates 按 (base, quote, 日期)
	// upsert，用真实的 USD 会和并行跑的其他测试/手工联调互相覆盖。
	f.currency = "T" + f.suffix[len(f.suffix)-6:]
	ac.post("/fx-rates", map[string]any{"base": f.currency, "quote": "CNY", "rate": "7"}, nil)
	// 成本 1 / 2（假币种）每百万 token → ×7 汇率 ×1.2 倍率 = ¥8.4 / ¥16.8；售价 ¥10 / ¥20 → 毛利率 16%。
	ac.post(fmt.Sprintf("/channels/%d/cost-price", ch.ID), map[string]any{"currency": f.currency, "components": []map[string]any{
		{"meter": "input", "unit": "per_1m_tokens", "unit_price": "1"}, {"meter": "output", "unit": "per_1m_tokens", "unit_price": "2"},
	}}, nil)
	ac.post(fmt.Sprintf("/virtual-models/%d/sell-price", vm.ID), map[string]any{"components": []map[string]any{
		{"meter": "input", "unit": "per_1m_tokens", "unit_price": "10"}, {"meter": "output", "unit": "per_1m_tokens", "unit_price": "20"},
	}}, nil)
	ac.post("/price-sources", map[string]any{"provider_id": p.ID, "level": "L2", "kind": "manual", "fetcher": "b2-test"}, &src)
	f.providerID, f.accountID, f.vmID, f.channelID, f.sourceID = p.ID, a.ID, vm.ID, ch.ID, src.ID
	return f
}

func TestAdminConsole_ListsAndDetails(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)

	var providers admin.Page[admin.ProviderSummary]
	ac.get("/providers?q="+url.QueryEscape("b2-"+f.suffix), &providers)
	if providers.Total != 1 || providers.Data[0].ID != f.providerID || providers.Data[0].AccountCount != 1 ||
		providers.Data[0].ActiveKeyCount != 1 || providers.Data[0].ChannelCount != 1 {
		t.Fatalf("providers = %+v, want the fixture provider with 1 account / 1 key / 1 channel", providers)
	}
	if status, _ := ac.do(http.MethodGet, "/providers?sort=bogus", nil, nil); status != http.StatusBadRequest {
		t.Errorf("unsupported sort: status = %d, want 400", status)
	}

	var pa admin.ProviderAccountDetail
	ac.get(fmt.Sprintf("/provider-accounts/%d", f.accountID), &pa)
	if len(pa.Keys) != 1 || pa.Keys[0].Last4 != "9f2a" || pa.CostMultiplier.String() != "1.2" {
		t.Fatalf("provider account = %+v, want 1 key ending 9f2a and multiplier 1.2", pa)
	}

	// 虚拟模型列表：兼容旧的 ?name= 精确查找，同时支持列表检索。
	var byName admin.VirtualModel
	ac.get("/virtual-models?name="+url.QueryEscape(f.vmName), &byName)
	if byName.ID != f.vmID {
		t.Fatalf("GET ?name= returned %+v, want id %d", byName, f.vmID)
	}
	var vms admin.Page[admin.VirtualModelSummary]
	ac.get("/virtual-models?q="+url.QueryEscape(f.vmName), &vms)
	if vms.Total != 1 || vms.Data[0].SellPrice == nil || vms.Data[0].MinMarginRatio == nil || vms.Data[0].MinMarginRatio.String() != "0.16" {
		t.Fatalf("virtual models = %+v, want sell price and min_margin_ratio 0.16", vms.Data)
	}

	var ch admin.ChannelDetail
	ac.get(fmt.Sprintf("/channels/%d", f.channelID), &ch)
	if ch.CostPriceCNY == nil || ch.CostPriceCNY.Input == nil || ch.CostPriceCNY.Input.String() != "8.4" ||
		ch.MarginRatio == nil || ch.MarginRatio.String() != "0.16" || len(ch.CostPriceHistory) != 1 || !ch.CostPriceHistory[0].IsCurrent {
		t.Fatalf("channel detail = %+v, want cost ¥8.4 input, margin 0.16, one current cost book", ch)
	}

	var vm admin.VirtualModelDetail
	ac.get(fmt.Sprintf("/virtual-models/%d", f.vmID), &vm)
	if vm.SellBook == nil || len(vm.SellBook.Components) != 2 || len(vm.Channels) != 1 || vm.Metadata != nil {
		t.Fatalf("vm detail = %+v, want current sell book with 2 components, 1 channel, no metadata", vm)
	}

	var sources struct {
		Data []admin.PriceSourceInfo `json:"data"`
	}
	ac.get(fmt.Sprintf("/price-sources?provider_id=%d", f.providerID), &sources)
	if len(sources.Data) != 1 || sources.Data[0].ID != f.sourceID {
		t.Fatalf("price sources = %+v", sources.Data)
	}
}

func TestAdminConsole_PatchWritesAuditAndGuardsLastChannel(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	actor := map[string]string{"X-Actor-Name": url.QueryEscape("王五")}

	status, body := ac.do(http.MethodPatch, fmt.Sprintf("/channels/%d", f.channelID), map[string]any{"weight": 50, "priority": 3}, actor)
	if status != http.StatusOK {
		t.Fatalf("patch channel: %d %s", status, body)
	}
	var updated admin.ChannelDetail
	_ = json.Unmarshal(body, &updated)
	if updated.Weight != 50 || updated.Priority != 3 {
		t.Errorf("patched channel = weight %d priority %d, want 50/3", updated.Weight, updated.Priority)
	}

	var audit struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.get(fmt.Sprintf("/audit-logs?target_type=channel&target_id=%d&action=channel.update", f.channelID), &audit)
	if len(audit.Data) != 1 || audit.Data[0].ActorName != "王五" {
		t.Fatalf("audit = %+v, want one channel.update by 王五", audit.Data)
	}
	var before, after map[string]any
	_ = json.Unmarshal(audit.Data[0].Before, &before)
	_ = json.Unmarshal(audit.Data[0].After, &after)
	if fmt.Sprint(before["weight"]) != "100" || fmt.Sprint(after["weight"]) != "50" || fmt.Sprint(before["priority"]) != "1" {
		t.Errorf("audit before/after = %v / %v, want weight 100→50 and priority 1→…", before, after)
	}

	// 它是这个虚拟模型唯一的 active 渠道：不带 force 停用必须 409。
	if status, body := ac.do(http.MethodPatch, fmt.Sprintf("/channels/%d", f.channelID), map[string]any{"status": "disabled"}, nil); status != http.StatusConflict {
		t.Errorf("disable last channel without force: %d %s, want 409", status, body)
	}
	if status, body := ac.do(http.MethodPatch, fmt.Sprintf("/channels/%d", f.channelID), map[string]any{"status": "disabled", "force": true}, nil); status != http.StatusOK {
		t.Errorf("disable last channel with force: %d %s", status, body)
	}

	if status, _ := ac.do(http.MethodPatch, fmt.Sprintf("/virtual-models/%d", f.vmID), map[string]any{"status": "bogus"}, nil); status != http.StatusBadRequest {
		t.Errorf("invalid status: %d, want 400", status)
	}
	if status, _ := ac.do(http.MethodPatch, fmt.Sprintf("/virtual-models/%d", f.vmID), map[string]any{}, nil); status != http.StatusBadRequest {
		t.Errorf("empty patch: %d, want 400", status)
	}
	if status, body := ac.do(http.MethodPatch, fmt.Sprintf("/virtual-models/%d", f.vmID), map[string]any{"status": "hidden"}, nil); status != http.StatusOK {
		t.Errorf("hide model: %d %s", status, body)
	}
	if status, _ := ac.do(http.MethodPatch, "/virtual-models/999999999", map[string]any{"status": "hidden"}, nil); status != http.StatusNotFound {
		t.Errorf("patch missing model: %d, want 404", status)
	}

	// 上游密钥：吊销后不能再修改。
	var pa admin.ProviderAccountDetail
	ac.get(fmt.Sprintf("/provider-accounts/%d", f.accountID), &pa)
	keyID := pa.Keys[0].ID
	if status, body := ac.do(http.MethodPost, fmt.Sprintf("/provider-keys/%d/revoke", keyID), nil, nil); status != http.StatusOK {
		t.Fatalf("revoke provider key: %d %s", status, body)
	}
	if status, _ := ac.do(http.MethodPatch, fmt.Sprintf("/provider-keys/%d", keyID), map[string]any{"weight": 10}, nil); status != http.StatusConflict {
		t.Errorf("patch revoked key: %d, want 409", status)
	}

	var prov struct {
		Status                 string `json:"status"`
		AffectedActiveChannels int    `json:"affected_active_channels"`
	}
	status, body = ac.do(http.MethodPatch, fmt.Sprintf("/providers/%d", f.providerID), map[string]any{"status": "disabled"}, nil)
	_ = json.Unmarshal(body, &prov)
	if status != http.StatusOK || prov.Status != "disabled" || prov.AffectedActiveChannels != 0 {
		t.Errorf("disable provider: %d %s", status, body)
	}
}

func TestAdminConsole_ChangeRequestDetailAndBatchApprove(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	observe := func(in, out string) ingestResultJSON {
		var res ingestResultJSON
		ac.post(fmt.Sprintf("/channels/%d/price-observations", f.channelID), map[string]any{
			"source_id": f.sourceID, "level": "L2", "upstream_model": "b2-up-" + f.suffix, "currency": f.currency, "raw_object": "evidence-" + in,
			"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": in}, {"meter": "output", "unit": "per_1m_tokens", "unit_price": out}},
		}, &res)
		return res
	}

	// 涨价 50%：超过自动通过阈值 → pending。
	up := observe("1.5", "3")
	if up.ChangeRequestID == nil || up.Decision != "pending" {
		t.Fatalf("ingest = %+v, want pending", up)
	}
	var d admin.ChangeRequestDetail
	ac.get(fmt.Sprintf("/price-change-requests/%d", *up.ChangeRequestID), &d)
	if d.VirtualModelName != f.vmName || d.Currency != f.currency || len(d.Components) != 2 || len(d.Evidence) != 1 || d.CurrentBook == nil {
		t.Fatalf("detail = %+v, want 2 component diffs, 1 evidence, current book", d)
	}
	// 新成本 1.5 → ¥12.6 vs 售价 ¥10 → 毛利 -26%；之前是 16%。
	if d.Impact == nil || d.Impact.MarginBefore == nil || d.Impact.MarginBefore.String() != "0.16" ||
		d.Impact.MarginAfter == nil || d.Impact.MarginAfter.String() != "-0.26" {
		t.Fatalf("impact = %+v, want margin 0.16 → -0.26", d.Impact)
	}

	var list admin.Page[admin.ChangeRequestSummary]
	ac.get(fmt.Sprintf("/price-change-requests?channel_id=%d", f.channelID), &list)
	if list.Total != 1 || list.Data[0].ID != *up.ChangeRequestID || list.Data[0].ProviderCode != "b2-"+f.suffix {
		t.Fatalf("list = %+v", list)
	}

	// 批量批准：阈值 0.2 < 实际 0.5，应逐条拒绝而不是整体失败。
	var batch struct {
		Results []struct {
			ID    int64 `json:"id"`
			OK    bool  `json:"ok"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"results"`
	}
	ac.post("/price-change-requests/batch-approve", map[string]any{"ids": []int64{*up.ChangeRequestID, 999999999}, "max_abs_change_ratio": "0.2"}, &batch)
	if len(batch.Results) != 2 || batch.Results[0].OK || batch.Results[0].Error == nil || batch.Results[1].Error == nil || batch.Results[1].Error.Code != "not_found" {
		t.Fatalf("batch = %+v, want both items rejected individually", batch.Results)
	}
	if status, _ := ac.do(http.MethodPost, "/price-change-requests/batch-approve", map[string]any{"ids": []int64{1}, "max_abs_change_ratio": "0.5"}, nil); status != http.StatusBadRequest {
		t.Errorf("threshold above 0.2: %d, want 400", status)
	}

	ac.post(fmt.Sprintf("/price-change-requests/%d/reject", *up.ChangeRequestID), map[string]any{"reason": "等官方公告"}, nil)
	var history admin.Page[admin.ChangeRequestSummary]
	ac.get(fmt.Sprintf("/price-change-requests?channel_id=%d&status=all", f.channelID), &history)
	if history.Total != 1 || history.Data[0].Status != "rejected" || history.Data[0].DecisionReason == nil || *history.Data[0].DecisionReason != "等官方公告" {
		t.Fatalf("history = %+v, want the rejected request with its reason", history.Data)
	}
}

func TestAdminConsole_PendingListingsAndTodoCounts(t *testing.T) {
	ac, done := newAdminTestServer(t, true)
	defer done()
	f := newB2Fixture(t, ac)

	model := "Org/NewModel-7B-" + f.suffix
	ac.post(fmt.Sprintf("/providers/%d/price-observations", f.providerID), map[string]any{
		"source_id": f.sourceID, "level": "L2", "upstream_model": model, "currency": f.currency,
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "0.5"}},
	}, nil)
	var listings admin.Page[admin.PendingListing]
	ac.get(fmt.Sprintf("/pending-model-listings?provider_id=%d", f.providerID), &listings)
	if listings.Total != 1 {
		t.Fatalf("listings = %+v, want 1", listings)
	}
	l := listings.Data[0]
	if l.UpstreamModel != model || l.Suggested.Family != "newmodel" || l.Suggested.InputPrice == nil || l.Suggested.InputPrice.String() != "0.5" ||
		len(l.ObservedSpec.Components) != 1 || l.ObservedSpec.Components[0].Meter != "input" || l.SourceLevel != "L2" {
		t.Fatalf("listing = %+v", l)
	}

	// 上架：观测价 0.5（假币种，汇率 7）× 账号倍率 1.2 × (1 + 25%) = ¥5.25。
	// 回归测试：之前直接用观测价 × 1.25 当人民币售价，外币模型会亏本上架。
	var published struct {
		VirtualModelID int64 `json:"virtual_model_id"`
	}
	ac.post(fmt.Sprintf("/pending-model-listings/%d/publish", l.ID), map[string]any{
		"virtual_model":       map[string]any{"name": "b2-pub-" + f.suffix, "family": "newmodel", "type": "chat", "context_window": 32000, "max_output": 4096},
		"provider_account_id": f.accountID,
		"sell_markup":         "0.25",
	}, &published)
	var vm admin.VirtualModelDetail
	ac.get(fmt.Sprintf("/virtual-models/%d", published.VirtualModelID), &vm)
	if vm.SellPrice == nil || vm.SellPrice.Currency != "CNY" || vm.SellPrice.Input == nil || vm.SellPrice.Input.String() != "5.25" {
		t.Fatalf("published sell price = %+v, want CNY 5.25", vm.SellPrice)
	}

	// 没有汇率的币种：必须整体失败，且不留下半上架的虚拟模型。
	orphan := "NoFX-" + f.suffix
	ac.post(fmt.Sprintf("/providers/%d/price-observations", f.providerID), map[string]any{
		"source_id": f.sourceID, "level": "L2", "upstream_model": orphan, "currency": "ZZZ" + f.suffix[len(f.suffix)-4:],
		"components": []map[string]any{{"meter": "input", "unit": "per_1m_tokens", "unit_price": "1"}},
	}, nil)
	var pending admin.Page[admin.PendingListing]
	ac.get(fmt.Sprintf("/pending-model-listings?provider_id=%d", f.providerID), &pending)
	var orphanID int64
	for _, p := range pending.Data {
		if p.UpstreamModel == orphan {
			orphanID = p.ID
		}
	}
	status, body := ac.do(http.MethodPost, fmt.Sprintf("/pending-model-listings/%d/publish", orphanID), map[string]any{
		"virtual_model":       map[string]any{"name": "b2-nofx-" + f.suffix, "family": "x", "type": "chat", "context_window": 1, "max_output": 1},
		"provider_account_id": f.accountID, "sell_markup": "0.25",
	}, nil)
	if status != http.StatusBadRequest {
		t.Errorf("publish without fx rate: %d %s, want 400", status, body)
	}
	var none admin.Page[admin.VirtualModelSummary]
	ac.get("/virtual-models?q="+url.QueryEscape("b2-nofx-"+f.suffix), &none)
	if none.Total != 0 {
		t.Errorf("a virtual model was created despite the failed publish: %+v", none.Data)
	}

	var todo admin.TodoCounts
	ac.get("/todo-counts", &todo)
	if todo.ListingsPending < 1 {
		t.Errorf("todo counts = %+v, want listings_pending >= 1", todo)
	}
}
