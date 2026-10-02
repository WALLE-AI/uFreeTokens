// Package relay 编排一次 /v1/chat/completions 请求的完整生命周期
// （技术方案 §3.2、§7.1）：鉴权信息已由中间件放入 context -> 解析请求 -> 预扣费用
// -> 路由选渠道/Key -> 转发上游（失败时按错误类别换 Key/换渠道重试，§7.6-7.7）
// -> 结算 -> 异步写入 request_logs（§6.8/§7.13）。
//
// 当前范围（有意的阶段性限制，不是遗漏）：
//   - 重试只发生在"拿到上游响应/连接失败"之后、"开始向客户端转发内容"之前
//     （技术方案 §7.7：一旦向客户端写出任何字节，就不能再换渠道重试）。
//   - "每实例每秒重试数 ≤ 正常请求数 20%"的全局重试预算（§7.7）由 RetryBudget
//     实现：用令牌桶近似这个比例，而不是严格的按秒计数窗口（见 retrybudget.go
//     的注释）。除此之外仍有单请求级别的 MaxAttempts + TotalDeadline 上限。
//   - 用量兜底估算是保守占位（上游完全不返回 usage 时，按预扣的上限计费，
//     不会让平台倒贴钱，但也不精确）——真正基于 tokenizer 的估算见 §7.9.4，留作后续。
//   - request_logs 只记录"预扣成功、进入路由/转发"之后的结果（成功或上游失败）；
//     鉴权失败、余额不足、模型不存在等预扣之前的拒绝目前只有结构化访问日志，
//     不落 request_logs（那些场景没有 channel/attempt 信息，价值有限，留作后续按需补充）。
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/health"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/promotion"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

const (
	logEndpointChat       = "chat.completions" // request_logs.endpoint 里记录的名字
	logEndpointEmbeddings = "embeddings"
)

// Config 是 relay.Service 的可调参数，默认值见技术方案附录 B。
type Config struct {
	ReserveOutputCap int           // 预扣费用时对 max_tokens 的上限裁剪（技术方案 §7.9.1）
	ReservationTTL   time.Duration // 预扣记录的兜底过期时间，供 worker 回收（尚未实现 worker 侧）
	MaxUpstreamBody  int64         // 请求体与非流式响应体的读取上限，防止恶意/异常的超大请求或上游响应
	Retry            RetryConfig

	// ImageTokenEstimate 是对话请求里每张图片按多少输入 token 预估（路由的上下文窗口
	// 过滤、TPM 与预扣都用它）。base64 图片的字节不再按 4 字节/token 计入，否则
	// 1MB 的图片会被估成 25 万 token（多模态技术方案 D2）。最终计费仍以上游 usage 为准。
	ImageTokenEstimate int
	// EnforceVision 为 true 时，带图片的对话请求只路由到具备 vision 能力的渠道
	// （多模态技术方案 D5）。默认关闭：存量 VLM 补齐 vision 能力后再开启，避免已上架
	// 但没勾 vision 的模型突然 503。
	EnforceVision bool
	// 多模态端点的请求上限。
	MaxRerankDocuments  int
	MaxImagesPerRequest int
	MaxSpeechChars      int
	// DisabledCodecs 中的 codec 被临时停用：方言指定了它们的渠道不参与路由。
	DisabledCodecs []string
	// UpstreamHeaderTimeout 是等上游响应头（首字节）的上限，超时换渠道；图像生成通常要
	// 几十秒才返回，单独用 ImagesHeaderTimeout。0 = 不限制（只受 HTTP 客户端自身的约束）。
	UpstreamHeaderTimeout time.Duration
	ImagesHeaderTimeout   time.Duration
}

// RetryConfig 控制换 Key/换渠道重试的上限（技术方案 §7.7）。
type RetryConfig struct {
	MaxAttempts     int           // 含首次在内的最大尝试次数
	TotalDeadline   time.Duration // 从第一次尝试起，超过这个时长不再重试
	DefaultCooldown time.Duration // 429 且上游未给 Retry-After 时的默认冷却时长
	MaxCooldown     time.Duration // Retry-After 头的取值上限，防止上游返回异常大的值把 Key 冻结太久
	KeyDownCooldown time.Duration // Key 失效/配额耗尽（非限流）时的冷却时长
}

func DefaultConfig() Config {
	return Config{
		ReserveOutputCap:    8192,
		ReservationTTL:      30 * time.Minute,
		MaxUpstreamBody:     20 * 1024 * 1024,
		ImageTokenEstimate:  1500,
		MaxRerankDocuments:  1000,
		MaxImagesPerRequest: 4,
		MaxSpeechChars:      4096,
		// 实测（2026-10-02）：百炼 / OpenRouter 的 qwen-image 同步生成 45–60 秒
		UpstreamHeaderTimeout: 60 * time.Second,
		ImagesHeaderTimeout:   180 * time.Second,
		Retry: RetryConfig{
			MaxAttempts:     3,
			TotalDeadline:   90 * time.Second,
			DefaultCooldown: 30 * time.Second,
			MaxCooldown:     5 * time.Minute,
			KeyDownCooldown: time.Hour,
		},
	}
}

