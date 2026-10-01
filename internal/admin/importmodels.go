package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

// 批量导入上游模型（B7）：替代前端接入向导逐个模型"先查后建"的 5~6 次请求。
// 服务端统一用 decimal 计算售价与毛利（口径同 PricingPreview），每个模型在
// 调用方给的事务里原子完成：建/复用虚拟模型 → 建/复用渠道 → 发布成本价 →
// （可选）发布售价。dry_run 只计算预览与平台现状，不写库。

type ImportModelItem struct {
	UpstreamModel string           `json:"upstream_model"`
	Name          string           `json:"name"` // 虚拟模型名，空 = upstream_model
	Family        string           `json:"family"`
	Type          string           `json:"type"` // 空 = chat
	ContextWindow int              `json:"context_window"`
	MaxOutput     int              `json:"max_output"`
	Capabilities  []string         `json:"capabilities"` // 空 = ["stream"]
	VisibleTiers  []string         `json:"visible_tiers"`
	CostInput     *decimal.Decimal `json:"cost_input"`
	CostOutput    *decimal.Decimal `json:"cost_output"`
	MarkupPercent *decimal.Decimal `json:"markup_percent"` // 覆盖全局加价
	SellInput     *decimal.Decimal `json:"sell_input"`     // 手工售价，覆盖自动计算
	SellOutput    *decimal.Decimal `json:"sell_output"`
	// KeepExistingSell 为 true 且虚拟模型已存在时不发布新售价（保留现有售价）。
	KeepExistingSell bool `json:"keep_existing_sell"`
}

type ImportModelsInput struct {
	ProviderAccountID int64             `json:"-"`
	Currency          string            `json:"currency"` // 成本价币种，默认 USD
	MarkupPercent     decimal.Decimal   `json:"markup_percent"`
	Items             []ImportModelItem `json:"items"`
}

// ImportPlatformStatus 是某个上游模型在本平台的现状。
type ImportPlatformStatus string

const (
	ImportStatusNew      ImportPlatformStatus = "new"       // 虚拟模型与渠道都不存在
	ImportStatusVMExists ImportPlatformStatus = "vm_exists" // 有同名虚拟模型，此账号还没有对应渠道
	ImportStatusListed   ImportPlatformStatus = "listed"    // 渠道已存在
)

type ImportModelPlan struct {
	UpstreamModel    string               `json:"upstream_model"`
	Name             string               `json:"name"`
	Status           ImportPlatformStatus `json:"status"`
	VirtualModelID   *int64               `json:"virtual_model_id"`
	ChannelID        *int64               `json:"channel_id"`
	CostInputCNY     *decimal.Decimal     `json:"cost_input_cny"`
	CostOutputCNY    *decimal.Decimal     `json:"cost_output_cny"`
	SellInput        *decimal.Decimal     `json:"sell_input"`
	SellOutput       *decimal.Decimal     `json:"sell_output"`
	MarginRatio      *decimal.Decimal     `json:"margin_ratio"`
	PublishSellPrice bool                 `json:"publish_sell_price"`
	Errors           []string             `json:"errors"`
}

type ImportModelResult struct {
	VirtualModelID int64  `json:"virtual_model_id"`
	ChannelID      int64  `json:"channel_id"`
	CreatedVM      bool   `json:"created_vm"`
	CreatedChannel bool   `json:"created_channel"`
	CostBookID     int64  `json:"cost_book_id"`
	SellBookID     *int64 `json:"sell_book_id"`
}

const maxImportItems = 200

