package benchsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/llm"
)

// self_eval 确定性评测执行器（fetcher=self_eval，实施方案 M4-B05、设计 §13.2）：经本平台网关对目标模型
// 跑一组固定题目，按规则判分（不用 LLM-as-judge），写 benchmark_runs(origin=self_eval)，再走与榜单导入
// 相同的发布闸门（来源 auto_publish + 异常检查）。它不是智能体任务：结果完全由题集与规则决定、可复现。
//
// 来源 config：
//
//	base_url      string   网关 OpenAI 兼容地址，如 http://gateway:8080/v1（为空取来源 url）
//	api_key_env   string   网关 API Key 所在的环境变量名（平台自己的测试账户）
//	models        []string 目标虚拟模型名
//	max_tokens    int      单题输出上限，默认 256
//	key           string   基准的 external_key（默认 self_eval:<来源 id>）
//	score_key     string   发布时投影进 scores 的键（可选）
//	benchmark     {...}    与 tabular 来源 boards[].benchmark 相同
//	questions     [{id, prompt, answer, match}]  match: exact（默认）/ contains / regex / number
//
// 分数 = 答对题数 / 总题数 × 100；单个模型调用失败的题按答错计，全部失败的模型不入榜。

// SelfEvalQuestion 是一道题。
type SelfEvalQuestion struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
	Match  string `json:"match"`
}

type selfEvalConfig struct {
	BaseURL   string             `json:"base_url"`
	APIKeyEnv string             `json:"api_key_env"`
	Models    []string           `json:"models"`
	MaxTokens int                `json:"max_tokens"`
	Key       string             `json:"key"`
	ScoreKey  string             `json:"score_key"`
	Benchmark benchmarkDef       `json:"benchmark"`
	Questions []SelfEvalQuestion `json:"questions"`
}

// Chatter 是向某个模型提一个问题的能力（llm.Client 满足；测试可替换）。
type Chatter interface {
	ChatTools(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef, opt llm.ChatOptions, onEvent func(llm.StreamEvent)) (*llm.Completion, error)
}

// SelfEvalJob 实现 datasync.Job。
type SelfEvalJob struct {
	Job
	// NewClient 按配置构造网关客户端；nil 时用 llm.Client。
	NewClient func(baseURL, apiKey string) Chatter
}

// Grade 按规则判断回答是否正确。
func Grade(q SelfEvalQuestion, reply string) bool {
	got := strings.TrimSpace(llm.StripThink(reply))
	want := strings.TrimSpace(q.Answer)
	switch q.Match {
	case "contains":
		return strings.Contains(strings.ToLower(got), strings.ToLower(want))
	case "regex":
		re, err := regexp.Compile(want)
		return err == nil && re.MatchString(got)
	case "number":
		w, err := strconv.ParseFloat(want, 64)
		if err != nil {
			return false
		}
		// 取回答中的最后一个数字
		nums := regexp.MustCompile(`-?\d+(?:\.\d+)?`).FindAllString(strings.ReplaceAll(got, ",", ""), -1)
		if len(nums) == 0 {
			return false
		}
		g, _ := strconv.ParseFloat(nums[len(nums)-1], 64)
		return math.Abs(g-w) <= 1e-6*math.Max(1, math.Abs(w))
	default:
		norm := func(s string) string { return strings.ToLower(strings.Trim(strings.TrimSpace(s), "。.!！\"'`")) }
		return norm(got) == norm(want)
	}
}

