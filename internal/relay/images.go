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

// ImagesGenerations 是 POST /v1/images/generations。经渠道方言选择的 codec 转发
// （OpenRouter 走 /images，百炼走原生接口）；响应统一为 OpenAI 的 data[]，按实际生成的
// 张数计费（image 计量项）。
func (s *Service) ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	log := s.Logger.With("request_id", httpx.RequestIDFromContext(ctx))

	principal, ok := auth.FromContext(ctx)
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
		return
	}
	_, reqMap, ok := s.readJSON(w, r)
	if !ok {
		return
	}
	modelName, _ := reqMap["model"].(string)
	adm, ok := s.admit(w, r, log, principal, specImages, modelName)
	if !ok {
		return
	}
	defer adm.release()

	if prompt, _ := reqMap["prompt"].(string); strings.TrimSpace(prompt) == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"prompt\" is required.")
		return
	}
	n := requestedImageCount(reqMap)
	if n > s.Cfg.MaxImagesPerRequest {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("At most %d images can be generated per request.", s.Cfg.MaxImagesPerRequest))
		return
	}
	estimate := schema.Usage{Images: int64(n)}
	if !s.reserve(w, r, log, adm, estimate) {
		return
	}

	meta := newMeta(r, adm, specImages, start)
	call := &adapter.Call{Endpoint: specImages.path, JSON: reqMap}
	resp, _, picked, trace, ok := s.dispatch(w, r, log, adm, specImages, meta, router.Features{}, codecRequest(call))
	if !ok {
		return
	}
	defer resp.Body.Close()
	s.finishCodec(w, r, log, adm, meta, resp, picked, trace, call, estimatedIfZero(log, meta.logEndpoint, estimate))
}

// requestedImageCount 取 n 或 batch_size（SiliconFlow），缺省 1。
func requestedImageCount(reqMap map[string]any) int {
	for _, k := range []string{"n", "batch_size"} {
		if v, ok := numberField(reqMap, k); ok && v >= 1 {
			return int(v)
		}
	}
	return 1
}
