import React, { useEffect, useMemo, useState } from 'react';
import {
  ArrowRight,
  ArrowUpRight,
  BarChart3,
  BookOpen,
  Check,
  Copy,
  FlaskConical,
  KeyRound,
  Layers,
  Plug,
  Receipt,
  Route,
  Search,
  ShieldCheck,
  Sparkles,
  TrendingDown,
  TrendingUp,
  Trophy,
} from 'lucide-react';
import { Model } from '../types';
import { useConsoleUser } from '../api/auth';
import { getModelRankings, ModelRankingEntry, ModelRankings } from '../api/rankings';
import { apiOrigin } from '../docs/origin';
import { useCopy } from '../docs/components/CopyButton';
import { ProviderIcon } from './ProviderIcon';

interface HomePageProps {
  allModels: Model[];
  onSearch: (q: string) => void;
  onOpenCommandPalette: () => void;
  onSelectModel: (m: Model) => void;
  onOpenPlayground: (m: Model) => void;
  onNavigate: (path: string) => void;
}

type SnippetLang = 'curl' | 'python' | 'node';

const SNIPPET_TABS: { id: SnippetLang; label: string }[] = [
  { id: 'curl', label: 'cURL' },
  { id: 'python', label: 'Python' },
  { id: 'node', label: 'Node.js' },
];

// 首页示例代码：Base URL 与文档站同一套规则（apiOrigin），模型 ID 取当前
// 热门/目录里的第一个真实模型，复制下来改个 Key 就能跑。
function buildSnippet(lang: SnippetLang, baseUrl: string, model: string): string {
  switch (lang) {
    case 'curl':
      return `curl ${baseUrl}/v1/chat/completions \\
  -H "Authorization: Bearer $UFT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "messages": [{"role": "user", "content": "你好"}]
  }'`;
    case 'python':
      return `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="sk-uft-...",
)

resp = client.chat.completions.create(
    model="${model}",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)`;
    case 'node':
      return `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${baseUrl}/v1",
  apiKey: process.env.UFT_API_KEY,
});

const resp = await client.chat.completions.create({
  model: "${model}",
  messages: [{ role: "user", content: "你好" }],
});
console.log(resp.choices[0].message.content);`;
  }
}

function formatTokens(n: number): string {
  if (n >= 1e12) return `${(n / 1e12).toFixed(1)}T`;
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(n);
}

const FEATURES = [
  {
    icon: Plug,
    title: '协议兼容，零改造接入',
    desc: '同时提供 OpenAI Chat Completions、Embeddings 与 Anthropic Messages 入口，现有 SDK 只需替换 Base URL 与 Key。',
  },
  {
    icon: Route,
    title: '多渠道路由与故障切换',
    desc: '同一模型背后挂多个上游渠道，按错误类别自动换 Key、换渠道重试，单一上游抖动不影响你的业务。',
  },
  {
    icon: Receipt,
    title: '按真实用量透明计费',
    desc: '流式请求强制回传 usage，按实际 Token 结算；每次调用的费用、延迟与用量来源都能在调用日志里查到。',
  },
  {
    icon: ShieldCheck,
    title: '不留存对话内容',
    desc: '调用日志只记录模型、状态、Token 数与费用等元数据，不保存请求与响应正文。',
  },
];

