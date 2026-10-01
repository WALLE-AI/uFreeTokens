package benchmarks

import "testing"

func ptr[T any](v T) *T { return &v }

func TestRank_TiesShareRank(t *testing.T) {
	rs := []BenchmarkResult{{ModelLabel: "b", Score: 80}, {ModelLabel: "a", Score: 90}, {ModelLabel: "c", Score: 80}, {ModelLabel: "d", Score: 70}}
	rank(rs, true)
	want := []struct {
		label string
		rank  int
	}{{"a", 1}, {"b", 2}, {"c", 2}, {"d", 4}}
	for i, w := range want {
		if rs[i].ModelLabel != w.label || rs[i].Rank != w.rank {
			t.Errorf("rs[%d] = %s#%d, want %s#%d", i, rs[i].ModelLabel, rs[i].Rank, w.label, w.rank)
		}
	}
	rank(rs, false) // 越低越好（如错误率、耗时类指标）
	if rs[0].ModelLabel != "d" || rs[0].Rank != 1 {
		t.Errorf("lower-is-better first = %s#%d, want d#1", rs[0].ModelLabel, rs[0].Rank)
	}
}

func TestChampions(t *testing.T) {
	rs := []BenchmarkResult{
		{ModelLabel: "best", Score: 95, CostPerTaskMicro: ptr[int64](900), AvgDurationMs: ptr(9000)},
		{ModelLabel: "good-cheap", Score: 85, CostPerTaskMicro: ptr[int64](100), AvgDurationMs: ptr(4000)},
		{ModelLabel: "median-fast", Score: 80, CostPerTaskMicro: ptr[int64](300), AvgDurationMs: ptr(1000)},
		// 低于中位数：再便宜再快也不参与性价比/速度评选。
		{ModelLabel: "bad-cheap", Score: 40, CostPerTaskMicro: ptr[int64](1), AvgDurationMs: ptr(10)},
		{ModelLabel: "bad", Score: 30},
	}
	rank(rs, true)
	c := champions(rs, true)
	if c.Quality == nil || c.Quality.ModelLabel != "best" {
		t.Errorf("quality = %+v, want best", c.Quality)
	}
	if c.Value == nil || c.Value.ModelLabel != "good-cheap" {
		t.Errorf("value = %+v, want good-cheap", c.Value)
	}
	if c.Speed == nil || c.Speed.ModelLabel != "median-fast" {
		t.Errorf("speed = %+v, want median-fast", c.Speed)
	}

	// 没有成本/耗时数据时对应冠军为空；空结果三项都为空。
	only := []BenchmarkResult{{ModelLabel: "x", Score: 1}}
	rank(only, true)
	if c := champions(only, true); c.Quality == nil || c.Value != nil || c.Speed != nil {
		t.Errorf("champions without cost/duration = %+v", c)
	}
	if c := champions(nil, true); c.Quality != nil {
		t.Error("empty results must have no champions")
	}
}
