package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// paginate 在内存里分页——只用于"需要先算出毛利才能过滤/排序"的列表（虚拟模型、
// 渠道）。这两类配置在运营后台的量级是百到千，全量加载再切片比把毛利口径塞进
// SQL 更简单、也和 internal/catalog 共用同一套 Go 口径。
func paginate[T any](all []T, p PageRequest) Page[T] {
	p = p.normalize()
	start := min(p.offset(), len(all))
	end := min(start+p.PageSize, len(all))
	data := all[start:end]
	if data == nil {
		data = []T{}
	}
	return Page[T]{Data: data, Total: len(all), Page: p.Page, PageSize: p.PageSize}
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
	(SELECT count(*) FROM pending_model_listings l WHERE l.provider_id = p.id AND l.status = 'pending')
	FROM providers p`

func scanProviderSummary(row pgx.Row) (ProviderSummary, error) {
	var p ProviderSummary
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Protocol, &p.Currency, &p.Status,
		&p.AccountCount, &p.ActiveKeyCount, &p.ChannelCount, &p.PendingListingCount)
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
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM providers p `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count providers: %w", err)
	}
	rows, err := s.pool.Query(ctx, providerSummarySelect+" "+where+" ORDER BY "+order+", p.id LIMIT $5 OFFSET $6",
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
	Accounts     []ProviderAccountSummary `json:"accounts"`
	PriceSources []PriceSourceInfo        `json:"price_sources"`
}

