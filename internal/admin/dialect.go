package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
)

// 供应商方言的读写（多供应商接口统一技术实施方案 §3.3）。方言存在
// provider_accounts.extra.dialect；每次修改把旧值压进 extra.dialect_history（保留最近
// maxDialectHistory 个版本），后台可以一键回退。保存前用 dialect.Load 做完整校验，
// 非法配置一律 400，不会写进库里让网关在加载时才发现。

const maxDialectHistory = 5

// DialectVersion 是方言的一个历史版本。
type DialectVersion struct {
	Dialect json.RawMessage `json:"dialect"`
	SavedAt time.Time       `json:"saved_at"`
	SavedBy *int64          `json:"saved_by"`
}

// AccountDialect 是 GET /provider-accounts/{id}/dialect 的返回值。
type AccountDialect struct {
	ProviderAccountID int64            `json:"provider_account_id"`
	ProviderCode      string           `json:"provider_code"`
	Protocol          string           `json:"protocol"`
	Dialect           json.RawMessage  `json:"dialect"`   // 账号上保存的原始配置（null = 没有方言）
	Effective         *dialect.Dialect `json:"effective"` // 合并预设后的生效配置
	// Endpoints 是按生效配置计算的各端点支持情况（不含 by_model 规则的细分）。
	Endpoints map[string]bool `json:"endpoints"`
	// SuggestedPreset 是与供应商 code 同名的内置预设（账号还没有方言时供后台提示）。
	SuggestedPreset string           `json:"suggested_preset,omitempty"`
	History         []DialectVersion `json:"history"`
}

func (s *Service) loadAccountExtra(ctx context.Context, id int64) (code, protocol string, extra map[string]json.RawMessage, err error) {
	var raw []byte
	err = s.db(ctx).QueryRow(ctx,
		`SELECT p.code, p.protocol, pa.extra FROM provider_accounts pa JOIN providers p ON p.id = pa.provider_id WHERE pa.id = $1`, id,
	).Scan(&code, &protocol, &raw)
	if isNoRows(err) {
		return "", "", nil, ErrProviderAccountNotFound
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("admin: load provider account extra: %w", err)
	}
	extra = map[string]json.RawMessage{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &extra)
	}
	return code, protocol, extra, nil
}

// GetAccountDialect 返回账号的方言（原始配置、生效配置、端点支持情况、历史）。
func (s *Service) GetAccountDialect(ctx context.Context, id int64) (*AccountDialect, error) {
	code, protocol, extra, err := s.loadAccountExtra(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &AccountDialect{ProviderAccountID: id, ProviderCode: code, Protocol: protocol, Dialect: extra["dialect"], History: []DialectVersion{}}
	if len(out.Dialect) == 0 {
		out.Dialect = json.RawMessage("null")
	}
	if raw := extra["dialect_history"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.History)
	}
	d, err := dialect.Load("", out.Dialect)
	if err != nil {
		return nil, invalid("stored dialect is invalid: %v", err)
	}
	out.Effective = d
	out.Endpoints = endpointSupport(protocol, d)
	if d == nil {
		if _, ok := dialect.PresetJSON(code); ok {
			out.SuggestedPreset = code
		}
	}
	return out, nil
}

// endpointSupport 按协议与生效方言计算各端点是否可用（不考虑 by_model）。
func endpointSupport(protocol string, d *dialect.Dialect) map[string]bool {
	adp, ok := adapter.NewRegistry().For(protocol)
	out := map[string]bool{}
	acct := &catalog.ProviderAccount{Protocol: protocol, Dialect: d}
	for name, ep := range endpointPaths {
		out[name] = ok && adapter.Serves(adp, acct, ep, "")
	}
	return out
}

var endpointPaths = map[string]string{
	dialect.EndpointChat: adapter.EndpointChat, dialect.EndpointEmbeddings: adapter.EndpointEmbeddings,
	dialect.EndpointRerank: adapter.EndpointRerank, dialect.EndpointImages: adapter.EndpointImages,
	dialect.EndpointSpeech: adapter.EndpointSpeech, dialect.EndpointTranscriptions: adapter.EndpointTranscriptions,
}

