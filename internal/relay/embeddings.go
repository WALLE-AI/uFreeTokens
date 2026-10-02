package relay

import (
	"net/http"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// Embeddings 是 POST /v1/embeddings 的 http.HandlerFunc（技术方案 Phase 2）。
// 复用 ChatCompletions 同一套鉴权/限流/预扣/重试/结算管线（callUpstreamWithRetry、
// handleNonStream 都是共用的），差异只在：不流式、没有工具调用/JSON Schema
// 相关的能力过滤、用量只有 input（没有 completion，Reserve/Charge 时
// OutputTokens 恒为 0）。
//
// 只有 openai 协议的渠道能处理 embeddings：anthropic/gemini 协议的渠道在
// callUpstreamWithRetry 里按 adapter.SupportsEndpoint 被排除。
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
	body, reqMap, ok := s.readJSON(w, r)
	if !ok {
		return
	}
	modelName, _ := reqMap["model"].(string)
	adm, ok := s.admit(w, r, log, principal, specEmbeddings, modelName)
	if !ok {
		return
	}
	defer adm.release()

	estInput := estimateTokens(len(body))
	if !s.consumeTPM(w, r, adm, int64(estInput)) {
		return
	}
	if !s.reserve(w, r, log, adm, schema.Usage{InputTokens: int64(estInput)}) {
		return
	}

	meta := newMeta(r, adm, specEmbeddings, start)
	meta.reqMap = reqMap
	resp, adp, picked, trace, ok := s.dispatch(w, r, log, adm, specEmbeddings, meta,
		router.Features{EstInputTokens: estInput}, jsonRequest(specEmbeddings.path, reqMap))
	if !ok {
		return
	}
	defer resp.Body.Close()

	sellBook := adm.snap.SellPriceBooks[adm.vm.ID]
	costBook := adm.snap.CostPriceBooks[picked.Channel.ID]
	s.handleNonStream(ctx, log, w, r, meta, resp, adp, picked, sellBook, costBook, adm.snap.FXRates, estInput, 0, trace)
}
