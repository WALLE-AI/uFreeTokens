import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  cancelAgentSession,
  createAgentSession,
  decideToolCallStream,
  getAgentSession,
  sendAgentMessage,
  type AgentEvent,
  type AgentSessionDetail,
  type ContextRef,
  type DecideResult,
} from '../api/agent';
import { useCan } from '../api/auth';
import { emitMutated } from './agentEvents';
import { ctxKey, loadAgentMeta, useAgentMeta, usePageContext } from './agentStore';

// AgentProvider：Dock 开关、当前会话、上下文芯片与 SSE 连接的持有者（设计 §19.2、实施方案 M1-F02）。
// 挂在 AdminLayout 上，<Outlet/> 之外——切换路由不卸载，运行中切页面流不中断。
// 运行状态机：idle → streaming → awaiting_approval → done / error。

export type Phase = 'idle' | 'streaming' | 'awaiting_approval' | 'done' | 'error';

export interface ApprovalData {
  id: string;
  proposal_id?: number;
  tool: string;
  args: unknown;
  summary: string;
  before: unknown;
  after: unknown;
  permission: string;
  rationale?: string;
  confidence?: number | null;
  evidence?: Array<{ url: string; quote: string }> | null;
  target_type?: string;
  target_id?: string;
}

export type LiveItem =
  | { kind: 'text'; text: string }
  | { kind: 'tool'; id: string; tool: string; args: unknown; risk: string; status: string; summary?: string; http_status?: number; duration_ms?: number }
  | { kind: 'approval'; data: ApprovalData; status: string };

export interface StartInput {
  playbook?: string;
  title?: string;
  context_ref?: ContextRef[];
  message?: string;
}

interface AgentContextValue {
  dockOpen: boolean;
  setDockOpen: (open: boolean) => void;
  toggleDock: () => void;
  sessionId: number | null;
  selectSession: (id: number | null) => void;
  detail: AgentSessionDetail | null;
  detailError: unknown;
  reload: () => Promise<void>;
  phase: Phase;
  live: LiveItem[];
  pendingUser: string | null;
  lastError: string | null;
  usage: { tokens_in: number; tokens_out: number; turns: number } | null;
  chips: ContextRef[];
  dismissChip: (c: ContextRef) => void;
  send: (text: string) => Promise<void>;
  start: (input: StartInput) => Promise<void>;
  decide: (callId: string, decision: 'approve' | 'reject', opts?: { note?: string; args?: unknown; sessionId?: number }) => Promise<DecideResult | null>;
  cancel: () => Promise<void>;
}

const Ctx = createContext<AgentContextValue | null>(null);

const DOCK_KEY = 'uft_agent_dock';
const SESSION_KEY = 'uft_agent_session';

function readNum(key: string): number | null {
  const v = Number(localStorage.getItem(key));
  return Number.isFinite(v) && v > 0 ? v : null;
}

