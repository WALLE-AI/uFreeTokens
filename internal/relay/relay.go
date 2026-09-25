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
	"strconv"
	"time"

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
	chatEndpoint    = "/chat/completions" // 拼在 provider_accounts.base_url 后面的上游路径
	logEndpointChat = "chat.completions"  // request_logs.endpoint 里记录的名字
)

// Config 是 relay.Service 的可调参数，默认值见技术方案附录 B。
type Config struct {
	ReserveOutputCap int           // 预扣费用时对 max_tokens 的上限裁剪（技术方案 §7.9.1）
	ReservationTTL   time.Duration // 预扣记录的兜底过期时间，供 worker 回收（尚未实现 worker 侧）
	MaxUpstreamBody  int64         // 非流式响应体读取上限，防止恶意/异常上游返回超大响应
	Retry            RetryConfig
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
		ReserveOutputCap: 8192,
		ReservationTTL:   30 * time.Minute,
		MaxUpstreamBody:  20 * 1024 * 1024,
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
	isStream    bool
	clientIP    string
	userAgent   string
	start       time.Time
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

	body, err := io.ReadAll(io.LimitReader(r.Body, s.Cfg.MaxUpstreamBody))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Failed to read request body.")
		return
	}
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return
	}

	modelName, _ := reqMap["model"].(string)
	if modelName == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"model\" is required.")
		return
	}
	if !modelAllowed(principal.AllowedModels, modelName) {
		httpx.WriteError(w, r, http.StatusForbidden, "model_not_allowed", "This API key is not allowed to use this model.")
		return
	}

	rlSubject := fmt.Sprintf("apikey:%d", principal.APIKeyID)
	if s.RateLimit != nil {
		if res := s.RateLimit.AllowRPM(ctx, rlSubject, intOrZero(principal.RPMLimit)); !res.Allowed {
			writeRateLimited(w, r, res, "rate_limit_exceeded", "Too many requests.")
			return
		}
		release, res := s.RateLimit.AcquireConcurrency(ctx, rlSubject, intOrZero(principal.ConcurrencyLimit), requestID, s.Cfg.Retry.TotalDeadline+time.Minute)
		if !res.Allowed {
			writeRateLimited(w, r, res, "concurrency_limit_exceeded", "Too many concurrent requests.")
			return
		}
		defer release()
	}

	snap, err := s.Catalog.Get(ctx)
	if err != nil {
		log.Error("catalog load failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load model catalog.")
		return
	}

	vm, ok := snap.Models[modelName]
	if !ok || !tierCanSee(vm.VisibleTiers, principal.AccountTier) {
		// 对不可见的模型也统一返回 404，不区分"不存在"和"对你不可见"，避免信息泄露。
		httpx.WriteError(w, r, http.StatusNotFound, "model_not_found", "The requested model does not exist.")
		return
	}

	stream, _ := reqMap["stream"].(bool)
	estInput := estimateTokens(len(body))
	reserveOutput := reserveOutputTokens(reqMap, vm.MaxOutput, s.Cfg.ReserveOutputCap)

	if s.RateLimit != nil {
		amount := int64(estInput + reserveOutput)
		if res := s.RateLimit.ConsumeTPM(ctx, rlSubject, intOrZero(principal.TPMLimit), amount); !res.Allowed {
			writeRateLimited(w, r, res, "rate_limit_exceeded", "Token-per-minute quota exceeded.")
			return
		}
	}

	features := router.Features{
		Stream:          stream,
		NeedTools:       hasKey(reqMap, "tools"),
		NeedJSONSchema:  hasResponseFormatJSONSchema(reqMap),
		EstInputTokens:  estInput,
		MaxOutputTokens: reserveOutput,
	}

	// 预扣的金额只取决于虚拟模型的售价，与最终选中哪个渠道无关（技术方案 §6.4：
	// 售价挂在虚拟模型上），所以可以先 Reserve，再在重试循环里尝试各个渠道。
	sellBook := snap.SellPriceBooks[vm.ID]
	quoteAmount, _ := pricing.Charge(sellBook,
		pricing.Usage{InputTokens: int64(estInput), OutputTokens: int64(reserveOutput)},
		"default", time.Now(), pricing.RoundCeil)

	if _, err := s.Wallet.Reserve(ctx, requestID, principal.AccountID, quoteAmount, s.Cfg.ReservationTTL); err != nil {
		if errors.Is(err, wallet.ErrInsufficientBalance) {
			httpx.WriteError(w, r, http.StatusPaymentRequired, "insufficient_balance", "Insufficient balance.")
			return
		}
		log.Error("reserve failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to reserve balance.")
		return
	}

	meta := requestMeta{
		requestID: requestID, accountID: principal.AccountID, apiKeyID: principal.APIKeyID,
		accountTier: principal.AccountTier,
		vmName:      vm.Name, isStream: stream, clientIP: clientIP(r), userAgent: r.UserAgent(), start: start,
	}

	// 只有真正进入重试循环、会对上游发起至少一次尝试的请求才计入"正常请求"基数
	// （技术方案 §7.7 的重试预算比较的是"重试数 vs 正常请求数"，鉴权失败/限流拒绝/
	// 余额不足这些根本没打到上游的请求不应该稀释这个比例）。
	s.RetryBudget.RecordRequest()
	resp, adp, picked, trace, err := s.callUpstreamWithRetry(ctx, log, snap, vm, features, principal.AccountTier, reqMap)
	if err != nil {
		s.releaseQuietly(log, requestID)
		status, code := classifyRelayError(err)
		httpx.WriteError(w, r, status, code, "Upstream request failed.")
		s.logFailure(meta, trace, status, code, len(trace))
		return
	}
	defer resp.Body.Close()

	// 成本价挂在渠道上（§6.4），只有到这里选定了 picked.Channel 才知道用哪个
	// cost book；没配置成本价的渠道 costBook 是零值（Components 为空），
	// computeCostAmount 会据此返回 nil，不记一个假的 0 成本。
	costBook := snap.CostPriceBooks[picked.Channel.ID]

	if stream {
		s.handleStream(ctx, log, w, r, meta, resp, adp, picked, sellBook, costBook, estInput, reserveOutput, trace)
		return
	}
	s.handleNonStream(ctx, log, w, r, meta, resp, adp, picked, sellBook, costBook, estInput, reserveOutput, trace)
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
}

