package admin

import (
	"maps"
	"math"
	"slices"
	"strings"
)

// virtual_model_metadata.scores 的键名约定（基准测试与排行榜方案 §3.1）。
// 以前这里"由运营和前端约定，后端不解析、不校验"，结果运营写成
// intelligence_index、前端读 intelligenceIndex，页面静默显示 0 分。现在后端
// 固定白名单：只允许下列键，值必须是有限数字（design_arena 是一层嵌套对象），
// 未知键/非数字一律 400。GET /v1/catalog 仍原样透传。

// ScoreKeys 是 scores 顶层允许的数值键。前三个是综合指数；其余是评测榜单导入时按
// benchmarks.score_key 投影进来的单项分数（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §4.2）。
var ScoreKeys = []string{
	"intelligence_index", "coding_index", "agentic_index",
	"arena_text", "arena_chinese", "arena_coding", "arena_webdev", "arena_vision",
	"gpqa_diamond", "swe_bench_verified", "hle", "terminal_bench", "aider_polyglot", "arc_agi_2", "livebench", "epoch_eci",
	"opencompass", "superclue",
}

// DesignArenaKeys 是 scores.design_arena 下允许的数值键。
var DesignArenaKeys = []string{"code", "ui_component", "game_dev", "data_viz", "three_d", "image", "video", "svg"}

// ValidateScores 校验 scores 是否符合键名约定；nil（不设置/清空评分）合法。
func ValidateScores(scores map[string]any) error {
	// 按键名排序遍历：多个非法键时错误信息稳定。
	for _, k := range slices.Sorted(maps.Keys(scores)) {
		v := scores[k]
		switch {
		case slices.Contains(ScoreKeys, k):
			if !finiteNumber(v) {
				return invalid("scores.%s must be a number", k)
			}
		case k == "design_arena":
			da, ok := v.(map[string]any)
			if !ok {
				return invalid("scores.design_arena must be an object")
			}
			for _, dk := range slices.Sorted(maps.Keys(da)) {
				if !slices.Contains(DesignArenaKeys, dk) {
					return invalid("unknown key scores.design_arena.%s (allowed: %s)", dk, strings.Join(DesignArenaKeys, ", "))
				}
				if !finiteNumber(da[dk]) {
					return invalid("scores.design_arena.%s must be a number", dk)
				}
			}
		default:
			return invalid("unknown key scores.%s (allowed: %s, design_arena)", k, strings.Join(ScoreKeys, ", "))
		}
	}
	return nil
}

func finiteNumber(v any) bool {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return false
	}
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