type Service struct {
	Catalog   *catalog.Store
	Wallet    *wallet.Service
	Adapters  *adapter.Registry
	HTTP      *http.Client
	Health    *health.Registry   // nil = 不做熔断/冷却过滤，退化为"每次都从全部候选里选"
	RateLimit *ratelimit.Limiter // nil = 不做 RPM/TPM/并发限流（技术方案 §7.12）
	Promotion *promotion.Engine  // nil = 不匹配促销，一律按原价结算（技术方案 §7.10）
	ReqLog    *reqlog.Writer     // nil = 不写 request_logs（reqlog.Writer 的方法对 nil 接收者是安全的 no-op）
	Logger    *slog.Logger
	Cfg       Config

	// altHosts 记住 Key → 可用的备用 base_url（方言 auth.alternate_hosts，如智谱国内站 /
	// 国际站）：主域名对这把 Key 返回 401/403 后改用备用域名，之后的请求直接走它。
	altHosts sync.Map // map[int64]string

	// RetryBudget 是"每实例每秒重试数 ≤ 正常请求数 20%"的全局重试预算
	// （技术方案 §7.7）。nil = 不限制（RetryBudget 的方法对 nil 接收者是安全的
	// no-op），生产环境应设为 DefaultRetryBudget() 或自定义参数。
	RetryBudget *RetryBudget
}

// requestMeta 收拢一次请求里贯穿始终、用于最后写 request_logs / 匹配促销的公共字段。
type requestMeta struct {
	requestID   string
	accountID   int64
	apiKeyID    int64
	accountTier string
	vmName      string
	vmID        int64
	isStream    bool
	// clientWantsUsage 见 ChatCompletions 里的赋值处；只有流式请求会用到。
	clientWantsUsage bool
	clientIP         string
	userAgent        string
	start            time.Time
	// logEndpoint 是 request_logs.endpoint 里记录的名字（logEndpointChat /
	// logEndpointEmbeddings），不是字面的上游 URL 路径。handleNonStream 在
	// ChatCompletions 和 Embeddings 之间共用，靠这个字段区分是谁调用的。
	logEndpoint string
	// 公开排行榜的采集字段（见 attribution.go）：请求阶段填 app/imageInputs，
	// handleStream / handleNonStream 在写日志前填 genMs/toolCalls。
	appName, appURL string
	imageInputs     int
	genMs           *int64
	toolCalls       int
	// reqMap 是客户端的原始请求体（目前只有向量端点用它做响应侧变换）。
	reqMap map[string]any
	// streamErr 非空表示上游在流中报错：按已转发内容结算，但 request_logs 记为失败。
	streamErr string
}