// PlanImport 校验输入并为每个条目算出导入计划（平台现状、人民币成本、售价、
// 毛利、错误）。只读，可用于 dry_run 与正式导入前的逐条校验。
func (s *Service) PlanImport(ctx context.Context, in ImportModelsInput) ([]ImportModelPlan, *PricingPreviewResult, error) {
	if len(in.Items) == 0 || len(in.Items) > maxImportItems {
		return nil, nil, invalid("items must contain 1-%d entries", maxImportItems)
	}
	var mult decimal.Decimal
	if err := s.db(ctx).QueryRow(ctx, `SELECT cost_multiplier FROM provider_accounts WHERE id = $1`, in.ProviderAccountID).Scan(&mult); err != nil {
		if isNoRows(err) {
			return nil, nil, ErrProviderAccountNotFound
		}
		return nil, nil, err
	}
	previewItems := make([]PricingPreviewItem, len(in.Items))
	for i, it := range in.Items {
		previewItems[i] = PricingPreviewItem{Key: it.UpstreamModel, CostInput: it.CostInput, CostOutput: it.CostOutput,
			SellInput: it.SellInput, SellOutput: it.SellOutput, MarkupPercent: it.MarkupPercent}
	}
	preview, err := s.PricingPreview(ctx, PricingPreviewInput{Currency: in.Currency, CostMultiplier: &mult, MarkupPercent: in.MarkupPercent, Items: previewItems})
	if err != nil {
		return nil, nil, err
	}
	plans := make([]ImportModelPlan, len(in.Items))
	seen := map[string]bool{}
	for i, it := range in.Items {
		p := ImportModelPlan{UpstreamModel: strings.TrimSpace(it.UpstreamModel), Name: strings.TrimSpace(it.Name), Errors: []string{}}
		if p.Name == "" {
			p.Name = p.UpstreamModel
		}
		pr := preview.Items[i]
		p.CostInputCNY, p.CostOutputCNY, p.SellInput, p.SellOutput, p.MarginRatio = pr.CostInputCNY, pr.CostOutputCNY, pr.SellInput, pr.SellOutput, pr.MarginRatio
		switch {
		case p.UpstreamModel == "":
			p.Errors = append(p.Errors, "缺少上游模型 ID")
		case seen[p.UpstreamModel]:
			p.Errors = append(p.Errors, "上游模型重复")
		}
		seen[p.UpstreamModel] = true
		if it.CostInput == nil || it.CostOutput == nil {
			p.Errors = append(p.Errors, "缺少成本价")
		}
		if preview.FXMissing {
			p.Errors = append(p.Errors, "缺少 "+preview.Currency+"→CNY 汇率")
		}

		vm, err := s.GetVirtualModelByName(ctx, p.Name)
		switch {
		case errors.Is(err, ErrVirtualModelNotFound):
			p.Status = ImportStatusNew
			if strings.TrimSpace(it.Family) == "" {
				p.Errors = append(p.Errors, "缺少 family")
			}
			if it.ContextWindow <= 0 || it.MaxOutput <= 0 {
				p.Errors = append(p.Errors, "上下文窗口/最大输出无效")
			}
		case err != nil:
			return nil, nil, err
		default:
			p.VirtualModelID = &vm.ID
			p.Status = ImportStatusVMExists
			ch, err := s.FindChannel(ctx, vm.ID, in.ProviderAccountID, p.UpstreamModel)
			switch {
			case errors.Is(err, ErrChannelNotFound):
			case err != nil:
				return nil, nil, err
			default:
				p.ChannelID = &ch.ID
				p.Status = ImportStatusListed
			}
		}
		p.PublishSellPrice = !(it.KeepExistingSell && p.Status != ImportStatusNew)
		if p.PublishSellPrice {
			if p.SellInput == nil || p.SellOutput == nil || p.SellInput.IsZero() || p.SellOutput.IsZero() {
				p.Errors = append(p.Errors, "缺少售价")
			} else if p.MarginRatio != nil && p.MarginRatio.IsNegative() {
				p.Errors = append(p.Errors, "负毛利")
			}
		}
		plans[i] = p
	}
	return plans, preview, nil
}

// ImportOne 按计划导入一个模型（在 ctx 的环境事务里；调用方负责事务与审计）。
func (s *Service) ImportOne(ctx context.Context, providerAccountID int64, currency string, it ImportModelItem, p ImportModelPlan) (*ImportModelResult, error) {
	if len(p.Errors) > 0 {
		return nil, invalid("%s", strings.Join(p.Errors, "、"))
	}
	res := &ImportModelResult{}
	vm, err := s.GetVirtualModelByName(ctx, p.Name)
	switch {
	case errors.Is(err, ErrVirtualModelNotFound):
		typ := it.Type
		if typ == "" {
			typ = "chat"
		}
		caps := it.Capabilities
		if len(caps) == 0 {
			caps = []string{"stream"}
		}
		if vm, err = s.CreateVirtualModel(ctx, CreateVirtualModelInput{
			Name: p.Name, Family: strings.TrimSpace(it.Family), Type: typ, ContextWindow: it.ContextWindow, MaxOutput: it.MaxOutput,
			Capabilities: caps, VisibleTiers: it.VisibleTiers,
		}); err != nil {
			return nil, err
		}
		res.CreatedVM = true
	case err != nil:
		return nil, err
	}
	res.VirtualModelID = vm.ID
	ch, err := s.FindChannel(ctx, vm.ID, providerAccountID, p.UpstreamModel)
	switch {
	case errors.Is(err, ErrChannelNotFound):
		if ch, err = s.CreateChannel(ctx, CreateChannelInput{VirtualModelID: vm.ID, ProviderAccountID: providerAccountID, UpstreamModel: p.UpstreamModel}); err != nil {
			return nil, err
		}
		res.CreatedChannel = true
	case err != nil:
		return nil, err
	}
	res.ChannelID = ch.ID
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if cur == "" {
		cur = "USD"
	}
	if res.CostBookID, err = s.SetCostPrice(ctx, SetCostPriceInput{ChannelID: ch.ID, Currency: cur, Components: []PriceComponentInput{
		{Meter: "input", Unit: "per_1m_tokens", UnitPrice: *it.CostInput},
		{Meter: "output", Unit: "per_1m_tokens", UnitPrice: *it.CostOutput},
	}}); err != nil {
		return nil, err
	}
	if p.PublishSellPrice {
		id, err := s.SetSellPrice(ctx, SetSellPriceInput{VirtualModelID: vm.ID, Components: []PriceComponentInput{
			{Meter: "input", Unit: "per_1m_tokens", UnitPrice: *p.SellInput},
			{Meter: "output", Unit: "per_1m_tokens", UnitPrice: *p.SellOutput},
		}})
		if err != nil {
			return nil, err
		}
		res.SellBookID = &id
	}
	return res, nil
}
