package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// endpointSpec 声明一个转发端点允许的模型类型/能力与上游逻辑路径（多模态技术方案
// §2.1 的"端点 × 模型类型矩阵"）。所有端点在查到虚拟模型后统一用 servedBy 校验，
// 类型不匹配一律 404 model_not_found——与"不存在/不可见"不做区分，不泄露模型信息。
type endpointSpec struct {
	path       string // adapter.Endpoint*，拼在上游 base_url 后面
	logName    string // request_logs.endpoint
	modelType  string // virtual_models.type
	capability string // 额外要求的能力（audio 模型用 tts/asr 区分），空 = 不要求
}

var (
	specChat           = endpointSpec{adapter.EndpointChat, logEndpointChat, "chat", ""}
	specEmbeddings     = endpointSpec{adapter.EndpointEmbeddings, logEndpointEmbeddings, "embedding", ""}
	specRerank         = endpointSpec{adapter.EndpointRerank, "rerank", "rerank", ""}
	specImages         = endpointSpec{adapter.EndpointImages, "images.generations", "image", ""}
	specSpeech         = endpointSpec{adapter.EndpointSpeech, "audio.speech", "audio", "tts"}
	specTranscriptions = endpointSpec{adapter.EndpointTranscriptions, "audio.transcriptions", "audio", "asr"}
)

func (e endpointSpec) servedBy(vm *catalog.VirtualModel) bool {
	if vm.Type != e.modelType {
		return false
	}
	return e.capability == "" || slices.Contains(vm.Capabilities, e.capability)
}

const msgModelNotFound = "The requested model does not exist or does not support this endpoint."

// errBodyTooLarge 表示请求体超过 MaxUpstreamBody。
var errBodyTooLarge = errors.New("relay: request body too large")

// readBody 读取完整请求体。超过 limit 时返回 errBodyTooLarge——用 limit+1 判断溢出，
// 不能用 io.LimitReader(limit) 静默截断（截断后的 JSON 会被误报成"不是合法 JSON"）；
// cmd/gateway 的 http.MaxBytesHandler 先于这里触发时，返回的是 *http.MaxBytesError。
func readBody(r *http.Request, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) || int64(len(body)) > limit {
		return nil, errBodyTooLarge
	}
	return body, err
}

func (s *Service) writeBodyReadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errBodyTooLarge) {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "request_too_large",
			fmt.Sprintf("Request body exceeds the %d MB limit.", s.Cfg.MaxUpstreamBody>>20))
		return
	}
	httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Failed to read request body.")
}

// readJSON 读取并解析 JSON 请求体；失败时已写好错误响应，返回 ok=false。
func (s *Service) readJSON(w http.ResponseWriter, r *http.Request) ([]byte, map[string]any, bool) {
	body, err := readBody(r, s.Cfg.MaxUpstreamBody)
	if err != nil {
		s.writeBodyReadError(w, r, err)
		return nil, nil, false
	}
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return nil, nil, false
	}
	return body, reqMap, true
}

// admission 是通过了"模型 → Key 白名单 → RPM/并发限流 → 目录查找 → 类型校验"
// 之后的请求上下文。调用方必须 defer release()。
type admission struct {
	principal *auth.Principal
	rlSubject string
	snap      *catalog.Snapshot
	vm        *catalog.VirtualModel
	release   func()
}

// admit 是所有转发端点共用的前置检查；失败时已写好错误响应，返回 ok=false。
func (s *Service) admit(w http.ResponseWriter, r *http.Request, log *slog.Logger, principal *auth.Principal, spec endpointSpec, modelName string) (*admission, bool) {
	ctx := r.Context()
	if modelName == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"model\" is required.")
		return nil, false
	}
	if !modelAllowed(principal.AllowedModels, modelName) {
		httpx.WriteError(w, r, http.StatusForbidden, "model_not_allowed", "This API key is not allowed to use this model.")
		return nil, false
	}

	adm := &admission{principal: principal, rlSubject: fmt.Sprintf("apikey:%d", principal.APIKeyID), release: func() {}}
	if s.RateLimit != nil {
		if res := s.RateLimit.AllowRPM(ctx, adm.rlSubject, intOrZero(principal.RPMLimit)); !res.Allowed {
			writeRateLimited(w, r, res, "rate_limit_exceeded", "Too many requests.")
			return nil, false
		}
		release, res := s.RateLimit.AcquireConcurrency(ctx, adm.rlSubject, intOrZero(principal.ConcurrencyLimit), httpx.RequestIDFromContext(ctx), s.Cfg.Retry.TotalDeadline+time.Minute)
		if !res.Allowed {
			writeRateLimited(w, r, res, "concurrency_limit_exceeded", "Too many concurrent requests.")
			return nil, false
		}
		adm.release = release
	}

	snap, err := s.Catalog.Get(ctx)
	if err != nil {
		adm.release()
		log.Error("catalog load failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load model catalog.")
		return nil, false
	}
	// 不存在、对你不可见、或类型/能力与端点不符，统一 404，避免信息泄露。
	vm, ok := snap.Models[modelName]
	if !ok || !spec.servedBy(vm) || !tierCanSee(vm.VisibleTiers, principal.AccountTier) {
		adm.release()
		httpx.WriteError(w, r, http.StatusNotFound, "model_not_found", msgModelNotFound)
		return nil, false
	}
	adm.snap, adm.vm = snap, vm
	return adm, true
}