func (s *Service) GetProvider(ctx context.Context, id int64) (*ProviderDetail, error) {
	p, err := scanProviderSummary(s.pool.QueryRow(ctx, providerSummarySelect+" WHERE p.id = $1", id))
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
	return &ProviderDetail{ProviderSummary: p, Accounts: accounts.Data, PriceSources: sources}, nil
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
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM provider_accounts pa `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count provider_accounts: %w", err)
	}
	rows, err := s.pool.Query(ctx, providerAccountSelect+" "+where+" ORDER BY pa.id LIMIT $5 OFFSET $6", append(args, pr.PageSize, pr.offset())...)
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
	a, err := scanProviderAccount(s.pool.QueryRow(ctx, providerAccountSelect+" WHERE pa.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProviderAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query provider_account: %w", err)
	}
	rows, err := s.pool.Query(ctx,
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
	rows, err := s.pool.Query(ctx,
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
	rows, err := s.pool.Query(ctx,
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

func (s *Service) ListVirtualModels(ctx context.Context, in ListVirtualModelsInput) (*Page[VirtualModelSummary], error) {
	where := `WHERE ($1 = '' OR vm.name ILIKE $2 OR md.display_name ILIKE $2 OR EXISTS (SELECT 1 FROM unnest(vm.aliases) a WHERE a ILIKE $2))
	  AND (cardinality($3::text[]) = 0 OR vm.status = ANY($3))
	  AND ($4 = '' OR vm.type = $4) AND ($5 = '' OR vm.family = $5) AND ($6 = '' OR $6 = ANY(vm.visible_tiers))`
	all, err := s.queryVirtualModels(ctx, where, strings.TrimSpace(in.Q), likePattern(in.Q), nonNilStrings(in.Statuses), in.Type, in.Family, in.Tier)
	if err != nil {
		return nil, err
	}
	switch in.Missing {
	case "":
	case "sell_price":
		all = filter(all, func(v VirtualModelSummary) bool { return v.SellPrice == nil })
	case "metadata":
		all = filter(all, func(v VirtualModelSummary) bool { return !v.HasMetadata })
	case "channel":
		all = filter(all, func(v VirtualModelSummary) bool { return v.ActiveChannelCount == 0 })
	default:
		return nil, fmt.Errorf("%w: missing=%q", ErrInvalidFilterOrValue, in.Missing)
	}
	if in.NegativeMargin {
		all = filter(all, func(v VirtualModelSummary) bool { return v.MinMarginRatio != nil && v.MinMarginRatio.IsNegative() })
	}
	less := map[string]func(a, b VirtualModelSummary) bool{
		"name":             func(a, b VirtualModelSummary) bool { return a.Name < b.Name },
		"id":               func(a, b VirtualModelSummary) bool { return a.ID < b.ID },
		"channel_count":    func(a, b VirtualModelSummary) bool { return a.ChannelCount < b.ChannelCount },
		"min_margin_ratio": func(a, b VirtualModelSummary) bool { return decLess(a.MinMarginRatio, b.MinMarginRatio) },
	}
	if err := sortSlice(all, in.Sort, "name", less); err != nil {
		return nil, err
	}
	p := paginate(all, in.PageRequest)
	return &p, nil
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
	err = s.pool.QueryRow(ctx,
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
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
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
	for i := range books {
		comps, err := s.loadComponents(ctx, books[i].ID)
		if err != nil {
			return nil, err
		}
		books[i].Components = comps
	}
	return books, nil
}

func (s *Service) loadComponents(ctx context.Context, bookID int64) ([]PriceComponentInput, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT meter, unit, service_tier, tier_min_input, tier_max_input, window_start_min, window_end_min, unit_price
		 FROM price_components WHERE price_book_id = $1 ORDER BY meter, service_tier, tier_min_input, window_start_min NULLS FIRST`, bookID)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_components: %w", err)
	}
	defer rows.Close()
	out := []PriceComponentInput{}
	for rows.Next() {
		var c PriceComponentInput
		if err := rows.Scan(&c.Meter, &c.Unit, &c.ServiceTier, &c.TierMinInput, &c.TierMaxInput, &c.WindowStartMin, &c.WindowEndMin, &c.UnitPrice); err != nil {
			return nil, fmt.Errorf("admin: scan price_component: %w", err)
		}
		out = append(out, c)
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
	rows, err := s.pool.Query(ctx,
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

func (s *Service) ListChannels(ctx context.Context, in ListChannelsInput) (*Page[ChannelSummary], error) {
	where := `WHERE ($1 = 0 OR c.virtual_model_id = $1) AND ($2 = 0 OR c.provider_account_id = $2) AND ($3 = 0 OR p.id = $3)
	  AND ($4 = '' OR c.status = $4) AND ($5 = '' OR vm.name ILIKE $6 OR c.upstream_model ILIKE $6)
	  AND (NOT $7 OR cardinality(c.allowed_account_ids) > 0)`
	all, err := s.queryChannels(ctx, where, in.VirtualModelID, in.ProviderAccountID, in.ProviderID, in.Status,
		strings.TrimSpace(in.Q), likePattern(in.Q), in.Dedicated)
	if err != nil {
		return nil, err
	}
	if in.NegativeMargin {
		all = filter(all, func(c ChannelSummary) bool { return c.MarginRatio != nil && c.MarginRatio.IsNegative() })
	}
	if in.MissingCost {
		all = filter(all, func(c ChannelSummary) bool { return c.CostPrice == nil })
	}
	less := map[string]func(a, b ChannelSummary) bool{
		"id":           func(a, b ChannelSummary) bool { return a.ID < b.ID },
		"priority":     func(a, b ChannelSummary) bool { return a.Priority < b.Priority },
		"weight":       func(a, b ChannelSummary) bool { return a.Weight < b.Weight },
		"margin_ratio": func(a, b ChannelSummary) bool { return decLess(a.MarginRatio, b.MarginRatio) },
	}
	if err := sortSlice(all, in.Sort, "id", less); err != nil {
		return nil, err
	}
	p := paginate(all, in.PageRequest)
	return &p, nil
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
	rows, err := s.pool.Query(ctx,
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
	return d, rows.Err()
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
	rows, err := s.pool.Query(ctx, q, base, quote, limit)
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

type PriceSourceInfo struct {
	ID                 int64           `json:"id"`
	ProviderID         *int64          `json:"provider_id"`
	ProviderCode       *string         `json:"provider_code"`
	Level              string          `json:"level"`
	Kind               string          `json:"kind"`
	Fetcher            string          `json:"fetcher"`
	URL                *string         `json:"url"`
	Schedule           string          `json:"schedule"`
	Config             json.RawMessage `json:"config"`
	Enabled            bool            `json:"enabled"`
	LastSuccessAt      *time.Time      `json:"last_success_at"`
	ObservationCount7d int             `json:"observation_count_7d"`
	CreatedAt          time.Time       `json:"created_at"`
}

type ListPriceSourcesInput struct {
	ProviderID int64
	Enabled    *bool
}

func (s *Service) ListPriceSources(ctx context.Context, in ListPriceSourcesInput) ([]PriceSourceInfo, error) {
	return s.queryPriceSources(ctx,
		`WHERE ($1 = 0 OR ps.provider_id = $1) AND ($2::boolean IS NULL OR ps.enabled = $2) ORDER BY ps.id`,
		in.ProviderID, in.Enabled)
}

func (s *Service) queryPriceSources(ctx context.Context, where string, args ...any) ([]PriceSourceInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT ps.id, ps.provider_id, p.code, ps.level, ps.kind, ps.fetcher, ps.url, ps.schedule, ps.config, ps.enabled, ps.last_success_at,
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
		if err := rows.Scan(&ps.ID, &ps.ProviderID, &ps.ProviderCode, &ps.Level, &ps.Kind, &ps.Fetcher, &ps.URL, &ps.Schedule, &ps.Config,
			&ps.Enabled, &ps.LastSuccessAt, &ps.ObservationCount7d, &ps.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan price_source: %w", err)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// ---------- 小工具 ----------

func filter[T any](in []T, keep func(T) bool) []T {
	out := in[:0]
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

// sortSlice 按白名单里的比较函数稳定排序；"-field" 表示降序。
func sortSlice[T any](all []T, sortParam, def string, less map[string]func(a, b T) bool) error {
	if sortParam == "" {
		sortParam = def
	}
	desc := strings.HasPrefix(sortParam, "-")
	fn, ok := less[strings.TrimPrefix(sortParam, "-")]
	if !ok {
		return fmt.Errorf("%w: %q", ErrInvalidSort, sortParam)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if desc {
			return fn(all[j], all[i])
		}
		return fn(all[i], all[j])
	})
	return nil
}

// decLess 把 nil（无法计算）排在最后。
func decLess(a, b *decimal.Decimal) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	default:
		return a.LessThan(*b)
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
