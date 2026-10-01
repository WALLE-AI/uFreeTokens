package benchsync

import (
	"regexp"
	"slices"
	"strings"
)

// 模型名映射（技术方案 §4.4）：各榜单的命名和本平台虚拟模型名不一致——
//   LMArena: claude-opus-4-6-high、gpt-4o-2024-05-13
//   Epoch:   gpt-6.1-sol_max、deepseek-v4-pro_max、"Claude Opus 5.5"
// 先把两边都归一化（去厂商前缀 / 日期后缀，统一分隔符），再比较：
//   exact      原名（忽略大小写）与虚拟模型名 / 别名 / 展示名相同；
//   normalized 归一化后相同；榜单名末尾的推理档位（-high、_max、-thinking-16k ...）逐个剥掉、
//              每剥一个就再比一次——"max" 既可能是档位（kimi-k3-max）也可能是型号的一部分
//              （qwen3.7-max），所以虚拟模型名从不剥档位，榜单名也不一次剥光；
//   fuzzy      同一家族、数字版本完全一致、词元 Jaccard ≥ 0.7——只作"建议"，不自动生效。

// effortSuffixes 是会被剥离进 variant 的推理档位 / 模式后缀。
var effortSuffixes = map[string]bool{
	"high": true, "low": true, "medium": true, "minimal": true, "max": true, "xhigh": true, "none": true,
	"thinking": true, "reasoning": true, "nothinking": true, "non-thinking": true, "unknown": true, "default": true,
}

var (
	dateSuffix   = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)
	budgetSuffix = regexp.MustCompile(`^\d+k$`)
	parenPart    = regexp.MustCompile(`\(([^)]*)\)`)
	sepRun       = regexp.MustCompile(`[\s_.:/]+`)
	dashRun      = regexp.MustCompile(`-+`)
	numToken     = regexp.MustCompile(`^\d+$`)
)

// Normalize 返回 (主干, 档位)：剥掉全部档位后缀后的主干，和被剥掉的档位。主干只含小写字母、数字和单个连字符。
func Normalize(label string) (base, variant string) {
	c := candidates(label)
	return c[len(c)-1].base, c[len(c)-1].variant
}

// normalizeName 是不剥档位的归一化（用于虚拟模型名）。
func normalizeName(name string) string { return candidates(name)[0].base }

type candidate struct{ base, variant string }

// candidates 返回逐步剥离档位后缀的候选：[0] 不剥档位，最后一个剥光。
func candidates(label string) []candidate {
	s := strings.ToLower(strings.TrimSpace(label))
	var variants []string
	// 括号里的通常是档位："GPT-5 (high)"、"Claude 4 Sonnet (Thinking 16K)"。
	s = parenPart.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.TrimSpace(m[1 : len(m)-1])
		if inner != "" {
			variants = append(variants, strings.Join(strings.Fields(inner), "-"))
		}
		return " "
	})
	// 厂商前缀：anthropic/claude-x、accounts/fireworks/models/x。
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	// Epoch 用下划线接档位：deepseek-v4-pro_max。
	if i := strings.LastIndex(s, "_"); i >= 0 && effortSuffixes[s[i+1:]] {
		variants = append(variants, s[i+1:])
		s = s[:i]
	}
	s = sepRun.ReplaceAllString(s, "-")
	s = dashRun.ReplaceAllString(strings.Trim(s, "-"), "-")
	s = strings.TrimSuffix(s, "-latest")
	s = dateSuffix.ReplaceAllString(s, "")
	join := func(vs []string) string {
		r := slices.Clone(vs)
		slices.Reverse(r)
		return strings.Join(r, "-")
	}
	// variants 此时是括号 / 下划线里的档位（按出现顺序）；逐个剥后缀时倒序追加，最后统一翻转。
	slices.Reverse(variants)
	out := []candidate{{s, join(variants)}}
	for {
		i := strings.LastIndex(s, "-")
		if i < 0 {
			break
		}
		last := s[i+1:]
		if !effortSuffixes[last] && !budgetSuffix.MatchString(last) {
			break
		}
		variants = append(variants, last)
		s = dateSuffix.ReplaceAllString(s[:i], "")
		out = append(out, candidate{s, join(variants)})
	}
	return out
}

