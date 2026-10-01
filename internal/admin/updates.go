package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// 编辑 / 状态变更（运营后台接口方案 §2）。价格不在这里改：价格版本化要求历史
// 不可篡改，改价继续走 SetSellPrice / SetCostPrice 追加新版本。
//
// 所有 Update* 都返回 Change：本次实际改动的字段在变更前/后的值，handler 直接
// 写进审计日志的 before/after。未传的字段（指针为 nil）不动。

type Change struct {
	Before map[string]any
	After  map[string]any
	// Version 是更新后的行版本（乐观锁），供响应头 ETag 使用。
	Version int
}

type expectedVersionKey struct{}

// WithExpectedVersion 声明调用方基于哪个版本做的修改（HTTP If-Match）：patchRow
// 在行锁内核对，不一致返回 ErrVersionConflict，避免两个运营互相覆盖。
func WithExpectedVersion(ctx context.Context, version int) context.Context {
	return context.WithValue(ctx, expectedVersionKey{}, version)
}

// RowVersion 返回可编辑实体当前的版本号（详情接口的 ETag）。table 只能是
// patchRow 支持的表名，由调用方传常量。
func (s *Service) RowVersion(ctx context.Context, table string, id int64) (int, error) {
	if !versionedTables[table] {
		return 0, fmt.Errorf("admin: %s is not a versioned table", table)
	}
	var v int
	if err := s.db(ctx).QueryRow(ctx, fmt.Sprintf(`SELECT version FROM %s WHERE id = $1`, table), id).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// versionedTables 是带 version 列（迁移 00018）、通过 patchRow 编辑的表。
var versionedTables = map[string]bool{
	"providers": true, "provider_accounts": true, "provider_keys": true, "virtual_models": true,
	"channels": true, "accounts": true, "price_sources": true, "api_keys": true, "benchmarks": true,
}

type setClause struct {
	col string
	val any
}

// patchRow 在一个事务里：锁住目标行、读出将要修改的列的旧值、执行 UPDATE。
// extraCheck 可在锁内做额外校验（例如"是否为最后一个 active 渠道"），
// 收到的是这些列的旧值。notFound 是行不存在时返回的哨兵错误。
func (s *Service) patchRow(ctx context.Context, table string, id int64, sets []setClause, notFound error,
	extraCheck func(tx pgx.Tx, before map[string]any) error) (*Change, error) {
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	if !versionedTables[table] {
		return nil, fmt.Errorf("admin: %s is not a versioned table", table)
	}
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("admin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	cols := make([]string, len(sets))
	for i, sc := range sets {
		cols[i] = sc.col
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT version, %s FROM %s WHERE id = $1 FOR UPDATE`, strings.Join(cols, ", "), table), id)
	if err != nil {
		return nil, fmt.Errorf("admin: lock %s: %w", table, err)
	}
	var old []any
	if rows.Next() {
		if old, err = rows.Values(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("admin: read %s: %w", table, err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: read %s: %w", table, err)
	}
	if old == nil {
		return nil, notFound
	}
	current := int(old[0].(int32))
	if expected, ok := ctx.Value(expectedVersionKey{}).(int); ok && expected != current {
		return nil, ErrVersionConflict
	}
	ch := &Change{Before: map[string]any{}, After: map[string]any{}, Version: current + 1}
	for i, sc := range sets {
		ch.Before[sc.col] = old[i+1]
		ch.After[sc.col] = sc.val
	}
	if extraCheck != nil {
		if err := extraCheck(tx, ch.Before); err != nil {
			return nil, err
		}
	}

	assign := make([]string, len(sets))
	args := []any{id}
	for i, sc := range sets {
		args = append(args, sc.val)
		assign[i] = fmt.Sprintf("%s = $%d", sc.col, len(args))
	}
	assign = append(assign, "version = version + 1", "updated_at = now()")
	if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE id = $1`, table, strings.Join(assign, ", ")), args...); err != nil {
		return nil, fmt.Errorf("admin: update %s: %w", table, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("admin: commit: %w", err)
	}
	return ch, nil
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFilterOrValue, fmt.Sprintf(format, a...))
}

func oneOf(field, v string, allowed ...string) error {
	if !slices.Contains(allowed, v) {
		return invalid("%s must be one of %s, got %q", field, strings.Join(allowed, "/"), v)
	}
	return nil
}

// validateEnum 校验可选的枚举参数：空字符串表示不限制。
func validateEnum(field, v string, allowed ...string) error {
	if v == "" {
		return nil
	}
	return oneOf(field, v, allowed...)
}

func nonEmpty(field string, v *string) error {
	if v != nil && strings.TrimSpace(*v) == "" {
		return invalid("%s must not be empty", field)
	}
	return nil
}

// nullIfEmpty：数组类"白名单"字段传空数组表示"不限制"，存 NULL，与创建时的语义一致。
func nullIfEmptyStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func nullIfEmptyInt64s(v []int64) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

// nullIfNonPositive：限流类字段传 0 或负数表示清除限制（存 NULL）。
func nullIfNonPositive(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

// ---------- providers ----------

type UpdateProviderInput struct {
	Name         *string   `json:"name"`
	Status       *string   `json:"status"`
	AllowedHosts *[]string `json:"allowed_hosts"` // 空数组 = 不限域名
}

func (s *Service) UpdateProvider(ctx context.Context, id int64, in UpdateProviderInput) (*Change, error) {
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
	if in.AllowedHosts != nil {
		hosts, err := normalizeHosts(*in.AllowedHosts)
		if err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"allowed_hosts", hosts})
	}
	return s.patchRow(ctx, "providers", id, sets, ErrProviderNotFound, nil)
}

