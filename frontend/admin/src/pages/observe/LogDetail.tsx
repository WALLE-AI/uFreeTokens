import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { getRequestLog } from '../../api/stats';
import { DataState, Money, SectionTitle, StatusBadge } from '../../components/ui';
import { formatMs } from '../../components/stats/metrics';
import { useAsync } from '../../hooks/useAsync';
import { cn } from '../../lib/cn';
import { formatInt } from '../../lib/money';
import { formatDateTime } from '../../lib/time';
import { AgentActionButton } from '../agent/components/AgentEmbeds';
import type { AttemptTraceEntry, RequestLogDetail } from '../../types';

// 调用日志详情（UI_DESIGN.md §5.6 抽屉）：计费明细、重试轨迹、性能、客户端信息与跨链接。

const ATTEMPT_STATUS_LABELS: Record<string, string> = {
  success: '成功',
  rate_limited: '被限流',
  key_exhausted: '密钥额度耗尽',
  key_invalid: '密钥无效',
  upstream_unavailable: '上游不可用',
  bad_request: '请求参数错误',
  content_filtered: '内容被过滤',
  connection_error: '连接失败',
};

export function LogDetail({ requestId, createdAt }: { requestId: string; createdAt?: string }) {
  const log = useAsync((signal) => getRequestLog(requestId, createdAt, signal), [requestId, createdAt]);
  return (
    <DataState loading={log.loading} error={log.error} onRetry={log.reload} skeleton="text">
      {log.data && <DetailBody d={log.data} />}
    </DataState>
  );
}