// ChatCompletions 是 POST /v1/chat/completions 的 http.HandlerFunc。
func (s *Service) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	requestID := httpx.RequestIDFromContext(ctx)
	log := s.Logger.With("request_id", requestID)

	principal, ok := auth.FromContext(ctx)
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
		return
	}

	body, reqMap, ok := s.readJSON(w, r)
	if !ok {
		return
	}
	modelName, _ := reqMap["model"].(string)
	adm, ok := s.admit(w, r, log, principal, specChat, modelName)
	if !ok {
		return
	}
	defer adm.release()
	snap, vm := adm.snap, adm.vm

	stream, _ := reqMap["stream"].(bool)
	// clientWantsUsage 记录客户端是不是自己主动要了 stream_options.include_usage
	// （技术方案 §7.4：注入 include_usage 是为了让 relay 层拿到真实用量计费，
	// 不代表客户端自己也想在 SSE 里看到那个额外的 usage-only chunk——上游总是
	// 会被要求带上 usage，见 adapter/openai.go 的 BuildRequest，这里只决定
	// handleStream 要不要把那个 chunk 转发给客户端，见 isUsageOnlyChunk）。
	clientWantsUsage := clientRequestedStreamUsage(reqMap)
	imageInputs := countImageInputs(reqMap)
	estInput := estimateChatInputTokens(body, reqMap, s.Cfg.ImageTokenEstimate)
	reserveOutput := reserveOutputTokens(reqMap, vm.MaxOutput, s.Cfg.ReserveOutputCap)

	if !s.consumeTPM(w, r, adm, int64(estInput+reserveOutput)) {
		return
	}

	features := router.Features{
		Stream:          stream,
		NeedTools:       hasKey(reqMap, "tools"),
		NeedJSONSchema:  hasResponseFormatJSONSchema(reqMap),
		NeedVision:      s.Cfg.EnforceVision && imageInputs > 0,
		EstInputTokens:  estInput,
		MaxOutputTokens: reserveOutput,
	}

	// 预扣的金额只取决于虚拟模型的售价，与最终选中哪个渠道无关（技术方案 §6.4：
	// 售价挂在虚拟模型上），所以可以先 Reserve，再在重试循环里尝试各个渠道。
	sellBook := snap.SellPriceBooks[vm.ID]
	if !s.reserve(w, r, log, adm, schema.Usage{InputTokens: int64(estInput), OutputTokens: int64(reserveOutput)}) {
		return
	}

	meta := requestMeta{
		requestID: requestID, accountID: principal.AccountID, apiKeyID: principal.APIKeyID,
		accountTier: principal.AccountTier,
		vmName:      vm.Name, vmID: vm.ID, isStream: stream, clientWantsUsage: clientWantsUsage,
		clientIP: clientIP(r), userAgent: r.UserAgent(), start: start,
		logEndpoint: logEndpointChat, imageInputs: imageInputs,
	}
	meta.appName, meta.appURL = appAttribution(r)

	// 只有真正进入重试循环、会对上游发起至少一次尝试的请求才计入"正常请求"基数
	// （技术方案 §7.7 的重试预算比较的是"重试数 vs 正常请求数"，鉴权失败/限流拒绝/
	// 余额不足这些根本没打到上游的请求不应该稀释这个比例）。
	s.RetryBudget.RecordRequest()
	resp, adp, picked, trace, err := s.callUpstreamWithRetry(ctx, log, snap, vm, features, principal.AccountTier, principal.AccountID, specChat.path, jsonRequest(specChat.path, reqMap))
	if err != nil {
		s.releaseQuietly(log, requestID)
		status, code, msg := classifyRelayError(err)
		httpx.WriteError(w, r, status, code, msg)
		s.logFailure(meta, trace, status, code, len(trace))
		return
	}
	defer resp.Body.Close()

	// 成本价挂在渠道上（§6.4），只有到这里选定了 picked.Channel 才知道用哪个
	// cost book；没配置成本价的渠道 costBook 是零值（Components 为空），
	// computeCostAmount 会据此返回 nil，不记一个假的 0 成本。
	costBook := snap.CostPriceBooks[picked.Channel.ID]

	if stream {
		s.handleStream(ctx, log, w, r, meta, resp, adp, picked, sellBook, costBook, snap.FXRates, estInput, reserveOutput, trace)
		return
	}
	s.handleNonStream(ctx, log, w, r, meta, resp, adp, picked, sellBook, costBook, snap.FXRates, estInput, reserveOutput, trace)
}

// clientIP 尽量拿到客户端地址（去掉端口）；拿不到时原样返回 RemoteAddr，
// 拿不到就是空字符串——写 request_logs 时空字符串会被存成 NULL。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// upstreamClientError 包装一次不可重试（或重试耗尽后最后一次）的上游错误，
// 供 ChatCompletions 决定回给客户端的状态码。
type upstreamClientError struct {
	class  adapter.ErrorClass
	status int
	detail string // 上游错误体里的原因（已截断），只用于 400 类错误回显给用户
}

func (e *upstreamClientError) Error() string {
	return fmt.Sprintf("relay: upstream error (class=%s, status=%d)", e.class, e.status)
}

// classifyRelayError 把 callUpstreamWithRetry 的错误映射为返回给客户端的状态码/错误码/
// 提示，供各转发端点和 request_logs 共用同一套判定逻辑。提示要说清楚原因（多模态
// 技术方案 D3）：以前一律是 "Upstream request failed."，用户无从判断是参数问题、
// 渠道能力不匹配还是上游故障。
func classifyRelayError(err error) (status int, code, message string) {
	var uerr *upstreamClientError
	switch {
	case errors.As(err, &uerr):
		status, code = clientFacingError(uerr.class)
		switch code {
		case "invalid_request":
			message = "Upstream rejected the request."
			if uerr.detail != "" {
				message = "Upstream rejected the request: " + uerr.detail
			}
		case "content_filtered":
			message = "The upstream provider rejected the request by content moderation."
		default:
			message = "Upstream provider failed after retries."
		}
		return status, code, message
	case errors.Is(err, router.ErrNoAvailableChannel):
		return http.StatusServiceUnavailable, "no_available_channel",
			"No upstream channel can serve this request right now (unsupported capability or context length, or all channels unhealthy)."
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream provider failed after retries."
	}
}

