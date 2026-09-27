package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 账户检索、资金流水、赠送余额、全局 API Key 检索（运营后台接口方案 §4）。

type AccountSummary struct {
	ID                int64      `json:"id"`
	Type              string     `json:"type"`
	Name              string     `json:"name"`
	Status            string     `json:"status"`
	Tier              string     `json:"tier"`
	CreditLimit       int64      `json:"credit_limit_micro"`
	CreatedAt         time.Time  `json:"created_at"`
	OwnerEmail        *string    `json:"owner_email"`
	CashBalanceMicro  int64      `json:"cash_balance_micro"`
	BonusBalanceMicro int64      `json:"bonus_balance_micro"`
	FrozenMicro       int64      `json:"frozen_micro"`
	ActiveKeyCount    int        `json:"active_key_count"`
	LastActiveAt      *time.Time `json:"last_active_at"`
}

type ListAccountsInput struct {
	Q, Status, Tier, Type, Sort string
	PageRequest
}

// ListAccounts：q 为纯数字时匹配账户 ID；含 @ 时匹配 owner 用户邮箱；否则匹配账户名。
// last_active_at 取该账户 API Key 的 max(last_used_at)，不扫 request_logs。
func (s *Service) ListAccounts(ctx context.Context, in ListAccountsInput) (*Page[AccountSummary], error) {
	order, err := orderBy(in.Sort, "-created_at", map[string]string{
		"created_at": "a.created_at", "id": "a.id", "name": "a.name",
		"cash_balance":   "w.cash_balance",
		"last_active_at": "(SELECT max(k.last_used_at) FROM api_keys k WHERE k.account_id = a.id)",
	})
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(in.Q)
	var idQ int64
	var emailQ, nameQ string
	switch {
	case q == "":
	case isDigits(q):
		idQ, _ = strconv.ParseInt(q, 10, 64)
	case strings.Contains(q, "@"):
		emailQ = likePattern(q)
	default:
		nameQ = likePattern(q)
	}
	where := `WHERE ($1 = 0 OR a.id = $1)
	  AND ($2 = '' OR EXISTS (SELECT 1 FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = a.id AND u.email::text ILIKE $2))
	  AND ($3 = '' OR a.name ILIKE $3)
	  AND ($4 = '' OR a.status = $4) AND ($5 = '' OR a.tier = $5) AND ($6 = '' OR a.type = $6)`
	args := []any{idQ, emailQ, nameQ, in.Status, in.Tier, in.Type}
	pr := in.PageRequest.normalize()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts a `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count accounts: %w", err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT a.id, a.type, a.name, a.status, a.tier, a.credit_limit, a.created_at,
		   (SELECT u.email::text FROM account_members m JOIN users u ON u.id = m.user_id
		      WHERE m.account_id = a.id AND m.role = 'owner' ORDER BY m.created_at LIMIT 1),
		   COALESCE(w.cash_balance, 0), COALESCE(w.bonus_balance, 0), COALESCE(w.frozen, 0),
		   (SELECT count(*) FROM api_keys k WHERE k.account_id = a.id AND k.status = 'active'),
		   (SELECT max(k.last_used_at) FROM api_keys k WHERE k.account_id = a.id)
		 FROM accounts a LEFT JOIN wallets w ON w.account_id = a.id `+where+
			` ORDER BY `+order+` NULLS LAST, a.id DESC LIMIT $7 OFFSET $8`,
		append(args, pr.PageSize, pr.offset())...)
	if err != nil {
		return nil, fmt.Errorf("admin: query accounts: %w", err)
	}
	defer rows.Close()
	out := []AccountSummary{}
	for rows.Next() {
		var a AccountSummary
		if err := rows.Scan(&a.ID, &a.Type, &a.Name, &a.Status, &a.Tier, &a.CreditLimit, &a.CreatedAt, &a.OwnerEmail,
			&a.CashBalanceMicro, &a.BonusBalanceMicro, &a.FrozenMicro, &a.ActiveKeyCount, &a.LastActiveAt); err != nil {
			return nil, fmt.Errorf("admin: scan account: %w", err)
		}
		out = append(out, a)
	}
	return &Page[AccountSummary]{Data: out, Total: total, Page: pr.Page, PageSize: pr.PageSize}, rows.Err()
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

