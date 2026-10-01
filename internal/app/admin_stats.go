package app

import (
	"net/http"
	"sync"
	"time"
	_ "time/tzdata" // 服务器（尤其是 Windows/精简容器）可能没有系统时区库

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 用量统计与全局调用日志（运营后台接口方案 §3、§6）。

// statsCache 按完整 URL 缓存统计结果 60 秒（接口方案 §3.4 阶段 A）：多个运营同时
// 打开工作台时，不必每人每次都扫一遍 request_logs。条目数有上限（超出时淘汰最旧的），
// 同一个 URL 的并发未命中只计算一次（singleflight），其余请求等结果。
var statsCache = struct {
	sync.Mutex
	entries map[string]statsCacheEntry
	group   singleflight.Group
}{entries: map[string]statsCacheEntry{}}

const statsCacheMaxEntries = 512

type statsCacheEntry struct {
	at  time.Time
	val any
}

const statsCacheTTL = 60 * time.Second

func cachedStats(r *http.Request, compute func() (any, error)) (any, error) {
	key := r.URL.Path + "?" + r.URL.RawQuery
	statsCache.Lock()
	if e, ok := statsCache.entries[key]; ok && time.Since(e.at) < statsCacheTTL {
		statsCache.Unlock()
		return e.val, nil
	}
	statsCache.Unlock()

	val, err, _ := statsCache.group.Do(key, compute)
	if err != nil {
		return nil, err
	}
	statsCache.Lock()
	defer statsCache.Unlock()
	// 顺手清理过期条目；仍超过上限时淘汰最旧的，避免不同参数组合无限累积。
	var oldestKey string
	var oldestAt time.Time
	for k, e := range statsCache.entries {
		if time.Since(e.at) >= statsCacheTTL {
			delete(statsCache.entries, k)
			continue
		}
		if oldestKey == "" || e.at.Before(oldestAt) {
			oldestKey, oldestAt = k, e.at
		}
	}
	if len(statsCache.entries) >= statsCacheMaxEntries && oldestKey != "" {
		delete(statsCache.entries, oldestKey)
	}
	statsCache.entries[key] = statsCacheEntry{at: time.Now(), val: val}
	return val, nil
}

// statsTZ 读 ?tz=（IANA 时区名，如 Asia/Shanghai），缺省为 UTC。
func (h *adminHandlers) statsTZ(w http.ResponseWriter, r *http.Request) (*time.Location, bool) {
	name := r.URL.Query().Get("tz")
	if name == "" {
		if h.defaultTZ != nil {
			return h.defaultTZ, true
		}
		return time.UTC, true
	}
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'tz', want an IANA time zone such as Asia/Shanghai")
		return nil, false
	}
	return loc, true
}

// statsRange 解析 from/to（纯日期按 ?tz= 时区解释）；缺省时取 [to-defaultSpan, now)。
func (h *adminHandlers) statsRange(w http.ResponseWriter, r *http.Request, defaultSpan time.Duration) (time.Time, time.Time, bool) {
	q := r.URL.Query()
	loc, ok := h.statsTZ(w, r)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	from, err := parseTimeParamIn(q.Get("from"), false, loc)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'from': "+err.Error())
		return time.Time{}, time.Time{}, false
	}
	to, err := parseTimeParamIn(q.Get("to"), true, loc)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'to': "+err.Error())
		return time.Time{}, time.Time{}, false
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-defaultSpan)
	}
	return from, to, true
}

func (h *adminHandlers) statsOverview(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.statsRange(w, r, 7*24*time.Hour)
	if !ok {
		return
	}
	out, err := cachedStats(r, func() (any, error) { return h.svc.Overview(r.Context(), from, to) })
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *adminHandlers) usageFromQuery(w http.ResponseWriter, r *http.Request, accountID int64) (admin.UsageInput, bool) {
	from, to, ok := h.statsRange(w, r, 7*24*time.Hour)
	if !ok {
		return admin.UsageInput{}, false
	}
	loc, _ := h.statsTZ(w, r) // statsRange 已经校验过
	q := &queryParser{r: r}
	in := admin.UsageInput{
		TZ: loc,
		StatsFilter: admin.StatsFilter{
			From: from, To: to, VirtualModel: q.str("virtual_model"), ChannelID: q.int64("channel_id"),
			ProviderID: q.int64("provider_id"), AccountID: q.int64("account_id"), APIKeyID: q.int64("api_key_id"),
		},
		Interval: q.str("interval"), GroupBy: q.str("group_by"), Top: q.int("top"), OrderBy: q.str("order_by"),
	}
	if accountID != 0 {
		in.AccountID = accountID
	}
	return in, q.ok(w)
}

func (h *adminHandlers) statsUsage(w http.ResponseWriter, r *http.Request) {
	in, ok := h.usageFromQuery(w, r, 0)
	if !ok {
		return
	}
	out, err := cachedStats(r, func() (any, error) { return h.svc.Usage(r.Context(), in) })
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	httpx.WriteJSON(w, http.StatusOK, out)
}

// accountUsage 是 /stats/usage 固定 account_id 的版本（接口方案 §4.5）。
func (h *adminHandlers) accountUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	in, ok := h.usageFromQuery(w, r, id)
	if !ok {
		return
	}
	out, err := cachedStats(r, func() (any, error) { return h.svc.Usage(r.Context(), in) })
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *adminHandlers) listRequestLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := parseTimeParam(q.Get("from"), false)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'from': "+err.Error())
		return
	}
	to, err := parseTimeParam(q.Get("to"), true)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'to': "+err.Error())
		return
	}
	p := &queryParser{r: r}
	in := admin.ListRequestLogsInput{
		StatsFilter: admin.StatsFilter{
			From: from, To: to, VirtualModel: p.str("virtual_model"), ChannelID: p.int64("channel_id"),
			ProviderID: p.int64("provider_id"), AccountID: p.int64("account_id"), APIKeyID: p.int64("api_key_id"),
		},
		ProviderKeyID: p.int64("provider_key_id"), Status: p.str("status"), ErrorCode: p.str("error_code"),
		HTTPStatus: p.int("http_status"), MinLatencyMs: p.int("min_latency_ms"), UsageSource: p.str("usage_source"),
		RequestID: p.str("request_id"), Before: p.str("before"), Limit: p.int("limit"),
	}
	if !p.ok(w) {
		return
	}
	logs, next, err := h.svc.ListRequestLogs(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": logs, "next_cursor": next})
}

func (h *adminHandlers) getRequestLog(w http.ResponseWriter, r *http.Request) {
	var createdAt time.Time
	if v := r.URL.Query().Get("created_at"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'created_at', want RFC3339")
			return
		}
		createdAt = t
	}
	d, err := h.svc.GetRequestLog(r.Context(), chi.URLParam(r, "requestID"), createdAt)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}
