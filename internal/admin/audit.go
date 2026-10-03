package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AuditLogInput 对应一条 admin_audit_logs（技术方案 Phase 4 审计导出的数据来源）。
// ActorID/ActorName/SessionID 来自鉴权中间件解析出的管理员身份（internal/adminauth），
// 不再读取客户端自填的请求头；ActorID=0 是 system（应急令牌、系统自动操作）。
type AuditLogInput struct {
	ActorID    int64
	ActorName  string // 写入时的管理员名称快照，空字符串存 NULL
	SessionID  int64  // 0 存 NULL
	Action     string // 例如 "wallet.adjust" / "provider_key.add" / "cost_price.set"
	TargetType string // 例如 "account" / "provider_key" / "channel"
	TargetID   string
	Before     any // 变更前的快照，nil 表示不适用（比如创建类操作没有"之前"）
	After      any
	IP         string // 调用方 IP；空字符串存 NULL
	UserAgent  string
	RequestID  string
	// AgentSessionID / AgentToolCallID 非零时表示经由运营智能体执行（审批后以审批人身份调用）。
	AgentSessionID  int64
	AgentToolCallID string
}

type AuditLogEntry struct {
	ID         int64           `json:"id"`
	ActorID    int64           `json:"actor_id"`
	ActorName  string          `json:"actor_name"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	IP         string          `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
	// 经由智能体执行时关联的会话与工具调用（否则为 null）。
	AgentSessionID  *int64  `json:"agent_session_id"`
	AgentToolCallID *string `json:"agent_tool_call_id"`
}

// RecordAudit 写一条审计记录。ctx 里有环境事务（Service.RunInTx）时审计写在
// 同一个事务里，与业务变更同成败；否则单独写入。
func (s *Service) RecordAudit(ctx context.Context, in AuditLogInput) (int64, error) {
	if in.Action == "" || in.TargetType == "" || in.TargetID == "" {
		return 0, errors.New("admin: audit action, target_type and target_id are required")
	}
	before, err := marshalAuditValue(in.Before)
	if err != nil {
		return 0, fmt.Errorf("admin: marshal audit before: %w", err)
	}
	after, err := marshalAuditValue(in.After)
	if err != nil {
		return 0, fmt.Errorf("admin: marshal audit after: %w", err)
	}
	ua := in.UserAgent
	if len(ua) > 256 {
		ua = ua[:256]
	}
	var sessionID *int64
	if in.SessionID != 0 {
		sessionID = &in.SessionID
	}
	var agentSessionID *int64
	if in.AgentSessionID != 0 {
		agentSessionID = &in.AgentSessionID
	}

	var id int64
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO admin_audit_logs (actor_id, actor_name, action, target_type, target_id, before, after, ip, session_id, user_agent, request_id,
		                               agent_session_id, agent_tool_call_id)
		 VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, NULLIF($8, '')::inet, $9, NULLIF($10, ''), NULLIF($11, ''), $12, NULLIF($13, '')) RETURNING id`,
		in.ActorID, in.ActorName, in.Action, in.TargetType, in.TargetID, before, after, in.IP, sessionID, ua, in.RequestID,
		agentSessionID, in.AgentToolCallID,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("admin: insert admin_audit_log: %w", err)
	}
	return id, nil
}

func marshalAuditValue(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// ListAuditLogsInput 的过滤字段都可以留空：都留空返回全部（按 Limit 截断）。
// Action 是前缀匹配（"price_change." 匹配全部审批类操作），其余是精确匹配。
// Before 是上一页返回的游标（见 EncodeAuditCursor），空字符串表示第一页。
type ListAuditLogsInput struct {
	TargetType string
	TargetID   string
	ActorID    int64
	ActorName  string
	Action     string
	From, To   time.Time // 零值表示不限
	Before     string
	Limit      int // <=0 时用默认值 100，>500 时截到 500
}

var ErrInvalidAuditCursor = errors.New("admin: invalid audit log cursor")

// ListAuditLogs 按 (created_at, id) 倒序做 keyset 分页。第二个返回值是下一页
// 游标；返回条数不足 Limit 时为空字符串（没有下一页）。
func (s *Service) ListAuditLogs(ctx context.Context, in ListAuditLogsInput) ([]AuditLogEntry, string, error) {
	limit := in.Limit
	switch {
	case limit <= 0:
		limit = 100
	case limit > 500:
		limit = 500
	}
	var beforeAt time.Time
	var beforeID int64
	if in.Before != "" {
		var err error
		if beforeAt, beforeID, err = decodeAuditCursor(in.Before); err != nil {
			return nil, "", err
		}
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT id, actor_id, COALESCE(actor_name, ''), action, target_type, target_id, before, after, COALESCE(host(ip), ''), created_at,
		        agent_session_id, agent_tool_call_id
		 FROM admin_audit_logs
		 WHERE ($1 = '' OR target_type = $1) AND ($2 = '' OR target_id = $2)
		   AND ($3 = 0 OR actor_id = $3) AND ($4 = '' OR actor_name = $4)
		   AND ($5 = '' OR starts_with(action, $5))
		   AND ($6::timestamptz IS NULL OR created_at >= $6) AND ($7::timestamptz IS NULL OR created_at < $7)
		   AND ($8::timestamptz IS NULL OR (created_at, id) < ($8, $9))
		 ORDER BY created_at DESC, id DESC LIMIT $10`,
		in.TargetType, in.TargetID, in.ActorID, in.ActorName, in.Action,
		nullTime(in.From), nullTime(in.To), nullTime(beforeAt), beforeID, limit,
	)
	if err != nil {
		return nil, "", fmt.Errorf("admin: query admin_audit_logs: %w", err)
	}
	defer rows.Close()

	out := make([]AuditLogEntry, 0, limit)
	for rows.Next() {
		var e AuditLogEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorName, &e.Action, &e.TargetType, &e.TargetID, &e.Before, &e.After, &e.IP, &e.CreatedAt,
			&e.AgentSessionID, &e.AgentToolCallID); err != nil {
			return nil, "", fmt.Errorf("admin: scan admin_audit_log: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var next string
	if len(out) == limit {
		last := out[len(out)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(last.ID, 10)
	}
	return out, next, nil
}

func decodeAuditCursor(cursor string) (time.Time, int64, error) {
	at, idStr, found := strings.Cut(cursor, "|")
	if !found {
		return time.Time{}, 0, ErrInvalidAuditCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, 0, ErrInvalidAuditCursor
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return time.Time{}, 0, ErrInvalidAuditCursor
	}
	return t, id, nil
}

// nullTime 把零值时间转成 SQL NULL，配合查询里的 "$n::timestamptz IS NULL OR ..."
// 表达"不限"。
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
