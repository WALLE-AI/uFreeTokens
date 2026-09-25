package admin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var validAccountTypes = map[string]bool{"personal": true, "organization": true}

type Account struct {
	ID          int64
	Type        string
	Name        string
	Status      string
	Tier        string
	CreditLimit int64
	CreatedAt   time.Time
}

type WalletSummary struct {
	CashBalance  int64
	BonusBalance int64
	Frozen       int64
}

type CreateAccountInput struct {
	Type        string // personal / organization
	Name        string
	Tier        string // 空则默认 "free"
	CreditLimit int64  // 企业授信额度（微元），可为 0
}

// CreateAccount 建一个账户并原子地给它初始化一个空钱包（技术方案 §6.1、§6.2）。
// 账户和钱包在这里必须一起创建，不允许出现"有账户没钱包"的中间状态——否则
// 这个账户的第一次计费请求会因为 wallet.Reserve 找不到钱包行而莫名其妙地失败。
func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (*Account, error) {
	if !validAccountTypes[in.Type] {
		return nil, fmt.Errorf("admin: invalid account type %q, want personal or organization", in.Type)
	}
	if in.Name == "" {
		return nil, errors.New("admin: account name is required")
	}
	if in.Tier == "" {
		in.Tier = "free"
	}
	if in.CreditLimit < 0 {
		return nil, errors.New("admin: credit_limit must not be negative")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	acct := &Account{Type: in.Type, Name: in.Name, Status: "active", Tier: in.Tier, CreditLimit: in.CreditLimit}
	if err := tx.QueryRow(ctx,
		`INSERT INTO accounts (type, name, status, tier, credit_limit) VALUES ($1, $2, 'active', $3, $4)
		 RETURNING id, created_at`,
		in.Type, in.Name, in.Tier, in.CreditLimit,
	).Scan(&acct.ID, &acct.CreatedAt); err != nil {
		return nil, fmt.Errorf("admin: insert account: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO wallets (account_id, cash_balance, bonus_balance, frozen) VALUES ($1, 0, 0, 0)`,
		acct.ID,
	); err != nil {
		return nil, fmt.Errorf("admin: create wallet: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("admin: commit: %w", err)
	}
	return acct, nil
}

var ErrAccountNotFound = errors.New("admin: account not found")

// GetAccount 返回账户信息与钱包余额快照。
func (s *Service) GetAccount(ctx context.Context, accountID int64) (*Account, *WalletSummary, error) {
	acct := &Account{ID: accountID}
	wallet := &WalletSummary{}
	err := s.pool.QueryRow(ctx,
		`SELECT a.type, a.name, a.status, a.tier, a.credit_limit, a.created_at,
		        w.cash_balance, w.bonus_balance, w.frozen
		 FROM accounts a JOIN wallets w ON w.account_id = a.id
		 WHERE a.id = $1`,
		accountID,
	).Scan(&acct.Type, &acct.Name, &acct.Status, &acct.Tier, &acct.CreditLimit, &acct.CreatedAt,
		&wallet.CashBalance, &wallet.BonusBalance, &wallet.Frozen)
	if err != nil {
		if isNoRows(err) {
			return nil, nil, ErrAccountNotFound
		}
		return nil, nil, fmt.Errorf("admin: get account: %w", err)
	}
	return acct, wallet, nil
}
