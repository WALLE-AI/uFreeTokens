package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// 调价审批与待上架队列的只读视图（运营后台接口方案 §5、§7）。
//
// 写操作（批准/驳回/发布/忽略）仍在 internal/pricesync；这里只负责给运营后台
// 组装展示数据。放在 admin 包而不是 pricesync，是因为 pricesync 已经依赖 admin
// （发布价格时调用 admin.SetCostPrice），反过来会形成循环依赖。
//
// price_change_requests.diff / proposed_spec、pending_model_listings.observed_spec
// 里存的是 pricesync 类型的 JSON（无 json tag，键是 PascalCase），下面的 stored*
// 结构体按同样的键名解码，不能改成 snake_case。

type storedComponent struct {
	Meter          string
	Unit           string
	ServiceTier    string
	TierMinInput   int
	TierMaxInput   *int
	WindowStartMin *int16
	WindowEndMin   *int16
	UnitPrice      decimal.Decimal
}

type storedSpec struct {
	Currency      string
	Components    []storedComponent
	EffectiveFrom *time.Time
	ExpiresAt     *time.Time
}

type storedComponentDiff struct {
	Meter        string
	ServiceTier  string
	TierMinInput int
	OldPrice     *decimal.Decimal
	NewPrice     *decimal.Decimal
	ChangeRatio  *decimal.Decimal
}

type storedIssue struct {
	Rule     string
	Message  string
	Severity string
}

type storedDiff struct {
	Components []storedComponentDiff `json:"components"`
	Issues     []storedIssue         `json:"issues"`
}

// SpecJSON 是 storedSpec 的 snake_case 输出形状。
type SpecJSON struct {
	Currency      string                `json:"currency"`
	Components    []PriceComponentInput `json:"components"`
	EffectiveFrom *time.Time            `json:"effective_from"`
	ExpiresAt     *time.Time            `json:"expires_at"`
}

func (s storedSpec) toJSON() SpecJSON {
	out := SpecJSON{Currency: s.Currency, EffectiveFrom: s.EffectiveFrom, ExpiresAt: s.ExpiresAt, Components: []PriceComponentInput{}}
	for _, c := range s.Components {
		tier := c.ServiceTier
		if tier == "" {
			tier = "default"
		}
		out.Components = append(out.Components, PriceComponentInput{
			Meter: c.Meter, Unit: c.Unit, ServiceTier: tier, TierMinInput: c.TierMinInput, TierMaxInput: c.TierMaxInput,
			WindowStartMin: c.WindowStartMin, WindowEndMin: c.WindowEndMin, UnitPrice: c.UnitPrice,
		})
	}
	return out
}

// baseUnitPrice 取基础（per_1m_tokens、default、tier_min_input=0、无时段窗口）计量项的单价。
func (s storedSpec) baseUnitPrice(meter string) *decimal.Decimal {
	for _, c := range s.Components {
		if c.Meter == meter && c.Unit == "per_1m_tokens" && (c.ServiceTier == "" || c.ServiceTier == "default") &&
			c.TierMinInput == 0 && c.WindowStartMin == nil {
			p := c.UnitPrice
			return &p
		}
	}
	return nil
}

var ErrChangeRequestNotFound = errors.New("admin: price change request not found")

// ---------- 调价申请列表 ----------

type ChangeRequestSummary struct {
	ID                  int64           `json:"id"`
	Status              string          `json:"status"`
	Direction           string          `json:"direction"`
	MaxChangeRatio      decimal.Decimal `json:"max_change_ratio"`
	EffectiveFrom       time.Time       `json:"effective_from"`
	CreatedAt           time.Time       `json:"created_at"`
	ChannelID           int64           `json:"channel_id"`
	VirtualModelID      int64           `json:"virtual_model_id"`
	VirtualModelName    string          `json:"virtual_model_name"`
	ProviderID          int64           `json:"provider_id"`
	ProviderCode        string          `json:"provider_code"`
	ProviderAccountName string          `json:"provider_account_name"`
	UpstreamModel       string          `json:"upstream_model"`
	IssueCount          int             `json:"issue_count"`
	BlockedReason       *string         `json:"blocked_reason"`
	DecidedBy           *int64          `json:"decided_by"`
	DecidedByName       *string         `json:"decided_by_name"`
	DecidedAt           *time.Time      `json:"decided_at"`
	DecisionReason      *string         `json:"decision_reason"`
	AppliedBookID       *int64          `json:"applied_book_id"`
}

