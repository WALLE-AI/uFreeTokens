// Package risk 实现技术方案 §7.12 描述的"Phase1 规则引擎"（不是"Risk Score
// 模型"——那是技术方案明确排除在 Phase1 范围之外的）：worker 定期扫描
// request_logs，按规则聚合出异常账户/异常 IP，命中阈值就采取相应动作。
//
// 目前只接了两条规则，都基于 request_logs 里真实会写入的字段：
//
//  1. ConsumptionSpike：账户在检测窗口内的消费总额（Σ charged_amount）相对
//     其历史基线速率的倍数。命中后自动降级 tier 到 free——比直接冻结温和，
//     消费突增不一定是恶意（可能是合法的用量爆发），先降级限制爆炸半径，
//     留给人工复核决定是否要进一步处置。没有历史基线的账户（比如刚注册、
//     第一次产生流量）不会被这条规则命中——没有基线就没法算"相对倍数"，
//     强行拿 0 做分母只会制造假阳性。
//  2. SharedIPFanout：同一个 client_ip 在窗口内被多少个不同账户使用。只上报
//     不自动处置——共享出口 IP（公司代理、移动网络 NAT、云函数出口）会产生
//     大量无害的"多账户共享一个 IP"场景，自动冻结的误伤风险比不处置更大；
//     命中只是把 Finding 交给调用方（目前是 cmd/worker 记日志），没有接真正
//     的人工通知渠道（邮件/Slack 之类），那是独立的运维集成工作。
//
// 技术方案原文还提到"大量 client_cancel（疑似断连白嫖）""大量 400（疑似探测）"
// 两条规则，这里没有实现——它们依赖的信号目前根本不存在：request_logs.status
// 现在只会被写成 success 或 upstream_error（见 internal/relay），被鉴权/模型
// 校验挡在 Reserve 之前的请求根本不会写日志，客户端主动断连也没有被识别为
// 单独状态。写一条查不到任何真实数据的规则不会带来任何检测能力，等
// internal/relay 把这两个信号接上（记录预检失败请求、区分客户端断连）再补。
//
// 这里的动作(降级/上报)在检测到问题时立即执行,不是"只发现问题、上报,
// 不自动修复"的对账哲学(见 internal/reconcile)——风控异常的代价是持续性的
// (账户在被处理之前每一分钟都可能在继续消耗/被滥用),而对账异常是历史事实,
// 晚一点人工介入不会让损失扩大,两者的权衡不同。
package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Action 是命中一条规则后采取的处置方式。
type Action string

const (
	ActionNone           Action = ""
	ActionDowngradeTier  Action = "downgrade_tier"
	ActionNotifyOperator Action = "notify_operator"
)

// Finding 是一次 Scan 命中的一条异常。AccountID 为 0 表示这条发现不是单账户
// 粒度的（目前只有 SharedIPFanout 这种情况，用 IP 分组）。
type Finding struct {
	Rule      string
	AccountID int64
	IP        string
	Detail    string
	Action    Action
	Applied   bool // Action 是否真的执行了改动；已经处于目标状态(比如账户已是 free tier)时为 false
}

// Thresholds 控制两条规则各自的判定标准。
type Thresholds struct {
	Window         time.Duration // 检测窗口(建议 1 分钟,worker 每分钟跑一次,与 §7.12 一致)
	BaselineWindow time.Duration // ConsumptionSpike 用于估算"正常速率"的历史窗口(建议 24 小时)
	MinSamples     int64         // 检测窗口内成功请求数低于这个值时不判定 ConsumptionSpike(样本太少,比率没有意义)
	SpikeRatio     float64       // 当前窗口消费 / 按基线速率折算到同等窗口长度的期望消费,超过此倍数判定为异常
	MinIPFanout    int64         // 同一 client_ip 在窗口内覆盖的不同账户数达到这个值才上报 SharedIPFanout
}

// DefaultThresholds 是一组保守的默认值，供 cmd/worker 直接使用；具体数值没有
// 生产流量数据支撑，上线后应该按实际误报率调整。
func DefaultThresholds() Thresholds {
	return Thresholds{
		Window:         time.Minute,
		BaselineWindow: 24 * time.Hour,
		MinSamples:     20,
		SpikeRatio:     5.0,
		MinIPFanout:    5,
	}
}

// Engine 是风控规则引擎,只依赖 Postgres 连接池——审计记录直接写
// admin_audit_logs(不依赖 internal/admin.Service,避免为了写一条审计记录
// 引入 wallet/secretbox 这些和风控本身无关的依赖)。
type Engine struct {
	pool       *pgxpool.Pool
	thresholds Thresholds
}

func New(pool *pgxpool.Pool, thresholds Thresholds) *Engine {
	return &Engine{pool: pool, thresholds: thresholds}
}

// Scan 以 now 为窗口结束时间跑一轮全部规则,命中的规则会立即执行对应的 Action
// (不是先上报再等人工确认要不要执行——见包注释关于风控 vs 对账的取舍),返回
// 全部命中的 Finding(包括没有触发实际改动的,比如账户已经是 free tier)。
func (e *Engine) Scan(ctx context.Context, now time.Time) ([]Finding, error) {
	var findings []Finding

	spikes, err := e.checkConsumptionSpike(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("risk: consumption spike check: %w", err)
	}
	findings = append(findings, spikes...)

	fanouts, err := e.checkSharedIPFanout(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("risk: shared ip fanout check: %w", err)
	}
	findings = append(findings, fanouts...)

	return findings, nil
}