// callUpstreamWithRetry 是重试/故障转移的核心循环（技术方案 §7.6-7.7）：
//   - Key 级错误（429/配额耗尽/Key 失效）→ 冷却该 Key（Redis 共享），同渠道换 Key 重试。
//   - 渠道级错误（连接失败/5xx）→ 记入该渠道的熔断器，换渠道重试。
//   - 请求本身有问题（400/内容审核拦截）→ 不重试，直接返回。
//   - 达到 MaxAttempts 或 TotalDeadline 后，返回最后一次的错误。
//
// 返回的 *http.Response 处于"已经拿到 2xx 响应头、尚未读取响应体"的状态，
// 调用方从这里开始才真正向客户端转发内容——转发开始之后就不再有重试的机会了。
// trace 记录了每一次真正发起的尝试（无论成败），供 request_logs 落盘审计。
func (s *Service) callUpstreamWithRetry(ctx context.Context, log *slog.Logger, snap *catalog.Snapshot, vm *catalog.VirtualModel,
	features router.Features, tier string, accountID int64, endpoint string, build requestBuilder) (*http.Response, adapter.Adapter, *router.Picked, []reqlog.AttemptTraceEntry, error) {

	maxAttempts := s.Cfg.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	deadline := time.Now().Add(s.Cfg.Retry.TotalDeadline)

	excludedChannels := map[int64]bool{}
	excludedKeys := map[int64]bool{}
	// 不能服务该端点的渠道直接排除：协议不支持（如 image 模型误挂在 anthropic 协议
	// 渠道上），或供应商方言声明不支持 / 指定的 codec 不存在。宁可 503
	// no_available_channel，也不能把请求打到错误的上游路径。
	for _, c := range snap.ChannelsByVM[vm.ID] {
		acct, ok := snap.ProviderAccounts[c.ProviderAccountID]
		if !ok {
			continue
		}
		if adp, ok := s.Adapters.For(acct.Protocol); !ok || !adapter.Serves(adp, acct, endpoint, c.UpstreamModel) {
			excludedChannels[c.ID] = true
		}
		if name := adapter.CodecName(acct, endpoint, c.UpstreamModel); name != "" && slices.Contains(s.Cfg.DisabledCodecs, name) {
			excludedChannels[c.ID] = true
		}
	}
	var trace []reqlog.AttemptTraceEntry
	var lastErr error
	attemptsMade := 0 // 真正对上游发起过的尝试次数，不含熔断器竞态导致的空转（见下）

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if !deadline.IsZero() && time.Now().After(deadline) {
			if lastErr != nil {
				return nil, nil, nil, trace, fmt.Errorf("relay: retry total deadline exceeded: %w", lastErr)
			}
			return nil, nil, nil, trace, errors.New("relay: retry total deadline exceeded")
		}

		opts := router.SelectOptions{ExcludeChannels: excludedChannels, ExcludeKeys: excludedKeys}
		if s.Health != nil {
			opts.ChannelHealth = s.Health
			opts.KeyHealth = s.Health
		}
		picked, perr := router.Pick(ctx, snap, vm, features, tier, accountID, opts)
		if perr != nil {
			if lastErr != nil {
				return nil, nil, nil, trace, fmt.Errorf("%w (previous attempt: %v)", perr, lastErr)
			}
			return nil, nil, nil, trace, perr
		}

		var done func(bool)
		if s.Health != nil {
			var allowed bool
			done, allowed = s.Health.TryChannel(picked.Channel.ID)
			if !allowed {
				// 熔断器刚好在过滤之后、真正尝试之前变成不可用（并发场景），换下一个候选，
				// 不计入 attempt 预算的浪费——但为避免死循环，仍然把它排除掉。
				excludedChannels[picked.Channel.ID] = true
				attempt--
				continue
			}
		}

		if attemptsMade > 0 && !s.RetryBudget.TryRetry() {
			// 全局重试预算耗尽（技术方案 §7.7）：不再换渠道/换 Key 重试，直接把最后
			// 一次的错误返回给客户端，防止上游整体故障时重试把流量放大、雪上加霜。
			if done != nil {
				done(false)
			}
			if lastErr != nil {
				return nil, nil, nil, trace, fmt.Errorf("relay: retry budget exhausted: %w", lastErr)
			}
			return nil, nil, nil, trace, errors.New("relay: retry budget exhausted")
		}
		attemptsMade++

		adp, aok := s.Adapters.For(picked.Account.Protocol)
		if !aok {
			if done != nil {
				done(false)
			}
			return nil, nil, nil, trace, fmt.Errorf("relay: no adapter registered for protocol %q", picked.Account.Protocol)
		}

		picked = s.withAltHost(picked)
		target := adapter.Target{Channel: picked.Channel, Account: picked.Account, Key: picked.Key}
		codecName := adapter.CodecName(picked.Account, endpoint, picked.Channel.UpstreamModel)
		entry := func(status string, latency int64) reqlog.AttemptTraceEntry {
			return reqlog.AttemptTraceEntry{ChannelID: picked.Channel.ID, KeyID: picked.Key.ID, Status: status, LatencyMs: latency, Codec: codecName}
		}
		upstreamReq, berr := build(ctx, adp, target)
		if berr != nil {
			if done != nil {
				done(false)
			}
			return nil, nil, nil, trace, fmt.Errorf("relay: build upstream request: %w", berr)
		}
		// 方言 transport.timeout_ms：非流式请求的整体超时（含读响应体；成功时在关闭响应体时释放）。
		var actx context.Context
		var cancel context.CancelFunc
		if d := picked.Account.Dialect; d != nil && d.Transport.TimeoutMs > 0 && !features.Stream && ctx.Value(streamingKey{}) == nil {
			actx, cancel = context.WithTimeout(upstreamReq.Context(), time.Duration(d.Transport.TimeoutMs)*time.Millisecond)
		} else {
			actx, cancel = context.WithCancel(upstreamReq.Context())
		}
		upstreamReq = upstreamReq.WithContext(actx)
		// 首字节超时：按端点区分，拿到响应头后解除，不影响读响应体（流式可以持续很久）。
		headerTimeout := s.Cfg.UpstreamHeaderTimeout
		if endpoint == adapter.EndpointImages && s.Cfg.ImagesHeaderTimeout > 0 {
			headerTimeout = s.Cfg.ImagesHeaderTimeout
		}
		var headerTimer *time.Timer
		if headerTimeout > 0 {
			headerTimer = time.AfterFunc(headerTimeout, cancel)
		}

		attemptStart := time.Now()
		resp, derr := s.HTTP.Do(upstreamReq)
		attemptLatency := time.Since(attemptStart).Milliseconds()
		if headerTimer != nil && !headerTimer.Stop() && derr == nil {
			// 计时器恰好在拿到响应头之后触发：响应体已不可读，按超时处理
			_ = resp.Body.Close()
			derr = fmt.Errorf("relay: upstream response header timeout after %s", headerTimeout)
		}
		if derr != nil {
			cancel()
			if done != nil {
				done(false)
			}
			trace = append(trace, entry("connection_error", attemptLatency))
			excludedChannels[picked.Channel.ID] = true
			lastErr = derr
			log.Warn("upstream call failed, retrying", "attempt", attempt, "channel_id", picked.Channel.ID, "error", derr)
			continue
		}

		if resp.StatusCode < 400 {
			ie, perr := adapter.PeekInBandError(target, resp, s.Cfg.MaxUpstreamBody)
			if perr == nil && ie == nil {
				if done != nil {
					done(true)
				}
				trace = append(trace, entry("success", attemptLatency))
				resp.Body = cancelOnClose{resp.Body, cancel}
				return resp, adp, picked, trace, nil
			}
			_ = resp.Body.Close()
			cancel()
			if done != nil {
				done(false)
			}
			if perr != nil {
				trace = append(trace, entry("connection_error", attemptLatency))
				excludedChannels[picked.Channel.ID] = true
				lastErr = perr
				continue
			}
			trace = append(trace, entry(string(ie.Class), attemptLatency))
			lastErr = &upstreamClientError{class: ie.Class, status: ie.Status, detail: ie.Detail}
			log.Warn("upstream returned an error inside a 2xx response", "attempt", attempt, "channel_id", picked.Channel.ID, "class", ie.Class)
			if !ie.Class.Retryable() {
				return nil, nil, nil, trace, lastErr
			}
			s.penalize(ctx, picked, ie.Class, resp.Header, excludedChannels, excludedKeys)
			continue
		}

		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		cancel()
		class := adp.ClassifyError(resp.StatusCode, errBody)
		// 方言 errors.challenge_is_transient：Cloudflare 质询页的 403 不是 Key 失效。
		if d := picked.Account.Dialect; class == adapter.ErrClassKeyInvalid && d != nil && d.Errors.ChallengeIsTransient && resp.Header.Get("Cf-Mitigated") == "challenge" {
			class = adapter.ErrClassUpstreamUnavailable
		}
		if done != nil {
			done(false)
		}
		trace = append(trace, entry(string(class), attemptLatency))
		lastErr = &upstreamClientError{class: class, status: resp.StatusCode, detail: upstreamErrorDetail(errBody)}

		log.Warn("upstream returned error", "attempt", attempt, "channel_id", picked.Channel.ID,
			"key_id", picked.Key.ID, "status", resp.StatusCode, "class", class)

		// 方言 auth.alternate_hosts：Key 在当前域名上无效时换下一个备用域名重试，不冻结 Key。
		if class == adapter.ErrClassKeyInvalid && s.nextAltHost(picked) {
			log.Info("key rejected, switching to alternate host", "channel_id", picked.Channel.ID, "key_id", picked.Key.ID)
			continue
		}
		if !class.Retryable() {
			return nil, nil, nil, trace, lastErr
		}

		s.penalize(ctx, picked, class, resp.Header, excludedChannels, excludedKeys)
	}

	return nil, nil, nil, trace, fmt.Errorf("relay: exhausted %d attempts: %w", maxAttempts, lastErr)
}

