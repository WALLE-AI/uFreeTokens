package pricesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// 价格来源的定时任务（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §3）。
// 一次运行：条件 GET -> 内容没变直接结束 -> 按 fetcher 解析成观测 -> 形状检查 ->
//   - 来源绑定了本平台 provider：能找到渠道的模型走 Engine.Ingest（比价、生成调价提案、
//     L2 小幅变化自动发布）；找不到渠道的按 config.discover_listings 决定是否进"新模型发现"；
//   - 未绑定 provider（聚合 / 社区来源）：价格有变化才记一条 price_observations，作市场情报；
//   - config.detect_offers=true 时顺带识别免费模型与降价，写进 upstream_offers。
//
// 来源 config 里与此相关的键：
//
//	discover_listings  bool      找不到渠道的模型是否进 pending_model_listings（默认 false，聚合来源会刷出几百条）
//	detect_offers      bool      是否识别优惠
//	offer_provider     string    优惠情报记在哪个厂商名下（OpenRouter 填 "openrouter"）；
//	                             为空时取 upstream_model 的 "提供商/" 前缀（models.dev）
//	offer_providers    []string  只对这些提供商识别优惠（为空 = 全部）
//	provider_code_map  {string: string}  优惠厂商名 -> 本平台 providers.code（如 "siliconflow-cn": "siliconflow"）；
//	                             识别出的免费模型据此进对应供应商的"待上架"，平台没有该供应商时只留情报

type parseFunc func(body []byte, src datasync.Source) ([]Observation, error)

// minKeepRatio：本次解析出的模型数低于上次成功运行的一半，判为页面 / 接口改版，整批拒收。
const minKeepRatio = 0.5

var parsers = map[string]parseFunc{
	"openrouter_models": func(body []byte, _ datasync.Source) ([]Observation, error) {
		obs, _, err := normalizeOpenRouterResponse(body)
		return obs, err
	},
	"litellm_dataset": func(body []byte, _ datasync.Source) ([]Observation, error) {
		obs, _, err := normalizeLiteLLMResponse(body)
		return obs, err
	},
	"html_table": func(body []byte, src datasync.Source) ([]Observation, error) {
		cfg, err := decodeHTMLTableConfig(src.Config)
		if err != nil {
			return nil, err
		}
		return ParseHTMLPriceTable(body, cfg)
	},
	"modelsdev": func(body []byte, src datasync.Source) ([]Observation, error) {
		var cfg modelsDevConfig
		raw, _ := json.Marshal(src.Config)
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("pricesync/modelsdev: decode config: %w", err)
		}
		obs, _, err := normalizeModelsDevResponse(body, cfg, src.ProviderID != nil && len(cfg.Providers) == 1)
		return obs, err
	},
}

