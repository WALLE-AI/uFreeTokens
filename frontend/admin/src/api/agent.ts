import { buildHeaders, buildURL, handleUnauthorized, request } from './client';
import { ApiError } from './errors';
import type {
  AgentMetaResponse,
  AgentReportDetail,
  AgentSessionDetail,
  CreateSessionInput,
  CursorPage_Report,
  CursorPage_Session,
  Dataset,
  DecideInput,
  DecideResult,
  Job,
  PageContext,
  Proposal,
  Session,
  UpdateInput,
} from './generated';

// 运营智能体（Harness）接口：会话、SSE 运行、审批、提案收件箱、后台作业
// （《运营后台 Agent 模块（Harness 智能体）技术架构设计方案》§5、docs/admin-api.md「智能体」）。

export type {
  AgentMetaResponse,
  AgentSessionDetail,
  DecideResult,
  AgentReportDetail,
  Dataset as AgentDataset,
  Report as AgentReport,
  Job as AgentJob,
  Message as AgentMessage,
  PageContext,
  Proposal as AgentProposal,
  Session as AgentSession,
  ToolCallView as AgentToolCall,
} from './generated';

export interface ContextRef {
  type: string;
  id: string;
  label?: string;
}

export function getAgentMeta(signal?: AbortSignal): Promise<AgentMetaResponse> {
  return request('/agent/meta', { signal });
}

export function listAgentSessions(
  query: { q?: string; before?: string; limit?: number; scope?: 'all'; mode?: 'interactive' | 'batch'; include_archived?: boolean } = {},
  signal?: AbortSignal,
): Promise<CursorPage_Session> {
  return request('/agent/sessions', { query, signal });
}

export function createAgentSession(input: Omit<CreateSessionInput, 'context_ref'> & { context_ref?: ContextRef[] }): Promise<Session> {
  return request('/agent/sessions', { method: 'POST', body: input });
}

export function getAgentSession(id: number, signal?: AbortSignal): Promise<AgentSessionDetail> {
  return request(`/agent/sessions/${id}`, { signal });
}

export function updateAgentSession(id: number, body: { title?: string; archived?: boolean }): Promise<Session> {
  return request(`/agent/sessions/${id}`, { method: 'PATCH', body });
}

export function cancelAgentSession(id: number): Promise<{ status: string }> {
  return request(`/agent/sessions/${id}/cancel`, { method: 'POST' });
}

// decideToolCall 以 JSON 方式审批（收件箱批量通过用：不继续运行智能体）。
export function decideToolCall(sessionId: number, callId: string, body: DecideInput): Promise<DecideResult> {
  return request(`/agent/sessions/${sessionId}/tool-calls/${encodeURIComponent(callId)}/decision`, { method: 'POST', body, timeoutMs: 90_000 });
}

export function listAgentProposals(
  query: { status?: string; target_type?: string; target_ids?: string[]; playbook?: string; session_id?: number; before?: number; limit?: number; mine?: 0 | 1 } = {},
  signal?: AbortSignal,
): Promise<{ data: Proposal[] | null; next_cursor: string }> {
  return request('/agent/proposals', { query, signal });
}

export function listAgentJobs(signal?: AbortSignal): Promise<{ data: Job[] | null }> {
  return request('/agent/jobs', { signal });
}

export function updateAgentJob(id: number, body: UpdateInput): Promise<Job> {
  return request(`/agent/jobs/${id}`, { method: 'PATCH', body });
}

export function runAgentJob(id: number): Promise<Job> {
  return request(`/agent/jobs/${id}/run`, { method: 'POST' });
}

// ---------- SSE ----------

// AgentEvent 是 SSE 流中的一个事件（event 名 + JSON data），协议见设计 §5.1。
export interface AgentEvent {
  event: string;
  data: Record<string, unknown>;
}

