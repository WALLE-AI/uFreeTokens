import { describeError } from '../../api/errors';
import { Switch } from '../../components/ui/index';
import { useEffect, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { ChevronDown, ChevronRight, History, Play } from 'lucide-react';
import { createPriceSource, listDataSourceRuns, runPriceSourceNow, updatePriceSource, type UpdatePriceSourceBody } from '../../api/pricing';
import {
  Button,
  DataState,
  DataTable,
  DetailDrawer,
  Field,
  FormModal,
  InfoGrid,
  Input,
  RadioCards,
  RemoteSelect,
  Select,
  StatusBadge,
  Textarea,
  useToast,
  type Column,
  type RowAction,
} from '../../components/ui';
import { useCan } from '../../api/auth';
import { useAsync } from '../../hooks/useAsync';
import { useEnums } from '../../hooks/useEnums';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatDateTime, formatFromNow, formatRelative } from '../../lib/time';
import type { DataSourceRun, PriceSource, SourceDomain, SourceKind, SourceLevel } from '../../types';
import { providerLabel, searchProviders } from '../../api/pickers';

// 数据源（价格 / 优惠情报 / 评测榜单）：供应商详情页与 /pricing/sources 共用。
// 表名仍是 price_sources，按 domain 区分领域；调度、运行历史、失败告警三个领域共用一套。

export const LEVEL_OPTIONS: Array<{ value: SourceLevel; label: string }> = [
  { value: 'L1', label: 'L1 官方 API' },
  { value: 'L2', label: 'L2 官方页面' },
  { value: 'L3', label: 'L3 第三方聚合（需二次确认）' },
  { value: 'L4', label: 'L4 公开数据集' },
  { value: 'L5', label: 'L5 人工录入' },
];

export const KIND_OPTIONS: Array<{ value: SourceKind; label: string }> = [
  { value: 'api', label: 'api' },
  { value: 'html', label: 'html' },
  { value: 'dataset', label: 'dataset' },
  { value: 'billing', label: 'billing' },
  { value: 'manual', label: 'manual' },
];

export const DOMAIN_LABELS: Record<SourceDomain, string> = {
  price: '价格',
  offer: '优惠',
  benchmark: '评测榜单',
};

export const DOMAIN_OPTIONS: Array<{ value: SourceDomain; label: string; hint: string }> = [
  { value: 'price', label: '价格', hint: '上游价格观测，变化进调价审批' },
  { value: 'offer', label: '优惠', hint: '免费模型、折扣、限时活动，进优惠雷达' },
  { value: 'benchmark', label: '评测榜单', hint: '外部公开榜单成绩，导入为基准测试 run' },
];

// 各领域可用的抓取器及默认配置（与后端 GET /meta/enums 的 fetchers 对应）
interface FetcherDef {
  value: string;
  domain: SourceDomain;
  label: string;
  level: SourceLevel;
  kind: SourceKind;
  url?: string;
  config?: Record<string, unknown>;
}

export const FETCHERS: FetcherDef[] = [
  { value: 'openrouter_models', domain: 'price', label: 'OpenRouter 模型列表（含 :free 免费模型）', level: 'L4', kind: 'api', url: 'https://openrouter.ai/api/v1/models' },
  { value: 'modelsdev', domain: 'price', label: 'models.dev 公开数据集', level: 'L4', kind: 'dataset', url: 'https://models.dev/api.json' },
  { value: 'litellm_dataset', domain: 'price', label: 'LiteLLM 价格数据集', level: 'L4', kind: 'dataset' },
  { value: 'html_table', domain: 'price', label: '官方定价页 HTML 表格', level: 'L2', kind: 'html' },
  {
    value: 'offer_page',
    domain: 'offer',
    label: '定价页 / 公告页优惠文案（LLM 抽取，均需人工确认）',
    level: 'L2',
    kind: 'html',
    config: { pages: [{ url: 'https://example.com/pricing', provider_code: 'deepseek' }] },
  },
  {
    value: 'tabular',
    domain: 'benchmark',
    label: '表格数据（parquet / csv / json / yaml / zip_csv）',
    level: 'L4',
    kind: 'dataset',
    config: {
      format: 'csv',
      alias_namespace: 'lmarena',
      boards: [
        {
          key: 'lmarena:text:overall',
          label: 'model_name',
          score: 'rating',
          score_key: 'arena_text',
          benchmark: { slug: 'lmarena-text', name: 'LMArena Text', category: 'general', metric_name: 'Elo', metric_unit: 'elo' },
        },
      ],
    },
  },
  { value: 'manual', domain: 'price', label: '人工录入', level: 'L5', kind: 'manual' },
];

