// Package promotion 是促销引擎的售价侧实现（技术方案 §7.10）。
//
// 促销分两个面：cost 面（改变平台成本，如上游免费月）和 sell 面（改变用户实付）。
// 本阶段只实现 sell 面——cost 面需要渠道成本价（cost price book）参与路由的毛利
// 计算，而 catalog 目前只加载了 sell price book（见 internal/catalog 包注释），
// cost 面留作后续。
//
// 支持的促销类型（sell 面）：
//   - price_discount：按比例打折，可选 budget_total 预算上限。
//   - free_quota：每周期（daily/monthly）一定金额内免费，超出部分正常计费。
//
// 叠加规则（stackable，技术方案 §7.10）：候选按 priority 从高到低依次尝试；
// 一条促销命中并成功应用后，只有它自己标了 stackable=true，才会继续把折后价
// 喂给下一条候选叠加；命中但 stackable=false（默认）到此为止，和只有一条促销
// 时行为完全一样。
//
// 不支持的（已知范围限制，非遗漏）：
//   - 促销之间失败降级链（比如预算用完后退而求其次匹配下一条不叠加的促销）：
//     某条候选预算/额度不足时，链就停在这里（保留已经叠加成功的部分），
//     不会跳过它去尝试优先级更低的下一条。
package promotion

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Engine struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Engine {
	return &Engine{pool: pool}
}

