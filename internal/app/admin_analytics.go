package app

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 分析查询与助手数据集（《运营后台全局助手执行方案》P2）：
//   - POST /analytics/query：语义指标层（admin.Service.Analytics），只读；
//     subject=wallet / balance 额外要求 account:read（路由表只能表达一个权限点）；
//   - GET /agent/datasets/{id}、/export：助手会话里 query_analytics 存下的数据集，可见性与会话相同
//     （本人，或有 audit:read 的管理员）。

// analyticsQuery 是 POST /analytics/query。
func (h *adminHandlers) analyticsQuery(w http.ResponseWriter, r *http.Request) {
	var in admin.AnalyticsQuery
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if (in.Subject == "wallet" || in.Subject == "balance") && !actor(r).Can(adminauth.PermAccountRead) {
		httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", "subject=wallet / balance requires permission account:read")
		return
	}
	loc := h.defaultTZ
	if loc == nil {
		loc = time.UTC
	}
	if in.TZ != "" {
		l, err := time.LoadLocation(in.TZ)
		if err != nil || in.TZ == "Local" {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'tz', want an IANA time zone such as Asia/Shanghai")
			return
		}
		loc = l
	}
	from, err := parseTimeParamIn(in.From, false, loc)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'from': "+err.Error())
		return
	}
	to, err := parseTimeParamIn(in.To, true, loc)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'to': "+err.Error())
		return
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-7 * 24 * time.Hour)
	}
	ds, err := h.svc.Analytics(r.Context(), in, admin.AnalyticsRange{From: from, To: to, Loc: loc})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ds)
}

// loadDataset 读取数据集并按其会话校验可见性（不暴露他人数据集是否存在）。
func (h *adminHandlers) loadDataset(w http.ResponseWriter, r *http.Request) (*pgstore.Dataset, bool) {
	if !h.requireAgent(w, r) {
		return nil, false
	}
	id, ok := pathID(w, r, "datasetID", "dataset")
	if !ok {
		return nil, false
	}
	notFound := func() {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Dataset not found.")
	}
	d, err := h.agent.Store.GetDataset(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			notFound()
		} else {
			writeAgentError(w, r, h, err)
		}
		return nil, false
	}
	sess, err := h.agent.Store.GetSession(r.Context(), d.SessionID)
	if err != nil {
		notFound()
		return nil, false
	}
	if p := actor(r); sess.AdminUserID != p.AdminID && !p.Can(adminauth.PermAuditRead) {
		notFound()
		return nil, false
	}
	return d, true
}

// getAgentDataset 是 GET /agent/datasets/{datasetID}。
func (h *adminHandlers) getAgentDataset(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDataset(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// exportAgentDataset 是 GET /agent/datasets/{datasetID}/export：UTF-8（带 BOM，Excel 直接打开不乱码）CSV，
// 表头用列的中文名，比率格式化为百分数。
func (h *adminHandlers) exportAgentDataset(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDataset(w, r)
	if !ok {
		return
	}
	var cols []admin.AnalyticsColumn
	var rows []map[string]any
	if err := json.Unmarshal(d.Columns, &cols); err != nil {
		writeAdminError(w, r, h.log, fmt.Errorf("decode dataset columns: %w", err))
		return
	}
	if err := json.Unmarshal(d.Rows, &rows); err != nil {
		writeAdminError(w, r, h.log, fmt.Errorf("decode dataset rows: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dataset-%d.csv"`, d.ID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("\xEF\xBB\xBF"))
	cw := csv.NewWriter(w)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Label
	}
	_ = cw.Write(header)
	for _, row := range rows {
		rec := make([]string, len(cols))
		for i, c := range cols {
			rec[i] = csvCell(row[c.Key], c.Type)
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}

func csvCell(v any, typ string) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if typ == "percent" || typ == "pp" {
			return strconv.FormatFloat(t*100, 'f', 2, 64) + "%"
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}
