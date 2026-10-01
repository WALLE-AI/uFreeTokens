package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 公开应用榜的运营治理（docs/基准测试与排行榜数据服务技术方案.md §8.2）：X-Title 是调用方
// 自报的，任何人都能冒用知名应用名。运营在这里看到全部声明过的应用（不受隐私阈值限制），
// 并可以屏蔽（不上榜）、合并（并入另一个 app_key）或改展示名。规则在网关读榜时实时生效。

var ErrPublicAppRuleNotFound = errors.New("admin: public app rule not found")

var publicAppActions = []string{"block", "merge", "rename"}

type PublicAppRule struct {
	ID          int64     `json:"id"`
	AppKey      string    `json:"app_key"`
	Action      string    `json:"action"`
	MergeInto   *string   `json:"merge_into"`
	DisplayName *string   `json:"display_name"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PublicAppCandidate 是统计期内声明过的一个应用（原始数据，未做隐私阈值、单账户上限）。
type PublicAppCandidate struct {
	AppKey           string         `json:"app_key"`
	AppName          string         `json:"app_name"`
	AppURL           string         `json:"app_url"`
	Requests         int64          `json:"requests"`
	Tokens           int64          `json:"tokens"`
	DistinctAccounts int            `json:"distinct_accounts"`
	Rule             *PublicAppRule `json:"rule"`
}

const publicAppRuleCols = `id, app_key, action, merge_into, display_name, note, created_at, updated_at`

func (s *Service) ListPublicAppRules(ctx context.Context) ([]PublicAppRule, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+publicAppRuleCols+` FROM public_app_rules ORDER BY app_key`)
	if err != nil {
		return nil, fmt.Errorf("admin: list public app rules: %w", err)
	}
	defer rows.Close()
	out := []PublicAppRule{}
	for rows.Next() {
		var r PublicAppRule
		if err := rows.Scan(&r.ID, &r.AppKey, &r.Action, &r.MergeInto, &r.DisplayName, &r.Note, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan public app rule: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPublicAppCandidates 返回最近 days 天（Asia/Shanghai 自然日，含今天）声明过的应用，按 token 降序。
func (s *Service) ListPublicAppCandidates(ctx context.Context, days int) ([]PublicAppCandidate, error) {
	if days < 1 || days > 90 {
		return nil, invalid("days must be between 1 and 90")
	}
	rules, err := s.ListPublicAppRules(ctx)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*PublicAppRule{}
	for i := range rules {
		byKey[rules[i].AppKey] = &rules[i]
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT d.app_key, mode() WITHIN GROUP (ORDER BY d.app_name), max(d.app_url), sum(d.requests), sum(d.tokens),
			(SELECT count(DISTINCT x.account_id) FROM public_app_account_daily x
			  WHERE x.app_key = d.app_key AND x.day > (now() AT TIME ZONE 'Asia/Shanghai')::date - $1::int)
		 FROM public_app_usage_daily d
		 WHERE d.day > (now() AT TIME ZONE 'Asia/Shanghai')::date - $1::int
		 GROUP BY d.app_key
		 ORDER BY sum(d.tokens) DESC, d.app_key
		 LIMIT 500`, days)
	if err != nil {
		return nil, fmt.Errorf("admin: list public app candidates: %w", err)
	}
	defer rows.Close()
	out := []PublicAppCandidate{}
	for rows.Next() {
		var c PublicAppCandidate
		if err := rows.Scan(&c.AppKey, &c.AppName, &c.AppURL, &c.Requests, &c.Tokens, &c.DistinctAccounts); err != nil {
			return nil, fmt.Errorf("admin: scan public app candidate: %w", err)
		}
		c.Rule = byKey[c.AppKey]
		out = append(out, c)
	}
	return out, rows.Err()
}

type CreatePublicAppRuleInput struct {
	AppKey      string  `json:"app_key"` // 'url:https://host' 或 'name:<小写应用名>'，见 GET /public-apps
	Action      string  `json:"action"`  // block / merge / rename
	MergeInto   *string `json:"merge_into"`
	DisplayName *string `json:"display_name"`
	Note        string  `json:"note"`
}

func validAppKey(k string) bool {
	return (strings.HasPrefix(k, "url:") || strings.HasPrefix(k, "name:")) && len(k) > 5 && len(k) <= 300
}

func (s *Service) CreatePublicAppRule(ctx context.Context, in CreatePublicAppRuleInput) (*PublicAppRule, error) {
	in.AppKey = strings.TrimSpace(in.AppKey)
	if !validAppKey(in.AppKey) {
		return nil, invalid("app_key must look like url:https://host or name:<app name>")
	}
	if err := oneOf("action", in.Action, publicAppActions...); err != nil {
		return nil, err
	}
	mergeInto, displayName := trimmedOrNil(in.MergeInto), trimmedOrNil(in.DisplayName)
	switch in.Action {
	case "merge":
		if mergeInto == nil || !validAppKey(*mergeInto) || *mergeInto == in.AppKey {
			return nil, invalid("merge requires merge_into: another app_key")
		}
	case "rename":
		if displayName == nil {
			return nil, invalid("rename requires display_name")
		}
		fallthrough
	default:
		if mergeInto != nil {
			return nil, invalid("merge_into is only allowed with action=merge")
		}
	}
	if displayName != nil && len([]rune(*displayName)) > 64 {
		return nil, invalid("display_name must be at most 64 characters")
	}
	var r PublicAppRule
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO public_app_rules (app_key, action, merge_into, display_name, note) VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+publicAppRuleCols,
		in.AppKey, in.Action, mergeInto, displayName, strings.TrimSpace(in.Note),
	).Scan(&r.ID, &r.AppKey, &r.Action, &r.MergeInto, &r.DisplayName, &r.Note, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, fmt.Errorf("admin: insert public app rule: %w", err)
	}
	return &r, nil
}

// DeletePublicAppRule 删除规则并返回被删除的内容（供审计）。
func (s *Service) DeletePublicAppRule(ctx context.Context, id int64) (*PublicAppRule, error) {
	var r PublicAppRule
	if err := s.db(ctx).QueryRow(ctx, `DELETE FROM public_app_rules WHERE id = $1 RETURNING `+publicAppRuleCols, id).
		Scan(&r.ID, &r.AppKey, &r.Action, &r.MergeInto, &r.DisplayName, &r.Note, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if isNoRows(err) {
			return nil, ErrPublicAppRuleNotFound
		}
		return nil, fmt.Errorf("admin: delete public app rule: %w", err)
	}
	return &r, nil
}
