// Package offers 是"上游优惠情报"（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §3.2）：
// 抓到的免费模型、降价、限时折扣等先进 upstream_offers 情报库，状态 new，等运营确认；
// 确认后可一键"采用"为一条 promotions。这里的任何数据都不会自动影响计费。
//
// 识别来源（detection）：
//   - structured：价格接口里输入 / 输出单价都为 0（OpenRouter 的 :free、models.dev 的 0 价）；
//   - price_diff：同一来源同一模型的新价格比上一次观测低 20% 以上；
//   - llm_extract：定价页 / 公告页里命中关键词的段落，交给 LLM 结构化抽取（page.go）；
//   - manual：运营手工录入。
package offers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

// 优惠类型，与 upstream_offers.offer_type 一致。
const (
	TypeFreeModel     = "free_model"
	TypeDiscount      = "discount"
	TypeOffPeak       = "off_peak"
	TypeFreeQuota     = "free_quota"
	TypeNewUserCredit = "new_user_credit"
	TypePriceCut      = "price_cut"
)

var Types = []string{TypeFreeModel, TypeDiscount, TypeOffPeak, TypeFreeQuota, TypeNewUserCredit, TypePriceCut}

var Statuses = []string{"new", "confirmed", "ignored", "expired", "adopted"}

// Offer 是一条优惠情报。
type Offer struct {
	ID                 int64            `json:"id"`
	SourceID           *int64           `json:"source_id"`
	SourceName         *string          `json:"source_name,omitempty"`
	ProviderCode       string           `json:"provider_code"`
	UpstreamModel      *string          `json:"upstream_model"`
	OfferType          string           `json:"offer_type"`
	DiscountRatio      *decimal.Decimal `json:"discount_ratio"`
	Quota              json.RawMessage  `json:"quota"`
	Limits             json.RawMessage  `json:"limits"`
	StartsAt           *time.Time       `json:"starts_at"`
	EndsAt             *time.Time       `json:"ends_at"`
	Conditions         *string          `json:"conditions"`
	EvidenceURL        *string          `json:"evidence_url"`
	EvidenceExcerpt    *string          `json:"evidence_excerpt"`
	Detection          string           `json:"detection"`
	Status             string           `json:"status"`
	AdoptedPromotionID *int64           `json:"adopted_promotion_id"`
	DecidedByName      *string          `json:"decided_by_name"`
	DecidedAt          *time.Time       `json:"decided_at"`
	FirstSeenAt        time.Time        `json:"first_seen_at"`
	LastSeenAt         time.Time        `json:"last_seen_at"`
	// 同一供应商 + 上游模型的待上架候选（免费模型会自动进待上架）；平台没有该供应商或还没有候选时为 nil。
	ListingID     *int64  `json:"listing_id"`
	ListingStatus *string `json:"listing_status"`
}

// Candidate 是检测器产出的一条待入库情报。
type Candidate struct {
	SourceID        *int64
	ProviderCode    string
	UpstreamModel   string // "" = 账号级
	OfferType       string
	DiscountRatio   *decimal.Decimal
	Quota           map[string]any
	Limits          map[string]any
	StartsAt        *time.Time
	EndsAt          *time.Time
	Conditions      string
	EvidenceURL     string
	EvidenceExcerpt string
	Detection       string
}