// ModelCandidate 是一个可被关联的虚拟模型及其全部名字（name / aliases / 展示名）。
type ModelCandidate struct {
	ID    int64
	Names []string
}

// Matcher 在一组虚拟模型里查找榜单模型名。
type Matcher struct {
	exact      map[string]int64 // 小写原名
	normalized map[string]int64 // 归一化主干；0 表示有歧义
	bases      []matchBase
}

type matchBase struct {
	id     int64
	tokens []string
}

func NewMatcher(models []ModelCandidate) *Matcher {
	m := &Matcher{exact: map[string]int64{}, normalized: map[string]int64{}}
	put := func(mp map[string]int64, k string, id int64) {
		if k == "" {
			return
		}
		if prev, ok := mp[k]; ok && prev != id {
			mp[k] = 0
			return
		}
		mp[k] = id
	}
	for _, c := range models {
		for _, n := range c.Names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			put(m.exact, strings.ToLower(n), c.ID)
			if i := strings.LastIndex(n, "/"); i >= 0 {
				put(m.exact, strings.ToLower(n[i+1:]), c.ID)
			}
			base := normalizeName(n)
			put(m.normalized, base, c.ID)
			m.bases = append(m.bases, matchBase{id: c.ID, tokens: strings.Split(base, "-")})
			// 也用剥光档位的主干参与模糊比较（目录里可能就叫 xxx-thinking）。
			if stripped, _ := Normalize(n); stripped != base {
				m.bases = append(m.bases, matchBase{id: c.ID, tokens: strings.Split(stripped, "-")})
			}
		}
	}
	return m
}

// MatchResult 是一次匹配的结论。
type MatchResult struct {
	VirtualModelID int64 // 0 = 没有匹配
	Method         string
	Confidence     float64
	Variant        string
}

// Match 返回最好的匹配；Method 为 fuzzy 时只是建议，调用方不应直接关联。
func (m *Matcher) Match(label string) MatchResult {
	cands := candidates(label)
	if id, ok := m.exact[strings.ToLower(strings.TrimSpace(label))]; ok && id != 0 {
		return MatchResult{VirtualModelID: id, Method: "exact", Confidence: 1, Variant: cands[0].variant}
	}
	for _, c := range cands {
		if id, ok := m.normalized[c.base]; ok {
			if id == 0 {
				break // 有歧义：不再往下剥，交给模糊建议 / 人工
			}
			return MatchResult{VirtualModelID: id, Method: "normalized", Confidence: 0.95, Variant: c.variant}
		}
	}
	last := cands[len(cands)-1]
	tokens := strings.Split(last.base, "-")
	best := MatchResult{Method: "none", Variant: last.variant}
	for _, b := range m.bases {
		if len(tokens) == 0 || len(b.tokens) == 0 || tokens[0] != b.tokens[0] || !sameNumbers(tokens, b.tokens) {
			continue
		}
		if j := jaccard(tokens, b.tokens); j >= 0.7 && j > best.Confidence {
			best = MatchResult{VirtualModelID: b.id, Method: "fuzzy", Confidence: j, Variant: last.variant}
		}
	}
	return best
}

func sameNumbers(a, b []string) bool {
	var na, nb []string
	for _, t := range a {
		if numToken.MatchString(t) {
			na = append(na, t)
		}
	}
	for _, t := range b {
		if numToken.MatchString(t) {
			nb = append(nb, t)
		}
	}
	return slices.Equal(na, nb)
}

// fuzzyNoise 是模糊比较时忽略的词元：预览 / 实验版通常就是同一个模型的早期版本，值得建议。
var fuzzyNoise = map[string]bool{"preview": true, "exp": true, "experimental": true, "beta": true}

func jaccard(a, b []string) float64 {
	set := map[string]int{}
	for _, t := range a {
		if !fuzzyNoise[t] {
			set[t] |= 1
		}
	}
	for _, t := range b {
		if !fuzzyNoise[t] {
			set[t] |= 2
		}
	}
	var inter, union int
	for _, v := range set {
		union++
		if v == 3 {
			inter++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