// penalize 按错误类别冷却 Key 或排除渠道（技术方案 §7.6），HTTP 错误与 2xx 内的错误共用。
func (s *Service) penalize(ctx context.Context, picked *router.Picked, class adapter.ErrorClass, header http.Header,
	excludedChannels, excludedKeys map[int64]bool) {
	switch class {
	case adapter.ErrClassRateLimited:
		d := retryAfter(header, s.Cfg.Retry.DefaultCooldown, s.Cfg.Retry.MaxCooldown)
		if s.Health != nil {
			s.Health.CooldownKey(ctx, picked.Key.ID, d)
		}
		excludedKeys[picked.Key.ID] = true
	case adapter.ErrClassKeyExhausted, adapter.ErrClassKeyInvalid:
		// 技术方案 §7.6：这类问题本质上需要人工介入（换 Key/充值），这里先用较长的
		// 冷却时间近似"标记失效"，避免同一 Key 在短时间内被反复选中；DB 状态更新与
		// 告警是运维工具的职责，留作后续（worker 或 admin 侧）。
		if s.Health != nil {
			s.Health.CooldownKey(ctx, picked.Key.ID, s.Cfg.Retry.KeyDownCooldown)
		}
		excludedKeys[picked.Key.ID] = true
	case adapter.ErrClassUpstreamUnavailable:
		excludedChannels[picked.Channel.ID] = true
	}
}

