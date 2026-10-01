import { useEffect, useState, type ReactNode } from 'react';
import { Link, useNavigate } from 'react-router';
import { Check, ExternalLink, EyeOff, Radar, RotateCcw, RotateCw, Sparkles, Upload } from 'lucide-react';
import { newIdempotencyKey } from '../../api/client';
import { adoptUpstreamOffer, getUpstreamOffer, listUpstreamOffers, setUpstreamOfferStatus, type AdoptOfferBody } from '../../api/pricing';
import { channelLabel, searchChannels, searchProviderCodes, searchVirtualModelNames } from '../../api/pickers';
import {
  Button,
  ConfirmDialog,
  DataState,
  DetailDrawer,
  Field,
  FilterBar,
  FormModal,
  InfoGrid,
  Input,
  MoneyInput,
  PageHeader,
  Pagination,
  Pills,
  RadioCards,
  RemoteSelect,
  Select,
  StatusBadge,
  useToast,
  type ActiveFilter,
} from '../../components/ui';
import { Can } from '../../components/ui/Can';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatDateTime, formatFromNow, formatRelative } from '../../lib/time';
import { isoToZonedInput, zonedInputToISO } from '../../lib/tz';
import type { Offer, OfferType } from '../../types';
import { PriceSyncDisabledCard, errorDetail, friendlyError, isNotConfigured } from './shared';

// 优惠雷达（外部数据采集技术方案 §3.2 / §3.3）：数据源观测到的上游免费模型、折扣、限时活动等情报。
// 情报只进情报库，不自动生效；运营确认后可"采用"为一条促销（成本面仅记录，售价面立即参与计费）。
// 免费模型（平台已接入该供应商时）会自动进"待上架模型"，卡片上直接"去上架"，不必再走采用。
// URL：?status=new|confirmed|adopted|ignored|expired|all &offer_type= &provider_code= &q= &page= &id=（打开详情）

const STATUS_TABS = [
  { value: 'new', label: '待确认' },
  { value: 'confirmed', label: '已确认' },
  { value: 'adopted', label: '已采用' },
  { value: 'ignored', label: '已忽略' },
  { value: 'expired', label: '已过期' },
  { value: 'all', label: '全部' },
];

export const OFFER_TYPE_LABELS: Record<OfferType, string> = {
  free_model: '免费模型',
  discount: '折扣',
  off_peak: '错峰优惠',
  free_quota: '免费额度',
  new_user_credit: '新用户赠金',
  price_cut: '降价',
};

const DETECTION_LABELS: Record<string, { label: string; hint: string }> = {
  structured: { label: '结构化', hint: '来自数据源的结构化字段（如 OpenRouter 价格为 0）' },
  price_diff: { label: '价格推导', hint: '价格观测相比上次大幅下降推导而来' },
  llm_extract: { label: 'LLM 抽取', hint: '从定价页 / 公告文案中由 LLM 抽取，务必核对原文' },
  manual: { label: '人工录入', hint: '' },
};

const PAGE_SIZE = 20;

const LISTING_STATUS_LABELS: Record<string, string> = { published: '已上架', dismissed: '已忽略', expired: '免费已结束', pending: '待上架' };

function trimNum(n: number, digits = 2): string {
  return String(Number(n.toFixed(digits)));
}

// 折扣文案：discount_ratio 是价格乘数。0 = 免费；降价类显示"降价至 x%"；其余显示"x 折"。
export function discountText(ratio: string | null | undefined, type?: string): string | null {
  if (ratio === null || ratio === undefined || ratio === '') return null;
  const r = Number(ratio);
  if (!Number.isFinite(r)) return null;
  if (r === 0) return '免费';
  if (r >= 1) return '原价';
  if (type === 'price_cut') return `降价至 ${trimNum(r * 100, 1)}%`;
  return `${trimNum(r * 10, 2)} 折`;
}

// compactJSON：quota / limits 之类的小对象压成一行
function compactJSON(v: unknown): string | null {
  if (v === null || v === undefined) return null;
  if (typeof v === 'object' && Object.keys(v as object).length === 0) return null;
  return typeof v === 'string' ? v : JSON.stringify(v);
}

