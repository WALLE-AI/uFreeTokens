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
// Settle 按 "赠送余额（按过期时间从近到远）→ 现金余额" 的顺序扣款（技术方案
// §7.9.1 step 2）：先花即将过期的赠款，剩下的部分才动现金——不会出现赠款还没
// 花完、现金却先被扣掉的情况，也不会破坏 "bonus_balance = Σ 未过期
// credit_grants.remaining" 这条不变式（因为扣减时同时更新了两边）。
// "免费额度"（free_quota 类促销）不在这里处理，那是在算出 actualAmount 之前，
// 由促销引擎（internal/promotion）就已经从应付金额里减掉的。
package wallet

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrInsufficientBalance 对应附录 A 的 402 insufficient_balance。
	ErrInsufficientBalance = errors.New("wallet: insufficient balance")
	// ErrReservationNotFound 表示 request_id 从未被 Reserve 过（Settle/Release 找不到记录）。
	ErrReservationNotFound = errors.New("wallet: reservation not found")
	// ErrReservationReleased 表示该 request_id 的冻结已经被释放，不能再结算。
	ErrReservationReleased = errors.New("wallet: reservation already released")
	// ErrDuplicateAdjustRef 表示同一账户已经用这个 refID 调过账。Adjust 里用
	// "advisory lock + 检查"给出这个明确的错误；数据库层 ledger_entries 的
	// UNIQUE NULLS NOT DISTINCT（迁移 00017）是最后一道防线。
	ErrDuplicateAdjustRef = errors.New("wallet: this ref_id has already been used for an adjustment on this account")
	// ErrDuplicateGrantRef 表示同一账户、同一来源已经用这个 refID 发过赠金——
	// 重试/重复提交不会重复发放（credit_grants 的唯一索引，迁移 00017）。
	ErrDuplicateGrantRef = errors.New("wallet: this ref_id has already been used for a credit grant on this account")
	// ErrBalanceChanged 表示调账时给出的"预期现金余额"与实际不符：运营看到的余额
	// 页面已经过时（期间有消费、充值或别人调过账），应该刷新后重新确认。
	ErrBalanceChanged = errors.New("wallet: cash balance has changed since it was displayed; refresh and confirm again")
	// ErrNegativeCashBalance 表示人工扣减会让现金余额变成负数。
	ErrNegativeCashBalance = errors.New("wallet: this adjustment would make the cash balance negative")
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

var validGrantSources = map[string]bool{"signup": true, "promotion": true, "compensation": true, "invite": true}

// GrantInput 描述一次赠送余额的发放（技术方案 §7.10 credit_grant 类促销、
// 注册赠送等）。
type GrantInput struct {
	AccountID  int64
	Source     string // signup / promotion / compensation / invite
	Amount     int64  // 微元，必须 > 0
	ExpiresAt  *time.Time
	ModelScope []string // nil = 全部模型可用
	RefID      string   // 审计用，比如促销活动 ID/工单号
}