type ListChangeRequestsInput struct {
	Statuses              []string // 空 = pending + blocked（与旧接口一致）；含 "all" 表示全部
	Direction             string
	ChannelID, ProviderID int64
	Sort                  string // created_at（默认，最早的优先处理）/ -created_at / max_change_ratio / -max_change_ratio
	PageRequest
}

const changeRequestSelect = `SELECT cr.id, cr.status, cr.direction, cr.max_change_ratio, cr.effective_from, cr.created_at,
	cr.channel_id, c.virtual_model_id, vm.name, p.id, p.code, pa.name, c.upstream_model, cr.diff,
	cr.decided_by, cr.decided_by_name, cr.decided_at, cr.decision_reason, cr.applied_book_id
	FROM price_change_requests cr
	JOIN channels c ON c.id = cr.channel_id
	JOIN virtual_models vm ON vm.id = c.virtual_model_id
	JOIN provider_accounts pa ON pa.id = c.provider_account_id
	JOIN providers p ON p.id = pa.provider_id`

func scanChangeRequest(row pgx.Row) (ChangeRequestSummary, storedDiff, error) {
	var cr ChangeRequestSummary
	var diffRaw []byte
	err := row.Scan(&cr.ID, &cr.Status, &cr.Direction, &cr.MaxChangeRatio, &cr.EffectiveFrom, &cr.CreatedAt,
		&cr.ChannelID, &cr.VirtualModelID, &cr.VirtualModelName, &cr.ProviderID, &cr.ProviderCode, &cr.ProviderAccountName, &cr.UpstreamModel, &diffRaw,
		&cr.DecidedBy, &cr.DecidedByName, &cr.DecidedAt, &cr.DecisionReason, &cr.AppliedBookID)
	if err != nil {
		return cr, storedDiff{}, err
	}
	var d storedDiff
	if len(diffRaw) > 0 {
		if err := json.Unmarshal(diffRaw, &d); err != nil {
			return cr, d, fmt.Errorf("admin: decode price_change_requests.diff: %w", err)
		}
	}
	cr.IssueCount = len(d.Issues)
	if cr.Status == "blocked" {
		for _, is := range d.Issues {
			if is.Severity == "blocking" {
				msg := is.Message
				cr.BlockedReason = &msg
				break
			}
		}
	}
	return cr, d, nil
}

