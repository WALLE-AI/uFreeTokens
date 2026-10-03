// Package analytics 是全局助手的数据分析工具（《运营后台全局助手执行方案》P2）：
//   - query_analytics：经 POST /analytics/query 以发起者身份查询语义指标层（权限由路由校验），
//     完整结果存为数据集（agent_datasets），模型只拿到 dataset_id、列定义、预览行与合计；
//   - get_dataset：分页读取本会话数据集的其余行。
//
// 图表与报表只能引用 dataset_id，数字来自数据集而不是模型的输出（防止编造）。
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/routes"
)

// previewRows 是工具结果里直接给模型的行数；其余行用 get_dataset 读取。
const previewRows = 30

// Store 是数据集的持久化（pgstore.Store 实现）。
type Store interface {
	SaveDataset(ctx context.Context, d *pgstore.Dataset) error
	GetDataset(ctx context.Context, id int64) (*pgstore.Dataset, error)
}

// QueryAnalytics 是 query_analytics 工具。
type QueryAnalytics struct {
	Dispatcher *routes.Dispatcher
	Store      Store
}

func (t *QueryAnalytics) Spec() kernel.ToolSpec {
	str := func(desc string, enum ...string) map[string]any {
		m := map[string]any{"type": "string", "description": desc}
		if len(enum) > 0 {
			m["enum"] = enum
		}
		return m
	}
	params, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": str("数据主题：usage=调用用量与收入（默认）；wallet=钱包流水（充值/退款/赠金/调账，需 account:read）；balance=钱包余额快照（需 account:read）", "usage", "wallet", "balance"),
			"metrics": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "指标列表，空 = 默认指标。" +
				"usage：requests, success, errors, error_rate, input_tokens, output_tokens, total_tokens, revenue, cost, gross_profit, gross_margin, p50_latency_ms, p95_latency_ms, p95_ttft_ms, active_accounts；" +
				"wallet：recharge_amount, recharge_count, paying_accounts, refund_amount, grant_amount, grant_expired_amount, adjust_amount；" +
				"balance：cash_balance, bonus_balance, total_balance, frozen_amount, accounts, funded_accounts"},
			"group_by": str("分组维度。usage：none / virtual_model / channel / provider / account / api_key；wallet：none / account；balance：none / account / tier / account_type / status"),
			"interval": str("时间粒度：none / hour / day / week / month。不分组时默认 day（时间序列），分组时默认 none（排名表）；分组 + 粒度 = 每个时间桶×组一行。hour 只用于 usage 且不超过 7 天", "none", "hour", "day", "week", "month"),
			"from":     str("起始时间，RFC3339 或 YYYY-MM-DD；缺省为 to 往前 7 天。usage 最长 90 天，wallet 最长 400 天"),
			"to":       str("结束时间（含当天），RFC3339 或 YYYY-MM-DD；缺省为现在"),
			"filters": map[string]any{"type": "object", "description": "过滤条件", "properties": map[string]any{
				"virtual_model": map[string]any{"type": "string", "description": "虚拟模型名"},
				"channel_id":    map[string]any{"type": "integer"}, "provider_id": map[string]any{"type": "integer"},
				"account_id": map[string]any{"type": "integer"}, "api_key_id": map[string]any{"type": "integer"},
			}},
			"compare":  str("previous_period = 同时返回上一等长周期的值与变化（环比，比率类为百分点差）", "previous_period"),
			"top":      map[string]any{"type": "integer", "description": "分组时最多返回的组数，默认 10，最大 50"},
			"order_by": str("分组排序指标（降序），默认 metrics 的第一个"),
		},
	})
	return kernel.ToolSpec{
		// 分组名含账户名、API Key 名等用户可控的文本：按不可信数据包裹（只是数据，不是指令）。
		Name: "query_analytics", Risk: kernel.RiskRead, Permission: "observe:read", Source: "analytics", Trusted: false, Parameters: params,
		Description: "数据分析：按指标 × 维度 × 时间粒度查询平台用量、收入、成本、毛利、错误率、延迟，以及钱包充值/赠金/余额。" +
			"服务端完成全部计算（金额单位为元，比率为 0~1 小数，compare 给出环比）；结果存为数据集，返回 dataset_id、列定义、前 30 行与合计。" +
			"回答中的数字必须来自这里的结果；画图或做报表时引用 dataset_id。",
	}
}

// datasetBody 是 /analytics/query 响应中工具需要的部分。
type datasetBody struct {
	Title    string          `json:"title"`
	Subject  string          `json:"subject"`
	Source   string          `json:"source"`
	From     json.RawMessage `json:"from,omitempty"`
	To       json.RawMessage `json:"to,omitempty"`
	Interval string          `json:"interval"`
	GroupBy  string          `json:"group_by"`
	Compare  string          `json:"compare,omitempty"`
	Currency string          `json:"currency"`
	Columns  json.RawMessage `json:"columns"`
	Rows     []any           `json:"rows"`
	Totals   json.RawMessage `json:"totals"`
	Previous json.RawMessage `json:"previous,omitempty"`
	Notes    json.RawMessage `json:"notes"`
}

