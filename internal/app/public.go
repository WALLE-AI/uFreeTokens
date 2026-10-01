package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/benchmarks"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/rankings"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
)

// 免鉴权的公开数据接口（docs/基准测试与排行榜数据服务技术方案.md §3.4）：
// GET /v1/rankings/* 与 GET /v1/benchmarks[/{slug}]。和 /v1/catalog 一样挂在
// auth.APIKey 之外；带不带 Key 返回相同内容（页面上的 curl 示例不需要登录）。
// 数据每 30 分钟才物化一次，响应带 Cache-Control: public, max-age=300 并在进程内
// 缓存 5 分钟，按 IP 限流防止绕过缓存打库。

const (
	publicRPMPerIP   = 60
	publicCacheTTL   = 5 * time.Minute
	publicCacheMax   = 512 // 缓存条目上限：参数组合有限，超过说明有人在刷随机参数，直接清空重来
	publicMaxAgeSecs = "300"
)

// publicCache 是按"路径 + 规范化参数"缓存的已编码响应。
type publicCache struct {
	mu      sync.Mutex
	entries map[string]publicCacheEntry
	now     func() time.Time
}

type publicCacheEntry struct {
	at  time.Time
	val any
}

func newPublicCache() *publicCache {
	return &publicCache{entries: map[string]publicCacheEntry{}, now: time.Now}
}

// get 返回缓存值；hit 报告是否命中（供指标统计）。
func (c *publicCache) get(key string, load func() (any, error)) (v any, hit bool, err error) {
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < publicCacheTTL {
		return e.val, true, nil
	}
	v, err = load()
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	if len(c.entries) >= publicCacheMax {
		c.entries = map[string]publicCacheEntry{}
	}
	c.entries[key] = publicCacheEntry{at: c.now(), val: v}
	c.mu.Unlock()
	return v, false, nil
}

type publicHandlers struct {
	rankings   *rankings.Service // nil = catalog 未就绪，/v1/rankings/* 返回 503 not_implemented
	benchmarks *benchmarks.Store
	enabled    bool // PublicConfig.RankingsEnabled
	rl         *ratelimit.Limiter
	cache      *publicCache
	metrics    *observability.Metrics // nil = 不上报
}

// observe 记录一次公开接口请求的结果与耗时。
func (h *publicHandlers) observe(r *http.Request, start time.Time, result string) {
	if h.metrics == nil {
		return
	}
	endpoint := chi.RouteContext(r.Context()).RoutePattern()
	h.metrics.PublicRequestsTotal.WithLabelValues(endpoint, result).Inc()
	h.metrics.PublicDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
}

func newPublicHandlers(d GatewayDeps) *publicHandlers {
	h := &publicHandlers{benchmarks: benchmarks.NewStore(d.PG), enabled: true, rl: d.RateLimit, cache: newPublicCache(), metrics: d.Metrics}
	opts := rankings.DefaultOptions()
	if d.Cfg != nil {
		h.enabled = d.Cfg.Public.RankingsEnabled
		opts.ShowAbsolute = d.Cfg.Public.RankingsShowAbsolute
		if d.Cfg.Public.RankingsMinAccounts > 0 {
			opts.MinDistinctAccounts = d.Cfg.Public.RankingsMinAccounts
		}
	}
	// 榜单要用 catalog 快照决定哪些模型公开可见、展示名是什么；没有快照（没配 KEK）时不提供。
	if d.Catalog != nil {
		h.rankings = rankings.NewService(d.PG, d.Catalog, opts)
	}
	return h
}

// serve 是公开接口的共同流程：限流 → 读缓存/加载 → 写 Cache-Control 与 JSON。
func (h *publicHandlers) serve(w http.ResponseWriter, r *http.Request, key string, load func(ctx context.Context) (any, error),
	onError func(w http.ResponseWriter, r *http.Request, err error)) {
	start := time.Now()
	if h.rl != nil {
		if res := h.rl.AllowRPM(r.Context(), "public:ip:"+requestIP(r), publicRPMPerIP); !res.Allowed {
			h.observe(r, start, "rate_limited")
			writeRateLimited(w, r, res, "rate_limit_exceeded", "Too many requests.")
			return
		}
	}
	v, hit, err := h.cache.get(key, func() (any, error) {
		ctx, cancel := timeoutCtx(r, 10*time.Second)
		defer cancel()
		return load(ctx)
	})
	if err != nil {
		h.observe(r, start, "error")
		onError(w, r, err)
		return
	}
	if hit {
		h.observe(r, start, "hit")
	} else {
		h.observe(r, start, "miss")
	}
	w.Header().Set("Cache-Control", "public, max-age="+publicMaxAgeSecs)
	httpx.WriteJSON(w, http.StatusOK, v)
}

