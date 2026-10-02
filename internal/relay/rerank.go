package relay

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// Rerank 是 POST /v1/rerank：按 query 对 documents 重排序（Cohere/Jina/SiliconFlow
// 通用形状）。经渠道方言选择的 codec 转发（百炼走 /compatible-api/v1/reranks）；
// 按输入 token 计费（input 计量项），用量字段由 codec 按方言提取。
func (s *Service) Rerank(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	log := s.Logger.With("request_id", httpx.RequestIDFromContext(ctx))

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
	adm, ok := s.admit(w, r, log, principal, specRerank, modelName)
	if !ok {
		return
	}
	defer adm.release()

	query, _ := reqMap["query"].(string)
	docs, _ := reqMap["documents"].([]any)
	if strings.TrimSpace(query) == "" || len(docs) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"query\" and a non-empty \"documents\" array are required.")
		return
	}
	if len(docs) > s.Cfg.MaxRerankDocuments {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("\"documents\" must contain at most %d items.", s.Cfg.MaxRerankDocuments))
		return
	}

	// 请求体基本就是 query + documents 的文本，直接按体积估算。
	estInput := estimateTokens(len(body))
	if !s.consumeTPM(w, r, adm, int64(estInput)) {
		return
	}
	estimate := schema.Usage{InputTokens: int64(estInput)}
	if !s.reserve(w, r, log, adm, estimate) {
		return
	}

	meta := newMeta(r, adm, specRerank, start)
	call := &adapter.Call{Endpoint: specRerank.path, JSON: reqMap}
	resp, _, picked, trace, ok := s.dispatch(w, r, log, adm, specRerank, meta, router.Features{}, codecRequest(call))
	if !ok {
		return
	}
	defer resp.Body.Close()
	s.finishCodec(w, r, log, adm, meta, resp, picked, trace, call, estimatedIfZero(log, meta.logEndpoint, estimate))
}
