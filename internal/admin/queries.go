package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// 运营后台的列表/详情查询（运营后台接口方案 §1）。写操作见 catalog.go、updates.go。

var (
	ErrInvalidSort          = errors.New("admin: unsupported sort field")
	ErrProviderNotFound     = errors.New("admin: provider not found")
	ErrProviderKeyNotFound  = errors.New("admin: provider key not found")
	ErrPriceSourceNotFound  = errors.New("admin: price source not found")
	ErrLastActiveChannel    = errors.New("admin: this is the last active channel of the virtual model; pass force=true to disable it anyway")
	ErrProviderKeyRevoked   = errors.New("admin: provider key is revoked and can no longer be changed")
	ErrNothingToUpdate      = errors.New("admin: no updatable field in request body")
	ErrInvalidFilterOrValue = errors.New("admin: invalid filter or field value")
)

// PageRequest 是页码分页参数（接口方案 §0.2）：page 从 1 开始，page_size 默认 20、最大 100。
type PageRequest struct {
	Page     int
	PageSize int
}

func (p PageRequest) normalize() PageRequest {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	return p
}

func (p PageRequest) offset() int { return (p.Page - 1) * p.PageSize }

type Page[T any] struct {
	Data     []T `json:"data"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// reorderByIDs 按 ids 的顺序排列 rows（SQL 已经决定了顺序与分页，明细查询按 id 取回后恢复顺序）。
func reorderByIDs[T any](ids []int64, rows []T, id func(T) int64) []T {
	pos := make(map[int64]int, len(ids))
	for i, v := range ids {
		pos[v] = i
	}
	out := make([]T, len(ids))
	n := 0
	for _, r := range rows {
		if i, ok := pos[id(r)]; ok {
			out[i] = r
			n++
		}
	}
	return out[:n]
}

// pageIDs 执行"只取 id 的分页查询"并返回 id 列表与总数。
func (s *Service) pageIDs(ctx context.Context, idCol, fromWhere, order string, p PageRequest, args ...any) ([]int64, int, error) {
	p = p.normalize()
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) `+fromWhere, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("admin: count: %w", err)
	}
	n := len(args)
	rows, err := s.db(ctx).Query(ctx, fmt.Sprintf(`SELECT %s %s ORDER BY %s LIMIT $%d OFFSET $%d`, idCol, fromWhere, order, n+1, n+2),
		append(args, p.PageSize, p.offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("admin: page ids: %w", err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	return ids, total, rows.Err()
}

// likePattern 把用户输入转义成 ILIKE 的"包含"模式。
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.TrimSpace(q)) + "%"
}

// orderBy 按白名单把 sort 参数（"field" 升序 / "-field" 降序）翻译成 SQL；
// 空字符串用默认值。
func orderBy(sortParam, def string, allowed map[string]string) (string, error) {
	if sortParam == "" {
		sortParam = def
	}
	desc := strings.HasPrefix(sortParam, "-")
	col, ok := allowed[strings.TrimPrefix(sortParam, "-")]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrInvalidSort, sortParam)
	}
	if desc {
		return col + " DESC", nil
	}
	return col + " ASC", nil
}

// ---------- providers ----------

type ProviderSummary struct {
	ID                  int64  `json:"id"`
	Code                string `json:"code"`
	Name                string `json:"name"`
	Protocol            string `json:"protocol"`
	Currency            string `json:"currency"`
	Status              string `json:"status"`
	AccountCount        int    `json:"account_count"`
	ActiveKeyCount      int    `json:"active_key_count"`
	ChannelCount        int    `json:"channel_count"`
	PendingListingCount int    `json:"pending_listing_count"`
	// AllowedHosts 是上游 base_url 的域名白名单；空表示不限域名（仍拦截内网地址）。
	AllowedHosts []string `json:"allowed_hosts"`
}

type ListProvidersInput struct {
	Q, Status, Protocol, Sort string
	PageRequest
}

const providerSummarySelect = `SELECT p.id, p.code, p.name, p.protocol, p.currency, p.status,
	(SELECT count(*) FROM provider_accounts pa WHERE pa.provider_id = p.id),
	(SELECT count(*) FROM provider_keys k JOIN provider_accounts pa ON pa.id = k.provider_account_id
	   WHERE pa.provider_id = p.id AND k.status = 'active'),
	(SELECT count(*) FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id WHERE pa.provider_id = p.id),
	(SELECT count(*) FROM pending_model_listings l WHERE l.provider_id = p.id AND l.status = 'pending'),
	p.allowed_hosts
	FROM providers p`

func scanProviderSummary(row pgx.Row) (ProviderSummary, error) {
	var p ProviderSummary
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Protocol, &p.Currency, &p.Status,
		&p.AccountCount, &p.ActiveKeyCount, &p.ChannelCount, &p.PendingListingCount, &p.AllowedHosts)
	return p, err
}

