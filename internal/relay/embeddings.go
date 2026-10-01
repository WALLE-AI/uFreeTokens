package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// Embeddings 是 POST /v1/embeddings 的 http.HandlerFunc（技术方案 Phase 2）。
// 复用 ChatCompletions 同一套鉴权/限流/预扣/重试/结算管线（callUpstreamWithRetry、
// handleNonStream 都是共用的），差异只在：不流式、没有工具调用/JSON Schema
// 相关的能力过滤、用量只有 input（没有 completion，Reserve/Charge 时
// OutputTokens 恒为 0）。
//
// 已知范围限制：只有 openai 协议的渠道能正常工作——AnthropicAdapter/
// GeminiAdapter 的 BuildRequest 会忽略 endpoint 参数、始终把请求发到它们自己
// 的 chat 端点（/messages、:generateContent），不会被正确路由到一个 embeddings
// 端点。这在实践中不是大问题：目前主流 embedding 模型基本都是通过 OpenAI 兼容
// 接口提供的，但如果给一个 embedding 类型的虚拟模型接了 anthropic/gemini 协议
// 的渠道，请求会打到错误的上游路径而失败，不会是一个更隐蔽的错误结果。
func (s *Service) Embeddings(w http.ResponseWriter, r *http.Request) {
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

	// 类型必须是 embedding——一个 chat 模型不该被 /v1/embeddings 路由到，反过来
	// 也一样（ChatCompletions 目前没有做对称检查，是既有的已知缺口，不在这里
	// 顺带修，只保证这条新路径自己是对的）。
	vm, ok := snap.Models[modelName]
	if !ok || vm.Type != "embedding" || !tierCanSee(vm.VisibleTiers, principal.AccountTier) {
		httpx.WriteError(w, r, http.StatusNotFound, "model_not_found", "The requested model does not exist.")
		return
	}

	estInput := estimateTokens(len(body))

	if s.RateLimit != nil {
		if res := s.RateLimit.ConsumeTPM(ctx, rlSubject, intOrZero(principal.TPMLimit), int64(estInput)); !res.Allowed {
			writeRateLimited(w, r, res, "rate_limit_exceeded", "Token-per-minute quota exceeded.")
			return
		}
	}

	features := router.Features{EstInputTokens: estInput}

	sellBook := snap.SellPriceBooks[vm.ID]
	quoteAmount, _ := pricing.Charge(sellBook, pricing.Usage{InputTokens: int64(estInput)}, "default", time.Now(), pricing.RoundCeil)

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
		vmName:      vm.Name, vmID: vm.ID, clientIP: clientIP(r), userAgent: r.UserAgent(), start: start,
		logEndpoint: logEndpointEmbeddings,
	}

	s.RetryBudget.RecordRequest()
	resp, adp, picked, trace, err := s.callUpstreamWithRetry(ctx, log, snap, vm, features, principal.AccountTier, principal.AccountID, embeddingsEndpoint, reqMap)
	if err != nil {
		s.releaseQuietly(log, requestID)
		status, code := classifyRelayError(err)
		httpx.WriteError(w, r, status, code, "Upstream request failed.")
		s.logFailure(meta, trace, status, code, len(trace))
		return
	}
	defer resp.Body.Close()

	costBook := snap.CostPriceBooks[picked.Channel.ID]
	s.handleNonStream(ctx, log, w, r, meta, resp, adp, picked, sellBook, costBook, snap.FXRates, estInput, 0, trace)
}
