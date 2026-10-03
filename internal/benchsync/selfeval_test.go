package benchsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/llm"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestGrade(t *testing.T) {
	cases := []struct {
		q     SelfEvalQuestion
		reply string
		want  bool
	}{
		{SelfEvalQuestion{Answer: "Paris"}, " paris. ", true},
		{SelfEvalQuestion{Answer: "Paris"}, "The capital is Paris", false},
		{SelfEvalQuestion{Answer: "paris", Match: "contains"}, "The capital is Paris", true},
		{SelfEvalQuestion{Answer: "42", Match: "number"}, "<think>6*7</think>答案是 42", true},
		{SelfEvalQuestion{Answer: "1000", Match: "number"}, "1,000", true},
		{SelfEvalQuestion{Answer: `^\d{4}-\d{2}$`, Match: "regex"}, "2026-10", true},
		{SelfEvalQuestion{Answer: "3", Match: "number"}, "无法回答", false},
	}
	for _, c := range cases {
		if got := Grade(c.q, c.reply); got != c.want {
			t.Errorf("Grade(%+v, %q) = %v, want %v", c.q, c.reply, got, c.want)
		}
	}
}

// fakeChatter：model "good" 全对，"half" 只答对第一题，"down" 全部报错。
type fakeChatter struct{}

func (fakeChatter) ChatTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, opt llm.ChatOptions, _ func(llm.StreamEvent)) (*llm.Completion, error) {
	q := msgs[len(msgs)-1].Content
	switch opt.Model {
	case "down":
		return nil, errors.New("503")
	case "half":
		if strings.Contains(q, "2+2") {
			return &llm.Completion{Content: "4"}, nil
		}
		return &llm.Completion{Content: "不知道"}, nil
	}
	if strings.Contains(q, "2+2") {
		return &llm.Completion{Content: "4"}, nil
	}
	return &llm.Completion{Content: "Paris"}, nil
}

func TestSelfEvalJob_RunsAndCreatesDraft(t *testing.T) {
	pool := testPool(t)
	svc := admin.New(pool, wallet.New(pool), nil, nil)
	job := &SelfEvalJob{Job: Job{Pool: pool, Publisher: svc}, NewClient: func(string, string) Chatter { return fakeChatter{} }}
	slug := fmt.Sprintf("self-eval-test-%d", time.Now().UnixNano())
	var srcID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO price_sources (domain, name, level, kind, fetcher, url, schedule, enabled) VALUES ('benchmark', $1, 'L5', 'api', 'self_eval', 'http://gateway/v1', '', false) RETURNING id`,
		slug).Scan(&srcID); err != nil {
		t.Fatal(err)
	}
	src := datasync.Source{ID: srcID, Name: "自测", Config: map[string]any{
		"base_url": "http://gateway/v1", "models": []any{"good", "half", "down"},
		"key":       "self_eval:test:" + slug,
		"benchmark": map[string]any{"slug": slug, "name": "自测基础题", "category": "general", "metric_name": "accuracy", "metric_unit": "%"},
		"questions": []any{
			map[string]any{"id": "q1", "prompt": "2+2=?", "answer": "4", "match": "number"},
			map[string]any{"id": "q2", "prompt": "Capital of France?", "answer": "Paris"},
		},
	}}
	res, err := job.Run(context.Background(), nil, src)
	if err != nil {
		t.Fatal(err)
	}
	if res.ItemsFetched != 2 || res.Detail["failed_models"] != 1 || res.Detail["status"] != "draft" {
		t.Fatalf("result = %+v", res)
	}
	var origin string
	var scores []float64
	if err := pool.QueryRow(context.Background(),
		`SELECT r.origin, array_agg(x.score::float8 ORDER BY x.score DESC) FROM benchmark_runs r JOIN benchmark_results x ON x.run_id = r.id
		 WHERE r.id = $1 GROUP BY r.origin`, res.Detail["run_id"]).Scan(&origin, &scores); err != nil {
		t.Fatal(err)
	}
	if origin != "self_eval" || len(scores) != 2 || scores[0] != 100 || scores[1] != 50 {
		t.Errorf("origin=%s scores=%v", origin, scores)
	}
}
