package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WALLE-AI/uFreeTokens/internal/store"

	"github.com/jackc/pgx/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

var validAccountTypes = map[string]bool{"personal": true, "organization": true}

type Account struct {
	ID          int64     `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Tier        string    `json:"tier"`
	CreditLimit int64     `json:"credit_limit_micro"`
	CreatedAt   time.Time `json:"created_at"`
}

type WalletSummary struct {
	CashBalance  int64 `json:"cash_balance_micro"`
	BonusBalance int64 `json:"bonus_balance_micro"`
	Frozen       int64 `json:"frozen_micro"`
}

type CreateAccountInput struct {
	Type        string `json:"type"` // personal / organization
	Name        string `json:"name"`
	Tier        string `json:"tier"`               // 空则默认 "free"
	CreditLimit int64  `json:"credit_limit_micro"` // 企业授信额度（微元），可为 0
}

// CreateAccount 建一个账户并原子地给它初始化一个空钱包（技术方案 §6.1、§6.2）。
// 账户和钱包在这里必须一起创建，不允许出现"有账户没钱包"的中间状态——否则
// 这个账户的第一次计费请求会因为 wallet.Reserve 找不到钱包行而莫名其妙地失败。
func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (*Account, error) {
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("admin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	acct, err := CreateAccountTx(ctx, tx, in)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("admin: commit: %w", err)
	}
	return acct, nil
}

// CreateAccountTx 是 CreateAccount 的事务内核，导出给 internal/console 的
// 用户注册流程复用：注册需要在同一个数据库事务里完成"建账户 + 建钱包 + 建
// user + 建 owner 身份的 account_member"，缺一步都不该提交——console 包
// 自己 Begin() 一个 tx，调这个函数完成前两步，再在同一个 tx 里插入
// users/account_members，最后自己 Commit。
func CreateAccountTx(ctx context.Context, tx pgx.Tx, in CreateAccountInput) (*Account, error) {
	if !validAccountTypes[in.Type] {
		return nil, fmt.Errorf("admin: invalid account type %q, want personal or organization", in.Type)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 128 {
		return nil, errors.New("admin: account name is required (at most 128 characters)")
	}
	if in.Tier == "" {
		in.Tier = "free"
	}
	if !slices.Contains(validTiers, in.Tier) {
		return nil, invalid("tier must be one of %s, got %q", strings.Join(validTiers, "/"), in.Tier)
	}
	if in.CreditLimit < 0 {
		return nil, errors.New("admin: credit_limit must not be negative")
	}

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

	return acct, nil
}

var ErrAccountNotFound = errors.New("admin: account not found")

// GetAccount 返回账户信息与钱包余额快照。
func (s *Service) GetAccount(ctx context.Context, accountID int64) (*Account, *WalletSummary, error) {
	acct := &Account{ID: accountID}
	wallet := &WalletSummary{}
	err := s.db(ctx).QueryRow(ctx,
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

// GrantCreditInput 描述一次赠送余额的发放（技术方案 §7.10 credit_grant）。
type GrantCreditInput struct {
	AccountID  int64
	Source     string // signup / promotion / compensation / invite
	Amount     int64  // 微元，必须 > 0
	ExpiresAt  *time.Time
	ModelScope []string // 空 = 不限模型
	RefID      string   // 幂等键（工单号/前端生成的 UUID），必填：同一账户同一来源重复提交只发放一次
}

type GrantedCredit struct {
	GrantID    int64 `json:"grant_id"`
	BonusAfter int64 `json:"bonus_after_micro"`
}

// GrantCredit 给账户发一笔赠送余额——包装 wallet.Grant，是目前 credit_grant 类
// 促销唯一的发放入口（还没有自动触发的注册赠送/活动赠送流程，都得靠这个接口
// 手工/由外部系统调用）。
func (s *Service) GrantCredit(ctx context.Context, in GrantCreditInput) (*GrantedCredit, error) {
	refID := strings.TrimSpace(in.RefID)
	if refID == "" {
		// 不再自动生成：自动生成的 ref_id 每次都不同，重试会重复发放。
		return nil, invalid("ref_id is required (use a ticket number or a client-generated UUID so retries are idempotent)")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return nil, invalid("expires_at must be in the future")
	}
	grantID, bonusAfter, err := s.wallet.Grant(ctx, wallet.GrantInput{
		AccountID: in.AccountID, Source: in.Source, Amount: in.Amount,
		ExpiresAt: in.ExpiresAt, ModelScope: in.ModelScope, RefID: refID,
	})
	if err != nil {
		return nil, err
	}
	return &GrantedCredit{GrantID: grantID, BonusAfter: bonusAfter}, nil
}