func (s *Service) ListProviders(ctx context.Context, in ListProvidersInput) (*Page[ProviderSummary], error) {
	order, err := orderBy(in.Sort, "code", map[string]string{
		"id": "p.id", "code": "p.code", "name": "p.name",
		"channel_count": "(SELECT count(*) FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id WHERE pa.provider_id = p.id)",
	})
	if err != nil {
		return nil, err
	}
	where := `WHERE ($1 = '' OR p.code ILIKE $2 OR p.name ILIKE $2) AND ($3 = '' OR p.status = $3) AND ($4 = '' OR p.protocol = $4)`
	args := []any{strings.TrimSpace(in.Q), likePattern(in.Q), in.Status, in.Protocol}

	pr := in.PageRequest.normalize()
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM providers p `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count providers: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx, providerSummarySelect+" "+where+" ORDER BY "+order+", p.id LIMIT $5 OFFSET $6",
		append(args, pr.PageSize, pr.offset())...)
	if err != nil {
		return nil, fmt.Errorf("admin: query providers: %w", err)
	}
	defer rows.Close()
	out := []ProviderSummary{}
	for rows.Next() {
		p, err := scanProviderSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("admin: scan provider: %w", err)
		}
		out = append(out, p)
	}
	return &Page[ProviderSummary]{Data: out, Total: total, Page: pr.Page, PageSize: pr.PageSize}, rows.Err()
}

type ProviderDetail struct {
	ProviderSummary
	Accounts []ProviderAccountSummary `json:"accounts"`
	// AccountsTruncated 为 true 表示账号超过 100 个、这里只返回了前 100 个；
	// 完整列表用 GET /provider-accounts?provider_id= 分页查询。
	AccountsTruncated bool              `json:"accounts_truncated"`
	PriceSources      []PriceSourceInfo `json:"price_sources"`
}

func (s *Service) GetProvider(ctx context.Context, id int64) (*ProviderDetail, error) {
	p, err := scanProviderSummary(s.db(ctx).QueryRow(ctx, providerSummarySelect+" WHERE p.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProviderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query provider: %w", err)
	}
	accounts, err := s.ListProviderAccounts(ctx, ListProviderAccountsInput{ProviderID: id, PageRequest: PageRequest{PageSize: 100}})
	if err != nil {
		return nil, err
	}
	sources, err := s.ListPriceSources(ctx, ListPriceSourcesInput{ProviderID: id})
	if err != nil {
		return nil, err
	}
	return &ProviderDetail{ProviderSummary: p, Accounts: accounts.Data, AccountsTruncated: accounts.Total > len(accounts.Data), PriceSources: sources}, nil
}

// ---------- provider accounts & keys ----------

type ProviderAccountSummary struct {
	ID             int64           `json:"id"`
	ProviderID     int64           `json:"provider_id"`
	ProviderCode   string          `json:"provider_code"`
	Name           string          `json:"name"`
	BaseURL        string          `json:"base_url"`
	Region         *string         `json:"region"`
	CostMultiplier decimal.Decimal `json:"cost_multiplier"`
	Status         string          `json:"status"`
	KeyCount       int             `json:"key_count"`
	ActiveKeyCount int             `json:"active_key_count"`
	ChannelCount   int             `json:"channel_count"`
}

type ListProviderAccountsInput struct {
	ProviderID int64
	Q, Status  string
	PageRequest
}

const providerAccountSelect = `SELECT pa.id, pa.provider_id, p.code, pa.name, pa.base_url, pa.region, pa.cost_multiplier, pa.status,
	(SELECT count(*) FROM provider_keys k WHERE k.provider_account_id = pa.id),
	(SELECT count(*) FROM provider_keys k WHERE k.provider_account_id = pa.id AND k.status = 'active'),
	(SELECT count(*) FROM channels c WHERE c.provider_account_id = pa.id)
	FROM provider_accounts pa JOIN providers p ON p.id = pa.provider_id`

func scanProviderAccount(row pgx.Row) (ProviderAccountSummary, error) {
	var a ProviderAccountSummary
	err := row.Scan(&a.ID, &a.ProviderID, &a.ProviderCode, &a.Name, &a.BaseURL, &a.Region, &a.CostMultiplier, &a.Status,
		&a.KeyCount, &a.ActiveKeyCount, &a.ChannelCount)
	return a, err
}

func (s *Service) ListProviderAccounts(ctx context.Context, in ListProviderAccountsInput) (*Page[ProviderAccountSummary], error) {
	where := `WHERE ($1 = 0 OR pa.provider_id = $1) AND ($2 = '' OR pa.name ILIKE $3 OR pa.base_url ILIKE $3) AND ($4 = '' OR pa.status = $4)`
	args := []any{in.ProviderID, strings.TrimSpace(in.Q), likePattern(in.Q), in.Status}
	pr := in.PageRequest.normalize()
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM provider_accounts pa `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count provider_accounts: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx, providerAccountSelect+" "+where+" ORDER BY pa.id LIMIT $5 OFFSET $6", append(args, pr.PageSize, pr.offset())...)
	if err != nil {
		return nil, fmt.Errorf("admin: query provider_accounts: %w", err)
	}
	defer rows.Close()
	out := []ProviderAccountSummary{}
	for rows.Next() {
		a, err := scanProviderAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("admin: scan provider_account: %w", err)
		}
		out = append(out, a)
	}
	return &Page[ProviderAccountSummary]{Data: out, Total: total, Page: pr.Page, PageSize: pr.PageSize}, rows.Err()
}

