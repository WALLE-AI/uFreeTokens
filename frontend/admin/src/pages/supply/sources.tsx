import { describeError } from '../../api/errors';
import { Switch } from '../../components/ui/index';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { createPriceSource, updatePriceSource } from '../../api/pricing';
import { DataTable, Field, FormModal, Input, RemoteSelect, Select, Textarea, useToast, type Column } from '../../components/ui';
import { useCan } from '../../api/auth';
import { formatRelative } from '../../lib/time';
import type { PriceSource, SourceKind, SourceLevel } from '../../types';
import { providerLabel, searchProviders } from '../../api/pickers';

// 价格源：供应商详情页与 /pricing/sources 共用。

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
  const canEdit = useCan('pricing:write');

  const toggle = async (s: PriceSource, enabled: boolean) => {
    setPending(s.id);
    try {
      await updatePriceSource(s.id, { enabled });
      toast.success(`${enabled ? '已启用' : '已停用'}价格源 #${s.id}`);
      onChanged();
    } catch (err) {
      toast.error('修改价格源失败', describeError(err));
    } finally {
      setPending(null);
    }
  };

  const columns: Column<PriceSource>[] = [
    { key: 'id', header: 'ID', render: (s) => <span className="font-mono text-[11px] text-gray-400">#{s.id}</span> },
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
      key: 'level',
      header: '级别',
      render: (s) => (
        <span className="font-mono" title={LEVEL_OPTIONS.find((l) => l.value === s.level)?.label}>
          {s.level}
        </span>
      ),
    },
    { key: 'kind', header: '类型', render: (s) => <span className="font-mono text-gray-600">{s.kind}</span> },
    { key: 'fetcher', header: 'Fetcher', render: (s) => <span className="font-mono">{s.fetcher}</span> },
    {
      key: 'url',
      header: 'URL',
      render: (s) =>
        s.url ? (
          <a href={s.url} target="_blank" rel="noreferrer" className="font-mono text-[11px] text-purple-600 hover:underline truncate max-w-56 inline-block align-bottom">
            {s.url}
          </a>
        ) : (
          <span className="text-gray-400">—</span>
        ),
    },
    {
      key: 'last_success_at',
      header: '最近成功',
      render: (s) => <span className="text-gray-500">{s.last_success_at ? formatRelative(s.last_success_at) : '从未'}</span>,
    },
    { key: 'observation_count_7d', header: '7 天观测', numeric: true, render: (s) => s.observation_count_7d },
    {
      key: 'enabled',
      header: '启用',
      align: 'center',
      render: (s) => <Switch checked={s.enabled} disabled={pending === s.id} onChange={(v) => void toggle(s, v)} label={s.enabled ? '停用' : '启用'} />,
    },
  ];

  return (
    <>
      <DataTable
        columns={columns}
        rows={sources}
        rowKey={(s) => s.id}
        rowActions={canEdit ? [{ label: '编辑配置', onClick: setEditing }] : []}
        empty="还没有价格源"
      />
      <EditSourceModal
        source={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          onChanged();
        }}
      />
    </>
  );
}

// EditSourceModal 编辑价格源的 URL、调度与抓取配置（此前只能启停）。config 是明文 JSON，
// 服务端会拒绝看起来像凭据的键（api_key、token、password…）。
function EditSourceModal({ source, onClose, onSaved }: { source: PriceSource | null; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [url, setUrl] = useState('');
  const [schedule, setSchedule] = useState('');
  const [config, setConfig] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!source) return;
    setUrl(source.url ?? '');
    setSchedule(source.schedule ?? '');
    setConfig(JSON.stringify(source.config ?? {}, null, 2));
    setError(null);
  }, [source]);

  const submit = async () => {
    if (!source) return;
    let parsed: Record<string, unknown>;
    try {
      const v = JSON.parse(config || '{}');
      if (v === null || typeof v !== 'object' || Array.isArray(v)) throw new Error('config 必须是 JSON 对象');
      parsed = v as Record<string, unknown>;
    } catch (e) {
      setError(e instanceof Error ? `config 不是合法的 JSON：${e.message}` : 'config 不是合法的 JSON');
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await updatePriceSource(source.id, { url: url.trim(), schedule: schedule.trim(), config: parsed });
      toast.success(`已保存价格源 #${source.id}`);
      onSaved();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal open={!!source} onClose={onClose} title={`编辑价格源 #${source?.id ?? ''}`} onSubmit={submit} submitting={submitting} error={error} width="lg">
      <Field label="URL" htmlFor="src-url" hint="留空清除；必须是 http(s) 地址，不能带账号密码">
        <Input id="src-url" mono value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com/pricing" />
      </Field>
      <Field label="调度" htmlFor="src-schedule" hint="5 段 cron（如 0 */6 * * *），或 @hourly / @daily / @every 30m">
        <Input id="src-schedule" mono value={schedule} onChange={(e) => setSchedule(e.target.value)} placeholder="@every 6h" />
      </Field>
      <Field label="抓取配置（JSON）" htmlFor="src-config" hint="明文存储，不要放 API Key、Token、密码等凭据">
        <Textarea id="src-config" rows={10} className="font-mono text-[11px]" value={config} onChange={(e) => setConfig(e.target.value)} />
      </Field>
    </FormModal>
  );
}

export function CreateSourceModal({
  open,
  onClose,
  providerId,
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  providerId?: number; // 固定供应商（详情页）；不传则可选择
  onSaved: () => void;
}) {
  const toast = useToast();
  const [provider, setProvider] = useState('');
  const [level, setLevel] = useState<SourceLevel>('L1');
  const [kind, setKind] = useState<SourceKind>('api');
  const [fetcher, setFetcher] = useState('');
  const [url, setUrl] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setProvider(providerId ? String(providerId) : '');
    setLevel('L1');
    setKind('api');
    setFetcher('');
    setUrl('');
    setError(null);
  }, [open, providerId]);

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const res = await createPriceSource({
        provider_id: provider ? Number(provider) : undefined,
        level,
        kind,
        fetcher: fetcher.trim(),
        url: url.trim() || undefined,
      });
      toast.success(`已创建价格源 #${res.id}`);
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
      title="新增价格源"
      description="价格源记录「从哪里、以什么可信度」获取上游价格；可信度级别决定调价是否可以自动生效"
      onSubmit={submit}
      submitting={submitting}
      submitDisabled={!fetcher.trim()}
      submitLabel="创建"
      error={error}
      width="lg"
    >
      {!providerId && (
        <Field label="供应商" hint="留空表示通用来源（如 OpenRouter / LiteLLM 数据集）">
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
      <Field label="Fetcher" required hint="抓取器标识，例如 deepseek_api / openrouter / manual">
        <Input mono value={fetcher} onChange={(e) => setFetcher(e.target.value)} autoFocus />
      </Field>
      <Field label="URL" hint="可选">
        <Input mono value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" />
      </Field>
    </FormModal>
  );
}