export function AgentProvider({ children }: { children: ReactNode }) {
  const allowed = useCan('agent:use');
  const meta = useAgentMeta();
  const enabled = allowed && !!meta?.enabled;

  useEffect(() => {
    if (allowed) void loadAgentMeta();
  }, [allowed]);

  const [dockOpen, setDockOpenState] = useState(() => localStorage.getItem(DOCK_KEY) === '1');
  const [sessionId, setSessionId] = useState<number | null>(() => readNum(SESSION_KEY));
  const [detail, setDetail] = useState<AgentSessionDetail | null>(null);
  const [detailError, setDetailError] = useState<unknown>(null);
  const [phase, setPhase] = useState<Phase>('idle');
  const [live, setLive] = useState<LiveItem[]>([]);
  const [pendingUser, setPendingUser] = useState<string | null>(null);
  const [lastError, setLastError] = useState<string | null>(null);
  const [usage, setUsage] = useState<AgentContextValue['usage']>(null);
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  const abortRef = useRef<AbortController | null>(null);
  const sessionRef = useRef(sessionId);
  sessionRef.current = sessionId;

  const setDockOpen = useCallback((open: boolean) => {
    setDockOpenState(open);
    localStorage.setItem(DOCK_KEY, open ? '1' : '0');
  }, []);
  const toggleDock = useCallback(() => setDockOpen(localStorage.getItem(DOCK_KEY) !== '1'), [setDockOpen]);

  const loadDetail = useCallback(async (id: number | null) => {
    if (!id) {
      setDetail(null);
      return;
    }
    try {
      const d = await getAgentSession(id);
      if (sessionRef.current !== id) return;
      setDetail(d);
      setDetailError(null);
      const st = d.session.status;
      setPhase(st === 'running' ? 'streaming' : st === 'awaiting_approval' ? 'awaiting_approval' : st === 'failed' ? 'error' : st === 'idle' ? 'idle' : 'done');
    } catch (err) {
      if (sessionRef.current === id) {
        setDetailError(err);
        setDetail(null);
      }
    }
  }, []);

  const reload = useCallback(() => loadDetail(sessionRef.current), [loadDetail]);

  // 选中会话：切换时断开当前流（服务端运行继续）。
  const selectSession = useCallback(
    (id: number | null) => {
      if (id !== sessionRef.current) {
        abortRef.current?.abort();
        setLive([]);
        setPendingUser(null);
        setLastError(null);
        setUsage(null);
      }
      setSessionId(id);
      sessionRef.current = id;
      if (id) localStorage.setItem(SESSION_KEY, String(id));
      else localStorage.removeItem(SESSION_KEY);
      void loadDetail(id);
    },
    [loadDetail],
  );

  useEffect(() => {
    if (enabled && sessionId) void loadDetail(sessionId);
    // 只在启用状态变化时恢复一次
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled]);

  // 断线恢复：会话仍在后台运行时轮询状态（一期不做断点续流）。
  useEffect(() => {
    if (!enabled || !sessionId || detail?.session.status !== 'running' || abortRef.current) return;
    const t = setInterval(() => void loadDetail(sessionId), 3000);
    return () => clearInterval(t);
  }, [enabled, sessionId, detail?.session.status, loadDetail]);

  const onEvent = useCallback((e: AgentEvent) => {
    const d = e.data;
    switch (e.event) {
      case 'run_started':
        setPhase('streaming');
        break;
      case 'text_delta':
        setLive((items) => {
          const last = items[items.length - 1];
          if (last?.kind === 'text') return [...items.slice(0, -1), { kind: 'text', text: last.text + String(d.text ?? '') }];
          return [...items, { kind: 'text', text: String(d.text ?? '') }];
        });
        break;
      case 'tool_call':
        setLive((items) => [
          ...items,
          { kind: 'tool', id: String(d.id), tool: String(d.tool), args: d.args, risk: String(d.risk ?? ''), status: 'running' },
        ]);
        break;
      case 'tool_result': {
        const id = String(d.id);
        setLive((items) => {
          let found = false;
          const next = items.map((it) => {
            if (it.kind === 'tool' && it.id === id) {
              found = true;
              return { ...it, status: String(d.status), summary: d.summary as string, http_status: d.http_status as number, duration_ms: d.duration_ms as number };
            }
            if (it.kind === 'approval' && it.data.id === id) {
              found = true;
              return { ...it, status: String(d.status) };
            }
            return it;
          });
          return found ? next : items;
        });
        if (d.status === 'executed' && d.target_type && d.target_id) {
          emitMutated({ target_type: String(d.target_type), target_id: String(d.target_id) });
        }
        break;
      }
      case 'approval_required':
        setLive((items) => [...items, { kind: 'approval', data: d as unknown as ApprovalData, status: 'pending_approval' }]);
        break;
      case 'usage':
        setUsage({ tokens_in: Number(d.tokens_in ?? 0), tokens_out: Number(d.tokens_out ?? 0), turns: Number(d.turns ?? 0) });
        break;
      case 'error':
        setLastError(String(d.message ?? d.code ?? '运行出错'));
        break;
      case 'run_finished': {
        const st = String(d.status);
        setPhase(st === 'awaiting_approval' ? 'awaiting_approval' : st === 'failed' ? 'error' : 'done');
        if (st === 'stopped' && d.reason) setLastError(`运行已停止：${String(d.reason)}`);
        break;
      }
    }
  }, []);

  // runStream 执行一次流式请求；结束后重新拉会话详情并清空实时覆盖层。
  const runStream = useCallback(
    async (id: number, fn: (signal: AbortSignal) => Promise<void>) => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      setLastError(null);
      setPhase('streaming');
      try {
        await fn(controller.signal);
      } catch (err) {
        if (!controller.signal.aborted) {
          setLastError(err instanceof Error ? err.message : String(err));
          setPhase('error');
        }
      } finally {
        if (abortRef.current === controller) abortRef.current = null;
        if (sessionRef.current === id) {
          await loadDetail(id);
          setLive([]);
          setPendingUser(null);
        }
      }
    },
    [loadDetail],
  );

  const pageCtx = usePageContext();
  const chips = useMemo(() => pageCtx.filter((c) => !dismissed.has(ctxKey(c))), [pageCtx, dismissed]);
  const dismissChip = useCallback((c: ContextRef) => setDismissed((s) => new Set(s).add(ctxKey(c))), []);

  const start = useCallback(
    async (input: StartInput) => {
      setDockOpen(true);
      const sess = await createAgentSession({ title: input.title ?? '', playbook: input.playbook ?? '', context_ref: input.context_ref });
      selectSession(sess.id);
      if (input.message) {
        setPendingUser(input.message);
        await runStream(sess.id, (signal) => sendAgentMessage(sess.id, input.message!, onEvent, signal));
      }
    },
    [onEvent, runStream, selectSession, setDockOpen],
  );

  const send = useCallback(
    async (text: string) => {
      let id = sessionRef.current;
      if (!id || detail?.read_only) {
        const sess = await createAgentSession({ title: '', playbook: '', context_ref: chips.length ? chips : undefined });
        id = sess.id;
        selectSession(id);
      }
      setPendingUser(text);
      const sid = id;
      await runStream(sid, (signal) => sendAgentMessage(sid, text, onEvent, signal));
    },
    [chips, detail?.read_only, onEvent, runStream, selectSession],
  );

  const decide = useCallback<AgentContextValue['decide']>(
    async (callId, decision, opts = {}) => {
      const id = opts.sessionId ?? sessionRef.current;
      if (!id) return null;
      let result: DecideResult | null = null;
      const handle = (e: AgentEvent) => {
        if (e.event === 'decision') result = e.data as unknown as DecideResult;
        else onEvent(e);
      };
      const body = { decision, note: opts.note ?? '', args: opts.args ?? null };
      if (id === sessionRef.current) {
        let failure: unknown = null;
        await runStream(id, async (signal) => {
          try {
            await decideToolCallStream(id, callId, body, handle, signal);
          } catch (err) {
            failure = err;
            throw err;
          }
        });
        if (failure) throw failure;
      } else {
        await decideToolCallStream(id, callId, body, handle);
      }
      return result;
    },
    [onEvent, runStream],
  );

  const cancel = useCallback(async () => {
    const id = sessionRef.current;
    if (!id) return;
    await cancelAgentSession(id).catch(() => undefined);
  }, []);

  const value = useMemo<AgentContextValue>(
    () => ({
      dockOpen: enabled && dockOpen,
      setDockOpen,
      toggleDock,
      sessionId,
      selectSession,
      detail,
      detailError,
      reload,
      phase,
      live,
      pendingUser,
      lastError,
      usage,
      chips,
      dismissChip,
      send,
      start,
      decide,
      cancel,
    }),
    [enabled, dockOpen, setDockOpen, toggleDock, sessionId, selectSession, detail, detailError, reload, phase, live, pendingUser, lastError, usage, chips, dismissChip, send, start, decide, cancel],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAgent(): AgentContextValue {
  const v = useContext(Ctx);
  if (!v) throw new Error('useAgent must be used inside <AgentProvider>');
  return v;
}

// useAgentOptional 供可能渲染在 Provider 之外的嵌入组件使用。
export function useAgentOptional(): AgentContextValue | null {
  return useContext(Ctx);
}
