import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Expand, Info, Maximize2, Minimize2, Plus, Shrink, Sparkles, X } from 'lucide-react';
import { IconButton } from '../../components/ui';
import { useAgent, type DockSize } from '../../agent/AgentProvider';
import { useAgentMeta } from '../../agent/agentStore';
import { ASSISTANT_NAME } from '../../agent/brand';
import { useDismiss } from '../../hooks/useDismiss';
import { cn } from '../../lib/cn';
import { formatDateTime } from '../../lib/time';
import { Conversation } from './components/Conversation';
import { ReportsPanel } from './components/ReportsPanel';
import { SessionList } from './components/SessionList';

// Agent Dock：平台全局助手的唯一入口（原 /agent 会话工作台已并入，见《运营后台全局助手执行方案》P0）。
//   ≥1280px 标准 420px 推挤式停靠（主区收窄，与业务页面并排）；宽屏 760px 浮在主区之上；
//   768–1279px 覆盖式（标准 560px / 宽屏 900px，不加遮罩点击关闭，防止运行中误关）；
//   全屏：覆盖顶栏以下的整个主区，用于阅读长回答、报表与图表；<768px 始终全屏。
// 页签：对话 / 历史（会话列表）/ 报表（助手生成的报表，可分享与导出）。挂在 AdminLayout 中 <main> 之后、<Outlet/> 之外，路由切换不卸载。

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

