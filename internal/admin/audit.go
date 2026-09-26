package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AuditLogInput 对应一条 admin_audit_logs（技术方案 Phase 4 审计导出的数据来源）。
// ActorID 目前只能是调用方自己声明的值（比如 HTTP 层从一个请求头读出来）——
// cmd/admin 还没有鉴权（见包文档），没有真正的"当前登录管理员"概念，这里不
// 假装有；ActorID=0 就是"未知/系统操作"，等真正的管理员登录实现了，
// 调用方自然会传真实的 user id 进来，这个字段不需要跟着改。
type AuditLogInput struct {
	ActorID    int64
	Action     string // 例如 "wallet.adjust" / "provider_key.add" / "cost_price.set"
	TargetType string // 例如 "account" / "provider_key" / "channel"
	TargetID   string
	Before     any // 变更前的快照，nil 表示不适用（比如创建类操作没有"之前"）
	After      any
	IP         string // 调用方 IP；空字符串存 NULL
}

type AuditLogEntry struct {
	ID         int64
	ActorID    int64
	Action     string
	TargetType string
	TargetID   string
	Before     json.RawMessage
	After      json.RawMessage
	IP         string
	CreatedAt  time.Time
}

// RecordAudit 写一条审计记录。这里没有把它塞进被审计操作自己的数据库事务里
// （比如 SetCostPrice 内部的事务）——是在 HTTP handler 层、操作成功之后单独
// 调用的，简单但不是跨库原子的：操作成功了但审计写入失败的极小概率窗口是
// 存在的，接受这个取舍（换来不用把审计关注点侵入每一个业务方法的内部事务）。
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

	var id int64
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO admin_audit_logs (actor_id, action, target_type, target_id, before, after, ip)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::inet) RETURNING id`,
		in.ActorID, in.Action, in.TargetType, in.TargetID, before, after, in.IP,
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

// ListAuditLogsInput 的两个过滤字段都可以留空：都留空返回全部（按 limit 截断）；
// 只填 TargetType 返回这一类操作的全部；两者都填缩到单个目标的历史。
type ListAuditLogsInput struct {
	TargetType string
	TargetID   string
	Limit      int // <=0 或 >500 时用默认值 100
}

func (s *Service) ListAuditLogs(ctx context.Context, in ListAuditLogsInput) ([]AuditLogEntry, error) {
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, actor_id, action, target_type, target_id, before, after, COALESCE(host(ip), ''), created_at
		 FROM admin_audit_logs
		 WHERE ($1 = '' OR target_type = $1) AND ($2 = '' OR target_id = $2)
		 ORDER BY created_at DESC LIMIT $3`,
		in.TargetType, in.TargetID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: query admin_audit_logs: %w", err)
	}
	defer rows.Close()

	var out []AuditLogEntry
	for rows.Next() {
		var e AuditLogEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.TargetType, &e.TargetID, &e.Before, &e.After, &e.IP, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan admin_audit_log: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
