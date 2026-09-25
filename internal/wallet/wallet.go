// Package wallet 是钱包与账本的唯一写入路径（技术方案 §7.11：
// "internal/wallet 是唯一能写 wallets / ledger_entries / credit_grants 的模块"）。
//
// 核心是两阶段计费（§7.9.1）：
//
//	Reserve（预扣）：原子冻结预估费用，余额不足直接拒绝，杜绝"先放行后扣费"的透支风险
//	                （技术方案 §1.3 A1，是 V1 方案里最严重的问题）。
//	Settle（结算）： 按实际用量扣款，释放冻结与实扣的差额；以 request_id 做幂等，
//	                重复调用不会重复扣费。
//	Release（释放）：请求未产生费用时（如上游首字节前失败）全额解冻。
//
// 当前实现只从现金余额（cash_balance）扣款；赠送余额/免费额度按来源精确扣减
// 属于促销引擎（§7.10）的职责，尚未接入——为了不破坏 "bonus_balance = Σ 未过期
// credit_grants.remaining" 这一不变式，这里不直接扣减 bonus_balance，避免账目
// 内部不一致。这是当前已知的范围限制，非遗漏。
package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrInsufficientBalance 对应附录 A 的 402 insufficient_balance。
	ErrInsufficientBalance = errors.New("wallet: insufficient balance")
	// ErrReservationNotFound 表示 request_id 从未被 Reserve 过（Settle/Release 找不到记录）。
	ErrReservationNotFound = errors.New("wallet: reservation not found")
	// ErrReservationReleased 表示该 request_id 的冻结已经被释放，不能再结算。
	ErrReservationReleased = errors.New("wallet: reservation already released")
)

type Status string

const (
	StatusHeld     Status = "held"
	StatusSettled  Status = "settled"
	StatusReleased Status = "released"
)

// Hold 是 Reserve 成功后的凭证。
type Hold struct {
	RequestID string
	AccountID int64
	Amount    int64 // 冻结的微元数
	Status    Status
}

