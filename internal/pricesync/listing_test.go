package pricesync

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

func seedProviderOnly(t *testing.T, adminSvc *admin.Service) int64 {
	t.Helper()
	provider, err := adminSvc.CreateProvider(context.Background(), admin.CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	return provider.ID
}

func TestEngine_IngestUnmapped_NoMatchingChannel_CreatesPendingListing(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	ctx := context.Background()
	providerID := seedProviderOnly(t, adminSvc)

	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L2','manual','test') RETURNING id`, providerID).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}

	model := uniqueCode(t)
	result, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: providerID, SourceID: sourceID, Level: LevelL2, UpstreamModel: model,
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(1)},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped: %v", err)
	}
	if result.ListingID == nil {
		t.Fatal("expected a pending listing when no channel matches")
	}
	if result.MappedResults != nil {
		t.Errorf("MappedResults = %v, want nil", result.MappedResults)
	}

	var status, upstreamModel string
	if err := pool.QueryRow(ctx, `SELECT status, upstream_model FROM pending_model_listings WHERE id = $1`, *result.ListingID).
		Scan(&status, &upstreamModel); err != nil {
		t.Fatalf("query pending_model_listings: %v", err)
	}
	if status != "pending" || upstreamModel != model {
		t.Errorf("status=%q upstream_model=%q, want pending/%s", status, upstreamModel, model)
	}
}

func TestEngine_IngestUnmapped_RepeatedObservation_UpdatesSameRow(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	ctx := context.Background()
	providerID := seedProviderOnly(t, adminSvc)

	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L2','manual','test') RETURNING id`, providerID).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}
	model := uniqueCode(t)

	first, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: providerID, SourceID: sourceID, Level: LevelL2, UpstreamModel: model,
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(1)},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped (first): %v", err)
	}
	second, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: providerID, SourceID: sourceID, Level: LevelL2, UpstreamModel: model,
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(2)},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped (second): %v", err)
	}
	if *first.ListingID != *second.ListingID {
		t.Errorf("expected the same listing ID to be reused, got %d and %d", *first.ListingID, *second.ListingID)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pending_model_listings WHERE provider_id = $1 AND upstream_model = $2`, providerID, model).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (repeated observations must upsert, not duplicate)", count)
	}
}

func TestEngine_IngestUnmapped_MatchingChannel_DelegatesToNormalIngest(t *testing.T) {
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
	vm, err := adminSvc.CreateVirtualModel(ctx, admin.CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1000, MaxOutput: 100})
	if err != nil {
		t.Fatalf("CreateVirtualModel: %v", err)
	}
	model := uniqueCode(t)
	ch, err := adminSvc.CreateChannel(ctx, admin.CreateChannelInput{VirtualModelID: vm.ID, ProviderAccountID: acc.ID, UpstreamModel: model})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L2','manual','test') RETURNING id`, provider.ID).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}

	result, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: provider.ID, SourceID: sourceID, Level: LevelL2, UpstreamModel: model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(5)},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped: %v", err)
	}
	if result.ListingID != nil {
		t.Errorf("ListingID = %v, want nil (a matching channel exists, should not queue as unmapped)", result.ListingID)
	}
	if len(result.MappedResults) != 1 {
		t.Fatalf("MappedResults = %+v, want exactly 1", result.MappedResults)
	}
	// 这个渠道之前从没配过成本价，direction=new，DecidePolicy 对 new 一律 pending
	// （即便来源是 L2）——这里主要验证的是"找到了匹配渠道就走正常 Ingest 流程"，
	// 不是这条具体的策略判断（那部分已经在 TestDecidePolicy_MixedNewRemoved_AlwaysPending 里测过）。
	if result.MappedResults[0].Decision != DecisionPending {
		t.Errorf("Decision = %q, want pending (direction=new)", result.MappedResults[0].Decision)
	}
	if result.MappedResults[0].ChangeRequestID == nil {
		t.Error("expected a change request to be created via the normal Ingest path")
	}
	_ = ch
}

func TestEngine_DismissListing(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	ctx := context.Background()
	providerID := seedProviderOnly(t, adminSvc)

	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L2','manual','test') RETURNING id`, providerID).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}
	result, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: providerID, SourceID: sourceID, Level: LevelL2, UpstreamModel: uniqueCode(t),
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(1)},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped: %v", err)
	}

	if err := e.DismissListing(ctx, *result.ListingID); err != nil {
		t.Fatalf("DismissListing: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM pending_model_listings WHERE id = $1`, *result.ListingID).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "dismissed" {
		t.Errorf("status = %q, want dismissed", status)
	}

	if err := e.DismissListing(ctx, *result.ListingID); err != ErrListingNotPending {
		t.Errorf("second Dismiss error = %v, want ErrListingNotPending", err)
	}
	if err := e.DismissListing(ctx, -1); err != ErrListingNotFound {
		t.Errorf("Dismiss(-1) error = %v, want ErrListingNotFound", err)
	}
}

