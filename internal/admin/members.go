package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// API Key 编辑与账户成员管理（B7）。此前 API Key 只能吊销，成员只能查看。

var (
	ErrUserNotFound       = errors.New("admin: no registered user with this email")
	ErrMemberNotFound     = errors.New("admin: user is not a member of this account")
	ErrLastAccountOwner   = errors.New("admin: an account must keep at least one owner")
	ErrAPIKeyRevokedFinal = errors.New("admin: revoked api keys cannot be modified")
)

var validMemberRoles = []string{"owner", "admin", "developer", "billing", "viewer"}

type UpdateAPIKeyInput struct {
	Name             *string    `json:"name"`
	Status           *string    `json:"status"`         // active / disabled（吊销走 /revoke，不可逆）
	AllowedModels    *[]string  `json:"allowed_models"` // 空数组 = 不限制
	RPMLimit         *int       `json:"rpm_limit"`      // <=0 清除
	TPMLimit         *int       `json:"tpm_limit"`
	ConcurrencyLimit *int       `json:"concurrency_limit"`
	BudgetLimitMicro *int64     `json:"budget_limit_micro"` // <=0 清除
	BudgetPeriod     *string    `json:"budget_period"`      // none / daily / monthly
	ExpiresAt        *time.Time `json:"expires_at"`
	ClearExpiresAt   bool       `json:"clear_expires_at"`
}

