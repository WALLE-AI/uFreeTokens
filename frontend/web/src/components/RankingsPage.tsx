import React, { useState, useEffect, useMemo, useCallback } from 'react';
import {
  BarChart3,
  TrendingUp,
  Zap,
  DollarSign,
  Globe,
  Code2,
  Maximize2,
  Wrench,
  Image as ImageIcon,
  Smartphone,
  HelpCircle,
  ChevronDown,
  ChevronUp,
  Search,
  ExternalLink,
  ArrowUpRight,
  ArrowDownRight,
  Info,
  Sliders,
  CheckCircle2,
  PieChart,
  CalendarDays,
  RefreshCw
} from 'lucide-react';
import { Model } from '../types';
import { EXTERNAL_SCORE_META, formatExternalScore } from '../data/models';
import { ApiError } from '../api/errors';
import {
  RankingsPeriod,
  RankingsMethodology,
  ModelRankings,
  ModelRankingEntry,
  AuthorRankings,
  SpeedRankings,
  AppRankings,
  getModelRankings,
  getAuthorRankings,
  getSpeedRankings,
  getToolRankings,
  getMultimodalRankings,
  getAppRankings,
  isRankingsMaintenance
} from '../api/rankings';

interface RankingsPageProps {
  allModels?: Model[];
  onSelectModel?: (model: Model) => void;
  onNavigateToBenchmarks?: () => void;
  onNavigateToModels?: () => void;
}

// 板块总开关（技术方案 §3.6）：按任务分类 / 多语言 / 编程语言三个板块需要
// 分析用户 prompt 内容，目前没有合规可行的数据来源，统一在这里关掉——不展示
// 假数据。将来有了数据源，把对应项改成 true 并接上接口即可；目录导航会跟着
// 这张表自动增减。
const SECTION_ENABLED: Record<string, boolean> = {
  'top-models': true,
  leaderboard: true,
  'task-models': false,
  'cost-session': true,
  'market-share': true,
  'benchmarks-sec': true,
  'fastest-models': true,
  'languages-sec': false,
  'programming-sec': false,
  'context-length': true,
  'tool-calls': true,
  'images-sec': true,
  'top-apps': true,
  measurement: true
};

const NAV_ITEMS = [
  { id: 'top-models', label: '热门模型趋势', icon: BarChart3 },
  { id: 'leaderboard', label: '模型排行榜', icon: TrendingUp },
  { id: 'task-models', label: '按任务分类排行', icon: Zap },
  { id: 'cost-session', label: '单会话成本', icon: DollarSign },
  { id: 'market-share', label: '市场份额', icon: PieChart },
  { id: 'benchmarks-sec', label: '基准测试', icon: Sliders },
  { id: 'fastest-models', label: '最快推理模型', icon: Zap },
  { id: 'languages-sec', label: '自然语言分布', icon: Globe },
  { id: 'programming-sec', label: '编程语言分布', icon: Code2 },
  { id: 'context-length', label: '上下文长度', icon: Maximize2 },
  { id: 'tool-calls', label: '工具调用', icon: Wrench },
  { id: 'images-sec', label: '图像处理量', icon: ImageIcon },
  { id: 'top-apps', label: '热门应用', icon: Smartphone },
  { id: 'measurement', label: '统计方法与说明', icon: HelpCircle }
].filter((item) => SECTION_ENABLED[item.id]);

// 统计周期：都是截至昨天的完整日（Asia/Shanghai），具体起止以接口回显的
// from/to 为准，这里只是按钮文案。
const PERIOD_OPTIONS: { id: RankingsPeriod; label: string }[] = [
  { id: 'day', label: '日榜' },
  { id: 'week', label: '周榜' },
  { id: 'month', label: '月榜' }
];

// 单次会话成本的 token 画像（编码智能体式多轮会话的假设，写进"统计方法"）：
// 第 1 轮输入 = 系统提示词 + 工具定义 + 首个请求；之后每轮都会把完整上下文
// 重新发送一遍，上下文每轮增长 contextGrowthPerTurn（上一轮的输出 + 新的
// 工具结果/用户消息）；每轮输出固定 outputPerTurn。不计缓存折扣。
const SESSION_PROFILE = {
  firstTurnInput: 4000,
  contextGrowthPerTurn: 2000,
  outputPerTurn: 500
};
const SESSION_TURNS = [1, 2, 10, 50] as const;

function sessionTokens(turns: number) {
  const { firstTurnInput, contextGrowthPerTurn, outputPerTurn } = SESSION_PROFILE;
  return {
    input: turns * firstTurnInput + (contextGrowthPerTurn * turns * (turns - 1)) / 2,
    output: turns * outputPerTurn,
    // 最后一轮请求占用的上下文，超过模型上下文窗口的会话实际跑不完。
    peakContext: firstTurnInput + contextGrowthPerTurn * (turns - 1) + outputPerTurn
  };
}

// 散点图横轴用的混合单价：按 3:1 的输入/输出 token 比例加权（业界常用口径）。
function blendedPrice(m: Model): number {
  return (3 * m.inputPricePerM + m.outputPricePerM) / 4;
}

// 基准跑分散点图可选的纵轴指标。除综合指数（运营录入）外，其余都是后端从
// 公开评测榜单自动投影进 /v1/catalog scores 的成绩；没有该项成绩的模型不画。
type ScatterMetricId = 'intelligenceIndex' | 'arenaText' | 'arenaCoding' | 'gpqaDiamond' | 'sweBenchVerified' | 'epochEci';

interface ScatterMetric {
  id: ScatterMetricId;
  label: string;
  axisLabel: string;
  source: string;
  get: (m: Model) => number | undefined;
  format: (v: number) => string;
}

const SCATTER_METRICS: ScatterMetric[] = [
  {
    id: 'intelligenceIndex',
    label: '综合指数',
    axisLabel: '综合智能指数',
    source: '运营录入的综合智能指数',
    get: (m) => m.scores?.intelligenceIndex,
    format: (v) => `${Math.round(v * 10) / 10}`,
  },
  ...(['arenaText', 'arenaCoding', 'gpqaDiamond', 'sweBenchVerified', 'epochEci'] as const).map(
    (key): ScatterMetric => ({
      id: key,
      label: EXTERNAL_SCORE_META[key].unit === 'elo' ? `${EXTERNAL_SCORE_META[key].label} Elo` : EXTERNAL_SCORE_META[key].label,
      axisLabel:
        EXTERNAL_SCORE_META[key].unit === 'elo'
          ? `${EXTERNAL_SCORE_META[key].label} Elo`
          : EXTERNAL_SCORE_META[key].unit === 'percent'
            ? `${EXTERNAL_SCORE_META[key].label}（%）`
            : EXTERNAL_SCORE_META[key].label,
      source:
        EXTERNAL_SCORE_META[key].unit === 'elo'
          ? 'LMArena (arena.ai) leaderboard dataset，CC BY 4.0'
          : "Epoch AI 'AI Benchmarking Hub'，CC BY 4.0",
      get: (m) => m.scores?.[key],
      format: (v) => formatExternalScore(key, v),
    })
  ),
];

const TREND_COLORS = [
  { bg: 'bg-emerald-500', text: 'text-emerald-400' },
  { bg: 'bg-blue-500', text: 'text-blue-400' },
  { bg: 'bg-purple-500', text: 'text-purple-400' },
  { bg: 'bg-pink-500', text: 'text-pink-400' },
  { bg: 'bg-amber-500', text: 'text-amber-400' }
];
const TREND_OTHERS_COLOR = { bg: 'bg-slate-400', text: 'text-gray-400' };

const AUTHOR_COLORS = ['#3b82f6', '#10b981', '#14b8a6', '#ec4899', '#8b5cf6', '#f59e0b', '#f97316', '#06b6d4', '#6366f1'];
const OTHERS_COLOR = '#94a3b8';

// ---------- 格式化 ----------

function formatTokens(n: number): string {
  const units: [number, string][] = [[1e12, 'T'], [1e9, 'B'], [1e6, 'M'], [1e3, 'K']];
  for (const [base, unit] of units) {
    if (n >= base) {
      const v = n / base;
      return `${v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)}${unit}`;
    }
  }
  return String(Math.round(n));
}

function formatShare(share: number): string {
  const pct = share * 100;
  if (share > 0 && pct < 0.1) return '<0.1%';
  return `${pct.toFixed(1)}%`;
}

function formatCNY(v: number): string {
  if (v === 0) return '¥0';
  if (v < 0.01) return `¥${v.toFixed(4)}`;
  if (v < 1) return `¥${v.toFixed(3)}`;
  return `¥${v.toFixed(2)}`;
}

function formatContext(n: number): string {
  if (n >= 1e6) return `${Number((n / 1e6).toFixed(2))}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}K`;
  return String(n);
}

// 日期都是 YYYY-MM-DD 的东八区自然日，按 UTC 解析只做日期加减，不涉及时区换算。
function addDays(day: string, n: number): string {
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return day;
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

function enumerateDays(from: string, to: string): string[] {
  const days: string[] = [];
  for (let d = from; d < to && days.length < 400; d = addDays(d, 1)) {
    if (addDays(d, 1) === d) break;
    days.push(d);
  }
  return days;
}

function formatDayLabel(day: string): string {
  const [, m, d] = day.split('-');
  return m && d ? `${Number(m)}月${Number(d)}日` : day;
}

function formatUpdatedAt(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  });
}