// retryAfter 解析上游的 Retry-After 头（RFC 7231，秒数形式；HTTP-date 形式不常见，
// 这里不处理，回退到默认值），并夹在 [0, max] 范围内。
func retryAfter(h http.Header, def, max time.Duration) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return def
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return def
	}
	d := time.Duration(secs) * time.Second
	if max > 0 && d > max {
		return max
	}
	return d
}

func (s *Service) handleNonStream(ctx context.Context, log *slog.Logger, w http.ResponseWriter, r *http.Request, meta requestMeta,
	resp *http.Response, adp adapter.Adapter, picked *router.Picked, sellBook, costBook pricing.Book, fxRates map[string]decimal.Decimal, estInput, reserveOutput int, trace []reqlog.AttemptTraceEntry) {

	ttft := time.Since(meta.start).Milliseconds()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, s.Cfg.MaxUpstreamBody))
	if err != nil {
		s.releaseQuietly(log, meta.requestID)
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_error", "Failed to read upstream response.")
		s.logFailure(meta, trace, http.StatusBadGateway, "upstream_error", len(trace))
		return
	}

	rewritten, usage, err := adp.DecodeResponse(respBody, meta.vmName, meta.requestID)
	if err != nil {
		s.releaseQuietly(log, meta.requestID)
		log.Error("decode upstream response failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_error", "Failed to decode upstream response.")
		s.logFailure(meta, trace, http.StatusBadGateway, "upstream_error", len(trace))
		return
	}
	if usage.IsZero() {
		usage = fallbackUsage(estInput, reserveOutput)
		log.Warn("upstream did not return usage, using conservative fallback", "request_id", meta.requestID)
	}
	if meta.logEndpoint == logEndpointEmbeddings {
		adapter.ApplyResponseTransforms(targetOf(picked), adapter.EndpointEmbeddings, meta.reqMap, rewritten)
	}

	list, charged, promoID := s.settleQuietly(ctx, log, meta, sellBook, usage)
	httpx.WriteJSON(w, http.StatusOK, rewritten)
	meta.toolCalls = countToolCalls(rewritten)
	costAmount := computeCostAmount(costBook, picked.Account.CostMultiplier, usage, fxRates)
	s.logSuccess(meta, picked, trace, http.StatusOK, ttft, usage, sellBook.ID, list, charged, promoID, costAmount)
}