// ActiveChannelCountForProvider 用于停用供应商时提示"还有多少 active 渠道受影响"——
// 停用供应商不级联停用下属账号/渠道（接口方案 §2）。
func (s *Service) ActiveChannelCountForProvider(ctx context.Context, providerID int64) (int, error) {
	var n int
	err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id
		 WHERE pa.provider_id = $1 AND c.status = 'active'`, providerID).Scan(&n)
	return n, err
}

// ---------- provider accounts ----------

type UpdateProviderAccountInput struct {
	Name           *string          `json:"name"`
	BaseURL        *string          `json:"base_url"`
	Region         *string          `json:"region"` // 空字符串清除
	CostMultiplier *decimal.Decimal `json:"cost_multiplier"`
	Status         *string          `json:"status"`
}

func (s *Service) UpdateProviderAccount(ctx context.Context, id int64, in UpdateProviderAccountInput) (*Change, error) {
	var sets []setClause
	if err := nonEmpty("name", in.Name); err != nil {
		return nil, err
	}
	if in.Name != nil {
		sets = append(sets, setClause{"name", strings.TrimSpace(*in.Name)})
	}
	if err := nonEmpty("base_url", in.BaseURL); err != nil {
		return nil, err
	}
	if in.BaseURL != nil {
		var providerID int64
		if err := s.db(ctx).QueryRow(ctx, `SELECT provider_id FROM provider_accounts WHERE id = $1`, id).Scan(&providerID); err != nil {
			if isNoRows(err) {
				return nil, ErrProviderAccountNotFound
			}
			return nil, fmt.Errorf("admin: load provider_account: %w", err)
		}
		u, err := s.validateUpstreamURL(ctx, s.db(ctx), providerID, *in.BaseURL)
		if err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"base_url", u}, setClause{"base_url_changed_at", time.Now()})
	}
	if in.Region != nil {
		var region any
		if r := strings.TrimSpace(*in.Region); r != "" {
			region = r
		}
		sets = append(sets, setClause{"region", region})
	}
	if in.CostMultiplier != nil {
		if !in.CostMultiplier.IsPositive() {
			return nil, invalid("cost_multiplier must be > 0")
		}
		sets = append(sets, setClause{"cost_multiplier", *in.CostMultiplier})
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "active", "disabled"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
	}
	return s.patchRow(ctx, "provider_accounts", id, sets, ErrProviderAccountNotFound, nil)
}

// ---------- provider keys ----------

type UpdateProviderKeyInput struct {
	Weight           *int    `json:"weight"`
	Status           *string `json:"status"` // 只允许 active / disabled；exhausted 由系统设置
	DisabledReason   *string `json:"disabled_reason"`
	RPMLimit         *int    `json:"rpm_limit"` // <=0 清除限制
	TPMLimit         *int    `json:"tpm_limit"`
	ConcurrencyLimit *int    `json:"concurrency_limit"`
}

func (s *Service) UpdateProviderKey(ctx context.Context, id int64, in UpdateProviderKeyInput) (*Change, error) {
	var sets []setClause
	if in.Weight != nil {
		if *in.Weight <= 0 {
			return nil, invalid("weight must be > 0")
		}
		sets = append(sets, setClause{"weight", *in.Weight})
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "active", "disabled"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
		if *in.Status == "active" && in.DisabledReason == nil {
			sets = append(sets, setClause{"disabled_reason", nil})
		}
	}
	if in.DisabledReason != nil {
		var reason any
		if r := strings.TrimSpace(*in.DisabledReason); r != "" {
			reason = r
		}
		sets = append(sets, setClause{"disabled_reason", reason})
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
	return s.patchRow(ctx, "provider_keys", id, sets, ErrProviderKeyNotFound, revokedKeyGuard(ctx, id))
}

// revokedKeyGuard：已吊销的上游密钥不能再改（吊销不可逆）。
func revokedKeyGuard(ctx context.Context, id int64) func(pgx.Tx, map[string]any) error {
	return func(tx pgx.Tx, _ map[string]any) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM provider_keys WHERE id = $1`, id).Scan(&status); err != nil {
			return fmt.Errorf("admin: read provider_key status: %w", err)
		}
		if status == "revoked" {
			return ErrProviderKeyRevoked
		}
		return nil
	}
}

