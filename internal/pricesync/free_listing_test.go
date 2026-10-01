package pricesync

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// freeFixture：一个供应商 + 上游账号 + 价格源，以及该供应商下一条免费模型情报和对应的免费待上架候选。
type freeFixture struct {
	e         *Engine
	offers    *offers.Store
	accountID int64
	sourceID  int64
	model     string
	offerID   int64
	listingID int64
}

func newFreeFixture(t *testing.T) (*freeFixture, *admin.Service) {
	t.Helper()
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	ctx := context.Background()
	provider, err := adminSvc.CreateProvider(ctx, admin.CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	acc, err := adminSvc.CreateProviderAccount(ctx, admin.CreateProviderAccountInput{ProviderID: provider.ID, Name: "acc", BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}
	f := &freeFixture{e: e, offers: offers.NewStore(pool), accountID: acc.ID, model: "org/" + uniqueCode(t) + ":free"}
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L4','api','test') RETURNING id`, provider.ID).Scan(&f.sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}
	sid, zero := f.sourceID, decimal.Zero
	if f.offerID, _, err = f.offers.Upsert(ctx, offers.Candidate{SourceID: &sid, ProviderCode: provider.Code, UpstreamModel: f.model,
		OfferType: offers.TypeFreeModel, DiscountRatio: &zero, EvidenceURL: "https://x", Detection: "structured"}); err != nil {
		t.Fatalf("offers.Upsert: %v", err)
	}
	free := PriceSpec{Currency: "USD", Components: []Component{
		{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, ServiceTier: "default", UnitPrice: decimal.Zero},
		{Meter: pricing.MeterOutput, Unit: pricing.UnitPer1MTokens, ServiceTier: "default", UnitPrice: decimal.Zero},
	}}
	meta := &ModelMeta{Type: "chat", ContextWindow: 64000, MaxOutput: 8000, Capabilities: []string{"stream"}}
	var ok bool
	if f.listingID, ok, err = e.UpsertFreeListing(ctx, FreeListingInput{ProviderID: provider.ID, SourceID: f.sourceID,
		UpstreamModel: f.model, Spec: free, Meta: meta, OfferID: f.offerID}); err != nil || !ok {
		t.Fatalf("UpsertFreeListing: ok=%v err=%v", ok, err)
	}
	return f, adminSvc
}

// expireOffer 模拟"这次抓取没再看到该免费模型"。
func (f *freeFixture) expireOffer(t *testing.T) {
	t.Helper()
	if _, err := f.offers.ExpireMissingFreeModels(context.Background(), f.sourceID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("ExpireMissingFreeModels: %v", err)
	}
}

func TestFreeListing_PublishThenRetireOnExpiry(t *testing.T) {
	f, _ := newFreeFixture(t)
	ctx := context.Background()
	pool := f.e.pool

	var origin string
	var meta []byte
	if err := pool.QueryRow(ctx, `SELECT origin, observed_meta FROM pending_model_listings WHERE id = $1`, f.listingID).Scan(&origin, &meta); err != nil {
		t.Fatalf("query listing: %v", err)
	}
	if origin != "free_offer" || len(meta) == 0 {
		t.Errorf("origin=%q meta=%s, want free_offer with meta", origin, meta)
	}

	// 没有 USD 汇率也能上架：免费模型售价恒为 0。
	res, err := f.e.PublishListing(ctx, f.listingID, PublishListingInput{
		VirtualModel:      admin.CreateVirtualModelInput{Name: uniqueCode(t) + ":free", Type: "chat", ContextWindow: 64000, MaxOutput: 8000},
		ProviderAccountID: f.accountID, SellMarkup: decimal.NewFromFloat(0.3), DecidedByName: "ops",
	})
	if err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	var sellNonZero int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_components WHERE price_book_id = $1 AND unit_price <> 0`, res.SellBookID).Scan(&sellNonZero); err != nil {
		t.Fatalf("query sell price: %v", err)
	}
	var vmName string
	if err := pool.QueryRow(ctx, `SELECT name FROM virtual_models WHERE id = $1`, res.VirtualModelID).Scan(&vmName); err != nil {
		t.Fatalf("query vm name: %v", err)
	}
	if vmName != f.model {
		t.Errorf("virtual model name = %q, want upstream model %q", vmName, f.model)
	}
	if res.SellBookID == 0 || sellNonZero != 0 {
		t.Errorf("sell book %d has %d non-zero components, want a zero-priced sell book", res.SellBookID, sellNonZero)
	}
	if o, _ := f.offers.Get(ctx, f.offerID); o == nil || o.Status != "confirmed" || o.ListingID == nil || *o.ListingID != f.listingID {
		t.Errorf("offer after publish = %+v, want confirmed and linked to listing %d", o, f.listingID)
	}

	// 免费还在：什么都不动。
	if life, err := f.e.SyncFreeListings(ctx); err != nil || len(life.RetiredChannels) != 0 {
		t.Fatalf("SyncFreeListings before expiry: %+v err=%v", life, err)
	}

	f.expireOffer(t)
	life, err := f.e.SyncFreeListings(ctx)
	if err != nil {
		t.Fatalf("SyncFreeListings: %v", err)
	}
	if len(life.RetiredChannels) != 1 || life.RetiredChannels[0] != res.ChannelID ||
		len(life.DeprecatedModels) != 1 || life.DeprecatedModels[0] != res.VirtualModelID {
		t.Errorf("lifecycle = %+v, want channel %d retired and model %d deprecated", life, res.ChannelID, res.VirtualModelID)
	}
	var chStatus, vmStatus string
	if err := pool.QueryRow(ctx, `SELECT c.status, v.status FROM channels c JOIN virtual_models v ON v.id = c.virtual_model_id WHERE c.id = $1`, res.ChannelID).
		Scan(&chStatus, &vmStatus); err != nil {
		t.Fatalf("query statuses: %v", err)
	}
	if chStatus != "disabled" || vmStatus != "deprecated" {
		t.Errorf("channel=%s model=%s, want disabled/deprecated", chStatus, vmStatus)
	}

	// 只处理一次：运营手动恢复后不会再被停掉。
	if _, err := pool.Exec(ctx, `UPDATE channels SET status = 'active' WHERE id = $1`, res.ChannelID); err != nil {
		t.Fatalf("re-enable channel: %v", err)
	}
	if life, err := f.e.SyncFreeListings(ctx); err != nil || len(life.RetiredChannels) != 0 {
		t.Errorf("second SyncFreeListings = %+v err=%v, want no-op", life, err)
	}
}

// 虚拟模型名 = 上游原始模型名：已有同名虚拟模型时自动复用，只挂渠道、不动售价。
func TestFreeListing_ReusesSameNameModel(t *testing.T) {
	f, adminSvc := newFreeFixture(t)
	ctx := context.Background()
	pool := f.e.pool

	vm, err := adminSvc.CreateVirtualModel(ctx, admin.CreateVirtualModelInput{Name: f.model, Type: "chat", ContextWindow: 64000, MaxOutput: 8000})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	paid, err := adminSvc.CreateChannel(ctx, admin.CreateChannelInput{VirtualModelID: vm.ID, ProviderAccountID: f.accountID, UpstreamModel: uniqueCode(t), Priority: 1})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	res, err := f.e.PublishListing(ctx, f.listingID, PublishListingInput{ProviderAccountID: f.accountID})
	if err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if res.VirtualModelID != vm.ID || res.SellBookID != 0 || res.CostBookID == 0 {
		t.Errorf("publish result = %+v, want reused vm %d, cost book, no sell book", res, vm.ID)
	}

	f.expireOffer(t)
	life, err := f.e.SyncFreeListings(ctx)
	if err != nil {
		t.Fatalf("SyncFreeListings: %v", err)
	}
	if len(life.RetiredChannels) != 1 || len(life.DeprecatedModels) != 0 {
		t.Errorf("lifecycle = %+v, want only the free channel retired", life)
	}
	var paidStatus, vmStatus string
	if err := pool.QueryRow(ctx, `SELECT c.status, v.status FROM channels c JOIN virtual_models v ON v.id = c.virtual_model_id WHERE c.id = $1`, paid.ID).
		Scan(&paidStatus, &vmStatus); err != nil {
		t.Fatalf("query statuses: %v", err)
	}
	if paidStatus != "active" || vmStatus != "active" {
		t.Errorf("paid channel=%s model=%s, want both active", paidStatus, vmStatus)
	}
}

func TestFreeListing_PendingExpiresAndComesBack(t *testing.T) {
	f, _ := newFreeFixture(t)
	ctx := context.Background()

	f.expireOffer(t)
	life, err := f.e.SyncFreeListings(ctx)
	if err != nil || life.Expired != 1 {
		t.Fatalf("SyncFreeListings = %+v err=%v, want 1 expired listing", life, err)
	}
	status := func() string {
		var s string
		if err := f.e.pool.QueryRow(ctx, `SELECT status FROM pending_model_listings WHERE id = $1`, f.listingID).Scan(&s); err != nil {
			t.Fatalf("query listing: %v", err)
		}
		return s
	}
	if s := status(); s != "expired" {
		t.Fatalf("listing status = %s, want expired", s)
	}

	// 上游又免费了：情报回到 new，候选回到 pending。
	o, _ := f.offers.Get(ctx, f.offerID)
	sid, zero := f.sourceID, decimal.Zero
	if _, _, err := f.offers.Upsert(ctx, offers.Candidate{SourceID: &sid, ProviderCode: o.ProviderCode, UpstreamModel: f.model,
		OfferType: offers.TypeFreeModel, DiscountRatio: &zero, EvidenceURL: "https://x", Detection: "structured"}); err != nil {
		t.Fatalf("offers.Upsert: %v", err)
	}
	var providerID int64
	if err := f.e.pool.QueryRow(ctx, `SELECT provider_id FROM pending_model_listings WHERE id = $1`, f.listingID).Scan(&providerID); err != nil {
		t.Fatalf("query provider: %v", err)
	}
	if _, ok, err := f.e.UpsertFreeListing(ctx, FreeListingInput{ProviderID: providerID, SourceID: f.sourceID, UpstreamModel: f.model,
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(0)}, OfferID: f.offerID}); err != nil || !ok {
		t.Fatalf("UpsertFreeListing: ok=%v err=%v", ok, err)
	}
	if s := status(); s != "pending" {
		t.Errorf("listing status = %s, want pending again", s)
	}
}