export function fetcherDef(name: string): FetcherDef | undefined {
  return FETCHERS.find((f) => f.value === name);
}

export function DomainTag({ domain }: { domain: string }) {
  const cls =
    domain === 'offer'
      ? 'bg-amber-50 text-amber-700 border-amber-200'
      : domain === 'benchmark'
        ? 'bg-blue-50 text-blue-700 border-blue-200'
        : 'bg-gray-50 text-gray-600 border-gray-200';
  return <span className={cn('inline-flex px-1.5 py-0.5 rounded border text-[10px] font-medium whitespace-nowrap', cls)}>{DOMAIN_LABELS[domain as SourceDomain] ?? domain}</span>;
}

function Tag({ children, tone = 'gray', title }: { children: ReactNode; tone?: 'gray' | 'amber' | 'green' | 'purple'; title?: string }) {
  const cls = {
    gray: 'bg-gray-100 text-gray-600',
    amber: 'bg-amber-50 text-amber-700',
    green: 'bg-emerald-50 text-emerald-700',
    purple: 'bg-purple-50 text-purple-700',
  }[tone];
  return (
    <span title={title} className={cn('inline-flex px-1.5 py-0.5 rounded text-[10px] whitespace-nowrap', cls)}>
      {children}
    </span>
  );
}

export function isFailing(s: Pick<PriceSource, 'consecutive_failures'>): boolean {
  return s.consecutive_failures > 0;
}

// LicenseTags：许可证 + 是否允许对外展示（public_display=false 的来源只能在后台看）
export function LicenseTags({ s }: { s: Pick<PriceSource, 'license' | 'public_display' | 'auto_publish' | 'domain' | 'attribution'> }) {
  return (
    <div className="flex flex-wrap items-center gap-1">
      {s.license ? (
        <Tag title={s.attribution ?? undefined}>{s.license}</Tag>
      ) : (
        <span className="text-[10px] text-gray-400">未登记许可</span>
      )}
      {s.public_display ? <Tag tone="green">可公开展示</Tag> : <Tag tone="amber">仅后台可见</Tag>}
      {s.domain === 'benchmark' && s.auto_publish && <Tag tone="purple">自动发布</Tag>}
    </div>
  );
}