// normalizeDialect 校验并规整方言配置：null / {} 表示删除方言。
func normalizeDialect(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" || string(trimmed) == "{}" {
		return nil, nil
	}
	if _, err := dialect.Load("", trimmed); err != nil {
		return nil, invalid("%v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return nil, invalid("dialect is not valid JSON")
	}
	return compact.Bytes(), nil
}

// SetAccountDialect 保存账号方言，旧值进历史。返回保存前后的配置（供审计）。
func (s *Service) SetAccountDialect(ctx context.Context, id int64, raw json.RawMessage) (before, after json.RawMessage, err error) {
	norm, err := normalizeDialect(raw)
	if err != nil {
		return nil, nil, err
	}
	_, _, extra, err := s.loadAccountExtra(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	before = extra["dialect"]
	var history []DialectVersion
	if h := extra["dialect_history"]; len(h) > 0 {
		_ = json.Unmarshal(h, &history)
	}
	if len(before) > 0 && string(before) != "null" {
		history = slices.Insert(history, 0, DialectVersion{Dialect: before, SavedAt: time.Now().UTC(), SavedBy: actorFrom(ctx)})
		if len(history) > maxDialectHistory {
			history = history[:maxDialectHistory]
		}
	}
	histRaw, _ := json.Marshal(history)
	var dialectArg any
	if norm != nil {
		dialectArg = string(norm)
	}
	if _, err := s.db(ctx).Exec(ctx,
		`UPDATE provider_accounts
		 SET extra = CASE WHEN $2::jsonb IS NULL THEN extra - 'dialect' ELSE jsonb_set(extra, '{dialect}', $2::jsonb) END
		           || jsonb_build_object('dialect_history', $3::jsonb)
		 WHERE id = $1`, id, dialectArg, string(histRaw)); err != nil {
		return nil, nil, fmt.Errorf("admin: save dialect: %w", err)
	}
	return before, norm, nil
}

// endpointOfModel 是模型类型 / 能力对应的逻辑端点（与网关 relay 的 endpointSpec 一致）。
func endpointOfModel(typ string, caps []string) string {
	switch typ {
	case "embedding":
		return adapter.EndpointEmbeddings
	case "rerank":
		return adapter.EndpointRerank
	case "image":
		return adapter.EndpointImages
	case "audio":
		if slices.Contains(caps, "asr") {
			return adapter.EndpointTranscriptions
		}
		return adapter.EndpointSpeech
	}
	return adapter.EndpointChat
}

var endpointLabels = map[string]string{
	adapter.EndpointChat: "对话", adapter.EndpointEmbeddings: "向量", adapter.EndpointRerank: "重排序",
	adapter.EndpointImages: "图像生成", adapter.EndpointSpeech: "语音合成", adapter.EndpointTranscriptions: "语音识别",
}

// accountServes 判断上游账号能否服务某个模型类型（导入 / 新建渠道时的上架校验，§7）。
// 返回不支持时给运营看的原因；支持时返回空串。
func (s *Service) accountServes(ctx context.Context, accountID int64, typ string, caps []string, upstreamModel string) (string, error) {
	_, protocol, extra, err := s.loadAccountExtra(ctx, accountID)
	if err != nil {
		return "", err
	}
	d, err := dialect.Load("", extra["dialect"])
	if err != nil {
		return "上游账号的方言配置无效：" + err.Error(), nil
	}
	adp, ok := adapter.NewRegistry().For(protocol)
	ep := endpointOfModel(typ, caps)
	if !ok || !adapter.Serves(adp, &catalog.ProviderAccount{Protocol: protocol, Dialect: d}, ep, upstreamModel) {
		return "该供应商不支持" + endpointLabels[ep] + "模型", nil
	}
	return "", nil
}