// RevokeProviderKey 吊销一把上游密钥（不可逆）。注意：路由层的密钥冷却记录在
// Redis 里，由 gateway 维护，admin 进程不持有 Redis 连接，这里不清理——吊销后
// catalog 快照刷新（默认 10 秒）即不再选中这把 Key，冷却记录自然过期。
func (s *Service) RevokeProviderKey(ctx context.Context, id int64) (*Change, error) {
	return s.patchRow(ctx, "provider_keys", id, []setClause{{"status", "revoked"}}, ErrProviderKeyNotFound, revokedKeyGuard(ctx, id))
}

// ---------- virtual models ----------

type UpdateVirtualModelInput struct {
	Status        *string   `json:"status"`
	VisibleTiers  *[]string `json:"visible_tiers"`
	Capabilities  *[]string `json:"capabilities"`
	ContextWindow *int      `json:"context_window"`
	MaxOutput     *int      `json:"max_output"`
	Aliases       *[]string `json:"aliases"`
}

func (s *Service) UpdateVirtualModel(ctx context.Context, id int64, in UpdateVirtualModelInput) (*Change, error) {
	var sets []setClause
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "active", "hidden", "deprecated"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
	}
	if in.VisibleTiers != nil {
		if len(*in.VisibleTiers) == 0 {
			return nil, invalid("visible_tiers must not be empty (use status=hidden to hide a model)")
		}
		for _, t := range *in.VisibleTiers {
			if err := oneOf("visible_tiers", t, "free", "pro", "enterprise"); err != nil {
				return nil, err
			}
		}
		sets = append(sets, setClause{"visible_tiers", *in.VisibleTiers})
	}
	if in.Capabilities != nil {
		sets = append(sets, setClause{"capabilities", nonNilStrings(*in.Capabilities)})
	}
	if in.ContextWindow != nil {
		if *in.ContextWindow <= 0 {
			return nil, invalid("context_window must be > 0")
		}
		sets = append(sets, setClause{"context_window", *in.ContextWindow})
	}
	if in.MaxOutput != nil {
		if *in.MaxOutput <= 0 {
			return nil, invalid("max_output must be > 0")
		}
		sets = append(sets, setClause{"max_output", *in.MaxOutput})
	}
	if in.Aliases != nil {
		sets = append(sets, setClause{"aliases", nonNilStrings(*in.Aliases)})
	}
	return s.patchRow(ctx, "virtual_models", id, sets, ErrVirtualModelNotFound, nil)
}