func (s *Service) handleStream(ctx context.Context, log *slog.Logger, w http.ResponseWriter, r *http.Request, meta requestMeta,
	resp *http.Response, adp adapter.Adapter, picked *router.Picked, sellBook, costBook pricing.Book, fxRates map[string]decimal.Decimal, estInput, reserveOutput int, trace []reqlog.AttemptTraceEntry) {

	ttft := time.Since(meta.start).Milliseconds()

	dec := adp.NewStreamDecoder(resp.Body, meta.vmName, meta.requestID)
	defer dec.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.releaseQuietly(log, meta.requestID)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Streaming is not supported by this server.")
		s.logFailure(meta, trace, http.StatusInternalServerError, "internal_error", len(trace))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var forwardedBytes int
	var stats streamStats
	for {
		chunk, err := dec.Next()
		if err != nil {
			// 上游在流中用 error chunk 报错（OpenRouter 等）：流已经开始不能重试，
			// 写出 OpenAI 风格的 error chunk 后结束，按已转发内容结算（同断流）。
			var se *adapter.StreamError
			if errors.As(err, &se) {
				meta.streamErr = "upstream_error"
				log.Warn("upstream reported an error mid-stream", "error", se.Message)
				if raw, merr := json.Marshal(map[string]any{"error": map[string]any{
					"message": "Upstream provider failed mid-stream: " + se.Message, "type": "api_error",
					"code": "upstream_error", "request_id": meta.requestID}}); merr == nil {
					_, _ = w.Write(append(append([]byte("data: "), raw...), '\n', '\n'))
					flusher.Flush()
				}
			}
			break // io.EOF（正常结束）或读取错误（客户端断开/上游中断）都在这里停止转发
		}
		stats.observe(chunk, time.Now())
		// 客户端没有自己要 include_usage 时，不把 relay 为了计费而注入的
		// usage-only chunk 转发出去——协议行为要和客户端自己发起、不带
		// stream_options 的请求完全一致（技术方案 §7.4）。usage 已经在
		// dec.Next() 内部解析并累计进 dec.Usage()，跳过转发不影响计费。
		// 有的上游（OpenRouter、SiliconFlow）在带内容的 chunk 上也附带 usage，
		// 同样剥掉，让客户端看到的行为在各家上游之间一致（多供应商实施方案 §4）。
		if !meta.clientWantsUsage {
			if isUsageOnlyChunk(chunk) {
				continue
			}
			chunk = stripChunkUsage(chunk)
		}
		if _, werr := w.Write(chunk); werr != nil {
			break // 客户端已断开，停止写入；下面仍然会按已产生内容结算
		}
		forwardedBytes += len(chunk)
		flusher.Flush()
	}

	usage := dec.Usage()
	if usage.IsZero() {
		// 上游完全没给 usage（多数场景是客户端中途断开，上游还没来得及吐出
		// 最后的 usage chunk）：不能再按 reserveOutput 这个预扣上限收费——
		// 那是"最多可能用掉多少"，用户实际可能只看到了几个字就断开了。改成按
		// 已经转发给客户端的字节数估算，同时仍然不超过 reserveOutput（防止
		// 估算函数本身出问题时超收）。usage_source 仍然是 estimated：这依旧是
		// 估算值，不是上游确认的真实用量。
		estOutput := estimateTokens(forwardedBytes)
		if estOutput > reserveOutput {
			estOutput = reserveOutput
		}
		usage = fallbackUsage(estInput, estOutput)
		log.Warn("stream ended without usage, estimating from forwarded content", "request_id", meta.requestID, "forwarded_bytes", forwardedBytes)
	}
	list, charged, promoID := s.settleQuietly(ctx, log, meta, sellBook, usage)
	costAmount := computeCostAmount(costBook, picked.Account.CostMultiplier, usage, fxRates)
	meta.genMs, meta.toolCalls = stats.genMillis(), len(stats.toolCalls)
	s.logSuccess(meta, picked, trace, http.StatusOK, ttft, usage, sellBook.ID, list, charged, promoID, costAmount)
}

// releaseQuietly / settleQuietly：结算失败不应该影响已经发给客户端的响应
// （响应已经发出去了，回滚没有意义），但必须记录下来供 worker 对账发现
// （技术方案 §7.9.3 的"未结算冻结"回收兜底）。
func (s *Service) releaseQuietly(log *slog.Logger, requestID string) {
	if err := s.Wallet.Release(context.Background(), requestID); err != nil {
		log.Error("release reservation failed", "error", err)
	}
}

// settleQuietly 计算原价、按促销引擎算出实扣价（若配置了 Promotion），再结算钱包。
// 返回 (原价, 实扣价, 命中的促销 ID 列表)，供 request_logs 记录完整的计费快照。
func (s *Service) settleQuietly(ctx context.Context, log *slog.Logger, meta requestMeta, book pricing.Book, usage schema.Usage) (list, charged int64, promotionIDs []int64) {
	list, _ = pricing.Charge(book, usage.ToPricing(), "default", time.Now(), pricing.RoundCeil)
	charged = list

	// 用独立的、不随 HTTP 请求取消的 context：客户端断开不应该导致结算/促销扣减被跳过
	// （技术方案 §7.8："无论成功、失败、断开，defer 中都执行结算"）。
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if s.Promotion != nil {
		c, promoIDs, err := s.Promotion.Quote(settleCtx, meta.accountID, meta.accountTier, meta.vmName, list)
		if err != nil {
			// 促销引擎故障不应该阻塞计费：退回按原价收取，只记日志。
			log.Error("promotion quote failed, charging list price", "error", err)
		} else {
			charged, promotionIDs = c, promoIDs
		}
	}

	if _, err := s.Wallet.Settle(settleCtx, meta.requestID, charged, meta.vmName); err != nil {
		log.Error("settle failed", "error", err, "amount", charged)
	}
	return list, charged, promotionIDs
}

