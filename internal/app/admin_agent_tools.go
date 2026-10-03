package app

import (
	"context"
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/research"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/routes"
)

// AgentToolDeps 是构建智能体工具集需要的依赖。
type AgentToolDeps struct {
	// Handler 是 NewAdminToolHandler 构造的内部路由。
	Handler http.Handler
	// Fetch 是 fetch_page 的出站环境（通常是 datasync.Env）；nil 时不提供 fetch_page。
	Fetch research.Fetcher
	// AllowDomains 返回 fetch_page 的出站白名单。
	AllowDomains func(ctx context.Context) ([]string, error)
}

// BuildAgentTools 构建全部工具：绑定路由的读/写工具 + 研究工具。任何工具声明与路由表不一致都返回错误
// （启动即失败，而不是运行时才发现）。
func BuildAgentTools(d AgentToolDeps) ([]kernel.Tool, error) {
	disp := &routes.Dispatcher{Handler: d.Handler}
	built, err := routes.Build(routes.AllSpecs(), routes.BuildDeps{
		BodySchema:      RouteRequestSchema,
		RoutePermission: RoutePermission,
		Dispatcher:      disp,
	})
	if err != nil {
		return nil, err
	}
	tools := make([]kernel.Tool, 0, len(built)+2)
	for _, t := range built {
		tools = append(tools, t)
	}
	tools = append(tools, &research.SearchCatalog{Dispatcher: disp})
	if d.Fetch != nil && d.AllowDomains != nil {
		tools = append(tools, &research.FetchPage{Env: d.Fetch, AllowDomains: d.AllowDomains})
	}
	return tools, nil
}
