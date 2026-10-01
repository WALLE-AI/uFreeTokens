import React, { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';
import { AlertCircle, ExternalLink, Loader2 } from 'lucide-react';
import { ApiError } from '../api/errors';
import {
  BENCHMARK_CATEGORIES,
  BENCHMARK_CATEGORY_LABELS,
  BenchmarkCategory,
  ModelBenchmarkResult,
  formatBenchmarkScore,
  getModelBenchmarks,
} from '../api/benchmarks';

// 模型详情页的「评测成绩」：读 GET /v1/model-benchmarks?model=<id>，展示该
// 模型在每个已发布基准上的成绩（同一模型多个变体参评时后端已取最好的一个），
// 按基准分类分组。数据全部来自接口，没有成绩时显示空状态，不编造分数。

interface ModelBenchmarksSectionProps {
  modelId: string;
}

function formatDate(iso: string | null): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' });
}

// 进度条宽度：百分制直接用分数；排名可用时用「超过了多少比例的参评模型」；
// 都没有就不画进度条。
function barPercent(r: ModelBenchmarkResult): number | null {
  if (r.metricUnit === 'percent') return Math.max(0, Math.min(100, r.score));
  if (r.rank !== null && r.modelsCount !== null && r.modelsCount > 1) {
    return Math.max(0, Math.min(100, ((r.modelsCount - r.rank) / (r.modelsCount - 1)) * 100));
  }
  return null;
}

const CATEGORY_BAR_COLORS: Partial<Record<BenchmarkCategory, string>> = {
  general: 'bg-indigo-500',
  coding: 'bg-sky-500',
  agents: 'bg-emerald-500',
  reasoning: 'bg-purple-600',
  chinese: 'bg-rose-500',
  search: 'bg-cyan-500',
  media: 'bg-amber-500',
  artifacts: 'bg-blue-500',
  embedding: 'bg-teal-500',
};

export const ModelBenchmarksSection: React.FC<ModelBenchmarksSectionProps> = ({ modelId }) => {
  const [results, setResults] = useState<ModelBenchmarkResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    getModelBenchmarks(modelId, controller.signal)
      .then((data) => setResults(data))
      .catch((err) => {
        if (controller.signal.aborted) return;
        setResults([]);
        setError(
          err instanceof ApiError
            ? err.status === 503
              ? '评测数据维护中，请稍后再来。'
              : err.message
            : '网络异常，暂时无法获取评测成绩。'
        );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [modelId, reloadKey]);

  // 按分类分组，分类顺序与基准测试页一致；后端新增的未知分类排在最后。
  const groups = useMemo(() => {
    const byCat = new Map<string, ModelBenchmarkResult[]>();
    for (const r of results) {
      const list = byCat.get(r.category) ?? [];
      list.push(r);
      byCat.set(r.category, list);
    }
    const order = [
      ...BENCHMARK_CATEGORIES.filter((c) => byCat.has(c)),
      ...[...byCat.keys()].filter((c) => !(BENCHMARK_CATEGORIES as string[]).includes(c)),
    ];
    return order.map((cat) => ({ category: cat as BenchmarkCategory, items: byCat.get(cat) ?? [] }));
  }, [results]);

  // 页脚的数据来源署名：去重后列出。
  const attributions = useMemo(() => {
    const seen = new Set<string>();
    for (const r of results) if (r.sourceName) seen.add(r.sourceName);
    return [...seen];
  }, [results]);

  if (loading) {
    return (
      <div className="py-10 flex flex-col items-center justify-center text-gray-400 space-y-2 border border-gray-200 rounded-lg bg-white">
        <Loader2 className="w-5 h-5 animate-spin" />
        <div className="text-xs">评测成绩加载中...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="py-10 flex flex-col items-center justify-center text-center space-y-2 border border-gray-200 rounded-lg bg-white">
        <AlertCircle className="w-5 h-5 text-gray-300" />
        <div className="text-xs font-medium text-gray-700">{error}</div>
        <button
          onClick={() => setReloadKey((k) => k + 1)}
          className="mt-1 px-3 py-1 text-xs bg-purple-50 text-purple-700 rounded-md hover:bg-purple-100 font-medium transition-colors cursor-pointer"
        >
          重试
        </button>
      </div>
    );
  }

  if (results.length === 0) {
    return (
      <div className="py-10 text-center space-y-1 border border-gray-200 rounded-lg bg-white">
        <div className="text-xs font-medium text-gray-700">暂无该模型的公开评测成绩</div>
        <div className="text-[11px] text-gray-400">已收录的评测榜单出现该模型后会自动显示在这里。</div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-xs">
        {groups.map(({ category, items }) => (
          <div key={category} className="border border-gray-200 rounded-lg p-4 bg-white space-y-3">
            <div className="font-bold text-gray-900 text-xs border-b border-gray-100 pb-2 flex items-center justify-between">
              <span>{BENCHMARK_CATEGORY_LABELS[category] ?? category}</span>
              <span className="text-[10px] font-normal text-gray-400">{items.length} 项评测</span>
            </div>
            <div className="space-y-3">
              {items.map((r) => {
                const pct = barPercent(r);
                const date = formatDate(r.runAt);
                return (
                  <div key={r.slug}>
                    <div className="flex justify-between items-baseline gap-2 text-[11px]">
                      <Link
                        to={`/benchmarks/${encodeURIComponent(r.slug)}`}
                        className="text-gray-700 font-medium hover:text-purple-700 hover:underline truncate"
                        title={r.modelLabel !== modelId ? `参评名称：${r.modelLabel}` : undefined}
                      >
                        {r.name}
                      </Link>
                      <span className="font-mono font-bold text-gray-900 shrink-0">
                        {formatBenchmarkScore(r.score, r.metricUnit)}
                      </span>
                    </div>
                    {pct !== null && (
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div
                          className={`h-full rounded-full ${CATEGORY_BAR_COLORS[category] ?? 'bg-purple-600'}`}
                          style={{ width: `${pct.toFixed(1)}%` }}
                        />
                      </div>
                    )}
                    <div className="mt-1 flex flex-wrap items-center gap-x-1.5 text-[10px] text-gray-400">
                      {r.rank !== null && (
                        <span className="font-mono text-gray-600">
                          #{r.rank}
                          {r.modelsCount !== null && ` / ${r.modelsCount}`}
                        </span>
                      )}
                      {r.metricName && <span>• {r.metricName}{r.higherIsBetter ? '' : '（越低越好）'}</span>}
                      {date && <span>• 更新于 {date}</span>}
                      {(r.sourceName || r.sourceUrl) && (
                        <span className="truncate max-w-full">
                          •{' '}
                          {r.sourceUrl ? (
                            <a
                              href={r.sourceUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="inline-flex items-center space-x-0.5 hover:text-purple-700 hover:underline"
                            >
                              <span>{r.sourceName ?? '来源'}</span>
                              <ExternalLink className="w-2.5 h-2.5" />
                            </a>
                          ) : (
                            r.sourceName
                          )}
                        </span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>

      {attributions.length > 0 && (
        <p className="text-[11px] text-gray-400 leading-relaxed">
          成绩来自公开评测榜单（同一模型多个参评版本时取最好成绩），数据署名：{attributions.join('；')}。
        </p>
      )}
    </div>
  );
};
