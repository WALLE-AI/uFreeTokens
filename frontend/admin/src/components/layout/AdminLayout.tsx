import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router';
import { Database, Gift, KeyRound, Layers, ScrollText, Users, Wallet, ArrowLeftRight, Search, Sparkles } from 'lucide-react';
import { AdminHeader } from './AdminHeader';
import { AdminSidebar } from './AdminSidebar';
import { ShortcutHelp } from './ShortcutHelp';
import { CommandPalette, DataState, type Command } from '../ui';
import { findNavItem, visibleNavItems } from '../../nav';
import { useAuth } from '../../api/auth';
import { useGlobalHotkeys } from '../../hooks/useHotkeys';
import { useTodoCounts } from '../../hooks/useTodoCounts';
import { AgentProvider, useAgent } from '../../agent/AgentProvider';
import { currentPageStore, useAgentEnabled } from '../../agent/agentStore';
import { AgentDock } from '../../pages/agent/AgentDock';
import { ASSISTANT_NAME } from '../../agent/brand';

// AdminLayout：Header + Sidebar + <Outlet/>（+ 右侧智能体 Dock），结构对齐 web App.tsx
// （min-h-screen flex flex-col；主区 flex-1 overflow-y-auto h-[calc(100vh-3rem)]）。
// AgentProvider 包在最外层：Dock 与 SSE 连接在 <Outlet/> 之外，切换路由不中断（设计 §19.2）。
export function AdminLayout() {
  return (
    <AgentProvider>
      <AdminShell />
    </AgentProvider>
  );
}