// Fingerprint 决定"同一条优惠"：免费模型按 来源+厂商+模型；降价带上新价格；其余带上时间窗与折扣。
func (c Candidate) Fingerprint() []byte {
	parts := []string{c.ProviderCode, c.UpstreamModel, c.OfferType}
	switch c.OfferType {
	case TypeFreeModel:
	default:
		if c.DiscountRatio != nil {
			parts = append(parts, c.DiscountRatio.StringFixed(4))
		}
		if c.EndsAt != nil {
			parts = append(parts, c.EndsAt.UTC().Format(time.DateOnly))
		}
		if c.Conditions != "" && c.DiscountRatio == nil {
			parts = append(parts, strings.ToLower(strings.Join(strings.Fields(c.Conditions), " ")))
		}
	}
	if c.SourceID != nil {
		parts = append(parts, fmt.Sprint(*c.SourceID))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return sum[:]
}

var (
	ErrNotFound      = errors.New("offers: offer not found")
	ErrInvalidStatus = errors.New("offers: invalid status transition")
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) store.Querier { return store.Q(ctx, s.pool) }

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func jsonOrNil(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

// Upsert 写入一条情报：同指纹已存在时刷新 last_seen_at 与证据；已过期的重新出现时回到 new。
// 返回 created=true 表示是新情报。
func (s *Store) Upsert(ctx context.Context, c Candidate) (id int64, created bool, err error) {
	if c.ProviderCode == "" || c.OfferType == "" || c.Detection == "" {
		return 0, false, errors.New("offers: provider_code, offer_type and detection are required")
	}
	err = s.db(ctx).QueryRow(ctx,
		`INSERT INTO upstream_offers (source_id, provider_code, upstream_model, offer_type, discount_ratio, quota, limits,
			starts_at, ends_at, conditions, evidence_url, evidence_excerpt, detection, fingerprint)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (fingerprint) DO UPDATE SET
			last_seen_at = now(),
			evidence_url = COALESCE(EXCLUDED.evidence_url, upstream_offers.evidence_url),
			evidence_excerpt = COALESCE(EXCLUDED.evidence_excerpt, upstream_offers.evidence_excerpt),
			limits = COALESCE(EXCLUDED.limits, upstream_offers.limits),
			status = CASE WHEN upstream_offers.status = 'expired' THEN 'new' ELSE upstream_offers.status END
		 RETURNING id, (xmax = 0)`,
		c.SourceID, c.ProviderCode, nullIfEmpty(c.UpstreamModel), c.OfferType, c.DiscountRatio, jsonOrNil(c.Quota), jsonOrNil(c.Limits),
		c.StartsAt, c.EndsAt, nullIfEmpty(c.Conditions), nullIfEmpty(c.EvidenceURL), nullIfEmpty(truncate(c.EvidenceExcerpt, 2000)),
		c.Detection, c.Fingerprint(),
	).Scan(&id, &created)
	if err != nil {
		return 0, false, fmt.Errorf("offers: upsert: %w", err)
	}
	return id, created, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// TouchFreeModels 刷新某来源仍有效的免费模型情报的 last_seen_at（来源内容没变、跳过解析时调用，
// 否则它们会被 ExpireStale 误判为已下线）。
func (s *Store) TouchFreeModels(ctx context.Context, sourceID int64) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE upstream_offers SET last_seen_at = now()
		 WHERE source_id = $1 AND offer_type = 'free_model' AND status IN ('new','confirmed')`, sourceID)
	if err != nil {
		return fmt.Errorf("offers: touch free models: %w", err)
	}
	return nil
}

// ExpireMissingFreeModels 把某来源这次没再出现的免费模型情报置为 expired——
// 调用方传入本次运行开始时间：last_seen_at 早于它的就是这次没看到的。
func (s *Store) ExpireMissingFreeModels(ctx context.Context, sourceID int64, runStart time.Time) (int64, error) {
	tag, err := s.db(ctx).Exec(ctx,
		`UPDATE upstream_offers SET status = 'expired'
		 WHERE source_id = $1 AND offer_type = 'free_model' AND status IN ('new','confirmed') AND last_seen_at < $2`,
		sourceID, runStart)
	if err != nil {
		return 0, fmt.Errorf("offers: expire missing free models: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ExpireEnded 把 ends_at 已过的情报置为 expired（采用过的不动，促销本身有自己的 ends_at）。
func (s *Store) ExpireEnded(ctx context.Context) (int64, error) {
	tag, err := s.db(ctx).Exec(ctx,
		`UPDATE upstream_offers SET status = 'expired' WHERE status IN ('new','confirmed') AND ends_at IS NOT NULL AND ends_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("offers: expire ended: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListInput 是情报列表的过滤条件。
type ListInput struct {
	Status       string
	OfferType    string
	ProviderCode string
	Query        string // 模糊匹配模型名
	Limit        int
	Offset       int
}

const offerCols = `o.id, o.source_id, ps.name, o.provider_code, o.upstream_model, o.offer_type, o.discount_ratio, o.quota, o.limits,
	o.starts_at, o.ends_at, o.conditions, o.evidence_url, o.evidence_excerpt, o.detection, o.status, o.adopted_promotion_id,
	o.decided_by_name, o.decided_at, o.first_seen_at, o.last_seen_at, pl.id, pl.status`

// offerFrom 带上数据源名与对应的待上架候选（按供应商 code + 模型名匹配，一对一）。
const offerFrom = ` FROM upstream_offers o LEFT JOIN price_sources ps ON ps.id = o.source_id
	LEFT JOIN LATERAL (SELECT l.id, l.status FROM pending_model_listings l JOIN providers p ON p.id = l.provider_id
	                   WHERE p.code = o.provider_code AND l.upstream_model = o.upstream_model LIMIT 1) pl ON true `

func scanOffer(row pgx.Row) (*Offer, error) {
	o := &Offer{}
	var quota, limits []byte
	if err := row.Scan(&o.ID, &o.SourceID, &o.SourceName, &o.ProviderCode, &o.UpstreamModel, &o.OfferType, &o.DiscountRatio,
		&quota, &limits, &o.StartsAt, &o.EndsAt, &o.Conditions, &o.EvidenceURL, &o.EvidenceExcerpt, &o.Detection, &o.Status,
		&o.AdoptedPromotionID, &o.DecidedByName, &o.DecidedAt, &o.FirstSeenAt, &o.LastSeenAt, &o.ListingID, &o.ListingStatus); err != nil {
		return nil, err
	}
	if len(quota) > 0 {
		o.Quota = quota
	}
	if len(limits) > 0 {
		o.Limits = limits
	}
	return o, nil
}

// List 返回情报列表与总数，新发现的在前。
func (s *Store) List(ctx context.Context, in ListInput) ([]Offer, int, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	where := `WHERE ($1 = '' OR o.status = $1) AND ($2 = '' OR o.offer_type = $2) AND ($3 = '' OR o.provider_code = $3)
		AND ($4 = '' OR o.upstream_model ILIKE '%' || $4 || '%')`
	args := []any{in.Status, in.OfferType, in.ProviderCode, in.Query}
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM upstream_offers o `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("offers: count: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT `+offerCols+offerFrom+where+
			` ORDER BY o.first_seen_at DESC, o.id DESC LIMIT $5 OFFSET $6`, append(args, in.Limit, in.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("offers: list: %w", err)
	}
	defer rows.Close()
	out := []Offer{}
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("offers: scan: %w", err)
		}
		out = append(out, *o)
	}
	return out, total, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (*Offer, error) {
	o, err := scanOffer(s.db(ctx).QueryRow(ctx,
		`SELECT `+offerCols+offerFrom+`WHERE o.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("offers: get: %w", err)
	}
	return o, nil
}

// Counts 返回各状态的条数（待办角标用）。
func (s *Store) CountNew(ctx context.Context) (int, error) {
	var n int
	err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM upstream_offers WHERE status = 'new'`).Scan(&n)
	return n, err
}

// SetStatus 人工确认 / 忽略 / 重新打开一条情报。adopted 只能经由 Adopt 设置。
func (s *Store) SetStatus(ctx context.Context, id int64, status, by string) (*Offer, error) {
	switch status {
	case "confirmed", "ignored", "new":
	default:
		return nil, fmt.Errorf("%w: cannot set status %q", ErrInvalidStatus, status)
	}
	tag, err := s.db(ctx).Exec(ctx,
		`UPDATE upstream_offers SET status = $2, decided_by_name = NULLIF($3, ''), decided_at = now()
		 WHERE id = $1 AND status <> 'adopted'`, id, status, by)
	if err != nil {
		return nil, fmt.Errorf("offers: set status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: offer already adopted", ErrInvalidStatus)
	}
	return s.Get(ctx, id)
}

// AdoptInput 描述把情报采用为一条 promotions。
type AdoptInput struct {
	// Side：cost = 记录我方上游成本优惠（渠道维度，供毛利核算参考，当前计费引擎不读取 cost 面）；
	// sell = 对用户让利（虚拟模型维度，立即参与计费）。
	Side          string
	ChannelID     *int64 // side=cost 时必填
	VirtualModel  string // side=sell 时必填
	Name          string
	DiscountRatio *decimal.Decimal // 覆盖情报里的折扣（价格乘数，0 = 免费）
	StartsAt      *time.Time
	EndsAt        *time.Time
	Priority      int
	BudgetTotal   *int64
	DecidedByName string
}

var ErrAdoptInvalid = errors.New("offers: invalid adopt request")

// Adopt 用情报生成一条 promotions 并把情报标记为 adopted（同一事务）。
func (s *Store) Adopt(ctx context.Context, id int64, in AdoptInput) (promotionID int64, err error) {
	err = store.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		o, err := scanOffer(s.db(ctx).QueryRow(ctx,
			`SELECT `+offerCols+offerFrom+`WHERE o.id = $1 FOR UPDATE OF o`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("offers: lock offer: %w", err)
		}
		if o.Status == "adopted" || o.Status == "expired" || o.Status == "ignored" {
			return fmt.Errorf("%w: offer is %s", ErrInvalidStatus, o.Status)
		}
		ratio := in.DiscountRatio
		if ratio == nil {
			ratio = o.DiscountRatio
		}
		if ratio == nil || ratio.IsNegative() || ratio.GreaterThan(decimal.NewFromInt(1)) {
			return fmt.Errorf("%w: discount_ratio must be between 0 and 1 (price multiplier)", ErrAdoptInvalid)
		}
		starts := time.Now()
		if in.StartsAt != nil {
			starts = *in.StartsAt
		} else if o.StartsAt != nil && o.StartsAt.After(starts) {
			starts = *o.StartsAt
		}
		ends := in.EndsAt
		if ends == nil {
			ends = o.EndsAt
		}
		if ends != nil && !ends.After(starts) {
			return fmt.Errorf("%w: ends_at must be after starts_at", ErrAdoptInvalid)
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			model := ""
			if o.UpstreamModel != nil {
				model = " " + *o.UpstreamModel
			}
			name = fmt.Sprintf("[情报#%d] %s%s %s", o.ID, o.ProviderCode, model, o.OfferType)
		}
		// promotions.params.discount 是"省掉的比例"（internal/promotion：saved = list × discount），
		// 情报里存的是价格乘数，两者互补。
		saved := decimal.NewFromInt(1).Sub(*ratio)
		var side, typ string
		var scope map[string]any
		switch in.Side {
		case "cost":
			if in.ChannelID == nil {
				return fmt.Errorf("%w: channel_id is required for side=cost", ErrAdoptInvalid)
			}
			side, scope = "cost", map[string]any{"channels": []int64{*in.ChannelID}}
			typ = "cost_discount"
			if ratio.IsZero() {
				typ = "cost_free"
			}
		case "sell":
			vm := strings.TrimSpace(in.VirtualModel)
			if vm == "" {
				return fmt.Errorf("%w: virtual_model is required for side=sell", ErrAdoptInvalid)
			}
			var exists bool
			if err := s.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM virtual_models WHERE name = $1)`, vm).Scan(&exists); err != nil {
				return fmt.Errorf("offers: check virtual model: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: virtual model %q does not exist", ErrAdoptInvalid, vm)
			}
			if saved.IsZero() {
				return fmt.Errorf("%w: a sell-side promotion needs a discount (ratio < 1)", ErrAdoptInvalid)
			}
			side, typ, scope = "sell", "price_discount", map[string]any{"models": []string{vm}}
		default:
			return fmt.Errorf("%w: side must be cost or sell", ErrAdoptInvalid)
		}
		params := map[string]any{"discount": saved.InexactFloat64(), "source_offer_id": o.ID}
		if err := s.db(ctx).QueryRow(ctx,
			`INSERT INTO promotions (name, side, type, priority, scope, params, budget_total, starts_at, ends_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			name, side, typ, in.Priority, scope, params, in.BudgetTotal, starts, ends,
		).Scan(&promotionID); err != nil {
			return fmt.Errorf("offers: insert promotion: %w", err)
		}
		if _, err := s.db(ctx).Exec(ctx,
			`UPDATE upstream_offers SET status = 'adopted', adopted_promotion_id = $2, decided_by_name = NULLIF($3, ''), decided_at = now()
			 WHERE id = $1`, id, promotionID, in.DecidedByName); err != nil {
			return fmt.Errorf("offers: mark adopted: %w", err)
		}
		return nil
	})
	return promotionID, err
}
