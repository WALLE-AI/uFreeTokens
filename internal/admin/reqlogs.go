package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// 全局调用日志（运营后台接口方案 §6）。与 internal/console.ListLogs 同样是
// (created_at, request_id) keyset 分页，区别是不固定 account_id、过滤维度更多、
// 返回成本等运营字段。时间窗必须有上限，才能依靠 request_logs 的按天分区裁剪。

var ErrRequestLogNotFound = errors.New("admin: request log not found")

const (
	maxLogsWindowNoAccount   = 7 * 24 * time.Hour
	maxLogsWindowWithAccount = 30 * 24 * time.Hour
)

type RequestLogItem struct {
	RequestID       string    `json:"request_id"`
	CreatedAt       time.Time `json:"created_at"`
	AccountID       int64     `json:"account_id"`
	APIKeyID        int64     `json:"api_key_id"`
	VirtualModel    string    `json:"virtual_model"`
	ChannelID       *int64    `json:"channel_id"`
	ProviderKeyID   *int64    `json:"provider_key_id"`
	Endpoint        string    `json:"endpoint"`
	IsStream        bool      `json:"is_stream"`
	Status          string    `json:"status"`
	HTTPStatus      *int      `json:"http_status"`
	ErrorCode       *string   `json:"error_code"`
	Attempts        int       `json:"attempts"`
	TTFTMs          *int      `json:"ttft_ms"`
	LatencyMs       *int      `json:"latency_ms"`
	InputTokens     *int      `json:"input_tokens"`
	OutputTokens    *int      `json:"output_tokens"`
	UsageSource     string    `json:"usage_source"`
	ChargedMicro    *int64    `json:"charged_amount_micro"`
	ListAmountMicro *int64    `json:"list_amount_micro"`
	CostMicro       *int64    `json:"cost_micro"`
}

type ListRequestLogsInput struct {
	StatsFilter
	ProviderKeyID int64
	Status        string
	ErrorCode     string
	HTTPStatus    int
	MinLatencyMs  int
	UsageSource   string
	RequestID     string
	Before        string
	Limit         int // 默认 50，最大 100
}

const requestLogListCols = `rl.request_id, rl.created_at, rl.account_id, rl.api_key_id, rl.virtual_model, rl.channel_id, rl.provider_key_id,
	rl.endpoint, rl.is_stream, rl.status, rl.http_status, rl.error_code, rl.attempts, rl.ttft_ms, rl.latency_ms,
	rl.input_tokens, rl.output_tokens, rl.usage_source, rl.charged_amount, rl.list_amount, rl.cost_amount`

func scanRequestLogItem(row pgx.Row, extra ...any) (RequestLogItem, error) {
	var l RequestLogItem
	var attempts int16
	dest := []any{&l.RequestID, &l.CreatedAt, &l.AccountID, &l.APIKeyID, &l.VirtualModel, &l.ChannelID, &l.ProviderKeyID,
		&l.Endpoint, &l.IsStream, &l.Status, &l.HTTPStatus, &l.ErrorCode, &attempts, &l.TTFTMs, &l.LatencyMs,
		&l.InputTokens, &l.OutputTokens, &l.UsageSource, &l.ChargedMicro, &l.ListAmountMicro, &l.CostMicro}
	err := row.Scan(append(dest, extra...)...)
	l.Attempts = int(attempts)
	return l, err
}

