package pricesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

// CostPricePublisher 是 Engine 发布一条自动通过/审批通过的成本价变更所需的
// 最小接口，而不是整个 *admin.Service——刻意保持窄接口，pricesync 不应该因为
// admin 包新增无关方法而被动跟着改。*admin.Service 满足这个接口。
type CostPricePublisher interface {
	SetCostPrice(ctx context.Context, in admin.SetCostPriceInput) (int64, error)
}

// Engine 把 Validate/Diff/DecidePolicy 接到真实数据库上：读当前生效的成本价、
// 写观测记录、写/更新变更提案、在自动通过或人工批准时调用 Publisher 真正发布
// 新价格版本。
type Engine struct {
	pool      *pgxpool.Pool
	publisher ListingPublisher
}

func NewEngine(pool *pgxpool.Pool, publisher ListingPublisher) *Engine {
	return &Engine{pool: pool, publisher: publisher}
}

var validSourceKinds = map[string]bool{"api": true, "html": true, "dataset": true, "billing": true, "manual": true}
var validLevels = map[Level]bool{LevelL1: true, LevelL2: true, LevelL3: true, LevelL4: true, LevelL5: true}

// CreateSourceInput 对应一条 price_sources（技术方案 §7.16.4）。调度（cron）、
// 抓取配置（config JSONB）没有在这里暴露——本阶段没有调度器去读它们（见包级
// 注释），先只暴露发起一次 Ingest 所必须的最小字段。
type CreateSourceInput struct {
	ProviderID *int64 // nil = 不关联具体 provider（比如跨厂商的社区数据集）
	Level      Level
	Kind       string // api / html / dataset / billing / manual
	Fetcher    string // 插件名，纯标识用途，不要求真的注册了同名 Fetcher 实现
	URL        string
}

// CreateSource 注册一个价格来源。这是 Ingest 的前置步骤——price_observations
// / price_change_requests 都要求一个真实存在的 source_id 才能落库审计。
func (e *Engine) CreateSource(ctx context.Context, in CreateSourceInput) (int64, error) {
	if !validLevels[in.Level] {
		return 0, fmt.Errorf("pricesync: invalid level %q, want L1-L5", in.Level)
	}
	if !validSourceKinds[in.Kind] {
		return 0, fmt.Errorf("pricesync: invalid kind %q", in.Kind)
	}
	if in.Fetcher == "" {
		return 0, errors.New("pricesync: fetcher name is required")
	}
	var id int64
	if err := e.db(ctx).QueryRow(ctx,
		`INSERT INTO price_sources (provider_id, level, kind, fetcher, url) VALUES ($1, $2, $3, $4, NULLIF($5, '')) RETURNING id`,
		in.ProviderID, string(in.Level), in.Kind, in.Fetcher, in.URL,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("pricesync: insert price_source: %w", err)
	}
	return id, nil
}

type IngestInput struct {
	ChannelID     int64
	SourceID      int64
	Level         Level
	UpstreamModel string
	Spec          PriceSpec
	RawObject     string // 原始内容/证据，供审计；可以为空
}

// IngestResult 描述这次 Ingest 调用实际发生了什么。ChangeRequestID 为 nil 有
// 三种可能：L3 来源还在等第二次确认观测；提案的价格和当前生效价格完全一致，
// 没有任何变化；两者都不是的话就一定会生成一条 change request。
type IngestResult struct {
	ObservationID   int64
	ChangeRequestID *int64
	Decision        Decision // 只有生成了 change request 才有意义
	AppliedBookID   *int64   // 非 nil 表示这次直接发布生效了（auto_approved）
}