func (e *Engine) checkConsumptionSpike(ctx context.Context, now time.Time) ([]Finding, error) {
	windowStart := now.Add(-e.thresholds.Window)
	baselineStart := windowStart.Add(-e.thresholds.BaselineWindow)

	current, err := e.sumChargedByAccount(ctx, windowStart, now)
	if err != nil {
		return nil, fmt.Errorf("query current window: %w", err)
	}
	baseline, err := e.sumChargedByAccount(ctx, baselineStart, windowStart)
	if err != nil {
		return nil, fmt.Errorf("query baseline window: %w", err)
	}

	scale := e.thresholds.Window.Seconds() / e.thresholds.BaselineWindow.Seconds()

	var findings []Finding
	for accountID, cur := range current {
		if cur.count < e.thresholds.MinSamples {
			continue
		}
		base, ok := baseline[accountID]
		if !ok || base.charged <= 0 {
			continue // 没有基线,没法算"相对倍数",不判定(见包注释)
		}
		expected := float64(base.charged) * scale
		if expected <= 0 {
			continue
		}
		ratio := float64(cur.charged) / expected
		if ratio < e.thresholds.SpikeRatio {
			continue
		}

		applied, err := e.downgradeTier(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("downgrade account %d: %w", accountID, err)
		}
		findings = append(findings, Finding{
			Rule:      "consumption_spike",
			AccountID: accountID,
			Detail:    fmt.Sprintf("window_charged=%d baseline_charged=%d ratio=%.2f threshold=%.2f", cur.charged, base.charged, ratio, e.thresholds.SpikeRatio),
			Action:    ActionDowngradeTier,
			Applied:   applied,
		})
	}
	return findings, nil
}

type accountWindowTotals struct {
	charged int64
	count   int64
}

func (e *Engine) sumChargedByAccount(ctx context.Context, from, to time.Time) (map[int64]accountWindowTotals, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT account_id, COALESCE(SUM(charged_amount), 0), COUNT(*)
		FROM request_logs
		WHERE status = 'success' AND created_at >= $1 AND created_at < $2
		GROUP BY account_id
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]accountWindowTotals{}
	for rows.Next() {
		var accountID int64
		var totals accountWindowTotals
		if err := rows.Scan(&accountID, &totals.charged, &totals.count); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out[accountID] = totals
	}
	return out, rows.Err()
}

// downgradeTier 把账户 tier 改成 free,并写一条审计记录。已经是 free 的账户
// 直接跳过(不产生一条"从 free 降到 free"的空审计记录)。
func (e *Engine) downgradeTier(ctx context.Context, accountID int64) (applied bool, err error) {
	var before string
	if err := e.pool.QueryRow(ctx, `SELECT tier FROM accounts WHERE id = $1`, accountID).Scan(&before); err != nil {
		return false, fmt.Errorf("load current tier: %w", err)
	}
	if before == "free" {
		return false, nil
	}

	if _, err := e.pool.Exec(ctx, `UPDATE accounts SET tier = 'free' WHERE id = $1`, accountID); err != nil {
		return false, fmt.Errorf("update tier: %w", err)
	}
	if err := e.recordAudit(ctx, "risk.auto_downgrade_tier", "account", strconv.FormatInt(accountID, 10),
		map[string]string{"tier": before}, map[string]string{"tier": "free"}); err != nil {
		return true, fmt.Errorf("record audit: %w", err)
	}
	return true, nil
}

func (e *Engine) checkSharedIPFanout(ctx context.Context, now time.Time) ([]Finding, error) {
	windowStart := now.Add(-e.thresholds.Window)

	rows, err := e.pool.Query(ctx, `
		SELECT host(client_ip), COUNT(DISTINCT account_id)
		FROM request_logs
		WHERE client_ip IS NOT NULL AND created_at >= $1 AND created_at < $2
		GROUP BY client_ip
		HAVING COUNT(DISTINCT account_id) >= $3
	`, windowStart, now, e.thresholds.MinIPFanout)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var ip string
		var accounts int64
		if err := rows.Scan(&ip, &accounts); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		findings = append(findings, Finding{
			Rule:   "shared_ip_fanout",
			IP:     ip,
			Detail: fmt.Sprintf("distinct_accounts=%d threshold=%d", accounts, e.thresholds.MinIPFanout),
			Action: ActionNotifyOperator,
		})
	}
	return findings, rows.Err()
}

func (e *Engine) recordAudit(ctx context.Context, action, targetType, targetID string, before, after any) error {
	beforeJSON, err := marshalJSON(before)
	if err != nil {
		return fmt.Errorf("marshal before: %w", err)
	}
	afterJSON, err := marshalJSON(after)
	if err != nil {
		return fmt.Errorf("marshal after: %w", err)
	}
	// actor_id=0 表示"系统自动执行"，和 internal/admin 包里人工操作留空 ActorID
	// 时的约定一致（见 internal/admin/audit.go 的 AuditLogInput 文档）。
	_, err = e.pool.Exec(ctx,
		`INSERT INTO admin_audit_logs (actor_id, action, target_type, target_id, before, after)
		 VALUES (0, $1, $2, $3, $4, $5)`,
		action, targetType, targetID, beforeJSON, afterJSON,
	)
	return err
}

func marshalJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