// 首页（/）：面向第一次来的访客，回答三个问题——这是什么、有哪些模型、
// 怎么开始用。模型数据复用 App 层已经拉好的 baseModels（公开目录），
// 热门榜额外请求一次 GET /v1/rankings/models；排行榜不可用时退化成
// "最新上架"，页面不出现空洞。
export const HomePage: React.FC<HomePageProps> = ({
  allModels,
  onSearch,
  onOpenCommandPalette,
  onSelectModel,
  onOpenPlayground,
  onNavigate,
}) => {
  const { me } = useConsoleUser();
  const [query, setQuery] = useState('');
  const [lang, setLang] = useState<SnippetLang>('python');
  const [copied, copy] = useCopy();
  const [rankings, setRankings] = useState<ModelRankings | null>(null);

  useEffect(() => {
    const ctrl = new AbortController();
    getModelRankings('week', { limit: 6, signal: ctrl.signal })
      .then(setRankings)
      .catch(() => setRankings(null));
    return () => ctrl.abort();
  }, []);

  const activeModels = useMemo(() => allModels.filter((m) => !m.isDeprecated), [allModels]);
  const modelById = useMemo(() => new Map(allModels.map((m) => [m.id, m])), [allModels]);
  const authorCount = useMemo(() => new Set(activeModels.map((m) => m.author)).size, [activeModels]);

  // 热门榜只展示公开目录里查得到的模型（需要价格/上下文来渲染卡片）。
  const trending = useMemo(
    () =>
      (rankings?.models ?? [])
        .map((entry) => ({ entry, model: modelById.get(entry.model) }))
        .filter((x): x is { entry: ModelRankingEntry; model: Model } => !!x.model)
        .slice(0, 6),
    [rankings, modelById]
  );
  const newest = useMemo(
    () =>
      [...activeModels]
        .sort((a, b) => new Date(b.releaseDate).getTime() - new Date(a.releaseDate).getTime())
        .slice(0, 6),
    [activeModels]
  );
  const showTrending = trending.length >= 3;

  const sampleModel = (showTrending ? trending[0].model : newest[0])?.id ?? 'deepseek-ai/DeepSeek-V4-Flash';
  const snippet = buildSnippet(lang, apiOrigin(), sampleModel);

  const submitSearch = (e: React.FormEvent) => {
    e.preventDefault();
    onSearch(query.trim());
  };

  return (
    <div className="flex-1 bg-white">
      {/* 1. Hero：定位 + 搜索 + 主 CTA，右侧直接给出可复制的接入代码 */}
      <section className="relative overflow-hidden border-b border-gray-100">
        <div
          aria-hidden
          className="absolute inset-0 bg-[radial-gradient(ellipse_at_top_left,_var(--color-purple-100)_0%,_transparent_55%)] opacity-70"
        />
        <div
          aria-hidden
          className="absolute inset-0 bg-[linear-gradient(to_right,#f3f4f6_1px,transparent_1px),linear-gradient(to_bottom,#f3f4f6_1px,transparent_1px)] bg-[size:32px_32px] [mask-image:linear-gradient(to_bottom,black,transparent_85%)]"
        />

        <div className="relative max-w-7xl mx-auto px-4 md:px-8 pt-14 pb-16 md:pt-20 md:pb-20 grid grid-cols-1 lg:grid-cols-12 gap-10 lg:gap-12 items-center">
          <div className="lg:col-span-6">
            <div className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white border border-purple-200 text-purple-700 text-xs font-medium shadow-2xs">
              <Sparkles className="w-3.5 h-3.5" />
              兼容 OpenAI / Anthropic 协议 · 按量计费
            </div>

            <h1 className="mt-5 text-4xl md:text-5xl font-extrabold tracking-tight text-gray-900 leading-[1.15]">
              一个 API Key，
              <br />
              调用<span className="text-purple-600">所有主流大模型</span>
            </h1>
            <p className="mt-4 text-sm md:text-base text-gray-600 leading-relaxed max-w-xl">
              uFreeTokens 聚合多家模型厂商，统一接口、自动路由与故障切换，按真实用量结算。比较价格与能力，选好模型，改一行 Base URL 即可上线。
            </p>

            {/* 搜索：回车直接带着关键词进模型库；⌘K 打开命令面板 */}
            <form onSubmit={submitSearch} className="mt-7 relative max-w-xl">
              <Search className="w-4 h-4 absolute left-3.5 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={`在 ${activeModels.length} 个模型中搜索，例如 DeepSeek、Claude、Qwen…`}
                className="w-full bg-white border border-gray-200 rounded-xl pl-10 pr-24 py-3 text-sm shadow-xs focus:outline-none focus:border-purple-400 focus:ring-4 focus:ring-purple-100 transition"
              />
              <button
                type="button"
                onClick={onOpenCommandPalette}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-[11px] text-gray-400 font-mono bg-gray-50 border border-gray-200 rounded-md px-1.5 py-0.5 hover:text-gray-700 cursor-pointer"
                title="打开命令面板"
              >
                ⌘K
              </button>
            </form>

            <div className="mt-6 flex flex-wrap items-center gap-3">
              <button
                onClick={() => onNavigate('/dashboard/api-keys')}
                className="inline-flex items-center gap-1.5 px-4 py-2.5 bg-purple-600 hover:bg-purple-700 text-white text-sm font-semibold rounded-xl shadow-sm transition-colors cursor-pointer"
              >
                <KeyRound className="w-4 h-4" />
                {me ? '管理 API Key' : '免费注册，获取 API Key'}
              </button>
              <button
                onClick={() => onNavigate('/models')}
                className="inline-flex items-center gap-1.5 px-4 py-2.5 bg-white border border-gray-200 hover:border-gray-300 hover:bg-gray-50 text-gray-800 text-sm font-semibold rounded-xl transition-colors cursor-pointer"
              >
                浏览模型库
                <ArrowRight className="w-4 h-4" />
              </button>
            </div>

            {/* 实时指标：全部来自真实数据，取不到的项直接不显示 */}
            <dl className="mt-10 grid grid-cols-3 gap-4 max-w-md">
              <div>
                <dt className="text-xs text-gray-500">可用模型</dt>
                <dd className="mt-0.5 text-2xl font-bold text-gray-900 tabular-nums">{activeModels.length}</dd>
              </div>
              <div>
                <dt className="text-xs text-gray-500">模型厂商</dt>
                <dd className="mt-0.5 text-2xl font-bold text-gray-900 tabular-nums">{authorCount}</dd>
              </div>
              {rankings?.totalTokens !== undefined && (
                <div>
                  <dt className="text-xs text-gray-500">近 7 日处理 Tokens</dt>
                  <dd className="mt-0.5 text-2xl font-bold text-gray-900 tabular-nums">
                    {formatTokens(rankings.totalTokens)}
                  </dd>
                </div>
              )}
            </dl>
          </div>

          {/* 代码卡片 */}
          <div className="lg:col-span-6">
            <div className="rounded-2xl border border-gray-800 bg-[#0d1117] shadow-2xl shadow-purple-900/10 overflow-hidden">
              <div className="flex items-center justify-between px-3 py-2 border-b border-white/10">
                <div className="flex items-center gap-1">
                  {SNIPPET_TABS.map((t) => (
                    <button
                      key={t.id}
                      onClick={() => setLang(t.id)}
                      className={`px-2.5 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer ${
                        lang === t.id ? 'bg-white/10 text-white' : 'text-gray-400 hover:text-gray-200'
                      }`}
                    >
                      {t.label}
                    </button>
                  ))}
                </div>
                <button
                  onClick={() => copy(snippet)}
                  className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs text-gray-400 hover:text-white hover:bg-white/10 transition-colors cursor-pointer"
                >
                  {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
                  {copied ? '已复制' : '复制'}
                </button>
              </div>
              <pre className="p-4 text-[12.5px] leading-relaxed text-[#e6edf3] font-mono overflow-x-auto min-h-[280px]">
                <code>{snippet}</code>
              </pre>
            </div>
            <div className="mt-3 flex items-center justify-between text-xs text-gray-500 px-1">
              <span className="truncate">
                Base URL <code className="font-mono text-gray-700">{apiOrigin()}/v1</code>
              </span>
              <button
                onClick={() => onNavigate('/docs')}
                className="shrink-0 inline-flex items-center gap-0.5 text-purple-600 hover:text-purple-800 font-medium cursor-pointer"
              >
                快速开始文档
                <ArrowUpRight className="w-3.5 h-3.5" />
              </button>
            </div>
          </div>
        </div>
      </section>

      {/* 2. 热门模型（本周用量 Top）/ 最新上架 */}
      <section className="max-w-7xl mx-auto px-4 md:px-8 py-14">
        <div className="flex items-end justify-between gap-4 mb-6">
          <div>
            <h2 className="text-xl md:text-2xl font-bold tracking-tight text-gray-900">
              {showTrending ? '本周热门模型' : '最新上架模型'}
            </h2>
            <p className="mt-1 text-sm text-gray-500">
              {showTrending
                ? '按开发者近 7 日通过 uFreeTokens 实际调用的 Token 份额排序'
                : '公开目录中最近发布的模型'}
            </p>
          </div>
          <button
            onClick={() => onNavigate(showTrending ? '/rankings' : '/models')}
            className="shrink-0 inline-flex items-center gap-1 text-sm text-purple-600 hover:text-purple-800 font-medium cursor-pointer"
          >
            {showTrending ? '完整排行榜' : '全部模型'}
            <ArrowRight className="w-4 h-4" />
          </button>
        </div>

        {(showTrending ? trending.length : newest.length) === 0 ? (
          <div className="py-16 text-center text-xs text-gray-400 border border-dashed border-gray-200 rounded-2xl">
            模型目录加载中…
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {(showTrending ? trending : newest.map((model) => ({ entry: null, model }))).map(({ entry, model }) => (
              <HomeModelCard
                key={model.id}
                model={model}
                entry={entry}
                onSelect={() => onSelectModel(model)}
                onTry={() => onOpenPlayground(model)}
              />
            ))}
          </div>
        )}
      </section>

      {/* 3. 核心能力 */}
      <section className="bg-gray-50/70 border-y border-gray-100">
        <div className="max-w-7xl mx-auto px-4 md:px-8 py-14">
          <h2 className="text-xl md:text-2xl font-bold tracking-tight text-gray-900">为什么用 uFreeTokens</h2>
          <p className="mt-1 text-sm text-gray-500">把多家模型厂商的接入、计费与稳定性问题收敛到一个网关里。</p>
          <div className="mt-8 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {FEATURES.map(({ icon: Icon, title, desc }) => (
              <div key={title} className="bg-white border border-gray-200 rounded-2xl p-5 shadow-2xs">
                <div className="w-9 h-9 rounded-xl bg-purple-50 border border-purple-100 flex items-center justify-center">
                  <Icon className="w-4.5 h-4.5 text-purple-600" />
                </div>
                <h3 className="mt-4 text-sm font-semibold text-gray-900">{title}</h3>
                <p className="mt-1.5 text-xs text-gray-600 leading-relaxed">{desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* 4. 三步上手 */}
      <section className="max-w-7xl mx-auto px-4 md:px-8 py-14">
        <h2 className="text-xl md:text-2xl font-bold tracking-tight text-gray-900">三步开始调用</h2>
        <ol className="mt-8 grid grid-cols-1 md:grid-cols-3 gap-4">
          {[
            {
              title: me ? '已登录控制台' : '注册账号',
              desc: me ? `当前账号 ${me.email}` : '邮箱 + 密码即可注册，无需绑定手机。',
              action: me ? null : { label: '去注册', path: '/dashboard/api-keys' },
              done: !!me,
            },
            {
              title: '创建 API Key',
              desc: '在个人中心创建 sk-uft- 开头的密钥，明文只展示一次，请妥善保存。',
              action: { label: '创建 Key', path: '/dashboard/api-keys' },
              done: false,
            },
            {
              title: '替换 Base URL',
              desc: `把 SDK 的 base_url 指向 ${apiOrigin()}/v1，选择模型 ID 即可调用。`,
              action: { label: '查看文档', path: '/docs' },
              done: false,
            },
          ].map((step, i) => (
            <li key={step.title} className="relative border border-gray-200 rounded-2xl p-5 bg-white">
              <div className="flex items-center gap-2.5">
                <span
                  className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-bold ${
                    step.done ? 'bg-emerald-500 text-white' : 'bg-purple-600 text-white'
                  }`}
                >
                  {step.done ? <Check className="w-4 h-4" /> : i + 1}
                </span>
                <h3 className="text-sm font-semibold text-gray-900">{step.title}</h3>
              </div>
              <p className="mt-3 text-xs text-gray-600 leading-relaxed break-all">{step.desc}</p>
              {step.action && (
                <button
                  onClick={() => onNavigate(step.action!.path)}
                  className="mt-4 inline-flex items-center gap-1 text-xs font-medium text-purple-600 hover:text-purple-800 cursor-pointer"
                >
                  {step.action.label}
                  <ArrowRight className="w-3.5 h-3.5" />
                </button>
              )}
            </li>
          ))}
        </ol>
      </section>

      {/* 5. 探索入口 */}
      <section className="max-w-7xl mx-auto px-4 md:px-8 pb-16">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {[
            { icon: Layers, title: '模型库', desc: '按价格、上下文、模态与能力筛选对比', path: '/models' },
            { icon: Trophy, title: '排行榜', desc: '基于真实调用量的模型与厂商榜单', path: '/rankings' },
            { icon: BarChart3, title: '基准测试', desc: '各项公开评测的跑分与冠军模型', path: '/benchmarks' },
            { icon: FlaskConical, title: 'Harness', desc: '在实验环境里评估智能体表现', path: '/harness' },
          ].map(({ icon: Icon, title, desc, path }) => (
            <button
              key={title}
              onClick={() => onNavigate(path)}
              className="group text-left border border-gray-200 hover:border-purple-300 hover:shadow-md rounded-2xl p-5 transition-all cursor-pointer bg-white"
            >
              <div className="flex items-center justify-between">
                <Icon className="w-5 h-5 text-gray-500 group-hover:text-purple-600 transition-colors" />
                <ArrowUpRight className="w-4 h-4 text-gray-300 group-hover:text-purple-500 transition-colors" />
              </div>
              <div className="mt-4 text-sm font-semibold text-gray-900">{title}</div>
              <div className="mt-1 text-xs text-gray-500">{desc}</div>
            </button>
          ))}
        </div>
      </section>

      <footer className="border-t border-gray-200 bg-gray-50 py-10 px-4 md:px-8 text-xs text-gray-500">
        <div className="max-w-7xl mx-auto flex flex-col md:flex-row items-center justify-between gap-6">
          <div className="flex items-center gap-2">
            <Layers className="w-4 h-4 text-purple-600" />
            <span className="font-bold text-gray-900 text-sm">uFreeTokens</span>
            <span className="text-gray-300">|</span>
            <span>统一接入多家大模型的 API 平台</span>
          </div>
          <div className="flex flex-wrap items-center gap-6">
            <button onClick={() => onNavigate('/models')} className="hover:text-gray-900 cursor-pointer">模型</button>
            <button onClick={() => onNavigate('/rankings')} className="hover:text-gray-900 cursor-pointer">排行榜</button>
            <button onClick={() => onNavigate('/docs')} className="hover:text-gray-900 cursor-pointer inline-flex items-center gap-1">
              <BookOpen className="w-3.5 h-3.5" />
              文档
            </button>
            <span className="text-gray-400">© 2026 uFreeTokens. 保留所有权利.</span>
          </div>
        </div>
      </footer>
    </div>
  );
};

// Model 上没有单独的币种字段，币种符号只拼在 inputPriceDisplay 开头
// （modelFromCatalog 生成的 "¥1.5 / 百万 Input Token"），卡片上空间有限，取出来自己拼短格式。
function priceSymbol(model: Model): string {
  return model.inputPriceDisplay.match(/^[^\d]*/)?.[0] ?? '';
}

interface HomeModelCardProps {
  model: Model;
  entry: ModelRankingEntry | null;
  onSelect: () => void;
  onTry: () => void;
}

const HomeModelCard: React.FC<HomeModelCardProps> = ({ model, entry, onSelect, onTry }) => (
  <div
    onClick={onSelect}
    className="group border border-gray-200 hover:border-purple-300 hover:shadow-md rounded-2xl p-4 bg-white transition-all cursor-pointer flex flex-col"
  >
    <div className="flex items-start gap-3">
      <ProviderIcon
        provider={model.provider}
        className="w-9 h-9 rounded-lg"
        fallbackBg={model.iconBg}
        fallbackTextClassName="text-xs"
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          {entry && <span className="text-xs font-mono text-gray-400">#{entry.rank}</span>}
          <span className="text-sm font-semibold text-gray-900 truncate group-hover:text-purple-700 transition-colors">
            {model.name}
          </span>
        </div>
        <div className="text-xs text-gray-500 truncate">{model.providerDisplay || model.author}</div>
      </div>
      {entry && (
        <div className="text-right shrink-0">
          <div className="text-sm font-semibold text-gray-900 tabular-nums">{(entry.share * 100).toFixed(1)}%</div>
          {entry.change !== null && (
            <div
              className={`inline-flex items-center gap-0.5 text-[11px] tabular-nums ${
                entry.change >= 0 ? 'text-emerald-600' : 'text-rose-500'
              }`}
            >
              {entry.change >= 0 ? <TrendingUp className="w-3 h-3" /> : <TrendingDown className="w-3 h-3" />}
              {Math.abs(entry.change * 100).toFixed(0)}%
            </div>
          )}
        </div>
      )}
    </div>

    <p className="mt-3 text-xs text-gray-600 leading-relaxed line-clamp-2 min-h-[2.5rem]">{model.description}</p>

    <div className="mt-4 pt-3 border-t border-gray-100 flex items-center justify-between gap-2 text-[11px] text-gray-500">
      <div className="flex items-center gap-3 min-w-0">
        <span className="truncate">
          <span className="text-gray-800 font-medium tabular-nums">
            {priceSymbol(model)}
            {model.inputPricePerM} / {priceSymbol(model)}
            {model.outputPricePerM}
          </span>{' '}
          每百万 Tokens
        </span>
        {model.contextDisplay && <span className="hidden sm:inline shrink-0">{model.contextDisplay}</span>}
      </div>
      <button
        onClick={(e) => {
          e.stopPropagation();
          onTry();
        }}
        className="shrink-0 px-2 py-1 rounded-md bg-purple-50 text-purple-700 hover:bg-purple-100 font-medium transition-colors cursor-pointer"
      >
        试一试
      </button>
    </div>
  </div>
);