type ProviderKeyInfo struct {
	ID               int64     `json:"id"`
	Last4            string    `json:"last4"`
	Weight           int       `json:"weight"`
	Status           string    `json:"status"`
	DisabledReason   *string   `json:"disabled_reason"`
	RPMLimit         *int      `json:"rpm_limit"`
	TPMLimit         *int      `json:"tpm_limit"`
	ConcurrencyLimit *int      `json:"concurrency_limit"`
	CreatedAt        time.Time `json:"created_at"`
}

type ProviderAccountDetail struct {
	ProviderAccountSummary
	Keys []ProviderKeyInfo `json:"keys"`
}

func (s *Service) GetProviderAccount(ctx context.Context, id int64) (*ProviderAccountDetail, error) {
	a, err := scanProviderAccount(s.db(ctx).QueryRow(ctx, providerAccountSelect+" WHERE pa.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProviderAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query provider_account: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT id, secret_last4, weight, status, disabled_reason, rpm_limit, tpm_limit, concurrency_limit, created_at
		 FROM provider_keys WHERE provider_account_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("admin: query provider_keys: %w", err)
	}
	defer rows.Close()
	keys := []ProviderKeyInfo{}
	for rows.Next() {
		var k ProviderKeyInfo
		if err := rows.Scan(&k.ID, &k.Last4, &k.Weight, &k.Status, &k.DisabledReason, &k.RPMLimit, &k.TPMLimit, &k.ConcurrencyLimit, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan provider_key: %w", err)
		}
		keys = append(keys, k)
	}
	return &ProviderAccountDetail{ProviderAccountSummary: a, Keys: keys}, rows.Err()
}

// ---------- virtual models ----------

type VirtualModelSummary struct {
	ID                 int64            `json:"id"`
	Name               string           `json:"name"`
	Family             string           `json:"family"`
	Type               string           `json:"type"`
	Status             string           `json:"status"`
	ContextWindow      int              `json:"context_window"`
	MaxOutput          int              `json:"max_output"`
	Capabilities       []string         `json:"capabilities"`
	VisibleTiers       []string         `json:"visible_tiers"`
	Aliases            []string         `json:"aliases"`
	DisplayName        *string          `json:"display_name"`
	HasMetadata        bool             `json:"has_metadata"`
	ChannelCount       int              `json:"channel_count"`
	ActiveChannelCount int              `json:"active_channel_count"`
	SellPrice          *PriceBrief      `json:"sell_price"`
	MinMarginRatio     *decimal.Decimal `json:"min_margin_ratio"`
}

type ListVirtualModelsInput struct {
	Q                           string
	Statuses                    []string
	Type, Family, Tier, Missing string // Missing: sell_price / metadata / channel
	NegativeMargin              bool   // 只要 min_margin_ratio < 0 的模型
	Sort                        string
	PageRequest
}

func (s *Service) queryVirtualModels(ctx context.Context, where string, args ...any) ([]VirtualModelSummary, error) {
	rows, err := s.db(ctx).Query(ctx,
		`SELECT vm.id, vm.name, vm.family, vm.type, vm.status, vm.context_window, vm.max_output, vm.capabilities, vm.visible_tiers, vm.aliases,
		   md.display_name, md.virtual_model_id IS NOT NULL,
		   (SELECT count(*) FROM channels c WHERE c.virtual_model_id = vm.id),
		   (SELECT count(*) FROM channels c WHERE c.virtual_model_id = vm.id AND c.status = 'active')
		 FROM virtual_models vm LEFT JOIN virtual_model_metadata md ON md.virtual_model_id = vm.id `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: query virtual_models: %w", err)
	}
	defer rows.Close()
	out := []VirtualModelSummary{}
	for rows.Next() {
		var v VirtualModelSummary
		if err := rows.Scan(&v.ID, &v.Name, &v.Family, &v.Type, &v.Status, &v.ContextWindow, &v.MaxOutput, &v.Capabilities, &v.VisibleTiers, &v.Aliases,
			&v.DisplayName, &v.HasMetadata, &v.ChannelCount, &v.ActiveChannelCount); err != nil {
			return nil, fmt.Errorf("admin: scan virtual_model: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.fillVirtualModelPrices(ctx, out)
}

// fillVirtualModelPrices 填 sell_price 与 min_margin_ratio（该模型所有 active 渠道里最低的毛利率）。
func (s *Service) fillVirtualModelPrices(ctx context.Context, vms []VirtualModelSummary) error {
	if len(vms) == 0 {
		return nil
	}
	vmIDs := make([]int64, len(vms))
	for i, v := range vms {
		vmIDs[i] = v.ID
	}
	type chRow struct {
		id, vmID   int64
		multiplier decimal.Decimal
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT c.id, c.virtual_model_id, pa.cost_multiplier FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id
		 WHERE c.virtual_model_id = ANY($1) AND c.status = 'active'`, vmIDs)
	if err != nil {
		return fmt.Errorf("admin: query channels for margin: %w", err)
	}
	var chs []chRow
	for rows.Next() {
		var c chRow
		if err := rows.Scan(&c.id, &c.vmID, &c.multiplier); err != nil {
			rows.Close()
			return fmt.Errorf("admin: scan channel for margin: %w", err)
		}
		chs = append(chs, c)
	}
	rows.Close()
	chIDs := make([]int64, len(chs))
	for i, c := range chs {
		chIDs[i] = c.id
	}
	pc, err := s.loadPriceContext(ctx, chIDs, vmIDs)
	if err != nil {
		return err
	}
	minByVM := map[int64]*decimal.Decimal{}
	for _, c := range chs {
		m := marginRatio(pc.sellByVM[c.vmID], pc.costInCNY(pc.costByChannel[c.id], c.multiplier))
		if m != nil && (minByVM[c.vmID] == nil || m.LessThan(*minByVM[c.vmID])) {
			minByVM[c.vmID] = m
		}
	}
	for i := range vms {
		vms[i].SellPrice = pc.sellByVM[vms[i].ID]
		vms[i].MinMarginRatio = minByVM[vms[i].ID]
	}
	return nil
}

// ListVirtualModels 的过滤（含缺售价/缺元数据/无活跃渠道/负毛利这些派生条件）、排序、
// 分页与总数都在 SQL 里完成（视图 v_admin_model_summary，迁移 00023）；只有当前页的
// 行再由 queryVirtualModels 填充价格明细。
func (s *Service) ListVirtualModels(ctx context.Context, in ListVirtualModelsInput) (*Page[VirtualModelSummary], error) {
	var missing string
	switch in.Missing {
	case "":
	case "sell_price":
		missing = "AND NOT ms.has_sell_price"
	case "metadata":
		missing = "AND NOT ms.has_metadata"
	case "channel":
		missing = "AND ms.active_channel_count = 0"
	default:
		return nil, fmt.Errorf("%w: missing=%q", ErrInvalidFilterOrValue, in.Missing)
	}
	order, err := orderBy(in.Sort, "name", map[string]string{
		"name": "vm.name", "id": "vm.id", "channel_count": "ms.channel_count", "min_margin_ratio": "ms.min_margin_ratio",
	})
	if err != nil {
		return nil, err
	}
	fromWhere := `FROM virtual_models vm
	  JOIN v_admin_model_summary ms ON ms.virtual_model_id = vm.id
	  LEFT JOIN virtual_model_metadata md ON md.virtual_model_id = vm.id
	  WHERE ($1 = '' OR vm.name ILIKE $2 OR md.display_name ILIKE $2 OR EXISTS (SELECT 1 FROM unnest(vm.aliases) a WHERE a ILIKE $2))
	  AND (cardinality($3::text[]) = 0 OR vm.status = ANY($3))
	  AND ($4 = '' OR vm.type = $4) AND ($5 = '' OR vm.family = $5) AND ($6 = '' OR $6 = ANY(vm.visible_tiers))
	  AND (NOT $7 OR ms.min_margin_ratio < 0) ` + missing
	ids, total, err := s.pageIDs(ctx, "vm.id", fromWhere, order+", vm.id", in.PageRequest,
		strings.TrimSpace(in.Q), likePattern(in.Q), nonNilStrings(in.Statuses), in.Type, in.Family, in.Tier, in.NegativeMargin)
	if err != nil {
		return nil, err
	}
	rows, err := s.queryVirtualModels(ctx, "WHERE vm.id = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	pr := in.PageRequest.normalize()
	return &Page[VirtualModelSummary]{Data: reorderByIDs(ids, rows, func(v VirtualModelSummary) int64 { return v.ID }), Total: total, Page: pr.Page, PageSize: pr.PageSize}, nil
}

type VirtualModelMetadata struct {
	DisplayName     *string         `json:"display_name"`
	Description     *string         `json:"description"`
	ProviderDisplay *string         `json:"provider_display"`
	Tags            []string        `json:"tags"`
	Scores          json.RawMessage `json:"scores"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type VirtualModelDetail struct {
	VirtualModelSummary
	Metadata *VirtualModelMetadata `json:"metadata"`
	SellBook *PriceBookInfo        `json:"sell_price_book"`
	Channels []ChannelSummary      `json:"channels"`
	// ChannelsTruncated 为 true 表示渠道超过 100 个、这里只返回了前 100 个；
	// 完整列表用 GET /channels?virtual_model_id= 分页查询。
	ChannelsTruncated bool `json:"channels_truncated"`
}

func (s *Service) GetVirtualModel(ctx context.Context, id int64) (*VirtualModelDetail, error) {
	list, err := s.queryVirtualModels(ctx, "WHERE vm.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrVirtualModelNotFound
	}
	d := &VirtualModelDetail{VirtualModelSummary: list[0]}

	var md VirtualModelMetadata
	err = s.db(ctx).QueryRow(ctx,
		`SELECT display_name, description, provider_display, tags, scores, updated_at FROM virtual_model_metadata WHERE virtual_model_id = $1`, id,
	).Scan(&md.DisplayName, &md.Description, &md.ProviderDisplay, &md.Tags, &md.Scores, &md.UpdatedAt)
	switch {
	case err == nil:
		d.Metadata = &md
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("admin: query virtual_model_metadata: %w", err)
	}

	books, err := s.ListPriceBooks(ctx, ListPriceBooksInput{Kind: "sell", VirtualModelID: id, Limit: 1, CurrentOnly: true})
	if err != nil {
		return nil, err
	}
	if len(books) > 0 {
		d.SellBook = &books[0]
	}
	chs, err := s.ListChannels(ctx, ListChannelsInput{VirtualModelID: id, PageRequest: PageRequest{PageSize: 100}})
	if err != nil {
		return nil, err
	}
	d.Channels = chs.Data
	d.ChannelsTruncated = chs.Total > len(chs.Data)
	return d, nil
}

// ---------- price books ----------

type PriceBookInfo struct {
	ID            int64                 `json:"id"`
	Kind          string                `json:"kind"`
	Tier          *string               `json:"tier"`
	Currency      string                `json:"currency"`
	EffectiveFrom time.Time             `json:"effective_from"`
	EffectiveTo   *time.Time            `json:"effective_to"`
	CreatedBy     *int64                `json:"created_by"`
	Note          *string               `json:"note"`
	CreatedAt     time.Time             `json:"created_at"`
	IsCurrent     bool                  `json:"is_current"`
	Components    []PriceComponentInput `json:"components"`
}

type ListPriceBooksInput struct {
	Kind           string // sell / cost
	VirtualModelID int64
	ChannelID      int64
	Limit          int  // 默认 20，最大 100
	CurrentOnly    bool // 只要当前生效的那一本
}

// ListPriceBooks 按 effective_from 倒序列出价格版本；IsCurrent 标出当前生效的那一本
// （与 catalog 口径一致：effective_from <= now() 且未过期的最新一本）。
func (s *Service) ListPriceBooks(ctx context.Context, in ListPriceBooksInput) ([]PriceBookInfo, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	keyCol, key := "virtual_model_id", in.VirtualModelID
	if in.Kind == "cost" {
		keyCol, key = "channel_id", in.ChannelID
	}
	rows, err := s.db(ctx).Query(ctx, fmt.Sprintf(
		`WITH cur AS (
		   SELECT id FROM price_books WHERE kind = $1 AND %[1]s = $2 AND effective_from <= now()
		     AND (effective_to IS NULL OR effective_to > now()) ORDER BY effective_from DESC LIMIT 1
		 )
		 SELECT pb.id, pb.kind, pb.tier, pb.currency, pb.effective_from, pb.effective_to, pb.created_by, pb.note, pb.created_at,
		   pb.id IN (SELECT id FROM cur)
		 FROM price_books pb WHERE pb.kind = $1 AND pb.%[1]s = $2 AND (NOT $3 OR pb.id IN (SELECT id FROM cur))
		 ORDER BY pb.effective_from DESC, pb.id DESC LIMIT $4`, keyCol),
		in.Kind, key, in.CurrentOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_books: %w", err)
	}
	books := []PriceBookInfo{}
	for rows.Next() {
		var b PriceBookInfo
		if err := rows.Scan(&b.ID, &b.Kind, &b.Tier, &b.Currency, &b.EffectiveFrom, &b.EffectiveTo, &b.CreatedBy, &b.Note, &b.CreatedAt, &b.IsCurrent); err != nil {
			rows.Close()
			return nil, fmt.Errorf("admin: scan price_book: %w", err)
		}
		books = append(books, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(books))
	for i := range books {
		ids[i] = books[i].ID
	}
	comps, err := s.loadComponents(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range books {
		books[i].Components = comps[books[i].ID]
		if books[i].Components == nil {
			books[i].Components = []PriceComponentInput{}
		}
	}
	return books, nil
}

// loadComponents 一次查出一批价格版本的全部分量（此前每本一条查询，N+1）。
func (s *Service) loadComponents(ctx context.Context, bookIDs []int64) (map[int64][]PriceComponentInput, error) {
	out := map[int64][]PriceComponentInput{}
	if len(bookIDs) == 0 {
		return out, nil
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT price_book_id, meter, unit, service_tier, tier_min_input, tier_max_input, window_start_min, window_end_min, unit_price
		 FROM price_components WHERE price_book_id = ANY($1)
		 ORDER BY price_book_id, meter, service_tier, tier_min_input, window_start_min NULLS FIRST`, bookIDs)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_components: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c PriceComponentInput
		if err := rows.Scan(&id, &c.Meter, &c.Unit, &c.ServiceTier, &c.TierMinInput, &c.TierMaxInput, &c.WindowStartMin, &c.WindowEndMin, &c.UnitPrice); err != nil {
			return nil, fmt.Errorf("admin: scan price_component: %w", err)
		}
		out[id] = append(out[id], c)
	}
	return out, rows.Err()
}

// ---------- channels ----------

type ChannelSummary struct {
	ID                     int64            `json:"id"`
	Status                 string           `json:"status"`
	VirtualModelID         int64            `json:"virtual_model_id"`
	VirtualModelName       string           `json:"virtual_model_name"`
	ProviderAccountID      int64            `json:"provider_account_id"`
	ProviderAccountName    string           `json:"provider_account_name"`
	ProviderID             int64            `json:"provider_id"`
	ProviderCode           string           `json:"provider_code"`
	UpstreamModel          string           `json:"upstream_model"`
	Priority               int              `json:"priority"`
	Weight                 int              `json:"weight"`
	AllowedTiers           []string         `json:"allowed_tiers"`
	AllowedAccountIDs      []int64          `json:"allowed_account_ids"`
	ExperimentKey          *string          `json:"experiment_key"`
	VariantLabel           *string          `json:"variant_label"`
	CostMultiplier         decimal.Decimal  `json:"cost_multiplier"`
	ParamOverrides         json.RawMessage  `json:"param_overrides"`
	CostPrice              *PriceBrief      `json:"cost_price"`
	CostPriceCNY           *CostCNY         `json:"cost_price_cny"`
	SellPrice              *PriceBrief      `json:"sell_price"`
	MarginRatio            *decimal.Decimal `json:"margin_ratio"`
	PendingChangeRequestID *int64           `json:"pending_change_request_id"`
}

type ListChannelsInput struct {
	VirtualModelID, ProviderAccountID, ProviderID int64
	Status, Q                                     string
	NegativeMargin, MissingCost, Dedicated        bool
	Sort                                          string
	PageRequest
}

func (s *Service) queryChannels(ctx context.Context, where string, args ...any) ([]ChannelSummary, error) {
	rows, err := s.db(ctx).Query(ctx,
		`SELECT c.id, c.status, c.virtual_model_id, vm.name, c.provider_account_id, pa.name, p.id, p.code, c.upstream_model,
		   c.priority, c.weight, c.allowed_tiers, c.allowed_account_ids, c.experiment_key, c.variant_label, pa.cost_multiplier, c.param_overrides,
		   (SELECT cr.id FROM price_change_requests cr WHERE cr.channel_id = c.id AND cr.status IN ('pending','blocked') ORDER BY cr.created_at DESC LIMIT 1)
		 FROM channels c
		 JOIN virtual_models vm ON vm.id = c.virtual_model_id
		 JOIN provider_accounts pa ON pa.id = c.provider_account_id
		 JOIN providers p ON p.id = pa.provider_id `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: query channels: %w", err)
	}
	defer rows.Close()
	out := []ChannelSummary{}
	for rows.Next() {
		var c ChannelSummary
		if err := rows.Scan(&c.ID, &c.Status, &c.VirtualModelID, &c.VirtualModelName, &c.ProviderAccountID, &c.ProviderAccountName, &c.ProviderID, &c.ProviderCode,
			&c.UpstreamModel, &c.Priority, &c.Weight, &c.AllowedTiers, &c.AllowedAccountIDs, &c.ExperimentKey, &c.VariantLabel, &c.CostMultiplier,
			&c.ParamOverrides, &c.PendingChangeRequestID); err != nil {
			return nil, fmt.Errorf("admin: scan channel: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	chIDs := make([]int64, len(out))
	vmSet := map[int64]bool{}
	var vmIDs []int64
	for i, c := range out {
		chIDs[i] = c.ID
		if !vmSet[c.VirtualModelID] {
			vmSet[c.VirtualModelID] = true
			vmIDs = append(vmIDs, c.VirtualModelID)
		}
	}
	pc, err := s.loadPriceContext(ctx, chIDs, vmIDs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		c := &out[i]
		c.CostPrice = pc.costByChannel[c.ID]
		c.CostPriceCNY = pc.costInCNY(c.CostPrice, c.CostMultiplier)
		c.SellPrice = pc.sellByVM[c.VirtualModelID]
		c.MarginRatio = marginRatio(c.SellPrice, c.CostPriceCNY)
	}
	return out, nil
}

// ListChannels 同 ListVirtualModels：过滤（含负毛利/缺成本价）、排序、分页在 SQL 里完成
// （视图 v_admin_channel_margin，迁移 00023），当前页再填价格明细。
func (s *Service) ListChannels(ctx context.Context, in ListChannelsInput) (*Page[ChannelSummary], error) {
	order, err := orderBy(in.Sort, "id", map[string]string{
		"id": "c.id", "priority": "c.priority", "weight": "c.weight", "margin_ratio": "m.margin_ratio",
	})
	if err != nil {
		return nil, err
	}
	fromWhere := `FROM channels c
	  JOIN virtual_models vm ON vm.id = c.virtual_model_id
	  JOIN provider_accounts pa ON pa.id = c.provider_account_id
	  JOIN v_admin_channel_margin m ON m.channel_id = c.id
	  WHERE ($1 = 0 OR c.virtual_model_id = $1) AND ($2 = 0 OR c.provider_account_id = $2) AND ($3 = 0 OR pa.provider_id = $3)
	  AND ($4 = '' OR c.status = $4) AND ($5 = '' OR vm.name ILIKE $6 OR c.upstream_model ILIKE $6)
	  AND (NOT $7 OR cardinality(c.allowed_account_ids) > 0)
	  AND (NOT $8 OR m.margin_ratio < 0) AND (NOT $9 OR m.cost_book_id IS NULL)`
	ids, total, err := s.pageIDs(ctx, "c.id", fromWhere, order+", c.id", in.PageRequest,
		in.VirtualModelID, in.ProviderAccountID, in.ProviderID, in.Status, strings.TrimSpace(in.Q), likePattern(in.Q), in.Dedicated,
		in.NegativeMargin, in.MissingCost)
	if err != nil {
		return nil, err
	}
	rows, err := s.queryChannels(ctx, "WHERE c.id = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	pr := in.PageRequest.normalize()
	return &Page[ChannelSummary]{Data: reorderByIDs(ids, rows, func(c ChannelSummary) int64 { return c.ID }), Total: total, Page: pr.Page, PageSize: pr.PageSize}, nil
}

type PriceObservationInfo struct {
	ID          int64           `json:"id"`
	SourceID    int64           `json:"source_id"`
	SourceLevel string          `json:"source_level"`
	SourceKind  string          `json:"source_kind"`
	ObservedAt  time.Time       `json:"observed_at"`
	Spec        json.RawMessage `json:"spec"`
}

type ChannelDetail struct {
	ChannelSummary
	CostPriceHistory   []PriceBookInfo        `json:"cost_price_history"`
	RecentObservations []PriceObservationInfo `json:"recent_observations"`
	// ChangeRequests 是这个渠道最近 20 条调价请求（任意状态），接口方案 §1.4。
	ChangeRequests []ChangeRequestSummary `json:"change_requests"`
}

func (s *Service) GetChannel(ctx context.Context, id int64) (*ChannelDetail, error) {
	list, err := s.queryChannels(ctx, "WHERE c.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrChannelNotFound
	}
	d := &ChannelDetail{ChannelSummary: list[0]}
	if d.CostPriceHistory, err = s.ListPriceBooks(ctx, ListPriceBooksInput{Kind: "cost", ChannelID: id, Limit: 20}); err != nil {
		return nil, err
	}
	// 观测按 (来源, upstream_model) 记录，不直接挂在渠道上：取同一供应商下的来源
	// 对这个 upstream_model 的最近观测。spec 是 pricesync.PriceSpec 的原始 JSON。
	rows, err := s.db(ctx).Query(ctx,
		`SELECT o.id, o.source_id, ps.level, ps.kind, o.observed_at, o.spec
		 FROM price_observations o JOIN price_sources ps ON ps.id = o.source_id
		 WHERE o.upstream_model = $1 AND (ps.provider_id = $2 OR ps.provider_id IS NULL)
		 ORDER BY o.observed_at DESC LIMIT 20`, d.UpstreamModel, d.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_observations: %w", err)
	}
	defer rows.Close()
	d.RecentObservations = []PriceObservationInfo{}
	for rows.Next() {
		var o PriceObservationInfo
		if err := rows.Scan(&o.ID, &o.SourceID, &o.SourceLevel, &o.SourceKind, &o.ObservedAt, &o.Spec); err != nil {
			return nil, fmt.Errorf("admin: scan price_observation: %w", err)
		}
		d.RecentObservations = append(d.RecentObservations, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	crs, err := s.ListChangeRequests(ctx, ListChangeRequestsInput{Statuses: []string{"all"}, ChannelID: id, PageRequest: PageRequest{PageSize: 20}})
	if err != nil {
		return nil, err
	}
	d.ChangeRequests = crs.Data
	return d, nil
}

// ---------- fx rates & price sources ----------

type FXRateInfo struct {
	Base          string          `json:"base"`
	Quote         string          `json:"quote"`
	Rate          decimal.Decimal `json:"rate"`
	Source        string          `json:"source"`
	EffectiveDate time.Time       `json:"effective_date"`
}

// ListFXRates：latest=true 时每个 (base, quote) 只取 effective_date <= 今天 的最新一条。
func (s *Service) ListFXRates(ctx context.Context, base, quote string, latest bool, limit int) ([]FXRateInfo, error) {
	if limit <= 0 || limit > 500 {
		limit = 30
	}
	q := `SELECT base, quote, rate, source, effective_date FROM fx_rates
	      WHERE ($1 = '' OR base = $1) AND ($2 = '' OR quote = $2) ORDER BY effective_date DESC, base, quote LIMIT $3`
	if latest {
		q = `SELECT DISTINCT ON (base, quote) base, quote, rate, source, effective_date FROM fx_rates
		     WHERE ($1 = '' OR base = $1) AND ($2 = '' OR quote = $2) AND effective_date <= CURRENT_DATE AND $3 > 0
		     ORDER BY base, quote, effective_date DESC`
	}
	rows, err := s.db(ctx).Query(ctx, q, base, quote, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: query fx_rates: %w", err)
	}
	defer rows.Close()
	out := []FXRateInfo{}
	for rows.Next() {
		var r FXRateInfo
		if err := rows.Scan(&r.Base, &r.Quote, &r.Rate, &r.Source, &r.EffectiveDate); err != nil {
			return nil, fmt.Errorf("admin: scan fx_rate: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PriceSourceInfo 是一个数据源（历史原因表名仍是 price_sources；domain 区分价格 / 优惠 / 评测榜单，
// 见 docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §2）。
type PriceSourceInfo struct {
	ID                  int64           `json:"id"`
	Domain              string          `json:"domain"`
	Name                string          `json:"name"`
	ProviderID          *int64          `json:"provider_id"`
	ProviderCode        *string         `json:"provider_code"`
	Level               string          `json:"level"`
	Kind                string          `json:"kind"`
	Fetcher             string          `json:"fetcher"`
	URL                 *string         `json:"url"`
	Schedule            string          `json:"schedule"`
	Config              json.RawMessage `json:"config"`
	Enabled             bool            `json:"enabled"`
	License             *string         `json:"license"`
	Attribution         *string         `json:"attribution"`
	PublicDisplay       bool            `json:"public_display"`
	AutoPublish         bool            `json:"auto_publish"`
	NextRunAt           *time.Time      `json:"next_run_at"`
	LastRunAt           *time.Time      `json:"last_run_at"`
	LastSuccessAt       *time.Time      `json:"last_success_at"`
	LastError           *string         `json:"last_error"`
	ConsecutiveFailures int             `json:"consecutive_failures"`
	ObservationCount7d  int             `json:"observation_count_7d"`
	CreatedAt           time.Time       `json:"created_at"`
}

type ListPriceSourcesInput struct {
	ProviderID int64
	Enabled    *bool
	Domain     string
}

func (s *Service) ListPriceSources(ctx context.Context, in ListPriceSourcesInput) ([]PriceSourceInfo, error) {
	return s.queryPriceSources(ctx,
		`WHERE ($1 = 0 OR ps.provider_id = $1) AND ($2::boolean IS NULL OR ps.enabled = $2) AND ($3 = '' OR ps.domain = $3) ORDER BY ps.domain, ps.id`,
		in.ProviderID, in.Enabled, in.Domain)
}

func (s *Service) queryPriceSources(ctx context.Context, where string, args ...any) ([]PriceSourceInfo, error) {
	rows, err := s.db(ctx).Query(ctx,
		`SELECT ps.id, ps.domain, ps.name, ps.provider_id, p.code, ps.level, ps.kind, ps.fetcher, ps.url, ps.schedule, ps.config, ps.enabled,
		   ps.license, ps.attribution, ps.public_display, ps.auto_publish, ps.next_run_at, ps.last_run_at, ps.last_success_at,
		   ps.last_error, ps.consecutive_failures,
		   (SELECT count(*) FROM price_observations o WHERE o.source_id = ps.id AND o.observed_at > now() - interval '7 days'),
		   ps.created_at
		 FROM price_sources ps LEFT JOIN providers p ON p.id = ps.provider_id `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_sources: %w", err)
	}
	defer rows.Close()
	out := []PriceSourceInfo{}
	for rows.Next() {
		var ps PriceSourceInfo
		if err := rows.Scan(&ps.ID, &ps.Domain, &ps.Name, &ps.ProviderID, &ps.ProviderCode, &ps.Level, &ps.Kind, &ps.Fetcher, &ps.URL,
			&ps.Schedule, &ps.Config, &ps.Enabled, &ps.License, &ps.Attribution, &ps.PublicDisplay, &ps.AutoPublish, &ps.NextRunAt,
			&ps.LastRunAt, &ps.LastSuccessAt, &ps.LastError, &ps.ConsecutiveFailures, &ps.ObservationCount7d, &ps.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan price_source: %w", err)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// ---------- 小工具 ----------

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// CurrentPriceBook 返回当前生效的售价（kind=sell，targetID 是虚拟模型）或成本价
// （kind=cost，targetID 是渠道）版本；没有时返回 nil。用于改价审计的 before 快照。
func (s *Service) CurrentPriceBook(ctx context.Context, kind string, targetID int64) (*PriceBookInfo, error) {
	in := ListPriceBooksInput{Kind: kind, Limit: 1, CurrentOnly: true}
	if kind == "sell" {
		in.VirtualModelID = targetID
	} else {
		in.ChannelID = targetID
	}
	books, err := s.ListPriceBooks(ctx, in)
	if err != nil || len(books) == 0 {
		return nil, err
	}
	return &books[0], nil
}

// GetVirtualModelMetadata 返回虚拟模型的展示元数据；没有录入过时返回 nil。
func (s *Service) GetVirtualModelMetadata(ctx context.Context, vmID int64) (*VirtualModelMetadata, error) {
	var md VirtualModelMetadata
	err := s.db(ctx).QueryRow(ctx,
		`SELECT display_name, description, provider_display, tags, scores, updated_at FROM virtual_model_metadata WHERE virtual_model_id = $1`, vmID,
	).Scan(&md.DisplayName, &md.Description, &md.ProviderDisplay, &md.Tags, &md.Scores, &md.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query virtual_model_metadata: %w", err)
	}
	return &md, nil
}