func TestEngine_PublishListing_CreatesVirtualModelChannelAndPrices(t *testing.T) {
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
	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L2','manual','test') RETURNING id`, provider.ID).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}

	model := uniqueCode(t)
	ingestResult, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: provider.ID, SourceID: sourceID, Level: LevelL2, UpstreamModel: model,
		Spec: PriceSpec{Currency: "CNY", Components: inputComponent(10)},
		Meta: &ModelMeta{Name: "Acme: Test Model (free)", Description: "A **test** model.\n\nMore.", Source: "test_fetcher"},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped: %v", err)
	}

	result, err := e.PublishListing(ctx, *ingestResult.ListingID, PublishListingInput{
		VirtualModel: admin.CreateVirtualModelInput{
			Name: uniqueCode(t), Type: "chat", ContextWindow: 128000, MaxOutput: 8192,
		},
		ProviderAccountID: acc.ID,
		SellMarkup:        decimal.NewFromFloat(0.5), // +50%
	})
	if err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if result.VirtualModelID == 0 || result.ChannelID == 0 || result.CostBookID == 0 || result.SellBookID == 0 {
		t.Fatalf("PublishListing result has a zero ID: %+v", result)
	}

	// 新虚拟模型没有展示元数据：按观测到的外部目录参数自动生成一条，评分留空。
	if !result.MetadataCreated {
		t.Errorf("MetadataCreated = false, want true")
	}
	var displayName, providerDisplay, description string
	var scoresNull bool
	if err := pool.QueryRow(ctx,
		`SELECT display_name, provider_display, description, scores IS NULL FROM virtual_model_metadata WHERE virtual_model_id = $1`, result.VirtualModelID,
	).Scan(&displayName, &providerDisplay, &description, &scoresNull); err != nil {
		t.Fatalf("query virtual_model_metadata: %v", err)
	}
	if displayName != "Test Model" || providerDisplay != "Acme" || description != "A test model." || !scoresNull {
		t.Errorf("metadata = %q/%q/%q scoresNull=%v, want Test Model/Acme/A test model./true", displayName, providerDisplay, description, scoresNull)
	}
	// 已有元数据时不覆盖。
	if created, err := adminSvc.EnsureVirtualModelMetadata(ctx, result.VirtualModelID); err != nil || created {
		t.Errorf("second EnsureVirtualModelMetadata = %v, %v; want false, nil", created, err)
	}

	var channelUpstreamModel string
	if err := pool.QueryRow(ctx, `SELECT upstream_model FROM channels WHERE id = $1`, result.ChannelID).Scan(&channelUpstreamModel); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if channelUpstreamModel != model {
		t.Errorf("channel upstream_model = %q, want %q", channelUpstreamModel, model)
	}

	var costPrice, sellPrice decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT unit_price FROM price_components WHERE price_book_id = $1`, result.CostBookID).Scan(&costPrice); err != nil {
		t.Fatalf("query cost price: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT unit_price FROM price_components WHERE price_book_id = $1`, result.SellBookID).Scan(&sellPrice); err != nil {
		t.Fatalf("query sell price: %v", err)
	}
	if !costPrice.Equal(decimal.NewFromInt(10)) {
		t.Errorf("cost price = %s, want 10", costPrice)
	}
	if !sellPrice.Equal(decimal.NewFromInt(15)) {
		t.Errorf("sell price = %s, want 15 (10 * 1.5 markup)", sellPrice)
	}

	var status string
	var publishedVMID, publishedChannelID int64
	if err := pool.QueryRow(ctx, `SELECT status, published_virtual_model_id, published_channel_id FROM pending_model_listings WHERE id = $1`, *ingestResult.ListingID).
		Scan(&status, &publishedVMID, &publishedChannelID); err != nil {
		t.Fatalf("query pending_model_listings: %v", err)
	}
	if status != "published" || publishedVMID != result.VirtualModelID || publishedChannelID != result.ChannelID {
		t.Errorf("status=%q vm=%d channel=%d, want published/%d/%d", status, publishedVMID, publishedChannelID, result.VirtualModelID, result.ChannelID)
	}

	// 已经发布过的候选不能再发布一次。
	if _, err := e.PublishListing(ctx, *ingestResult.ListingID, PublishListingInput{
		VirtualModel:      admin.CreateVirtualModelInput{Name: uniqueCode(t), Type: "chat", ContextWindow: 1, MaxOutput: 1},
		ProviderAccountID: acc.ID,
	}); err != ErrListingNotPending {
		t.Errorf("second PublishListing error = %v, want ErrListingNotPending", err)
	}
}

func TestEngine_PublishListing_NotFound(t *testing.T) {
	pool := testPool(t)
	e, _ := newEngine(t, pool)
	if _, err := e.PublishListing(context.Background(), -1, PublishListingInput{}); err != ErrListingNotFound {
		t.Errorf("error = %v, want ErrListingNotFound", err)
	}
}

func TestEngine_PublishListing_RejectsNegativeMarkup(t *testing.T) {
	pool := testPool(t)
	e, adminSvc := newEngine(t, pool)
	ctx := context.Background()
	providerID := seedProviderOnly(t, adminSvc)

	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_sources (provider_id, level, kind, fetcher) VALUES ($1,'L2','manual','test') RETURNING id`, providerID).Scan(&sourceID); err != nil {
		t.Fatalf("insert price_source: %v", err)
	}
	result, err := e.IngestUnmapped(ctx, UnmappedObservationInput{
		ProviderID: providerID, SourceID: sourceID, Level: LevelL2, UpstreamModel: uniqueCode(t),
		Spec: PriceSpec{Currency: "USD", Components: inputComponent(1)},
	})
	if err != nil {
		t.Fatalf("IngestUnmapped: %v", err)
	}

	if _, err := e.PublishListing(ctx, *result.ListingID, PublishListingInput{SellMarkup: decimal.NewFromFloat(-0.1)}); err == nil {
		t.Error("expected an error for a negative sell_markup")
	}
}
