import React, { useEffect, useMemo, useState } from 'react';
import { Link, useParams } from 'react-router';
import {
  Code2,
  ExternalLink,
  Bot,
  Image as ImageIcon,
  Sparkles,
  FlaskConical,
  Globe,
  ChevronRight,
  ChevronLeft,
  Zap,
  DollarSign,
  Award,
  Check,
  Copy,
  X,
  Info,
  AlertCircle,
  Loader2,
  Layers,
  Languages,
  Binary,
} from 'lucide-react';
import { Model } from '../types';
import { GATEWAY_BASE_URL } from '../api/client';
import { ApiError } from '../api/errors';
import {
  BENCHMARK_CATEGORIES,
  BENCHMARK_CATEGORY_LABELS,
  BenchmarkCategory,
  BenchmarkChampion,
  BenchmarkDetail,
  BenchmarkResult,
  BenchmarkSummary,
  formatBenchmarkScore,
  getBenchmark,
  listBenchmarks,
} from '../api/benchmarks';

// 基准测试页（技术方案阶段 2）：列表读 GET /v1/benchmarks，详情是
// /benchmarks/:slug 路由读 GET /v1/benchmarks/{slug}，链接可直接分享。
// 页面上不再保留任何写死的基准数据——接口失败时展示错误/空状态，绝不回落
// 到 mock，避免把编造的分数当成真实评测结果展示给用户。

interface BenchmarksPageProps {
  onNavigateToModels: () => void;
  // allModels 是 App 传入的真实目录模型（baseModels），只用来给已上架的参评
  // 模型补一个厂商名，分数本身完全来自基准接口。
  allModels?: Model[];
}

// ---------- 展示用格式化 ----------

// 和 App.tsx 的 modelPath 保持一致：模型 ID 逐段编码后拼进 /models/*。
function modelPath(id: string): string {
  return `/models/${id.split('/').map(encodeURIComponent).join('/')}`;
}

// 按 metric_unit 格式化：elo 取整带 "Elo"，percent 一位小数加 %，index /
// score 一位小数（实现在 api/benchmarks.ts，模型详情页的评测成绩共用）。
const formatScore = formatBenchmarkScore;

function extraNumber(extra: Record<string, unknown> | null, key: string): number | null {
  const v = extra?.[key];
  if (typeof v === 'number' && Number.isFinite(v)) return v;
  if (typeof v === 'string' && v.trim() !== '' && Number.isFinite(Number(v))) return Number(v);
  return null;
}

function extraString(extra: Record<string, unknown> | null, key: string): string | null {
  const v = extra?.[key];
  return typeof v === 'string' && v.trim() !== '' ? v : null;
}

// LMArena 榜单的 extra 带 95% 置信区间上下界（ci_high / ci_low），展示成
// 相对分数的 "+x/−y"；没有时返回 null。
function formatCI(row: BenchmarkResult): string | null {
  const low = extraNumber(row.extra, 'ci_low');
  const high = extraNumber(row.extra, 'ci_high');
  if (low === null || high === null) return null;
  const plus = Math.max(0, high - row.score);
  const minus = Math.max(0, row.score - low);
  return `+${Math.round(plus)}/−${Math.round(minus)}`;
}

// 许可协议的展示文字：CC-BY-4.0 → CC BY 4.0。
function formatLicense(license: string | null): string | null {
  if (!license) return null;
  return /^CC-/i.test(license) ? license.replace(/-/g, ' ') : license;
}

// 成本是 cost_currency 下的整数微单位（1e-6 元 / 美元）。常规金额保留 4 位
// 小数（$0.0123），更小的金额改用 2 位有效数字，避免一律显示成 $0.0000。
function formatCost(micro: number | null, currency: string | undefined): string {
  if (micro === null) return '—';
  const symbol = currency === 'CNY' ? '¥' : currency === 'USD' ? '$' : '';
  const value = micro / 1_000_000;
  const text = value === 0 || value >= 0.0001 ? value.toFixed(4) : value.toPrecision(2);
  return symbol ? `${symbol}${text}` : `${text} ${currency ?? ''}`.trim();
}