export default function OffersPage() {
  const toast = useToast();
  const [params, setParams] = useQueryParams();
  const status = STATUS_TABS.some((t) => t.value === params.status) ? params.status : 'new';
  const page = Math.max(1, Number(params.page) || 1);
  const [tick, setTick] = useState(0);

  const list = useAsync(
    (signal) =>
      listUpstreamOffers(
        {
          status: status === 'all' ? undefined : status,
          offer_type: params.offer_type || undefined,
          provider_code: params.provider_code || undefined,
          q: params.q || undefined,
          page,
          page_size: PAGE_SIZE,
        },
        signal,
      ),
    [status, params.offer_type, params.provider_code, params.q, page, tick],
  );
  const items = list.data?.data ?? [];

  const [busyId, setBusyId] = useState<number | null>(null);
  const [adopting, setAdopting] = useState<Offer | null>(null);
  const reload = () => {
    setTick((t) => t + 1);
    refreshTodoCounts();
  };

  const changeStatus = async (o: Offer, next: 'confirmed' | 'ignored' | 'new') => {
    setBusyId(o.id);
    try {
      await setUpstreamOfferStatus(o.id, next);
      toast.success(next === 'confirmed' ? `已确认情报 #${o.id}` : next === 'ignored' ? `已忽略情报 #${o.id}` : `已重新打开情报 #${o.id}`);
      reload();
    } catch (err) {
      toast.error(friendlyError(err, '修改状态失败'), errorDetail(err));
    } finally {
      setBusyId(null);
    }
  };

  if (isNotConfigured(list.error)) {
    return (
      <>
        <PageHeader title="优惠雷达" description="上游免费模型、折扣与限时活动情报" />
        <PriceSyncDisabledCard />
      </>
    );
  }

  const active: ActiveFilter[] = [];
  if (params.q) active.push({ key: 'q', label: `搜索: ${params.q}`, onRemove: () => setParams({ q: null }) });
  if (params.offer_type)
    active.push({
      key: 'offer_type',
      label: `类型: ${OFFER_TYPE_LABELS[params.offer_type as OfferType] ?? params.offer_type}`,
      onRemove: () => setParams({ offer_type: null }),
    });
  if (params.provider_code) active.push({ key: 'provider_code', label: `供应商: ${params.provider_code}`, onRemove: () => setParams({ provider_code: null }) });

  return (
    <>
      <PageHeader
        title="优惠雷达"
        description="数据源观测到的上游免费模型、折扣、错峰价与限时活动。情报不会自动生效：核对证据后确认，再按需采用为促销。"
        actions={
          <Button icon={<RotateCw className="w-3.5 h-3.5" />} loading={list.refreshing} onClick={() => setTick((t) => t + 1)}>
            刷新
          </Button>
        }
      />

      <div className="mb-3">
        <Pills
          options={STATUS_TABS.map((t) => ({ ...t, count: t.value === status ? list.data?.total : undefined }))}
          value={status}
          onChange={(v) => setParams({ status: v === 'new' ? null : v })}
        />
      </div>

      <FilterBar
        search={params.q ?? ''}
        onSearch={(q) => setParams({ q })}
        searchPlaceholder="搜索上游模型、条件、原文…"
        controls={
          <>
            <Select
              value={params.offer_type ?? ''}
              placeholder="全部类型"
              options={Object.entries(OFFER_TYPE_LABELS).map(([value, label]) => ({ value, label }))}
              onChange={(e) => setParams({ offer_type: e.target.value || null })}
            />
            <RemoteSelect
              value={params.provider_code ?? ''}
              placeholder="全部供应商"
              clearable
              load={searchProviderCodes}
              resolve={async (code) => code}
              onChange={(v) => setParams({ provider_code: v || null })}
            />
          </>
        }
        active={active}
        onClearAll={() => setParams({ q: null, offer_type: null, provider_code: null })}
      />

      <DataState
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        empty={items.length === 0}
        skeleton="cards"
        emptyIcon={<Radar className="w-8 h-8" />}
        emptyTitle={status === 'new' && active.length === 0 ? '暂无待确认的优惠情报' : '没有符合条件的情报'}
        emptyDescription={status === 'new' ? '优惠类数据源（offer_page）与价格源（OpenRouter 免费模型、价格骤降）发现新情报时会出现在这里' : undefined}
      >
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
          {items.map((o) => (
            <OfferCard
              key={o.id}
              o={o}
              busy={busyId === o.id}
              onStatus={(s) => void changeStatus(o, s)}
              onAdopt={() => setAdopting(o)}
              onOpen={() => setParams({ id: String(o.id) }, { keepPage: true })}
            />
          ))}
        </div>
        {list.data && list.data.total > PAGE_SIZE && (
          <Pagination page={page} pageSize={PAGE_SIZE} total={list.data.total} onPageChange={(p) => setParams({ page: String(p) }, { keepPage: true })} />
        )}
      </DataState>

      <OfferDrawer
        id={params.id ? Number(params.id) : null}
        reloadKey={tick}
        onClose={() => setParams({ id: null }, { keepPage: true })}
        onStatus={(o, s) => void changeStatus(o, s)}
        onAdopt={setAdopting}
      />

      <AdoptOfferModal
        offer={adopting}
        onClose={() => setAdopting(null)}
        onAdopted={(o, promotionId, side) => {
          setAdopting(null);
          reload();
          toast.success(`已采用情报 #${o.id}，生成${side === 'sell' ? '售价面' : '成本面'}促销 #${promotionId}`);
        }}
      />
    </>
  );
}