function AdminShell() {
  const navigate = useNavigate();
  const location = useLocation();
  const counts = useTodoCounts();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  // 切换页面后主区域回到顶部、关闭移动端抽屉
  useEffect(() => {
    document.getElementById('admin-main')?.scrollTo({ top: 0 });
    setMobileNavOpen(false);
  }, [location.pathname]);

  // 全局助手的「当前页面」：路径带上查询参数（列表筛选、时间范围多数在 URL 里）。
  useEffect(() => {
    const path = `${location.pathname}${location.search}`.slice(0, 300);
    currentPageStore.set({ path, title: findNavItem(location.pathname)?.label ?? '工作台' });
  }, [location.pathname, location.search]);

  // ⌘K / Ctrl+K：输入框聚焦时也要能打开，所以不走 useGlobalHotkeys
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  const { me: authMe } = useAuth();
  const agentEnabled = useAgentEnabled();
  const agent = useAgent();
  const toggleDock = agent.toggleDock;
  const setDockOpen = agent.setDockOpen;
  const startAgent = agent.start;

  // ⌘J / Ctrl+J 开关智能体 Dock（与 ⌘K 一样在输入框聚焦时也生效）
  useEffect(() => {
    if (!agentEnabled) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'j') {
        e.preventDefault();
        toggleDock();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [agentEnabled, toggleDock]);

  useGlobalHotkeys({
    onGoto: (key) => {
      // G I：打开全局助手（没有独立页面）
      if (key === 'i' && agentEnabled) {
        setDockOpen(true);
        return;
      }
      const item = visibleNavItems(authMe, agentEnabled).find((i) => i.gotoKey === key);
      if (item) navigate(item.path);
    },
    onFocusSearch: () => {
      const el = document.querySelector<HTMLInputElement>('[data-page-search="true"]');
      el?.focus();
      el?.select();
    },
    onHelp: () => setHelpOpen(true),
  });

  const commands = useMemo<Command[]>(
    () => [
      ...visibleNavItems(authMe, agentEnabled).map((i) => {
        const Icon = i.icon;
        return {
          id: `page:${i.path}`,
          group: '页面',
          label: i.label,
          hint: i.gotoKey ? `G ${i.gotoKey.toUpperCase()}` : i.path,
          keywords: `${i.group ?? ''} ${i.path}`,
          icon: <Icon className="w-3.5 h-3.5" />,
          run: () => navigate(i.path),
        };
      }),
      {
        id: 'action:new-provider',
        group: '操作',
        label: '接入新供应商',
        keywords: 'provider wizard 向导 新建',
        icon: <Database className="w-3.5 h-3.5" />,
        run: () => navigate('/providers/new'),
      },
      {
        id: 'action:grant',
        group: '操作',
        label: '发放赠送余额（先选择账户）',
        keywords: 'credit grant 赠送 补偿',
        icon: <Gift className="w-3.5 h-3.5" />,
        run: () => navigate('/accounts'),
      },
      {
        id: 'action:adjust',
        group: '操作',
        label: '人工调账（先选择账户）',
        keywords: 'wallet adjust 调账 充值',
        icon: <Wallet className="w-3.5 h-3.5" />,
        run: () => navigate('/accounts'),
      },
    ],
    [navigate, authMe, agentEnabled],
  );

  const dynamicCommands = useCallback(
    (q: string): Command[] => {
      const enc = encodeURIComponent(q);
      // 输入不匹配任何跳转时也能“问助手”：执行即打开 Dock 新建会话（设计 §19.2）。
      const ask: Command[] = agentEnabled
        ? [{ id: `agent:${q}`, group: '智能体', label: `✦ 问${ASSISTANT_NAME}：${q}`, icon: <Sparkles className="w-3.5 h-3.5" />, run: () => void startAgent({ message: q }) }]
        : [];
      if (/^\d+$/.test(q)) {
        return [
          { id: `acct:${q}`, group: '跳转到对象', label: `账户 #${q}`, icon: <Users className="w-3.5 h-3.5" />, run: () => navigate(`/accounts/${q}`) },
          { id: `key:${q}`, group: '跳转到对象', label: `API Key #${q}`, icon: <KeyRound className="w-3.5 h-3.5" />, run: () => navigate(`/api-keys?q=${q}`) },
          { id: `chan:${q}`, group: '跳转到对象', label: `渠道 #${q}`, icon: <ArrowLeftRight className="w-3.5 h-3.5" />, run: () => navigate(`/channels/${q}`) },
          { id: `model:${q}`, group: '跳转到对象', label: `虚拟模型 #${q}`, icon: <Layers className="w-3.5 h-3.5" />, run: () => navigate(`/models/${q}`) },
          ...ask,
        ];
      }
      return [
        { id: `sm:${q}`, group: '搜索', label: `在虚拟模型中搜索 “${q}”`, icon: <Layers className="w-3.5 h-3.5" />, run: () => navigate(`/models?q=${enc}`) },
        { id: `sa:${q}`, group: '搜索', label: `在账户中搜索 “${q}”`, icon: <Users className="w-3.5 h-3.5" />, run: () => navigate(`/accounts?q=${enc}`) },
        { id: `sp:${q}`, group: '搜索', label: `在供应商中搜索 “${q}”`, icon: <Search className="w-3.5 h-3.5" />, run: () => navigate(`/providers?q=${enc}`) },
        { id: `sr:${q}`, group: '搜索', label: `按 request_id 查调用日志 “${q}”`, icon: <ScrollText className="w-3.5 h-3.5" />, run: () => navigate(`/logs?request_id=${enc}`) },
        ...ask,
      ];
    },
    [navigate, agentEnabled, startAgent],
  );

  return (
    <div className="min-h-screen flex flex-col">
      <AdminHeader
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenShortcuts={() => setHelpOpen(true)}
        onOpenMobileNav={() => setMobileNavOpen(true)}
        agent={
          agentEnabled
            ? { open: agent.dockOpen, running: agent.phase === 'streaming', pending: counts?.agent_pending_approvals ?? 0, onToggle: toggleDock }
            : undefined
        }
      />
      <div className="flex flex-1">
        <aside className="hidden md:block sticky top-12 h-[calc(100vh-3rem)]">
          <AdminSidebar counts={counts} />
        </aside>
        <main id="admin-main" className="flex-1 min-w-0 overflow-y-auto h-[calc(100vh-3rem)]">
          <div className="px-4 md:px-8 py-6 max-w-7xl">
            <Suspense fallback={<DataState loading skeleton="text" />}>
              <Outlet />
            </Suspense>
          </div>
        </main>
        <AgentDock />
      </div>

      {mobileNavOpen && (
        <div className="fixed inset-0 z-50 md:hidden">
          <div className="absolute inset-0 bg-black/40 backdrop-blur-xs animate-in fade-in duration-200" onClick={() => setMobileNavOpen(false)} />
          <div className="absolute inset-y-0 left-0 w-72 max-w-[85vw] bg-white shadow-2xl animate-in slide-in-from-left duration-200">
            <AdminSidebar counts={counts} onNavigate={() => setMobileNavOpen(false)} />
          </div>
        </div>
      )}

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} commands={commands} dynamicCommands={dynamicCommands} />
      <ShortcutHelp open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}