// ListRequestLogs 默认最近 24 小时；不带 account_id 时时间窗最长 7 天，带时 30 天。
func (s *Service) ListRequestLogs(ctx context.Context, in ListRequestLogsInput) ([]RequestLogItem, string, error) {
	limit := in.Limit
	switch {
	case limit <= 0:
		limit = 50
	case limit > 100:
		limit = 100
	}
	now := time.Now()
	if in.To.IsZero() {
		in.To = now
	}
	if in.From.IsZero() {
		in.From = in.To.Add(-24 * time.Hour)
	}
	maxWindow := maxLogsWindowNoAccount
	if in.AccountID != 0 {
		maxWindow = maxLogsWindowWithAccount
	}
	if !in.To.After(in.From) || in.To.Sub(in.From) > maxWindow {
		return nil, "", fmt.Errorf("%w: log window must be non-empty and at most %d days (%d with account_id)",
			ErrInvalidStatsRange, int(maxLogsWindowNoAccount.Hours()/24), int(maxLogsWindowWithAccount.Hours()/24))
	}

	where, args := in.StatsFilter.where()
	add := func(cond string, v any) {
		args = append(args, v)
		where += " AND " + fmt.Sprintf(cond, len(args))
	}
	if in.RequestID != "" {
		// 精确定位时忽略其它过滤条件，只保留时间窗
		where, args = "WHERE rl.created_at >= $1 AND rl.created_at < $2", []any{in.From, in.To}
		add("rl.request_id = $%d", in.RequestID)
	} else {
		if in.ProviderKeyID != 0 {
			add("rl.provider_key_id = $%d", in.ProviderKeyID)
		}
		if in.Status != "" {
			add("rl.status = $%d", in.Status)
		}
		if in.ErrorCode != "" {
			add("rl.error_code = $%d", in.ErrorCode)
		}
		if in.HTTPStatus != 0 {
			add("rl.http_status = $%d", in.HTTPStatus)
		}
		if in.MinLatencyMs > 0 {
			add("rl.latency_ms >= $%d", in.MinLatencyMs)
		}
		if in.UsageSource != "" {
			add("rl.usage_source = $%d", in.UsageSource)
		}
	}
	if in.Before != "" {
		at, id, found := strings.Cut(in.Before, "|")
		t, err := time.Parse(time.RFC3339Nano, at)
		if !found || id == "" || err != nil {
			return nil, "", ErrInvalidCursor
		}
		args = append(args, t, id)
		where += fmt.Sprintf(" AND (rl.created_at, rl.request_id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit)
	rows, err := s.db(ctx).Query(ctx, fmt.Sprintf(`SELECT %s %s %s ORDER BY rl.created_at DESC, rl.request_id DESC LIMIT $%d`,
		requestLogListCols, statsFrom, where, len(args)), args...)
	if err != nil {
		return nil, "", fmt.Errorf("admin: query request_logs: %w", err)
	}
	defer rows.Close()
	out := make([]RequestLogItem, 0, limit)
	for rows.Next() {
		l, err := scanRequestLogItem(rows)
		if err != nil {
			return nil, "", fmt.Errorf("admin: scan request_log: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.RequestID
	}
	return out, next, nil
}

type RequestLogDetail struct {
	RequestLogItem
	CacheReadTokens  *int             `json:"cache_read_tokens"`
	CacheWriteTokens *int             `json:"cache_write_tokens"`
	ReasoningTokens  *int             `json:"reasoning_tokens"`
	AttemptTrace     json.RawMessage  `json:"attempt_trace"`
	SellPriceBookID  *int64           `json:"sell_price_book_id"`
	CostPriceBookID  *int64           `json:"cost_price_book_id"`
	PromotionIDs     []int64          `json:"promotion_ids"`
	UpstreamCost     *decimal.Decimal `json:"upstream_cost"`
	FXRate           *decimal.Decimal `json:"fx_rate"`
	ClientIP         *string          `json:"client_ip"`
	UserAgent        *string          `json:"user_agent"`
	ExperimentKey    *string          `json:"experiment_key"`
	VariantLabel     *string          `json:"variant_label"`
	AccountName      *string          `json:"account_name"`
	APIKeyName       *string          `json:"api_key_name"`
	ChannelLabel     *string          `json:"channel_label"`
	ProviderCode     *string          `json:"provider_code"`
}

// GetRequestLog 按 request_id 取完整日志。createdAt 非零时直接命中分区；否则在
// 最近 30 天内查找。
func (s *Service) GetRequestLog(ctx context.Context, requestID string, createdAt time.Time) (*RequestLogDetail, error) {
	cond := "rl.request_id = $1 AND rl.created_at >= now() - interval '30 days'"
	args := []any{requestID}
	if !createdAt.IsZero() {
		cond = "rl.request_id = $1 AND rl.created_at = $2"
		args = append(args, createdAt)
	}
	var d RequestLogDetail
	var ip *string
	item, err := scanRequestLogItem(s.db(ctx).QueryRow(ctx, `SELECT `+requestLogListCols+`,
		   rl.cache_read_tokens, rl.cache_write_tokens, rl.reasoning_tokens, rl.attempt_trace, rl.sell_price_book_id, rl.cost_price_book_id,
		   rl.promotion_ids, rl.upstream_cost, rl.fx_rate, host(rl.client_ip), rl.user_agent, rl.experiment_key, rl.variant_label,
		   a.name, k.name, pa.name || ' / ' || c.upstream_model, p.code
		 `+statsFrom+`
		 LEFT JOIN providers p ON p.id = pa.provider_id
		 LEFT JOIN accounts a ON a.id = rl.account_id
		 LEFT JOIN api_keys k ON k.id = rl.api_key_id
		 WHERE `+cond+` ORDER BY rl.created_at DESC LIMIT 1`, args...),
		&d.CacheReadTokens, &d.CacheWriteTokens, &d.ReasoningTokens, &d.AttemptTrace, &d.SellPriceBookID, &d.CostPriceBookID,
		&d.PromotionIDs, &d.UpstreamCost, &d.FXRate, &ip, &d.UserAgent, &d.ExperimentKey, &d.VariantLabel,
		&d.AccountName, &d.APIKeyName, &d.ChannelLabel, &d.ProviderCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestLogNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query request_log: %w", err)
	}
	d.RequestLogItem, d.ClientIP = item, ip
	return &d, nil
}