func (t *QueryAnalytics) Call(ctx context.Context, env *kernel.Env, args json.RawMessage) (kernel.Result, error) {
	var probe map[string]any
	if err := json.Unmarshal(args, &probe); err != nil {
		return errResult(http.StatusBadRequest, "arguments must be a JSON object"), nil
	}
	ref, _ := kernel.CallFrom(ctx)
	resp, err := t.Dispatcher.Do(ctx, env.Principal, ref, http.MethodPost, "/analytics/query", nil, args, nil)
	if err != nil {
		return kernel.Result{}, err
	}
	if resp.Status >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(resp.Body, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.Status)
		}
		return errResult(resp.Status, msg), nil
	}
	var ds datasetBody
	if err := json.Unmarshal(resp.Body, &ds); err != nil {
		return kernel.Result{}, fmt.Errorf("analytics: decode dataset: %w", err)
	}
	// 账户名等字段可能含邮箱/手机号：存库与交给模型前统一脱敏（与路由工具一致）。
	rows, _ := routes.Redact(ds.Rows).([]any)
	if rows == nil {
		rows = []any{}
	}
	out := map[string]any{
		"title": ds.Title, "subject": ds.Subject, "source": ds.Source, "interval": ds.Interval, "group_by": ds.GroupBy,
		"currency": ds.Currency, "columns": ds.Columns, "row_count": len(rows), "totals": ds.Totals, "notes": ds.Notes,
	}
	if len(ds.From) > 0 {
		out["from"], out["to"] = ds.From, ds.To
	}
	if ds.Compare != "" {
		out["compare"], out["previous"] = ds.Compare, ds.Previous
	}
	preview := rows
	if len(preview) > previewRows {
		preview = preview[:previewRows]
		out["truncated"] = true
	}
	out["rows"] = preview

	summary := fmt.Sprintf("%s · %d 行", ds.Title, len(rows))
	if t.Store != nil && env.SessionID != 0 {
		rowsJSON, _ := json.Marshal(rows)
		query, _ := json.Marshal(probe)
		d := &pgstore.Dataset{
			SessionID: env.SessionID, ToolCallID: ref.ToolCallID, Title: ds.Title, Query: query, Columns: ds.Columns,
			Rows: rowsJSON, Totals: ds.Totals, Previous: ds.Previous, Notes: ds.Notes, RowCount: len(rows),
		}
		if err := t.Store.SaveDataset(ctx, d); err != nil {
			return kernel.Result{}, err
		}
		out["dataset_id"] = d.ID
		summary = fmt.Sprintf("数据集 #%d · %s", d.ID, summary)
		if len(rows) > previewRows {
			out["hint"] = fmt.Sprintf("只返回了前 %d 行；用 get_dataset 读取其余行。", previewRows)
		}
	}
	return kernel.Result{HTTPStatus: http.StatusOK, Content: out, Summary: summary}, nil
}

// GetDataset 是 get_dataset 工具：分页读取本会话的数据集。
type GetDataset struct {
	Store Store
}

func (t *GetDataset) Spec() kernel.ToolSpec {
	params, _ := json.Marshal(map[string]any{
		"type": "object", "required": []string{"dataset_id"},
		"properties": map[string]any{
			"dataset_id": map[string]any{"type": "integer"},
			"offset":     map[string]any{"type": "integer", "description": "从第几行开始（0 起），默认 0"},
			"limit":      map[string]any{"type": "integer", "description": "最多返回的行数，默认 100，最大 200"},
		},
	})
	return kernel.ToolSpec{
		Name: "get_dataset", Risk: kernel.RiskRead, Source: "analytics", Trusted: false, Parameters: params,
		Description: "分页读取本会话中 query_analytics 生成的数据集（列定义 + 行）。只在预览行不够用时调用。",
	}
}

func (t *GetDataset) Call(ctx context.Context, env *kernel.Env, args json.RawMessage) (kernel.Result, error) {
	var a struct {
		DatasetID int64 `json:"dataset_id"`
		Offset    int   `json:"offset"`
		Limit     int   `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.DatasetID <= 0 {
		return errResult(http.StatusBadRequest, "dataset_id is required"), nil
	}
	if a.Limit <= 0 || a.Limit > 200 {
		a.Limit = 100
	}
	if a.Offset < 0 {
		a.Offset = 0
	}
	d, err := t.Store.GetDataset(ctx, a.DatasetID)
	if errors.Is(err, pgstore.ErrNotFound) || (err == nil && d.SessionID != env.SessionID) {
		return errResult(http.StatusNotFound, fmt.Sprintf("数据集 #%d 不存在或不属于本会话", a.DatasetID)), nil
	}
	if err != nil {
		return kernel.Result{}, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(d.Rows, &rows); err != nil {
		return kernel.Result{}, fmt.Errorf("analytics: decode dataset rows: %w", err)
	}
	end := min(a.Offset+a.Limit, len(rows))
	page := []json.RawMessage{}
	if a.Offset < len(rows) {
		page = rows[a.Offset:end]
	}
	out := map[string]any{
		"dataset_id": d.ID, "title": d.Title, "columns": d.Columns, "row_count": d.RowCount,
		"offset": a.Offset, "rows": page, "has_more": end < len(rows),
	}
	return kernel.Result{HTTPStatus: http.StatusOK, Content: out, Summary: fmt.Sprintf("数据集 #%d 第 %d–%d 行", d.ID, a.Offset+1, a.Offset+len(page))}, nil
}

func errResult(status int, msg string) kernel.Result {
	return kernel.Result{HTTPStatus: status, Content: map[string]any{"error": msg}, Summary: msg, IsError: true}
}