// Ingest 处理一条新的价格观测（技术方案 §7.16.3 的 Normalizer 之后、Mapper 已经
// 把 upstream_model 解析到具体 channel 之后的全部流程）：记观测 -> （L3 还需要
// 二次确认）-> 和当前生效价比较 -> 校验 -> 生效策略判定 -> 写变更提案 ->
// 自动通过的直接发布。
func (e *Engine) Ingest(ctx context.Context, in IngestInput) (*IngestResult, error) {
	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return nil, fmt.Errorf("pricesync: marshal spec: %w", err)
	}
	hash := sha256.Sum256(specJSON)

	var obsID int64
	if err := e.db(ctx).QueryRow(ctx,
		`INSERT INTO price_observations (source_id, upstream_model, spec, spec_hash, raw_object)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')) RETURNING id`,
		in.SourceID, in.UpstreamModel, specJSON, hash[:], in.RawObject,
	).Scan(&obsID); err != nil {
		return nil, fmt.Errorf("pricesync: insert observation: %w", err)
	}
	result := &IngestResult{ObservationID: obsID}

	if in.Level == LevelL3 {
		confirmed, err := e.hasMatchingPriorObservation(ctx, in.SourceID, in.UpstreamModel, hash[:], obsID)
		if err != nil {
			return nil, err
		}
		if !confirmed {
			return result, nil // 记下来了，等下一次抓取确认后再生成提案（技术方案 §7.16.6）
		}
	}

	current, currentBookID, err := e.loadCurrentCostComponents(ctx, in.ChannelID)
	if err != nil {
		return nil, err
	}

	diff := Diff(current, in.Spec)
	if !diff.HasChanges() {
		return result, nil // 价格和现状完全一致，没有什么好提议的
	}

	issues := Validate(current, in.Spec)
	conflictIssues, err := e.checkCrossSourceConflict(ctx, in.SourceID, in.UpstreamModel, in.Spec)
	if err != nil {
		return nil, err
	}
	issues = append(issues, conflictIssues...)

	decision := DecidePolicy(in.Level, diff, issues)
	result.Decision = decision

	diffJSON, err := json.Marshal(diffPayload{Components: diff.Components, Issues: issues})
	if err != nil {
		return nil, fmt.Errorf("pricesync: marshal diff: %w", err)
	}

	effectiveFrom := time.Now()
	if in.Spec.EffectiveFrom != nil {
		effectiveFrom = *in.Spec.EffectiveFrom
	}

	var crID int64
	if err := e.db(ctx).QueryRow(ctx,
		`INSERT INTO price_change_requests
		    (channel_id, current_book_id, proposed_spec, diff, max_change_ratio, direction, evidence, effective_from, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		in.ChannelID, currentBookID, specJSON, diffJSON, diff.MaxChangeRatio, string(diff.Direction),
		[]int64{obsID}, effectiveFrom, string(decision),
	).Scan(&crID); err != nil {
		return nil, fmt.Errorf("pricesync: insert change request: %w", err)
	}
	result.ChangeRequestID = &crID

	if decision == DecisionAutoApproved {
		bookID, err := e.apply(ctx, crID, in.ChannelID, in.Spec, effectiveFrom)
		if err != nil {
			return nil, err
		}
		result.AppliedBookID = &bookID
	}
	return result, nil
}

// diffPayload 是 price_change_requests.diff 列存的 JSON 形状：逐计量项对比 +
// 校验阶段产生的告警/强制审批理由（拦截类的问题不会走到这里——拦截直接让
// Decision=blocked，但拦截的理由本身也值得让审批人看到，所以同样记进 Issues）。
type diffPayload struct {
	Components []ComponentDiff   `json:"components"`
	Issues     []ValidationIssue `json:"issues"`
}

var (
	ErrChangeRequestNotFound   = errors.New("pricesync: change request not found")
	ErrChangeRequestNotPending = errors.New("pricesync: change request is not pending or blocked")
	// ErrBlockedNeedsConfirm：批准 blocked 提案必须显式确认（DecisionMeta.ConfirmBlocked），
	// 把前端"输入确认"的防误操作在服务端再兜一层（运营后台接口方案 §5.3）。
	ErrBlockedNeedsConfirm = errors.New("pricesync: change request is blocked, approving it requires confirm_blocked=true")
)

// DecisionMeta 是人工审批时记录的"谁、为什么"。By/ByName 是已认证管理员的
// ID 与名称（internal/adminauth），0 表示 system。
type DecisionMeta struct {
	By             int64
	ByName         string
	Reason         string
	ConfirmBlocked bool // 只对 Approve 有意义
}

// Approve 人工批准一条 pending 或 blocked 状态的变更提案并立即发布
// （技术方案 §7.16.10）。blocked 状态也允许批准——拦截是让人去看一眼、不是
// 永久禁止，人工确认过"这真的是厂商在这么调价，不是解析错误"之后应该能放行。
//
// 整个审批在一个事务里完成，并先对变更请求行加 FOR UPDATE 锁：两个运营同时
// 点"批准"时，第二个会等第一个提交后看到 status=applied 而返回
// ErrChangeRequestNotPending，不会发布两本成本价。
func (e *Engine) Approve(ctx context.Context, changeRequestID int64, meta DecisionMeta) (int64, error) {
	var bookID int64
	err := store.RunInTx(ctx, e.pool, func(ctx context.Context) error {
		var channelID int64
		var specJSON []byte
		var effectiveFrom time.Time
		var status string
		err := e.db(ctx).QueryRow(ctx,
			`SELECT channel_id, proposed_spec, effective_from, status FROM price_change_requests WHERE id = $1 FOR UPDATE`,
			changeRequestID,
		).Scan(&channelID, &specJSON, &effectiveFrom, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrChangeRequestNotFound
		}
		if err != nil {
			return fmt.Errorf("pricesync: load change request: %w", err)
		}
		if status != string(DecisionPending) && status != string(DecisionBlocked) {
			return ErrChangeRequestNotPending
		}
		if status == string(DecisionBlocked) && !meta.ConfirmBlocked {
			return ErrBlockedNeedsConfirm
		}

		var spec PriceSpec
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			return fmt.Errorf("pricesync: unmarshal proposed_spec: %w", err)
		}

		if bookID, err = e.apply(ctx, changeRequestID, channelID, spec, effectiveFrom); err != nil {
			return err
		}
		if _, err := e.db(ctx).Exec(ctx,
			`UPDATE price_change_requests SET decided_by = $2, decided_by_name = NULLIF($3, ''), decision_reason = NULLIF($4, ''), decided_at = now() WHERE id = $1`,
			changeRequestID, meta.By, meta.ByName, meta.Reason,
		); err != nil {
			return fmt.Errorf("pricesync: record decision: %w", err)
		}
		return nil
	})
	return bookID, err
}

// Reject 驳回一条 pending 或 blocked 状态的变更提案，不发布任何新价格。
// 与 Approve 一样先锁行再判断状态，避免与并发的批准交错。
func (e *Engine) Reject(ctx context.Context, changeRequestID int64, meta DecisionMeta) error {
	return store.RunInTx(ctx, e.pool, func(ctx context.Context) error {
		var status string
		err := e.db(ctx).QueryRow(ctx, `SELECT status FROM price_change_requests WHERE id = $1 FOR UPDATE`, changeRequestID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrChangeRequestNotFound
		}
		if err != nil {
			return fmt.Errorf("pricesync: load change request: %w", err)
		}
		if status != string(DecisionPending) && status != string(DecisionBlocked) {
			return ErrChangeRequestNotPending
		}
		if _, err := e.db(ctx).Exec(ctx,
			`UPDATE price_change_requests SET status = 'rejected', decided_by = $2, decided_by_name = NULLIF($3, ''), decision_reason = NULLIF($4, ''), decided_at = now() WHERE id = $1`,
			changeRequestID, meta.By, meta.ByName, meta.Reason,
		); err != nil {
			return fmt.Errorf("pricesync: reject change request: %w", err)
		}
		return nil
	})
}

// apply 是 Ingest（auto_approved）和 Approve 共用的"真正发布"步骤：把 PriceSpec
// 转换成 admin.SetCostPriceInput 发布一个新的 cost price_book 版本，再把这条
// change request 标记为 applied。
//
// 发布价格与标记 applied 在同一个事务里：不会出现"价格已发布但请求仍是 pending"
// 的中间态（否则可能被再批准一次）。
func (e *Engine) apply(ctx context.Context, changeRequestID, channelID int64, spec PriceSpec, effectiveFrom time.Time) (int64, error) {
	var bookID int64
	err := store.RunInTx(ctx, e.pool, func(ctx context.Context) error {
		components := toAdminComponents(spec.Components)
		ef := effectiveFrom
		var err error
		bookID, err = e.publisher.SetCostPrice(ctx, admin.SetCostPriceInput{
			ChannelID: channelID, Currency: spec.Currency, EffectiveFrom: &ef, Components: components,
		})
		if err != nil {
			return fmt.Errorf("pricesync: publish cost price: %w", err)
		}
		if _, err := e.db(ctx).Exec(ctx,
			`UPDATE price_change_requests SET status = 'applied', applied_book_id = $2 WHERE id = $1`,
			changeRequestID, bookID,
		); err != nil {
			return fmt.Errorf("pricesync: mark change request applied: %w", err)
		}
		return nil
	})
	return bookID, err
}

// ChangeRequestSummary 是 ListPending 的返回行，只包含审批界面列表视图需要的
// 字段——完整的 diff/proposed_spec 留给"查看详情"再单独查（本阶段没有实现
// 那个接口，见包级注释）。
type ChangeRequestSummary struct {
	ID             int64
	ChannelID      int64
	Direction      Direction
	MaxChangeRatio decimal.Decimal
	Status         string
	EffectiveFrom  time.Time
	CreatedAt      time.Time
}

// ListPending 列出所有等待人工处理的变更提案（pending 和 blocked 都算，两者都
// 需要人看一眼才能继续，见 Approve 的注释）。
func (e *Engine) ListPending(ctx context.Context) ([]ChangeRequestSummary, error) {
	rows, err := e.db(ctx).Query(ctx,
		`SELECT id, channel_id, direction, max_change_ratio, status, effective_from, created_at
		 FROM price_change_requests WHERE status IN ('pending','blocked') ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("pricesync: query pending change requests: %w", err)
	}
	defer rows.Close()

	var out []ChangeRequestSummary
	for rows.Next() {
		var c ChangeRequestSummary
		var direction string
		if err := rows.Scan(&c.ID, &c.ChannelID, &direction, &c.MaxChangeRatio, &c.Status, &c.EffectiveFrom, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("pricesync: scan change request: %w", err)
		}
		c.Direction = Direction(direction)
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadCurrentCostComponents 加载渠道当前生效的成本价分量（用作 Diff 的基线）。
// 没有任何生效的成本价时返回 (nil, nil, nil)——这不是错误，Diff 会把它判定成
// "new"（技术方案里的"新模型"场景）。
func (e *Engine) loadCurrentCostComponents(ctx context.Context, channelID int64) ([]Component, *int64, error) {
	var bookID int64
	err := e.db(ctx).QueryRow(ctx,
		`SELECT id FROM price_books
		 WHERE kind = 'cost' AND channel_id = $1 AND effective_from <= now()
		   AND (effective_to IS NULL OR effective_to > now())
		 ORDER BY effective_from DESC LIMIT 1`,
		channelID,
	).Scan(&bookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("pricesync: load current cost price_book: %w", err)
	}

	rows, err := e.db(ctx).Query(ctx,
		`SELECT meter, unit, service_tier, tier_min_input, tier_max_input, window_start_min, window_end_min, unit_price
		 FROM price_components WHERE price_book_id = $1`, bookID)
	if err != nil {
		return nil, nil, fmt.Errorf("pricesync: load current cost price_components: %w", err)
	}
	defer rows.Close()

	var components []Component
	for rows.Next() {
		var meter, unit, tier string
		var tierMin int
		var tierMax *int
		var winStart, winEnd *int16
		var price decimal.Decimal
		if err := rows.Scan(&meter, &unit, &tier, &tierMin, &tierMax, &winStart, &winEnd, &price); err != nil {
			return nil, nil, fmt.Errorf("pricesync: scan price_component: %w", err)
		}
		components = append(components, Component{
			Meter: pricing.Meter(meter), Unit: pricing.Unit(unit), ServiceTier: tier,
			TierMinInput: tierMin, TierMaxInput: tierMax, WindowStartMin: winStart, WindowEndMin: winEnd,
			UnitPrice: price,
		})
	}
	return components, &bookID, rows.Err()
}

// hasMatchingPriorObservation 实现"L3 需要连续 2 次抓取结果一致才生成提案"
// （技术方案 §7.16.6）：查这个来源对这个模型的上一条观测（排除本次刚插入的），
// 比较 spec_hash 是否相同。第一次观测（没有"上一条"）视为未确认。
func (e *Engine) hasMatchingPriorObservation(ctx context.Context, sourceID int64, upstreamModel string, hash []byte, excludeID int64) (bool, error) {
	var priorHash []byte
	err := e.db(ctx).QueryRow(ctx,
		`SELECT spec_hash FROM price_observations
		 WHERE source_id = $1 AND upstream_model = $2 AND id != $3
		 ORDER BY observed_at DESC LIMIT 1`,
		sourceID, upstreamModel, excludeID,
	).Scan(&priorHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pricesync: load prior observation: %w", err)
	}
	return bytes.Equal(priorHash, hash), nil
}

// checkCrossSourceConflict 是技术方案 §7.16.6"多来源冲突"规则的简化实现：原文
// 是"L2 与 L4 差异 > 5%"，这里放宽成"同一 upstream_model 在过去 24 小时内、
// 任意其它来源的最新观测，只要有任一共同计量项差异 > 5%"就强制人工审批
// （不区分具体是哪两个级别在冲突——级别判断留给审批人看 diff 里的说明）。
func (e *Engine) checkCrossSourceConflict(ctx context.Context, sourceID int64, upstreamModel string, proposed PriceSpec) ([]ValidationIssue, error) {
	rows, err := e.db(ctx).Query(ctx,
		`SELECT DISTINCT ON (source_id) source_id, spec
		 FROM price_observations
		 WHERE upstream_model = $1 AND source_id != $2 AND observed_at > now() - interval '24 hours'
		 ORDER BY source_id, observed_at DESC`,
		upstreamModel, sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("pricesync: query cross-source observations: %w", err)
	}
	defer rows.Close()

	proposedByKey := indexComponents(proposed.Components)
	const conflictThreshold = 0.05
	var issues []ValidationIssue
	for rows.Next() {
		var otherSourceID int64
		var specJSON []byte
		if err := rows.Scan(&otherSourceID, &specJSON); err != nil {
			return nil, fmt.Errorf("pricesync: scan cross-source observation: %w", err)
		}
		var otherSpec PriceSpec
		if err := json.Unmarshal(specJSON, &otherSpec); err != nil {
			continue // 畸形历史数据不应该挡住这次提案
		}
		for _, oc := range otherSpec.Components {
			pc, ok := proposedByKey[componentKey(oc.Meter, oc.ServiceTier, oc.TierMinInput)]
			if !ok || oc.UnitPrice.IsZero() {
				continue
			}
			relDiff := pc.UnitPrice.Sub(oc.UnitPrice).Div(oc.UnitPrice).Abs()
			if relDiff.GreaterThan(decimal.NewFromFloat(conflictThreshold)) {
				issues = append(issues, ValidationIssue{
					Rule:     "source_conflict",
					Severity: IssueForcesReview,
					Message: fmt.Sprintf("%s price %s differs by more than 5%% from source %d's recent observation %s",
						oc.Meter, pc.UnitPrice, otherSourceID, oc.UnitPrice),
				})
			}
		}
	}
	return issues, rows.Err()
}

// db 返回 ctx 里的环境事务，没有时返回连接池（见 store.RunInTx）。
func (e *Engine) db(ctx context.Context) store.Querier { return store.Q(ctx, e.pool) }
