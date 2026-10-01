package offers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

func d(s string) *decimal.Decimal {
	v := decimal.RequireFromString(s)
	return &v
}

func TestDetectFromPrices(t *testing.T) {
	obs := []PriceObservation{
		{Model: "free-one", Current: PricePoint{Input: d("0"), Output: d("0")}},
		{Model: "cut", Current: PricePoint{Input: d("1"), Output: d("4")}, Previous: &PricePoint{Input: d("2"), Output: d("8")}},
		{Model: "small-change", Current: PricePoint{Input: d("1.9"), Output: d("7.5")}, Previous: &PricePoint{Input: d("2"), Output: d("8")}},
		{Model: "raise", Current: PricePoint{Input: d("3"), Output: d("9")}, Previous: &PricePoint{Input: d("2"), Output: d("8")}},
		{Model: "new", Current: PricePoint{Input: d("1"), Output: d("2")}},
		{Model: "only-input-zero", Current: PricePoint{Input: d("0")}},
	}
	got := DetectFromPrices(9, "openrouter", "https://x", obs)
	if len(got) != 2 {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	if got[0].UpstreamModel != "free-one" || got[0].OfferType != TypeFreeModel || !got[0].DiscountRatio.IsZero() {
		t.Errorf("free model candidate: %+v", got[0])
	}
	if got[1].UpstreamModel != "cut" || got[1].OfferType != TypePriceCut || !got[1].DiscountRatio.Equal(decimal.RequireFromString("0.5")) {
		t.Errorf("price cut candidate: %+v", got[1])
	}
	// 免费模型的指纹不随价格变化，降价的指纹带上新价格。
	a := Candidate{ProviderCode: "p", UpstreamModel: "m", OfferType: TypeFreeModel}
	b := a
	b.EvidenceExcerpt = "different evidence"
	if string(a.Fingerprint()) != string(b.Fingerprint()) {
		t.Error("free_model fingerprint must not depend on evidence")
	}
	c1 := Candidate{ProviderCode: "p", UpstreamModel: "m", OfferType: TypePriceCut, DiscountRatio: d("0.5")}
	c2 := Candidate{ProviderCode: "p", UpstreamModel: "m", OfferType: TypePriceCut, DiscountRatio: d("0.4")}
	if string(c1.Fingerprint()) == string(c2.Fingerprint()) {
		t.Error("different price cuts must have different fingerprints")
	}
}

func TestPageTextAndFilter(t *testing.T) {
	html := []byte(`<html><head><title>x</title><script>var free = 1</script></head><body>
		<h1>定价</h1><p>deepseek-chat 输入 2 元 / 百万 tokens</p>
		<div>常规说明</div><p>即日起至 2026-10-31，deepseek-chat 限时 5 折优惠</p><ul><li>其它</li><li>无关</li></ul>
		<p>Free tier: 1M tokens per month</p></body></html>`)
	text := PageText(html)
	if strings.Contains(text, "var free") {
		t.Error("script content must be removed")
	}
	got := FilterLines(text, DefaultKeywords, 1000)
	for _, want := range []string{"限时 5 折", "常规说明", "Free tier"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered text missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "无关") && !strings.Contains(got, "Free tier") {
		t.Errorf("unexpected filter output:\n%s", got)
	}
	if out := FilterLines(text, DefaultKeywords, 10); len(out) > 10 {
		t.Errorf("max chars not honoured: %d", len(out))
	}
}

func TestExtractedOfferValidation(t *testing.T) {
	page := "即日起至 2026-10-31，deepseek-chat 限时 5 折优惠\nFree tier: 1M tokens per month"
	model := "deepseek-chat"
	half, bad := 0.5, 1.5
	end := "2026-10-31"
	ok := ExtractedOffer{UpstreamModel: &model, OfferType: TypeDiscount, DiscountRatio: &half, EndsAt: &end, Evidence: "deepseek-chat 限时 5 折优惠"}
	c, valid := ok.toCandidate(1, "deepseek", "https://p", page)
	if !valid || c.EndsAt == nil || c.EndsAt.Format(time.DateOnly) != "2026-10-31" || !c.DiscountRatio.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("valid offer rejected or wrong: %+v %v", c, valid)
	}
	for name, e := range map[string]ExtractedOffer{
		"made-up evidence": {OfferType: TypeDiscount, Evidence: "全场一折"},
		"bad type":         {OfferType: "lottery", Evidence: "Free tier: 1M tokens"},
		"price_cut by llm": {OfferType: TypePriceCut, Evidence: "Free tier: 1M tokens"},
		"ratio > 1":        {OfferType: TypeDiscount, DiscountRatio: &bad, Evidence: "Free tier: 1M tokens"},
		"bad date":         {OfferType: TypeFreeQuota, EndsAt: &model, Evidence: "Free tier: 1M tokens"},
	} {
		if _, valid := e.toCandidate(1, "deepseek", "https://p", page); valid {
			t.Errorf("%s: should be rejected", name)
		}
	}
	if items, err := ParseExtraction("```json\n{\"offers\":[{\"offer_type\":\"free_quota\",\"evidence\":\"x\"}]}\n```"); err != nil || len(items) != 1 {
		t.Errorf("ParseExtraction: %v %v", items, err)
	}
	if _, err := ParseExtraction("not json"); err == nil {
		t.Error("ParseExtraction should fail on garbage")
	}
}

