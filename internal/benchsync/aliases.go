package benchsync

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// resolver 把榜单模型名解析成虚拟模型，并维护 model_aliases：
//   - confirmed / ignored（人工决定）永远优先，不被自动匹配覆盖；
//   - 其余每次按当前虚拟模型重新匹配（模型上架 / 改名后自动跟上），结果写回 auto / suggested / unmatched。
type resolver struct {
	job       *Job
	namespace string
	matcher   *Matcher
	manual    map[string]*int64 // 人工决定的映射（ignored 为 nil）
}

func (j *Job) newResolver(ctx context.Context, namespace string) (*resolver, error) {
	models, err := LoadModelCandidates(ctx, j.db(ctx))
	if err != nil {
		return nil, err
	}
	rows, err := j.db(ctx).Query(ctx,
		`SELECT external_label, virtual_model_id FROM model_aliases WHERE namespace = $1 AND status IN ('confirmed','ignored')`, namespace)
	if err != nil {
		return nil, fmt.Errorf("benchsync: load model aliases: %w", err)
	}
	manual := map[string]*int64{}
	for rows.Next() {
		var label string
		var vm *int64
		if err := rows.Scan(&label, &vm); err != nil {
			rows.Close()
			return nil, err
		}
		manual[label] = vm
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &resolver{job: j, namespace: namespace, matcher: NewMatcher(models), manual: manual}, nil
}

// Querier 是 LoadModelCandidates 需要的最小接口。
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// LoadModelCandidates 读全部虚拟模型及其可匹配的名字（name、aliases、展示名）。
func LoadModelCandidates(ctx context.Context, q Querier) ([]ModelCandidate, error) {
	rows, err := q.Query(ctx,
		`SELECT vm.id, vm.name, vm.aliases, md.display_name
		 FROM virtual_models vm LEFT JOIN virtual_model_metadata md ON md.virtual_model_id = vm.id
		 ORDER BY vm.id`)
	if err != nil {
		return nil, fmt.Errorf("benchsync: load virtual models: %w", err)
	}
	defer rows.Close()
	var out []ModelCandidate
	for rows.Next() {
		var c ModelCandidate
		var name string
		var aliases []string
		var display *string
		if err := rows.Scan(&c.ID, &name, &aliases, &display); err != nil {
			return nil, fmt.Errorf("benchsync: scan virtual model: %w", err)
		}
		c.Names = append([]string{name}, aliases...)
		if display != nil && *display != "" {
			c.Names = append(c.Names, *display)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// resolve 返回 label -> 关联的虚拟模型（没有关联为 nil），并把匹配结果写回 model_aliases。
func (r *resolver) resolve(ctx context.Context, labels []string) (map[string]*int64, error) {
	out := make(map[string]*int64, len(labels))
	for _, label := range labels {
		if vm, ok := r.manual[label]; ok {
			out[label] = vm
			if _, err := r.job.db(ctx).Exec(ctx,
				`UPDATE model_aliases SET seen_count = seen_count + 1, last_seen_at = now() WHERE namespace = $1 AND external_label = $2`,
				r.namespace, label); err != nil {
				return nil, fmt.Errorf("benchsync: touch alias: %w", err)
			}
			continue
		}
		m := r.matcher.Match(label)
		status := "unmatched"
		var vm *int64
		var conf *float64
		switch m.Method {
		case "exact", "normalized":
			status = "auto"
			id := m.VirtualModelID
			vm = &id
			out[label] = &id
		case "fuzzy":
			status = "suggested" // 只是建议：记下候选，但不关联
			id := m.VirtualModelID
			vm = &id
			out[label] = nil
		default:
			out[label] = nil
		}
		if m.Confidence > 0 {
			c := m.Confidence
			conf = &c
		}
		var variant *string
		if m.Variant != "" {
			variant = &m.Variant
		}
		if _, err := r.job.db(ctx).Exec(ctx,
			`INSERT INTO model_aliases (namespace, external_label, virtual_model_id, status, method, confidence, variant)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (namespace, external_label) DO UPDATE SET
			   virtual_model_id = EXCLUDED.virtual_model_id, status = EXCLUDED.status, method = EXCLUDED.method,
			   confidence = EXCLUDED.confidence, variant = EXCLUDED.variant,
			   seen_count = model_aliases.seen_count + 1, last_seen_at = now()
			 WHERE model_aliases.status NOT IN ('confirmed','ignored')`,
			r.namespace, label, vm, status, m.Method, conf, variant); err != nil {
			return nil, fmt.Errorf("benchsync: upsert alias %q: %w", label, err)
		}
	}
	return out, nil
}