// PriceFetchers 是价格域已实现的抓取器名。
func PriceFetchers() []string {
	out := make([]string, 0, len(parsers))
	for k := range parsers {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

type jobConfig struct {
	DiscoverListings bool              `json:"discover_listings"`
	DetectOffers     bool              `json:"detect_offers"`
	OfferProvider    string            `json:"offer_provider"`
	OfferProviders   []string          `json:"offer_providers"`
	ProviderCodeMap  map[string]string `json:"provider_code_map"`
}

// Job 把 Engine 接到 datasync 调度上。
type Job struct {
	Engine *Engine
	Offers *offers.Store // 为空时不识别优惠
}

// Registry 返回价格域全部抓取器对应的 Job。
func (j *Job) Registry() datasync.Registry {
	r := datasync.Registry{}
	for name := range parsers {
		r[name] = j
	}
	return r
}

func (j *Job) Run(ctx context.Context, env *datasync.Env, src datasync.Source) (datasync.Result, error) {
	runStart := time.Now()
	parse, ok := parsers[src.Fetcher]
	if !ok {
		return datasync.Result{}, fmt.Errorf("pricesync: no parser for fetcher %q", src.Fetcher)
	}
	if src.URL == "" {
		return datasync.Result{}, fmt.Errorf("pricesync: source %d has no url", src.ID)
	}
	var cfg jobConfig
	raw, _ := json.Marshal(src.Config)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return datasync.Result{}, fmt.Errorf("pricesync: decode source config: %w", err)
	}

	resp, err := env.Get(ctx, src.URL, datasync.GetOptions{ETag: src.HTTPETag, LastModified: src.HTTPLastModified})
	if err != nil {
		return datasync.Result{}, err
	}
	if resp.NotModified || bytes.Equal(resp.Hash, src.LastContentHash) {
		if cfg.DetectOffers && j.Offers != nil {
			if err := j.Offers.TouchFreeModels(ctx, src.ID); err != nil {
				return datasync.Result{}, err
			}
		}
		return datasync.Result{Status: datasync.StatusUnchanged, HTTPETag: resp.ETag, HTTPLastModified: resp.LastModified}, nil
	}

	obs, err := parse(resp.Body, src)
	if err != nil {
		return datasync.Result{}, fmt.Errorf("%w: %v", datasync.ErrRejected, err)
	}
	if len(obs) == 0 {
		return datasync.Result{}, datasync.Rejectf("parsed 0 models")
	}
	if prev, err := j.lastFetchedCount(ctx, src.ID); err != nil {
		return datasync.Result{}, err
	} else if prev > 0 && float64(len(obs)) < float64(prev)*minKeepRatio {
		return datasync.Result{}, datasync.Rejectf("parsed %d models, last successful run had %d", len(obs), prev)
	}

	previous, err := j.latestSpecs(ctx, src.ID)
	if err != nil {
		return datasync.Result{}, err
	}

	stats := map[string]int{}
	var priceObs []detectItem
	for _, o := range obs {
		sortComponents(o.Spec.Components)
		specJSON, err := json.Marshal(o.Spec)
		if err != nil {
			return datasync.Result{}, fmt.Errorf("pricesync: marshal spec: %w", err)
		}
		hash := sha256.Sum256(specJSON)
		prev, seen := previous[o.UpstreamModel]

		if src.ProviderID != nil {
			channels, err := j.Engine.resolveChannels(ctx, *src.ProviderID, o.UpstreamModel)
			if err != nil {
				return datasync.Result{}, err
			}
			switch {
			case len(channels) > 0:
				for _, ch := range channels {
					r, err := j.Engine.Ingest(ctx, IngestInput{ChannelID: ch, SourceID: src.ID, Level: Level(src.Level),
						UpstreamModel: o.UpstreamModel, Spec: o.Spec, RawObject: o.RawObject})
					if err != nil {
						return datasync.Result{}, fmt.Errorf("pricesync: ingest %s: %w", o.UpstreamModel, err)
					}
					stats["ingested"]++
					if r.ChangeRequestID != nil {
						stats["change_requests"]++
					}
					if r.AppliedBookID != nil {
						stats["auto_applied"]++
					}
				}
			case cfg.DiscoverListings:
				if _, err := j.Engine.IngestUnmapped(ctx, UnmappedObservationInput{ProviderID: *src.ProviderID, SourceID: src.ID,
					Level: Level(src.Level), UpstreamModel: o.UpstreamModel, Spec: o.Spec, RawObject: o.RawObject, Meta: o.Meta}); err != nil {
					return datasync.Result{}, fmt.Errorf("pricesync: ingest unmapped %s: %w", o.UpstreamModel, err)
				}
				stats["listings"]++
			case !seen || !bytes.Equal(prev.hash, hash[:]):
				if err := j.recordIfChanged(ctx, src.ID, o, specJSON, hash[:], prev, seen); err != nil {
					return datasync.Result{}, err
				}
				stats["market_changed"]++
			}
		} else if !seen || !bytes.Equal(prev.hash, hash[:]) {
			if err := j.recordIfChanged(ctx, src.ID, o, specJSON, hash[:], prev, seen); err != nil {
				return datasync.Result{}, err
			}
			stats["market_changed"]++
		}

		if cfg.DetectOffers {
			po := offers.PriceObservation{Model: o.UpstreamModel, Current: pricePoint(o.Spec)}
			if seen {
				pp := pricePoint(prev.spec)
				po.Previous = &pp
			}
			priceObs = append(priceObs, detectItem{price: po, obs: o})
		}
	}

	detail := map[string]any{"models": len(obs)}
	for k, v := range stats {
		detail[k] = v
	}
	if cfg.DetectOffers && j.Offers != nil {
		ds, err := j.detectOffers(ctx, src, cfg, priceObs, runStart)
		if err != nil {
			return datasync.Result{}, err
		}
		detail["offers_created"], detail["offers_expired"], detail["free_listings"] = ds.created, ds.expired, ds.freeListings
		life, err := j.Engine.SyncFreeListings(ctx)
		if err != nil {
			return datasync.Result{}, err
		}
		detail["free_listings_expired"] = life.Expired
		if len(life.RetiredChannels) > 0 || len(life.DeprecatedModels) > 0 {
			detail["free_channels_retired"], detail["free_models_deprecated"] = life.RetiredChannels, life.DeprecatedModels
		}
	}
	return datasync.Result{
		ItemsFetched: len(obs), ItemsChanged: stats["market_changed"] + stats["change_requests"] + stats["listings"],
		ContentHash: resp.Hash, HTTPETag: resp.ETag, HTTPLastModified: resp.LastModified, Detail: detail,
	}, nil
}

type prevSpec struct {
	spec PriceSpec
	hash []byte
}

// latestSpecs 读这个来源每个模型最近一次观测。
func (j *Job) latestSpecs(ctx context.Context, sourceID int64) (map[string]prevSpec, error) {
	rows, err := j.Engine.db(ctx).Query(ctx,
		`SELECT DISTINCT ON (upstream_model) upstream_model, spec, spec_hash
		 FROM price_observations WHERE source_id = $1 ORDER BY upstream_model, observed_at DESC, id DESC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("pricesync: load latest observations: %w", err)
	}
	defer rows.Close()
	out := map[string]prevSpec{}
	for rows.Next() {
		var model string
		var specJSON, hash []byte
		if err := rows.Scan(&model, &specJSON, &hash); err != nil {
			return nil, fmt.Errorf("pricesync: scan latest observation: %w", err)
		}
		var spec PriceSpec
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			continue
		}
		out[model] = prevSpec{spec: spec, hash: hash}
	}
	return out, rows.Err()
}

// recordIfChanged 记一条纯情报观测（不走调价流程）；价格和上一次一样就不记，避免每 6 小时
// 把几百个没变的模型重复写一遍。
func (j *Job) recordIfChanged(ctx context.Context, sourceID int64, o Observation, specJSON, hash []byte, prev prevSpec, seen bool) error {
	if seen && bytes.Equal(prev.hash, hash) {
		return nil
	}
	if _, err := j.Engine.db(ctx).Exec(ctx,
		`INSERT INTO price_observations (source_id, upstream_model, spec, spec_hash, raw_object) VALUES ($1, $2, $3, $4, NULLIF($5, ''))`,
		sourceID, o.UpstreamModel, specJSON, hash, o.RawObject); err != nil {
		return fmt.Errorf("pricesync: insert observation %s: %w", o.UpstreamModel, err)
	}
	return nil
}

func (j *Job) lastFetchedCount(ctx context.Context, sourceID int64) (int, error) {
	var n *int
	err := j.Engine.db(ctx).QueryRow(ctx,
		`SELECT items_fetched FROM data_source_runs WHERE source_id = $1 AND status = 'ok' ORDER BY started_at DESC LIMIT 1`,
		sourceID).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("pricesync: load last run: %w", err)
	}
	if n == nil {
		return 0, nil
	}
	return *n, nil
}

// detectItem 是一个模型本次的价格点（识别优惠用）与完整观测（建免费待上架候选用）。
type detectItem struct {
	price offers.PriceObservation
	obs   Observation
}

type detectStats struct {
	created      int
	expired      int64
	freeListings int
}

func (j *Job) detectOffers(ctx context.Context, src datasync.Source, cfg jobConfig, items []detectItem, runStart time.Time) (detectStats, error) {
	var st detectStats
	byProvider := map[string][]detectItem{}
	for _, it := range items {
		provider, model := cfg.OfferProvider, it.price.Model
		if provider == "" {
			if src.ProviderCode != "" && !strings.Contains(model, "/") {
				provider = src.ProviderCode
			} else if p, m, found := strings.Cut(model, "/"); found {
				provider, model = p, m
			} else {
				continue
			}
		}
		if len(cfg.OfferProviders) > 0 && !slices.Contains(cfg.OfferProviders, provider) {
			continue
		}
		it.price.Model = model
		byProvider[provider] = append(byProvider[provider], it)
	}
	for provider, list := range byProvider {
		prices := make([]offers.PriceObservation, len(list))
		byModel := make(map[string]Observation, len(list))
		for i, it := range list {
			prices[i] = it.price
			byModel[it.price.Model] = it.obs
		}
		providerID, err := j.listingProviderID(ctx, src, cfg, provider)
		if err != nil {
			return st, err
		}
		for _, c := range offers.DetectFromPrices(src.ID, provider, src.URL, prices) {
			offerID, isNew, err := j.Offers.Upsert(ctx, c)
			if err != nil {
				return st, err
			}
			if isNew {
				st.created++
			}
			// 免费模型直通待上架：模型名用情报里的（即该供应商 API 认的模型 ID），价格与参数用完整观测。
			if c.OfferType != offers.TypeFreeModel || providerID == 0 {
				continue
			}
			o := byModel[c.UpstreamModel]
			if _, ok, err := j.Engine.UpsertFreeListing(ctx, FreeListingInput{ProviderID: providerID, SourceID: src.ID,
				UpstreamModel: c.UpstreamModel, Spec: o.Spec, Meta: o.Meta, OfferID: offerID}); err != nil {
				return st, fmt.Errorf("pricesync: free listing %s/%s: %w", provider, c.UpstreamModel, err)
			} else if ok {
				st.freeListings++
			}
		}
	}
	var err error
	if st.expired, err = j.Offers.ExpireMissingFreeModels(ctx, src.ID, runStart); err != nil {
		return st, err
	}
	if _, err := j.Offers.ExpireEnded(ctx); err != nil {
		return st, err
	}
	return st, nil
}

// listingProviderID 把优惠厂商名映射到本平台供应商：来源绑定的供应商优先，其次 provider_code_map，
// 最后按同名 code 查找；找不到返回 0（平台没接这家，免费模型只留情报）。
func (j *Job) listingProviderID(ctx context.Context, src datasync.Source, cfg jobConfig, provider string) (int64, error) {
	if src.ProviderID != nil && provider == src.ProviderCode {
		return *src.ProviderID, nil
	}
	code := provider
	if mapped, ok := cfg.ProviderCodeMap[provider]; ok {
		code = mapped
	}
	return j.Engine.ProviderIDByCode(ctx, code)
}

// sortComponents 固定计量项顺序：OpenRouter / LiteLLM 的归一化按 map 遍历，顺序随机，
// 不排序的话同样的价格每次 spec_hash 都不同，会被当成"价格变了"。
func sortComponents(cs []Component) {
	slices.SortFunc(cs, func(a, b Component) int {
		if c := compareStrings(string(a.Meter), string(b.Meter)); c != 0 {
			return c
		}
		if c := compareStrings(a.ServiceTier, b.ServiceTier); c != 0 {
			return c
		}
		return a.TierMinInput - b.TierMinInput
	})
}

// pricePoint 取默认档、输入长度从 0 起步、不限时段的输入 / 输出单价。
func pricePoint(spec PriceSpec) offers.PricePoint {
	var p offers.PricePoint
	for _, c := range spec.Components {
		if (c.ServiceTier != "" && c.ServiceTier != "default") || c.TierMinInput != 0 || c.WindowStartMin != nil || c.Unit != pricing.UnitPer1MTokens {
			continue
		}
		v := c.UnitPrice
		switch c.Meter {
		case pricing.MeterInput:
			p.Input = &v
		case pricing.MeterOutput:
			p.Output = &v
		}
	}
	return p
}