func (j *SelfEvalJob) Run(ctx context.Context, _ *datasync.Env, src datasync.Source) (datasync.Result, error) {
	raw, _ := json.Marshal(src.Config)
	var cfg selfEvalConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return datasync.Result{}, fmt.Errorf("selfeval: decode config: %w", err)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = src.URL
	}
	if cfg.BaseURL == "" || len(cfg.Models) == 0 || len(cfg.Questions) == 0 || cfg.Benchmark.Slug == "" {
		return datasync.Result{}, errors.New("selfeval: base_url, models, questions and benchmark.slug are required")
	}
	apiKey := ""
	if cfg.APIKeyEnv != "" {
		if apiKey = os.Getenv(cfg.APIKeyEnv); apiKey == "" {
			return datasync.Result{}, fmt.Errorf("selfeval: env %s is empty", cfg.APIKeyEnv)
		}
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 256
	}
	if cfg.Key == "" {
		cfg.Key = fmt.Sprintf("self_eval:%d", src.ID)
	}
	var client Chatter
	if j.NewClient != nil {
		client = j.NewClient(cfg.BaseURL, apiKey)
	} else {
		client = &llm.Client{BaseURL: strings.TrimRight(cfg.BaseURL, "/"), APIKey: apiKey, HTTP: &http.Client{Timeout: 2 * time.Minute}}
	}

	var entries []Entry
	failedModels := 0
	for _, model := range cfg.Models {
		correct, answered := 0, 0
		var totalMs int64
		for _, q := range cfg.Questions {
			start := time.Now()
			out, err := client.ChatTools(ctx, []llm.Message{
				{Role: "system", Content: "Answer with the final answer only, no explanation."},
				{Role: "user", Content: q.Prompt},
			}, nil, llm.ChatOptions{Model: model, MaxTokens: cfg.MaxTokens, NoStream: true}, nil)
			if ctx.Err() != nil {
				return datasync.Result{}, ctx.Err()
			}
			if err != nil {
				continue
			}
			answered++
			totalMs += time.Since(start).Milliseconds()
			if Grade(q, out.Content) {
				correct++
			}
		}
		if answered == 0 {
			failedModels++
			continue
		}
		avg := int(totalMs / int64(answered))
		entries = append(entries, Entry{
			Label: model, Score: math.Round(float64(correct)/float64(len(cfg.Questions))*10000) / 100, DurationMs: &avg,
			Extra: map[string]any{"correct": correct, "total": len(cfg.Questions), "answered": answered},
		})
	}
	if len(entries) == 0 {
		return datasync.Result{}, datasync.Rejectf("all %d models failed to answer", len(cfg.Models))
	}

	b := boardConfig{Key: cfg.Key, ScoreKey: cfg.ScoreKey, Benchmark: cfg.Benchmark}
	bm, err := j.ensureBenchmark(ctx, src, "self_eval", b)
	if err != nil {
		return datasync.Result{}, err
	}
	results := make([]admin.BenchmarkResultInput, 0, len(entries))
	for _, e := range entries {
		score := e.Score
		in := admin.BenchmarkResultInput{ModelLabel: e.Label, Score: &score, AvgDurationMs: e.DurationMs}
		var vmID int64
		if err := j.db(ctx).QueryRow(ctx, `SELECT id FROM virtual_models WHERE name = $1`, e.Label).Scan(&vmID); err == nil {
			in.VirtualModelID = &vmID
		}
		extra, _ := json.Marshal(e.Extra)
		in.Extra = extra
		results = append(results, in)
	}
	run, err := j.Publisher.CreateBenchmarkRun(ctx, bm.id, admin.CreateBenchmarkRunInput{
		Origin: "self_eval", RunAt: time.Now(), CostCurrency: "USD", Results: results,
		Notes: fmt.Sprintf("自测评测：%s（%d 题 × %d 个模型）", src.Name, len(cfg.Questions), len(entries)),
	})
	if err != nil {
		return datasync.Result{}, fmt.Errorf("selfeval: create run: %w", err)
	}
	detail := map[string]any{"run_id": run.ID, "models": len(entries), "failed_models": failedModels, "status": "draft"}
	if src.AutoPublish && bm.status == "published" {
		reason, err := j.sanityCheck(ctx, bm.id, entries)
		if err != nil {
			return datasync.Result{}, err
		}
		if reason == "" {
			if _, err := j.Publisher.PublishBenchmarkRun(ctx, run.ID); err != nil {
				return datasync.Result{}, fmt.Errorf("selfeval: publish run: %w", err)
			}
			detail["status"] = "published"
		} else {
			detail["hold_reason"] = reason
		}
	}
	return datasync.Result{Status: datasync.StatusOK, ItemsFetched: len(entries), ItemsChanged: 1, Detail: detail}, nil
}
