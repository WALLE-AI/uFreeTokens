// Package reconcile 实现三类对账（技术方案 §7.11、§7.16.8）：
//
//  1. 钱包内部一致性：wallets.cash_balance 应该等于该账户全部现金流水
//     （ledger_entries, balance_kind='cash'）之和；wallets.frozen 应该等于
//     该账户当前处于 held 状态的预扣金额之和。
//  2. 账本 vs 请求日志：某个时间窗口内，ledger_entries 里的消费总额应该等于
//     request_logs 里成功请求的 charged_amount 总额。
//  3. 账单级对账（billing.go，§7.16.8）：按"渠道 × 模型 × 天"比较本地成本
//     （request_logs.cost_amount）与上游账单（BillingFetcher，L1 来源）。
//     这里只搭好框架和比对逻辑——没有真实上游账单 API 的凭据无法验证任何具体
//     实现是否正确，各家认证方式/明细粒度/时区处理也都不一样，接一个新
//     BillingFetcher 实现留给后续按需接入；测试只用 MockBillingFetcher，不发
//     任何真实网络请求。
//
// 这里只发现问题、上报，不自动修复——账本、余额这类数据出现不一致本身就说明
// 某处逻辑或者某次人工操作绕开了应有的路径，自动"纠正"很可能是在掩盖问题而不是
// 解决问题，应该由人工介入排查。
package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Reconciler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Reconciler {
	return &Reconciler{pool: pool}
}

// WalletDiscrepancy 描述某个账户在某个字段上出现的不一致。
type WalletDiscrepancy struct {
	AccountID int64
	Field     string // cash_balance / frozen / bonus_balance
	Recorded  int64  // wallets 表里当前存的值
	Expected  int64  // 根据流水/预扣记录反推出的应有值
}

func (d WalletDiscrepancy) Diff() int64 { return d.Recorded - d.Expected }