// Quote 根据账户/虚拟模型/tier 和原价（listAmount，微元）依次尝试命中的 sell 面
// 促销并原子应用，返回实扣金额与命中的促销 ID 列表（未命中任何促销时 promotionIDs
// 为 nil，chargedAmount == listAmount）。命中多条时的叠加规则见包注释
// （stackable=true 才会继续叠加下一条，否则到此为止）。
//
// "原子应用"指：free_quota 的额度扣减、price_discount 的预算扣减都在一次数据库
// 事务内用条件更新完成，和 wallet.Reserve 同样的原子性要求——促销额度本质上也是
// "钱"，不能有并发下的超发。
func (e *Engine) Quote(ctx context.Context, accountID int64, tier, vmName string, listAmount int64) (chargedAmount int64, promotionIDs []int64, err error) {
	if listAmount <= 0 {
		return listAmount, nil, nil
	}

	candidates, err := e.loadCandidates(ctx, tier, vmName)
	if err != nil {
		return listAmount, nil, fmt.Errorf("promotion: load candidates: %w", err)
	}
	if len(candidates) == 0 {
		return listAmount, nil, nil
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return listAmount, nil, fmt.Errorf("promotion: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	current := listAmount
	var applied []int64

	for _, c := range candidates {
		var chargedThis int64
		var ok bool
		switch c.Type {
		case TypeFreeQuota:
			chargedThis, ok, err = e.applyFreeQuota(ctx, tx, c, accountID, current)
		case TypePriceDiscount:
			chargedThis, ok, err = e.applyPriceDiscount(ctx, tx, c, current)
		default:
			continue // 未知类型，不应用（防御性兜底，理论上不会发生），继续看下一条候选
		}
		if err != nil {
			return listAmount, nil, err
		}
		if !ok {
			break // 额度/预算不足：链停在这里，不降级找下一条候选（见包注释）
		}
		applied = append(applied, c.ID)
		current = chargedThis
		if !c.Stackable {
			break
		}
	}

	if len(applied) == 0 {
		return listAmount, nil, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return listAmount, nil, fmt.Errorf("promotion: commit: %w", err)
	}
	return current, applied, nil
}

type Type string

const (
	TypePriceDiscount Type = "price_discount"
	TypeFreeQuota     Type = "free_quota"
)

type promo struct {
	ID          int64
	Type        Type
	Discount    float64 // price_discount
	Period      string  // free_quota: daily / monthly
	AmountMicro int64   // free_quota: 每周期免费额度
	HasBudget   bool
	Stackable   bool // true 时命中并应用成功后会继续叠加下一条候选（见包注释）
}

type scopeJSON struct {
	Models []string `json:"models,omitempty"`
	Tiers  []string `json:"tiers,omitempty"`
}

type paramsJSON struct {
	Discount    *float64 `json:"discount,omitempty"`
	Period      string   `json:"period,omitempty"`
	AmountMicro *int64   `json:"amount_micro,omitempty"`
}

func (e *Engine) loadCandidates(ctx context.Context, tier, vmName string) ([]promo, error) {
	rows, err := e.pool.Query(ctx,
		`SELECT id, type, params, scope, budget_total, stackable
		 FROM promotions
		 WHERE side = 'sell' AND status = 'active'
		   AND starts_at <= now() AND (ends_at IS NULL OR ends_at > now())
		 ORDER BY priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []promo
	for rows.Next() {
		var (
			id                  int64
			typ                 string
			paramsRaw, scopeRaw []byte
			budgetTotal         *int64
			stackable           bool
		)
		if err := rows.Scan(&id, &typ, &paramsRaw, &scopeRaw, &budgetTotal, &stackable); err != nil {
			return nil, err
		}

		var scope scopeJSON
		if len(scopeRaw) > 0 {
			if err := json.Unmarshal(scopeRaw, &scope); err != nil {
				continue // 畸形数据跳过，不影响其它促销
			}
		}
		if !matches(scope.Models, vmName) || !matches(scope.Tiers, tier) {
			continue
		}

		var params paramsJSON
		if len(paramsRaw) > 0 {
			if err := json.Unmarshal(paramsRaw, &params); err != nil {
				continue
			}
		}

		p := promo{ID: id, Type: Type(typ), HasBudget: budgetTotal != nil, Stackable: stackable}
		switch p.Type {
		case TypePriceDiscount:
			if params.Discount == nil || *params.Discount <= 0 || *params.Discount > 1 {
				continue
			}
			p.Discount = *params.Discount
		case TypeFreeQuota:
			if params.AmountMicro == nil || *params.AmountMicro <= 0 {
				continue
			}
			p.AmountMicro = *params.AmountMicro
			p.Period = params.Period
			if p.Period != "monthly" {
				p.Period = "daily" // 默认按天，未识别的周期也归为 daily
			}
		default:
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// matches 判断 value 是否在 scope 限定范围内；scope 为空表示不限制。
func matches(scope []string, value string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if s == value {
			return true
		}
	}
	return false
}

func periodKey(period string, at time.Time) string {
	at = at.UTC()
	if period == "monthly" {
		return at.Format("2006-01")
	}
	return at.Format("2006-01-02")
}

// applyFreeQuota 原子地从本周期剩余免费额度中扣减最多 listAmount，返回实扣金额
// （原价 - 实际覆盖的免费部分）。
//
// 分两条语句顺序执行，而不是塞进一条带两个平级 CTE 的语句：INSERT ... ON CONFLICT
// DO NOTHING 和随后的 SELECT ... FOR UPDATE 如果写成互不引用的两个 CTE，
// PostgreSQL 不保证先后顺序——本行第一次消费（counter 行还不存在）时，
// SELECT 端的 CTE 有可能先于 INSERT 端执行，锁不到刚插入的行，整条语句静默返回
// 0 行。拆成两条顺序语句是让"先插入、再加锁读改"这个先后关系由客户端保证，
// 而不是依赖优化器不承诺的执行顺序。
const ensureCounterSQL = `
INSERT INTO promotion_counters (promotion_id, account_id, period_key, used_amount)
VALUES ($1, $2, $3, 0)
ON CONFLICT (promotion_id, account_id, period_key) DO NOTHING
`

const consumeCounterSQL = `
WITH locked AS (
    SELECT used_amount FROM promotion_counters
    WHERE promotion_id = $1 AND account_id = $2 AND period_key = $3
    FOR UPDATE
)
UPDATE promotion_counters pc
SET used_amount = pc.used_amount + LEAST($4, GREATEST($5 - locked.used_amount, 0))
FROM locked
WHERE pc.promotion_id = $1 AND pc.account_id = $2 AND pc.period_key = $3
RETURNING LEAST($4, GREATEST($5 - locked.used_amount, 0))
`

func (e *Engine) applyFreeQuota(ctx context.Context, tx pgx.Tx, p promo, accountID, listAmount int64) (charged int64, applied bool, err error) {
	key := periodKey(p.Period, time.Now())

	if _, err := tx.Exec(ctx, ensureCounterSQL, p.ID, accountID, key); err != nil {
		return listAmount, false, fmt.Errorf("promotion: ensure counter: %w", err)
	}

	var covered int64
	if err := tx.QueryRow(ctx, consumeCounterSQL, p.ID, accountID, key, listAmount, p.AmountMicro).Scan(&covered); err != nil {
		return listAmount, false, fmt.Errorf("promotion: apply free_quota: %w", err)
	}
	if covered <= 0 {
		return listAmount, false, nil // 本周期额度已用尽，不应用
	}
	return listAmount - covered, true, nil
}

// applyPriceDiscount 按折扣比例计算实扣金额；若配置了预算上限，原子地检查并扣减
// promotions.budget_used（节省的金额），预算不足则不应用、按原价收取。
func (e *Engine) applyPriceDiscount(ctx context.Context, tx pgx.Tx, p promo, listAmount int64) (charged int64, applied bool, err error) {
	saved := int64(float64(listAmount) * p.Discount)
	charged = listAmount - saved
	if saved <= 0 {
		return listAmount, false, nil
	}

	if !p.HasBudget {
		return charged, true, nil
	}

	tag, err := tx.Exec(ctx,
		`UPDATE promotions SET budget_used = budget_used + $2
		 WHERE id = $1 AND (budget_used + $2) <= budget_total`,
		p.ID, saved,
	)
	if err != nil {
		return listAmount, false, fmt.Errorf("promotion: apply price_discount budget: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return listAmount, false, nil // 预算已耗尽
	}
	return charged, true, nil
}
