package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 事件触发（设计 §15.3）：不引入消息总线，每个作业在代码里注册一个“待处理查询”，tick 时检查是否有
// 新对象；游标存在 agent_jobs.cursor。同一对象只预审一次：已有该剧本提案的对象被排除。

// Item 是一个待处理对象（会作为 context_ref 带入会话）。
type Item struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// Cursor 是作业游标：按自增 ID 的表用 LastID，按时间的用 Since，按状态变化的用 Seen。
type Cursor struct {
	LastID int64            `json:"last_id,omitempty"`
	Since  *time.Time       `json:"since,omitempty"`
	Seen   map[string]int64 `json:"seen,omitempty"`
}

// Trigger 返回待处理对象与推进后的游标。
type Trigger func(ctx context.Context, pool *pgxpool.Pool, playbook string, cur Cursor, limit int) ([]Item, Cursor, error)

// Triggers 是代码内注册的待处理查询（agent_jobs.trigger_query 引用这里的名字）。
var Triggers = map[string]Trigger{
	"pending_price_changes": idTrigger("price_change_request", "调价 #",
		`SELECT id, '' FROM price_change_requests WHERE status IN ('pending','blocked') AND id > $1 %s ORDER BY id LIMIT $2`),
	"pending_listings": idTrigger("pending_listing", "待上架 #",
		`SELECT id, upstream_model FROM pending_model_listings WHERE status = 'pending' AND id > $1 %s ORDER BY id LIMIT $2`),
	"new_offers": idTrigger("upstream_offer", "优惠 #",
		`SELECT id, provider_code FROM upstream_offers WHERE status = 'new' AND id > $1 %s ORDER BY id LIMIT $2`),
	"held_benchmark_runs": idTrigger("benchmark_run", "基准运行 #",
		`SELECT id, '' FROM benchmark_runs WHERE NOT published AND origin <> 'manual' AND created_at > now() - interval '7 days' AND id > $1 %s ORDER BY id LIMIT $2`),
	"missing_metadata": idTrigger("virtual_model", "模型 #",
		`SELECT vm.id, vm.name FROM virtual_models vm LEFT JOIN virtual_model_metadata m ON m.virtual_model_id = vm.id
		 WHERE vm.status = 'active' AND COALESCE(m.description, '') = '' AND vm.id > $1 %s ORDER BY vm.id LIMIT $2`),
	"pending_aliases": pendingAliases,
	"failing_sources": failingSources,
}

// notProposed 排除已有该剧本提案的对象（同一对象只预审一次）。
const notProposed = `AND NOT EXISTS (SELECT 1 FROM agent_proposals p WHERE p.target_type = '%s' AND p.target_id = %s::text AND p.playbook = $3)`

func idTrigger(targetType, labelPrefix, query string) Trigger {
	return func(ctx context.Context, pool *pgxpool.Pool, playbook string, cur Cursor, limit int) ([]Item, Cursor, error) {
		idCol := "id"
		if targetType == "virtual_model" {
			idCol = "vm.id"
		}
		q := fmt.Sprintf(query, fmt.Sprintf(notProposed, targetType, idCol))
		rows, err := pool.Query(ctx, q, cur.LastID, limit, playbook)
		if err != nil {
			return nil, cur, fmt.Errorf("trigger %s: %w", targetType, err)
		}
		defer rows.Close()
		var items []Item
		next := cur
		for rows.Next() {
			var id int64
			var label string
			if err := rows.Scan(&id, &label); err != nil {
				return nil, cur, err
			}
			l := labelPrefix + strconv.FormatInt(id, 10)
			if label != "" {
				l += " " + label
			}
			items = append(items, Item{Type: targetType, ID: strconv.FormatInt(id, 10), Label: l})
			next.LastID = id
		}
		return items, next, rows.Err()
	}
}

// pendingAliases：新出现的 suggested / unmatched 榜单名（按 first_seen_at 推进）。
func pendingAliases(ctx context.Context, pool *pgxpool.Pool, playbook string, cur Cursor, limit int) ([]Item, Cursor, error) {
	since := time.Time{}
	if cur.Since != nil {
		since = *cur.Since
	}
	rows, err := pool.Query(ctx,
		`SELECT namespace, external_label, first_seen_at FROM model_aliases a
		 WHERE status IN ('suggested','unmatched') AND first_seen_at > $1
		   AND NOT EXISTS (SELECT 1 FROM agent_proposals p WHERE p.target_type = 'model_alias'
		                   AND p.target_id = a.namespace || ':' || a.external_label AND p.playbook = $3)
		 ORDER BY first_seen_at LIMIT $2`, since, limit, playbook)
	if err != nil {
		return nil, cur, fmt.Errorf("trigger pending_aliases: %w", err)
	}
	defer rows.Close()
	var items []Item
	next := cur
	for rows.Next() {
		var ns, label string
		var at time.Time
		if err := rows.Scan(&ns, &label, &at); err != nil {
			return nil, cur, err
		}
		items = append(items, Item{Type: "model_alias", ID: ns + ":" + label, Label: ns + "「" + label + "」"})
		next.Since = &at
	}
	return items, next, rows.Err()
}

// failingSources：连续失败 >= 3 次的数据源；失败次数变化后才再次诊断。
func failingSources(ctx context.Context, pool *pgxpool.Pool, _ string, cur Cursor, limit int) ([]Item, Cursor, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, name, consecutive_failures FROM price_sources WHERE enabled AND consecutive_failures >= 3 ORDER BY id`)
	if err != nil {
		return nil, cur, fmt.Errorf("trigger failing_sources: %w", err)
	}
	defer rows.Close()
	next := Cursor{Seen: map[string]int64{}}
	var items []Item
	for rows.Next() {
		var id, failures int64
		var name string
		if err := rows.Scan(&id, &name, &failures); err != nil {
			return nil, cur, err
		}
		key := strconv.FormatInt(id, 10)
		next.Seen[key] = failures
		if cur.Seen[key] == failures || len(items) >= limit {
			if len(items) >= limit {
				next.Seen[key] = cur.Seen[key] // 本轮没处理，下轮再来
			}
			continue
		}
		items = append(items, Item{Type: "price_source", ID: key, Label: "数据源 #" + key + " " + name})
	}
	return items, next, rows.Err()
}

func decodeCursor(raw []byte) Cursor {
	var c Cursor
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}