function TypeTag({ type }: { type: string }) {
  const free = type === 'free_model' || type === 'free_quota' || type === 'new_user_credit';
  return (
    <span
      className={cn(
        'inline-flex px-1.5 py-0.5 rounded border text-[10px] font-medium whitespace-nowrap',
        free ? 'bg-emerald-50 text-emerald-700 border-emerald-200' : 'bg-amber-50 text-amber-700 border-amber-200',
      )}
    >
      {OFFER_TYPE_LABELS[type as OfferType] ?? type}
    </span>
  );
}

function DetectionTag({ detection }: { detection: string }) {
  const d = DETECTION_LABELS[detection] ?? { label: detection, hint: '' };
  return (
    <span
      title={d.hint || undefined}
      className={cn('inline-flex px-1.5 py-0.5 rounded text-[10px] whitespace-nowrap', detection === 'llm_extract' ? 'bg-purple-50 text-purple-700' : 'bg-gray-100 text-gray-600')}
    >
      {d.label}
    </span>
  );
}

// 时间窗 + 倒计时：距结束 ≤ 3 天标 amber，已结束标 gray
function TimeWindow({ o }: { o: Offer }) {
  if (!o.starts_at && !o.ends_at) return <span className="text-gray-400">长期 / 未注明时间</span>;
  const now = Date.now();
  const ends = o.ends_at ? new Date(o.ends_at).getTime() : null;
  const starts = o.starts_at ? new Date(o.starts_at).getTime() : null;
  let badge: ReactNode = null;
  if (ends !== null && ends <= now) badge = <span className="text-gray-400">已结束</span>;
  else if (starts !== null && starts > now) badge = <span className="text-blue-600">{formatFromNow(o.starts_at)}开始</span>;
  else if (ends !== null)
    badge = <span className={cn(ends - now <= 3 * 86400_000 ? 'text-amber-700 font-medium' : 'text-gray-600')}>{formatFromNow(o.ends_at)}结束</span>;
  return (
    <span className="inline-flex flex-wrap items-center gap-x-2">
      <span className="font-mono text-gray-600">
        {o.starts_at ? formatDateTime(o.starts_at).slice(0, 16) : '…'} ~ {o.ends_at ? formatDateTime(o.ends_at).slice(0, 16) : '长期'}
      </span>
      {badge}
    </span>
  );
}