export function PriceSourcesTable({
  sources,
  showProvider,
  onChanged,
}: {
  sources: PriceSource[];
  showProvider?: boolean;
  onChanged: () => void;
}) {
  const toast = useToast();
  const [pending, setPending] = useState<number | null>(null);
  const [editing, setEditing] = useState<PriceSource | null>(null);
  // 抽屉按 id 取最新的行，列表刷新（立即运行、启停）后抽屉里的信息同步更新
  const [viewingId, setViewingId] = useState<number | null>(null);
  const viewing = sources.find((s) => s.id === viewingId) ?? null;
  const setViewing = (s: PriceSource | null) => setViewingId(s?.id ?? null);
  const canEdit = useCan('pricing:write');

  const toggle = async (s: PriceSource, enabled: boolean) => {
    setPending(s.id);
    try {
      await updatePriceSource(s.id, { enabled });
      toast.success(`${enabled ? '已启用' : '已停用'}数据源「${s.name || s.fetcher}」`);
      onChanged();
      refreshTodoCounts();
    } catch (err) {
      toast.error('修改数据源失败', describeError(err));
    } finally {
      setPending(null);
    }
  };

  const runNow = async (s: PriceSource) => {
    setPending(s.id);
    try {
      await runPriceSourceNow(s.id);
      toast.success(`已排队运行「${s.name || s.fetcher}」，worker 约 1 分钟内拾取，结果见运行历史`);
      onChanged();
    } catch (err) {
      toast.error('立即运行失败', describeError(err));
    } finally {
      setPending(null);
    }
  };

  const columns: Column<PriceSource>[] = [
    {
      key: 'name',
      header: '数据源',
      render: (s) => (
        <div className="min-w-0 max-w-64">
          <div className="flex items-center gap-1.5">
            <span className={cn('truncate', isFailing(s) ? 'text-rose-700 font-medium' : 'text-gray-900')} title={s.name}>
              {s.name || s.fetcher}
            </span>
            <span className="font-mono text-[10px] text-gray-400 shrink-0">#{s.id}</span>
          </div>
          {s.url && (
            <a
              href={s.url}
              target="_blank"
              rel="noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="font-mono text-[10px] text-purple-600 hover:underline truncate block"
              title={s.url}
            >
              {s.url}
            </a>
          )}
        </div>
      ),
    },
    { key: 'domain', header: '领域', render: (s) => <DomainTag domain={s.domain} /> },
    ...(showProvider
      ? [
          {
            key: 'provider',
            header: '供应商',
            render: (s: PriceSource) =>
              s.provider_id ? (
                <Link to={`/providers/${s.provider_id}`} className="font-mono text-purple-600 hover:underline" onClick={(e) => e.stopPropagation()}>
                  {s.provider_code ?? `#${s.provider_id}`}
                </Link>
              ) : (
                <span className="text-gray-400">通用</span>
              ),
          } satisfies Column<PriceSource>,
        ]
      : []),
    {
      key: 'fetcher',
      header: '抓取器',
      render: (s) => (
        <div>
          <div className="font-mono">{s.fetcher}</div>
          <div className="text-[10px] text-gray-400 font-mono" title={LEVEL_OPTIONS.find((l) => l.value === s.level)?.label}>
            {s.level} · {s.kind}
          </div>
        </div>
      ),
    },
    {
      key: 'schedule',
      header: '调度',
      render: (s) =>
        s.schedule ? (
          <div>
            <div className="font-mono text-gray-700">{s.schedule}</div>
            <div className="text-[10px] text-gray-400" title={formatDateTime(s.next_run_at)}>
              {!s.enabled ? '已停用' : s.next_run_at ? `下次 ${formatFromNow(s.next_run_at)}` : '—'}
            </div>
          </div>
        ) : (
          <span className="text-gray-400 text-[11px]">不自动调度</span>
        ),
    },
    {
      key: 'last_success_at',
      header: '最近成功',
      render: (s) => (
        <div>
          <div className="text-gray-500" title={formatDateTime(s.last_success_at)}>
            {s.last_success_at ? formatRelative(s.last_success_at) : '从未'}
          </div>
          {s.last_run_at && s.last_run_at !== s.last_success_at && (
            <div className="text-[10px] text-gray-400" title={formatDateTime(s.last_run_at)}>
              最近运行 {formatRelative(s.last_run_at)}
            </div>
          )}
        </div>
      ),
    },
    {
      key: 'health',
      header: '健康',
      render: (s) =>
        isFailing(s) ? (
          <div className="max-w-56">
            <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-rose-50 text-rose-700 border border-rose-200 text-[10px] font-medium whitespace-nowrap">
              连续失败 {s.consecutive_failures} 次
            </span>
            {s.last_error && (
              <div className="text-[10px] text-rose-600 truncate mt-0.5" title={s.last_error}>
                {s.last_error}
              </div>
            )}
          </div>
        ) : s.last_success_at ? (
          <span className="text-[11px] text-emerald-700">正常</span>
        ) : (
          <span className="text-gray-400">—</span>
        ),
    },
    { key: 'license', header: '许可', render: (s) => <LicenseTags s={s} /> },
    {
      key: 'observation_count_7d',
      header: '7 天观测',
      numeric: true,
      render: (s) => (s.domain === 'price' ? s.observation_count_7d : <span className="text-gray-300">—</span>),
    },
    {
      key: 'enabled',
      header: '启用',
      align: 'center',
      render: (s) =>
        canEdit ? (
          <Switch checked={s.enabled} disabled={pending === s.id} onChange={(v) => void toggle(s, v)} ariaLabel={s.enabled ? '停用' : '启用'} />
        ) : (
          <StatusBadge kind="price_source" value={s.enabled ? 'enabled' : 'disabled'} />
        ),
    },
  ];

  const actions: RowAction<PriceSource>[] = [{ label: '运行历史', icon: <History className="w-3.5 h-3.5" />, onClick: setViewing }];
  if (canEdit) {
    actions.unshift({ label: '立即运行', icon: <Play className="w-3.5 h-3.5" />, hidden: (s) => !s.enabled || pending === s.id, onClick: (s) => void runNow(s) });
    actions.push({ label: '编辑配置', onClick: setEditing });
  }

  return (
    <>
      <DataTable columns={columns} rows={sources} rowKey={(s) => s.id} onRowClick={setViewing} rowActions={actions} empty="还没有数据源" />
      <EditSourceModal
        source={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          onChanged();
        }}
      />
      <SourceRunsDrawer
        source={viewing}
        onClose={() => setViewing(null)}
        onRunNow={
          canEdit
            ? async (s) => {
                await runNow(s);
              }
            : undefined
        }
        onEdit={
          canEdit
            ? (s) => {
                setViewing(null);
                setEditing(s);
              }
            : undefined
        }
      />
    </>
  );
}