// Grant 发放一笔赠送余额：写一条 credit_grants 记录，同时原子地把
// wallets.bonus_balance 加上同样的金额——这两步必须在一个事务里完成，
// 否则会出现"赠款记录已经存在、但账户余额还没涨"的中间态，用户这时候
// 发起请求会因为可用余额不足被拒绝，即使他们其实已经有赠款了。
func (s *Service) Grant(ctx context.Context, in GrantInput) (grantID int64, bonusAfter int64, err error) {
	if !validGrantSources[in.Source] {
		return 0, 0, fmt.Errorf("wallet: invalid grant source %q", in.Source)
	}
	if in.Amount <= 0 {
		return 0, 0, fmt.Errorf("wallet: grant amount must be positive, got %d", in.Amount)
	}
	if in.RefID == "" {
		return 0, 0, errors.New("wallet: grant requires a non-empty refID for audit purposes")
	}

	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return 0, 0, fmt.Errorf("wallet: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := tx.QueryRow(ctx,
		`INSERT INTO credit_grants (account_id, source, amount, remaining, model_scope, expires_at, ref_id)
		 VALUES ($1, $2, $3, $3, $4, $5, $6) RETURNING id`,
		in.AccountID, in.Source, in.Amount, in.ModelScope, in.ExpiresAt, in.RefID,
	).Scan(&grantID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_credit_grants_ref" {
			return 0, 0, ErrDuplicateGrantRef
		}
		return 0, 0, fmt.Errorf("wallet: insert credit_grant: %w", err)
	}

	var cashAfter int64
	if err := tx.QueryRow(ctx,
		`UPDATE wallets SET bonus_balance = bonus_balance + $2, updated_at = now()
		 WHERE account_id = $1 RETURNING cash_balance, bonus_balance`,
		in.AccountID, in.Amount,
	).Scan(&cashAfter, &bonusAfter); err != nil {
		return 0, 0, fmt.Errorf("wallet: update wallet bonus_balance: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, grant_id, cash_after, bonus_after, ref_type, ref_id, journal_id)
		 VALUES ($1, 'grant', $2, 'bonus', $3, $4, $5, 'promotion', $6, $7)`,
		in.AccountID, in.Amount, grantID, cashAfter, bonusAfter, in.RefID, newJournalID(),
	); err != nil {
		return 0, 0, fmt.Errorf("wallet: insert grant ledger entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("wallet: commit: %w", err)
	}
	return grantID, bonusAfter, nil
}

// CreateWallet 给一个新账户初始化钱包（余额全为 0）。技术方案 §6.5 的
// wallets 表以 account_id 为主键，每个账户有且只有一个钱包；这个方法是
// internal/admin 创建账户时应该调用的路径，而不是直接对 wallets 表写 SQL——
// 保持"只有 internal/wallet 写 wallets 表"这条不变式（见包文档）。
// 幂等：账户已经有钱包时直接返回，不报错。
func (s *Service) CreateWallet(ctx context.Context, accountID int64) error {
	_, err := s.db(ctx).Exec(ctx,
		`INSERT INTO wallets (account_id, cash_balance, bonus_balance, frozen)
		 VALUES ($1, 0, 0, 0) ON CONFLICT (account_id) DO NOTHING`,
		accountID,
	)
	if err != nil {
		return fmt.Errorf("wallet: create wallet: %w", err)
	}
	return nil
}

// Adjust 是管理员对现金余额的手工调整（充值到账、退款、纠错等），在真正的
// payment_orders 充值流程（技术方案 §7.11）落地之前，这是唯一合法的"给账户
// 加钱"的入口——不允许任何代码直接 UPDATE wallets.cash_balance，必须经过这里
// 才能同时写下 ledger_entries，保证账本和余额不会出现 §1.3 提到的那种对不上的
// 情况。amount 可正可负（正数=入账，负数=扣减，比如撤销一笔错误的赠送）。
// refID 建议填运营侧的工单号/操作记录 ID，方便审计时追溯这笔调整的来由。
func (s *Service) Adjust(ctx context.Context, accountID int64, amount int64, refID string) (*Receipt, error) {
	receipt, _, err := s.AdjustChecked(ctx, accountID, amount, refID, nil)
	return receipt, err
}

// AdjustChecked 是 Adjust 的带防护版本，供运营后台使用（运营后台接口方案 §4.7）：
//   - expectedCash 非 nil 时，在同一事务里锁住钱包行并核对当前现金余额，
//     不等返回 ErrBalanceChanged——防止运营照着一份过时的余额页面调账；
//   - 人工扣减不允许把现金余额扣成负数（ErrNegativeCashBalance）；
//   - 额外返回调账前的现金余额，供审计日志记录 before 快照。
func (s *Service) AdjustChecked(ctx context.Context, accountID int64, amount int64, refID string, expectedCash *int64) (*Receipt, int64, error) {
	if amount == 0 {
		return nil, 0, fmt.Errorf("wallet: adjust amount must not be zero")
	}
	if refID == "" {
		return nil, 0, fmt.Errorf("wallet: adjust requires a non-empty refID for audit purposes")
	}

	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, 0, fmt.Errorf("wallet: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 幂等：同一 (account, refID) 只能调一次账。事务级 advisory lock 让并发的
	// 重复提交串行化，再查一次是否已存在（ledger_entries 只追加、不能删，历史上
	// 已有的重复记录无法清理，所以用"检查 + 锁"而不是唯一索引，见 ErrDuplicateAdjustRef）。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('wallet.adjust:' || $1::bigint || ':' || $2::text, 0))`, accountID, refID); err != nil {
		return nil, 0, fmt.Errorf("wallet: adjust: acquire lock: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE account_id = $1 AND ref_type = 'admin' AND type = 'adjust' AND ref_id = $2)`,
		accountID, refID,
	).Scan(&exists); err != nil {
		return nil, 0, fmt.Errorf("wallet: adjust: check duplicate ref: %w", err)
	}
	if exists {
		return nil, 0, ErrDuplicateAdjustRef
	}

	var cashBefore int64
	if err := tx.QueryRow(ctx, `SELECT cash_balance FROM wallets WHERE account_id = $1 FOR UPDATE`, accountID).Scan(&cashBefore); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, fmt.Errorf("wallet: adjust: account %d has no wallet", accountID)
		}
		return nil, 0, fmt.Errorf("wallet: adjust: lock wallet: %w", err)
	}
	if expectedCash != nil && *expectedCash != cashBefore {
		return nil, 0, ErrBalanceChanged
	}
	if amount < 0 && cashBefore+amount < 0 {
		return nil, 0, ErrNegativeCashBalance
	}

	var cashAfter, bonusAfter int64
	if err := tx.QueryRow(ctx,
		`UPDATE wallets SET cash_balance = cash_balance + $2, updated_at = now()
		 WHERE account_id = $1
		 RETURNING cash_balance, bonus_balance`,
		accountID, amount,
	).Scan(&cashAfter, &bonusAfter); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, fmt.Errorf("wallet: adjust: account %d has no wallet", accountID)
		}
		return nil, 0, fmt.Errorf("wallet: adjust: update wallet: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id, journal_id)
		 VALUES ($1, 'adjust', $2, 'cash', $3, $4, 'admin', $5, $6)`,
		accountID, amount, cashAfter, bonusAfter, refID, newJournalID(),
	); err != nil {
		return nil, 0, fmt.Errorf("wallet: adjust: insert ledger entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("wallet: adjust: commit: %w", err)
	}

	return &Receipt{RequestID: refID, AccountID: accountID, ChargedAmount: -amount, CashAfter: cashAfter, BonusAfter: bonusAfter}, cashBefore, nil
}

// Reserve 原子地冻结 amount 微元。可用余额 = cash_balance + bonus_balance +
// accounts.credit_limit - frozen（技术方案 §6.5）。幂等：同一 request_id 重复调用
// 返回首次创建的 Hold，不会重复冻结。
func (s *Service) Reserve(ctx context.Context, requestID string, accountID int64, amount int64, ttl time.Duration) (*Hold, error) {
	if amount < 0 {
		return nil, fmt.Errorf("wallet: reserve amount must be >= 0, got %d", amount)
	}

	tx, err := store.BeginOrJoin(ctx, s.pool)
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
//
// vmName 是本次请求用的虚拟模型名，用来匹配 credit_grants.model_scope——赠款可以
// 限定"只能用在某些模型上"（比如活动赠送只给某个促销模型用），花赠款时必须尊重
// 这个限定，不能让一笔限定模型的赠款被花在别的模型请求上。vmName 传空字符串
// 时仍然只会匹配 model_scope 为 NULL（不限模型）的赠款，不会把"空字符串"当成
// 一个可以命中任何 scope 的通配符。
func (s *Service) Settle(ctx context.Context, requestID string, actualAmount int64, vmName string) (*Receipt, error) {
	if actualAmount < 0 {
		return nil, fmt.Errorf("wallet: settle amount must be >= 0, got %d", actualAmount)
	}

	tx, err := store.BeginOrJoin(ctx, s.pool)
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

	// 扣减顺序：赠送余额（按过期时间从近到远，即将过期的先花）→ 现金余额
	//（技术方案 §7.9.1 step 2；"免费额度"那一档由促销引擎在算出 actualAmount
	// 之前就已经处理掉了，见 internal/promotion）。
	bonusSpent, grantSpends, err := s.spendBonus(ctx, tx, accountID, actualAmount, vmName)
	if err != nil {
		return nil, fmt.Errorf("wallet: spend bonus balance: %w", err)
	}
	cashPortion := actualAmount - bonusSpent

	var cashAfter, bonusAfter int64
	if err := tx.QueryRow(ctx,
		`UPDATE wallets
		 SET cash_balance = cash_balance - $2, bonus_balance = bonus_balance - $3, frozen = frozen - $4, updated_at = now()
		 WHERE account_id = $1
		 RETURNING cash_balance, bonus_balance`,
		accountID, cashPortion, bonusSpent, heldAmount,
	).Scan(&cashAfter, &bonusAfter); err != nil {
		return nil, fmt.Errorf("wallet: update wallet balance: %w", err)
	}

	// 不依赖 ledger_entries 上的 UNIQUE(ref_type, ref_id, type, balance_kind, grant_id)
	// 做幂等去重：grant_id 为 NULL 时 Postgres 唯一索引把每个 NULL 视为互不相同，
	// ON CONFLICT 在这种场景下不会触发，起不到防重复的作用。真正的幂等保证来自上面
	// "reservations: held -> settled" 的原子状态迁移——同一 request_id 只有第一次
	// 调用能把状态从 held 改成 settled，重复调用会在前面的 switch 里短路返回，
	// 根本不会执行到这里，所以这里只需要普通 INSERT。
	//
	// 花了几笔赠款就写几条 bonus 流水（各自带自己的 grant_id），花现金再补一条——
	// 每条都记同样的 cash_after/bonus_after（这是这次结算完成后的最终余额，
	// 不是"扣这一笔之前"的快照；ledger_entries 的设计就是账户级别的 after 值，
	// 不是逐来源的中间态）。
	// 同一次结算写下的赠款流水与现金流水共享一个 journal_id（迁移 00022）。
	journal := newJournalID()
	for _, sp := range grantSpends {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, grant_id, cash_after, bonus_after, ref_type, ref_id, journal_id)
			 VALUES ($1, 'consume', $2, 'bonus', $3, $4, $5, 'request', $6, $7)`,
			accountID, -sp.amount, sp.grantID, cashAfter, bonusAfter, requestID, journal,
		); err != nil {
			return nil, fmt.Errorf("wallet: insert bonus ledger entry (grant=%d): %w", sp.grantID, err)
		}
	}
	if cashPortion > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (account_id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id, journal_id)
			 VALUES ($1, 'consume', $2, 'cash', $3, $4, 'request', $5, $6)`,
			accountID, -cashPortion, cashAfter, bonusAfter, requestID, journal,
		); err != nil {
			return nil, fmt.Errorf("wallet: insert cash ledger entry: %w", err)
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
	tx, err := store.BeginOrJoin(ctx, s.pool)
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
	rows, err := s.db(ctx).Query(ctx,
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

// grantSpend 记录一次结算里，从某个具体的 credit_grants 行扣了多少（技术方案
// §6.5：credit_grants 是逐笔赠款记录，一次消费可能同时花掉好几笔——比如注册赠送
// 还没花完，活动赠送又发了一笔，账户里同时存在两条 credit_grants）。
type grantSpend struct {
	grantID int64
	amount  int64
}

// spendBonus 按过期时间从近到远（NULL/永不过期的排最后）锁定并扣减 credit_grants，
// 最多扣 amount，返回实际扣掉的总额（赠款不够时会小于 amount，剩下的由调用方
// 转去扣现金）。SELECT ... FOR UPDATE 锁住候选行，防止同一账户的并发结算重复
// 花同一笔赠款——这和 wallet 其它方法要求的原子性是同一个道理。
func (s *Service) spendBonus(ctx context.Context, tx pgx.Tx, accountID int64, amount int64, vmName string) (spent int64, spends []grantSpend, err error) {
	if amount <= 0 {
		return 0, nil, nil
	}

	// model_scope IS NULL 的赠款不限模型，任何请求都能花；否则 vmName 必须落在
	// 数组里才行——NULLIF($2, '') 让空字符串和"没传模型名"一样，只能命中不限模型
	// 的赠款，不会被当成通配符匹配所有 scope。
	rows, err := tx.Query(ctx,
		`SELECT id, remaining FROM credit_grants
		 WHERE account_id = $1 AND remaining > 0 AND (expires_at IS NULL OR expires_at > now())
		   AND (model_scope IS NULL OR NULLIF($2, '') = ANY(model_scope))
		 ORDER BY expires_at ASC NULLS LAST, id ASC
		 FOR UPDATE`,
		accountID, vmName,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("wallet: query credit_grants: %w", err)
	}

	type candidate struct {
		id        int64
		remaining int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.remaining); err != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("wallet: scan credit_grant: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	need := amount
	for _, c := range candidates {
		if need <= 0 {
			break
		}
		take := c.remaining
		if take > need {
			take = need
		}
		spends = append(spends, grantSpend{grantID: c.id, amount: take})
		need -= take
		spent += take
	}

	for _, sp := range spends {
		if _, err := tx.Exec(ctx, `UPDATE credit_grants SET remaining = remaining - $2 WHERE id = $1`, sp.grantID, sp.amount); err != nil {
			return 0, nil, fmt.Errorf("wallet: update credit_grant %d: %w", sp.grantID, err)
		}
	}
	return spent, spends, nil
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

// db 返回 ctx 里的环境事务，没有时返回连接池（见 store.RunInTx）。
func (s *Service) db(ctx context.Context) store.Querier { return store.Q(ctx, s.pool) }

// newJournalID 生成一个 UUIDv4 字符串，作为同一笔业务（一次结算/调账/赠送）写下的
// 全部流水的 journal_id。
func newJournalID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("wallet: crypto/rand unavailable: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
