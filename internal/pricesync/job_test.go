package pricesync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestNormalizeModelsDev(t *testing.T) {
	body := []byte(`{
	  "deepseek": {"models": {
	    "deepseek-chat": {"id": "deepseek-chat", "cost": {"input": 0.27, "output": 1.1, "cache_read": 0.07}},
	    "old": {"id": "old", "status": "deprecated", "cost": {"input": 1, "output": 2}},
	    "nocost": {"id": "nocost"}
	  }},
	  "zai": {"models": {"glm-4.5-flash": {"id": "glm-4.5-flash", "cost": {"input": 0, "output": 0}}}},
	  "other": {"models": {"x": {"id": "x", "cost": {"input": 5, "output": 5}}}}
	}`)
	obs, _, err := normalizeModelsDevResponse(body, modelsDevConfig{Providers: []string{"deepseek", "zai"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || obs[0].UpstreamModel != "deepseek/deepseek-chat" || obs[1].UpstreamModel != "zai/glm-4.5-flash" {
		t.Fatalf("unexpected observations: %+v", obs)
	}
	if len(obs[0].Spec.Components) != 3 || obs[0].Spec.Currency != "USD" {
		t.Errorf("deepseek components: %+v", obs[0].Spec)
	}
	plain, _, _ := normalizeModelsDevResponse(body, modelsDevConfig{Providers: []string{"deepseek"}}, true)
	if len(plain) != 1 || plain[0].UpstreamModel != "deepseek-chat" {
		t.Errorf("plain ids: %+v", plain)
	}
}

// openRouterBody 生成一个最小的 OpenRouter /api/v1/models 响应。
func openRouterBody(models map[string][2]string) string {
	s := `{"data":[`
	first := true
	for id, p := range models {
		if !first {
			s += ","
		}
		first = false
		s += fmt.Sprintf(`{"id":%q,"pricing":{"prompt":%q,"completion":%q}}`, id, p[0], p[1])
	}
	return s + `]}`
}

func TestJob_MarketObservationsAndOffers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	prefix := uniqueCode(t)
	free, paid := prefix+"/free-model:free", prefix+"/paid-model"

	var body atomic.Value
	body.Store(openRouterBody(map[string][2]string{free: {"0", "0"}, paid: {"0.000002", "0.000008"}}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer srv.Close()

	var srcID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_sources (domain, name, level, kind, fetcher, url, schedule, enabled, config)
		 VALUES ('price', 'or test', 'L4', 'api', 'openrouter_models', $1, '', true, '{"detect_offers": true, "offer_provider": "openrouter"}')
		 RETURNING id`, srv.URL).Scan(&srcID); err != nil {
		t.Fatal(err)
	}
	// price_observations 只追加（触发器禁止删除），来源删不掉：测试结束时停用，免得被调度器拾起。
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM upstream_offers WHERE source_id = $1`, srcID)
		_, _ = pool.Exec(ctx, `UPDATE price_sources SET enabled = false WHERE id = $1`, srcID)
	})

	adminSvc := admin.New(pool, wallet.New(pool), nil, nil)
	job := &Job{Engine: NewEngine(pool, adminSvc), Offers: offers.NewStore(pool)}
	env := datasync.NewEnv(datasync.EnvOptions{AllowPrivateNetworks: true, MinHostInterval: -1})
	load := func() datasync.Source {
		src, err := datasync.LoadSource(ctx, pool, srcID)
		if err != nil {
			t.Fatal(err)
		}
		return src
	}
	countObs := func() int {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM price_observations WHERE source_id = $1`, srcID).Scan(&n)
		return n
	}
	offerStatus := func(model, typ string) string {
		var s string
		_ = pool.QueryRow(ctx, `SELECT status FROM upstream_offers WHERE source_id = $1 AND upstream_model = $2 AND offer_type = $3`,
			srcID, model, typ).Scan(&s)
		return s
	}

	res, err := job.Run(ctx, env, load())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.ItemsFetched != 2 || countObs() != 2 || offerStatus(free, offers.TypeFreeModel) != "new" {
		t.Errorf("first run: %+v obs=%d free=%q", res, countObs(), offerStatus(free, offers.TypeFreeModel))
	}

	// 同样的内容：调度器记下 hash 后，第二次直接 unchanged。
	if _, err := pool.Exec(ctx, `UPDATE price_sources SET last_content_hash = $2 WHERE id = $1`, srcID, res.ContentHash); err != nil {
		t.Fatal(err)
	}
	if res, err := job.Run(ctx, env, load()); err != nil || res.Status != datasync.StatusUnchanged || countObs() != 2 {
		t.Errorf("second run should be unchanged: %+v %v obs=%d", res, err, countObs())
	}

	// 解析出的模型数不到上次成功运行的一半：整批拒收。（这里没有经过调度器，补一条 ok 记录模拟"上次抓到 6 个"。）
	if _, err := pool.Exec(ctx, `INSERT INTO data_source_runs (source_id, status, items_fetched) VALUES ($1, 'ok', 6)`, srcID); err != nil {
		t.Fatal(err)
	}
	body.Store(openRouterBody(map[string][2]string{paid: {"0.000001", "0.000004"}, prefix + "/third": {"0.000001", "0.000001"}}))
	if _, err := job.Run(ctx, env, load()); !isRejected(err) {
		t.Fatalf("losing most of the models should be rejected, got %v", err)
	}
	// 付费模型降价一半、免费模型下线、新增两个模型：记新观测、生成降价情报、免费情报过期。
	body.Store(openRouterBody(map[string][2]string{paid: {"0.000001", "0.000004"}, prefix + "/third": {"0.000001", "0.000001"},
		prefix + "/fourth": {"0.000001", "0.000001"}}))
	res, err = job.Run(ctx, env, load())
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if got := res.Detail["market_changed"]; got != 3 {
		t.Errorf("market_changed = %v, want 3 (paid + two new)", got)
	}
	if s := offerStatus(paid, offers.TypePriceCut); s != "new" {
		t.Errorf("price cut offer status = %q, want new", s)
	}
	if s := offerStatus(free, offers.TypeFreeModel); s != "expired" {
		t.Errorf("free model that disappeared should expire, got %q", s)
	}
}

func isRejected(err error) bool {
	return err != nil && errors.Is(err, datasync.ErrRejected)
}

func TestJob_ProviderBoundSourceIngestsMappedChannels(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	engine, adminSvc := newEngine(t, pool)
	fx := seedFixture(t, pool, adminSvc, LevelL4)
	var providerID int64
	if err := pool.QueryRow(ctx,
		`SELECT pa.provider_id FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id WHERE c.id = $1`, fx.channelID,
	).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(openRouterBody(map[string][2]string{fx.model: {"0.000003", "0.000009"}, "unmapped-" + fx.model: {"0.000001", "0.000002"}})))
	}))
	defer srv.Close()
	if _, err := pool.Exec(ctx, `UPDATE price_sources SET url = $2, provider_id = $3, fetcher = 'openrouter_models' WHERE id = $1`,
		fx.sourceID, srv.URL, providerID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `UPDATE price_sources SET enabled = false WHERE id = $1`, fx.sourceID) })
	job := &Job{Engine: engine}
	src, err := datasync.LoadSource(ctx, pool, fx.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := job.Run(ctx, datasync.NewEnv(datasync.EnvOptions{AllowPrivateNetworks: true, MinHostInterval: -1}), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Detail["ingested"] != 1 || res.Detail["change_requests"] != 1 {
		t.Errorf("detail = %+v, want 1 ingested with a change request", res.Detail)
	}
	var crs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_change_requests WHERE channel_id = $1`, fx.channelID).Scan(&crs); err != nil {
		t.Fatal(err)
	}
	if crs != 1 {
		t.Errorf("change requests for channel = %d, want 1 (L4 never auto-applies)", crs)
	}
	// 未映射的模型：discover_listings 默认关闭，不进待上架队列。
	var listings int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pending_model_listings WHERE upstream_model = $1`, "unmapped-"+fx.model).Scan(&listings)
	if listings != 0 {
		t.Errorf("listing discovery is off by default, got %d listings", listings)
	}
}
