import React, { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { AlertTriangle, Play, Square } from 'lucide-react';
import { useApiKey } from '../../api/auth';
import { resolveURL } from '../../api/client';
import { parseSSE } from '../../api/sse';
import { CopyButton } from '../components/CopyButton';
import { useLocale, useT } from '../i18n';
import type { OperationEntry } from './openapi';
import { exampleBody } from './snippets';

interface Result {
  status: number;
  ms: number;
  requestId: string;
  body: string;
  errorCode?: string;
}

function pretty(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

// 计费接口的调试默认把 max_tokens 压到 64，避免误点一次就按示例的大额度预扣。
function tryItBody(entry: OperationEntry): unknown {
  const body = exampleBody(entry);
  if (entry.op['x-billable'] && body && typeof body === 'object' && 'max_tokens' in body) {
    return { ...(body as Record<string, unknown>), max_tokens: 64 };
  }
  return body;
}

// TryIt 发起真实请求（同源，走 dev proxy / Nginx）。Key 优先用用户在本站
// "连接 Key"时存下的那把；没有就在这里临时填，临时 Key 只放组件 state，不落盘。
export const TryIt: React.FC<{ entry: OperationEntry }> = ({ entry }) => {
  const t = useT();
  const locale = useLocale();
  const connectedKey = useApiKey();
  const { op } = entry;
  const needsKey = op['x-auth'] !== 'none';
  const queryParams = (op.parameters ?? []).filter((p) => p.in === 'query');
  const initialBody = tryItBody(entry);

  const [tempKey, setTempKey] = useState('');
  const [query, setQuery] = useState<Record<string, string>>({});
  const [body, setBody] = useState(initialBody === undefined ? '' : JSON.stringify(initialBody, null, 2));
  const [sending, setSending] = useState(false);
  const [problem, setProblem] = useState('');
  const [result, setResult] = useState<Result | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  // 切换接口时重置；离开页面时中止进行中的请求。
  useEffect(() => {
    setBody(initialBody === undefined ? '' : JSON.stringify(initialBody, null, 2));
    setQuery({});
    setResult(null);
    setProblem('');
    return () => abortRef.current?.abort();
  }, [op.operationId]);

  const send = async () => {
    setProblem('');
    const key = tempKey.trim() || connectedKey;
    if (needsKey && !key) {
      setProblem(t('apiKeyMissing'));
      return;
    }
    let payload: unknown;
    if (body.trim()) {
      try {
        payload = JSON.parse(body);
      } catch {
        setProblem(t('invalidJson'));
        return;
      }
    }

    const qs = Object.entries(query)
      .filter(([, v]) => v.trim())
      .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v.trim())}`)
      .join('&');
    const headers: Record<string, string> = {};
    if (needsKey && key) headers.Authorization = `Bearer ${key}`;
    if (payload !== undefined) headers['Content-Type'] = 'application/json';

    const controller = new AbortController();
    abortRef.current = controller;
    setSending(true);
    setResult(null);
    const started = performance.now();
    try {
      const res = await fetch(resolveURL(`${entry.path}${qs ? `?${qs}` : ''}`), {
        method: entry.method,
        headers,
        body: payload !== undefined ? JSON.stringify(payload) : undefined,
        signal: controller.signal,
      });
      const base = { status: res.status, requestId: res.headers.get('X-Request-Id') ?? '' };
      const isSSE = res.ok && (res.headers.get('Content-Type') ?? '').includes('text/event-stream') && res.body;
      if (isSSE) {
        let text = '';
        for await (const data of parseSSE(res.body!)) {
          text += `data: ${data}\n\n`;
          setResult({ ...base, ms: Math.round(performance.now() - started), body: text });
        }
        setResult({ ...base, ms: Math.round(performance.now() - started), body: `${text}data: [DONE]\n` });
      } else {
        const text = await res.text();
        let errorCode: string | undefined;
        if (!res.ok) {
          try {
            errorCode = JSON.parse(text)?.error?.code;
          } catch {
            // 非 JSON 错误体（比如网关没起、反代返回的 HTML），按原文展示。
          }
        }
        setResult({ ...base, ms: Math.round(performance.now() - started), body: pretty(text), errorCode });
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') setProblem(t('networkError'));
    } finally {
      setSending(false);
      abortRef.current = null;
    }
  };

  const statusCls = !result
    ? ''
    : result.status < 300
      ? 'text-emerald-700 bg-emerald-50 border-emerald-200'
      : result.status < 500
        ? 'text-amber-700 bg-amber-50 border-amber-200'
        : 'text-rose-700 bg-rose-50 border-rose-200';

  return (
    <div className="rounded-lg border border-gray-200 bg-white overflow-hidden">
      <div className="px-4 py-2.5 border-b border-gray-200 bg-gray-50 text-xs font-semibold text-gray-800">{t('tryIt')}</div>
      <div className="p-4 space-y-3 text-xs">
        {op['x-billable'] && (
          <p className="flex gap-2 items-start rounded-md bg-amber-50 border border-amber-200 px-3 py-2 text-amber-900">
            <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0 text-amber-600" />
            {t('billableWarning')}
          </p>
        )}

        {needsKey && (
          <label className="block space-y-1">
            <span className="font-medium text-gray-700">{t('apiKey')}</span>
            <input
              type="password"
              autoComplete="off"
              value={tempKey}
              onChange={(e) => setTempKey(e.target.value)}
              placeholder={connectedKey ? `${connectedKey.slice(0, 7)}••••${connectedKey.slice(-4)}` : t('apiKeyPlaceholder')}
              className="w-full rounded-md border border-gray-200 px-2.5 py-1.5 font-mono outline-none focus:border-purple-400"
            />
            {connectedKey && !tempKey && <span className="block text-[11px] text-gray-500">{t('apiKeyConnected')}</span>}
          </label>
        )}

        {queryParams.map((p) => (
          <label key={p.name} className="block space-y-1">
            <span className="font-medium text-gray-700 font-mono">{p.name}</span>
            <input
              value={query[p.name] ?? ''}
              onChange={(e) => setQuery((q) => ({ ...q, [p.name]: e.target.value }))}
              placeholder={p.example !== undefined ? String(p.example) : ''}
              className="w-full rounded-md border border-gray-200 px-2.5 py-1.5 font-mono outline-none focus:border-purple-400"
            />
          </label>
        ))}

        {initialBody !== undefined && (
          <label className="block space-y-1">
            <span className="font-medium text-gray-700">{t('body')}</span>
            <textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              spellCheck={false}
              rows={Math.min(16, body.split('\n').length + 1)}
              className="w-full rounded-md border border-gray-200 px-2.5 py-2 font-mono text-[12px] leading-relaxed outline-none focus:border-purple-400 resize-y"
            />
          </label>
        )}

        {problem && <p className="text-rose-700">{problem}</p>}

        <div className="flex gap-2">
          <button
            type="button"
            onClick={send}
            disabled={sending}
            className="inline-flex items-center gap-1.5 px-3.5 py-1.5 rounded-md bg-purple-600 hover:bg-purple-700 disabled:opacity-60 text-white font-semibold cursor-pointer"
          >
            <Play className="w-3.5 h-3.5 fill-current" />
            {sending ? t('sending') : t('send')}
          </button>
          {sending && (
            <button
              type="button"
              onClick={() => abortRef.current?.abort()}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-gray-200 text-gray-700 hover:bg-gray-50 cursor-pointer"
            >
              <Square className="w-3 h-3" />
              {t('abort')}
            </button>
          )}
        </div>
      </div>

      <div className="border-t border-gray-200">
        <div className="px-4 py-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500 bg-gray-50">
          <span className="font-semibold text-gray-800">{t('response')}</span>
          {result && (
            <>
              <span className={`font-mono font-bold border rounded px-1.5 ${statusCls}`}>{result.status}</span>
              <span>
                {t('duration')} {result.ms}ms
              </span>
              {result.requestId && (
                <span className="inline-flex items-center gap-1">
                  {t('requestId')} <code className="font-mono text-gray-700">{result.requestId}</code>
                  <CopyButton getText={() => result.requestId} className="text-gray-400 hover:text-gray-700" />
                </span>
              )}
              {result.errorCode && (
                <Link to={`/docs/${locale}/errors#code-${result.errorCode}`} className="text-purple-700 font-medium hover:underline">
                  {t('errorDoc')} →
                </Link>
              )}
            </>
          )}
        </div>
        <pre className="m-0 max-h-[420px] overflow-auto bg-[#0d1117] text-gray-200 px-4 py-3 text-[12px] leading-relaxed font-mono whitespace-pre-wrap break-all">
          {result ? result.body || '(empty)' : <span className="text-gray-500">{t('noResponse')}</span>}
        </pre>
      </div>
    </div>
  );
};