func (s *Service) ListChangeRequests(ctx context.Context, in ListChangeRequestsInput) (*Page[ChangeRequestSummary], error) {
	statuses := in.Statuses
	if len(statuses) == 0 {
		statuses = []string{"pending", "blocked"}
	}
	if len(statuses) == 1 && statuses[0] == "all" {
		statuses = []string{}
	}
	order, err := orderBy(in.Sort, "created_at", map[string]string{"created_at": "cr.created_at", "max_change_ratio": "abs(cr.max_change_ratio)", "id": "cr.id"})
	if err != nil {
		return nil, err
	}
	where := `WHERE (cardinality($1::text[]) = 0 OR cr.status = ANY($1)) AND ($2 = '' OR cr.direction = $2)
	  AND ($3 = 0 OR cr.channel_id = $3) AND ($4 = 0 OR p.id = $4)`
	args := []any{statuses, in.Direction, in.ChannelID, in.ProviderID}
	pr := in.PageRequest
	if pr.PageSize == 0 {
		pr.PageSize = 100 // 不传分页参数时维持旧行为：待处理项一页返回完
	}
	pr = pr.normalize()
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM price_change_requests cr
		JOIN channels c ON c.id = cr.channel_id JOIN provider_accounts pa ON pa.id = c.provider_account_id
		JOIN providers p ON p.id = pa.provider_id `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count price_change_requests: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx, changeRequestSelect+" "+where+" ORDER BY "+order+", cr.id LIMIT $5 OFFSET $6",
		append(args, pr.PageSize, pr.offset())...)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_change_requests: %w", err)
	}
	defer rows.Close()
	out := []ChangeRequestSummary{}
	for rows.Next() {
		cr, _, err := scanChangeRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cr)
	}
	return &Page[ChangeRequestSummary]{Data: out, Total: total, Page: pr.Page, PageSize: pr.PageSize}, rows.Err()
}

// ---------- 调价申请详情 ----------

type ComponentChange struct {
	Meter        string           `json:"meter"`
	ServiceTier  string           `json:"service_tier"`
	TierMinInput int              `json:"tier_min_input"`
	OldPrice     *decimal.Decimal `json:"old_price"`
	NewPrice     *decimal.Decimal `json:"new_price"`
	ChangeRatio  *decimal.Decimal `json:"change_ratio"`
}

type ValidationIssue struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type EvidenceInfo struct {
	ObservationID int64     `json:"observation_id"`
	SourceID      int64     `json:"source_id"`
	SourceLevel   string    `json:"source_level"`
	SourceKind    string    `json:"source_kind"`
	SourceURL     *string   `json:"source_url"`
	ObservedAt    time.Time `json:"observed_at"`
	RawExcerpt    string    `json:"raw_excerpt"`
}

// ChangeImpact 是调价影响评估：按近 7 天该渠道成功请求的实际 token 量，
// 分别用新旧成本价估算成本（人民币微元，含汇率与 cost_multiplier），以及
// 对当前生效售价的毛利率影响。只统计基础计量项（input / output /
// input_cache_read / input_cache_write 的 per_1m_tokens 默认档），分档、时段价
// 与 reasoning 不计入——这是审批参考值，不是账单。
type ChangeImpact struct {
	WindowDays      int              `json:"window_days"`
	CostBeforeMicro int64            `json:"cost_before_micro"`
	CostAfterMicro  int64            `json:"cost_after_micro"`
	CostDeltaMicro  int64            `json:"cost_delta_micro"`
	SellPrice       *PriceBrief      `json:"sell_price"`
	MarginBefore    *decimal.Decimal `json:"margin_before"`
	MarginAfter     *decimal.Decimal `json:"margin_after"`
	FXMissing       bool             `json:"fx_missing"`
}

type ChangeRequestDetail struct {
	ChangeRequestSummary
	Currency     string            `json:"currency"`
	Components   []ComponentChange `json:"components"`
	Issues       []ValidationIssue `json:"issues"`
	ProposedSpec SpecJSON          `json:"proposed_spec"`
	CurrentBook  *PriceBookInfo    `json:"current_book"`
	Evidence     []EvidenceInfo    `json:"evidence"`
	Impact       *ChangeImpact     `json:"impact"`
}

func (s *Service) GetChangeRequest(ctx context.Context, id int64) (*ChangeRequestDetail, error) {
	cr, diff, err := scanChangeRequest(s.db(ctx).QueryRow(ctx, changeRequestSelect+" WHERE cr.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrChangeRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: query price_change_request: %w", err)
	}
	d := &ChangeRequestDetail{ChangeRequestSummary: cr, Components: []ComponentChange{}, Issues: []ValidationIssue{}, Evidence: []EvidenceInfo{}}
	for _, c := range diff.Components {
		tier := c.ServiceTier
		if tier == "" {
			tier = "default"
		}
		d.Components = append(d.Components, ComponentChange{Meter: c.Meter, ServiceTier: tier, TierMinInput: c.TierMinInput,
			OldPrice: c.OldPrice, NewPrice: c.NewPrice, ChangeRatio: c.ChangeRatio})
	}
	for _, is := range diff.Issues {
		d.Issues = append(d.Issues, ValidationIssue{Rule: is.Rule, Severity: is.Severity, Message: is.Message})
	}

	var specRaw []byte
	var currentBookID *int64
	var evidence []int64
	if err := s.db(ctx).QueryRow(ctx, `SELECT proposed_spec, current_book_id, evidence FROM price_change_requests WHERE id = $1`, id).
		Scan(&specRaw, &currentBookID, &evidence); err != nil {
		return nil, fmt.Errorf("admin: query proposed_spec: %w", err)
	}
	var spec storedSpec
	if err := json.Unmarshal(specRaw, &spec); err != nil {
		return nil, fmt.Errorf("admin: decode proposed_spec: %w", err)
	}
	d.ProposedSpec = spec.toJSON()
	d.Currency = spec.Currency

	if currentBookID != nil {
		book, err := s.getPriceBook(ctx, *currentBookID)
		if err != nil {
			return nil, err
		}
		d.CurrentBook = book
	}

	if len(evidence) > 0 {
		rows, err := s.db(ctx).Query(ctx,
			`SELECT o.id, o.source_id, ps.level, ps.kind, ps.url, o.observed_at, left(COALESCE(o.raw_object, ''), 2048)
			 FROM price_observations o JOIN price_sources ps ON ps.id = o.source_id WHERE o.id = ANY($1) ORDER BY o.observed_at DESC`, evidence)
		if err != nil {
			return nil, fmt.Errorf("admin: query evidence: %w", err)
		}
		for rows.Next() {
			var e EvidenceInfo
			if err := rows.Scan(&e.ObservationID, &e.SourceID, &e.SourceLevel, &e.SourceKind, &e.SourceURL, &e.ObservedAt, &e.RawExcerpt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("admin: scan evidence: %w", err)
			}
			d.Evidence = append(d.Evidence, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	impact, err := s.changeImpact(ctx, cr.ChannelID, cr.VirtualModelID, d.CurrentBook, spec)
	if err != nil {
		return nil, err
	}
	d.Impact = impact
	return d, nil
}

func (s *Service) getPriceBook(ctx context.Context, bookID int64) (*PriceBookInfo, error) {
	var b PriceBookInfo
	err := s.db(ctx).QueryRow(ctx,
		`SELECT id, kind, tier, currency, effective_from, effective_to, created_by, note, created_at FROM price_books WHERE id = $1`, bookID,
	).Scan(&b.ID, &b.Kind, &b.Tier, &b.Currency, &b.EffectiveFrom, &b.EffectiveTo, &b.CreatedBy, &b.Note, &b.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("admin: query price_book %d: %w", bookID, err)
	}
	comps, err := s.loadComponents(ctx, []int64{bookID})
	if err != nil {
		return nil, err
	}
	b.Components = comps[bookID]
	if b.Components == nil {
		b.Components = []PriceComponentInput{}
	}
	return &b, nil
}

// impactMeters：计量项 → request_logs 的 token 列。
var impactMeters = map[string]string{
	"input": "input_tokens", "output": "output_tokens",
	"input_cache_read": "cache_read_tokens", "input_cache_write": "cache_write_tokens",
}

func (s *Service) changeImpact(ctx context.Context, channelID, vmID int64, current *PriceBookInfo, proposed storedSpec) (*ChangeImpact, error) {
	const windowDays = 7
	tokens := map[string]int64{}
	var in, out, cr, cw int64
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT COALESCE(sum(input_tokens),0), COALESCE(sum(output_tokens),0), COALESCE(sum(cache_read_tokens),0), COALESCE(sum(cache_write_tokens),0)
		 FROM request_logs WHERE channel_id = $1 AND status = 'success' AND created_at >= now() - make_interval(days => $2)`,
		channelID, windowDays).Scan(&in, &out, &cr, &cw); err != nil {
		return nil, fmt.Errorf("admin: query channel usage for impact: %w", err)
	}
	tokens["input"], tokens["output"], tokens["input_cache_read"], tokens["input_cache_write"] = in, out, cr, cw

	var multiplier decimal.Decimal
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT pa.cost_multiplier FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id WHERE c.id = $1`, channelID,
	).Scan(&multiplier); err != nil {
		return nil, fmt.Errorf("admin: query cost_multiplier: %w", err)
	}
	pc, err := s.loadPriceContext(ctx, nil, []int64{vmID})
	if err != nil {
		return nil, err
	}

	im := &ChangeImpact{WindowDays: windowDays, SellPrice: pc.sellByVM[vmID]}
	var oldSpec storedSpec
	if current != nil {
		oldSpec.Currency = current.Currency
		for _, c := range current.Components {
			oldSpec.Components = append(oldSpec.Components, storedComponent{Meter: c.Meter, Unit: c.Unit, ServiceTier: c.ServiceTier,
				TierMinInput: c.TierMinInput, WindowStartMin: c.WindowStartMin, UnitPrice: c.UnitPrice})
		}
	}
	// costMicro：把一套价格按 7 天 token 量折算成人民币微元。
	costMicro := func(sp storedSpec) (int64, bool) {
		rate := decimal.NewFromInt(1)
		if sp.Currency != "" && sp.Currency != "CNY" {
			r, ok := pc.fx[sp.Currency]
			if !ok {
				return 0, false
			}
			rate = r.rate
		}
		total := decimal.Zero
		for meter, tok := range tokens {
			p := sp.baseUnitPrice(meter)
			if p == nil || tok == 0 {
				continue
			}
			// tokens / 1e6 × 单价（元）× 汇率 × 倍率 × 1e6（微元）= tokens × 单价 × 汇率 × 倍率
			total = total.Add(decimal.NewFromInt(tok).Mul(*p).Mul(rate).Mul(multiplier))
		}
		return total.Round(0).IntPart(), true
	}
	before, ok1 := costMicro(oldSpec)
	after, ok2 := costMicro(proposed)
	im.FXMissing = !ok1 || !ok2
	im.CostBeforeMicro, im.CostAfterMicro, im.CostDeltaMicro = before, after, after-before

	toBrief := func(sp storedSpec) *PriceBrief {
		if len(sp.Components) == 0 {
			return nil
		}
		return &PriceBrief{Currency: sp.Currency, Input: sp.baseUnitPrice("input"), Output: sp.baseUnitPrice("output")}
	}
	im.MarginBefore = marginRatio(im.SellPrice, pc.costInCNY(toBrief(oldSpec), multiplier))
	im.MarginAfter = marginRatio(im.SellPrice, pc.costInCNY(toBrief(proposed), multiplier))
	return im, nil
}

// ---------- 待上架队列 ----------

// ListingSuggestion 是上架表单的预填值：名称 / 系列按上游模型名推断，类型、上下文、最大输出、
// 能力取自来源给出的模型参数（observed_meta），来源没给的为零值，需要运营补填。
type ListingSuggestion struct {
	Name          string           `json:"name"`
	Family        string           `json:"family"`
	Currency      string           `json:"currency"`
	InputPrice    *decimal.Decimal `json:"input_price"`
	OutputPrice   *decimal.Decimal `json:"output_price"`
	Type          string           `json:"type"`
	ContextWindow int              `json:"context_window"`
	MaxOutput     int              `json:"max_output"`
	Capabilities  []string         `json:"capabilities"`
}

// ListingMeta 对应 pending_model_listings.observed_meta（pricesync.ModelMeta 的 JSON 形状）。
type ListingMeta struct {
	Name             string   `json:"name,omitempty"`
	Type             string   `json:"type,omitempty"`
	ContextWindow    int      `json:"context_window,omitempty"`
	MaxOutput        int      `json:"max_output,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Source           string   `json:"source,omitempty"`
}