// SourceRunsDrawer：数据源概要 + 最近 50 次运行记录（状态、条数、错误，detail JSON 可展开）。
function SourceRunsDrawer({
  source,
  onClose,
  onRunNow,
  onEdit,
}: {
  source: PriceSource | null;
  onClose: () => void;
  onRunNow?: (s: PriceSource) => Promise<void>;
  onEdit?: (s: PriceSource) => void;
}) {
  const id = source?.id;
  const runs = useAsync((signal) => (id ? listDataSourceRuns(id, 50, signal) : Promise.resolve(null)), [id]);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [running, setRunning] = useState(false);
  useEffect(() => setExpanded(new Set()), [id]);

  if (!source) return null;
  const s = source;
  const list = runs.data?.data ?? [];

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={
        <span className="flex items-center gap-2">
          {s.name || s.fetcher} <DomainTag domain={s.domain} />
        </span>
      }
      subtitle={`数据源 #${s.id} · ${s.fetcher}`}
      footer={
        <>
          <Button size="sm" variant="ghost" loading={runs.refreshing} onClick={runs.reload}>
            刷新
          </Button>
          {onEdit && (
            <Button size="sm" onClick={() => onEdit(s)}>
              编辑配置
            </Button>
          )}
          {onRunNow && (
            <Button
              size="sm"
              variant="primary"
              icon={<Play className="w-3.5 h-3.5" />}
              disabled={!s.enabled}
              title={s.enabled ? undefined : '已停用的数据源不能立即运行'}
              loading={running}
              onClick={async () => {
                setRunning(true);
                try {
                  await onRunNow(s);
                  runs.reload();
                } finally {
                  setRunning(false);
                }
              }}
            >
              立即运行
            </Button>
          )}
        </>
      }
    >
      {isFailing(s) && (
        <div className="bg-rose-50 border border-rose-200 rounded-lg p-3 mb-4 text-rose-800">
          <div className="font-medium">连续失败 {s.consecutive_failures} 次</div>
          {s.last_error && <div className="mt-1 text-[11px] font-mono break-all whitespace-pre-wrap">{s.last_error}</div>}
          <div className="mt-1 text-[11px] text-rose-600">失败后按调度周期指数退避（最长 24 小时）；页面改版导致解析失败时请更新抓取配置。</div>
        </div>
      )}
      <div className="mb-5">
        <InfoGrid
          items={[
            { label: '调度', value: s.schedule ? <span className="font-mono">{s.schedule}</span> : <span className="text-gray-400">不自动调度</span> },
            { label: '下次运行', value: s.enabled ? (s.next_run_at ? `${formatDateTime(s.next_run_at)}（${formatFromNow(s.next_run_at)}）` : '—') : '已停用' },
            { label: '最近成功', value: s.last_success_at ? formatDateTime(s.last_success_at) : '从未' },
            { label: '可信度', value: <span className="font-mono">{s.level} · {s.kind}</span> },
            { label: '许可', value: <LicenseTags s={s} /> },
            { label: '署名', value: s.attribution || <span className="text-gray-400">—</span> },
            {
              label: 'URL',
              value: s.url ? (
                <a href={s.url} target="_blank" rel="noreferrer" className="font-mono text-purple-600 hover:underline break-all">
                  {s.url}
                </a>
              ) : (
                <span className="text-gray-400">—</span>
              ),
            },
          ]}
        />
      </div>

      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-2">运行历史（最近 50 次）</div>
      <DataState loading={runs.loading} error={runs.error} onRetry={runs.reload} empty={list.length === 0} emptyTitle="还没有运行记录" emptyDescription="启用并配置调度后，worker 每分钟检查一次到期的数据源">
        <ul className="divide-y divide-gray-100 border border-gray-200 rounded-lg">
          {list.map((r) => (
            <RunItem
              key={r.id}
              r={r}
              open={expanded.has(r.id)}
              onToggle={() =>
                setExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(r.id)) next.delete(r.id);
                  else next.add(r.id);
                  return next;
                })
              }
            />
          ))}
        </ul>
      </DataState>
    </DetailDrawer>
  );
}