function formatDuration(ms: number | null): string {
  if (ms === null) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60_000).toFixed(1)}m`;
}

function formatErrorRate(rate: number | null): string {
  if (rate === null) return '—';
  return `${(rate * 100).toFixed(2)}%`;
}

function formatDate(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' });
}

// 网关地址：前后端分开部署时取 VITE_GATEWAY_BASE_URL，同源部署时就是当前站点。
function gatewayBase(): string {
  return GATEWAY_BASE_URL || window.location.origin;
}

function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'service_unavailable' || err.status === 503) return '基准测试数据维护中，请稍后再来。';
    return err.message;
  }
  return '网络异常，暂时无法获取基准测试数据。';
}

// ---------- 分类元信息 ----------

const categoryTabIcons: Record<BenchmarkCategory, React.ReactNode> = {
  general: <Layers className="w-3.5 h-3.5" />,
  coding: <Code2 className="w-3.5 h-3.5" />,
  agents: <Bot className="w-3.5 h-3.5" />,
  reasoning: <FlaskConical className="w-3.5 h-3.5" />,
  chinese: <Languages className="w-3.5 h-3.5" />,
  search: <Globe className="w-3.5 h-3.5" />,
  media: <ImageIcon className="w-3.5 h-3.5" />,
  artifacts: <Sparkles className="w-3.5 h-3.5" />,
  embedding: <Binary className="w-3.5 h-3.5" />,
};

const categoryMeta: Record<BenchmarkCategory, { title: string; icon: React.ReactNode; subtitle?: string }> = {
  general: {
    title: '综合能力',
    icon: <Layers className="w-4 h-4 text-indigo-600" />,
    subtitle: '跨任务的综合排名：LMArena 真人盲测 Elo 与 Epoch AI 跨基准能力指数等。',
  },
  coding: {
    title: '编程',
    icon: <Code2 className="w-4 h-4 text-sky-600" />,
    subtitle: '真实代码仓库修复、多语言编程练习与编程类对话盲测。',
  },
  chinese: {
    title: '中文',
    icon: <Languages className="w-4 h-4 text-rose-600" />,
    subtitle: '中文提示与中文场景下的模型表现。',
  },
  embedding: {
    title: '向量与检索嵌入',
    icon: <Binary className="w-4 h-4 text-teal-600" />,
    subtitle: '文本向量模型在检索、聚类、语义相似度等任务上的表现。',
  },
  agents: {
    title: '智能体与工具调用',
    icon: <Bot className="w-4 h-4 text-emerald-600" />,
    subtitle: '评估模型在多步自主决策、API 工具集成及复杂业务环境中的表现。',
  },
  media: {
    title: '多模态（视觉 / 图像 / 音频）',
    icon: <ImageIcon className="w-4 h-4 text-amber-600" />,
    subtitle: '图像理解与生成、视频与语音等多模态任务的表现。',
  },
  artifacts: {
    title: '生成 / 创作（网页、应用与工件）',
    icon: <Sparkles className="w-4 h-4 text-blue-600" />,
    subtitle: '一次性生成可运行网页、交互式应用、矢量图形等工件的能力。',
  },
  reasoning: {
    title: '推理',
    icon: <FlaskConical className="w-4 h-4 text-purple-600" />,
    subtitle: '聚焦高难度学术级抗检索推导、数学证明、形式化逻辑与深层归纳。',
  },
  search: {
    title: '搜索与深度调研',
    icon: <Globe className="w-4 h-4 text-cyan-600" />,
    subtitle: '针对海量长尾事实检索、聚合型问答及实时多步骤网页分析能力评测。',
  },
};

// ---------- 小组件 ----------

// 三项冠军的计算口径，列表页和详情页都展示，和后端接口层的算法保持一致。
const ChampionRulesNote: React.FC = () => (
  <div className="flex items-start space-x-2 p-3 bg-gray-50 rounded-lg border border-gray-200 text-[11px] text-gray-600 leading-relaxed">
    <Info className="w-3.5 h-3.5 text-gray-400 shrink-0 mt-0.5" />
    <div>
      <span className="font-semibold text-gray-800">冠军评选规则：</span>
      <span className="text-emerald-700 font-medium">最高质量</span> = 得分最好的模型；
      <span className="text-purple-700 font-medium">最高性价比</span> = 得分不低于全体参评模型中位数的模型里，单题成本最低者；
      <span className="text-amber-700 font-medium">最快响应</span> = 得分不低于中位数的模型里，平均耗时最短者。没有成本或耗时数据的模型不参与对应评选。
    </div>
  </div>
);

// OriginBadge 标注数据来源：平台实测（self_eval）与外部录入的公开结果口径不同，
// 成本数字尤其不能直接和平台售价对比（技术方案 §4 第 5 点）。
const OriginBadge: React.FC<{ origin: string | undefined }> = ({ origin }) => {
  if (!origin) return null;
  return origin === 'self_eval' ? (
    <span className="px-1.5 py-0.5 rounded bg-emerald-50 text-emerald-700 border border-emerald-200 text-[10px] font-semibold">
      平台实测
    </span>
  ) : (
    <span className="px-1.5 py-0.5 rounded bg-gray-100 text-gray-600 border border-gray-200 text-[10px] font-semibold">
      外部数据
    </span>
  );
};

const SourceLink: React.FC<{ name: string | null; url: string | null }> = ({ name, url }) => {
  if (!name && !url) return null;
  const label = name ?? url ?? '';
  return url ? (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      onClick={(e) => e.stopPropagation()}
      className="inline-flex items-center space-x-0.5 text-purple-600 hover:text-purple-800 hover:underline"
    >
      <span>{label}</span>
      <ExternalLink className="w-2.5 h-2.5" />
    </a>
  ) : (
    <span>{label}</span>
  );
};

// SourceAttribution：外部数据的署名 + 许可协议。sourceName 本身已带许可
// 文字（如 "..., CC BY 4.0"）时不再重复展示许可标签。
const SourceAttribution: React.FC<{ name: string | null; url: string | null; license: string | null; prefix?: string }> = ({
  name,
  url,
  license,
  prefix = '来源',
}) => {
  if (!name && !url && !license) return null;
  const licenseText = formatLicense(license);
  const showLicense = !!licenseText && !(name ?? '').toLowerCase().includes(licenseText.toLowerCase());
  return (
    <span>
      {(name || url) && (
        <>
          {prefix} <SourceLink name={name} url={url} />
        </>
      )}
      {showLicense && (
        <span className="ml-1 px-1 py-px rounded border border-gray-200 bg-gray-50 text-gray-500 text-[9px] font-semibold">
          {licenseText}
        </span>
      )}
    </span>
  );
};

// ModelName：参评模型在平台上架（model 非空）时链接到模型详情页，否则只显示
// 展示名——平台没上架的模型也能参评，但不能假装它可以点进去调用。
const ModelName: React.FC<{ label: string; model: string | null; className?: string }> = ({ label, model, className = '' }) =>
  model ? (
    <Link
      to={modelPath(model)}
      onClick={(e) => e.stopPropagation()}
      className={`hover:text-purple-700 hover:underline ${className}`}
      title={model}
    >
      {label}
    </Link>
  ) : (
    <span className={className}>{label}</span>
  );

const ChampionCell: React.FC<{
  champion: BenchmarkChampion | null;
  kind: 'quality' | 'value' | 'speed';
  bench: BenchmarkSummary;
}> = ({ champion, kind, bench }) => {
  if (!champion) return <span className="text-[11px] text-gray-300">—</span>;
  const main =
    kind === 'quality'
      ? formatScore(champion.score, bench.metricUnit)
      : kind === 'value'
        ? formatCost(champion.costPerTaskMicro, bench.run?.costCurrency)
        : formatDuration(champion.avgDurationMs);
  const marker =
    kind === 'quality' ? (
      <span className="text-emerald-600">✦</span>
    ) : kind === 'value' ? (
      <span className="text-purple-600 font-mono text-[9px]">❖</span>
    ) : (
      <span className="text-amber-600 font-mono text-[9px]">⚡</span>
    );
  return (
    <div className="space-y-1">
      <div className="text-sm font-extrabold text-gray-900 font-mono">
        {main}
        {kind === 'value' && champion.costPerTaskMicro !== null && (
          <span className="text-[10px] font-normal text-gray-400"> / 题</span>
        )}
      </div>
      <div className="text-[10px] text-gray-500 flex items-center space-x-1 truncate max-w-[160px]">
        {marker}
        <ModelName label={champion.modelLabel} model={champion.model} className="truncate" />
      </div>
    </div>
  );
};

const LoadingState: React.FC = () => (
  <div className="py-20 flex flex-col items-center justify-center text-gray-400 space-y-2">
    <Loader2 className="w-5 h-5 animate-spin" />
    <div className="text-xs">加载中...</div>
  </div>
);

const ErrorState: React.FC<{ message: string; onRetry?: () => void }> = ({ message, onRetry }) => (
  <div className="py-20 flex flex-col items-center justify-center text-center space-y-2">
    <AlertCircle className="w-5 h-5 text-gray-300" />
    <div className="text-xs font-medium text-gray-700">{message}</div>
    {onRetry && (
      <button
        onClick={onRetry}
        className="mt-1 px-3 py-1 text-xs bg-purple-50 text-purple-700 rounded-md hover:bg-purple-100 font-medium transition-colors cursor-pointer"
      >
        重试
      </button>
    )}
  </div>
);

// 页脚：订阅周刊表单已移除——没有后端接口，提交后只是前端提示成功，等于
// 收集了邮箱却没落库（技术方案 §8.6）。
const BenchmarksFooter: React.FC<{ onNavigateToModels: () => void }> = ({ onNavigateToModels }) => (
  <footer className="border-t border-gray-200 bg-white text-gray-600 text-xs mt-12">
    <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-12">
      <div className="grid grid-cols-2 md:grid-cols-4 gap-8">
        {/* Col 1: Brand */}
        <div className="col-span-2 md:col-span-1 space-y-3">
          <div className="flex items-center space-x-1.5 font-bold text-gray-900">
            <div className="w-4 h-4 rounded bg-purple-600 flex items-center justify-center text-white text-[10px]">
              ▲
            </div>
            <span className="text-sm font-semibold tracking-tight">uFreeTokens</span>
          </div>
          <p className="text-[11px] text-gray-400 leading-relaxed">© 2026 uFreeTokens, Inc.</p>
        </div>

        {/* Col 2: Product */}
        <div className="space-y-2">
          <h4 className="font-semibold text-gray-900 text-xs">产品与服务</h4>
          <ul className="space-y-1.5 text-gray-500 text-[11px]">
            <li>
              <button onClick={onNavigateToModels} className="hover:text-purple-600 transition-colors cursor-pointer">
                模型集市
              </button>
            </li>
            <li>
              <Link to="/rankings" className="hover:text-purple-600 transition-colors">
                排行榜
              </Link>
            </li>
            <li>
              <Link to="/benchmarks" className="font-semibold text-purple-600">
                基准评测
              </Link>
            </li>
          </ul>
        </div>

        {/* Col 3: Developer */}
        <div className="space-y-2">
          <h4 className="font-semibold text-gray-900 text-xs">开发者生态</h4>
          <ul className="space-y-1.5 text-gray-500 text-[11px]">
            <li>
              <Link to="/docs" className="hover:text-purple-600 transition-colors">
                开发文档
              </Link>
            </li>
            <li>
              <Link to="/docs/zh/public-data" className="hover:text-purple-600 transition-colors">
                排行榜与基准测试 API
              </Link>
            </li>
          </ul>
        </div>
      </div>
    </div>
  </footer>
);

// ---------- 列表页 /benchmarks ----------

export const BenchmarksPage: React.FC<BenchmarksPageProps> = ({ onNavigateToModels }) => {
  const [selectedCategory, setSelectedCategory] = useState<'all' | BenchmarkCategory>('all');
  const [benchmarks, setBenchmarks] = useState<BenchmarkSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [isApiModalOpen, setIsApiModalOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    // 一次取全部分类、前端按分类过滤：这样才知道哪些分类真的有基准，只为
    // 有数据的分类显示标签。
    listBenchmarks(undefined, controller.signal)
      .then((data) => setBenchmarks(data))
      .catch((err) => {
        if (controller.signal.aborted) return;
        setBenchmarks([]);
        setError(errorMessage(err));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [reloadKey]);

  // 只为有已发布基准的分类显示标签；后端新增了前端还不认识的分类时也照样
  // 显示（排在已知分类之后，标签直接用分类名）。
  const availableCategories = useMemo(() => {
    const present = new Set<string>(benchmarks.map((b) => b.category));
    const known = BENCHMARK_CATEGORIES.filter((c) => present.has(c));
    const unknown = [...present].filter((c) => !(BENCHMARK_CATEGORIES as string[]).includes(c)) as BenchmarkCategory[];
    return [...known, ...unknown];
  }, [benchmarks]);

  const categoryTabs: { id: 'all' | BenchmarkCategory; label: string; icon?: React.ReactNode }[] = [
    { id: 'all', label: '全部' },
    ...availableCategories.map((c) => ({
      id: c,
      label: BENCHMARK_CATEGORY_LABELS[c] ?? c,
      icon: categoryTabIcons[c],
    })),
  ];

  // 选中的分类在重新加载后没有数据了，就回到「全部」。
  const effectiveCategory: 'all' | BenchmarkCategory =
    selectedCategory !== 'all' && !availableCategories.includes(selectedCategory) ? 'all' : selectedCategory;

  // Group benchmarks by category for section rendering
  const categoriesToRender: BenchmarkCategory[] = effectiveCategory === 'all' ? availableCategories : [effectiveCategory];

  // 头部统计只用接口返回的真实数据：基准数量与最近一次运行时间。
  const latestRunAt = useMemo(() => {
    const times = benchmarks.map((b) => b.run?.runAt).filter((t): t is string => !!t).sort();
    return times.length > 0 ? times[times.length - 1] : undefined;
  }, [benchmarks]);

  const curlExample = `curl ${gatewayBase()}/v1/benchmarks`;

  const handleCopyCode = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="min-h-screen bg-white text-gray-900 flex flex-col font-sans select-text">
      {/* Top Main Benchmarks Header */}
      <div className="max-w-6xl mx-auto w-full px-4 sm:px-6 lg:px-8 pt-8 pb-4">
        <div className="flex flex-col md:flex-row md:items-start justify-between gap-6 pb-6 border-b border-gray-100">
          <div className="space-y-2 max-w-2xl">
            <h1 className="text-2xl sm:text-3xl font-bold text-gray-900 tracking-tight">基准测试</h1>
            <p className="text-xs text-gray-500 leading-relaxed">
              汇总各项基准评测中模型的质量得分、单题成本与响应耗时。每个基准都标注数据来源与评测批次：
              「平台实测」由 uFreeTokens 通过自家网关实际调用得出，「外部数据」引用自公开评测来源。
            </p>
            {!loading && !error && (
              <div className="text-[11px] text-gray-400 flex items-center space-x-2 pt-1 font-mono">
                <span>
                  <span className="font-semibold text-gray-700">{benchmarks.length}</span> 项基准评测
                </span>
                {latestRunAt && (
                  <>
                    <span>•</span>
                    <span>最近运行时间 {formatDate(latestRunAt)}</span>
                  </>
                )}
              </div>
            )}
          </div>

          {/* Right Benchmarks API Button Card */}
          <button
            onClick={() => setIsApiModalOpen(true)}
            className="flex items-center space-x-2.5 p-3 rounded-xl border border-gray-200 hover:border-purple-300 hover:bg-purple-50/40 transition-all text-left group shrink-0 bg-white shadow-xs cursor-pointer"
          >
            <div className="w-7 h-7 rounded-lg bg-gray-100 group-hover:bg-purple-100 text-gray-700 group-hover:text-purple-700 flex items-center justify-center font-mono text-xs font-bold transition-colors">
              {'</>'}
            </div>
            <div>
              <div className="text-xs font-bold text-gray-900 group-hover:text-purple-700 flex items-center space-x-1">
                <span>基准测试 API</span>
                <ExternalLink className="w-3 h-3 text-gray-400 group-hover:text-purple-600" />
              </div>
              <div className="text-[10px] text-gray-400 mt-0.5">通过公开 API 获取最新基准评测数据</div>
            </div>
          </button>
        </div>

        {/* Filter Pills */}
        <div className="flex flex-wrap items-center gap-2 pt-4 pb-2 text-xs">
          {categoryTabs.map((tab) => {
            const active = effectiveCategory === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => setSelectedCategory(tab.id)}
                className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-full border text-xs font-medium transition-colors cursor-pointer ${
                  active
                    ? 'bg-gray-900 text-white border-gray-900 shadow-xs'
                    : 'bg-white text-gray-600 border-gray-200 hover:bg-gray-50'
                }`}
              >
                {tab.icon && <span>{tab.icon}</span>}
                <span>{tab.label}</span>
                {tab.id !== 'all' && (
                  <span className={`text-[10px] font-mono ${active ? 'text-gray-300' : 'text-gray-400'}`}>
                    {benchmarks.filter((b) => b.category === tab.id).length}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      </div>

      {/* Main Content: Categories & Benchmark Lists */}
      <div className="max-w-6xl mx-auto w-full px-4 sm:px-6 lg:px-8 py-4 space-y-10 flex-1">
        {loading ? (
          <LoadingState />
        ) : error ? (
          <ErrorState message={error} onRetry={() => setReloadKey((k) => k + 1)} />
        ) : benchmarks.length === 0 ? (
          <div className="py-20 text-center space-y-1">
            <div className="text-xs font-medium text-gray-700">暂无已发布的基准测试</div>
            <div className="text-[11px] text-gray-400">评测结果发布后会显示在这里。</div>
          </div>
        ) : (
          <>
            {benchmarks.some((b) => b.champions.value || b.champions.speed) && <ChampionRulesNote />}

            {categoriesToRender.map((catKey) => {
              const catBenchmarks = benchmarks.filter((b) => b.category === catKey);
              if (catBenchmarks.length === 0) return null;
              const meta = categoryMeta[catKey] ?? {
                title: BENCHMARK_CATEGORY_LABELS[catKey] ?? catKey,
                icon: <Award className="w-4 h-4 text-gray-500" />,
              };
              // 很多导入的公开榜单没有成本 / 耗时数据，整个分类都没有对应冠军时
              // 直接隐藏这一列，而不是显示一整列 "—"。
              const showValue = catBenchmarks.some((b) => b.champions.value !== null);
              const showSpeed = catBenchmarks.some((b) => b.champions.speed !== null);

              return (
                <section key={catKey} className="space-y-3">
                  {/* Category Header */}
                  <div className="space-y-1">
                    <div className="flex items-center space-x-2">
                      {meta.icon}
                      <h2 className="text-sm sm:text-base font-bold text-gray-900">{meta.title}</h2>
                    </div>
                    {meta.subtitle && <p className="text-xs text-gray-500 leading-relaxed">{meta.subtitle}</p>}
                  </div>

                  {/* Table Container */}
                  <div className="border border-gray-200 rounded-xl overflow-x-auto bg-white shadow-xs">
                    <table className="w-full text-left text-xs border-collapse">
                      <thead>
                        <tr className="bg-gray-50/70 border-b border-gray-200 text-[10px] text-gray-400 font-semibold uppercase tracking-wider">
                          <th className="py-2.5 px-4 font-medium w-2/5">基准测试项</th>
                          <th className="py-2.5 px-4 font-medium">
                            <span className="flex items-center space-x-1 text-emerald-700">
                              <Award className="w-3 h-3" />
                              <span>最高质量</span>
                            </span>
                          </th>
                          {showValue && (
                            <th className="py-2.5 px-4 font-medium">
                              <span className="flex items-center space-x-1 text-purple-700">
                                <DollarSign className="w-3 h-3" />
                                <span>最高性价比</span>
                              </span>
                            </th>
                          )}
                          {showSpeed && (
                            <th className="py-2.5 px-4 font-medium">
                              <span className="flex items-center space-x-1 text-amber-700">
                                <Zap className="w-3 h-3" />
                                <span>最快响应</span>
                              </span>
                            </th>
                          )}
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100">
                        {catBenchmarks.map((bench) => (
                          <tr key={bench.slug} className="hover:bg-purple-50/30 transition-colors group">
                            {/* 1. Benchmark Name & Info */}
                            <td className="py-3.5 px-4 align-top">
                              <div className="space-y-1 pr-2">
                                <Link
                                  to={`/benchmarks/${encodeURIComponent(bench.slug)}`}
                                  className="font-bold text-gray-900 group-hover:text-purple-700 flex items-center space-x-1 transition-colors"
                                >
                                  <span>{bench.name}</span>
                                  <ChevronRight className="w-3.5 h-3.5 text-gray-400 group-hover:text-purple-600 transition-transform group-hover:translate-x-0.5" />
                                </Link>
                                {bench.description && (
                                  <p className="text-[11px] text-gray-500 leading-snug line-clamp-2">{bench.description}</p>
                                )}
                                <div className="text-[10px] text-gray-400 font-mono pt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-1">
                                  <OriginBadge origin={bench.run?.origin} />
                                  <span>{bench.modelsCount} 个评测模型</span>
                                  {bench.run && <span>• 更新于 {formatDate(bench.run.runAt)}</span>}
                                  {(bench.sourceName || bench.sourceUrl || bench.license) && (
                                    <span>
                                      •{' '}
                                      <SourceAttribution name={bench.sourceName} url={bench.sourceUrl} license={bench.license} />
                                    </span>
                                  )}
                                </div>
                              </div>
                            </td>

                            {/* 2-4. Champions */}
                            <td className="py-3.5 px-4 align-top">
                              <ChampionCell champion={bench.champions.quality} kind="quality" bench={bench} />
                            </td>
                            {showValue && (
                              <td className="py-3.5 px-4 align-top">
                                <ChampionCell champion={bench.champions.value} kind="value" bench={bench} />
                              </td>
                            )}
                            {showSpeed && (
                              <td className="py-3.5 px-4 align-top">
                                <ChampionCell champion={bench.champions.speed} kind="speed" bench={bench} />
                              </td>
                            )}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              );
            })}
          </>
        )}

        {/* Footer Sub-links */}
        <div className="pt-4 pb-2 text-xs text-gray-500 text-center">
          如需查看基于调用量权重的同类模型视图，请参阅{' '}
          <Link to="/rankings" className="text-purple-600 hover:text-purple-800 font-medium underline">
            模型排行榜
          </Link>{' '}
          以及{' '}
          <button
            onClick={onNavigateToModels}
            className="text-purple-600 hover:text-purple-800 font-medium underline cursor-pointer"
          >
            完整模型列表
          </button>
          。
        </div>
      </div>

      {/* Benchmarks API Documentation Modal */}
      {isApiModalOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-xs"
          onClick={() => setIsApiModalOpen(false)}
        >
          <div
            className="bg-white rounded-2xl shadow-2xl max-w-xl w-full border border-gray-200 overflow-hidden"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="p-4 border-b border-gray-200 flex items-center justify-between">
              <div className="flex items-center space-x-2">
                <Code2 className="w-4 h-4 text-purple-600" />
                <h3 className="font-bold text-sm text-gray-900">uFreeTokens Benchmarks API</h3>
              </div>
              <button onClick={() => setIsApiModalOpen(false)} className="p-1 rounded text-gray-400 hover:text-gray-700">
                <X className="w-4 h-4" />
              </button>
            </div>

            <div className="p-4 space-y-3 text-xs">
              <p className="text-gray-600 leading-relaxed">
                基准测试数据通过公开接口提供，无需 API Key，可直接调用：
              </p>

              {/* 网关地址取 VITE_GATEWAY_BASE_URL，同源部署时就是当前站点地址 */}
              <div className="bg-gray-900 text-gray-100 rounded-lg p-3 font-mono text-[11px] relative">
                <button
                  onClick={() => handleCopyCode(curlExample)}
                  className="absolute right-2 top-2 p-1 text-gray-400 hover:text-white rounded bg-gray-800"
                >
                  {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
                <pre className="overflow-x-auto text-emerald-400 pr-8">{curlExample}</pre>
              </div>

              <ul className="text-[11px] text-gray-500 space-y-1 list-disc pl-4">
                <li>
                  <code className="text-purple-700 font-mono">GET /v1/benchmarks?category=reasoning</code>：已发布基准列表，含参评模型数、最近批次与三项冠军；
                  <code className="font-mono">category</code> 可选 general / coding / agents / reasoning / chinese / search / media / artifacts / embedding。
                </li>
                <li>
                  <code className="text-purple-700 font-mono">GET /v1/benchmarks/{'{slug}'}</code>：单个基准最新已发布批次的完整排行榜。
                </li>
                <li>
                  <code className="text-purple-700 font-mono">GET /v1/model-benchmarks?model={'{id}'}</code>：单个模型在全部已发布基准上的成绩。
                </li>
                <li>导入的外部榜单数据沿用原许可（如 CC BY 4.0），转载请按 <code className="font-mono">source_name</code> 署名。</li>
                <li>成本字段为整数微单位（除以 1,000,000 得到 cost_currency 币种下的金额）；响应缓存 5 分钟。</li>
              </ul>
            </div>

            <div className="p-3 bg-gray-50 border-t border-gray-200 flex justify-between items-center">
              <Link
                to="/docs/zh/public-data"
                className="text-[11px] text-purple-600 hover:text-purple-800 font-medium"
              >
                查看完整文档 →
              </Link>
              <button
                onClick={() => setIsApiModalOpen(false)}
                className="px-4 py-1.5 rounded-lg bg-gray-900 text-white text-xs font-medium cursor-pointer"
              >
                知道了
              </button>
            </div>
          </div>
        </div>
      )}

      <BenchmarksFooter onNavigateToModels={onNavigateToModels} />
    </div>
  );
};

// ---------- 详情页 /benchmarks/:slug ----------

export const BenchmarkDetailPage: React.FC<BenchmarksPageProps> = ({ onNavigateToModels, allModels = [] }) => {
  const { slug = '' } = useParams();
  const [detail, setDetail] = useState<BenchmarkDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    setNotFound(false);
    setDetail(null);
    getBenchmark(slug, controller.signal)
      .then((data) => setDetail(data))
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (err instanceof ApiError && err.status === 404) setNotFound(true);
        else setError(errorMessage(err));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [slug, reloadKey]);

  // 已上架的参评模型用目录里的厂商名补一列，未上架的留空。
  const providerOf = useMemo(() => {
    const byId = new Map(allModels.map((m) => [m.id, m.providerDisplay]));
    return (model: string | null) => (model ? byId.get(model) : undefined);
  }, [allModels]);

  const backLink = (
    <Link
      to="/benchmarks"
      className="inline-flex items-center space-x-1 text-xs text-gray-500 hover:text-purple-700 transition-colors"
    >
      <ChevronLeft className="w-3.5 h-3.5" />
      <span>全部基准测试</span>
    </Link>
  );

  let body: React.ReactNode;
  if (loading) {
    body = <LoadingState />;
  } else if (notFound) {
    body = (
      <div className="py-20 text-center space-y-1">
        <div className="text-xs font-medium text-gray-700">基准测试 {slug} 不存在或尚未发布</div>
        <Link to="/benchmarks" className="inline-block mt-2 px-3 py-1 text-xs bg-purple-50 text-purple-700 rounded-md hover:bg-purple-100 font-medium transition-colors">
          返回基准测试列表
        </Link>
      </div>
    );
  } else if (error || !detail) {
    body = <ErrorState message={error ?? '暂时无法获取基准测试数据。'} onRetry={() => setReloadKey((k) => k + 1)} />;
  } else {
    const currency = detail.run?.costCurrency;
    const rows = detail.results;
    // 导入的公开榜单大多没有成本 / 耗时 / 异常率 / 样本数——整列都为空时隐藏。
    const showCost = rows.some((r) => r.costPerTaskMicro !== null);
    const showDuration = rows.some((r) => r.avgDurationMs !== null);
    const showErrorRate = rows.some((r) => r.errorRate !== null);
    const showSamples = rows.some((r) => r.sampleCount !== null);
    // LMArena 榜单的 extra 带置信区间与票数。
    const showCI = rows.some((r) => formatCI(r) !== null);
    const showVotes = rows.some((r) => extraNumber(r.extra, 'votes') !== null);
    const championCards = (
      [
        { kind: 'quality', title: '最高质量', icon: <Award className="w-3.5 h-3.5" />, color: 'text-emerald-700' },
        { kind: 'value', title: '最高性价比', icon: <DollarSign className="w-3.5 h-3.5" />, color: 'text-purple-700' },
        { kind: 'speed', title: '最快响应', icon: <Zap className="w-3.5 h-3.5" />, color: 'text-amber-700' },
      ] as const
    ).filter((c) => detail.champions[c.kind] !== null);
    const gridCols =
      championCards.length >= 3 ? 'sm:grid-cols-3' : championCards.length === 2 ? 'sm:grid-cols-2' : 'sm:grid-cols-1';
    body = (
      <div className="space-y-6">
        {/* Header */}
        <div className="space-y-2 pb-5 border-b border-gray-100">
          <div className="flex items-center space-x-2">
            <span className="px-2 py-0.5 rounded bg-purple-100 text-purple-700 text-[10px] font-bold font-mono">
              {BENCHMARK_CATEGORY_LABELS[detail.category] ?? detail.category}
            </span>
            <OriginBadge origin={detail.run?.origin} />
          </div>
          <h1 className="text-2xl sm:text-3xl font-bold text-gray-900 tracking-tight">{detail.name}</h1>
          {detail.description && <p className="text-xs text-gray-500 leading-relaxed max-w-3xl">{detail.description}</p>}
          <div className="text-[11px] text-gray-400 font-mono flex flex-wrap items-center gap-x-2 gap-y-1 pt-1">
            <span>
              指标 <span className="text-gray-700 font-semibold">{detail.metricName}</span>（
              {detail.higherIsBetter ? '越高越好' : '越低越好'}）
            </span>
            <span>•</span>
            <span>
              评测覆盖 <span className="text-gray-700 font-semibold">{detail.modelsCount}</span> 个模型
            </span>
            {detail.run && (
              <>
                <span>•</span>
                <span>数据更新于 {formatDate(detail.run.runAt)}</span>
              </>
            )}
            {(detail.sourceName || detail.sourceUrl || detail.license) && (
              <>
                <span>•</span>
                <SourceAttribution name={detail.sourceName} url={detail.sourceUrl} license={detail.license} prefix="数据来源" />
              </>
            )}
          </div>
          {detail.run?.notes && <p className="text-[11px] text-gray-500 pt-1">备注：{detail.run.notes}</p>}
        </div>

        {/* Champions */}
        {championCards.length > 0 && (
          <div className={`grid grid-cols-1 ${gridCols} gap-3`}>
            {championCards.map((c) => (
              <div key={c.kind} className="p-3 rounded-xl border border-gray-200 bg-white shadow-xs space-y-2">
                <div className={`flex items-center space-x-1 text-[11px] font-semibold ${c.color}`}>
                  {c.icon}
                  <span>{c.title}</span>
                </div>
                <ChampionCell champion={detail.champions[c.kind]} kind={c.kind} bench={detail} />
              </div>
            ))}
          </div>
        )}
        {(detail.champions.value || detail.champions.speed) && <ChampionRulesNote />}

        {/* Leaderboard */}
        {detail.results.length === 0 ? (
          <div className="py-12 text-center text-xs text-gray-400">该基准暂无已发布的评测结果。</div>
        ) : (
          <div className="border border-gray-200 rounded-xl overflow-x-auto bg-white shadow-xs">
            <table className="w-full text-left text-xs">
              <thead className="bg-gray-50 text-[10px] text-gray-400 font-semibold uppercase">
                <tr>
                  <th className="py-2 px-3">排名</th>
                  <th className="py-2 px-3">模型</th>
                  <th className="py-2 px-3 text-right">{detail.metricName}</th>
                  {showCI && <th className="py-2 px-3 text-right">95% CI</th>}
                  {showVotes && <th className="py-2 px-3 text-right">票数</th>}
                  {showCost && <th className="py-2 px-3 text-right">单题成本</th>}
                  {showDuration && <th className="py-2 px-3 text-right">平均耗时</th>}
                  {showErrorRate && <th className="py-2 px-3 text-right">异常率</th>}
                  {showSamples && <th className="py-2 px-3 text-right">样本数</th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {detail.results.map((row) => {
                  // 未上架模型用榜单 extra 里的 organization 补厂商名。
                  const provider = providerOf(row.model) ?? extraString(row.extra, 'organization');
                  const ci = formatCI(row);
                  const votes = extraNumber(row.extra, 'votes');
                  return (
                    <tr key={`${row.rank}-${row.modelLabel}`} className="hover:bg-purple-50/40 transition-colors">
                      <td className="py-2.5 px-3 font-mono font-bold">
                        {row.rank === 1 ? (
                          <span className="w-5 h-5 rounded-full bg-amber-100 text-amber-800 inline-flex items-center justify-center text-[10px]">
                            1
                          </span>
                        ) : row.rank === 2 ? (
                          <span className="w-5 h-5 rounded-full bg-gray-200 text-gray-800 inline-flex items-center justify-center text-[10px]">
                            2
                          </span>
                        ) : row.rank === 3 ? (
                          <span className="w-5 h-5 rounded-full bg-amber-700/20 text-amber-900 inline-flex items-center justify-center text-[10px]">
                            3
                          </span>
                        ) : (
                          <span className="text-gray-400 pl-1">{row.rank}</span>
                        )}
                      </td>
                      <td className="py-2.5 px-3">
                        <ModelName label={row.modelLabel} model={row.model} className="font-semibold text-gray-900" />
                        {provider && <div className="text-[10px] text-gray-400">{provider}</div>}
                      </td>
                      <td className="py-2.5 px-3 text-right font-mono font-bold text-emerald-700">
                        {formatScore(row.score, detail.metricUnit)}
                      </td>
                      {showCI && <td className="py-2.5 px-3 text-right font-mono text-gray-500">{ci ?? '—'}</td>}
                      {showVotes && (
                        <td className="py-2.5 px-3 text-right font-mono text-gray-500">
                          {votes === null ? '—' : Math.round(votes).toLocaleString('en-US')}
                        </td>
                      )}
                      {showCost && (
                        <td className="py-2.5 px-3 text-right font-mono text-purple-700 font-medium">
                          {formatCost(row.costPerTaskMicro, currency)}
                        </td>
                      )}
                      {showDuration && (
                        <td className="py-2.5 px-3 text-right font-mono text-gray-600">{formatDuration(row.avgDurationMs)}</td>
                      )}
                      {showErrorRate && (
                        <td className="py-2.5 px-3 text-right font-mono text-gray-400">{formatErrorRate(row.errorRate)}</td>
                      )}
                      {showSamples && (
                        <td className="py-2.5 px-3 text-right font-mono text-gray-400">
                          {row.sampleCount === null ? '—' : row.sampleCount.toLocaleString('en-US')}
                        </td>
                      )}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        <p className="text-[11px] text-gray-400 leading-relaxed">
          {detail.run?.origin === 'self_eval'
            ? '本批次由 uFreeTokens 通过自家网关实际调用得出，成本为平台实际计费金额。'
            : `本批次为外部公开评测结果${detail.run?.origin === 'import' ? '（自动导入）' : ''}${
                showCost ? '，成本按来源口径记录，可能与 uFreeTokens 的实际售价不同' : ''
              }。`}
          已在平台上架的模型可点击查看详情与价格，未上架的模型仅展示成绩。
          {detail.sourceName && ` 数据署名：${detail.sourceName}。`}
        </p>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-white text-gray-900 flex flex-col font-sans select-text">
      <div className="max-w-6xl mx-auto w-full px-4 sm:px-6 lg:px-8 pt-6 pb-4 space-y-4 flex-1">
        {backLink}
        {body}
      </div>
      <BenchmarksFooter onNavigateToModels={onNavigateToModels} />
    </div>
  );
};