type PendingListing struct {
	ID            int64             `json:"id"`
	Status        string            `json:"status"`
	ProviderID    int64             `json:"provider_id"`
	ProviderCode  string            `json:"provider_code"`
	ProviderName  string            `json:"provider_name"`
	UpstreamModel string            `json:"upstream_model"`
	SourceID      int64             `json:"source_id"`
	SourceLevel   string            `json:"source_level"`
	ObservedSpec  SpecJSON          `json:"observed_spec"`
	ObservedMeta  *ListingMeta      `json:"observed_meta"`
	Suggested     ListingSuggestion `json:"suggested"`
	// Origin：price_source = 价格源发现的新模型；free_offer = 优惠识别出的免费模型（OfferID 为对应情报）。
	Origin    string     `json:"origin"`
	OfferID   *int64     `json:"offer_id"`
	Free      bool       `json:"free"`       // 观测到的计量项单价全为 0
	Attached  bool       `json:"attached"`   // 上架时复用了已有同名虚拟模型（只挂渠道，售价不变）
	RetiredAt *time.Time `json:"retired_at"` // 上游免费结束、系统自动停用渠道的时间
	// 与上游模型同名的已有虚拟模型（虚拟模型名 = 上游原始模型名）。在用时上架只会挂一个新渠道、售价不变；
	// deprecated 时上架会重新启用并按观测重新定价。
	ExistingVirtualModelID     *int64     `json:"existing_virtual_model_id"`
	ExistingVirtualModelStatus *string    `json:"existing_virtual_model_status"`
	PublishedVirtualModelID    *int64     `json:"published_virtual_model_id"`
	PublishedChannelID         *int64     `json:"published_channel_id"`
	FirstObservedAt            time.Time  `json:"first_observed_at"`
	LastObservedAt             time.Time  `json:"last_observed_at"`
	DecidedAt                  *time.Time `json:"decided_at"`
}