function runDuration(r: DataSourceRun): string {
  if (!r.finished_at) return '';
  const ms = new Date(r.finished_at).getTime() - new Date(r.started_at).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  return ms < 1000 ? `${ms} ms` : ms < 60_000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms / 60_000)} 分钟`;
}

function RunItem({ r, open, onToggle }: { r: DataSourceRun; open: boolean; onToggle: () => void }) {
  const hasDetail = r.detail !== null && r.detail !== undefined && !(typeof r.detail === 'object' && Object.keys(r.detail as object).length === 0);
  const dur = runDuration(r);
  return (
    <li className="px-3 py-2">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onToggle}
          disabled={!hasDetail}
          className="text-gray-400 hover:text-gray-700 disabled:opacity-30 cursor-pointer disabled:cursor-default"
          aria-label={open ? '收起详情' : '展开详情'}
        >
          {open ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
        </button>
        <StatusBadge kind="data_source_run" value={r.status} />
        <span className="font-mono text-gray-700" title={formatDateTime(r.started_at)}>
          {formatDateTime(r.started_at).slice(5, 16)}
        </span>
        {dur && <span className="text-[10px] text-gray-400">耗时 {dur}</span>}
        <span className="ml-auto text-[11px] text-gray-500 font-mono">
          {r.items_fetched ?? '—'} 条 / 变化 {r.items_changed ?? '—'}
        </span>
      </div>
      {r.error && <div className="mt-1 pl-6 text-[11px] text-rose-600 font-mono break-all whitespace-pre-wrap">{r.error}</div>}
      {open && hasDetail && (
        <pre className="mt-2 ml-6 bg-gray-50 border border-gray-200 rounded-md p-2 text-[10px] font-mono text-gray-700 overflow-x-auto max-h-72">
          {JSON.stringify(r.detail, null, 2)}
        </pre>
      )}
    </li>
  );
}

// 解析抓取配置 JSON；返回错误文案或对象
function parseConfig(text: string): { value?: Record<string, unknown>; error?: string } {
  try {
    const v = JSON.parse(text.trim() || '{}');
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return { error: 'config 必须是 JSON 对象' };
    return { value: v as Record<string, unknown> };
  } catch (e) {
    return { error: e instanceof Error ? `config 不是合法的 JSON：${e.message}` : 'config 不是合法的 JSON' };
  }
}

const SCHEDULE_HINT = '5 段 cron（如 0 */6 * * *），或 @hourly / @daily / @every 6h；留空表示不自动调度';
const CONFIG_HINT = '明文存储，不要放 API Key、Token、密码等凭据（需要密钥时用 auth_header_env 引用 worker 环境变量）';

// EditSourceModal 编辑数据源的名称、URL、调度、许可与抓取配置。只提交改动过的字段；
// config 是明文 JSON，服务端会拒绝看起来像凭据的键（api_key、token、password…）。
function EditSourceModal({ source, onClose, onSaved }: { source: PriceSource | null; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [schedule, setSchedule] = useState('');
  const [license, setLicense] = useState('');
  const [attribution, setAttribution] = useState('');
  const [publicDisplay, setPublicDisplay] = useState(false);
  const [autoPublish, setAutoPublish] = useState(false);
  const [config, setConfig] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!source) return;
    setName(source.name ?? '');
    setUrl(source.url ?? '');
    setSchedule(source.schedule ?? '');
    setLicense(source.license ?? '');
    setAttribution(source.attribution ?? '');
    setPublicDisplay(source.public_display);
    setAutoPublish(source.auto_publish);
    setConfig(JSON.stringify(source.config ?? {}, null, 2));
    setError(null);
  }, [source]);

  const submit = async () => {
    if (!source) return;
    const parsed = parseConfig(config);
    if (parsed.error) {
      setError(parsed.error);
      return;
    }
    const body: UpdatePriceSourceBody = {};
    if (name.trim() !== (source.name ?? '')) body.name = name.trim();
    if (url.trim() !== (source.url ?? '')) body.url = url.trim();
    if (schedule.trim() !== (source.schedule ?? '')) body.schedule = schedule.trim();
    if (license.trim() !== (source.license ?? '')) body.license = license.trim();
    if (attribution.trim() !== (source.attribution ?? '')) body.attribution = attribution.trim();
    if (publicDisplay !== source.public_display) body.public_display = publicDisplay;
    if (autoPublish !== source.auto_publish) body.auto_publish = autoPublish;
    if (JSON.stringify(parsed.value) !== JSON.stringify(source.config ?? {})) body.config = parsed.value;
    if (Object.keys(body).length === 0) {
      onClose();
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await updatePriceSource(source.id, body);
      toast.success(`已保存数据源「${name.trim() || source.fetcher}」`);
      onSaved();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={!!source}
      onClose={onClose}
      title={`编辑数据源 #${source?.id ?? ''}`}
      description={source ? `${DOMAIN_LABELS[source.domain] ?? source.domain} · ${source.fetcher}` : undefined}
      onSubmit={submit}
      submitting={submitting}
      error={error}
      width="lg"
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="名称" htmlFor="src-name">
          <Input id="src-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={source?.fetcher} />
        </Field>
        <Field label="调度" htmlFor="src-schedule" hint={SCHEDULE_HINT}>
          <Input id="src-schedule" mono value={schedule} onChange={(e) => setSchedule(e.target.value)} placeholder="@every 6h" />
        </Field>
      </div>
      <Field label="URL" htmlFor="src-url" hint="留空清除；必须是 http(s) 地址，不能带账号密码">
        <Input id="src-url" mono value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com/pricing" />
      </Field>
      <LicenseFields
        domain={source?.domain ?? 'price'}
        license={license}
        attribution={attribution}
        publicDisplay={publicDisplay}
        autoPublish={autoPublish}
        onLicense={setLicense}
        onAttribution={setAttribution}
        onPublicDisplay={setPublicDisplay}
        onAutoPublish={setAutoPublish}
      />
      <Field label="抓取配置（JSON）" htmlFor="src-config" hint={CONFIG_HINT}>
        <Textarea id="src-config" rows={10} className="font-mono text-[11px]" value={config} onChange={(e) => setConfig(e.target.value)} />
      </Field>
    </FormModal>
  );
}