function OfferActions({
  o,
  busy,
  onStatus,
  onAdopt,
}: {
  o: Offer;
  busy: boolean;
  onStatus: (s: 'confirmed' | 'ignored' | 'new') => void;
  onAdopt: () => void;
}) {
  const canAdopt = o.status === 'new' || o.status === 'confirmed';
  const navigate = useNavigate();
  const toListing = o.listing_id && o.listing_status === 'pending';
  return (
    <Can perm="pricing:write">
      {toListing && (
        <Button
          size="sm"
          variant="dark"
          icon={<Upload className="w-3.5 h-3.5" />}
          onClick={() => navigate(`/pricing/listings?origin=free_offer&id=${o.listing_id}`)}
        >
          去上架
        </Button>
      )}
      {(o.status === 'new' || o.status === 'confirmed') && (
        <Button size="sm" variant="ghost" disabled={busy} icon={<EyeOff className="w-3.5 h-3.5" />} onClick={() => onStatus('ignored')}>
          忽略
        </Button>
      )}
      {(o.status === 'ignored' || o.status === 'confirmed') && (
        <Button size="sm" variant="ghost" disabled={busy} icon={<RotateCcw className="w-3.5 h-3.5" />} onClick={() => onStatus('new')}>
          重新打开
        </Button>
      )}
      {o.status === 'new' && (
        <Button size="sm" disabled={busy} icon={<Check className="w-3.5 h-3.5" />} onClick={() => onStatus('confirmed')}>
          确认
        </Button>
      )}
      {canAdopt && (
        <Button size="sm" variant={toListing ? 'ghost' : 'dark'} disabled={busy} icon={<Sparkles className="w-3.5 h-3.5" />} onClick={onAdopt}>
          采用…
        </Button>
      )}
    </Can>
  );
}