function safeHost(url: string): string | null {
  try {
    const u = new URL(url);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.host : null;
  } catch {
    return null;
  }
}

// ---------- 数据加载 ----------

type LoadState<T> =
  | { status: 'loading' }
  | { status: 'ready'; data: T }
  | { status: 'maintenance' }
  | { status: 'error'; message: string };

function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    return err.status === 404 ? '该榜单暂未开放' : err.message;
  }
  if (err instanceof DOMException && err.name === 'AbortError') return '请求超时，请稍后重试';
  return '网络异常，请稍后重试';
}

// useRankings 拉取一个榜单接口；切换周期时取消上一次请求。失败只进入错误态，
// 绝不回落到任何本地假数据。
function useRankings<T>(
  fetcher: (period: RankingsPeriod, signal: AbortSignal) => Promise<T>,
  period: RankingsPeriod
): [LoadState<T>, () => void] {
  const [state, setState] = useState<LoadState<T>>({ status: 'loading' });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: 'loading' });
    fetcher(period, controller.signal).then(
      (data) => {
        if (!controller.signal.aborted) setState({ status: 'ready', data });
      },
      (err) => {
        if (controller.signal.aborted) return;
        setState(isRankingsMaintenance(err) ? { status: 'maintenance' } : { status: 'error', message: describeError(err) });
      }
    );
    return () => controller.abort();
  }, [fetcher, period, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return [state, reload];
}

// fetcher 放在模块级，保证引用稳定，不会让 useRankings 的 effect 反复触发。
const fetchModels = (p: RankingsPeriod, signal: AbortSignal) => getModelRankings(p, { signal, limit: 20, series: true });
const fetchAuthors = (p: RankingsPeriod, signal: AbortSignal) => getAuthorRankings(p, { signal });
const fetchSpeed = (p: RankingsPeriod, signal: AbortSignal) => getSpeedRankings(p, { signal });
const fetchTools = (p: RankingsPeriod, signal: AbortSignal) => getToolRankings(p, { signal });
const fetchMultimodal = (p: RankingsPeriod, signal: AbortSignal) => getMultimodalRankings(p, { signal });
const fetchApps = (p: RankingsPeriod, signal: AbortSignal) => getAppRankings(p, { signal });

// ---------- 通用小组件 ----------

interface StatusBlockProps<T> {
  state: LoadState<T>;
  isEmpty: (data: T) => boolean;
  emptyText: string;
  onRetry: () => void;
  children: (data: T) => React.ReactNode;
}

// StatusBlock 统一渲染加载中 / 数据维护中（503）/ 失败 / 空数据四种状态。
function StatusBlock<T>({ state, isEmpty, emptyText, onRetry, children }: StatusBlockProps<T>) {
  if (state.status === 'loading') {
    return (
      <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-12 rounded-lg bg-gray-100 animate-pulse" />
          ))}
        </div>
      </div>
    );
  }
  if (state.status === 'maintenance') {
    return (
      <div className="bg-amber-50/60 border border-amber-200 rounded-2xl p-8 text-center shadow-xs">
        <Wrench className="w-5 h-5 text-amber-500 mx-auto mb-2" />
        <div className="text-sm font-semibold text-amber-900">数据维护中</div>
        <p className="text-xs text-amber-700 mt-1">公开榜单暂时下线维护，请稍后再来查看。</p>
      </div>
    );
  }
  if (state.status === 'error') {
    return (
      <div className="bg-white border border-gray-200 rounded-2xl p-8 text-center shadow-xs">
        <div className="text-sm font-semibold text-gray-900">加载失败</div>
        <p className="text-xs text-gray-500 mt-1">{state.message}</p>
        <button
          onClick={onRetry}
          className="mt-3 inline-flex items-center gap-1 text-xs font-medium text-purple-600 hover:text-purple-800 px-3 py-1.5 rounded-lg bg-white border border-gray-200 hover:bg-gray-50 shadow-xs transition-colors"
        >
          <RefreshCw className="w-3.5 h-3.5" />
          重试
        </button>
      </div>
    );
  }
  if (isEmpty(state.data)) {
    return <EmptyBlock text={emptyText} />;
  }
  return <>{children(state.data)}</>;
}

function EmptyBlock({ text }: { text: string }) {
  return (
    <div className="bg-white border border-dashed border-gray-300 rounded-2xl p-8 text-center text-xs text-gray-500">
      {text}
    </div>
  );
}

function DeprecatedBadge() {
  return (
    <span className="shrink-0 px-1.5 py-0.5 rounded bg-gray-100 text-gray-500 text-[10px] font-medium border border-gray-200">
      已下架
    </span>
  );
}

function ChangeTag({ change }: { change: number | null }) {
  if (change === null) {
    return <div className="text-[11px] font-medium text-purple-600">新上榜</div>;
  }
  const pct = Math.round(change * 100);
  const color = pct > 0 ? 'text-emerald-600' : pct < 0 ? 'text-rose-600' : 'text-gray-400';
  return (
    <div className={`text-[11px] font-medium flex items-center justify-end gap-0.5 ${color}`}>
      {pct > 0 && <ArrowUpRight className="w-3 h-3" />}
      {pct < 0 && <ArrowDownRight className="w-3 h-3" />}
      {pct > 0 ? '+' : ''}
      {pct}%
    </div>
  );
}

function SectionHeader({
  icon: Icon,
  iconClass,
  title,
  subtitle,
  children
}: {
  icon: React.ComponentType<{ className?: string }>;
  iconClass: string;
  title: string;
  subtitle: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
      <div>
        <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
          <Icon className={`w-5 h-5 ${iconClass}`} />
          {title}
        </h2>
        <p className="text-xs text-gray-500 mt-0.5">{subtitle}</p>
      </div>
      {children}
    </div>
  );
}