type AccountMember struct {
	UserID        int64     `json:"user_id"`
	Email         *string   `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	Role          string    `json:"role"`
	CreatedAt     time.Time `json:"created_at"`
}

type GrantsSummary struct {
	Count            int        `json:"count"`
	RemainingMicro   int64      `json:"remaining_micro"`
	NearestExpiresAt *time.Time `json:"nearest_expires_at"`
}

// AccountExtras 是账户详情在 {account, wallet} 之外补充的信息（接口方案 §4.2）。
type AccountExtras struct {
	Members             []AccountMember `json:"members"`
	ActiveGrantsSummary GrantsSummary   `json:"active_grants_summary"`
}

func (s *Service) GetAccountExtras(ctx context.Context, accountID int64) (*AccountExtras, error) {
	out := &AccountExtras{Members: []AccountMember{}}
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.email::text, u.email_verified, m.role, m.created_at
		 FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = $1 ORDER BY m.created_at`, accountID)
	if err != nil {
		return nil, fmt.Errorf("admin: query account_members: %w", err)
	}
	for rows.Next() {
		var m AccountMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.EmailVerified, &m.Role, &m.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("admin: scan account_member: %w", err)
		}
		out.Members = append(out.Members, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(remaining), 0), min(expires_at)
		 FROM credit_grants WHERE account_id = $1 AND remaining > 0 AND (expires_at IS NULL OR expires_at > now())`, accountID,
	).Scan(&out.ActiveGrantsSummary.Count, &out.ActiveGrantsSummary.RemainingMicro, &out.ActiveGrantsSummary.NearestExpiresAt); err != nil {
		return nil, fmt.Errorf("admin: query grants summary: %w", err)
	}
	return out, nil
}

// ---------- 资金流水 ----------

type LedgerEntry struct {
	ID              int64     `json:"id"`
	Type            string    `json:"type"`
	AmountMicro     int64     `json:"amount_micro"`
	BalanceKind     string    `json:"balance_kind"`
	CashAfterMicro  int64     `json:"cash_after_micro"`
	BonusAfterMicro int64     `json:"bonus_after_micro"`
	RefType         string    `json:"ref_type"`
	RefID           string    `json:"ref_id"`
	GrantID         *int64    `json:"grant_id"`
	CreatedAt       time.Time `json:"created_at"`
}

type ListLedgerInput struct {
	AccountID         int64
	Type, BalanceKind string
	From, To          time.Time
	Before            string
	Limit             int // 默认 50，最大 100
}

var ErrInvalidCursor = errors.New("admin: invalid pagination cursor")

// ListLedger 按 (created_at, id) 倒序游标分页，走 idx_ledger_entries_account。
func (s *Service) ListLedger(ctx context.Context, in ListLedgerInput) ([]LedgerEntry, string, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var beforeAt time.Time
	var beforeID int64
	if in.Before != "" {
		var err error
		if beforeAt, beforeID, err = decodeIDCursor(in.Before); err != nil {
			return nil, "", err
		}
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, type, amount, balance_kind, cash_after, bonus_after, ref_type, ref_id, grant_id, created_at
		 FROM ledger_entries
		 WHERE account_id = $1 AND ($2 = '' OR type = $2) AND ($3 = '' OR balance_kind = $3)
		   AND ($4::timestamptz IS NULL OR created_at >= $4) AND ($5::timestamptz IS NULL OR created_at < $5)
		   AND ($6::timestamptz IS NULL OR (created_at, id) < ($6, $7))
		 ORDER BY created_at DESC, id DESC LIMIT $8`,
		in.AccountID, in.Type, in.BalanceKind, nullTime(in.From), nullTime(in.To), nullTime(beforeAt), beforeID, limit)
	if err != nil {
		return nil, "", fmt.Errorf("admin: query ledger_entries: %w", err)
	}
	defer rows.Close()
	out := make([]LedgerEntry, 0, limit)
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.Type, &e.AmountMicro, &e.BalanceKind, &e.CashAfterMicro, &e.BonusAfterMicro, &e.RefType, &e.RefID, &e.GrantID, &e.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("admin: scan ledger_entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = encodeIDCursor(last.CreatedAt, last.ID)
	}
	return out, next, nil
}

func encodeIDCursor(t time.Time, id int64) string {
	return t.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)
}

