package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 运营智能体（Harness）指标（实施方案 M4-B04）。注册在默认注册表：cmd/admin 由
// UFT_ADMIN_METRICS_ADDR 暴露，cmd/worker 的 /metrics 同时汇总默认注册表。
// 标签只到剧本 / 工具 / 状态级，不带会话或管理员 ID。
var (
	agentRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_runs_total", Help: "智能体运行次数（按结束状态与剧本）",
	}, []string{"status", "playbook"})
	agentRunDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "agent_run_duration_seconds", Help: "智能体单次运行耗时",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"playbook"})
	agentTokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_tokens_total", Help: "智能体消耗的 Token（direction=in/out）",
	}, []string{"direction"})
	agentToolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_tool_calls_total", Help: "智能体工具调用次数（按工具与状态）",
	}, []string{"tool", "status"})
	agentApprovalsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_approvals_total", Help: "智能体提案的审批决定（approve/reject）",
	}, []string{"decision"})
	agentRejectedRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "agent_proposals_rejected_ratio", Help: "后台作业最近 50 条已处理提案的拒绝率（熔断依据）",
	}, []string{"job"})
)

// ObserveAgentRun 记录一次运行。
func ObserveAgentRun(playbook, status string, tokensIn, tokensOut int, d time.Duration) {
	if playbook == "" {
		playbook = "free_chat"
	}
	agentRunsTotal.WithLabelValues(status, playbook).Inc()
	agentRunDuration.WithLabelValues(playbook).Observe(d.Seconds())
	agentTokensTotal.WithLabelValues("in").Add(float64(tokensIn))
	agentTokensTotal.WithLabelValues("out").Add(float64(tokensOut))
}

// ObserveAgentToolCall 记录一次工具调用。
func ObserveAgentToolCall(tool, status string) {
	agentToolCallsTotal.WithLabelValues(tool, status).Inc()
}

// ObserveAgentDecision 记录一次审批决定；decision 为空时只预注册（让指标从启动起可见）。
func ObserveAgentDecision(decision string) {
	if decision == "" {
		agentApprovalsTotal.WithLabelValues("approve")
		agentApprovalsTotal.WithLabelValues("reject")
		return
	}
	agentApprovalsTotal.WithLabelValues(decision).Inc()
}

// SetAgentRejectedRatio 上报后台作业的提案拒绝率。
func SetAgentRejectedRatio(job string, ratio float64) {
	agentRejectedRatio.WithLabelValues(job).Set(ratio)
}