func (e *upstreamClientError) Error() string {
	return fmt.Sprintf("relay: upstream error (class=%s, status=%d)", e.class, e.status)
}

// classifyRelayError 把 callUpstreamWithRetry 的错误映射为返回给客户端的状态码/错误码，
// 供 ChatCompletions 和 request_logs 共用同一套判定逻辑。
func classifyRelayError(err error) (status int, code string) {
	var uerr *upstreamClientError
	switch {
	case errors.As(err, &uerr):
		return clientFacingError(uerr.class)
	case errors.Is(err, router.ErrNoAvailableChannel):
		return http.StatusServiceUnavailable, "no_available_channel"
	default:
		return http.StatusBadGateway, "upstream_error"
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
	features router.Features, tier string, reqMap map[string]any) (*http.Response, adapter.Adapter, *router.Picked, []reqlog.AttemptTraceEntry, error) {

	maxAttempts := s.Cfg.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	deadline := time.Now().Add(s.Cfg.Retry.TotalDeadline)

	excludedChannels := map[int64]bool{}
	excludedKeys := map[int64]bool{}
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
		picked, perr := router.Pick(ctx, snap, vm, features, tier, opts)
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

		target := adapter.Target{Channel: picked.Channel, Account: picked.Account, Key: picked.Key}
		upstreamReq, berr := adp.BuildRequest(ctx, target, chatEndpoint, reqMap)
		if berr != nil {
			if done != nil {
				done(false)
			}
			return nil, nil, nil, trace, fmt.Errorf("relay: build upstream request: %w", berr)
		}

		attemptStart := time.Now()
		resp, derr := s.HTTP.Do(upstreamReq)
		attemptLatency := time.Since(attemptStart).Milliseconds()
		if derr != nil {
			if done != nil {
				done(false)
			}
			trace = append(trace, reqlog.AttemptTraceEntry{ChannelID: picked.Channel.ID, KeyID: picked.Key.ID, Status: "connection_error", LatencyMs: attemptLatency})
			excludedChannels[picked.Channel.ID] = true
			lastErr = derr
			log.Warn("upstream call failed, retrying", "attempt", attempt, "channel_id", picked.Channel.ID, "error", derr)
			continue
		}

		if resp.StatusCode < 400 {
			if done != nil {
				done(true)
			}
			trace = append(trace, reqlog.AttemptTraceEntry{ChannelID: picked.Channel.ID, KeyID: picked.Key.ID, Status: "success", LatencyMs: attemptLatency})
			return resp, adp, picked, trace, nil
		}

		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		class := adp.ClassifyError(resp.StatusCode, errBody)
		if done != nil {
			done(false)
		}
		trace = append(trace, reqlog.AttemptTraceEntry{ChannelID: picked.Channel.ID, KeyID: picked.Key.ID, Status: string(class), LatencyMs: attemptLatency})
		lastErr = &upstreamClientError{class: class, status: resp.StatusCode}

		log.Warn("upstream returned error", "attempt", attempt, "channel_id", picked.Channel.ID,
			"key_id", picked.Key.ID, "status", resp.StatusCode, "class", class)

		if !class.Retryable() {
			return nil, nil, nil, trace, lastErr
		}

		switch class {
		case adapter.ErrClassRateLimited:
			d := retryAfter(resp.Header, s.Cfg.Retry.DefaultCooldown, s.Cfg.Retry.MaxCooldown)
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

	return nil, nil, nil, trace, fmt.Errorf("relay: exhausted %d attempts: %w", maxAttempts, lastErr)
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
	resp *http.Response, adp adapter.Adapter, picked *router.Picked, sellBook, costBook pricing.Book, estInput, reserveOutput int, trace []reqlog.AttemptTraceEntry) {

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

	list, charged, promoID := s.settleQuietly(ctx, log, meta, sellBook, usage)
	httpx.WriteJSON(w, http.StatusOK, rewritten)
	costAmount := computeCostAmount(costBook, picked.Account.CostMultiplier, usage)
	s.logSuccess(meta, picked, trace, http.StatusOK, ttft, usage, sellBook.ID, list, charged, promoID, costAmount)
}

func (s *Service) handleStream(ctx context.Context, log *slog.Logger, w http.ResponseWriter, r *http.Request, meta requestMeta,
	resp *http.Response, adp adapter.Adapter, picked *router.Picked, sellBook, costBook pricing.Book, estInput, reserveOutput int, trace []reqlog.AttemptTraceEntry) {

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

	for {
		chunk, err := dec.Next()
		if err != nil {
			break // io.EOF（正常结束）或读取错误（客户端断开/上游中断）都在这里停止转发
		}
		if _, werr := w.Write(chunk); werr != nil {
			break // 客户端已断开，停止写入；下面仍然会按已产生内容结算
		}
		flusher.Flush()
	}

	usage := dec.Usage()
	if usage.IsZero() {
		usage = fallbackUsage(estInput, reserveOutput)
		log.Warn("stream ended without usage, using conservative fallback", "request_id", meta.requestID)
	}
	list, charged, promoID := s.settleQuietly(ctx, log, meta, sellBook, usage)
	costAmount := computeCostAmount(costBook, picked.Account.CostMultiplier, usage)
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
// 返回 (原价, 实扣价, 命中的促销 ID)，供 request_logs 记录完整的计费快照。
func (s *Service) settleQuietly(ctx context.Context, log *slog.Logger, meta requestMeta, book pricing.Book, usage schema.Usage) (list, charged int64, promotionID *int64) {
	list, _ = pricing.Charge(book, usage.ToPricing(), "default", time.Now(), pricing.RoundCeil)
	charged = list

	// 用独立的、不随 HTTP 请求取消的 context：客户端断开不应该导致结算/促销扣减被跳过
	// （技术方案 §7.8："无论成功、失败、断开，defer 中都执行结算"）。
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if s.Promotion != nil {
		c, promoID, err := s.Promotion.Quote(settleCtx, meta.accountID, meta.accountTier, meta.vmName, list)
		if err != nil {
			// 促销引擎故障不应该阻塞计费：退回按原价收取，只记日志。
			log.Error("promotion quote failed, charging list price", "error", err)
		} else {
			charged, promotionID = c, promoID
		}
	}

	if _, err := s.Wallet.Settle(settleCtx, meta.requestID, charged, meta.vmName); err != nil {
		log.Error("settle failed", "error", err, "amount", charged)
	}
	return list, charged, promotionID
}

// logSuccess / logFailure 把一次请求的结果异步写入 request_logs（§6.8/§7.13）。
// s.ReqLog 为 nil 时 Write 是安全的 no-op（见 reqlog.Writer 的方法注释）。
func (s *Service) logSuccess(meta requestMeta, picked *router.Picked, trace []reqlog.AttemptTraceEntry,
	httpStatus int, ttftMs int64, usage schema.Usage, sellBookID int64, list, charged int64, promotionID *int64, costAmount *int64) {

	rec := reqlog.Record{
		RequestID: meta.requestID, CreatedAt: meta.start, AccountID: meta.accountID, APIKeyID: meta.apiKeyID,
		VirtualModel: meta.vmName, Endpoint: logEndpointChat, IsStream: meta.isStream,
		Status: "success", HTTPStatus: httpStatus, Attempts: len(trace), AttemptTrace: trace,
		TTFTMillis: &ttftMs, LatencyMillis: time.Since(meta.start).Milliseconds(),
		Usage: usage, ClientIP: meta.clientIP, UserAgent: meta.userAgent,
	}
	if picked != nil {
		rec.ChannelID = &picked.Channel.ID
		rec.ProviderKeyID = &picked.Key.ID
	}
	if sellBookID != 0 {
		rec.SellBookID = &sellBookID
	}
	rec.ListAmount = &list
	rec.ChargedAmount = &charged
	rec.CostAmount = costAmount
	if promotionID != nil {
		rec.PromotionIDs = []int64{*promotionID}
	}

	s.ReqLog.Write(rec)
}

func (s *Service) logFailure(meta requestMeta, trace []reqlog.AttemptTraceEntry, httpStatus int, errorCode string, attempts int) {
	rec := reqlog.Record{
		RequestID: meta.requestID, CreatedAt: meta.start, AccountID: meta.accountID, APIKeyID: meta.apiKeyID,
		VirtualModel: meta.vmName, Endpoint: logEndpointChat, IsStream: meta.isStream,
		Status: "upstream_error", HTTPStatus: httpStatus, ErrorCode: errorCode, Attempts: attempts, AttemptTrace: trace,
		LatencyMillis: time.Since(meta.start).Milliseconds(),
		Usage:         schema.Usage{Source: schema.UsageSourceEstimated}, // 未产生任何计费用量
		ClientIP:      meta.clientIP, UserAgent: meta.userAgent,
	}
	if n := len(trace); n > 0 {
		last := trace[n-1]
		rec.ChannelID = &last.ChannelID
		rec.ProviderKeyID = &last.KeyID
	}
	s.ReqLog.Write(rec)
}