// consumeTPM 按 token 数扣减 TPM 配额；amount<=0（图像、语音）不计 TPM。
func (s *Service) consumeTPM(w http.ResponseWriter, r *http.Request, adm *admission, amount int64) bool {
	if s.RateLimit == nil || amount <= 0 {
		return true
	}
	if res := s.RateLimit.ConsumeTPM(r.Context(), adm.rlSubject, intOrZero(adm.principal.TPMLimit), amount); !res.Allowed {
		writeRateLimited(w, r, res, "rate_limit_exceeded", "Token-per-minute quota exceeded.")
		return false
	}
	return true
}

// reserve 按 estimate 用售价算出预扣金额并冻结。
func (s *Service) reserve(w http.ResponseWriter, r *http.Request, log *slog.Logger, adm *admission, estimate schema.Usage) bool {
	quote, _ := pricing.Charge(adm.snap.SellPriceBooks[adm.vm.ID], estimate.ToPricing(), "default", time.Now(), pricing.RoundCeil)
	if _, err := s.Wallet.Reserve(r.Context(), httpx.RequestIDFromContext(r.Context()), adm.principal.AccountID, quote, s.Cfg.ReservationTTL); err != nil {
		if errors.Is(err, wallet.ErrInsufficientBalance) {
			httpx.WriteError(w, r, http.StatusPaymentRequired, "insufficient_balance", "Insufficient balance.")
			return false
		}
		log.Error("reserve failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to reserve balance.")
		return false
	}
	return true
}

func newMeta(r *http.Request, adm *admission, spec endpointSpec, start time.Time) requestMeta {
	meta := requestMeta{
		requestID: httpx.RequestIDFromContext(r.Context()), accountID: adm.principal.AccountID, apiKeyID: adm.principal.APIKeyID,
		accountTier: adm.principal.AccountTier, vmName: adm.vm.Name, vmID: adm.vm.ID,
		clientIP: clientIP(r), userAgent: r.UserAgent(), start: start, logEndpoint: spec.logName,
	}
	meta.appName, meta.appURL = appAttribution(r)
	return meta
}

// requestBuilder 为一次尝试构造上游请求。每次重试都会重新调用（请求体需要可重放）。
type requestBuilder func(ctx context.Context, adp adapter.Adapter, target adapter.Target) (*http.Request, error)

func jsonRequest(endpoint string, reqMap map[string]any) requestBuilder {
	return func(ctx context.Context, adp adapter.Adapter, target adapter.Target) (*http.Request, error) {
		return adp.BuildRequest(ctx, target, endpoint, reqMap)
	}
}

// dispatch 发起带重试的上游调用；失败时释放预扣、写错误响应与 request_logs，返回 ok=false。
func (s *Service) dispatch(w http.ResponseWriter, r *http.Request, log *slog.Logger, adm *admission, spec endpointSpec,
	meta requestMeta, features router.Features, build requestBuilder) (*http.Response, adapter.Adapter, *router.Picked, []reqlog.AttemptTraceEntry, bool) {
	s.RetryBudget.RecordRequest()
	ctx := r.Context()
	if meta.isStream {
		// 流式响应（如语音合成 stream:true）不受方言 transport.timeout_ms 的整体超时约束。
		ctx = context.WithValue(ctx, streamingKey{}, true)
	}
	resp, adp, picked, trace, err := s.callUpstreamWithRetry(ctx, log, adm.snap, adm.vm, features, adm.principal.AccountTier, adm.principal.AccountID, spec.path, build)
	if err != nil {
		s.releaseQuietly(log, meta.requestID)
		status, code, msg := classifyRelayError(err)
		httpx.WriteError(w, r, status, code, msg)
		s.logFailure(meta, trace, status, code, len(trace))
		return nil, nil, nil, trace, false
	}
	return resp, adp, picked, trace, true
}

// failAfterDispatch 处理"上游返回了 2xx，但响应读取/解析失败"：释放预扣、返回 502。
func (s *Service) failAfterDispatch(w http.ResponseWriter, r *http.Request, log *slog.Logger, meta requestMeta, trace []reqlog.AttemptTraceEntry, msg string, err error) {
	s.releaseQuietly(log, meta.requestID)
	log.Error(msg, "error", err)
	httpx.WriteError(w, r, http.StatusBadGateway, "upstream_error", msg)
	s.logFailure(meta, trace, http.StatusBadGateway, "upstream_error", len(trace))
}

