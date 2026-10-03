package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/agent"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/llmmodel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/research"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/llm"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

// AgentSetup 是构造运营智能体需要的依赖（cmd/admin 与 cmd/worker 共用，实施方案 M1-B08 / M3-B04）。
type AgentSetup struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Admin  *admin.Service
	// Deps 用于构造内部工具路由（NewAdminToolHandler）；其中的 Agent 字段被忽略。
	Deps     AdminDeps
	Fetch    *datasync.Env
	Logger   *slog.Logger
	Location *time.Location
	// Batch 为 true 时默认模型取 agent.batch_model（后台作业）。
	Batch bool
}

// NewAgentService 组装智能体：工具（绑定路由 + 研究工具）、模型（agent.llm_* 回落 datasync.llm_*）、
// PG 存储与审批审计。未启用或 LLM 未配置时返回的 Service.Enabled() 为 false，Missing 列出缺失项；
// 只有工具声明与路由表不一致才返回错误（启动即失败）。
func NewAgentService(s AgentSetup) (*agent.Service, error) {
	deps := s.Deps
	deps.Agent = nil
	domains := &research.DomainSource{Pool: s.Pool, Extra: research.SplitDomains(s.Config.Agent.FetchAllowDomains)}
	var fetch research.Fetcher
	if s.Fetch != nil {
		fetch = s.Fetch
	}
	st := pgstore.New(s.Pool)
	tools, err := BuildAgentTools(AgentToolDeps{Handler: NewAdminToolHandler(deps), Fetch: fetch, AllowDomains: domains.Domains, Datasets: st, Reports: st})
	if err != nil {
		return nil, err
	}
	ac := s.Config.Agent
	settings := s.Config.LLMSettings()
	model := settings.LLMModel
	if s.Batch && ac.BatchModel != "" {
		model = ac.BatchModel
	}
	svc := &agent.Service{
		Cfg: agent.Config{
			Enabled: ac.Enabled, Model: model, BatchModel: ac.BatchModel, RunTimeout: ac.RunTimeout, Location: s.Location,
			Budget: kernel.Budget{MaxTurns: ac.MaxTurns, MaxToolCalls: ac.MaxToolCalls, MaxTokens: ac.MaxTokens},
		},
		Store: st, Tools: tools, Logger: s.Logger, Missing: []string{},
	}
	if !ac.Enabled {
		svc.Missing = append(svc.Missing, "agent.enabled (UFT_AGENT_ENABLED=true)")
	}
	if c, missing := llm.FromConfig(settings); c != nil {
		svc.Model = llmmodel.New(c)
	} else {
		svc.Missing = append(svc.Missing, missing...)
	}
	if s.Admin != nil {
		adminSvc := s.Admin
		svc.Audit = func(ctx context.Context, e agent.AuditEvent) error {
			_, err := adminSvc.RecordAudit(ctx, admin.AuditLogInput{
				ActorID: e.Actor.AdminID, ActorName: e.Actor.Name, SessionID: e.Actor.SessionID,
				Action: "agent.decision", TargetType: "agent_tool_call", TargetID: e.ToolCallID,
				After: map[string]any{"decision": e.Decision, "tool": e.Tool, "target_type": e.TargetType, "target_id": e.TargetID,
					"note": e.Note, "args": e.Args},
				AgentSessionID: e.SessionID, AgentToolCallID: e.ToolCallID,
			})
			return err
		}
	}
	observability.ObserveAgentDecision("") // 预注册指标，/metrics 上从启动起可见
	return svc, nil
}
