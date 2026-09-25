// Package relay 编排一次 /v1/chat/completions 请求的完整生命周期
// （技术方案 §3.2、§7.1）：鉴权信息已由中间件放入 context -> 解析请求 -> 路由选渠道
// -> 预扣费用 -> 转发上游 -> 结算。
//
// 当前范围（有意的阶段性限制，不是遗漏）：
//   - 单次尝试，不做跨渠道/跨 Key 的自动重试与故障转移（技术方案 §7.7）——
//     上游返回可重试类错误时，直接把错误返回给客户端，同时释放预扣的费用。
//     完整的重试循环依赖 §7.6 的运行时健康度/熔断，是下一阶段要接入的部分。
//   - 用量兜底估算是保守占位（上游完全不返回 usage 时，按预扣的上限计费，
//     不会让平台倒贴钱，但也不精确）——真正基于 tokenizer 的估算见 §7.9.4，留作后续。
//   - 请求日志（request_logs）尚未接入，本阶段只有结构化访问日志。
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

const chatEndpoint = "/chat/completions"

// Config 是 relay.Service 的可调参数，默认值见技术方案附录 B。
type Config struct {
	ReserveOutputCap int           // 预扣费用时对 max_tokens 的上限裁剪（技术方案 §7.9.1）
	ReservationTTL   time.Duration // 预扣记录的兜底过期时间，供 worker 回收（尚未实现 worker 侧）
	MaxUpstreamBody  int64         // 非流式响应体读取上限，防止恶意/异常上游返回超大响应
}

func DefaultConfig() Config {
	return Config{
		ReserveOutputCap: 8192,
		ReservationTTL:   30 * time.Minute,
		MaxUpstreamBody:  20 * 1024 * 1024,
	}
}

type Service struct {
	Catalog  *catalog.Store
	Wallet   *wallet.Service
	Adapters *adapter.Registry
	HTTP     *http.Client
	Logger   *slog.Logger
	Cfg      Config
}

// ChatCompletions 是 POST /v1/chat/completions 的 http.HandlerFunc。
func (s *Service) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	features := router.Features{
		Stream:          stream,
		NeedTools:       hasKey(reqMap, "tools"),
		NeedJSONSchema:  hasResponseFormatJSONSchema(reqMap),
		EstInputTokens:  estInput,
		MaxOutputTokens: reserveOutput,
	}

	picked, err := router.Pick(snap, vm, features, principal.AccountTier, nil)
	if err != nil {
		if errors.Is(err, router.ErrNoAvailableChannel) {
			httpx.WriteError(w, r, http.StatusServiceUnavailable, "no_available_channel", "No healthy channel is available for this model right now.")
			return
		}
		log.Error("router pick failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Routing failed.")
		return
	}

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

	adp, ok := s.Adapters.For(picked.Account.Protocol)
	if !ok {
		s.releaseQuietly(log, requestID)
		log.Error("no adapter for protocol", "protocol", picked.Account.Protocol)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Unsupported upstream protocol.")
		return
	}

	target := adapter.Target{Channel: picked.Channel, Account: picked.Account, Key: picked.Key}
	upstreamReq, err := adp.BuildRequest(ctx, target, chatEndpoint, reqMap)
	if err != nil {
		s.releaseQuietly(log, requestID)
		log.Error("build upstream request failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to build upstream request.")
		return
	}

	resp, err := s.HTTP.Do(upstreamReq)
	if err != nil {
		// 连接失败/超时，发生在拿到任何响应之前：本次请求未产生费用，全额释放。
		s.releaseQuietly(log, requestID)
		log.Warn("upstream request failed", "channel_id", picked.Channel.ID, "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_error", "Failed to reach upstream provider.")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		class := adp.ClassifyError(resp.StatusCode, errBody)
		s.releaseQuietly(log, requestID)
		log.Warn("upstream returned error", "channel_id", picked.Channel.ID, "status", resp.StatusCode, "class", class)
		status, code := clientFacingError(class)
		httpx.WriteError(w, r, status, code, "Upstream request failed.")
		return
	}

	if stream {
		s.handleStream(ctx, log, w, r, resp, adp, sellBook, vm.Name, requestID, estInput, reserveOutput)
		return
	}
	s.handleNonStream(ctx, log, w, r, resp, adp, sellBook, vm.Name, requestID, estInput, reserveOutput)
}

func (s *Service) handleNonStream(ctx context.Context, log *slog.Logger, w http.ResponseWriter, r *http.Request,
	resp *http.Response, adp adapter.Adapter, sellBook pricing.Book, vmName, requestID string, estInput, reserveOutput int) {

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, s.Cfg.MaxUpstreamBody))
	if err != nil {
		s.releaseQuietly(log, requestID)
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_error", "Failed to read upstream response.")
		return
	}

	rewritten, usage, err := adp.DecodeResponse(respBody, vmName, requestID)
	if err != nil {
		s.releaseQuietly(log, requestID)
		log.Error("decode upstream response failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_error", "Failed to decode upstream response.")
		return
	}
	if usage.IsZero() {
		usage = fallbackUsage(estInput, reserveOutput)
		log.Warn("upstream did not return usage, using conservative fallback", "request_id", requestID)
	}

	s.settleQuietly(ctx, log, requestID, sellBook, usage)
	httpx.WriteJSON(w, http.StatusOK, rewritten)
}

func (s *Service) handleStream(ctx context.Context, log *slog.Logger, w http.ResponseWriter, r *http.Request, resp *http.Response,
	adp adapter.Adapter, sellBook pricing.Book, vmName, requestID string, estInput, reserveOutput int) {

	dec := adp.NewStreamDecoder(resp.Body, vmName, requestID)
	defer dec.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.releaseQuietly(log, requestID)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Streaming is not supported by this server.")
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
		log.Warn("stream ended without usage, using conservative fallback", "request_id", requestID)
	}
	s.settleQuietly(ctx, log, requestID, sellBook, usage)
}

// releaseQuietly / settleQuietly：结算失败不应该影响已经发给客户端的响应
// （响应已经发出去了，回滚没有意义），但必须记录下来供 worker 对账发现
// （技术方案 §7.9.3 的"未结算冻结"回收兜底）。
func (s *Service) releaseQuietly(log *slog.Logger, requestID string) {
	if err := s.Wallet.Release(context.Background(), requestID); err != nil {
		log.Error("release reservation failed", "error", err)
	}
}

func (s *Service) settleQuietly(ctx context.Context, log *slog.Logger, requestID string, book pricing.Book, usage schema.Usage) {
	amount, _ := pricing.Charge(book, usage.ToPricing(), "default", time.Now(), pricing.RoundCeil)
	// 用独立的、不随 HTTP 请求取消的 context：客户端断开不应该导致结算被跳过
	// （技术方案 §7.8："无论成功、失败、断开，defer 中都执行结算"）。
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.Wallet.Settle(settleCtx, requestID, amount); err != nil {
		log.Error("settle failed", "error", err, "amount", amount)
	}
}