function DetailBody({ d }: { d: RequestLogDetail }) {
  const profit = d.charged_amount_micro !== null && d.cost_micro !== null ? d.charged_amount_micro - d.cost_micro : null;
  const discount = d.list_amount_micro !== null && d.charged_amount_micro !== null ? d.list_amount_micro - d.charged_amount_micro : null;
  const trace = parseTrace(d.attempt_trace);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge kind="request" value={d.status} />
        {d.http_status !== null && <span className="font-mono text-gray-500">HTTP {d.http_status}</span>}
        {d.error_code && <span className="font-mono text-rose-700">{d.error_code}</span>}
        {d.usage_source !== 'upstream' && <StatusBadge kind="usage_source" value={d.usage_source} />}
        <span className="ml-auto text-[11px] text-gray-400">{formatDateTime(d.created_at)}</span>
        {d.status !== 'success' && (
          <AgentActionButton
            size="sm"
            label="✦ 解释这次失败"
            message={`请解释这次调用为什么失败，并给出排查建议：日志 ${d.request_id}（先用 get_request_log 读取详情）。`}
          />
        )}
      </div>

      <section>
        <SectionTitle>关联对象</SectionTitle>
        <KV
          rows={[
            ['账户', <Link key="a" to={`/accounts/${d.account_id}`} className="text-purple-600 hover:text-purple-700">{d.account_name ?? `#${d.account_id}`}</Link>],
            ['API Key', <span key="k">{d.api_key_name ?? '—'} <span className="font-mono text-gray-400">#{d.api_key_id}</span></span>],
            ['虚拟模型', <Link key="m" to={`/models?q=${encodeURIComponent(d.virtual_model)}`} className="font-mono text-purple-600 hover:text-purple-700">{d.virtual_model}</Link>],
            [
              '渠道',
              d.channel_id !== null ? (
                <Link key="c" to={`/channels/${d.channel_id}`} className="text-purple-600 hover:text-purple-700">
                  {d.channel_label ?? `#${d.channel_id}`}
                </Link>
              ) : (
                '—'
              ),
            ],
            ['供应商', d.provider_code ?? '—'],
            ['接口', <span key="e" className="font-mono">{d.endpoint}{d.is_stream ? '（流式）' : ''}</span>],
            ...(d.experiment_key ? ([['实验分组', `${d.experiment_key} / ${d.variant_label ?? '—'}`]] as Array<[string, ReactNode]>) : []),
          ]}
        />
      </section>

      <section>
        <SectionTitle>计费明细</SectionTitle>
        <KV
          rows={[
            ['原价', <Money key="l" micro={d.list_amount_micro} />],
            [
              '促销让利',
              discount && discount > 0 ? (
                <span key="p">
                  <Money micro={-discount} signed />
                  {d.promotion_ids && d.promotion_ids.length > 0 && <span className="text-gray-400 ml-1">（促销 #{d.promotion_ids.join('、#')}）</span>}
                </span>
              ) : (
                '—'
              ),
            ],
            ['实收', <Money key="c" micro={d.charged_amount_micro} className="font-medium text-gray-900" />],
            ['成本', <Money key="co" micro={d.cost_micro} />],
            [
              '上游原币成本',
              d.upstream_cost !== null ? (
                <span key="u" className="font-mono">
                  {d.upstream_cost}
                  {d.fx_rate !== null && <span className="text-gray-400"> × 汇率 {d.fx_rate}</span>}
                </span>
              ) : (
                '—'
              ),
            ],
            ['毛利', <Money key="g" micro={profit} signed />],
            ['价格版本', <span key="b" className="font-mono text-gray-500">售价 #{d.sell_price_book_id ?? '—'} · 成本价 #{d.cost_price_book_id ?? '—'}</span>],
          ]}
        />
        {d.usage_source !== 'upstream' && (
          <p className="mt-2 text-[11px] text-amber-700">用量为估算值：客户端在上游返回用量前断开，按已转发内容估算计费。</p>
        )}
      </section>

      <section>
        <SectionTitle>重试轨迹（{trace.length} 次尝试）</SectionTitle>
        {trace.length === 0 ? (
          <div className="text-gray-400">无尝试记录</div>
        ) : (
          <ol className="relative border-l border-gray-200 ml-2 space-y-3">
            {trace.map((a, i) => {
              const ok = a.status === 'success';
              return (
                <li key={i} className="pl-5 relative">
                  <span className={cn('absolute -left-[5px] top-1 w-2.5 h-2.5 rounded-full', ok ? 'bg-emerald-500' : 'bg-rose-500')} />
                  <div className="flex items-center gap-3">
                    <span className="text-gray-400 font-mono w-6">#{i + 1}</span>
                    <Link to={`/channels/${a.channel_id}`} className="font-mono text-purple-600 hover:text-purple-700">
                      渠道 #{a.channel_id}
                    </Link>
                    <span className="font-mono text-gray-400">密钥 #{a.key_id}</span>
                    {a.codec && <span className="font-mono text-purple-600" title="供应商方言 codec">{a.codec}</span>}
                    <span className={cn('font-medium', ok ? 'text-emerald-700' : 'text-rose-700')}>{ATTEMPT_STATUS_LABELS[a.status] ?? a.status}</span>
                    <span className="ml-auto font-mono text-gray-500">{formatMs(a.latency_ms)}</span>
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </section>

      <section>
        <SectionTitle>性能与 Tokens</SectionTitle>
        <KV
          rows={[
            ['首字延迟 TTFT', <span key="t" className="font-mono">{formatMs(d.ttft_ms)}</span>],
            ['总延迟', <span key="l" className="font-mono">{formatMs(d.latency_ms)}</span>],
            ['输入 tokens', <Num key="i" v={d.input_tokens} />],
            ['缓存读 / 写', <span key="c" className="font-mono"><Num v={d.cache_read_tokens} /> / <Num v={d.cache_write_tokens} /></span>],
            ['输出 tokens', <Num key="o" v={d.output_tokens} />],
            ['推理 tokens', <Num key="r" v={d.reasoning_tokens} />],
            ['图片张数', <Num key="img" v={d.image_count} />],
            ['合成字符', <Num key="ch" v={d.input_chars} />],
            ['音频时长 (ms)', <Num key="au" v={d.audio_ms} />],
          ]}
        />
      </section>

      <section>
        <SectionTitle>客户端</SectionTitle>
        <KV
          rows={[
            ['IP', <span key="ip" className="font-mono">{d.client_ip ?? '—'}</span>],
            ['User-Agent', <span key="ua" className="font-mono break-all">{d.user_agent ?? '—'}</span>],
            ['request_id', <span key="id" className="font-mono break-all">{d.request_id}</span>],
          ]}
        />
      </section>
    </div>
  );
}

function parseTrace(v: unknown): AttemptTraceEntry[] {
  return Array.isArray(v) ? (v as AttemptTraceEntry[]) : [];
}

function Num({ v }: { v: number | null }) {
  return <span className="font-mono">{v === null ? '—' : formatInt(v)}</span>;
}

function KV({ rows }: { rows: Array<[string, ReactNode]> }) {
  return (
    <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-gray-400">{k}</dt>
          <dd className="text-gray-700 min-w-0">{v}</dd>
        </div>
      ))}
    </dl>
  );
}
