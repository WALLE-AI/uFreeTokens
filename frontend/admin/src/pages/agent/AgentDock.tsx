import { useEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { Maximize2, Plus, Sparkles, X } from 'lucide-react';
import { IconButton } from '../../components/ui';
import { useAgent } from '../../agent/AgentProvider';
import { useAgentMeta } from '../../agent/agentStore';
import { cn } from '../../lib/cn';
import { Conversation } from './components/Conversation';

// Agent Dock（设计 §19.2）：
//   ≥1280px 推挤式停靠（w-[420px] z-30，主区收窄，与业务页面并排）；
//   768–1279px 覆盖式（复用 DetailDrawer 配方，但不加遮罩点击关闭，防止运行中误关）；
//   <768px 全屏面板（只保证看结果、能审批）。
// 挂在 AdminLayout 中 <main> 之后、<Outlet/> 之外，路由切换不卸载。

function useMedia(query: string): boolean {
  const [match, setMatch] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const on = () => setMatch(mq.matches);
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, [query]);
  return match;
}

const PHASE_LABEL: Record<string, string> = { streaming: '运行中', awaiting_approval: '待审批', error: '出错', done: '', idle: '' };

export function AgentDock() {
  const { dockOpen, setDockOpen, detail, phase, selectSession, sessionId } = useAgent();
  const meta = useAgentMeta();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const wide = useMedia('(min-width: 1280px)');
  const mobile = !useMedia('(min-width: 768px)');
  // 会话工作台本身就是同一个对话的大尺寸形态，不重复显示 Dock。
  const onAgentPage = pathname === '/agent' || /^\/agent\/\d+/.test(pathname);
  if (!dockOpen || !meta?.enabled || onAgentPage) return null;

  const title = detail?.session.title || (sessionId ? `会话 #${sessionId}` : '运营智能体');
  const header = (
    <div className="h-11 px-3 border-b border-gray-200 flex items-center gap-2 shrink-0">
      <Sparkles className="w-4 h-4 text-purple-600" />
      <span className="text-xs font-semibold text-gray-900 truncate flex-1">
        {title}
        {PHASE_LABEL[phase] && <span className={cn('ml-1.5 font-normal', phase === 'streaming' ? 'text-purple-600' : 'text-amber-600')}>· {PHASE_LABEL[phase]}</span>}
      </span>
      <IconButton label="新会话" onClick={() => selectSession(null)}>
        <Plus className="w-4 h-4" />
      </IconButton>
      <IconButton label="在工作台打开" onClick={() => navigate(sessionId ? `/agent/${sessionId}` : '/agent')}>
        <Maximize2 className="w-3.5 h-3.5" />
      </IconButton>
      <IconButton label="收起（⌘J）" onClick={() => setDockOpen(false)}>
        <X className="w-4 h-4" />
      </IconButton>
    </div>
  );

  if (wide) {
    return (
      <aside className="sticky top-12 h-[calc(100vh-3rem)] w-[420px] shrink-0 border-l border-gray-200 bg-white z-30 flex flex-col">
        {header}
        <Conversation className="flex-1" />
      </aside>
    );
  }
  return (
    <aside
      className={cn(
        'fixed z-50 bg-white flex flex-col shadow-2xl animate-in slide-in-from-right duration-200',
        mobile ? 'inset-0' : 'inset-y-0 right-0 w-[560px] max-w-[95vw] border-l border-gray-200',
      )}
    >
      {header}
      <Conversation className="flex-1" />
    </aside>
  );
}