func (s *Service) UpdateAPIKey(ctx context.Context, id int64, in UpdateAPIKeyInput) (*Change, error) {
	var sets []setClause
	if err := nonEmpty("name", in.Name); err != nil {
		return nil, err
	}
	if in.Name != nil {
		sets = append(sets, setClause{"name", strings.TrimSpace(*in.Name)})
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "active", "disabled"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
	}
	if in.AllowedModels != nil {
		sets = append(sets, setClause{"allowed_models", nullIfEmptyStrings(*in.AllowedModels)})
	}
	if in.RPMLimit != nil {
		sets = append(sets, setClause{"rpm_limit", nullIfNonPositive(*in.RPMLimit)})
	}
	if in.TPMLimit != nil {
		sets = append(sets, setClause{"tpm_limit", nullIfNonPositive(*in.TPMLimit)})
	}
	if in.ConcurrencyLimit != nil {
		sets = append(sets, setClause{"concurrency_limit", nullIfNonPositive(*in.ConcurrencyLimit)})
	}
	if in.BudgetLimitMicro != nil {
		var v any
		if *in.BudgetLimitMicro > 0 {
			v = *in.BudgetLimitMicro
		}
		sets = append(sets, setClause{"budget_limit", v})
	}
	if in.BudgetPeriod != nil {
		if err := oneOf("budget_period", *in.BudgetPeriod, "none", "daily", "monthly"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"budget_period", *in.BudgetPeriod})
	}
	switch {
	case in.ClearExpiresAt:
		sets = append(sets, setClause{"expires_at", nil})
	case in.ExpiresAt != nil:
		if !in.ExpiresAt.After(time.Now()) {
			return nil, invalid("expires_at must be in the future")
		}
		sets = append(sets, setClause{"expires_at", *in.ExpiresAt})
	}
	return s.patchRow(ctx, "api_keys", id, sets, ErrAPIKeyNotFound, func(tx pgx.Tx, _ map[string]any) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM api_keys WHERE id = $1`, id).Scan(&status); err != nil {
			return fmt.Errorf("admin: read api_key status: %w", err)
		}
		if status == "revoked" {
			return ErrAPIKeyRevokedFinal
		}
		return nil
	})
}

// GetAPIKey 供 PATCH 之后返回最新对象。
func (s *Service) GetAPIKey(ctx context.Context, id int64) (*APIKeyListItem, error) {
	page, err := s.SearchAPIKeys(ctx, ListAPIKeysInput{KeyID: id, PageRequest: PageRequest{PageSize: 1}})
	if err != nil {
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, ErrAPIKeyNotFound
	}
	return &page.Data[0], nil
}

// ---------- 成员 ----------

// AddAccountMember 把一个已注册用户（按邮箱）加入账户。
func (s *Service) AddAccountMember(ctx context.Context, accountID int64, email, role string) (*AccountMember, error) {
	if !slices.Contains(validMemberRoles, role) {
		return nil, invalid("role must be one of %s", strings.Join(validMemberRoles, "/"))
	}
	var userID int64
	if err := s.db(ctx).QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, strings.TrimSpace(email)).Scan(&userID); err != nil {
		if isNoRows(err) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("admin: find user: %w", err)
	}
	if _, err := s.db(ctx).Exec(ctx,
		`INSERT INTO account_members (account_id, user_id, role) VALUES ($1, $2, $3)`, accountID, userID, role); err != nil {
		return nil, fmt.Errorf("admin: insert account_member: %w", err)
	}
	return s.getMember(ctx, accountID, userID)
}

func (s *Service) getMember(ctx context.Context, accountID, userID int64) (*AccountMember, error) {
	var m AccountMember
	err := s.db(ctx).QueryRow(ctx,
		`SELECT u.id, u.email::text, u.email_verified, m.role, m.created_at
		 FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = $1 AND m.user_id = $2`, accountID, userID,
	).Scan(&m.UserID, &m.Email, &m.EmailVerified, &m.Role, &m.CreatedAt)
	if isNoRows(err) {
		return nil, ErrMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query account_member: %w", err)
	}
	return &m, nil
}

// UpdateAccountMember 修改成员角色；RemoveAccountMember 移除成员。两者都保证
// 账户至少保留一个 owner（在账户行锁内检查，防并发把 owner 都降级/移除）。
func (s *Service) UpdateAccountMember(ctx context.Context, accountID, userID int64, role string) (before, after *AccountMember, err error) {
	if !slices.Contains(validMemberRoles, role) {
		return nil, nil, invalid("role must be one of %s", strings.Join(validMemberRoles, "/"))
	}
	if before, err = s.lockMemberForChange(ctx, accountID, userID); err != nil {
		return nil, nil, err
	}
	if before.Role == "owner" && role != "owner" {
		if err := s.ensureAnotherOwner(ctx, accountID, userID); err != nil {
			return nil, nil, err
		}
	}
	if _, err := s.db(ctx).Exec(ctx, `UPDATE account_members SET role = $3 WHERE account_id = $1 AND user_id = $2`, accountID, userID, role); err != nil {
		return nil, nil, fmt.Errorf("admin: update account_member: %w", err)
	}
	after, err = s.getMember(ctx, accountID, userID)
	return before, after, err
}

func (s *Service) RemoveAccountMember(ctx context.Context, accountID, userID int64) (*AccountMember, error) {
	before, err := s.lockMemberForChange(ctx, accountID, userID)
	if err != nil {
		return nil, err
	}
	if before.Role == "owner" {
		if err := s.ensureAnotherOwner(ctx, accountID, userID); err != nil {
			return nil, err
		}
	}
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM account_members WHERE account_id = $1 AND user_id = $2`, accountID, userID); err != nil {
		return nil, fmt.Errorf("admin: delete account_member: %w", err)
	}
	return before, nil
}

// lockMemberForChange 锁住账户行（串行化同一账户的成员变更）并返回成员当前状态。
// 需要在事务里调用（HTTP 层的 audited 保证）。
func (s *Service) lockMemberForChange(ctx context.Context, accountID, userID int64) (*AccountMember, error) {
	if _, err := s.db(ctx).Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, accountID); err != nil {
		return nil, fmt.Errorf("admin: lock account: %w", err)
	}
	return s.getMember(ctx, accountID, userID)
}

func (s *Service) ensureAnotherOwner(ctx context.Context, accountID, exceptUserID int64) error {
	var owners int
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM account_members WHERE account_id = $1 AND role = 'owner' AND user_id <> $2`, accountID, exceptUserID).Scan(&owners); err != nil {
		return fmt.Errorf("admin: count owners: %w", err)
	}
	if owners == 0 {
		return ErrLastAccountOwner
	}
	return nil
}