function LicenseFields({
  domain,
  license,
  attribution,
  publicDisplay,
  autoPublish,
  onLicense,
  onAttribution,
  onPublicDisplay,
  onAutoPublish,
}: {
  domain: string;
  license: string;
  attribution: string;
  publicDisplay: boolean;
  autoPublish: boolean;
  onLicense: (v: string) => void;
  onAttribution: (v: string) => void;
  onPublicDisplay: (v: boolean) => void;
  onAutoPublish: (v: boolean) => void;
}) {
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <Field label="许可证" hint="例如 CC-BY-4.0 / Apache-2.0 / proprietary-internal / unknown">
          <Input mono list="src-license-suggestions" value={license} onChange={(e) => onLicense(e.target.value)} />
          <datalist id="src-license-suggestions">
            {['CC-BY-4.0', 'CC-BY-SA-4.0', 'MIT', 'Apache-2.0', 'proprietary-internal', 'unknown'].map((l) => (
              <option key={l} value={l} />
            ))}
          </datalist>
        </Field>
        <Field label="署名文案" hint="对外展示时必须附带，例如：数据来源 LMArena（CC BY 4.0）">
          <Input value={attribution} onChange={(e) => onAttribution(e.target.value)} />
        </Field>
      </div>
      <div className="flex flex-wrap gap-x-6 gap-y-2">
        <Switch
          checked={publicDisplay}
          onChange={onPublicDisplay}
          label={publicDisplay ? '许可允许在公开页展示' : '仅后台可见（许可不允许对外展示）'}
        />
        {domain === 'benchmark' && (
          <Switch checked={autoPublish} onChange={onAutoPublish} label={autoPublish ? '映射完整且无异常时自动发布导入的 run' : '导入的 run 留草稿，人工发布'} />
        )}
      </div>
    </>
  );
}