function OfferCard({
  o,
  busy,
  onStatus,
  onAdopt,
  onOpen,
}: {
  o: Offer;
  busy: boolean;
  onStatus: (s: 'confirmed' | 'ignored' | 'new') => void;
  onAdopt: () => void;
  onOpen: () => void;
}) {
  const discount = discountText(o.discount_ratio, o.offer_type);
  const quota = compactJSON(o.quota);
  const limits = compactJSON(o.limits);
  return (
    <div className={cn('bg-white border rounded-xl p-4 shadow-xs flex flex-col', o.status === 'new' ? 'border-gray-200 hover:border-purple-200' : 'border-gray-200')}>
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-1.5 flex-wrap">
            <TypeTag type={o.offer_type} />
            <span className="font-mono text-xs font-semibold text-gray-900 break-all">{o.upstream_model ?? <span className="font-sans text-gray-500">全场 / 账号级优惠</span>}</span>
            <StatusBadge kind="offer" value={o.status} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500">
            <span className="font-mono">{o.provider_code}</span>
            <DetectionTag detection={o.detection} />
            {o.source_name && <span>来源 {o.source_name}</span>}
            <span title={formatDateTime(o.first_seen_at)}>首次发现 {formatRelative(o.first_seen_at)}</span>
            <span title={formatDateTime(o.last_seen_at)}>最近出现 {formatRelative(o.last_seen_at)}</span>
          </div>
        </div>
        <div className={cn('text-right shrink-0 font-bold', discount === '免费' ? 'text-emerald-600 text-lg' : 'text-gray-900 text-base')}>
          {discount ?? <span className="text-gray-300 text-sm font-normal">—</span>}
        </div>
      </div>

      <div className="mt-2 text-[11px]">
        <TimeWindow o={o} />
      </div>
      {(o.conditions || quota || limits) && (
        <div className="mt-1.5 space-y-0.5 text-[11px] text-gray-600">
          {o.conditions && <div>条件：{o.conditions}</div>}
          {quota && <div className="font-mono text-gray-500 truncate" title={quota}>额度：{quota}</div>}
          {limits && <div className="font-mono text-gray-500 truncate" title={limits}>限制：{limits}</div>}
        </div>
      )}
      {o.evidence_excerpt && (
        <blockquote className="mt-2 border-l-2 border-gray-200 pl-2 text-[11px] text-gray-500 line-clamp-3 whitespace-pre-wrap" title={o.evidence_excerpt}>
          {o.evidence_excerpt}
        </blockquote>
      )}

      <div className="mt-3 pt-2 border-t border-gray-100 flex flex-wrap items-center gap-2">
        {o.evidence_url && (
          <a href={o.evidence_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-[11px] text-purple-600 hover:text-purple-700">
            证据原文 <ExternalLink className="w-3 h-3" />
          </a>
        )}
        <button type="button" onClick={onOpen} className="text-[11px] text-gray-500 hover:text-gray-800 cursor-pointer">
          详情
        </button>
        {o.status === 'adopted' && o.adopted_promotion_id && <span className="text-[11px] text-emerald-700">已生成促销 #{o.adopted_promotion_id}</span>}
        {o.listing_id && o.listing_status && o.listing_status !== 'pending' && (
          <Link to={`/pricing/listings?status=${o.listing_status}`} className="text-[11px] text-emerald-700 hover:underline">
            待上架候选 #{o.listing_id} · {LISTING_STATUS_LABELS[o.listing_status]}
          </Link>
        )}
        {o.decided_by_name && o.status !== 'new' && (
          <span className="text-[11px] text-gray-400" title={formatDateTime(o.decided_at)}>
            {o.decided_by_name} · {formatRelative(o.decided_at)}
          </span>
        )}
        <div className="flex-1" />
        <OfferActions o={o} busy={busy} onStatus={onStatus} onAdopt={onAdopt} />
      </div>
    </div>
  );
}

function OfferDrawer({
  id,
  reloadKey,
  onClose,
  onStatus,
  onAdopt,
}: {
  id: number | null;
  reloadKey: number;
  onClose: () => void;
  onStatus: (o: Offer, s: 'confirmed' | 'ignored' | 'new') => void;
  onAdopt: (o: Offer) => void;
}) {
  const res = useAsync((signal) => (id ? getUpstreamOffer(id, signal) : Promise.resolve(null)), [id, reloadKey]);
  if (!id) return null;
  const o = res.data;
  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`优惠情报 #${id}`}
      subtitle={o ? `${o.provider_code} · ${o.upstream_model ?? '全场 / 账号级'}` : undefined}
      footer={o ? <OfferActions o={o} busy={false} onStatus={(s) => onStatus(o, s)} onAdopt={() => onAdopt(o)} /> : undefined}
    >
      <DataState loading={res.loading} error={res.error} onRetry={res.reload}>
        {o && (
          <div className="space-y-4">
            <InfoGrid
              items={[
                { label: '状态', value: <StatusBadge kind="offer" value={o.status} /> },
                { label: '类型', value: <TypeTag type={o.offer_type} /> },
                { label: '折扣', value: discountText(o.discount_ratio, o.offer_type) ?? '—' },
                { label: '价格乘数', value: <span className="font-mono">{o.discount_ratio ?? '—'}</span> },
                { label: '识别方式', value: <DetectionTag detection={o.detection} /> },
                { label: '数据源', value: o.source_name ?? (o.source_id ? `#${o.source_id}` : '—') },
                { label: '时间窗', value: <TimeWindow o={o} /> },
                { label: '首次发现', value: formatDateTime(o.first_seen_at) },
                { label: '最近出现', value: formatDateTime(o.last_seen_at) },
                { label: '处理人', value: o.decided_by_name ? `${o.decided_by_name} · ${formatDateTime(o.decided_at)}` : '—' },
                { label: '已生成促销', value: o.adopted_promotion_id ? <span className="font-mono">#{o.adopted_promotion_id}</span> : '—' },
              ]}
            />
            {o.conditions && (
              <div>
                <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1">条件</div>
                <p className="text-gray-700 whitespace-pre-wrap">{o.conditions}</p>
              </div>
            )}
            {[
              ['额度 quota', o.quota],
              ['限制 limits', o.limits],
            ].map(([label, v]) =>
              compactJSON(v) ? (
                <div key={label as string}>
                  <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1">{label as string}</div>
                  <pre className="bg-gray-50 border border-gray-200 rounded-md p-2 text-[10px] font-mono text-gray-700 overflow-x-auto">{JSON.stringify(v, null, 2)}</pre>
                </div>
              ) : null,
            )}
            <div>
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1">证据</div>
              {o.evidence_excerpt ? (
                <blockquote className="border-l-2 border-gray-200 pl-2 text-gray-600 whitespace-pre-wrap">{o.evidence_excerpt}</blockquote>
              ) : (
                <p className="text-gray-400">无原文摘录</p>
              )}
              {o.evidence_url && (
                <a href={o.evidence_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 mt-1 text-purple-600 hover:text-purple-700 break-all">
                  {o.evidence_url} <ExternalLink className="w-3 h-3 shrink-0" />
                </a>
              )}
            </div>
          </div>
        )}
      </DataState>
    </DetailDrawer>
  );
}

// ---------- 采用 ----------

type Side = 'cost' | 'sell';

function AdoptOfferModal({
  offer,
  onClose,
  onAdopted,
}: {
  offer: Offer | null;
  onClose: () => void;
  onAdopted: (o: Offer, promotionId: number, side: Side) => void;
}) {
  const [side, setSide] = useState<Side>('cost');
  const [channelId, setChannelId] = useState('');
  const [virtualModel, setVirtualModel] = useState('');
  const [name, setName] = useState('');
  const [ratio, setRatio] = useState('');
  const [startsAt, setStartsAt] = useState('');
  const [endsAt, setEndsAt] = useState('');
  const [priority, setPriority] = useState('0');
  const [budget, setBudget] = useState<number | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [idemKey, setIdemKey] = useState('');

  useEffect(() => {
    if (!offer) return;
    setSide('cost');
    setChannelId('');
    setVirtualModel('');
    setName('');
    setRatio(offer.discount_ratio ?? '');
    setStartsAt(isoToZonedInput(offer.starts_at));
    setEndsAt(isoToZonedInput(offer.ends_at));
    setPriority('0');
    setBudget(null);
    setConfirming(false);
    setError(null);
    setIdemKey(newIdempotencyKey());
  }, [offer]);

  const ratioNum = ratio.trim() === '' ? null : Number(ratio);
  const ratioErr =
    ratio.trim() === ''
      ? '情报里没有折扣，需要填写'
      : ratioNum === null || !Number.isFinite(ratioNum) || ratioNum < 0 || ratioNum > 1
        ? '需为 0 到 1 之间的价格乘数（0 = 免费，0.5 = 五折）'
        : side === 'sell' && ratioNum >= 1
          ? '售价面促销必须有折扣（< 1）'
          : undefined;
  const startsISO = zonedInputToISO(startsAt);
  const endsISO = zonedInputToISO(endsAt);
  const timeErr =
    startsISO === null || endsISO === null
      ? '时间格式不正确'
      : startsISO && endsISO && Date.parse(endsISO) <= Date.parse(startsISO)
        ? '结束时间必须晚于开始时间'
        : undefined;
  const priorityErr = /^-?\d+$/.test(priority.trim()) ? undefined : '需为整数';
  const targetMissing = side === 'cost' ? !channelId : !virtualModel;
  const invalid = !!ratioErr || !!timeErr || !!priorityErr || targetMissing;

  const submit = async () => {
    if (!offer) return;
    const body: AdoptOfferBody = {
      side,
      name: name.trim() || undefined,
      discount_ratio: ratio.trim(),
      starts_at: startsISO ?? undefined,
      ends_at: endsISO ?? undefined,
      priority: Number(priority.trim()),
    };
    if (side === 'cost') body.channel_id = Number(channelId);
    else {
      body.virtual_model = virtualModel;
      if (budget !== null) body.budget_total = budget;
    }
    setSubmitting(true);
    setError(null);
    try {
      const res = await adoptUpstreamOffer(offer.id, body, idemKey);
      setConfirming(false);
      onAdopted(offer, res.promotion_id, side);
    } catch (err) {
      setConfirming(false);
      setError(friendlyError(err, '采用失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const discount = ratioErr ? null : discountText(ratio, offer?.offer_type);

  return (
    <>
      <FormModal
        open={!!offer && !confirming}
        onClose={onClose}
        width="lg"
        title={`采用优惠情报 #${offer?.id ?? ''}`}
        description={offer ? `${offer.provider_code} · ${offer.upstream_model ?? '全场 / 账号级'} · ${OFFER_TYPE_LABELS[offer.offer_type] ?? offer.offer_type}` : undefined}
        onSubmit={() => (side === 'sell' ? setConfirming(true) : submit())}
        submitting={submitting}
        submitDisabled={invalid}
        submitLabel={side === 'sell' ? '下一步' : '采用'}
        error={error}
      >
        <Field label="采用方式" required>
          <RadioCards<Side>
            value={side}
            onChange={setSide}
            options={[
              { value: 'cost', label: '记录成本优惠', hint: '渠道维度，仅供毛利核算参考，不影响用户计费' },
              { value: 'sell', label: '对用户让利', hint: '虚拟模型维度，保存后立即按折扣向用户计费', tone: 'danger' },
            ]}
          />
        </Field>
        {side === 'cost' ? (
          <Field label="渠道" required hint={offer?.upstream_model ? `可搜索上游模型 ${offer.upstream_model}` : '选择享受该优惠的渠道'}>
            <RemoteSelect className="w-full" value={channelId} placeholder="搜索渠道…" load={searchChannels} resolve={channelLabel} onChange={(v) => setChannelId(v)} />
          </Field>
        ) : (
          <>
            <div className="bg-rose-50 border border-rose-200 rounded-lg p-3 text-xs text-rose-800">
              <b>售价面促销会立即影响用户账单</b>：所选虚拟模型的所有调用在时间窗内都会按折扣计费，直到促销结束或预算用尽。请确认上游优惠真实有效、且我方成本足以覆盖。
            </div>
            <Field label="虚拟模型" required>
              <RemoteSelect
                className="w-full"
                value={virtualModel}
                placeholder="搜索虚拟模型…"
                load={searchVirtualModelNames}
                resolve={async (v) => v}
                onChange={(v) => setVirtualModel(v)}
              />
            </Field>
          </>
        )}
        <div className="grid grid-cols-2 gap-3">
          <Field label="价格乘数" required error={ratioErr} hint={discount ? `= ${discount}` : undefined}>
            <Input mono value={ratio} invalid={!!ratioErr} onChange={(e) => setRatio(e.target.value)} placeholder="0.5" />
          </Field>
          <Field label="促销名称" hint="留空自动生成">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={offer ? `[情报#${offer.id}] …` : ''} />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label="开始时间" hint="留空 = 立即（或情报的开始时间）" error={timeErr}>
            <Input type="datetime-local" mono value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
          </Field>
          <Field label="结束时间" hint="留空 = 情报的结束时间；都没有则长期有效">
            <Input type="datetime-local" mono value={endsAt} onChange={(e) => setEndsAt(e.target.value)} />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label="优先级" error={priorityErr} hint="多个促销同时命中时数值大的优先">
            <Input mono inputMode="numeric" value={priority} invalid={!!priorityErr} onChange={(e) => setPriority(e.target.value)} />
          </Field>
          {side === 'sell' && (
            <Field label="让利预算（元）" hint="累计让利达到预算后按原价计费；留空 = 不限">
              <MoneyInput valueMicro={budget} onChange={setBudget} placeholder="不限" />
            </Field>
          )}
        </div>
      </FormModal>
      <ConfirmDialog
        open={!!offer && confirming}
        onClose={() => setConfirming(false)}
        onConfirm={submit}
        loading={submitting}
        level="danger"
        confirmLabel="确认让利"
        title={`对「${virtualModel}」的用户${discount ?? ''}让利？`}
      >
        <p className="text-rose-700 font-medium">保存后立即生效，所有调用 {virtualModel} 的用户都会按折扣计费。</p>
        <ul className="text-xs text-gray-600 space-y-0.5 list-disc pl-4">
          <li>
            价格乘数 <span className="font-mono">{ratio}</span>（{discount}）
          </li>
          <li>
            时间：{startsISO ? formatDateTime(startsISO).slice(0, 16) : '立即'} ~ {endsISO ? formatDateTime(endsISO).slice(0, 16) : offer?.ends_at ? formatDateTime(offer.ends_at).slice(0, 16) : '长期'}
          </li>
          <li>预算：{budget !== null ? `¥${(budget / 1_000_000).toLocaleString('en-US')}` : '不限'}</li>
        </ul>
        <p className="text-[11px] text-gray-500">
          后台暂无促销管理页，如需提前结束需联系研发停用生成的促销。建议先到{' '}
          <Link to="/pricing/comparison" className="text-purple-600 hover:underline">
            比价看板
          </Link>{' '}
          核对该模型的成本与毛利。
        </p>
      </ConfirmDialog>
    </>
  );
}