// logSuccess / logFailure 把一次请求的结果异步写入 request_logs（§6.8/§7.13）。
// s.ReqLog 为 nil 时 Write 是安全的 no-op（见 reqlog.Writer 的方法注释）。
func (s *Service) logSuccess(meta requestMeta, picked *router.Picked, trace []reqlog.AttemptTraceEntry,
	httpStatus int, ttftMs int64, usage schema.Usage, sellBookID int64, list, charged int64, promotionIDs []int64, costAmount *int64) {

	rec := reqlog.Record{
		RequestID: meta.requestID, CreatedAt: meta.start, AccountID: meta.accountID, APIKeyID: meta.apiKeyID,
		VirtualModel: meta.vmName, VirtualModelID: meta.vmID, Endpoint: meta.logEndpoint, IsStream: meta.isStream,
		Status: "success", HTTPStatus: httpStatus, ErrorCode: meta.streamErr, Attempts: len(trace), AttemptTrace: trace,
		TTFTMillis: &ttftMs, LatencyMillis: time.Since(meta.start).Milliseconds(),
		Usage: usage, ClientIP: meta.clientIP, UserAgent: meta.userAgent,
		GenMillis: meta.genMs, ToolCalls: meta.toolCalls, ImageInputs: meta.imageInputs,
		AppName: meta.appName, AppURL: meta.appURL,
	}
	if meta.streamErr != "" {
		rec.Status = "upstream_error"
	}
	if picked != nil {
		rec.ChannelID = &picked.Channel.ID
		rec.ProviderKeyID = &picked.Key.ID
		if picked.Channel.ExperimentKey != nil {
			rec.ExperimentKey = *picked.Channel.ExperimentKey
			rec.VariantLabel = *picked.Channel.VariantLabel
		}
	}
	if sellBookID != 0 {
		rec.SellBookID = &sellBookID
	}
	rec.ListAmount = &list
	rec.ChargedAmount = &charged
	rec.CostAmount = costAmount
	rec.PromotionIDs = promotionIDs

	s.ReqLog.Write(rec)
}

func (s *Service) logFailure(meta requestMeta, trace []reqlog.AttemptTraceEntry, httpStatus int, errorCode string, attempts int) {
	rec := reqlog.Record{
		RequestID: meta.requestID, CreatedAt: meta.start, AccountID: meta.accountID, APIKeyID: meta.apiKeyID,
		VirtualModel: meta.vmName, VirtualModelID: meta.vmID, Endpoint: meta.logEndpoint, IsStream: meta.isStream,
		Status: "upstream_error", HTTPStatus: httpStatus, ErrorCode: errorCode, Attempts: attempts, AttemptTrace: trace,
		LatencyMillis: time.Since(meta.start).Milliseconds(),
		Usage:         schema.Usage{Source: schema.UsageSourceEstimated}, // 未产生任何计费用量
		ClientIP:      meta.clientIP, UserAgent: meta.userAgent,
		ImageInputs: meta.imageInputs, AppName: meta.appName, AppURL: meta.appURL,
	}
	if n := len(trace); n > 0 {
		last := trace[n-1]
		rec.ChannelID = &last.ChannelID
		rec.ProviderKeyID = &last.KeyID
	}
	s.ReqLog.Write(rec)
}

// streamingKey 标记流式请求的 context（见 dispatch）。
type streamingKey struct{}

// cancelOnClose 在关闭响应体时释放方言超时的 context。
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// withAltHost 返回把账号 base_url 换成该 Key 已记住的备用域名后的 picked（没有则原样返回）。
func (s *Service) withAltHost(p *router.Picked) *router.Picked {
	v, ok := s.altHosts.Load(p.Key.ID)
	if !ok || v.(string) == p.Account.BaseURL {
		return p
	}
	acct := *p.Account
	acct.BaseURL = v.(string)
	cp := *p
	cp.Account = &acct
	return &cp
}

// nextAltHost 让该 Key 改用方言 auth.alternate_hosts 中的下一个域名；已经是最后一个时
// 清掉记忆（回到主域名）并返回 false，由调用方按 Key 失效处理。
func (s *Service) nextAltHost(p *router.Picked) bool {
	d := p.Account.Dialect
	if d == nil || len(d.Auth.AlternateHosts) == 0 {
		return false
	}
	next := 0
	if i := slices.Index(d.Auth.AlternateHosts, p.Account.BaseURL); i >= 0 {
		next = i + 1
	}
	if next >= len(d.Auth.AlternateHosts) {
		s.altHosts.Delete(p.Key.ID)
		return false
	}
	s.altHosts.Store(p.Key.ID, d.Auth.AlternateHosts[next])
	return true
}