// rankingsGuard 处理总开关与依赖缺失，返回 false 时已写出错误。
func (h *publicHandlers) rankingsGuard(w http.ResponseWriter, r *http.Request) (rankings.Period, bool) {
	if !h.enabled {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "service_unavailable", "Public rankings are temporarily unavailable.")
		return "", false
	}
	if h.rankings == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Public rankings are not available on this deployment.")
		return "", false
	}
	p, err := rankings.ParsePeriod(r.URL.Query().Get("period"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'period' must be one of day, week, month.")
		return "", false
	}
	return p, true
}

func rankingsLoadError(w http.ResponseWriter, r *http.Request, _ error) {
	httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load rankings.")
}

// parseLimit 解析 ?limit=（1..100，默认 20），返回 false 时已写出错误。
func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 20, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 100 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'limit' must be an integer between 1 and 100.")
		return 0, false
	}
	return n, true
}

// GET /v1/rankings/models?period=&limit=&series=none|day
func (h *publicHandlers) rankingsModels(w http.ResponseWriter, r *http.Request) {
	p, ok := h.rankingsGuard(w, r)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	series := r.URL.Query().Get("series")
	if !slices.Contains([]string{"", "none", "day"}, series) {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'series' must be none or day.")
		return
	}
	withSeries := series == "day"
	key := "rankings/models:" + string(p) + ":" + strconv.Itoa(limit) + ":" + strconv.FormatBool(withSeries)
	h.serve(w, r, key, func(ctx context.Context) (any, error) {
		return h.rankings.Models(ctx, p, limit, withSeries)
	}, rankingsLoadError)
}

// GET /v1/rankings/authors?period=
func (h *publicHandlers) rankingsAuthors(w http.ResponseWriter, r *http.Request) {
	p, ok := h.rankingsGuard(w, r)
	if !ok {
		return
	}
	h.serve(w, r, "rankings/authors:"+string(p), func(ctx context.Context) (any, error) {
		return h.rankings.Authors(ctx, p)
	}, rankingsLoadError)
}

// rankingsByLimit 是 speed / tools / multimodal / apps 四个只带 period、limit 参数的榜单的共同入口。
func (h *publicHandlers) rankingsByLimit(name string, load func(ctx context.Context, svc *rankings.Service, p rankings.Period, limit int) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.rankingsGuard(w, r)
		if !ok {
			return
		}
		limit, ok := parseLimit(w, r)
		if !ok {
			return
		}
		h.serve(w, r, "rankings/"+name+":"+string(p)+":"+strconv.Itoa(limit), func(ctx context.Context) (any, error) {
			return load(ctx, h.rankings, p, limit)
		}, rankingsLoadError)
	}
}

// benchmarkListResponse 对应 GET /v1/benchmarks 的 {"object":"list","data":[...]}。
type benchmarkListResponse struct {
	Object string                 `json:"object"`
	Data   []benchmarks.Benchmark `json:"data"`
}

func benchmarksLoadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, benchmarks.ErrNotFound) {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Benchmark not found.")
		return
	}
	httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load benchmarks.")
}

// GET /v1/benchmarks?category=
func (h *publicHandlers) listBenchmarks(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	if category != "" && !slices.Contains(benchmarks.Categories, category) {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'category' must be one of "+strings.Join(benchmarks.Categories, ", ")+".")
		return
	}
	h.serve(w, r, "benchmarks:"+category, func(ctx context.Context) (any, error) {
		list, err := h.benchmarks.List(ctx, category)
		if err != nil {
			return nil, err
		}
		return benchmarkListResponse{Object: "list", Data: list}, nil
	}, benchmarksLoadError)
}

type modelBenchmarksResponse struct {
	Object string                      `json:"object"`
	Model  string                      `json:"model"`
	Data   []benchmarks.ModelBenchmark `json:"data"`
}

// GET /v1/model-benchmarks?model=：某个公开模型在各已发布基准上的成绩（模型详情页"评测成绩"卡片）。
func (h *publicHandlers) modelBenchmarks(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" || len(model) > 200 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'model' is required.")
		return
	}
	h.serve(w, r, "model-benchmarks:"+model, func(ctx context.Context) (any, error) {
		list, err := h.benchmarks.ForModel(ctx, model)
		if err != nil {
			return nil, err
		}
		return modelBenchmarksResponse{Object: "list", Model: model, Data: list}, nil
	}, benchmarksLoadError)
}

// GET /v1/benchmarks/{slug}
func (h *publicHandlers) getBenchmark(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if len(slug) > 100 {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Benchmark not found.")
		return
	}
	h.serve(w, r, "benchmark:"+slug, func(ctx context.Context) (any, error) {
		return h.benchmarks.Get(ctx, slug)
	}, benchmarksLoadError)
}