// parseSSE 是增量 SSE 解析器：feed 任意分片的文本，回调每个完整事件；注释行（: ping）忽略。
export function createSSEParser(onEvent: (e: AgentEvent) => void) {
  let buf = '';
  let event = '';
  let data: string[] = [];
  const dispatch = () => {
    if (data.length > 0 || event) {
      let parsed: Record<string, unknown> = {};
      try {
        parsed = data.length ? (JSON.parse(data.join('\n')) as Record<string, unknown>) : {};
      } catch {
        parsed = { raw: data.join('\n') };
      }
      onEvent({ event: event || 'message', data: parsed });
    }
    event = '';
    data = [];
  };
  return {
    feed(chunk: string) {
      buf += chunk;
      let idx: number;
      while ((idx = buf.indexOf('\n')) >= 0) {
        let line = buf.slice(0, idx);
        buf = buf.slice(idx + 1);
        if (line.endsWith('\r')) line = line.slice(0, -1);
        if (line === '') dispatch();
        else if (line.startsWith(':')) continue;
        else if (line.startsWith('event:')) event = line.slice(6).trim();
        else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
      }
    },
    end() {
      if (buf) this.feed('\n');
      dispatch();
    },
  };
}

// streamAgent 发起一个 SSE 请求（不能用 EventSource：它无法携带 Authorization 头）。
// 返回时流已结束；signal 中止只断开连接，服务端运行继续（重连后拉会话详情）。
export async function streamAgent(path: string, body: unknown, onEvent: (e: AgentEvent) => void, signal?: AbortSignal): Promise<void> {
  const res = await fetch(buildURL(path), {
    method: 'POST',
    headers: buildHeaders({ json: true, accept: 'text/event-stream' }),
    body: JSON.stringify(body),
    credentials: 'same-origin',
    signal,
  });
  if (!res.ok) {
    const err = await ApiError.fromResponse(res);
    if (res.status === 401) handleUnauthorized();
    throw err;
  }
  if (!res.body) return;
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  const parser = createSSEParser(onEvent);
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    parser.feed(decoder.decode(value, { stream: true }));
  }
  parser.end();
}

// page 是发送时所在的后台页面（全局助手据此理解「这个」「当前筛选」），只作用于本次运行。
export function sendAgentMessage(sessionId: number, content: string, onEvent: (e: AgentEvent) => void, signal?: AbortSignal, page?: PageContext | null) {
  return streamAgent(`/agent/sessions/${sessionId}/messages`, page ? { content, page } : { content }, onEvent, signal);
}

export function decideToolCallStream(sessionId: number, callId: string, body: DecideInput, onEvent: (e: AgentEvent) => void, signal?: AbortSignal) {
  return streamAgent(`/agent/sessions/${sessionId}/tool-calls/${encodeURIComponent(callId)}/decision?stream=1`, body, onEvent, signal);
}

// ---------- 数据集与报表（《运营后台全局助手执行方案》P2/P3） ----------

const datasetCache = new Map<number, Promise<Dataset>>();

// getAgentDataset 读取助手数据集（写入后不再修改，按 ID 缓存）。
export function getAgentDataset(id: number): Promise<Dataset> {
  let p = datasetCache.get(id);
  if (!p) {
    p = request<Dataset>(`/agent/datasets/${id}`);
    p.catch(() => datasetCache.delete(id));
    datasetCache.set(id, p);
  }
  return p;
}

export function listAgentReports(query: { scope?: 'mine' | 'all'; q?: string; before?: string; limit?: number } = {}, signal?: AbortSignal): Promise<CursorPage_Report> {
  return request('/agent/reports', { query, signal });
}

export function getAgentReport(id: number, signal?: AbortSignal): Promise<AgentReportDetail> {
  return request(`/agent/reports/${id}`, { signal });
}

export function updateAgentReport(id: number, body: { title?: string; visibility?: 'private' | 'shared' }) {
  return request(`/agent/reports/${id}`, { method: 'PATCH', body });
}

export function deleteAgentReport(id: number): Promise<void> {
  return request(`/agent/reports/${id}`, { method: 'DELETE' });
}

// downloadAgentFile 带会话令牌下载导出文件（报表 xlsx / md、数据集 csv）。
export async function downloadAgentFile(path: string, filename: string): Promise<void> {
  const res = await fetch(buildURL(path), { headers: buildHeaders(), credentials: 'same-origin' });
  if (res.status === 401) handleUnauthorized();
  if (!res.ok) throw await ApiError.fromResponse(res);
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