// CheckWallets 扫描全部钱包，比较 cash_balance/frozen/bonus_balance 与各自的
// 事实来源（ledger_entries、held reservations、credit_grants）是否一致。
// 返回全部发现的不一致项；没有问题时返回空切片。
func (r *Reconciler) CheckWallets(ctx context.Context) ([]WalletDiscrepancy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
		    w.account_id,
		    w.cash_balance,
		    w.frozen,
		    w.bonus_balance,
		    COALESCE(cash.total, 0)  AS ledger_cash_total,
		    COALESCE(held.total, 0) AS held_total,
		    COALESCE(grants.total, 0) AS grants_total
		FROM wallets w
		LEFT JOIN (
		    SELECT account_id, SUM(amount) AS total
		    FROM ledger_entries WHERE balance_kind = 'cash'
		    GROUP BY account_id
		) cash ON cash.account_id = w.account_id
		LEFT JOIN (
		    SELECT account_id, SUM(amount) AS total
		    FROM reservations WHERE status = 'held'
		    GROUP BY account_id
		) held ON held.account_id = w.account_id
		LEFT JOIN (
		    SELECT account_id, SUM(remaining) AS total
		    FROM credit_grants WHERE expires_at IS NULL OR expires_at > now()
		    GROUP BY account_id
		) grants ON grants.account_id = w.account_id
	`)
	if err != nil {
		return nil, fmt.Errorf("reconcile: query wallets: %w", err)
	}
	defer rows.Close()

	var out []WalletDiscrepancy
	for rows.Next() {
		var accountID, cashBalance, frozen, bonusBalance, ledgerCashTotal, heldTotal, grantsTotal int64
		if err := rows.Scan(&accountID, &cashBalance, &frozen, &bonusBalance, &ledgerCashTotal, &heldTotal, &grantsTotal); err != nil {
			return nil, fmt.Errorf("reconcile: scan wallet row: %w", err)
		}
		if cashBalance != ledgerCashTotal {
			out = append(out, WalletDiscrepancy{AccountID: accountID, Field: "cash_balance", Recorded: cashBalance, Expected: ledgerCashTotal})
		}
		if frozen != heldTotal {
			out = append(out, WalletDiscrepancy{AccountID: accountID, Field: "frozen", Recorded: frozen, Expected: heldTotal})
		}
		if bonusBalance != grantsTotal {
			out = append(out, WalletDiscrepancy{AccountID: accountID, Field: "bonus_balance", Recorded: bonusBalance, Expected: grantsTotal})
		}
	}
	return out, rows.Err()
}

// LedgerVsLogs 是账本消费总额与请求日志计费总额在某个时间窗口内的对比结果。
type LedgerVsLogs struct {
	WindowStart      time.Time
	WindowEnd        time.Time
	LedgerTotal      int64 // ledger_entries 里 type='consume' 的绝对值总和
	RequestLogsTotal int64 // request_logs 里 status='success' 的 charged_amount 总和
}

func (l LedgerVsLogs) Diff() int64 { return l.LedgerTotal - l.RequestLogsTotal }

// CheckLedgerVsRequestLogs 比较 [from, to) 窗口内的账本消费与请求日志计费总额。
// 调用方应该让 to 落后于"现在"至少几分钟——request_logs 是异步批量写入的
// （internal/reqlog：每 500 条或 1 秒 flush），账本是同步写入的，太靠近当前时刻
// 的窗口会因为这个写入延迟出现"账本已有、日志还没落盘"的暂时性差异，不是真的
// 数据不一致。
func (r *Reconciler) CheckLedgerVsRequestLogs(ctx context.Context, from, to time.Time) (*LedgerVsLogs, error) {
	var ledgerTotal int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(-amount), 0) FROM ledger_entries
		 WHERE type = 'consume' AND balance_kind = 'cash' AND created_at >= $1 AND created_at < $2`,
		from, to,
	).Scan(&ledgerTotal); err != nil {
		return nil, fmt.Errorf("reconcile: sum ledger consume: %w", err)
	}

	var logsTotal int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(charged_amount), 0) FROM request_logs
		 WHERE status = 'success' AND created_at >= $1 AND created_at < $2`,
		from, to,
	).Scan(&logsTotal); err != nil {
		return nil, fmt.Errorf("reconcile: sum request_logs charged_amount: %w", err)
	}

	return &LedgerVsLogs{WindowStart: from, WindowEnd: to, LedgerTotal: ledgerTotal, RequestLogsTotal: logsTotal}, nil
}

// UpstreamCostDrift 是一个渠道在窗口内「按成本价计算的成本」与「上游报告的成本」的
// 偏差（多供应商实施方案 §8）：偏差大通常意味着成本价录错，或上游调价了。
type UpstreamCostDrift struct {
	ChannelID      int64
	VirtualModel   string
	Requests       int64
	CostCNY        float64 // sum(request_logs.cost_amount) / 1e6
	UpstreamCNY    float64 // sum(request_logs.upstream_cost) × 供应商币种汇率
	UpstreamNative float64 // sum(request_logs.upstream_cost)，供应商币种
	Currency       string
}

// Ratio 是偏差比例 |成本 − 上游| / 上游。
func (d UpstreamCostDrift) Ratio() float64 {
	if d.UpstreamCNY == 0 {
		return 0
	}
	diff := d.CostCNY - d.UpstreamCNY
	if diff < 0 {
		diff = -diff
	}
	return diff / d.UpstreamCNY
}

// UpstreamCostDriftThreshold 是告警阈值（偏差超过 20%）。
const UpstreamCostDriftThreshold = 0.2

// CheckUpstreamCost 找出窗口内上游报告了成本、且与按成本价计算的成本偏差超过阈值的
// 渠道。只统计同时有 cost_amount 与 upstream_cost 的成功请求；供应商币种没有汇率时跳过。
func (r *Reconciler) CheckUpstreamCost(ctx context.Context, from, to time.Time) ([]UpstreamCostDrift, error) {
	rows, err := r.pool.Query(ctx, `
		WITH fx AS (
			SELECT DISTINCT ON (base) base, rate FROM fx_rates
			WHERE quote = 'CNY' AND effective_date <= CURRENT_DATE ORDER BY base, effective_date DESC
		)
		SELECT rl.channel_id, max(rl.virtual_model), count(*), p.currency,
		       sum(rl.cost_amount)::float8 / 1e6, sum(rl.upstream_cost)::float8,
		       sum(rl.upstream_cost)::float8 * CASE WHEN p.currency = 'CNY' THEN 1 ELSE max(fx.rate)::float8 END
		FROM request_logs rl
		JOIN channels c ON c.id = rl.channel_id
		JOIN provider_accounts pa ON pa.id = c.provider_account_id
		JOIN providers p ON p.id = pa.provider_id
		LEFT JOIN fx ON fx.base = p.currency
		WHERE rl.created_at >= $1 AND rl.created_at < $2 AND rl.status = 'success'
		  AND rl.cost_amount IS NOT NULL AND rl.upstream_cost IS NOT NULL
		GROUP BY rl.channel_id, p.currency
		HAVING p.currency = 'CNY' OR max(fx.rate) IS NOT NULL`, from, to)
	if err != nil {
		return nil, fmt.Errorf("reconcile: query upstream cost: %w", err)
	}
	defer rows.Close()
	var out []UpstreamCostDrift
	for rows.Next() {
		var d UpstreamCostDrift
		if err := rows.Scan(&d.ChannelID, &d.VirtualModel, &d.Requests, &d.Currency, &d.CostCNY, &d.UpstreamNative, &d.UpstreamCNY); err != nil {
			return nil, fmt.Errorf("reconcile: scan upstream cost: %w", err)
		}
		if d.Ratio() > UpstreamCostDriftThreshold {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// Report 汇总一次对账运行的全部发现。
type Report struct {
	WalletDiscrepancies []WalletDiscrepancy
	LedgerVsLogs        *LedgerVsLogs
	// UpstreamCostDrifts 是成本价与上游报告成本偏差过大的渠道（只告警，不影响 Clean）。
	UpstreamCostDrifts []UpstreamCostDrift
}

// Clean 判断本次对账是否完全没有发现问题。
func (rep Report) Clean() bool {
	return len(rep.WalletDiscrepancies) == 0 && (rep.LedgerVsLogs == nil || rep.LedgerVsLogs.Diff() == 0)
}

// Run 执行一次完整对账：全量钱包一致性检查 + 指定回溯窗口的账本/日志对比
// （窗口结束时间为 now() - buffer，避开异步写入延迟）。
func (r *Reconciler) Run(ctx context.Context, lookback, buffer time.Duration) (*Report, error) {
	discrepancies, err := r.CheckWallets(ctx)
	if err != nil {
		return nil, err
	}

	to := time.Now().Add(-buffer)
	from := to.Add(-lookback)
	ledgerVsLogs, err := r.CheckLedgerVsRequestLogs(ctx, from, to)
	if err != nil {
		return nil, err
	}

	drifts, err := r.CheckUpstreamCost(ctx, from, to)
	if err != nil {
		return nil, err
	}
	return &Report{WalletDiscrepancies: discrepancies, LedgerVsLogs: ledgerVsLogs, UpstreamCostDrifts: drifts}, nil
}