// ---------- 集成 ----------

const defaultTestDSN = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("UFT_TEST_PG_DSN")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: cannot create postgres pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping: postgres not reachable at %s: %v", dsn, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestStore_UpsertExpireAdopt(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	st := NewStore(pool)
	provider := fmt.Sprintf("test-%d", time.Now().UnixNano())
	var vmName string
	if err := pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output) VALUES ($1, 'x', 'chat', 1000, 100) RETURNING name`,
		provider+"/vm").Scan(&vmName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM upstream_offers WHERE provider_code = $1`, provider)
		_, _ = pool.Exec(ctx, `DELETE FROM promotions WHERE name LIKE '%' || $1 || '%'`, provider)
		_, _ = pool.Exec(ctx, `DELETE FROM virtual_models WHERE name = $1`, vmName)
	})

	zero := decimal.Zero
	free := Candidate{ProviderCode: provider, UpstreamModel: "m-free", OfferType: TypeFreeModel, DiscountRatio: &zero, Detection: "structured"}
	id, created, err := st.Upsert(ctx, free)
	if err != nil || !created {
		t.Fatalf("Upsert: %v created=%v", err, created)
	}
	if id2, created, err := st.Upsert(ctx, free); err != nil || created || id2 != id {
		t.Errorf("second Upsert should refresh the same row: id=%d created=%v err=%v", id2, created, err)
	}

	// 过期后重新出现会回到 new。
	if _, err := pool.Exec(ctx, `UPDATE upstream_offers SET status = 'expired' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert(ctx, free); err != nil {
		t.Fatal(err)
	}
	if o, _ := st.Get(ctx, id); o.Status != "new" {
		t.Errorf("reappearing offer status = %s, want new", o.Status)
	}

	past := time.Now().Add(-time.Hour)
	ended := Candidate{ProviderCode: provider, UpstreamModel: "m-old", OfferType: TypeDiscount, DiscountRatio: d("0.5"), EndsAt: &past, Detection: "manual"}
	endedID, _, err := st.Upsert(ctx, ended)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ExpireEnded(ctx); err != nil {
		t.Fatal(err)
	}
	if o, _ := st.Get(ctx, endedID); o.Status != "expired" {
		t.Errorf("ended offer status = %s, want expired", o.Status)
	}

	// 采用为对用户的折扣：免费模型（乘数 0）→ 折扣 100%。
	if _, err := st.Adopt(ctx, id, AdoptInput{Side: "sell", VirtualModel: "missing-" + provider}); !errors.Is(err, ErrAdoptInvalid) {
		t.Errorf("unknown virtual model should be rejected, got %v", err)
	}
	pid, err := st.Adopt(ctx, id, AdoptInput{Side: "sell", VirtualModel: vmName, DecidedByName: "tester"})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	var side, typ string
	var discount float64
	if err := pool.QueryRow(ctx, `SELECT side, type, (params->>'discount')::float8 FROM promotions WHERE id = $1`, pid).Scan(&side, &typ, &discount); err != nil {
		t.Fatal(err)
	}
	if side != "sell" || typ != "price_discount" || discount != 1 {
		t.Errorf("promotion = %s/%s/%v, want sell/price_discount/1", side, typ, discount)
	}
	if o, _ := st.Get(ctx, id); o.Status != "adopted" || o.AdoptedPromotionID == nil || *o.AdoptedPromotionID != pid {
		t.Errorf("offer after adopt: %+v", o)
	}
	if _, err := st.Adopt(ctx, id, AdoptInput{Side: "sell", VirtualModel: vmName}); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("adopting twice should fail, got %v", err)
	}
	if _, err := st.SetStatus(ctx, id, "ignored", "x"); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("adopted offer cannot be re-statused, got %v", err)
	}

	list, total, err := st.List(ctx, ListInput{ProviderCode: provider})
	if err != nil || total != 2 || len(list) != 2 {
		t.Errorf("List: total=%d len=%d err=%v", total, len(list), err)
	}
}