export function CreateSourceModal({
  open,
  onClose,
  providerId,
  defaultDomain = 'price',
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  providerId?: number; // 固定供应商（详情页）；不传则可选择
  defaultDomain?: SourceDomain;
  onSaved: () => void;
}) {
  const toast = useToast();
  const enums = useEnums();
  const [domain, setDomain] = useState<SourceDomain>(defaultDomain);
  const [name, setName] = useState('');
  const [provider, setProvider] = useState('');
  const [level, setLevel] = useState<SourceLevel>('L1');
  const [kind, setKind] = useState<SourceKind>('api');
  const [fetcher, setFetcher] = useState('');
  const [url, setUrl] = useState('');
  const [schedule, setSchedule] = useState('');
  const [license, setLicense] = useState('');
  const [attribution, setAttribution] = useState('');
  const [publicDisplay, setPublicDisplay] = useState(false);
  const [autoPublish, setAutoPublish] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [config, setConfig] = useState('{}');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setDomain(defaultDomain);
    setName('');
    setProvider(providerId ? String(providerId) : '');
    setLevel('L1');
    setKind('api');
    setFetcher('');
    setUrl('');
    setSchedule('');
    setLicense('');
    setAttribution('');
    setPublicDisplay(false);
    setAutoPublish(false);
    setEnabled(true);
    setConfig('{}');
    setError(null);
  }, [open, providerId, defaultDomain]);

  // 选抓取器时带出推荐的可信度、类型、URL 与配置模板（只在对应字段还是空/默认时覆盖）
  const pickFetcher = (v: string) => {
    setFetcher(v);
    const def = fetcherDef(v);
    if (!def) return;
    setLevel(def.level);
    setKind(def.kind);
    if (def.url && !url.trim()) setUrl(def.url);
    if (def.config && (config.trim() === '' || config.trim() === '{}')) setConfig(JSON.stringify(def.config, null, 2));
    if (!schedule.trim()) setSchedule(def.domain === 'benchmark' ? '@daily' : '@every 6h');
  };

  // 下拉候选：本领域的已知抓取器 + 后端字典里有、前端还不认识的抓取器
  const known = new Set(FETCHERS.map((f) => f.value));
  const fetcherOptions = [
    ...FETCHERS.filter((f) => f.domain === domain || f.value === 'manual').map((f) => ({ value: f.value, label: `${f.value} · ${f.label}` })),
    ...(enums?.fetchers ?? []).filter((f) => !known.has(f)).map((f) => ({ value: f, label: f })),
  ];

  const parsed = parseConfig(config);

  const submit = async () => {
    if (parsed.error) {
      setError(parsed.error);
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const res = await createPriceSource({
        domain,
        name: name.trim() || undefined,
        provider_id: provider ? Number(provider) : undefined,
        level,
        kind,
        fetcher: fetcher.trim(),
        url: url.trim() || undefined,
        schedule: schedule.trim() || undefined,
        config: parsed.value,
        enabled,
        license: license.trim() || undefined,
        attribution: attribution.trim() || undefined,
        public_display: publicDisplay,
        auto_publish: domain === 'benchmark' ? autoPublish : false,
      });
      toast.success(`已创建数据源 #${res.id}`);
      onSaved();
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      title="新增数据源"
      description="数据源记录「从哪里、以什么可信度、多久一次」获取外部数据；价格源的可信度级别决定调价是否可以自动生效"
      onSubmit={submit}
      submitting={submitting}
      submitDisabled={!fetcher.trim() || !!parsed.error}
      submitLabel="创建"
      error={error}
      width="lg"
    >
      <Field label="领域" required>
        <RadioCards
          cols={3}
          value={domain}
          onChange={(v) => {
            setDomain(v);
            setFetcher('');
          }}
          options={DOMAIN_OPTIONS}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="抓取器" required hint={fetcherDef(fetcher)?.label ?? '选择后自动带出推荐的级别、类型与配置模板'}>
          <Select className="w-full" value={fetcher} placeholder="请选择抓取器" options={fetcherOptions} onChange={(e) => pickFetcher(e.target.value)} />
        </Field>
        <Field label="名称" hint="留空 = 抓取器名">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 LMArena 文本榜" />
        </Field>
      </div>
      {!providerId && (
        <Field label="供应商" hint="留空表示通用来源（如 OpenRouter / models.dev / 公开榜单）">
          <RemoteSelect
            className="w-full"
            value={provider}
            placeholder="通用（不绑定供应商）"
            clearable
            load={searchProviders}
            resolve={providerLabel}
            onChange={(v) => setProvider(v)}
          />
        </Field>
      )}
      <div className="grid grid-cols-2 gap-3">
        <Field label="可信度级别" required>
          <Select className="w-full" value={level} options={LEVEL_OPTIONS} onChange={(e) => setLevel(e.target.value as SourceLevel)} />
        </Field>
        <Field label="类型" required>
          <Select className="w-full" value={kind} options={KIND_OPTIONS} onChange={(e) => setKind(e.target.value as SourceKind)} />
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="URL" hint="可选；tabular 的各榜单也可在配置里单独指定 url">
          <Input mono value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" />
        </Field>
        <Field label="调度" hint={SCHEDULE_HINT}>
          <Input mono value={schedule} onChange={(e) => setSchedule(e.target.value)} placeholder="@every 6h" />
        </Field>
      </div>
      <LicenseFields
        domain={domain}
        license={license}
        attribution={attribution}
        publicDisplay={publicDisplay}
        autoPublish={autoPublish}
        onLicense={setLicense}
        onAttribution={setAttribution}
        onPublicDisplay={setPublicDisplay}
        onAutoPublish={setAutoPublish}
      />
      <Field label="抓取配置（JSON）" hint={CONFIG_HINT} error={parsed.error}>
        <Textarea rows={8} className="font-mono text-[11px]" value={config} onChange={(e) => setConfig(e.target.value)} />
      </Field>
      <Switch checked={enabled} onChange={setEnabled} label={enabled ? '创建后立即启用' : '创建为停用状态'} />
    </FormModal>
  );
}