// Popover：图标按钮 + 下拉面板（点外 / Esc 关闭）。
function Popover({ label, icon, width = 'w-56', children }: { label: string; icon: ReactNode; width?: string; children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(ref, open, close);
  return (
    <div ref={ref} className="relative">
      <IconButton label={label} onClick={() => setOpen((o) => !o)}>
        {icon}
      </IconButton>
      {open && (
        <div className={cn('absolute right-0 top-8 bg-white border border-gray-200 rounded-lg shadow-lg z-50 text-xs animate-in fade-in zoom-in-95 duration-100', width)}>
          {children(close)}
        </div>
      )}
    </div>
  );
}

function NewSessionMenu() {
  const { selectSession, setDockTab, start } = useAgent();
  const meta = useAgentMeta();
  const playbooks = meta?.playbooks ?? [];
  return (
    <Popover label="新会话" icon={<Plus className="w-4 h-4" />} width="w-64">
      {(close) => (
        <div className="py-1 max-h-80 overflow-y-auto">
          <button
            type="button"
            onClick={() => {
              close();
              selectSession(null);
              setDockTab('chat');
            }}
            className="w-full px-3 py-1.5 text-left hover:bg-gray-50 cursor-pointer text-gray-900"
          >
            自由对话
          </button>
          {playbooks.length > 0 && <div className="px-3 pt-2 pb-1 text-[10px] text-gray-400 uppercase tracking-wider font-semibold border-t border-gray-100">剧本</div>}
          {playbooks.map((p) => (
            <button
              key={p.name}
              type="button"
              title={p.description}
              onClick={() => {
                close();
                void start({ playbook: p.name, message: p.starter });
              }}
              className="w-full px-3 py-1.5 text-left hover:bg-gray-50 cursor-pointer"
            >
              <div className="text-gray-900">{p.title}</div>
              {p.description && <div className="text-[11px] text-gray-500 line-clamp-2">{p.description}</div>}
            </button>
          ))}
        </div>
      )}
    </Popover>
  );
}

function SessionInfo() {
  const { detail, usage } = useAgent();
  const meta = useAgentMeta();
  const s = detail?.session;
  if (!s) return null;
  return (
    <Popover label="会话信息" icon={<Info className="w-3.5 h-3.5" />} width="w-64">
      {() => (
        <div className="p-3 space-y-3">
          <div>
            <div className="text-gray-900 font-medium break-words">{s.title || `会话 #${s.id}`}</div>
            <div className="text-gray-500 mt-0.5">
              {s.admin_name} · {formatDateTime(s.created_at)}
            </div>
            {s.playbook && <div className="text-gray-500 mt-0.5">剧本：{meta?.playbooks?.find((p) => p.name === s.playbook)?.title ?? s.playbook}</div>}
          </div>
          <div className="space-y-0.5 text-gray-600">
            <div>
              状态：{s.status}
              {s.status_reason ? `（${s.status_reason}）` : ''}
            </div>
            <div>
              轮数：{usage?.turns ? `${usage.turns} / ` : ''}
              {s.turns}（上限 {meta?.max_turns}）
            </div>
            <div>
              Token：{((s.tokens_in + s.tokens_out) / 1000).toFixed(1)}k / {((meta?.max_tokens ?? 0) / 1000).toFixed(0)}k
            </div>
            <div>模型：{s.model || meta?.model}</div>
          </div>
        </div>
      )}
    </Popover>
  );
}

export function AgentDock() {
  const { dockOpen, setDockOpen, dockSize, setDockSize, dockTab, setDockTab, detail, phase, sessionId } = useAgent();
  const meta = useAgentMeta();
  const desktop = useMedia('(min-width: 1280px)');
  const mobile = !useMedia('(min-width: 768px)');
  if (!dockOpen || !meta?.enabled) return null;

  const size: DockSize = mobile ? 'full' : dockSize;
  const title = detail?.session.title || (sessionId ? `会话 #${sessionId}` : ASSISTANT_NAME);
  const tab = (key: typeof dockTab, label: string) => (
    <button
      type="button"
      onClick={() => setDockTab(key)}
      className={cn('px-2 py-0.5 rounded-md cursor-pointer', dockTab === key ? 'bg-purple-50 text-purple-700 font-medium' : 'text-gray-500 hover:text-gray-800')}
    >
      {label}
    </button>
  );
  const header = (
    <div className="h-11 px-3 border-b border-gray-200 flex items-center gap-2 shrink-0">
      <Sparkles className="w-4 h-4 text-purple-600 shrink-0" />
      <span className="text-xs font-semibold text-gray-900 truncate flex-1 min-w-0">
        {title}
        {PHASE_LABEL[phase] && <span className={cn('ml-1.5 font-normal', phase === 'streaming' ? 'text-purple-600' : 'text-amber-600')}>· {PHASE_LABEL[phase]}</span>}
      </span>
      <div className="flex items-center gap-0.5 text-[11px] shrink-0">
        {tab('chat', '对话')}
        {tab('history', '历史')}
        {tab('reports', '报表')}
      </div>
      <div className="flex items-center shrink-0">
        <NewSessionMenu />
        <SessionInfo />
        {!mobile && size !== 'full' && (
          <IconButton label={size === 'wide' ? '标准宽度' : '加宽'} onClick={() => setDockSize(size === 'wide' ? 'normal' : 'wide')}>
            {size === 'wide' ? <Shrink className="w-3.5 h-3.5" /> : <Expand className="w-3.5 h-3.5" />}
          </IconButton>
        )}
        {!mobile && (
          <IconButton label={size === 'full' ? '退出全屏' : '全屏'} onClick={() => setDockSize(size === 'full' ? 'normal' : 'full')}>
            {size === 'full' ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
          </IconButton>
        )}
        <IconButton label="收起（⌘J）" onClick={() => setDockOpen(false)}>
          <X className="w-4 h-4" />
        </IconButton>
      </div>
    </div>
  );
  const body = (
    <div className={cn('flex-1 min-h-0 flex flex-col', size === 'full' && 'w-full max-w-4xl mx-auto')}>
      {dockTab === 'history' ? <SessionList className="flex-1" /> : dockTab === 'reports' ? <ReportsPanel /> : <Conversation className="flex-1" />}
    </div>
  );

  if (size === 'full') {
    return (
      <aside className={cn('fixed z-50 bg-white flex flex-col', mobile ? 'inset-0' : 'inset-x-0 bottom-0 top-12 border-t border-gray-200 shadow-2xl')}>
        {header}
        {body}
      </aside>
    );
  }
  if (desktop && size === 'normal') {
    return (
      <aside className="sticky top-12 h-[calc(100vh-3rem)] w-[420px] shrink-0 border-l border-gray-200 bg-white z-30 flex flex-col">
        {header}
        {body}
      </aside>
    );
  }
  if (desktop) {
    // 宽屏浮在主区之上（不推挤）：760px 推挤会把业务页面压到 500px 以下。
    return (
      <aside className="fixed top-12 bottom-0 right-0 w-[760px] max-w-[70vw] z-40 bg-white flex flex-col border-l border-gray-200 shadow-2xl">
        {header}
        {body}
      </aside>
    );
  }
  return (
    <aside
      className={cn(
        'fixed z-50 inset-y-0 right-0 max-w-[95vw] bg-white flex flex-col shadow-2xl border-l border-gray-200 animate-in slide-in-from-right duration-200',
        size === 'wide' ? 'w-[900px]' : 'w-[560px]',
      )}
    >
      {header}
      {body}
    </aside>
  );
}