type ListPendingListingsInput struct {
	Status     string // 默认 pending；all = 全部
	ProviderID int64
	Origin     string // price_source / free_offer；空 = 全部
	PageRequest
}

var familySplit = regexp.MustCompile(`[-_.:\s]`)

// suggestFamily：取上游模型名最后一段（去掉 "Qwen/" 这类组织前缀）的第一个词，
// 例如 deepseek-chat → deepseek、Qwen/Qwen3-8B → qwen3；取不到时用供应商 code。
func suggestFamily(upstreamModel, providerCode string) string {
	name := upstreamModel
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if parts := familySplit.Split(strings.ToLower(name), 2); len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return providerCode
}

func (s storedSpec) isFree() bool {
	for _, c := range s.Components {
		if !c.UnitPrice.IsZero() {
			return false
		}
	}
	return len(s.Components) > 0
}

func (s *Service) ListPendingListings(ctx context.Context, in ListPendingListingsInput) (*Page[PendingListing], error) {
	status := in.Status
	if status == "" {
		status = "pending"
	}
	if status == "all" {
		status = ""
	}
	where := `WHERE ($1 = '' OR l.status = $1) AND ($2 = 0 OR l.provider_id = $2) AND ($3 = '' OR l.origin = $3)`
	args := []any{status, in.ProviderID, in.Origin}
	pr := in.PageRequest
	if pr.PageSize == 0 {
		pr.PageSize = 100
	}
	pr = pr.normalize()
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM pending_model_listings l `+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("admin: count pending_model_listings: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT l.id, l.status, l.provider_id, p.code, p.name, l.upstream_model, l.source_id, ps.level, l.observed_spec, l.observed_meta,
		   l.origin, l.offer_id, l.attached, l.retired_at, vm.id, vm.status,
		   l.published_virtual_model_id, l.published_channel_id, l.first_observed_at, l.last_observed_at, l.decided_at
		 FROM pending_model_listings l JOIN providers p ON p.id = l.provider_id JOIN price_sources ps ON ps.id = l.source_id
		 LEFT JOIN virtual_models vm ON vm.name = l.upstream_model `+where+
			` ORDER BY l.first_observed_at, l.id LIMIT $4 OFFSET $5`, append(args, pr.PageSize, pr.offset())...)
	if err != nil {
		return nil, fmt.Errorf("admin: query pending_model_listings: %w", err)
	}
	defer rows.Close()
	out := []PendingListing{}
	for rows.Next() {
		var l PendingListing
		var specRaw, metaRaw []byte
		if err := rows.Scan(&l.ID, &l.Status, &l.ProviderID, &l.ProviderCode, &l.ProviderName, &l.UpstreamModel, &l.SourceID, &l.SourceLevel, &specRaw, &metaRaw,
			&l.Origin, &l.OfferID, &l.Attached, &l.RetiredAt, &l.ExistingVirtualModelID, &l.ExistingVirtualModelStatus,
			&l.PublishedVirtualModelID, &l.PublishedChannelID, &l.FirstObservedAt, &l.LastObservedAt, &l.DecidedAt); err != nil {
			return nil, fmt.Errorf("admin: scan pending_model_listing: %w", err)
		}
		var spec storedSpec
		if err := json.Unmarshal(specRaw, &spec); err != nil {
			return nil, fmt.Errorf("admin: decode observed_spec: %w", err)
		}
		l.ObservedSpec = spec.toJSON()
		l.Free = spec.isFree()
		l.Suggested = ListingSuggestion{
			Name: l.UpstreamModel, Family: suggestFamily(l.UpstreamModel, l.ProviderCode), Currency: spec.Currency,
			InputPrice: spec.baseUnitPrice("input"), OutputPrice: spec.baseUnitPrice("output"), Capabilities: []string{},
		}
		if len(metaRaw) > 0 {
			var m ListingMeta
			if err := json.Unmarshal(metaRaw, &m); err != nil {
				return nil, fmt.Errorf("admin: decode observed_meta: %w", err)
			}
			l.ObservedMeta = &m
			l.Suggested.Type, l.Suggested.ContextWindow, l.Suggested.MaxOutput = m.Type, m.ContextWindow, m.MaxOutput
			if m.MaxOutput > m.ContextWindow && m.ContextWindow > 0 {
				l.Suggested.MaxOutput = m.ContextWindow
			}
			if len(m.Capabilities) > 0 {
				l.Suggested.Capabilities = m.Capabilities
			}
		}
		out = append(out, l)
	}
	return &Page[PendingListing]{Data: out, Total: total, Page: pr.Page, PageSize: pr.PageSize}, rows.Err()
}