// ModelShareList 渲染"模型 + 份额"的两列网格（工具调用、多模态共用）。
function ModelShareList({
  data,
  accentClass,
  resolveModel,
  onSelect
}: {
  data: ModelRankings;
  accentClass: string;
  resolveModel: (entry: { model: string; deprecated: boolean }) => Model | undefined;
  onSelect: (m: Model) => void;
}) {
  return (
    <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
        {data.models.map((row) => {
          const target = resolveModel(row);
          return (
            <div
              key={row.model}
              onClick={target ? () => onSelect(target) : undefined}
              className={`flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 transition-colors ${
                target ? 'hover:bg-purple-50/30 cursor-pointer' : ''
              }`}
            >
              <div className="flex items-center gap-2 min-w-0">
                <span className="w-5 text-center font-mono text-xs text-gray-400">{row.rank}</span>
                <span className={`text-xs font-medium truncate ${row.deprecated ? 'text-gray-400' : 'text-gray-900'}`}>
                  {row.displayName}
                </span>
                {row.deprecated && <DeprecatedBadge />}
              </div>
              <div className="text-right shrink-0">
                <span className={`font-mono text-xs font-bold ${accentClass}`}>{formatShare(row.share)}</span>
                {row.tokens !== undefined && (
                  <span className="text-[11px] text-gray-500 ml-1.5">({formatTokens(row.tokens)})</span>
                )}
              </div>
            </div>
          );
        })}
        {data.others && data.others.share > 0 && (
          <div className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-dashed border-gray-200">
            <div className="flex items-center gap-2">
              <span className="w-5 text-center font-mono text-xs text-gray-400">—</span>
              <span className="text-xs font-medium text-gray-500">其他模型</span>
            </div>
            <div className="text-right">
              <span className="font-mono text-xs font-bold text-gray-500">{formatShare(data.others.share)}</span>
              {data.others.tokens !== undefined && (
                <span className="text-[11px] text-gray-500 ml-1.5">({formatTokens(data.others.tokens)})</span>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// 无数据源板块的占位（SECTION_ENABLED 打开但尚未接线时显示），不放任何假数据。
function PlaceholderSection({ id, icon, iconClass, title }: { id: string; icon: React.ComponentType<{ className?: string }>; iconClass: string; title: string }) {
  return (
    <section id={id} className="scroll-mt-32">
      <SectionHeader icon={icon} iconClass={iconClass} title={title} subtitle="该榜单需要分析请求内容，暂无合规的数据来源" />
      <EmptyBlock text="暂未开放" />
    </section>
  );
}

// ---------- 热门模型趋势图 ----------

// TrendChart 画前 5 个（未下架的）模型的每日堆叠柱。份额模式下每根柱子是
// 当天全平台 100%，"其他"= 1 − 前 5 名份额之和；绝对量模式用 tokens/share
// 反推当天总量，再算出"其他"。
function TrendChart({ data, mode }: { data: ModelRankings; mode: 'share' | 'absolute' }) {
  const top = data.models.filter((m) => !m.deprecated && m.series && m.series.length > 0).slice(0, 5);

  let days = enumerateDays(data.from, data.to);
  if (days.length === 0) {
    days = Array.from(new Set(top.flatMap((m) => (m.series ?? []).map((p) => p.day)))).sort();
  }

  const lookup = top.map((m) => new Map((m.series ?? []).map((p) => [p.day, p])));
  const columns = days.map((day) => {
    const points = lookup.map((l) => l.get(day));
    if (mode === 'share') {
      const values = points.map((p) => p?.share ?? 0);
      const sum = values.reduce((a, b) => a + b, 0);
      const others = sum > 0 ? Math.max(0, 1 - sum) : 0;
      return { day, values, others, total: sum + others };
    }
    const values = points.map((p) => p?.tokens ?? 0);
    const sum = values.reduce((a, b) => a + b, 0);
    const ref = points.find((p) => p && p.share > 0 && p.tokens !== undefined);
    const dayTotal = ref ? (ref.tokens as number) / ref.share : sum;
    const others = Math.max(0, dayTotal - sum);
    return { day, values, others, total: sum + others };
  });

  const yMax = mode === 'share' ? 1 : Math.max(0, ...columns.map((c) => c.total));
  if (top.length === 0 || yMax <= 0) {
    return <EmptyBlock text="统计区间内暂无可展示的模型用量趋势" />;
  }

  const fmt = (v: number) => (mode === 'share' ? formatShare(v) : formatTokens(v));
  const tickEvery = Math.max(1, Math.ceil(days.length / 8));
  const peak = Math.max(...columns.map((c) => c.total));

  return (
    <div className="bg-white border border-gray-200 rounded-2xl p-6 relative overflow-hidden shadow-xs">
      <div className="flex items-center justify-between text-xs text-gray-500 mb-3">
        <span>{mode === 'share' ? '每日 Token 份额（占当日全平台）' : '每日处理 Token 量'}</span>
        {mode === 'absolute' && (
          <span className="font-mono text-emerald-600 font-semibold">峰值：{formatTokens(peak)} / 日</span>
        )}
      </div>

      <div className="h-64 w-full flex flex-col justify-end pt-4 pb-2 relative">
        {/* Grid Lines */}
        <div className="absolute inset-0 flex flex-col justify-between pointer-events-none opacity-40">
          {[1, 0.75, 0.5, 0.25, 0].map((r) => (
            <div
              key={r}
              className={`border-b ${r === 0 ? '' : 'border-dashed'} border-gray-300 w-full flex justify-end text-[10px] text-gray-400 pr-1`}
            >
              {r === 0 ? '0' : fmt(yMax * r)}
            </div>
          ))}
        </div>

        <div className="flex items-end justify-between h-48 gap-1 md:gap-1.5 z-10 px-2">
          {columns.map((col, idx) => (
            <div key={col.day} className="flex-1 flex flex-col items-center group relative h-full justify-end">
              <div
                className="w-full rounded-t-sm flex flex-col justify-end transition-all group-hover:brightness-110 overflow-hidden"
                style={{ height: `${(col.total / yMax) * 100}%` }}
              >
                {col.total > 0 && (
                  <>
                    <div className={`${TREND_OTHERS_COLOR.bg} w-full`} style={{ height: `${(col.others / col.total) * 100}%` }} />
                    {col.values
                      .map((v, i) => ({ v, i }))
                      .reverse()
                      .map(({ v, i }) => (
                        <div key={i} className={`${TREND_COLORS[i].bg} w-full`} style={{ height: `${(v / col.total) * 100}%` }} />
                      ))}
                  </>
                )}
              </div>

              {/* Tooltip on hover */}
              <div className="opacity-0 group-hover:opacity-100 transition-opacity absolute bottom-full mb-2 z-30 bg-gray-900 border border-gray-800 text-gray-100 text-[11px] p-2.5 rounded-lg shadow-2xl pointer-events-none whitespace-nowrap min-w-36">
                <div className="font-semibold text-white mb-1 border-b border-gray-800 pb-1">{col.day}</div>
                {top.map((m, i) => (
                  <div key={m.model} className={`${TREND_COLORS[i].text} flex justify-between gap-2`}>
                    <span>{m.displayName}</span> <span>{fmt(col.values[i])}</span>
                  </div>
                ))}
                <div className={`${TREND_OTHERS_COLOR.text} flex justify-between gap-2`}>
                  <span>其他模型</span> <span>{fmt(col.others)}</span>
                </div>
                {mode === 'absolute' && (
                  <div className="font-bold text-white mt-1 pt-1 border-t border-gray-800 flex justify-between">
                    <span>总计</span> <span>{fmt(col.total)}</span>
                  </div>
                )}
              </div>

              <span className="text-[10px] text-gray-400 mt-2 h-3 whitespace-nowrap">
                {idx % tickEvery === 0 ? formatDayLabel(col.day) : ''}
              </span>
            </div>
          ))}
        </div>
      </div>

      {/* Legend */}
      <div className="flex flex-wrap items-center justify-center gap-4 mt-8 pt-4 border-t border-gray-100 text-xs">
        {top.map((m, i) => (
          <div key={m.model} className="flex items-center gap-1.5">
            <span className={`w-3 h-3 rounded-sm ${TREND_COLORS[i].bg}`} /> <span className="text-gray-600">{m.displayName}</span>
          </div>
        ))}
        <div className="flex items-center gap-1.5">
          <span className={`w-3 h-3 rounded-sm ${TREND_OTHERS_COLOR.bg}`} /> <span className="text-gray-500">其他模型</span>
        </div>
      </div>
    </div>
  );
}

// ---------- 页面 ----------

export const RankingsPage: React.FC<RankingsPageProps> = ({
  allModels = [],
  onSelectModel,
  onNavigateToBenchmarks,
  onNavigateToModels
}) => {
  const [period, setPeriod] = useState<RankingsPeriod>('week');

  // Active section for sidebar highlight
  const [activeSection, setActiveSection] = useState<string>('top-models');

  // Section states
  const [trendMode, setTrendMode] = useState<'share' | 'absolute'>('share');
  const [showMoreLeaderboard, setShowMoreLeaderboard] = useState<boolean>(false);
  const [showMoreCostModels, setShowMoreCostModels] = useState<boolean>(false);
  const [costSort, setCostSort] = useState<'asc' | 'desc'>('asc');
  const [marketShareMode, setMarketShareMode] = useState<'absolute' | 'percentage'>('percentage');
  const [benchmarkSearch, setBenchmarkSearch] = useState<string>('');
  // null = 自动（覆盖模型最多的指标）。
  const [scatterMetricId, setScatterMetricId] = useState<ScatterMetricId | null>(null);
  const [showPareto, setShowPareto] = useState<boolean>(true);
  const [showMoreContext, setShowMoreContext] = useState<boolean>(false);

  const [modelsState, reloadModels] = useRankings<ModelRankings>(fetchModels, period);
  const [authorsState, reloadAuthors] = useRankings<AuthorRankings>(fetchAuthors, period);
  const [speedState, reloadSpeed] = useRankings<SpeedRankings>(fetchSpeed, period);
  const [toolsState, reloadTools] = useRankings<ModelRankings>(fetchTools, period);
  const [multimodalState, reloadMultimodal] = useRankings<ModelRankings>(fetchMultimodal, period);
  const [appsState, reloadApps] = useRankings<AppRankings>(fetchApps, period);

  const scrollToSection = (id: string) => {
    setActiveSection(id);
    const element = document.getElementById(id);
    if (element) {
      const yOffset = -80;
      const y = element.getBoundingClientRect().top + window.pageYOffset + yOffset;
      window.scrollTo({ top: y, behavior: 'smooth' });
    }
  };

  // Track scroll position for active section
  useEffect(() => {
    const handleScroll = () => {
      const scrollPos = window.scrollY + 120;
      for (const item of NAV_ITEMS) {
        const el = document.getElementById(item.id);
        if (el) {
          const top = el.offsetTop;
          const height = el.offsetHeight;
          if (scrollPos >= top && scrollPos < top + height) {
            setActiveSection(item.id);
            break;
          }
        }
      }
    };
    window.addEventListener('scroll', handleScroll, { passive: true });
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);

  // 榜单里的 model 字段就是虚拟模型名，等于 catalog 模型的 id；已下架或不在
  // 公开目录里的模型不可点击。
  const modelById = useMemo(() => new Map(allModels.map((m) => [m.id, m])), [allModels]);
  const resolveModel = useCallback(
    (entry: { model: string; deprecated: boolean }) => (entry.deprecated ? undefined : modelById.get(entry.model)),
    [modelById]
  );
  const selectModel = (m: Model) => onSelectModel?.(m);

  // ----- 阶段 0：基于模型目录在前端计算的三个板块 -----

  const activeModels = useMemo(() => allModels.filter((m) => !m.isDeprecated), [allModels]);

  // 基准跑分散点的纵轴指标：用户没手动选时，默认取已加载模型里覆盖最多
  // （同时有该项成绩和售价）的指标。
  const scatterCoverage = useMemo(
    () =>
      SCATTER_METRICS.map((metric) => ({
        metric,
        count: activeModels.filter((m) => {
          const v = metric.get(m);
          return v !== undefined && Number.isFinite(v) && v > 0 && blendedPrice(m) > 0;
        }).length,
      })),
    [activeModels]
  );
  const scatterMetric = useMemo(() => {
    const chosen = scatterCoverage.find((c) => c.metric.id === scatterMetricId);
    if (chosen) return chosen.metric;
    let best = scatterCoverage[0];
    for (const c of scatterCoverage) if (c.count > best.count) best = c;
    return best.metric;
  }, [scatterCoverage, scatterMetricId]);

  // 基准跑分散点：所选指标 × 混合单价；没有该项成绩或售价为 0（未录入）的不参与。
  const scatterPoints = useMemo(() => {
    const pts = activeModels
      .map((m) => ({ model: m, score: scatterMetric.get(m) ?? 0, price: blendedPrice(m), isPareto: false }))
      .filter((p) => Number.isFinite(p.score) && p.score > 0 && p.price > 0)
      .sort((a, b) => a.price - b.price || b.score - a.score);
    // 帕累托前沿：按价格升序扫描，分数严格创新高的点不被任何"更便宜且更强"的模型支配。
    let best = -Infinity;
    for (const p of pts) {
      if (p.score > best) {
        p.isPareto = true;
        best = p.score;
      }
    }
    return pts;
  }, [activeModels, scatterMetric]);

  const filteredScatter = useMemo(() => {
    const q = benchmarkSearch.trim().toLowerCase();
    if (!q) return scatterPoints;
    return scatterPoints.filter(
      (p) =>
        p.model.name.toLowerCase().includes(q) ||
        p.model.id.toLowerCase().includes(q) ||
        p.model.providerDisplay.toLowerCase().includes(q)
    );
  }, [scatterPoints, benchmarkSearch]);

  const scatterAxes = useMemo(() => {
    if (scatterPoints.length === 0) return null;
    const prices = scatterPoints.map((p) => p.price);
    const scores = scatterPoints.map((p) => p.score);
    let lMin = Math.log10(Math.min(...prices));
    let lMax = Math.log10(Math.max(...prices));
    if (lMax - lMin < 0.2) {
      lMin -= 0.3;
      lMax += 0.3;
    }
    // Elo 这类大数值区间按 50 取整刻度，百分制 / 指数按 5。
    const step = Math.max(...scores) - Math.min(...scores) > 150 ? 50 : 5;
    const pad = step === 50 ? 10 : 2;
    let sMin = Math.floor((Math.min(...scores) - pad) / step) * step;
    let sMax = Math.ceil((Math.max(...scores) + pad) / step) * step;
    if (sMin < 0) sMin = 0;
    if (sMax <= sMin) sMax = sMin + 10;
    const x = (price: number) => 50 + ((Math.log10(price) - lMin) / (lMax - lMin)) * 420;
    const y = (score: number) => 170 - ((score - sMin) / (sMax - sMin)) * 150;
    const xTicks = [0, 1 / 3, 2 / 3, 1].map((r) => Math.pow(10, lMin + r * (lMax - lMin)));
    const yTicks = [0, 1 / 3, 2 / 3, 1].map((r) => sMin + r * (sMax - sMin));
    return { x, y, xTicks, yTicks };
  }, [scatterPoints]);

  const paretoPath = useMemo(() => {
    if (!scatterAxes) return '';
    return scatterPoints
      .filter((p) => p.isPareto)
      .map((p, i) => `${i === 0 ? 'M' : 'L'} ${scatterAxes.x(p.price).toFixed(1)} ${scatterAxes.y(p.score).toFixed(1)}`)
      .join(' ');
  }, [scatterPoints, scatterAxes]);

  const scatterRanked = useMemo(() => [...filteredScatter].sort((a, b) => b.score - a.score), [filteredScatter]);

  // 单次会话成本：售价（¥/百万 token）× SESSION_PROFILE 推出的 token 量。
  const sessionProfiles = useMemo(() => SESSION_TURNS.map((t) => ({ turns: t, ...sessionTokens(t) })), []);
  const costRows = useMemo(() => {
    const rows = activeModels
      .filter((m) => m.inputPricePerM > 0 || m.outputPricePerM > 0)
      .map((m) => ({
        model: m,
        costs: sessionProfiles.map((p) => ({
          cost: (p.input / 1e6) * m.inputPricePerM + (p.output / 1e6) * m.outputPricePerM,
          exceeds: m.contextTokens > 0 && p.peakContext > m.contextTokens
        }))
      }));
    // 按 10 轮会话成本排序。
    rows.sort((a, b) => (costSort === 'asc' ? 1 : -1) * (a.costs[2].cost - b.costs[2].cost));
    return rows;
  }, [activeModels, sessionProfiles, costSort]);

  const contextRows = useMemo(
    () => activeModels.filter((m) => m.contextTokens > 0).sort((a, b) => b.contextTokens - a.contextTokens),
    [activeModels]
  );
  const maxContext = contextRows[0]?.contextTokens ?? 0;

  // ----- 统计区间 / 统计口径（取第一个加载成功的用量类接口） -----

  const usageMeta = [modelsState, authorsState, appsState, toolsState, multimodalState, speedState]
    .map((s) => (s.status === 'ready' ? s.data : null))
    .find((d) => d !== null) ?? null;
  const methodology: RankingsMethodology | null =
    [modelsState, authorsState, appsState, toolsState, multimodalState, speedState]
      .map((s) => (s.status === 'ready' ? s.data.methodology : null))
      .find((m) => m !== null) ?? null;

  const minAccountsText = methodology ? `${methodology.minDistinctAccounts} 个` : '最低门槛数量的';
  const maxShareText = methodology ? `${Math.round(methodology.maxAccountShare * 100)}%` : '一定比例';

  const trendAbsoluteAvailable =
    modelsState.status === 'ready' &&
    modelsState.data.models.some((m) => (m.series ?? []).some((p) => p.tokens !== undefined));
  const effectiveTrendMode = trendAbsoluteAvailable ? trendMode : 'share';

  const marketAbsoluteAvailable =
    authorsState.status === 'ready' && authorsState.data.authors.some((a) => a.tokens !== undefined);
  const effectiveMarketMode = marketAbsoluteAvailable ? marketShareMode : 'percentage';

  const periodLabel = PERIOD_OPTIONS.find((p) => p.id === period)?.label ?? '';

  return (
    <div className="min-h-screen bg-white text-gray-900 font-sans selection:bg-purple-100">
      {/* Top sticky period switcher */}
      <div className="sticky top-12 z-30 bg-white/95 backdrop-blur-md border-b border-gray-200 px-4 md:px-8 py-2.5 overflow-x-auto scrollbar-none">
        <div className="max-w-7xl mx-auto flex items-center gap-1.5 min-w-max">
          <span className="flex items-center gap-1.5 text-xs text-gray-500 mr-2">
            <CalendarDays className="w-3.5 h-3.5" />
            统计周期
          </span>
          {PERIOD_OPTIONS.map((opt) => {
            const isActive = period === opt.id;
            return (
              <button
                key={opt.id}
                onClick={() => setPeriod(opt.id)}
                className={`flex items-center gap-2 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all ${
                  isActive
                    ? 'bg-gray-900 text-white shadow-xs'
                    : 'bg-gray-100 text-gray-600 hover:text-gray-900 hover:bg-gray-200'
                }`}
              >
                {opt.label}
              </button>
            );
          })}
          {usageMeta && (
            <span className="ml-3 text-xs text-gray-400 font-mono">
              {usageMeta.from} 至 {addDays(usageMeta.to, -1)}（{usageMeta.tz}）
            </span>
          )}
        </div>
      </div>

      {/* Main Container */}
      <div className="max-w-7xl mx-auto px-4 md:px-8 py-8">
        {/* Title Header */}
        <div className="mb-10">
          <h1 className="text-3xl md:text-4xl font-extrabold tracking-tight text-gray-900 mb-2">
            AI 模型排行榜
          </h1>
          <p className="text-sm md:text-base text-gray-600 max-w-4xl leading-relaxed">
            基于真实调用的大模型排行榜。用量类榜单依据开发者通过{' '}
            <span className="text-gray-900 font-semibold">uFreeTokens API</span> 实际处理的 Token 用量统计；价格、上下文与评分类榜单取自公开模型目录。{' '}
            <button
              onClick={onNavigateToModels}
              className="text-purple-600 hover:text-purple-800 font-medium underline inline-flex items-center gap-0.5 ml-1"
            >
              查看全部模型
              <ArrowUpRight className="w-3.5 h-3.5" />
            </button>
          </p>
          <div className="mt-3 flex items-center gap-2 text-xs text-gray-400 font-mono">
            {modelsState.status === 'ready' ? (
              <>
                <span className="inline-block w-2 h-2 rounded-full bg-emerald-500" />
                <span>
                  {periodLabel}统计区间 {modelsState.data.from} 至 {addDays(modelsState.data.to, -1)}
                  {modelsState.data.updatedAt ? `，数据更新于 ${formatUpdatedAt(modelsState.data.updatedAt)}` : ''}
                </span>
              </>
            ) : modelsState.status === 'loading' ? (
              <>
                <span className="inline-block w-2 h-2 rounded-full bg-gray-300 animate-pulse" />
                <span>正在加载用量数据…</span>
              </>
            ) : (
              <>
                <span className="inline-block w-2 h-2 rounded-full bg-amber-400" />
                <span>{modelsState.status === 'maintenance' ? '用量数据维护中' : '用量数据暂不可用'}</span>
              </>
            )}
          </div>
        </div>

        {/* 2-Column Layout */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-8 items-start">
          {/* Left Sticky Sidebar Nav */}
          <aside className="hidden lg:block lg:col-span-3 sticky top-28 space-y-1 bg-white border border-gray-200 rounded-2xl p-3 shadow-xs">
            <div className="px-3 py-2 text-xs font-semibold text-gray-400 uppercase tracking-wider">
              目录导航
            </div>
            <nav className="space-y-0.5 max-h-[calc(100vh-180px)] overflow-y-auto pr-1">
              {NAV_ITEMS.map((item) => {
                const Icon = item.icon;
                const isActive = activeSection === item.id;
                return (
                  <button
                    key={item.id}
                    onClick={() => scrollToSection(item.id)}
                    className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-xs font-medium transition-all text-left ${
                      isActive
                        ? 'bg-purple-50 text-purple-700 border border-purple-200 font-semibold'
                        : 'text-gray-600 hover:text-gray-900 hover:bg-gray-50'
                    }`}
                  >
                    <Icon className={`w-3.5 h-3.5 shrink-0 ${isActive ? 'text-purple-600' : 'text-gray-400'}`} />
                    <span className="truncate">{item.label}</span>
                  </button>
                );
              })}
            </nav>
          </aside>

          {/* Right Main Content Stream */}
          <main className="lg:col-span-9 space-y-16">
            {/* SECTION 1: Top Models（/v1/rankings/models?series=day） */}
            {SECTION_ENABLED['top-models'] && (
              <section id="top-models" className="scroll-mt-32">
                <SectionHeader
                  icon={BarChart3}
                  iconClass="text-purple-600"
                  title="热门模型趋势"
                  subtitle={`uFreeTokens 平台${periodLabel}内用量前 5 的模型每日 Token 走势`}
                >
                  <div className="flex items-center gap-1 bg-gray-100 border border-gray-200 p-1 rounded-lg self-start sm:self-auto">
                    <button
                      onClick={() => setTrendMode('share')}
                      className={`px-3 py-1 rounded-md text-xs font-medium transition-colors ${
                        effectiveTrendMode === 'share' ? 'bg-white text-gray-900 shadow-xs' : 'text-gray-500 hover:text-gray-900'
                      }`}
                    >
                      份额
                    </button>
                    <button
                      onClick={() => setTrendMode('absolute')}
                      disabled={!trendAbsoluteAvailable}
                      title={trendAbsoluteAvailable ? undefined : '当前不公开绝对 Token 数'}
                      className={`px-3 py-1 rounded-md text-xs font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                        effectiveTrendMode === 'absolute' ? 'bg-white text-gray-900 shadow-xs' : 'text-gray-500 hover:text-gray-900'
                      }`}
                    >
                      绝对量
                    </button>
                  </div>
                </SectionHeader>

                <StatusBlock
                  state={modelsState}
                  isEmpty={(d) => d.models.length === 0}
                  emptyText="统计区间内暂无达到上榜门槛的模型"
                  onRetry={reloadModels}
                >
                  {(d) => <TrendChart data={d} mode={effectiveTrendMode} />}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 2: LLM Leaderboard（/v1/rankings/models） */}
            {SECTION_ENABLED.leaderboard && (
              <section id="leaderboard" className="scroll-mt-32">
                <SectionHeader
                  icon={TrendingUp}
                  iconClass="text-emerald-600"
                  title="大模型排行榜"
                  subtitle={`按${periodLabel}内处理的 Token 份额排名，并与上一周期对比`}
                />

                <StatusBlock
                  state={modelsState}
                  isEmpty={(d) => d.models.length === 0}
                  emptyText="统计区间内暂无达到上榜门槛的模型"
                  onRetry={reloadModels}
                >
                  {(d) => (
                    <>
                      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                        {(showMoreLeaderboard ? d.models : d.models.slice(0, 10)).map((item: ModelRankingEntry) => {
                          const target = resolveModel(item);
                          return (
                            <div
                              key={item.model}
                              onClick={target ? () => selectModel(target) : undefined}
                              className={`flex items-center justify-between p-3.5 bg-white border border-gray-200 rounded-xl transition-all group shadow-xs ${
                                target ? 'hover:bg-purple-50/30 hover:border-purple-200 cursor-pointer' : ''
                              }`}
                            >
                              <div className="flex items-center gap-3.5 min-w-0">
                                <span className="w-6 text-center font-mono font-bold text-sm text-gray-400 group-hover:text-purple-600">
                                  {item.rank}
                                </span>
                                <div className="min-w-0">
                                  <div className="flex items-center gap-1.5 min-w-0">
                                    <span
                                      className={`font-semibold text-sm truncate ${
                                        item.deprecated ? 'text-gray-400' : 'text-gray-900 group-hover:text-purple-700'
                                      }`}
                                    >
                                      {item.displayName}
                                    </span>
                                    {item.deprecated && <DeprecatedBadge />}
                                  </div>
                                  <div className="text-xs text-gray-400 flex items-center gap-1.5">
                                    <span>来自 {item.providerDisplay || item.author}</span>
                                  </div>
                                </div>
                              </div>
                              <div className="text-right shrink-0">
                                <div className="text-xs font-semibold text-gray-800 font-mono">
                                  {formatShare(item.share)}
                                  {item.tokens !== undefined && (
                                    <span className="text-gray-400 font-normal ml-1">· {formatTokens(item.tokens)} tokens</span>
                                  )}
                                </div>
                                <ChangeTag change={item.change} />
                              </div>
                            </div>
                          );
                        })}
                        {(showMoreLeaderboard || d.models.length <= 10) && d.others && d.others.share > 0 && (
                          <div className="flex items-center justify-between p-3.5 bg-gray-50/70 border border-dashed border-gray-200 rounded-xl">
                            <div className="flex items-center gap-3.5">
                              <span className="w-6 text-center font-mono font-bold text-sm text-gray-300">—</span>
                              <div>
                                <div className="font-semibold text-sm text-gray-500">其他模型</div>
                                <div className="text-xs text-gray-400">未单独上榜的模型合计</div>
                              </div>
                            </div>
                            <div className="text-xs font-semibold text-gray-500 font-mono">
                              {formatShare(d.others.share)}
                              {d.others.tokens !== undefined && (
                                <span className="text-gray-400 font-normal ml-1">· {formatTokens(d.others.tokens)} tokens</span>
                              )}
                            </div>
                          </div>
                        )}
                      </div>

                      {d.models.length > 10 && (
                        <div className="text-center mt-4">
                          <button
                            onClick={() => setShowMoreLeaderboard(!showMoreLeaderboard)}
                            className="inline-flex items-center gap-1 text-xs font-medium text-purple-600 hover:text-purple-800 px-4 py-2 rounded-lg bg-white border border-gray-200 hover:bg-gray-50 shadow-xs transition-colors"
                          >
                            {showMoreLeaderboard ? '收起' : '展开更多'}
                            {showMoreLeaderboard ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
                          </button>
                        </div>
                      )}
                    </>
                  )}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 3: Top models by task —— 无数据源，由 SECTION_ENABLED 关闭 */}
            {SECTION_ENABLED['task-models'] && (
              <PlaceholderSection id="task-models" icon={Zap} iconClass="text-amber-500" title="按任务分类排行" />
            )}

            {/* SECTION 4: Cost per session（前端按目录售价计算） */}
            {SECTION_ENABLED['cost-session'] && (
              <section id="cost-session" className="scroll-mt-32">
                <SectionHeader
                  icon={DollarSign}
                  iconClass="text-emerald-600"
                  title="单次会话成本"
                  subtitle="按目录售价与典型多轮智能体会话的 Token 画像估算（¥，不含缓存折扣，画像见“统计方法”）"
                >
                  <div className="relative">
                    <select
                      value={costSort}
                      onChange={(e) => setCostSort(e.target.value as 'asc' | 'desc')}
                      className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs"
                    >
                      <option value="asc">成本：由低到高</option>
                      <option value="desc">成本：由高到低</option>
                    </select>
                    <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                </SectionHeader>

                {costRows.length === 0 ? (
                  <EmptyBlock text="模型目录中暂无已标价的模型" />
                ) : (
                  <div className="bg-white border border-gray-200 rounded-2xl overflow-x-auto shadow-xs">
                    <div className="min-w-[640px]">
                      {/* Table Header */}
                      <div className="grid grid-cols-12 px-5 py-3 border-b border-gray-200 text-xs font-semibold text-gray-500 bg-gray-50/80">
                        <div className="col-span-4">模型</div>
                        {sessionProfiles.map((p) => (
                          <div key={p.turns} className="col-span-2 text-center" title={`输入 ${formatTokens(p.input)} / 输出 ${formatTokens(p.output)} tokens`}>
                            {p.turns} 轮会话
                            <div className="text-[10px] font-normal text-gray-400">
                              入 {formatTokens(p.input)} / 出 {formatTokens(p.output)}
                            </div>
                          </div>
                        ))}
                      </div>

                      {/* Rows */}
                      <div className="divide-y divide-gray-100 text-xs">
                        {(showMoreCostModels ? costRows : costRows.slice(0, 10)).map(({ model, costs }) => (
                          <div
                            key={model.id}
                            onClick={() => selectModel(model)}
                            className="grid grid-cols-12 px-5 py-3 items-center hover:bg-purple-50/30 transition-colors cursor-pointer"
                          >
                            <div className="col-span-4 min-w-0">
                              <div className="font-semibold text-gray-900 hover:text-purple-700 truncate">{model.name}</div>
                              <div className="text-[11px] text-gray-400">来自 {model.providerDisplay}</div>
                            </div>
                            {costs.map((c, i) => (
                              <div
                                key={i}
                                className={`col-span-2 text-center font-mono font-medium ${
                                  i < 2 ? 'text-emerald-600' : i === 2 ? 'text-amber-600' : 'text-rose-600 font-semibold'
                                }`}
                              >
                                {c.exceeds ? (
                                  <span className="text-[11px] text-gray-400 font-sans font-normal">超出上下文</span>
                                ) : (
                                  formatCNY(c.cost)
                                )}
                              </div>
                            ))}
                          </div>
                        ))}
                      </div>

                      {costRows.length > 10 && (
                        <div className="p-3 text-center border-t border-gray-100">
                          <button
                            onClick={() => setShowMoreCostModels(!showMoreCostModels)}
                            className="text-xs text-purple-600 hover:text-purple-800 font-medium inline-flex items-center gap-1"
                          >
                            {showMoreCostModels ? '收起部分' : `显示全部 ${costRows.length} 个模型`}
                            {showMoreCostModels ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
                          </button>
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </section>
            )}

            {/* SECTION 5: Market Share（/v1/rankings/authors） */}
            {SECTION_ENABLED['market-share'] && (
              <section id="market-share" className="scroll-mt-32">
                <SectionHeader
                  icon={PieChart}
                  iconClass="text-indigo-600"
                  title="提供商市场份额"
                  subtitle={`各模型厂商在 uFreeTokens 平台上${periodLabel}内的 Token 份额`}
                >
                  <div className="flex items-center gap-1 bg-gray-100 border border-gray-200 p-1 rounded-lg">
                    <button
                      onClick={() => setMarketShareMode('absolute')}
                      disabled={!marketAbsoluteAvailable}
                      title={marketAbsoluteAvailable ? undefined : '当前不公开绝对 Token 数'}
                      className={`px-3 py-1 rounded-md text-xs font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                        effectiveMarketMode === 'absolute' ? 'bg-white text-gray-900 shadow-xs' : 'text-gray-500 hover:text-gray-900'
                      }`}
                    >
                      绝对数值
                    </button>
                    <button
                      onClick={() => setMarketShareMode('percentage')}
                      className={`px-3 py-1 rounded-md text-xs font-medium transition-colors ${
                        effectiveMarketMode === 'percentage' ? 'bg-white text-gray-900 shadow-xs' : 'text-gray-500 hover:text-gray-900'
                      }`}
                    >
                      百分比
                    </button>
                  </div>
                </SectionHeader>

                <StatusBlock
                  state={authorsState}
                  isEmpty={(d) => d.authors.length === 0}
                  emptyText="统计区间内暂无达到上榜门槛的厂商"
                  onRetry={reloadAuthors}
                >
                  {(d) => {
                    const rows = [
                      ...d.authors.map((a, i) => ({
                        key: a.author,
                        label: a.providerDisplay || a.author,
                        sub: `${a.models} 个模型`,
                        share: a.share,
                        tokens: a.tokens,
                        color: AUTHOR_COLORS[i % AUTHOR_COLORS.length]
                      })),
                      ...(d.others && d.others.share > 0
                        ? [{ key: '__others', label: '其他厂商', sub: '', share: d.others.share, tokens: d.others.tokens, color: OTHERS_COLOR }]
                        : [])
                    ];
                    const value = (r: (typeof rows)[number]) =>
                      effectiveMarketMode === 'absolute' && r.tokens !== undefined ? formatTokens(r.tokens) : formatShare(r.share);
                    return (
                      <div className="bg-white border border-gray-200 rounded-2xl p-6 shadow-xs">
                        <div className="text-xs text-gray-500 mb-2 flex justify-between">
                          <span>厂商分布（{periodLabel} Token 份额）</span>
                          {d.totalTokens !== undefined && (
                            <span className="font-mono text-gray-900 font-semibold">总量：{formatTokens(d.totalTokens)} tokens</span>
                          )}
                        </div>

                        <div className="h-6 w-full rounded-lg overflow-hidden flex shadow-inner bg-gray-100">
                          {rows.map((r) => (
                            <div
                              key={r.key}
                              style={{ width: `${r.share * 100}%`, backgroundColor: r.color }}
                              title={`${r.label}: ${formatShare(r.share)}`}
                              className="h-full hover:opacity-80 transition-opacity"
                            />
                          ))}
                        </div>

                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-6 pt-4 border-t border-gray-100">
                          {rows.map((r) => (
                            <div
                              key={r.key}
                              className="flex items-center justify-between p-2.5 rounded-lg bg-gray-50/70 border border-gray-200"
                            >
                              <div className="flex items-center gap-2.5 min-w-0">
                                <span className="w-3 h-3 rounded-full shrink-0" style={{ backgroundColor: r.color }} />
                                <span className="text-xs font-semibold text-gray-900 truncate">{r.label}</span>
                                {r.sub && <span className="text-[11px] text-gray-400 shrink-0">{r.sub}</span>}
                              </div>
                              <span className="text-xs font-mono font-bold text-gray-800 shrink-0">{value(r)}</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    );
                  }}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 6: Benchmarks（前端按目录 scores 中所选指标 × 售价计算） */}
            {SECTION_ENABLED['benchmarks-sec'] && (
              <section id="benchmarks-sec" className="scroll-mt-32">
                <SectionHeader
                  icon={Sliders}
                  iconClass="text-purple-600"
                  title="基准跑分评估"
                  subtitle={
                    <>
                      {scatterMetric.axisLabel}与混合单价的性价比分布 |{' '}
                      <button
                        onClick={onNavigateToBenchmarks}
                        className="text-purple-600 hover:text-purple-800 underline font-medium"
                      >
                        查看完整基准测试
                      </button>
                    </>
                  }
                >
                  <div className="flex flex-wrap items-center gap-3">
                    <select
                      value={scatterMetric.id}
                      onChange={(e) => setScatterMetricId(e.target.value as ScatterMetricId)}
                      aria-label="纵轴评测指标"
                      className="bg-white border border-gray-200 rounded-lg px-2 py-1.5 text-xs text-gray-800 focus:outline-none focus:border-purple-500 shadow-xs cursor-pointer"
                    >
                      {scatterCoverage
                        .filter((c) => c.count > 0 || c.metric.id === scatterMetric.id)
                        .map((c) => (
                          <option key={c.metric.id} value={c.metric.id}>
                            {c.metric.label}（{c.count}）
                          </option>
                        ))}
                    </select>
                    <label className="flex items-center gap-2 text-xs text-gray-600 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={showPareto}
                        onChange={(e) => setShowPareto(e.target.checked)}
                        className="rounded bg-white border-gray-300 text-purple-600 focus:ring-0"
                      />
                      <span>显示帕累托前沿 (Pareto)</span>
                    </label>
                    <div className="relative">
                      <Search className="w-3.5 h-3.5 text-gray-400 absolute left-2.5 top-1/2 -translate-y-1/2" />
                      <input
                        type="text"
                        placeholder="搜索模型..."
                        value={benchmarkSearch}
                        onChange={(e) => setBenchmarkSearch(e.target.value)}
                        className="bg-white border border-gray-200 rounded-lg pl-8 pr-3 py-1.5 text-xs text-gray-800 placeholder-gray-400 focus:outline-none focus:border-purple-500 shadow-xs"
                      />
                    </div>
                  </div>
                </SectionHeader>

                {!scatterAxes ? (
                  <EmptyBlock text={`模型目录中暂无同时具备「${scatterMetric.axisLabel}」成绩与售价的模型`} />
                ) : (
                  <div className="bg-white border border-gray-200 rounded-2xl p-6 shadow-xs">
                    <div className="flex justify-between text-xs text-gray-500 mb-2">
                      <span>{scatterMetric.axisLabel} vs. 混合单价（¥/100万 tokens，对数刻度）</span>
                      <span className="text-[11px] text-purple-600 font-medium">帕累托前沿：同价位下分数最高</span>
                    </div>

                    <div className="relative h-60 w-full bg-gray-50/80 border border-gray-200 rounded-xl p-4 overflow-hidden">
                      <svg className="w-full h-full" viewBox="0 0 500 200">
                        {/* Grid lines + Y Axis Labels */}
                        {scatterAxes.yTicks.map((t, i) => (
                          <g key={`y${i}`}>
                            <line
                              x1="40"
                              y1={scatterAxes.y(t)}
                              x2="480"
                              y2={scatterAxes.y(t)}
                              stroke={i === 0 ? '#cbd5e1' : '#e2e8f0'}
                              strokeDasharray={i === 0 ? undefined : '3 3'}
                            />
                            <text x="10" y={scatterAxes.y(t) + 4} fill="#64748b" fontSize="10">
                              {Math.round(t)}
                            </text>
                          </g>
                        ))}

                        {/* X Axis Labels */}
                        {scatterAxes.xTicks.map((t, i) => (
                          <text key={`x${i}`} x={scatterAxes.x(t) - 12} y="192" fill="#64748b" fontSize="10">
                            ¥{t < 1 ? t.toFixed(2) : t < 100 ? t.toFixed(1) : t.toFixed(0)}
                          </text>
                        ))}

                        {/* Pareto frontier */}
                        {showPareto && paretoPath && (
                          <path d={paretoPath} fill="none" stroke="#9333ea" strokeWidth="2" strokeDasharray="4 4" />
                        )}

                        {/* Scatter points */}
                        {filteredScatter.map((pt) => {
                          const cx = scatterAxes.x(pt.price);
                          const cy = scatterAxes.y(pt.score);
                          const highlight = showPareto && pt.isPareto;
                          return (
                            <g key={pt.model.id} className="cursor-pointer group" onClick={() => selectModel(pt.model)}>
                              <title>{`${pt.model.name}：${scatterMetric.format(pt.score)} / ${formatCNY(pt.price)} 每百万 tokens`}</title>
                              <circle
                                cx={cx}
                                cy={cy}
                                r={highlight ? 6 : 4.5}
                                fill={highlight ? '#9333ea' : '#94a3b8'}
                                stroke="#ffffff"
                                strokeWidth="2"
                                className="transition-transform group-hover:scale-125"
                              />
                              {highlight && (
                                <text
                                  x={cx + 8}
                                  y={cy + 3}
                                  fill="#334155"
                                  fontSize="9"
                                  className="pointer-events-none opacity-80 group-hover:opacity-100 font-medium"
                                >
                                  {pt.model.name.length > 16 ? `${pt.model.name.slice(0, 15)}…` : pt.model.name} ({scatterMetric.format(pt.score)})
                                </text>
                              )}
                            </g>
                          );
                        })}
                      </svg>
                    </div>

                    {/* Ranked List below */}
                    {scatterRanked.length === 0 ? (
                      <div className="mt-4 text-center text-xs text-gray-500">没有匹配的模型</div>
                    ) : (
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 mt-4">
                        {scatterRanked.slice(0, 10).map((m, idx) => (
                          <div
                            key={m.model.id}
                            onClick={() => selectModel(m.model)}
                            className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 cursor-pointer transition-colors"
                          >
                            <div className="flex items-center gap-2 min-w-0">
                              <span className="font-mono text-xs text-gray-400">{idx + 1}.</span>
                              <div className="min-w-0">
                                <div className="text-xs font-semibold text-gray-900 truncate">{m.model.name}</div>
                                <div className="text-[10px] text-gray-500">来自 {m.model.providerDisplay}</div>
                              </div>
                            </div>
                            <div className="text-right shrink-0">
                              <div className="text-xs font-bold font-mono text-purple-600">{scatterMetric.format(m.score)}</div>
                              <div className="text-[10px] text-gray-400">{formatCNY(m.price)}/M</div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}

                    <div className="mt-3 text-[11px] text-gray-400 leading-relaxed">
                      数据来源：{scatterMetric.source}
                      {scatterMetric.id !== 'intelligenceIndex' && '（公开榜单自动导入，取每个模型成绩最好的变体）'}；仅显示同时具备该项成绩与售价的
                      {scatterPoints.length} 个在架模型。
                    </div>
                  </div>
                )}
              </section>
            )}

            {/* SECTION 7: Fastest models（/v1/rankings/speed） */}
            {SECTION_ENABLED['fastest-models'] && (
              <section id="fastest-models" className="scroll-mt-32">
                <SectionHeader
                  icon={Zap}
                  iconClass="text-amber-500"
                  title="最快推理速度"
                  subtitle={`${periodLabel}内各模型流式请求实测输出速度的中位数（tokens/秒）`}
                />

                <StatusBlock
                  state={speedState}
                  isEmpty={(d) => d.models.length === 0}
                  emptyText="统计区间内暂无足够的流式请求样本"
                  onRetry={reloadSpeed}
                >
                  {(d) => (
                    <div className="bg-white border border-gray-200 rounded-2xl overflow-hidden shadow-xs">
                      <div className="divide-y divide-gray-100">
                        {d.models.map((item) => {
                          const target = resolveModel(item);
                          const catalogModel = modelById.get(item.model);
                          return (
                            <div
                              key={item.model}
                              onClick={target ? () => selectModel(target) : undefined}
                              className={`p-4 flex items-center justify-between transition-colors ${
                                target ? 'hover:bg-purple-50/30 cursor-pointer' : ''
                              }`}
                            >
                              <div className="flex items-center gap-4 min-w-0">
                                <span className="font-mono font-bold text-sm text-gray-400 w-5 text-center">{item.rank}</span>
                                <div className="min-w-0">
                                  <div className="flex items-center gap-1.5">
                                    <span className={`text-sm font-semibold truncate ${item.deprecated ? 'text-gray-400' : 'text-gray-900'}`}>
                                      {item.displayName}
                                    </span>
                                    {item.deprecated && <DeprecatedBadge />}
                                  </div>
                                  <div className="text-xs text-gray-400 mt-0.5">来自 {item.providerDisplay || item.author}</div>
                                </div>
                              </div>
                              <div className="text-right shrink-0">
                                <div className="text-sm font-mono font-bold text-amber-600">
                                  {item.tokensPerSecond.toFixed(1)} tok/s
                                </div>
                                {catalogModel && catalogModel.outputPricePerM > 0 && (
                                  <div className="text-xs text-gray-400 font-mono">
                                    输出 {formatCNY(catalogModel.outputPricePerM)}/M
                                  </div>
                                )}
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  )}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 8 / 9: Languages & Programming —— 无数据源，由 SECTION_ENABLED 关闭 */}
            {SECTION_ENABLED['languages-sec'] && (
              <PlaceholderSection id="languages-sec" icon={Globe} iconClass="text-emerald-600" title="多语言表现" />
            )}
            {SECTION_ENABLED['programming-sec'] && (
              <PlaceholderSection id="programming-sec" icon={Code2} iconClass="text-pink-600" title="编程语言生态" />
            )}

            {/* SECTION 10: Context Length（前端按目录 context_window 排名） */}
            {SECTION_ENABLED['context-length'] && (
              <section id="context-length" className="scroll-mt-32">
                <SectionHeader
                  icon={Maximize2}
                  iconClass="text-cyan-600"
                  title="上下文窗口长度"
                  subtitle="按模型目录中的上下文窗口（context window）从长到短排名"
                />

                {contextRows.length === 0 ? (
                  <EmptyBlock text="模型目录中暂无上下文窗口数据" />
                ) : (
                  <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                      {(showMoreContext ? contextRows : contextRows.slice(0, 10)).map((m, idx) => (
                        <div
                          key={m.id}
                          onClick={() => selectModel(m)}
                          className="relative overflow-hidden flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 cursor-pointer transition-colors"
                        >
                          <div
                            className="absolute inset-y-0 left-0 bg-cyan-100/50 pointer-events-none"
                            style={{ width: `${maxContext > 0 ? (m.contextTokens / maxContext) * 100 : 0}%` }}
                          />
                          <div className="relative flex items-center gap-2 min-w-0">
                            <span className="w-5 text-center font-mono text-xs text-gray-400">{idx + 1}</span>
                            <span className="text-xs font-medium text-gray-900 truncate">{m.name}</span>
                          </div>
                          <span className="relative font-mono text-xs font-bold text-cyan-600 shrink-0">
                            {formatContext(m.contextTokens)}
                          </span>
                        </div>
                      ))}
                    </div>
                    {contextRows.length > 10 && (
                      <div className="text-center mt-3">
                        <button
                          onClick={() => setShowMoreContext(!showMoreContext)}
                          className="text-xs text-purple-600 hover:text-purple-800 font-medium inline-flex items-center gap-1"
                        >
                          {showMoreContext ? '收起部分' : `显示全部 ${contextRows.length} 个模型`}
                          {showMoreContext ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
                        </button>
                      </div>
                    )}
                  </div>
                )}
              </section>
            )}

            {/* SECTION 11: Tool Calls（/v1/rankings/tools） */}
            {SECTION_ENABLED['tool-calls'] && (
              <section id="tool-calls" className="scroll-mt-32">
                <SectionHeader
                  icon={Wrench}
                  iconClass="text-orange-600"
                  title="工具调用 (Tool Calls)"
                  subtitle={`${periodLabel}内含工具调用的请求中，各模型的 Token 份额`}
                />
                <StatusBlock
                  state={toolsState}
                  isEmpty={(d) => d.models.length === 0}
                  emptyText="统计区间内暂无达到上榜门槛的工具调用流量"
                  onRetry={reloadTools}
                >
                  {(d) => <ModelShareList data={d} accentClass="text-orange-600" resolveModel={resolveModel} onSelect={selectModel} />}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 12: Images（/v1/rankings/multimodal） */}
            {SECTION_ENABLED['images-sec'] && (
              <section id="images-sec" className="scroll-mt-32">
                <SectionHeader
                  icon={ImageIcon}
                  iconClass="text-teal-600"
                  title="多模态图像处理"
                  subtitle={`${periodLabel}内含图片输入的请求中，各模型的 Token 份额`}
                />
                <StatusBlock
                  state={multimodalState}
                  isEmpty={(d) => d.models.length === 0}
                  emptyText="统计区间内暂无达到上榜门槛的图片输入流量"
                  onRetry={reloadMultimodal}
                >
                  {(d) => <ModelShareList data={d} accentClass="text-teal-600" resolveModel={resolveModel} onSelect={selectModel} />}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 13: Top Apps（/v1/rankings/apps） */}
            {SECTION_ENABLED['top-apps'] && (
              <section id="top-apps" className="scroll-mt-32">
                <SectionHeader
                  icon={Smartphone}
                  iconClass="text-purple-600"
                  title="热门应用与客户端"
                  subtitle={
                    <>
                      通过 <code className="font-mono text-gray-700">X-Title</code> 请求头主动声明身份的应用，按{periodLabel}内 Token 份额排名
                    </>
                  }
                />

                <StatusBlock
                  state={appsState}
                  isEmpty={(d) => d.apps.length === 0}
                  emptyText="统计区间内暂无达到上榜门槛的应用"
                  onRetry={reloadApps}
                >
                  {(d) => (
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                      {d.apps.map((app) => {
                        const host = app.appUrl ? safeHost(app.appUrl) : null;
                        return (
                          <div
                            key={`${app.rank}-${app.appName}`}
                            className="flex items-center justify-between p-3.5 bg-white border border-gray-200 rounded-xl hover:bg-purple-50/30 transition-colors shadow-xs"
                          >
                            <div className="flex items-center gap-3 min-w-0">
                              <span className="font-mono font-bold text-sm text-gray-400 w-5 text-center">{app.rank}</span>
                              <div className="min-w-0">
                                <div className="text-sm font-semibold text-gray-900 truncate">{app.appName}</div>
                                {host ? (
                                  <a
                                    href={app.appUrl}
                                    target="_blank"
                                    rel="noopener noreferrer nofollow"
                                    className="text-xs text-gray-500 hover:text-purple-600 inline-flex items-center gap-0.5"
                                  >
                                    {host}
                                    <ExternalLink className="w-3 h-3" />
                                  </a>
                                ) : (
                                  <div className="text-xs text-gray-400">未声明网址</div>
                                )}
                              </div>
                            </div>
                            <div className="text-right shrink-0">
                              <div className="text-xs font-mono font-bold text-purple-600">
                                {formatShare(app.share)}
                                {app.tokens !== undefined && (
                                  <span className="text-gray-400 font-normal ml-1">· {formatTokens(app.tokens)} tokens</span>
                                )}
                              </div>
                              <ChangeTag change={app.change} />
                            </div>
                          </div>
                        );
                      })}
                      {d.others && d.others.share > 0 && (
                        <div className="flex items-center justify-between p-3.5 bg-gray-50/70 border border-dashed border-gray-200 rounded-xl">
                          <div className="flex items-center gap-3">
                            <span className="font-mono font-bold text-sm text-gray-300 w-5 text-center">—</span>
                            <div>
                              <div className="text-sm font-semibold text-gray-500">其他应用</div>
                              <div className="text-xs text-gray-400">未单独上榜的已声明应用合计</div>
                            </div>
                          </div>
                          <div className="text-xs font-mono font-bold text-gray-500">
                            {formatShare(d.others.share)}
                            {d.others.tokens !== undefined && (
                              <span className="text-gray-400 font-normal ml-1">· {formatTokens(d.others.tokens)} tokens</span>
                            )}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </StatusBlock>
              </section>
            )}

            {/* SECTION 14: How these rankings are measured —— 与后端公开口径（技术方案 §3.4 / §8）保持一致 */}
            {SECTION_ENABLED.measurement && (
              <section id="measurement" className="scroll-mt-32">
                <div className="mb-5 border-b border-gray-200 pb-4">
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <HelpCircle className="w-5 h-5 text-gray-400" />
                    榜单统计方法与规则
                  </h2>
                </div>

                <div className="bg-white border border-gray-200 rounded-2xl p-6 space-y-6 text-xs leading-relaxed text-gray-700 shadow-xs">
                  <div>
                    <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-purple-600" />
                      统计指标与范围
                    </h3>
                    <p className="text-gray-500">
                      用量类榜单只统计<span className="text-gray-700 font-medium">成功请求</span>的输入 + 输出 Token，失败请求不计入排名。输出
                      Token 取自上游返回的 completion_tokens，已经包含推理（reasoning）Token，不会重复计算。
                      {methodology?.excludesInternal !== false && '标记为内部、测试或评测用途的账户流量不计入。'}
                      只列出公开模型目录中可见的模型；已下架模型在其仍有用量的历史周期内照常计入，但标注“已下架”且不可点击。
                    </p>
                  </div>

                  <div>
                    <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                      统计周期与时区
                    </h3>
                    <p className="text-gray-500">
                      统计日按 Asia/Shanghai（东八区）自然日切分。日榜、周榜、月榜都由截至昨天的完整日组成，不含当天的不完整数据，区间为起始日（含）至结束日（不含）
                      {usageMeta ? `；当前${periodLabel}覆盖 ${usageMeta.from} 至 ${addDays(usageMeta.to, -1)}` : ''}。
                      环比 = 本期 ÷ 上期 − 1，上期为 0 时标记为“新上榜”。
                    </p>
                  </div>

                  <div>
                    <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-indigo-600" />
                      隐私保护与防刷规则
                    </h3>
                    <p className="text-gray-500">
                      模型、厂商或应用在统计周期内需有至少 {minAccountsText}独立账户调用才单独上榜，否则归入“其他”，避免从榜单反推单个客户的用量。
                      单个账户对某模型的计入量不超过该模型当期总量的 {maxShareText}，超出部分不参与排名（原始数据不删除）。
                      {methodology?.showsAbsolute
                        ? '当前同时公开份额与绝对 Token 数。'
                        : '当前只公开份额（%）与排名，不展示绝对 Token 数。'}
                      份额 = 该项 Token ÷ 统计周期内全平台（或对应细分榜单）计入的 Token 总量。
                    </p>
                  </div>

                  <div>
                    <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-amber-500" />
                      细分榜单口径
                    </h3>
                    <ul className="text-gray-500 space-y-1 list-disc pl-4">
                      <li>
                        最快推理速度取各请求"输出 Token ÷ 生成耗时（秒）"的中位数（P50），只统计成功的<span className="text-gray-700 font-medium">流式</span>
                        请求，生成耗时从首个 Token 到最后一个分片，每个模型至少 10 个样本；非流式请求无法区分排队与生成耗时，不参与统计。不展示上游渠道。
                      </li>
                      <li>工具调用 / 多模态图像处理榜只统计含工具调用 / 含图片输入的请求，份额以该类请求的 Token 总量为分母。</li>
                      <li>
                        热门应用只包含通过请求头 <code className="font-mono text-gray-700">X-Title</code>（可选{' '}
                        <code className="font-mono text-gray-700">HTTP-Referer</code>）主动声明身份的应用，同样需要至少 {minAccountsText}独立账户。
                      </li>
                    </ul>
                  </div>

                  <div>
                    <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-cyan-600" />
                      基于模型目录计算的板块
                    </h3>
                    <ul className="text-gray-500 space-y-1 list-disc pl-4">
                      <li>
                        基准跑分评估：纵轴可选运营录入的综合智能指数，或从公开评测榜单自动导入的成绩（LMArena Elo、GPQA Diamond、
                        SWE-bench Verified、Epoch ECI 等，数据分别来自 LMArena 与 Epoch AI，均为 CC BY 4.0），横轴为混合单价 = (3 × 输入单价 + 输出单价) ÷ 4（¥/百万 Token）；
                        没有该项成绩或未录入售价的模型不显示。帕累托前沿上的模型不存在“更便宜且分数更高”的其他模型。
                      </li>
                      <li>
                        单次会话成本：按目录售价计算，不含缓存折扣。假设第 1 轮输入 {SESSION_PROFILE.firstTurnInput.toLocaleString()} Token
                        （系统提示词、工具定义与首个请求），之后每轮重发完整上下文、上下文每轮增长 {SESSION_PROFILE.contextGrowthPerTurn.toLocaleString()} Token，
                        每轮输出 {SESSION_PROFILE.outputPerTurn.toLocaleString()} Token；最后一轮所需上下文超过模型上下文窗口时标记为“超出上下文”。
                      </li>
                      <li>上下文窗口：取模型目录中的 context window。以上三项均不含已下架模型。</li>
                    </ul>
                  </div>

                  <div>
                    <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                      <Info className="w-4 h-4 text-amber-500" />
                      榜单客观性声明
                    </h3>
                    <p className="text-gray-500">
                      实际调用热度与流行度并不等同于特定基准测试的跑分能力。选型时建议综合考量模型的性价比、上下文窗口、响应速度以及在特定细分领域的适配表现。
                    </p>
                  </div>
                </div>
              </section>
            )}
          </main>
        </div>
      </div>

      {/* Footer */}
      <footer className="mt-24 border-t border-gray-200 bg-gray-50 py-12 px-4 md:px-8 text-xs text-gray-500">
        <div className="max-w-7xl mx-auto flex flex-col md:flex-row items-center justify-between gap-6">
          <div className="flex items-center gap-2">
            <span className="font-bold text-gray-900 text-sm">uFreeTokens 排行榜</span>
            <span className="text-gray-300">|</span>
            <span>真实调用数据，匿名聚合统计</span>
          </div>
          <div className="flex flex-wrap items-center gap-6">
            <button onClick={onNavigateToModels} className="hover:text-gray-900">模型列表</button>
            <button onClick={onNavigateToBenchmarks} className="hover:text-gray-900">基准测试</button>
            <a href="#top-models" className="hover:text-gray-900">热门模型</a>
            <a href="#cost-session" className="hover:text-gray-900">成本矩阵</a>
            <span className="text-gray-400">© 2026 uFreeTokens. 保留所有权利.</span>
          </div>
        </div>
      </footer>
    </div>
  );
};
