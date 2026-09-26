import React, { useState } from 'react';
import {
  DocContentArticle,
  getDocArticle
} from '../data/docsContentData';
import {
  Terminal,
  Copy,
  Check,
  Shield,
  ExternalLink,
  ChevronRight,
  Code2,
  CheckCircle2,
  Sparkles,
  Zap,
  Info,
  BookOpen
} from 'lucide-react';

interface DocArticleViewProps {
  docId: string;
  onNavigateToEndpoint?: (endpointId: string) => void;
}

export const DocArticleView: React.FC<DocArticleViewProps> = ({
  docId,
  onNavigateToEndpoint
}) => {
  const article: DocContentArticle = getDocArticle(docId);
  const [copiedIndex, setCopiedIndex] = useState<number | null>(null);

  const handleCopy = (text: string, index: number) => {
    navigator.clipboard.writeText(text);
    setCopiedIndex(index);
    setTimeout(() => setCopiedIndex(null), 2000);
  };

  return (
    <div className="max-w-4xl mx-auto p-4 sm:p-8 space-y-8 pb-32">
      {/* Breadcrumbs & Badge */}
      <div className="flex flex-wrap items-center gap-2 text-xs text-gray-500">
        <span className="text-gray-400">开发指南</span>
        <span>/</span>
        <span className="text-gray-600 font-medium">{article.category}</span>
        <span>/</span>
        <span className="text-purple-700 font-semibold">{article.title}</span>
        {article.badge && (
          <span className="ml-2 px-2 py-0.5 rounded-full bg-purple-50 text-purple-700 text-[10px] font-semibold border border-purple-200">
            {article.badge}
          </span>
        )}
      </div>

      {/* Header */}
      <div className="border-b border-gray-200 pb-6 space-y-2">
        <h1 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight">
          {article.title}
        </h1>
        <p className="text-sm sm:text-base text-gray-600 leading-relaxed">
          {article.subtitle}
        </p>
      </div>

      {/* Overview Paragraph */}
      <div className="bg-white border border-gray-200 rounded-xl p-5 shadow-xs text-sm text-gray-700 leading-relaxed space-y-3">
        <div className="flex items-center gap-2 font-bold text-gray-900 text-xs uppercase tracking-wider">
          <BookOpen className="w-4 h-4 text-purple-600" />
          <span>核心设计与规范概述</span>
        </div>
        <p>{article.overview}</p>
      </div>

      {/* Quick Steps for Quickstart */}
      {article.id === 'quickstart' && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3 shadow-xs">
            <div className="w-8 h-8 rounded-lg bg-purple-100 text-purple-700 flex items-center justify-center font-bold text-sm">
              1
            </div>
            <h3 className="text-gray-950 font-bold text-sm">获取 API 凭证</h3>
            <p className="text-gray-600 text-xs leading-relaxed">
              在个人中心生成高强度 Bearer Token，支持为开发、测试与生产环境设置隔离的月度限额。
            </p>
          </div>

          <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3 shadow-xs">
            <div className="w-8 h-8 rounded-lg bg-blue-100 text-blue-700 flex items-center justify-center font-bold text-sm">
              2
            </div>
            <h3 className="text-gray-950 font-bold text-sm">配置 Base URL</h3>
            <p className="text-gray-600 text-xs leading-relaxed">
              将任何现有 OpenAI SDK 或 LangChain 的 baseURL 切换为 <code className="text-purple-700 font-mono bg-purple-50 px-1 rounded">https://ufreetokens.com/api/v1</code>。
            </p>
          </div>

          <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3 shadow-xs">
            <div className="w-8 h-8 rounded-lg bg-emerald-100 text-emerald-700 flex items-center justify-center font-bold text-sm">
              3
            </div>
            <h3 className="text-gray-950 font-bold text-sm">选择模型发起请求</h3>
            <p className="text-gray-600 text-xs leading-relaxed">
              传入 <code className="text-purple-700 font-mono bg-purple-50 px-1 rounded">anthropic/claude-3.7-sonnet</code> 或 <code className="text-purple-700 font-mono bg-purple-50 px-1 rounded">ufreetokens/auto</code> 即可全速响应。
            </p>
          </div>
        </div>
      )}

      {/* Key Features / Principles */}
      {article.keyFeatures && article.keyFeatures.length > 0 && (
        <div className="space-y-3">
          <h2 className="text-base font-bold text-gray-950 flex items-center gap-2">
            <Zap className="w-4 h-4 text-amber-500" />
            <span>特性要点与技术优势</span>
          </h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {article.keyFeatures.map((feat, idx) => (
              <div
                key={idx}
                className="bg-white border border-gray-200 rounded-lg p-3.5 flex items-start gap-3 shadow-xs hover:border-purple-200 transition-colors"
              >
                <CheckCircle2 className="w-4 h-4 text-emerald-600 shrink-0 mt-0.5" />
                <span className="text-xs text-gray-800 leading-relaxed font-medium">
                  {feat}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Code Examples */}
      {article.codeExamples && article.codeExamples.length > 0 && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-bold text-gray-950 flex items-center gap-2">
              <Terminal className="w-4 h-4 text-purple-600" />
              <span>标准实现代码示例</span>
            </h2>
            {onNavigateToEndpoint && (
              <button
                onClick={() => onNavigateToEndpoint('post-chat-completions')}
                className="text-xs text-purple-700 hover:text-purple-900 font-semibold flex items-center gap-1 cursor-pointer"
              >
                <span>在调试终端中在线运行</span>
                <ChevronRight className="w-3.5 h-3.5" />
              </button>
            )}
          </div>

          <div className="space-y-4">
            {article.codeExamples.map((ex, i) => (
              <div
                key={i}
                className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-sm"
              >
                <div className="flex items-center justify-between px-4 py-2 bg-slate-950 border-b border-slate-800 text-xs text-slate-400">
                  <span className="font-mono text-slate-300 font-semibold">
                    {ex.title}
                  </span>
                  <div className="flex items-center gap-3">
                    <span className="text-[10px] uppercase font-mono px-1.5 py-0.5 rounded bg-slate-800 text-purple-300">
                      {ex.lang}
                    </span>
                    <button
                      onClick={() => handleCopy(ex.code, i)}
                      className="flex items-center gap-1 text-slate-400 hover:text-slate-100 transition-colors cursor-pointer"
                    >
                      {copiedIndex === i ? (
                        <>
                          <Check className="w-3.5 h-3.5 text-emerald-400" />
                          <span className="text-emerald-400">已复制</span>
                        </>
                      ) : (
                        <>
                          <Copy className="w-3.5 h-3.5" />
                          <span>复制代码</span>
                        </>
                      )}
                    </button>
                  </div>
                </div>
                <pre className="p-4 text-xs font-mono text-slate-100 overflow-x-auto leading-relaxed">
                  {ex.code}
                </pre>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Config Snippet */}
      {article.configSnippet && (
        <div className="bg-purple-50/70 border border-purple-200 rounded-xl p-5 space-y-2.5">
          <div className="flex items-center gap-2 font-bold text-purple-900 text-xs uppercase tracking-wider">
            <Sparkles className="w-4 h-4 text-purple-700" />
            <span>推荐参数配置 (Configuration)</span>
          </div>
          <p className="text-xs text-gray-700 leading-relaxed font-mono">
            {article.configSnippet}
          </p>
        </div>
      )}

      {/* Security & Notes */}
      {article.notes && (
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-4 flex items-start gap-3 text-xs text-amber-900">
          <Info className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
          <div className="space-y-1">
            <span className="font-bold">安全建议与最佳实践：</span>
            <p className="leading-relaxed text-amber-800">{article.notes}</p>
          </div>
        </div>
      )}

      {/* Privacy Commitment */}
      <div className="bg-gray-50 border border-gray-200 rounded-xl p-5 flex items-start gap-3 text-xs text-gray-600">
        <Shield className="w-5 h-5 text-purple-600 shrink-0 mt-0.5" />
        <div className="space-y-1">
          <span className="font-bold text-gray-900">企业级 ZDR 零数据保留与隐私保障</span>
          <p className="leading-relaxed">
            uFreeTokens 严格遵循无日志暂存协议（Zero Data Retention），在调用支持 ZDR 的模型与提供商时，您的 Prompt 及响应绝不落地磁盘或被用于第三方 AI 训练。
          </p>
        </div>
      </div>
    </div>
  );
};