// settleAndLog 按 usage 结算并写 request_logs（在响应写出之后调用）。
func (s *Service) settleAndLog(ctx context.Context, log *slog.Logger, meta requestMeta, picked *router.Picked, trace []reqlog.AttemptTraceEntry,
	ttft int64, usage schema.Usage, sellBook, costBook pricing.Book, fxRates map[string]decimal.Decimal) {
	list, charged, promoID := s.settleQuietly(ctx, log, meta, sellBook, usage)
	costAmount := computeCostAmount(costBook, picked.Account.CostMultiplier, usage, fxRates)
	s.logSuccess(meta, picked, trace, http.StatusOK, ttft, usage, sellBook.ID, list, charged, promoID, costAmount)
}

// codecRequest 为非对话端点构造上游请求：按渠道方言选 codec（多供应商实施方案 §2）。
func codecRequest(call *adapter.Call) requestBuilder {
	return func(ctx context.Context, _ adapter.Adapter, target adapter.Target) (*http.Request, error) {
		return adapter.CodecFor(target, call.Endpoint).Build(ctx, target, call)
	}
}

func targetOf(p *router.Picked) adapter.Target {
	return adapter.Target{Channel: p.Channel, Account: p.Account, Key: p.Key}
}

// finishCodec 是非对话端点的成功路径：codec 解码 → 写回（JSON / 原样字节 / 流式字节）→
// 结算。fixUsage 在结算前调整用量（上游没报告时按估算值兜底，usage_source=estimated）。
func (s *Service) finishCodec(w http.ResponseWriter, r *http.Request, log *slog.Logger, adm *admission, meta requestMeta,
	resp *http.Response, picked *router.Picked, trace []reqlog.AttemptTraceEntry, call *adapter.Call,
	fixUsage func(u schema.Usage) schema.Usage) {
	ctx := r.Context()
	ttft := time.Since(meta.start).Milliseconds()
	target := targetOf(picked)
	res, err := adapter.CodecFor(target, call.Endpoint).Decode(ctx, resp, target, call,
		adapter.Env{HTTP: s.HTTP, MaxBody: s.Cfg.MaxUpstreamBody})
	if err != nil {
		if ie, ok := adapter.IsInBand(err); ok {
			s.releaseQuietly(log, meta.requestID)
			status, code := clientFacingError(ie.Class)
			msg := "Upstream provider failed: " + ie.Detail
			if code == "invalid_request" {
				msg = "Upstream rejected the request: " + ie.Detail
			}
			httpx.WriteError(w, r, status, code, msg)
			s.logFailure(meta, trace, status, code, len(trace))
			return
		}
		s.failAfterDispatch(w, r, log, meta, trace, "Failed to read upstream response.", err)
		return
	}
	usage := res.Usage
	if fixUsage != nil {
		usage = fixUsage(usage)
	}
	switch res.Kind {
	case adapter.ResultJSON:
		if res.RewriteIDModel {
			res.JSON["id"] = meta.requestID
			res.JSON["model"] = meta.vmName
		}
		httpx.WriteJSON(w, http.StatusOK, res.JSON)
	case adapter.ResultRaw:
		w.Header().Set("Content-Type", res.ContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(res.Body)
	case adapter.ResultStream:
		defer res.Stream.Close()
		w.Header().Set("Content-Type", res.ContentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		copyFlushing(w, res.Stream)
	}
	s.settleAndLog(ctx, log, meta, picked, trace, ttft, usage,
		adm.snap.SellPriceBooks[adm.vm.ID], adm.snap.CostPriceBooks[picked.Channel.ID], adm.snap.FXRates)
}

// estimatedIfZero 返回一个 fixUsage：上游没报告用量时按 estimate 计费。
func estimatedIfZero(log *slog.Logger, endpoint string, estimate schema.Usage) func(schema.Usage) schema.Usage {
	return func(u schema.Usage) schema.Usage {
		if !u.IsZero() {
			return u
		}
		log.Warn("upstream did not return usage, using estimate", "endpoint", endpoint)
		estimate.Source = schema.UsageSourceEstimated
		return estimate
	}
}

// upstreamErrorDetail 从上游错误响应体里取出给用户看的原因（OpenAI 的
// error.message、SiliconFlow 的 message 等），压成单行并截断，避免把大段上游
// 输出原样透给用户。取不到时返回空串。
func upstreamErrorDetail(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	var msg string
	switch e := m["error"].(type) {
	case map[string]any:
		msg, _ = e["message"].(string)
	case string:
		msg = e
	}
	if msg == "" {
		msg, _ = m["message"].(string)
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if utf8.RuneCountInString(msg) > 200 {
		msg = string([]rune(msg)[:200]) + "…"
	}
	return msg
}
