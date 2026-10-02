package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxUsageIntervalDays = 90

var (
	ErrInvalidDateRange = errors.New("console: invalid date range")
	ErrInvalidGroupBy   = errors.New("console: group_by must be 'day' or 'model'")
)

// UsageIntervalRow 是 GET /console/usage 按 group_by 聚合出的一行。Group 的
// 取值取决于 groupBy：day 时是 "YYYY-MM-DD"，model 时是虚拟模型名。
type UsageIntervalRow struct {
	Group              string `json:"group"`
	Requests           int64  `json:"requests"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	Images             int64  `json:"images"`
	InputChars         int64  `json:"input_chars"`
	AudioMillis        int64  `json:"audio_ms"`
	ChargedAmountMicro int64  `json:"charged_amount_micro"`
}

// UsageInterval 按天或按模型聚合某账户在 [from, to] 闭区间内的用量（技术方案
// 迭代5）。区间最长 90 天；from/to 为零值时默认取本月至今（UTC）。只统计
// status='success' 的请求，和 GET /v1/usage、internal/reconcile 用的同一个
// "成功请求"口径。这条查询能触发 request_logs 按天分区裁剪（created_at 上
// 有范围条件），不需要额外索引。
func (s *Service) UsageInterval(ctx context.Context, accountID int64, from, to time.Time, groupBy string) ([]UsageIntervalRow, error) {
	if groupBy == "" {
		groupBy = "day"
	}
	if groupBy != "day" && groupBy != "model" {
		return nil, ErrInvalidGroupBy
	}

	now := time.Now().UTC()
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if from.After(to) || to.Sub(from) > maxUsageIntervalDays*24*time.Hour {
		return nil, ErrInvalidDateRange
	}
	// 上界按天对齐到"次日 00:00"：调用方传 to=某一天 时，那一天要整天都被
	// 计入（闭区间语义），而不是被 created_at < to 的午夜时刻切掉。
	toExclusive := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location()).AddDate(0, 0, 1)

	groupExpr := "to_char(date_trunc('day', created_at), 'YYYY-MM-DD')"
	if groupBy == "model" {
		groupExpr = "virtual_model"
	}

	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s AS grp, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(image_count),0), COALESCE(SUM(input_chars),0), COALESCE(SUM(audio_ms),0), COALESCE(SUM(charged_amount),0)
		 FROM request_logs
		 WHERE account_id = $1 AND status = 'success' AND created_at >= $2 AND created_at < $3
		 GROUP BY grp ORDER BY grp`, groupExpr),
		accountID, from, toExclusive,
	)
	if err != nil {
		return nil, fmt.Errorf("console: query usage interval: %w", err)
	}
	defer rows.Close()

	out := make([]UsageIntervalRow, 0)
	for rows.Next() {
		var row UsageIntervalRow
		if err := rows.Scan(&row.Group, &row.Requests, &row.InputTokens, &row.OutputTokens,
			&row.Images, &row.InputChars, &row.AudioMillis, &row.ChargedAmountMicro); err != nil {
			return nil, fmt.Errorf("console: scan usage interval row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const (
	maxLogsLimit      = 100
	maxLogsWindowDays = 30
)

var ErrInvalidCursor = errors.New("console: invalid pagination cursor")

// LogEntry 是 GET /console/logs 单条记录——只有元数据（模型、状态、tokens、
// 费用、延迟、usage_source），绝不含请求/响应正文（技术方案迭代5：这些日志
// 直接暴露给终端用户在控制台自助查看）。
type LogEntry struct {
	RequestID          string    `json:"request_id"`
	CreatedAt          time.Time `json:"created_at"`
	APIKeyID           int64     `json:"api_key_id"`
	VirtualModel       string    `json:"virtual_model"`
	Endpoint           string    `json:"endpoint"`
	Status             string    `json:"status"`
	HTTPStatus         int       `json:"http_status"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	Images             int64     `json:"images"`
	InputChars         int64     `json:"input_chars"`
	AudioMillis        int64     `json:"audio_ms"`
	ChargedAmountMicro int64     `json:"charged_amount_micro"`
	LatencyMillis      int64     `json:"latency_ms"`
	UsageSource        string    `json:"usage_source"`
}

// ListLogs 按 (created_at, request_id) 做 keyset 分页，倒序返回该账户最近的
// 调用日志（技术方案迭代5）。时间窗最长 30 天；apiKeyID<=0 表示不按 Key 过滤。
// 返回值的第二个结果是下一页的 cursor（传给下一次调用的 before 参数），
// 结果为空时是空字符串。account_id 恒等于调用方自己的账户，所以传一个属于
// 别的账户的 api_key_id 只会查出空结果，不会跨账户泄露数据。
func (s *Service) ListLogs(ctx context.Context, accountID int64, before string, limit int, apiKeyID int64) ([]LogEntry, string, error) {
	if limit <= 0 || limit > maxLogsLimit {
		limit = maxLogsLimit
	}
	windowStart := time.Now().Add(-maxLogsWindowDays * 24 * time.Hour)

	args := []any{accountID, windowStart}
	where := "account_id = $1 AND created_at >= $2"
	if apiKeyID > 0 {
		args = append(args, apiKeyID)
		where += fmt.Sprintf(" AND api_key_id = $%d", len(args))
	}
	if before != "" {
		beforeAt, beforeID, err := decodeLogsCursor(before)
		if err != nil {
			return nil, "", err
		}
		args = append(args, beforeAt, beforeID)
		where += fmt.Sprintf(" AND (created_at, request_id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT request_id, created_at, api_key_id, virtual_model, endpoint, status, COALESCE(http_status,0),
		        COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		        COALESCE(image_count,0), COALESCE(input_chars,0), COALESCE(audio_ms,0), COALESCE(charged_amount,0),
		        COALESCE(latency_ms,0), usage_source
		 FROM request_logs
		 WHERE %s
		 ORDER BY created_at DESC, request_id DESC
		 LIMIT $%d`, where, len(args)),
		args...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("console: query logs: %w", err)
	}
	defer rows.Close()

	out := make([]LogEntry, 0, limit)
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.RequestID, &e.CreatedAt, &e.APIKeyID, &e.VirtualModel, &e.Endpoint, &e.Status, &e.HTTPStatus,
			&e.InputTokens, &e.OutputTokens, &e.Images, &e.InputChars, &e.AudioMillis, &e.ChargedAmountMicro, &e.LatencyMillis, &e.UsageSource); err != nil {
			return nil, "", fmt.Errorf("console: scan log entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeLogsCursor(last.CreatedAt, last.RequestID)
	}
	return out, nextCursor, nil
}

func encodeLogsCursor(createdAt time.Time, requestID string) string {
	return createdAt.UTC().Format(time.RFC3339Nano) + "|" + requestID
}

func decodeLogsCursor(cursor string) (time.Time, string, error) {
	at, id, found := strings.Cut(cursor, "|")
	if !found || id == "" {
		return time.Time{}, "", ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	return t, id, nil
}