// ---------- channels ----------

type UpdateChannelInput struct {
	Priority          *int             `json:"priority"`
	Weight            *int             `json:"weight"`
	Status            *string          `json:"status"`
	AllowedTiers      *[]string        `json:"allowed_tiers"`       // 空数组 = 不限制
	AllowedAccountIDs *[]int64         `json:"allowed_account_ids"` // 空数组 = 公共渠道
	ParamOverrides    *json.RawMessage `json:"param_overrides"`
	// Force 允许停用某虚拟模型的最后一个 active 渠道（否则返回 ErrLastActiveChannel）。
	Force bool `json:"force"`
}

func (s *Service) UpdateChannel(ctx context.Context, id int64, in UpdateChannelInput) (*Change, error) {
	var sets []setClause
	if in.Priority != nil {
		sets = append(sets, setClause{"priority", *in.Priority})
	}
	if in.Weight != nil {
		if *in.Weight < 0 {
			return nil, invalid("weight must be >= 0")
		}
		sets = append(sets, setClause{"weight", *in.Weight})
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "active", "disabled"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
	}
	if in.AllowedTiers != nil {
		for _, t := range *in.AllowedTiers {
			if err := oneOf("allowed_tiers", t, "free", "pro", "enterprise"); err != nil {
				return nil, err
			}
		}
		sets = append(sets, setClause{"allowed_tiers", nullIfEmptyStrings(*in.AllowedTiers)})
	}
	if in.AllowedAccountIDs != nil {
		sets = append(sets, setClause{"allowed_account_ids", nullIfEmptyInt64s(*in.AllowedAccountIDs)})
	}
	if in.ParamOverrides != nil {
		var obj map[string]any
		if err := json.Unmarshal(*in.ParamOverrides, &obj); err != nil || obj == nil {
			return nil, invalid("param_overrides must be a JSON object")
		}
		sets = append(sets, setClause{"param_overrides", obj})
	}
	var guard func(pgx.Tx, map[string]any) error
	if in.Status != nil && *in.Status == "disabled" && !in.Force {
		guard = func(tx pgx.Tx, _ map[string]any) error {
			// 先锁住所属虚拟模型行：同一模型下并发停用不同渠道的请求在这里串行，
			// 后到的那个会看到前一个已提交的停用，不会两个都判断"还有别的活跃渠道"。
			if _, err := tx.Exec(ctx,
				`SELECT 1 FROM virtual_models WHERE id = (SELECT virtual_model_id FROM channels WHERE id = $1) FOR UPDATE`, id); err != nil {
				return fmt.Errorf("admin: lock virtual model: %w", err)
			}
			var others int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM channels WHERE status = 'active' AND id <> $1
				   AND virtual_model_id = (SELECT virtual_model_id FROM channels WHERE id = $1)`, id).Scan(&others); err != nil {
				return fmt.Errorf("admin: count sibling channels: %w", err)
			}
			if others == 0 {
				return ErrLastActiveChannel
			}
			return nil
		}
	}
	return s.patchRow(ctx, "channels", id, sets, ErrChannelNotFound, guard)
}

// ---------- accounts ----------

type UpdateAccountInput struct {
	Name        *string `json:"name"`
	Status      *string `json:"status"`
	Tier        *string `json:"tier"`
	CreditLimit *int64  `json:"credit_limit_micro"`
	// ExcludeFromPublicStats 只影响公开排行榜：worker 每天重建一次全部历史物化数据，
	// 当天与前一天每 30 分钟重算，改动最迟一天内反映到全部历史。
	ExcludeFromPublicStats *bool `json:"exclude_from_public_stats"`
}

func (s *Service) UpdateAccount(ctx context.Context, id int64, in UpdateAccountInput) (*Change, error) {
	var sets []setClause
	if err := nonEmpty("name", in.Name); err != nil {
		return nil, err
	}
	if in.Name != nil {
		sets = append(sets, setClause{"name", strings.TrimSpace(*in.Name)})
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "active", "suspended", "closed"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"status", *in.Status})
	}
	if in.Tier != nil {
		if err := oneOf("tier", *in.Tier, "free", "pro", "enterprise"); err != nil {
			return nil, err
		}
		sets = append(sets, setClause{"tier", *in.Tier})
	}
	if in.CreditLimit != nil {
		if *in.CreditLimit < 0 {
			return nil, invalid("credit_limit_micro must be >= 0")
		}
		sets = append(sets, setClause{"credit_limit", *in.CreditLimit})
	}
	if in.ExcludeFromPublicStats != nil {
		sets = append(sets, setClause{"exclude_from_public_stats", *in.ExcludeFromPublicStats})
	}
	return s.patchRow(ctx, "accounts", id, sets, ErrAccountNotFound, nil)
}

// ---------- price sources ----------

type UpdatePriceSourceInput struct {
	Enabled       *bool            `json:"enabled"`
	URL           *string          `json:"url"` // 空字符串清除
	Schedule      *string          `json:"schedule"`
	Config        *json.RawMessage `json:"config"`
	Name          *string          `json:"name"`
	License       *string          `json:"license"`     // 空字符串清除
	Attribution   *string          `json:"attribution"` // 空字符串清除
	PublicDisplay *bool            `json:"public_display"`
	AutoPublish   *bool            `json:"auto_publish"`
	// ProviderID：0 = 解除绑定。绑定后价格观测会按该 provider 的渠道走调价流程。
	ProviderID *int64 `json:"provider_id"`
}

func (s *Service) UpdatePriceSource(ctx context.Context, id int64, in UpdatePriceSourceInput) (*Change, error) {
	var sets []setClause
	if in.Enabled != nil {
		sets = append(sets, setClause{"enabled", *in.Enabled})
		if *in.Enabled {
			// 重新启用时让调度器下一轮就接手（schedule 为空的来源仍只能手工触发）。
			sets = append(sets, setClause{"next_run_at", time.Now()})
		}
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return nil, invalid("name must not be empty")
		}
		sets = append(sets, setClause{"name", strings.TrimSpace(*in.Name)})
	}
	if in.License != nil {
		sets = append(sets, setClause{"license", trimmedOrNil(in.License)})
	}
	if in.Attribution != nil {
		sets = append(sets, setClause{"attribution", trimmedOrNil(in.Attribution)})
	}
	if in.PublicDisplay != nil {
		sets = append(sets, setClause{"public_display", *in.PublicDisplay})
	}
	if in.AutoPublish != nil {
		sets = append(sets, setClause{"auto_publish", *in.AutoPublish})
	}
	if in.ProviderID != nil {
		var v any
		if *in.ProviderID != 0 {
			if _, err := s.GetProvider(ctx, *in.ProviderID); err != nil {
				return nil, err
			}
			v = *in.ProviderID
		}
		sets = append(sets, setClause{"provider_id", v})
	}
	if in.URL != nil {
		var u any
		if v := strings.TrimSpace(*in.URL); v != "" {
			pu, err := url.Parse(v)
			if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" || pu.User != nil {
				return nil, invalid("url must be an absolute http(s) URL without credentials")
			}
			u = v
		}
		sets = append(sets, setClause{"url", u})
	}
	if in.Schedule != nil {
		sched := strings.TrimSpace(*in.Schedule)
		if sched != "" && !validSchedule(sched) {
			return nil, invalid("schedule must be a 5-field cron expression or @hourly/@daily/@every <duration>")
		}
		sets = append(sets, setClause{"schedule", sched})
		if in.Enabled == nil || !*in.Enabled { // 启用分支已经设置过 next_run_at
			// 改了调度就按新调度重新排：有调度的下一轮立即跑一次，清空调度的不再自动跑。
			var next any
			if sched != "" {
				next = time.Now()
			}
			sets = append(sets, setClause{"next_run_at", next})
		}
	}
	if in.Config != nil {
		var obj map[string]any
		if err := json.Unmarshal(*in.Config, &obj); err != nil || obj == nil {
			return nil, invalid("config must be a JSON object")
		}
		// config 是明文 JSONB：不允许把凭据塞进来（方案 §2.1 S10）。确实需要鉴权的
		// 来源应该走上游密钥的加密存储，而不是写在抓取配置里。
		if key := findSecretLikeKey(obj); key != "" {
			return nil, invalid("config must not contain credentials (found key %q); price source configs are stored in plaintext", key)
		}
		sets = append(sets, setClause{"config", obj})
	}
	return s.patchRow(ctx, "price_sources", id, sets, ErrPriceSourceNotFound, nil)
}

// GetPriceSource 供 PATCH 之后返回最新对象。
func (s *Service) GetPriceSource(ctx context.Context, id int64) (*PriceSourceInfo, error) {
	list, err := s.queryPriceSources(ctx, "WHERE ps.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrPriceSourceNotFound
	}
	return &list[0], nil
}

// GetProviderKey 供 PATCH 之后返回最新对象。
func (s *Service) GetProviderKey(ctx context.Context, id int64) (*ProviderKeyInfo, error) {
	var k ProviderKeyInfo
	err := s.db(ctx).QueryRow(ctx,
		`SELECT id, secret_last4, weight, status, disabled_reason, rpm_limit, tpm_limit, concurrency_limit, created_at
		 FROM provider_keys WHERE id = $1`, id,
	).Scan(&k.ID, &k.Last4, &k.Weight, &k.Status, &k.DisabledReason, &k.RPMLimit, &k.TPMLimit, &k.ConcurrencyLimit, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProviderKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query provider_key: %w", err)
	}
	return &k, nil
}

var secretLikeKey = regexp.MustCompile(`(?i)(^|[_\-.])(api[_\-]?key|access[_\-]?key|token|secret|password|passwd|authorization|cookie|credentials?)($|[_\-.])`)

// findSecretLikeKey 递归查找看起来像凭据的键名，返回第一个命中的键。
func findSecretLikeKey(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if secretLikeKey.MatchString(k) {
				return k
			}
			if found := findSecretLikeKey(child); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range t {
			if found := findSecretLikeKey(child); found != "" {
				return found
			}
		}
	}
	return ""
}

var cronField = regexp.MustCompile(`^[0-9*/,\-]+$`)

// ValidSchedule 供创建数据源时复用 validSchedule。
func ValidSchedule(s string) bool { return validSchedule(strings.TrimSpace(s)) }

// CheckSourceConfig 拒绝把凭据写进明文的数据源配置（方案 §2.1 S10）。需要密钥的来源用
// "auth_header_env" 之类的键引用 worker 的环境变量名，而不是把值写进来。
func CheckSourceConfig(obj map[string]any) error {
	if key := findSecretLikeKey(obj); key != "" {
		return invalid("config must not contain credentials (found key %q); price source configs are stored in plaintext", key)
	}
	return nil
}

// validSchedule 接受 5 段 cron 表达式、@hourly/@daily/@weekly，或 @every <Go duration>（至少 1 分钟）。
func validSchedule(s string) bool {
	switch s {
	case "@hourly", "@daily", "@weekly":
		return true
	}
	if d, ok := strings.CutPrefix(s, "@every "); ok {
		dur, err := time.ParseDuration(strings.TrimSpace(d))
		return err == nil && dur >= time.Minute
	}
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		if !cronField.MatchString(f) {
			return false
		}
	}
	return true
}