// ---------- 待办计数 ----------

type TodoCounts struct {
	PriceChangesPending    int `json:"price_changes_pending"`
	PriceChangesBlocked    int `json:"price_changes_blocked"`
	ListingsPending        int `json:"listings_pending"`
	ChannelsNegativeMargin int `json:"channels_negative_margin"`
	ChannelsMissingCost    int `json:"channels_missing_cost"`
	ModelsMissingSellPrice int `json:"models_missing_sell_price"`
	// 外部数据采集（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md）
	OffersNew          int `json:"offers_new"`
	AliasesSuggested   int `json:"aliases_suggested"`
	DataSourcesFailing int `json:"data_sources_failing"`
}

// GetTodoCounts 给侧栏徽标和工作台待办条用。前三项是走索引的轻量计数；
// 后三项需要算毛利（全量加载 active 渠道），调用方应做短时缓存。
func (s *Service) GetTodoCounts(ctx context.Context) (*TodoCounts, error) {
	var t TodoCounts
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT (SELECT count(*) FROM price_change_requests WHERE status = 'pending'),
		        (SELECT count(*) FROM price_change_requests WHERE status = 'blocked'),
		        (SELECT count(*) FROM pending_model_listings WHERE status = 'pending'),
		        (SELECT count(*) FROM upstream_offers WHERE status = 'new'),
		        (SELECT count(*) FROM model_aliases WHERE status = 'suggested'),
		        (SELECT count(*) FROM price_sources WHERE enabled AND consecutive_failures > 0)`,
	).Scan(&t.PriceChangesPending, &t.PriceChangesBlocked, &t.ListingsPending, &t.OffersNew, &t.AliasesSuggested, &t.DataSourcesFailing); err != nil {
		return nil, fmt.Errorf("admin: query todo counts: %w", err)
	}
	// 负毛利 / 缺成本价 / 缺售价的计数直接用视图聚合（迁移 00023），不再加载全部渠道与模型。
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE margin_ratio < 0), count(*) FILTER (WHERE cost_book_id IS NULL)
		 FROM v_admin_channel_margin WHERE status = 'active'`,
	).Scan(&t.ChannelsNegativeMargin, &t.ChannelsMissingCost); err != nil {
		return nil, fmt.Errorf("admin: count channel todos: %w", err)
	}
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM virtual_models vm JOIN v_admin_model_summary ms ON ms.virtual_model_id = vm.id
		 WHERE vm.status = 'active' AND NOT ms.has_sell_price`,
	).Scan(&t.ModelsMissingSellPrice); err != nil {
		return nil, fmt.Errorf("admin: count model todos: %w", err)
	}
	return &t, nil
}

// ChangeRequestBrief 是批量审批前置检查需要的最小信息。
type ChangeRequestBrief struct {
	Status         string
	MaxChangeRatio decimal.Decimal
}

// ChangeRequestBriefs 一次查出一批调价请求的状态与变化幅度（batch-approve 用），
// 代替逐条 GetChangeRequest（每条约 8 条 SQL，含 7 天 request_logs 扫描）。
func (s *Service) ChangeRequestBriefs(ctx context.Context, ids []int64) (map[int64]ChangeRequestBrief, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT id, status, max_change_ratio FROM price_change_requests WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("admin: query change request briefs: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]ChangeRequestBrief, len(ids))
	for rows.Next() {
		var id int64
		var b ChangeRequestBrief
		if err := rows.Scan(&id, &b.Status, &b.MaxChangeRatio); err != nil {
			return nil, fmt.Errorf("admin: scan change request brief: %w", err)
		}
		out[id] = b
	}
	return out, rows.Err()
}