func decodeIDCursor(cursor string) (time.Time, int64, error) {
	at, idStr, found := strings.Cut(cursor, "|")
	if !found {
		return time.Time{}, 0, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, 0, ErrInvalidCursor
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return time.Time{}, 0, ErrInvalidCursor
	}
	return t, id, nil
}

// ---------- 赠送余额 ----------

type CreditGrantInfo struct {
	ID             int64      `json:"id"`
	Source         string     `json:"source"`
	PromotionID    *int64     `json:"promotion_id"`
	AmountMicro    int64      `json:"amount_micro"`
	RemainingMicro int64      `json:"remaining_micro"`
	ModelScope     []string   `json:"model_scope"`
	ExpiresAt      *time.Time `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ListCreditGrants：activeOnly=true 时只返回未用完且未过期的赠款。
func (s *Service) ListCreditGrants(ctx context.Context, accountID int64, activeOnly bool) ([]CreditGrantInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, source, promotion_id, amount, remaining, model_scope, expires_at, created_at
		 FROM credit_grants
		 WHERE account_id = $1 AND (NOT $2 OR (remaining > 0 AND (expires_at IS NULL OR expires_at > now())))
		 ORDER BY created_at DESC LIMIT 200`, accountID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("admin: query credit_grants: %w", err)
	}
	defer rows.Close()
	out := []CreditGrantInfo{}
	for rows.Next() {
		var g CreditGrantInfo
		if err := rows.Scan(&g.ID, &g.Source, &g.PromotionID, &g.AmountMicro, &g.RemainingMicro, &g.ModelScope, &g.ExpiresAt, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan credit_grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---------- 全局 API Key 检索 ----------

type APIKeyListItem struct {
	APIKey
	AccountName      string     `json:"account_name"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	ExpiresAt        *time.Time `json:"expires_at"`
	BudgetLimitMicro *int64     `json:"budget_limit_micro"`
	BudgetPeriod     *string    `json:"budget_period"`
}

type ListAPIKeysInput struct {
	Q         string
	AccountID int64
	Status    string
	PageRequest
}

// SearchAPIKeys：q 匹配 Key 名称或展示前缀——运营拿到用户发来的 sk-uft-xxxx
// 前缀就能定位是哪把 Key、属于哪个账户。
func (s *Service) SearchAPIKeys(ctx context.Context, in ListAPIKeysInput) (*Page[APIKeyListItem], error) {
	q := strings.TrimSpace(in.Q)
	where := `WHERE ($1 = '' OR k.name ILIKE $2 OR k.display_prefix ILIKE $2 OR $1 LIKE k.display_prefix || '%')
	  AND ($3 = 0 OR k.account_id = $3) AND ($4 = '' OR k.status = $4)`
	args := []any{q, likePattern(q), in.AccountID, in.Status}
	pr := in.PageRequest.normalize()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys k `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count api_keys: %w", err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT k.id, k.account_id, k.name, k.display_prefix, k.status, k.allowed_models, k.rpm_limit, k.tpm_limit, k.concurrency_limit, k.created_at,
		   a.name, k.last_used_at, k.expires_at, k.budget_limit, k.budget_period
		 FROM api_keys k JOIN accounts a ON a.id = k.account_id `+where+` ORDER BY k.id DESC LIMIT $5 OFFSET $6`,
		append(args, pr.PageSize, pr.offset())...)
	if err != nil {
		return nil, fmt.Errorf("admin: query api_keys: %w", err)
	}
	defer rows.Close()
	out := []APIKeyListItem{}
	for rows.Next() {
		var k APIKeyListItem
		if err := rows.Scan(&k.ID, &k.AccountID, &k.Name, &k.DisplayPrefix, &k.Status, &k.AllowedModels, &k.RPMLimit, &k.TPMLimit, &k.ConcurrencyLimit, &k.CreatedAt,
			&k.AccountName, &k.LastUsedAt, &k.ExpiresAt, &k.BudgetLimitMicro, &k.BudgetPeriod); err != nil {
			return nil, fmt.Errorf("admin: scan api_key: %w", err)
		}
		out = append(out, k)
	}
	return &Page[APIKeyListItem]{Data: out, Total: total, Page: pr.Page, PageSize: pr.PageSize}, rows.Err()
}
