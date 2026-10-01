package app

import (
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
)

// writeRateLimited 统一处理限流拒绝：带上 Retry-After 头（有明确等待时长时），
// 返回 429。和 internal/relay/helpers.go 里的同名函数逻辑一致——两个包不应该
// 互相依赖，各自维护一份这几行足够简单，不值得为此抽出共享包。
func writeRateLimited(w http.ResponseWriter, r *http.Request, res ratelimit.Result, code, message string) {
	if res.RetryAfter > 0 {
		secs := int(res.RetryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	httpx.WriteError(w, r, http.StatusTooManyRequests, code, message)
}

// catalogRPMPerIP 是 GET /v1/catalog 的按 IP 限流上限。这是个免鉴权的公开
// 端点，Cache-Control 已经让大多数客户端/CDN 把请求挡在缓存层，这个限流只是
// 防止有人绕过缓存直接打爆它；不需要像登录那样 fail-closed，Redis 抖动时
// 放行比拒绝公开只读数据更合理（见 ratelimit.Limiter.AllowRPM 的包注释）。
const catalogRPMPerIP = 60

// catalogModel 是 GET /v1/catalog 单条模型的响应形状（技术方案迭代5）。
// 只包含硬性配置（name/family/type/context_window/max_output/capabilities/
// sell_price）和运营录入的展示层字段（display_name 等，可能为空——运营还
// 没录入过）；不暴露渠道、上游账号、成本价这些内部路由细节。
type catalogModel struct {
	Name            string         `json:"name"`
	Family          string         `json:"family"`
	Type            string         `json:"type"`
	ContextWindow   int            `json:"context_window"`
	MaxOutput       int            `json:"max_output"`
	Capabilities    []string       `json:"capabilities"`
	SellPrice       *catalogPrice  `json:"sell_price,omitempty"`
	DisplayName     string         `json:"display_name,omitempty"`
	Description     string         `json:"description,omitempty"`
	ProviderDisplay string         `json:"provider_display,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Scores          map[string]any `json:"scores,omitempty"`
	// Status 是 "active" 或 "deprecated"（技术方案迭代：左侧栏 Inactive
	// models 筛选项）。deprecated 模型只在这里展示，router/relay 不会路由
	// 到它们——见 internal/catalog.Snapshot.DeprecatedModels 的注释。
	Status string `json:"status"`
}

type catalogPrice struct {
	Currency   string                  `json:"currency"`
	Components []catalogPriceComponent `json:"components"`
}

type catalogPriceComponent struct {
	Meter     string `json:"meter"`
	Unit      string `json:"unit"`
	UnitPrice string `json:"unit_price"` // decimal 序列化成字符串，避免浮点精度问题
}

// catalogHandler 是 GET /v1/catalog 的入口：免鉴权公开目录，只返回
// status=active 且对 free tier 可见的模型（技术方案迭代5）。store 为 nil
// 时调用方（NewGatewayRouter）应该直接挂 notImplementedHandler，不会走到
// 这里；rl 为 nil 时跳过限流（测试/未配置 Redis 的场景）。
func catalogHandler(store *catalog.Store, rl *ratelimit.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rl != nil {
			if res := rl.AllowRPM(r.Context(), "catalog:ip:"+requestIP(r), catalogRPMPerIP); !res.Allowed {
				writeRateLimited(w, r, res, "rate_limit_exceeded", "Too many requests.")
				return
			}
		}

		ctx, cancel := timeoutCtx(r, 5*time.Second)
		defer cancel()
		snap, err := store.Get(ctx)
		if err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load model catalog.")
			return
		}

		models := make([]catalogModel, 0, len(snap.Models)+len(snap.DeprecatedModels))
		appendVisible := func(vms map[string]*catalog.VirtualModel) {
			for _, vm := range vms {
				if !tierVisible(vm.VisibleTiers, "free") {
					continue
				}
				cm := catalogModel{
					Name: vm.Name, Family: vm.Family, Type: vm.Type,
					ContextWindow: vm.ContextWindow, MaxOutput: vm.MaxOutput, Capabilities: vm.Capabilities,
					Status: vm.Status,
				}
				if book, ok := snap.SellPriceBooks[vm.ID]; ok {
					cm.SellPrice = toCatalogPrice(book)
				}
				if vm.Metadata != nil {
					cm.DisplayName = vm.Metadata.DisplayName
					cm.Description = vm.Metadata.Description
					cm.ProviderDisplay = vm.Metadata.ProviderDisplay
					cm.Tags = vm.Metadata.Tags
					cm.Scores = vm.Metadata.Scores
				}
				models = append(models, cm)
			}
		}
		appendVisible(snap.Models)
		// deprecated 模型也带出来，前端"Inactive models → Show deprecated"靠
		// status 字段做客户端过滤；这里不排除它们，因为它们本来就不在
		// snap.Models 里，不存在被误路由的风险（见 catalog.Snapshot 的注释）。
		appendVisible(snap.DeprecatedModels)
		sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

		w.Header().Set("Cache-Control", "public, max-age=60")
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
	}
}

func tierVisible(tiers []string, tier string) bool {
	for _, t := range tiers {
		if t == tier {
			return true
		}
	}
	return false
}

func toCatalogPrice(book pricing.Book) *catalogPrice {
	if len(book.Components) == 0 {
		return nil
	}
	comps := make([]catalogPriceComponent, 0, len(book.Components))
	for _, c := range book.Components {
		comps = append(comps, catalogPriceComponent{
			Meter: string(c.Meter), Unit: string(c.Unit), UnitPrice: c.UnitPrice.String(),
		})
	}
	return &catalogPrice{Currency: book.Currency, Components: comps}
}

// requestIP 取 RemoteAddr 的主机部分（去掉端口），拿不到就原样返回。
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