// Receipt 是 Settle 成功后的结果，可直接写入 request_logs 的计费快照字段。
type Receipt struct {
	RequestID     string
	AccountID     int64
	ChargedAmount int64 // 实扣微元数（可能小于、等于或大于 Hold.Amount，见 §7.9.1）
	CashAfter     int64
	BonusAfter    int64
}

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Reserve 原子地冻结 amount 微元。可用余额 = cash_balance + bonus_balance +
// accounts.credit_limit - frozen（技术方案 §6.5）。幂等：同一 request_id 重复调用
// 返回首次创建的 Hold，不会重复冻结。
func (s *Service) Reserve(ctx context.Context, requestID string, accountID int64, amount int64, ttl time.Duration) (*Hold, error) {
	if amount < 0 {
		return nil, fmt.Errorf("wallet: reserve amount must be >= 0, got %d", amount)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("wallet: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 提交后 Rollback 是 no-op

	// 先尝试插入 reservation；ON CONFLICT DO NOTHING 使得重复请求天然幂等。
	var inserted bool
	if err := tx.QueryRow(ctx,
		`INSERT INTO reservations (request_id, account_id, amount, status, expires_at)
		 VALUES ($1, $2, $3, 'held', $4)
		 ON CONFLICT (request_id) DO NOTHING
		 RETURNING true`,
		requestID, accountID, amount, time.Now().Add(ttl),
	).Scan(&inserted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			inserted = false
		} else {
			return nil, fmt.Errorf("wallet: insert reservation: %w", err)
		}
	}

	if !inserted {
		// 已存在：查出当前状态直接返回，保证幂等（不重复冻结余额）。
		existing, err := s.loadReservation(ctx, tx, requestID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("wallet: commit: %w", err)
		}
		return existing, nil
	}

	// 原子条件更新：只有可用余额足够时才会真正冻结（技术方案 §7.9.1）。
	// UPDATE 本身对匹配行加锁，与并发的 Reserve/Settle 天然互斥，不需要额外 SELECT ... FOR UPDATE。
	tag, err := tx.Exec(ctx,
		`UPDATE wallets w
		 SET frozen = frozen + $2, updated_at = now()
		 FROM accounts a
		 WHERE w.account_id = $1
		   AND a.id = $1
		   AND (w.cash_balance + w.bonus_balance + a.credit_limit - w.frozen) >= $2`,
		accountID, amount,
	)
	if err != nil {
		return nil, fmt.Errorf("wallet: update wallet frozen: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// 余额不足（或账户不存在）：回滚整个事务，reservation 插入也一并撤销。
		return nil, ErrInsufficientBalance
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("wallet: commit: %w", err)
	}

	return &Hold{RequestID: requestID, AccountID: accountID, Amount: amount, Status: StatusHeld}, nil
}

// Settle 按实际费用结算。actualAmount 可以大于、等于或小于 Reserve 时冻结的金额
// （技术方案 §7.9.1：允许现金余额出现小额负数，由上层的并发上限兜底）。
// 幂等：对已结算的 request_id 重复调用会返回 nil, nil（无副作用，非错误），
// 调用方应视为"已经处理过"，除非需要拿回 Receipt——此时应改从 request_logs 读取快照。
func (s *Service) Settle(ctx context.Context, requestID string, actualAmount int64) (*Receipt, error) {
	if actualAmount < 0 {
		return nil, fmt.Errorf("wallet: settle amount must be >= 0, got %d", actualAmount)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("wallet: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var accountID, heldAmount int64
	err = tx.QueryRow(ctx,
		`UPDATE reservations SET status = 'settled'
		 WHERE request_id = $1 AND status = 'held'
		 RETURNING account_id, amount`,
		requestID,
	).Scan(&accountID, &heldAmount)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// 未能从 held 迁移到 settled：要么从未 Reserve 过，要么已经处理完毕（幂等短路）。
		status, ferr := s.reservationStatus(ctx, tx, requestID)
		if ferr != nil {
			return nil, ferr
		}
		switch status {
		case StatusSettled:
			return nil, nil // 已结算过，幂等：调用方不应重复展示/重复下游动作
		case StatusReleased:
			return nil, ErrReservationReleased
		default:
			return nil, ErrReservationNotFound
		}
	case err != nil:
		return nil, fmt.Errorf("wallet: mark reservation settled: %w", err)
	}

	var cashAfter, bonusAfter int64
	if err := tx.QueryRow(ctx,
		`UPDATE wallets
		 SET cash_balance = cash_balance - $2, frozen = frozen - $3, updated_at = now()
		 WHERE account_id = $1
		 RETURNING cash_balance, bonus_balance`,
		accountID, actualAmount, heldAmount,
	).Scan(&cashAfter, &bonusAfter); err != nil {
		return nil, fmt.Errorf("wallet: update wallet balance: %w", err)
	}

	if actualAmount > 0 {
		// 不依赖 ledger_entries 上的 UNIQUE(ref_type, ref_id, type, balance_kind, grant_id)
		// 做幂等去重：grant_id 为 NULL 时 Postgres 唯一索引把每个 NULL 视为互不相同，
		// ON CONFLICT 在这种场景下不会触发，起不到防重复的作用。真正的幂等保证来自上面
		// "reservations: held -> settled" 的原子状态迁移——同一 request_id 只有第一次
		// 调用能把状态从 held 改成 settled，重复调用会在前面的 switch 里短路返回，
		// 根本不会执行到这里，所以这里只需要普通 INSERT。
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id)
			 VALUES ($1, 'consume', $2, 'cash', $3, $4, 'request', $5)`,
			accountID, -actualAmount, cashAfter, bonusAfter, requestID,
		); err != nil {
			return nil, fmt.Errorf("wallet: insert ledger entry: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("wallet: commit: %w", err)
	}

	return &Receipt{
		RequestID:     requestID,
		AccountID:     accountID,
		ChargedAmount: actualAmount,
		CashAfter:     cashAfter,
		BonusAfter:    bonusAfter,
	}, nil
}

// Release 全额解冻一个尚未结算的 reservation（例如上游在首字节前失败，本次请求
// 未产生任何费用）。幂等：对已释放/已结算的 request_id 重复调用直接返回 nil。
func (s *Service) Release(ctx context.Context, requestID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("wallet: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var accountID, amount int64
	err = tx.QueryRow(ctx,
		`UPDATE reservations SET status = 'released'
		 WHERE request_id = $1 AND status = 'held'
		 RETURNING account_id, amount`,
		requestID,
	).Scan(&accountID, &amount)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		status, ferr := s.reservationStatus(ctx, tx, requestID)
		if ferr != nil {
			return ferr
		}
		if status == StatusReleased || status == StatusSettled {
			return nil // 幂等短路
		}
		return ErrReservationNotFound
	case err != nil:
		return fmt.Errorf("wallet: mark reservation released: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE wallets SET frozen = frozen - $2, updated_at = now() WHERE account_id = $1`,
		accountID, amount,
	); err != nil {
		return fmt.Errorf("wallet: update wallet frozen: %w", err)
	}

	return tx.Commit(ctx)
}

// ReclaimExpired 释放所有已过期仍处于 held 状态的预扣记录（技术方案 §7.9.3、§7.11）。
// 网关在 Reserve 之后、Settle/Release 之前崩溃或异常退出，会留下"孤儿"预扣——这些
// 冻结的钱会一直卡住，直到这里按 reservations.expires_at 超时把它们释放。
// 只处理 Reserve 时约定好的过期时间已经过去的记录，正常在途请求不受影响。
// 供 worker 定时调用；单次最多处理 limit 条（避免一次性扫出海量数据阻塞太久）。
// 返回本次实际释放的记录数。
func (s *Service) ReclaimExpired(ctx context.Context, limit int) (int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT request_id FROM reservations WHERE status = 'held' AND expires_at < now() ORDER BY expires_at ASC LIMIT $1`,
		limit,
	)
	if err != nil {
		return 0, fmt.Errorf("wallet: query expired reservations: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("wallet: scan expired reservation: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	reclaimed := 0
	for _, id := range ids {
		if err := s.Release(ctx, id); err != nil {
			// ErrReservationNotFound 理论上不会出现（我们刚查到它），但如果出现了，
			// 说明它在查询和释放之间被别的路径处理掉了——跳过继续，不中断整批回收。
			if errors.Is(err, ErrReservationNotFound) {
				continue
			}
			return reclaimed, fmt.Errorf("wallet: release expired reservation %s: %w", id, err)
		}
		reclaimed++
	}
	return reclaimed, nil
}

func (s *Service) loadReservation(ctx context.Context, tx pgx.Tx, requestID string) (*Hold, error) {
	var h Hold
	h.RequestID = requestID
	if err := tx.QueryRow(ctx,
		`SELECT account_id, amount, status FROM reservations WHERE request_id = $1`,
		requestID,
	).Scan(&h.AccountID, &h.Amount, &h.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReservationNotFound
		}
		return nil, fmt.Errorf("wallet: load reservation: %w", err)
	}
	return &h, nil
}

func (s *Service) reservationStatus(ctx context.Context, tx pgx.Tx, requestID string) (Status, error) {
	var status Status
	if err := tx.QueryRow(ctx, `SELECT status FROM reservations WHERE request_id = $1`, requestID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrReservationNotFound
		}
		return "", fmt.Errorf("wallet: load reservation status: %w", err)
	}
	return status, nil
}
