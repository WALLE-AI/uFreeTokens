// 平台全局助手的对外名称（顶栏按钮、Dock 标题、回答署名、命令面板）。
// 后端系统提示里的自称在 internal/agent/context.go，改名时两处同步。
export const ASSISTANT_NAME = '小U';

// agentHref 返回在当前页面打开某个会话的链接（AgentProvider 读取 ?agent= 后打开 Dock 并清掉该参数）。
export function agentHref(sessionId: number | string): string {
  return `?agent=${sessionId}`;
}

// reportHref 返回在当前页面打开某份报表的链接（Dock「报表」页签）。
export function reportHref(reportId: number | string): string {
  return `?report=${reportId}`;
}
