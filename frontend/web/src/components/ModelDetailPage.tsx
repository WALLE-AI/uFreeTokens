import React, { useState } from 'react';
import {
  ArrowLeft,
  Copy,
  Check,
  ExternalLink,
  Play,
  Scale,
  Server,
  DollarSign,
  Activity as ActivityIcon,
  Clock,
  BarChart3,
  Grid,
  TrendingUp,
  HelpCircle,
  Compass,
  ChevronDown,
  ChevronUp,
  ShieldCheck,
  Layers,
  ArrowUpRight
} from 'lucide-react';
import { Model } from '../types';

interface ModelDetailPageProps {
  model: Model;
  allModels: Model[];
  onBack: () => void;
  onOpenPlayground: (model: Model) => void;
  onToggleCompare: (model: Model) => void;
  isInCompare: boolean;
  onSelectOtherModel: (model: Model) => void;
  onNavigateToBenchmarks?: () => void;
}

export const ModelDetailPage: React.FC<ModelDetailPageProps> = ({
  model,
  allModels,
  onBack,
  onOpenPlayground,
  onToggleCompare,
  isInCompare,
  onSelectOtherModel,
  onNavigateToBenchmarks,
}) => {
  const [copied, setCopied] = useState(false);
  const [isDescExpanded, setIsDescExpanded] = useState(false);
  const [activeSection, setActiveSection] = useState('providers');
  const [openFaqIndex, setOpenFaqIndex] = useState<number | null>(0);
  const [emailInput, setEmailInput] = useState('');
  const [subscribed, setSubscribed] = useState(false);

  const handleCopyId = () => {
    navigator.clipboard.writeText(model.id);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const scrollTo = (id: string) => {
    setActiveSection(id);
    const el = document.getElementById(id);
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  };

  // Model providers list - Provider names remain in English as requested
  const providersData = [
    {
      name: 'Relace',
      flag: '⚡',
      badge: null,
      input: `$${model.inputPricePerM.toFixed(2)}`,
      output: `$${model.outputPricePerM.toFixed(2)}`,
      cacheRead: '$0.015',
      latency: '1.52s',
      throughput: '41 tps',
      uptime: '99.17%',
      verified: true,
      color: 'text-purple-600',
    },
    {
      name: 'DeepInfra',
      flag: '🛡️',
      badge: null,
      input: `$${(model.inputPricePerM * 1.3).toFixed(2)}`,
      output: `$${model.outputPricePerM.toFixed(2)}`,
      cacheRead: '$0.006',
      latency: '1.14s',
      throughput: '58 tps',
      uptime: '99.85%',
      verified: true,
      color: 'text-blue-600',
    },
    {
      name: 'Morph',
      flag: '⚡',
      badge: '30% 优惠',
      badgeColor: 'text-emerald-700 bg-emerald-50 border-emerald-200',
      input: `$${(model.inputPricePerM * 0.7).toFixed(3)}`,
      output: `$${(model.outputPricePerM * 0.7).toFixed(3)}`,
      cacheRead: '$0.0063',
      latency: '1.47s',
      throughput: '24 tps',
      uptime: '99.76%',
      verified: false,
      color: 'text-emerald-600',
    },
    {
      name: 'Fireworks',
      flag: '🎆',
      badge: null,
      input: `$${(model.inputPricePerM * 1.1).toFixed(2)}`,
      output: `$${(model.outputPricePerM * 1.1).toFixed(2)}`,
      cacheRead: '$0.007',
      latency: '1.21s',
      throughput: '75 tps',
      uptime: '99.83%',
      verified: true,
      color: 'text-amber-600',
    },
    {
      name: 'GMICall',
      flag: '🌐',
      badge: '5% 优惠',
      badgeColor: 'text-emerald-700 bg-emerald-50 border-emerald-200',
      input: `$${(model.inputPricePerM * 0.95).toFixed(3)}`,
      output: `$${(model.outputPricePerM * 0.95).toFixed(3)}`,
      cacheRead: '$0.0057',
      latency: '3.24s',
      throughput: '107 tps',
      uptime: '99.90%',
      verified: true,
      color: 'text-cyan-600',
    },
    {
      name: 'AtlasCloud',
      flag: '🚀',
      badge: null,
      input: `$${(model.inputPricePerM * 1.2).toFixed(2)}`,
      output: `$${(model.outputPricePerM * 1.2).toFixed(2)}`,
      cacheRead: '$0.03',
      latency: '1.47s',
      throughput: '81 tps',
      uptime: '99.81%',
      verified: false,
      color: 'text-indigo-600',
    },
    {
      name: 'Baseten',
      flag: '🇺🇸',
      badge: null,
      input: `$${(model.inputPricePerM * 1.2).toFixed(2)}`,
      output: `$${(model.outputPricePerM * 1.2).toFixed(2)}`,
      cacheRead: '$0.03',
      latency: '0.41s',
      throughput: '60 tps',
      uptime: '99.95%',
      verified: true,
      color: 'text-rose-600',
    },
    {
      name: 'Together',
      flag: '🤝',
      badge: null,
      input: `$${(model.inputPricePerM * 1.05).toFixed(2)}`,
      output: `$${(model.outputPricePerM * 1.05).toFixed(2)}`,
      cacheRead: '$0.007',
      latency: '1.14s',
      throughput: '113 tps',
      uptime: '99.79%',
      verified: true,
      color: 'text-teal-600',
    },
    {
      name: 'SiliconFlow',
      flag: '⚡',
      badge: null,
      input: `$${model.inputPricePerM.toFixed(2)}`,
      output: `$${model.outputPricePerM.toFixed(2)}`,
      cacheRead: '$0.005',
      latency: '0.98s',
      throughput: '125 tps',
      uptime: '99.92%',
      verified: true,
      color: 'text-emerald-700',
    },
    {
      name: 'DeepSeek 官方',
      flag: '🇨🇳',
      badge: '不可路由',
      badgeColor: 'text-gray-600 bg-gray-100 border-gray-300',
      input: `$${model.inputPricePerM.toFixed(2)}`,
      output: `$${model.outputPricePerM.toFixed(2)}`,
      cacheRead: '$0.0037',
      latency: '1.20s',
      throughput: '76 tps',
      uptime: '99.98%',
      verified: true,
      color: 'text-blue-700',
    },
  ];

  // FAQs in Chinese
  const faqs = [
    {
      q: `什么是 ${model.name}？`,
      a: `${model.name} 是由 ${model.providerDisplay} 推出的旗舰级前沿生成式 AI 模型。${model.description}`,
    },
    {
      q: `${model.name} 的调用价格是多少？`,
      a: `该模型在 uFreeTokens 上的基准输入单价为 ${model.inputPriceDisplay}，输出单价为 ${model.outputPriceDisplay || '免费'}。由于平台支持 Prompt 智能缓存与不同算力提供商的路由竞争，实际计费常常更低。`,
    },
    {
      q: `${model.name} 的上下文窗口支持多大？`,
      a: `该模型支持长达 ${model.contextDisplay || '标准'} 的上下文容量（约 ${model.contextTokens.toLocaleString()} tokens），能够支持超大型代码库解析、超长文本文档阅读与持续多轮长程会话。`,
    },
    {
      q: `${model.name} 是否支持工具调用和结构化输出？`,
      a: `全面支持。在基准评测中，该模型的工具调用准确率高达 ${model.toolCallingCapability}%，同时支持标准 JSON Schema 的 response_format 结构化数据输出。`,
    },
    {
      q: `${model.name} 支持哪些输入与输出模态？`,
      a: `输入模态支持: ${model.modalities.join(', ')}；输出模态支持标准文本以及流式数据生成。`,
    },
    {
      q: `哪些算力提供商托管了 ${model.name}？`,
      a: `目前已有 Relace、DeepInfra、Morph、Fireworks、GMICall、AtlasCloud、Baseten、Together、SiliconFlow 等多家全球算力提供商提供端点托管，uFreeTokens 会实时根据延迟和吞吐进行智能分流路由。`,
    },
    {
      q: `${model.name} 是何时发布的？`,
      a: `该模型官方发布并上线 uFreeTokens 路由的时间为 ${model.date}。`,
    },
  ];

  // Related models from same provider or series
  const relatedModels = allModels.filter((m) => m.id !== model.id).slice(0, 3);

  return (
    <div className="min-h-screen bg-white text-gray-900 flex flex-col font-sans select-text">
      {/* Top sticky Breadcrumb & Header Bar */}
      <div className="sticky top-12 z-30 bg-white/95 backdrop-blur-xs border-b border-gray-200 px-4 lg:px-8 py-3">
        <div className="max-w-7xl mx-auto flex flex-wrap items-center justify-between gap-3">
          {/* Breadcrumb + Model Name */}
          <div className="flex items-center space-x-2 text-xs text-gray-500">
            <button
              onClick={onBack}
              className="flex items-center space-x-1 text-gray-600 hover:text-purple-600 font-medium transition-colors cursor-pointer"
            >
              <ArrowLeft className="w-3.5 h-3.5" />
              <span>返回模型列表</span>
            </button>
            <span>/</span>
            <span className="text-gray-400 font-mono text-[11px]">{model.id}</span>
            <button
              onClick={handleCopyId}
              title="复制模型 ID"
              className="p-1 hover:text-gray-800 text-gray-400 rounded hover:bg-gray-100 transition-colors cursor-pointer"
            >
              {copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
            </button>
            <span className="hidden sm:inline px-1.5 py-0.5 rounded bg-gray-100 text-[10px] text-gray-600 border border-gray-200 flex items-center space-x-1">
              <span>模型权重</span>
              <ExternalLink className="w-2.5 h-2.5" />
            </span>
          </div>

          {/* Action buttons */}
          <div className="flex items-center space-x-2 text-xs">
            <button
              onClick={() => onToggleCompare(model)}
              className={`flex items-center space-x-1 px-3 py-1.5 rounded-md border text-xs font-medium transition-colors cursor-pointer ${
                isInCompare
                  ? 'bg-purple-50 text-purple-700 border-purple-300'
                  : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-50'
              }`}
            >
              <Scale className="w-3.5 h-3.5" />
              <span>{isInCompare ? '已加入对比' : '加入对比'}</span>
            </button>

            <button
              onClick={() => onOpenPlayground(model)}
              className="flex items-center space-x-1 px-3 py-1.5 rounded-md border border-gray-300 bg-white hover:bg-gray-50 text-gray-700 text-xs font-medium transition-colors cursor-pointer"
            >
              <Play className="w-3.5 h-3.5" />
              <span>测试场</span>
            </button>

            <button
              onClick={() => onOpenPlayground(model)}
              className="flex items-center space-x-1.5 px-4 py-1.5 rounded-md bg-purple-600 hover:bg-purple-700 text-white text-xs font-medium shadow-xs transition-colors cursor-pointer"
            >
              <span>立即体验</span>
              <ArrowUpRight className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </div>

      {/* Main Container */}
      <div className="max-w-7xl mx-auto w-full px-4 lg:px-8 py-6">
        {/* Model Hero Header */}
        <div className="mb-6 space-y-3">
          <div className="flex items-center space-x-2.5">
            <div
              className={`w-7 h-7 rounded-lg flex items-center justify-center text-xs font-bold shadow-xs shrink-0 ${model.iconBg}`}
            >
              ▲
            </div>
            <h1 className="text-xl sm:text-2xl font-bold text-gray-900 tracking-tight">
              {model.name}
            </h1>
            {model.badge && (
              <span
                className={`px-2 py-0.5 text-xs rounded border font-medium ${
                  model.badgeColor || 'bg-purple-50 text-purple-700 border-purple-200'
                }`}
              >
                {model.badge}
              </span>
            )}
          </div>

          <div className="text-xs text-gray-600 max-w-4xl leading-relaxed">
            <p className={isDescExpanded ? '' : 'line-clamp-2'}>{model.description}</p>
            <button
              onClick={() => setIsDescExpanded(!isDescExpanded)}
              className="text-purple-600 hover:text-purple-800 font-medium text-xs mt-1 block cursor-pointer"
            >
              {isDescExpanded ? '收起' : '展开更多 ▾'}
            </button>
          </div>

          {/* 4-Item Quick Stats Banner */}
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 pt-2 text-xs">
            <div className="border border-gray-200 rounded-lg p-3 bg-white">
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                支持模态
              </div>
              <div className="mt-1 flex items-center space-x-1.5">
                <span className="w-4 h-4 rounded bg-purple-100 text-purple-800 flex items-center justify-center text-[10px] font-bold">
                  文本
                </span>
                <span className="text-gray-400">→</span>
                <span className="w-4 h-4 rounded bg-purple-100 text-purple-800 flex items-center justify-center text-[10px] font-bold">
                  文本
                </span>
                {model.modalities.includes('image') && (
                  <span className="px-1 bg-gray-100 text-[10px] text-gray-600 rounded">
                    视觉图像
                  </span>
                )}
              </div>
            </div>

            <div className="border border-gray-200 rounded-lg p-3 bg-white">
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                输入 / 输出单价
              </div>
              <div className="mt-1 text-sm font-bold text-gray-900 font-mono">
                ${model.inputPricePerM.toFixed(2)} / ${model.outputPricePerM.toFixed(2)}{' '}
                <span className="text-xs font-normal text-gray-400">每百万 Token</span>
              </div>
            </div>

            <div className="border border-gray-200 rounded-lg p-3 bg-white">
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                上下文窗口
              </div>
              <div className="mt-1 text-sm font-bold text-gray-900 font-mono">
                {model.contextDisplay || `${(model.contextTokens / 1000).toFixed(0)}K`}
              </div>
            </div>

            <div className="border border-gray-200 rounded-lg p-3 bg-white">
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                发布日期
              </div>
              <div className="mt-1 text-sm font-bold text-gray-900">{model.date}</div>
            </div>
          </div>
        </div>

        {/* Two-column layout: Sticky Left Nav + Content */}
        <div className="flex items-start gap-8 relative">
          {/* Left Anchor Navigation */}
          <div className="w-48 shrink-0 hidden lg:block sticky top-28 space-y-1 text-xs">
            <button
              onClick={onBack}
              className="w-full flex items-center space-x-2 px-3 py-2 text-purple-700 bg-purple-50 hover:bg-purple-100 font-semibold rounded-lg mb-3 transition-colors text-left cursor-pointer"
            >
              <ArrowLeft className="w-3.5 h-3.5" />
              <span>返回模型列表</span>
            </button>

            {[
              { id: 'providers', label: '服务提供商', icon: <Server className="w-3.5 h-3.5" /> },
              { id: 'pricing', label: '价格方案', icon: <DollarSign className="w-3.5 h-3.5" /> },
              { id: 'performance', label: '性能表现', icon: <ActivityIcon className="w-3.5 h-3.5" /> },
              { id: 'uptime', label: '可用率与稳定性', icon: <Clock className="w-3.5 h-3.5" /> },
              { id: 'benchmarks', label: '权威基准评测', icon: <BarChart3 className="w-3.5 h-3.5" /> },
              { id: 'apps', label: '热门生态应用', icon: <Grid className="w-3.5 h-3.5" /> },
              { id: 'activity', label: '调用活跃度', icon: <TrendingUp className="w-3.5 h-3.5" /> },
              { id: 'faq', label: '常见问题解答', icon: <HelpCircle className="w-3.5 h-3.5" /> },
              { id: 'explore', label: '探索更多模型', icon: <Compass className="w-3.5 h-3.5" /> },
            ].map((nav) => {
              const active = activeSection === nav.id;
              return (
                <button
                  key={nav.id}
                  onClick={() => scrollTo(nav.id)}
                  className={`w-full flex items-center space-x-2 px-3 py-2 rounded-lg text-left transition-colors cursor-pointer ${
                    active
                      ? 'text-purple-700 font-semibold bg-purple-50/70 border-l-2 border-purple-600'
                      : 'text-gray-600 hover:text-gray-900 hover:bg-gray-50'
                  }`}
                >
                  <span className={active ? 'text-purple-600' : 'text-gray-400'}>{nav.icon}</span>
                  <span>{nav.label}</span>
                </button>
              );
            })}
          </div>

          {/* Right Main Body Content */}
          <div className="flex-1 space-y-12 min-w-0">
            {/* 1. SECTION: Providers */}
            <section id="providers" className="space-y-3 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <Server className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">服务提供商</h2>
                </div>
                <p className="text-xs text-gray-500 mt-1 max-w-3xl leading-relaxed">
                  多家算力服务商同时托管并提供此模型。uFreeTokens 会根据你选择的路由模式将请求分流至最优节点 —— <strong>均衡模式 (Balanced，兼顾价格与速度)</strong>、<strong>极速模式 (Nitro)</strong> 或 <strong>精准模式 (Exacto，工具调用成功率最高)</strong>。
                </p>
              </div>

              {/* Provider Filters Bar */}
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                <div className="flex items-center space-x-2">
                  <span className="px-2.5 py-1 rounded bg-gray-100 border border-gray-200 font-medium text-gray-700">
                    标准模式 ▾
                  </span>
                  <span className="px-2 py-1 rounded border border-gray-200 text-gray-600">
                    推理思考强度: 全部 ▾
                  </span>
                </div>
                <div className="flex items-center space-x-2">
                  <span className="px-2 py-1 rounded border border-gray-200 text-gray-600">
                    延迟 / 吞吐量: P50 ▾
                  </span>
                  <span className="px-2 py-1 rounded border border-gray-200 text-gray-600">
                    量化级别筛选 ▾
                  </span>
                </div>
              </div>

              {/* Providers Table - Provider names remain in English */}
              <div className="overflow-x-auto border border-gray-200 rounded-lg shadow-xs bg-white">
                <table className="w-full text-left text-xs border-collapse">
                  <thead>
                    <tr className="bg-gray-50/80 border-b border-gray-200 text-gray-500 font-semibold text-[11px]">
                      <th className="py-2.5 px-3">提供商</th>
                      <th className="py-2.5 px-3 text-right">输入单价 /M</th>
                      <th className="py-2.5 px-3 text-right">输出单价 /M</th>
                      <th className="py-2.5 px-3 text-right">缓存读取 /M</th>
                      <th className="py-2.5 px-3 text-right">P50 延迟</th>
                      <th className="py-2.5 px-3 text-right">吞吐速度</th>
                      <th className="py-2.5 px-3 text-right">在线率</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {providersData.map((prov, i) => (
                      <tr key={i} className="hover:bg-gray-50/60 transition-colors">
                        <td className="py-2.5 px-3">
                          <div className="flex items-center space-x-2">
                            <span className="text-xs">{prov.flag}</span>
                            <span className="font-semibold text-gray-900">{prov.name}</span>
                            {prov.verified && (
                              <span title="已认证托管节点">
                                <ShieldCheck className="w-3 h-3 text-emerald-600" />
                              </span>
                            )}
                            {prov.badge && (
                              <span
                                className={`px-1.5 py-0.2 text-[9px] rounded border font-medium ${prov.badgeColor}`}
                              >
                                {prov.badge}
                              </span>
                            )}
                          </div>
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-gray-800">
                          {prov.input}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-gray-800">
                          {prov.output}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-gray-500">
                          {prov.cacheRead}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-gray-600">
                          {prov.latency}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono font-medium text-purple-700">
                          {prov.throughput}
                        </td>
                        <td className="py-2.5 px-3 text-right">
                          <span className="inline-flex items-center space-x-1 font-mono text-[11px] text-emerald-700 bg-emerald-50 px-1.5 py-0.5 rounded border border-emerald-200">
                            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
                            <span>{prov.uptime}</span>
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            {/* 2. SECTION: Pricing */}
            <section id="pricing" className="space-y-3 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <DollarSign className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">价格方案</h2>
                </div>
                <p className="text-xs text-gray-500 mt-1">
                  展示用户实际调用该模型支付的平均价格与各提供商公示单价。借助 Prompt 智能缓存与平台路由优惠，实际支付费用通常明显低于标牌公示价。
                </p>
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                <div className="p-4 rounded-lg border border-purple-200 bg-purple-50/40">
                  <div className="text-[11px] text-purple-700 font-medium">Prompt Token 定价</div>
                  <div className="text-lg font-bold text-purple-900 font-mono mt-0.5">
                    ${model.inputPricePerM.toFixed(2)}{' '}
                    <span className="text-xs font-normal text-purple-700">/ 1M tokens</span>
                  </div>
                  <div className="text-[10px] text-purple-600 mt-1">
                    启用 Prompt Caching 自动命中可享 50%~80% 降费
                  </div>
                </div>

                <div className="p-4 rounded-lg border border-purple-200 bg-purple-50/40">
                  <div className="text-[11px] text-purple-700 font-medium">Completion Token 定价</div>
                  <div className="text-lg font-bold text-purple-900 font-mono mt-0.5">
                    ${model.outputPricePerM.toFixed(2)}{' '}
                    <span className="text-xs font-normal text-purple-700">/ 1M tokens</span>
                  </div>
                  <div className="text-[10px] text-purple-600 mt-1">无隐藏费用，按实际返回 Token 结算</div>
                </div>

                <div className="p-4 rounded-lg border border-gray-200 bg-white">
                  <div className="text-[11px] text-gray-500 font-medium">智能路由折扣</div>
                  <div className="text-lg font-bold text-emerald-700 font-mono mt-0.5">
                    最高 30% 优惠
                  </div>
                  <div className="text-[10px] text-gray-400 mt-1">自动撮合最优质的低价算力提供方</div>
                </div>
              </div>
            </section>

            {/* 3. SECTION: Performance */}
            <section id="performance" className="space-y-4 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <ActivityIcon className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">性能表现</h2>
                </div>
              </div>

              {/* KPI Header values */}
              <div className="flex items-center space-x-8 text-xs">
                <div>
                  <span className="text-gray-400 block text-[11px]">吞吐速度 (Throughput)</span>
                  <span className="text-xl font-bold font-mono text-gray-900">134 tok/s</span>
                  <span className="text-[10px] text-gray-400 block">P50，跨提供商综合最优</span>
                </div>
                <div>
                  <span className="text-gray-400 block text-[11px]">首次响应延迟 (Latency)</span>
                  <span className="text-xl font-bold font-mono text-gray-900">0.40 秒</span>
                  <span className="text-[10px] text-gray-400 block">P50，最优提供商表现</span>
                </div>
              </div>

              {/* Performance Filter pills */}
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="px-2 py-1 rounded bg-gray-100 text-gray-700 border border-gray-200">
                  全部地域 ▾
                </span>
                <span className="px-2 py-1 rounded border border-gray-200 text-gray-600">
                  推理思考强度: 全部 ▾
                </span>
                <span className="px-2 py-1 rounded border border-gray-200 text-gray-600">
                  延迟 / 吞吐量: P50 ▾
                </span>
                <span className="px-2 py-1 rounded border border-gray-200 text-gray-600">
                  最近 1 周 ▾
                </span>
              </div>

              {/* 3x2 Performance Charts Grid */}
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-xs">
                {/* 1. Throughput Chart */}
                <div className="p-3.5 border border-gray-200 rounded-lg bg-white space-y-2">
                  <div className="flex items-center justify-between font-semibold text-gray-800">
                    <span>吞吐速度趋势</span>
                    <span className="text-gray-400 text-[10px]">⤢</span>
                  </div>
                  <div className="h-28 w-full">
                    <svg className="w-full h-full" viewBox="0 0 200 80">
                      <path
                        d="M 10 60 Q 50 30, 90 40 T 170 25 T 190 35"
                        fill="none"
                        stroke="#a855f7"
                        strokeWidth="2"
                      />
                      <path
                        d="M 10 70 Q 50 50, 90 55 T 170 40 T 190 45"
                        fill="none"
                        stroke="#06b6d4"
                        strokeWidth="1.5"
                      />
                      <path
                        d="M 10 50 Q 50 45, 90 48 T 170 30 T 190 28"
                        fill="none"
                        stroke="#10b981"
                        strokeWidth="1.5"
                      />
                    </svg>
                  </div>
                  <div className="space-y-1 text-[10px] text-gray-500 pt-1">
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-emerald-500" />
                        <span>SiliconFlow</span>
                      </span>
                      <span className="font-mono">平均 125 tok/s</span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-purple-500" />
                        <span>NovitaAI</span>
                      </span>
                      <span className="font-mono">平均 115 tok/s</span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-cyan-500" />
                        <span>Together</span>
                      </span>
                      <span className="font-mono">平均 113 tok/s</span>
                    </div>
                  </div>
                </div>

                {/* 2. Latency Chart */}
                <div className="p-3.5 border border-gray-200 rounded-lg bg-white space-y-2">
                  <div className="flex items-center justify-between font-semibold text-gray-800">
                    <span>首次响应延迟 (TTFT)</span>
                    <span className="text-gray-400 text-[10px]">⤢</span>
                  </div>
                  <div className="h-28 w-full">
                    <svg className="w-full h-full" viewBox="0 0 200 80">
                      <path
                        d="M 10 40 Q 60 20, 100 25 T 160 30 T 190 32"
                        fill="none"
                        stroke="#10b981"
                        strokeWidth="2"
                      />
                      <path
                        d="M 10 50 Q 60 45, 100 35 T 160 38 T 190 40"
                        fill="none"
                        stroke="#f59e0b"
                        strokeWidth="1.5"
                      />
                    </svg>
                  </div>
                  <div className="space-y-1 text-[10px] text-gray-500 pt-1">
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-emerald-500" />
                        <span>Baseten</span>
                      </span>
                      <span className="font-mono">平均 0.41 秒</span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-amber-500" />
                        <span>Wafer</span>
                      </span>
                      <span className="font-mono">平均 0.64 秒</span>
                    </div>
                  </div>
                </div>

                {/* 3. E2E Latency Chart */}
                <div className="p-3.5 border border-gray-200 rounded-lg bg-white space-y-2">
                  <div className="flex items-center justify-between font-semibold text-gray-800">
                    <span>端到端完整生成延迟</span>
                    <span className="text-gray-400 text-[10px]">⤢</span>
                  </div>
                  <div className="h-28 w-full">
                    <svg className="w-full h-full" viewBox="0 0 200 80">
                      <path
                        d="M 10 35 Q 70 30, 110 38 T 170 32 T 190 30"
                        fill="none"
                        stroke="#3b82f6"
                        strokeWidth="2"
                      />
                      <path
                        d="M 10 60 Q 70 55, 110 50 T 170 52 T 190 48"
                        fill="none"
                        stroke="#ec4899"
                        strokeWidth="1.5"
                      />
                    </svg>
                  </div>
                  <div className="space-y-1 text-[10px] text-gray-500 pt-1">
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-blue-500" />
                        <span>Baseten</span>
                      </span>
                      <span className="font-mono">平均 1.15 秒</span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="flex items-center space-x-1">
                        <span className="w-2 h-2 rounded-full bg-pink-500" />
                        <span>DeepSeek</span>
                      </span>
                      <span className="font-mono">平均 3.61 秒</span>
                    </div>
                  </div>
                </div>

                {/* 4. AutoExacto Benchmarks */}
                <div className="p-3.5 border border-gray-200 rounded-lg bg-white space-y-2">
                  <div className="flex items-center justify-between font-semibold text-gray-800">
                    <span>AutoExacto 路由精准度评测</span>
                    <span className="text-gray-400 text-[10px]">⤢</span>
                  </div>
                  <div className="space-y-1.5 text-[11px] pt-1">
                    <div className="flex justify-between font-mono text-[10px] text-gray-400 border-b pb-1">
                      <span>提供商</span>
                      <span>GPQA DIAMOND</span>
                      <span>TAU-BENCH</span>
                    </div>
                    {[
                      { name: 'SiliconFlow', p1: '91.0%', p2: '--' },
                      { name: 'Fireworks', p1: '86.3%', p2: '--' },
                      { name: 'Baseten', p1: '84.0%', p2: '--' },
                      { name: '智能路由 (uFreeTokens)', p1: '90.2%', p2: '76.7%', bold: true },
                    ].map((row, i) => (
                      <div
                        key={i}
                        className={`flex justify-between font-mono ${
                          row.bold ? 'font-bold text-purple-700 bg-purple-50 p-1 rounded' : 'text-gray-600'
                        }`}
                      >
                        <span className="truncate pr-2">{row.name}</span>
                        <span>{row.p1}</span>
                        <span>{row.p2}</span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* 5. Tool Call Error Rate */}
                <div className="p-3.5 border border-gray-200 rounded-lg bg-white space-y-2">
                  <div className="flex items-center justify-between font-semibold text-gray-800">
                    <span>工具调用错误率</span>
                    <span className="text-gray-400 text-[10px]">⤢</span>
                  </div>
                  <div className="h-24 w-full">
                    <svg className="w-full h-full" viewBox="0 0 200 80">
                      <path
                        d="M 10 70 Q 70 72, 110 68 T 170 65 T 190 69"
                        fill="none"
                        stroke="#10b981"
                        strokeWidth="2"
                      />
                    </svg>
                  </div>
                  <div className="space-y-1 text-[10px] text-gray-500 pt-1">
                    <div className="flex justify-between items-center">
                      <span>Fireworks</span>
                      <span className="font-mono text-emerald-600 font-bold">平均 0.09%</span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span>AtlasCloud</span>
                      <span className="font-mono">平均 0.56%</span>
                    </div>
                  </div>
                </div>

                {/* 6. Structured Output Error Rate */}
                <div className="p-3.5 border border-gray-200 rounded-lg bg-white space-y-2">
                  <div className="flex items-center justify-between font-semibold text-gray-800">
                    <span>结构化输出错误率</span>
                    <span className="text-gray-400 text-[10px]">⤢</span>
                  </div>
                  <div className="h-24 w-full">
                    <svg className="w-full h-full" viewBox="0 0 200 80">
                      <path
                        d="M 10 55 Q 70 65, 110 50 T 170 45 T 190 52"
                        fill="none"
                        stroke="#ec4899"
                        strokeWidth="2"
                      />
                    </svg>
                  </div>
                  <div className="space-y-1 text-[10px] text-gray-500 pt-1">
                    <div className="flex justify-between items-center">
                      <span>Morph</span>
                      <span className="font-mono">平均 3.06%</span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span>Together</span>
                      <span className="font-mono">平均 3.29%</span>
                    </div>
                  </div>
                </div>
              </div>
            </section>

            {/* 4. SECTION: Uptime */}
            <section id="uptime" className="space-y-4 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <Clock className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">可用率与稳定性</h2>
                </div>
                <p className="text-xs text-gray-500 mt-1 max-w-3xl leading-relaxed">
                  在线率指过去 3 天内至少有一家算力提供商正常响应请求的时间占比；可用率指推理请求被成功完成的服务比例。uFreeTokens 会持续监控并在某节点返回异常时自动无感切换至次优节点。
                </p>
              </div>

              {/* Uptime KPI row */}
              <div className="grid grid-cols-2 gap-4 max-w-sm text-xs">
                <div className="p-3 rounded-lg border border-gray-200 bg-white">
                  <span className="text-[11px] text-gray-400 block">在线率 (近 3 天) ⓘ</span>
                  <span className="text-xl font-bold font-mono text-emerald-600">100.00%</span>
                </div>
                <div className="p-3 rounded-lg border border-gray-200 bg-white">
                  <span className="text-[11px] text-gray-400 block">可用率 (近 3 天) ⓘ</span>
                  <span className="text-xl font-bold font-mono text-emerald-600">99.83%</span>
                </div>
              </div>

              {/* 3 Days Availability Segmented Bar */}
              <div className="border border-gray-200 rounded-lg p-4 bg-white space-y-2 text-xs">
                <div className="flex justify-between text-[11px] text-gray-500 font-medium">
                  <span>过去 3 天可用率监控 (9月15日 - 9月18日)</span>
                  <span className="text-emerald-700 font-bold font-mono">整体可用率 99.83%</span>
                </div>
                <div className="grid grid-cols-60 gap-0.5 h-6 w-full bg-gray-50 p-1 rounded border border-gray-100">
                  {Array.from({ length: 60 }).map((_, idx) => (
                    <div
                      key={idx}
                      className={`h-full rounded-xs ${
                        idx === 34 ? 'bg-amber-400' : 'bg-emerald-500'
                      }`}
                      title={idx === 34 ? '服务轻微降级 (98.2%)' : '平稳运行 (100%)'}
                    />
                  ))}
                </div>
                <div className="flex justify-between text-[10px] text-gray-400 pt-1">
                  <span>周二</span>
                  <span>周三</span>
                  <span>周四</span>
                  <span>当前状态</span>
                </div>
              </div>

              {/* Availability over 24h Line Chart */}
              <div className="border border-gray-200 rounded-lg p-4 bg-white space-y-2 text-xs">
                <div className="flex justify-between text-[11px] text-gray-700 font-semibold">
                  <span>过去 24 小时服务可用率对比</span>
                </div>
                <div className="h-28 w-full relative">
                  <svg className="w-full h-full" viewBox="0 0 500 100">
                    <line x1="0" y1="20" x2="500" y2="20" stroke="#f3f4f6" strokeWidth="1" />
                    <line x1="0" y1="50" x2="500" y2="50" stroke="#f3f4f6" strokeWidth="1" />
                    <line x1="0" y1="80" x2="500" y2="80" stroke="#f3f4f6" strokeWidth="1" />
                    <path
                      d="M 0 15 Q 150 14, 250 15 T 400 15 T 500 15"
                      fill="none"
                      stroke="#10b981"
                      strokeWidth="2"
                    />
                    <path
                      d="M 0 45 Q 60 35, 120 60 T 200 40 T 280 75 T 360 45 T 440 50 T 500 48"
                      fill="none"
                      stroke="#f97316"
                      strokeWidth="1.5"
                    />
                  </svg>
                </div>
                <div className="flex justify-between text-[10px] text-gray-400 border-t border-gray-100 pt-2">
                  <div className="flex items-center space-x-4">
                    <span className="flex items-center space-x-1.5">
                      <span className="w-2 h-2 rounded-full bg-emerald-500" />
                      <span className="text-gray-700 font-medium">uFreeTokens 智能路由可用率: 99.91%</span>
                    </span>
                    <span className="flex items-center space-x-1.5">
                      <span className="w-2 h-2 rounded-full bg-orange-500" />
                      <span className="text-gray-500">单节点未开启路由: 94.36%</span>
                    </span>
                  </div>
                  <span>API 自动负载熔断与多节点故障切换</span>
                </div>
              </div>
            </section>

            {/* 5. SECTION: Benchmarks */}
            <section id="benchmarks" className="space-y-4 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3 flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                <div>
                  <div className="flex items-center space-x-2">
                    <BarChart3 className="w-4 h-4 text-purple-600" />
                    <h2 className="text-base font-bold text-gray-900">权威基准评测</h2>
                  </div>
                  <p className="text-xs text-gray-500 mt-1">
                    标准化公开评测集测试得分。百分比数值越高代表能力越优，百分位排名展示了该模型在 uFreeTokens 全平台模型中的相对水准。
                  </p>
                </div>
                {onNavigateToBenchmarks && (
                  <button
                    onClick={onNavigateToBenchmarks}
                    className="flex items-center space-x-1.5 px-3 py-1.5 bg-purple-50 hover:bg-purple-100 text-purple-700 font-medium rounded-lg text-xs border border-purple-200 transition-colors shrink-0 self-start sm:self-auto cursor-pointer"
                  >
                    <span>查看全平台基准测试榜单</span>
                    <ArrowUpRight className="w-3.5 h-3.5" />
                  </button>
                )}
              </div>

              {/* Big Score Card with Normal Distribution Curve */}
              <div className="border border-gray-200 rounded-lg p-5 bg-white space-y-3 text-center">
                <div className="text-3xl sm:text-4xl font-extrabold text-gray-900 font-mono">
                  {model.scores.intelligenceIndex || '39.5'}
                </div>
                <div className="text-xs font-semibold text-gray-600">
                  综合智能评测指数 (Artificial Analysis Intelligence Index)
                </div>

                <div className="max-w-md mx-auto h-16 relative">
                  <svg className="w-full h-full" viewBox="0 0 300 60">
                    <path
                      d="M 10 55 Q 80 50, 120 20 T 150 5 T 180 20 T 220 50 T 290 55"
                      fill="none"
                      stroke="#e5e7eb"
                      strokeWidth="2"
                    />
                    <line x1="210" y1="10" x2="210" y2="55" stroke="#7c3aed" strokeWidth="2" strokeDasharray="2 2" />
                    <circle cx="210" cy="18" r="4" fill="#7c3aed" />
                  </svg>
                </div>
                <div className="text-[11px] text-purple-700 font-semibold">
                  超越全平台 82% 的对比模型
                </div>
              </div>

              {/* Sub-benchmarks grid */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-xs">
                {/* Reasoning category */}
                <div className="border border-gray-200 rounded-lg p-4 bg-white space-y-3">
                  <div className="font-bold text-gray-900 text-xs border-b border-gray-100 pb-2">
                    逻辑推演能力 (Reasoning)
                  </div>
                  <div className="space-y-2.5">
                    <div>
                      <div className="flex justify-between text-[11px]">
                        <span className="text-gray-700 font-medium">HLE (终极智力测试难题集)</span>
                        <span className="font-mono font-bold text-gray-900">39.2%</span>
                      </div>
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div className="h-full bg-purple-600 rounded-full" style={{ width: '39.2%' }} />
                      </div>
                    </div>

                    <div>
                      <div className="flex justify-between text-[11px]">
                        <span className="text-gray-700 font-medium">AA-LCR (超长文本长程推演)</span>
                        <span className="font-mono font-bold text-gray-900">84.0%</span>
                      </div>
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div className="h-full bg-purple-600 rounded-full" style={{ width: '84.0%' }} />
                      </div>
                    </div>

                    <div>
                      <div className="flex justify-between text-[11px]">
                        <span className="text-gray-700 font-medium">GDPval-AA (高经济价值专业任务)</span>
                        <span className="font-mono font-bold text-gray-900">56.6%</span>
                      </div>
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div className="h-full bg-purple-600 rounded-full" style={{ width: '56.6%' }} />
                      </div>
                    </div>
                  </div>
                </div>

                {/* Coding & Knowledge */}
                <div className="border border-gray-200 rounded-lg p-4 bg-white space-y-3">
                  <div className="font-bold text-gray-900 text-xs border-b border-gray-100 pb-2">
                    代码编程与常识知识 (Coding & Knowledge)
                  </div>
                  <div className="space-y-2.5">
                    <div>
                      <div className="flex justify-between text-[11px]">
                        <span className="text-gray-700 font-medium">SciCode (科学计算与算法代码)</span>
                        <span className="font-mono font-bold text-blue-600">51.9%</span>
                      </div>
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div className="h-full bg-blue-600 rounded-full" style={{ width: '51.9%' }} />
                      </div>
                    </div>

                    <div>
                      <div className="flex justify-between text-[11px]">
                        <span className="text-gray-700 font-medium">AA-Omniscience Accuracy (常识知识问答准确率)</span>
                        <span className="font-mono font-bold text-emerald-600">46.4%</span>
                      </div>
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div className="h-full bg-emerald-600 rounded-full" style={{ width: '46.4%' }} />
                      </div>
                    </div>

                    <div>
                      <div className="flex justify-between text-[11px]">
                        <span className="text-gray-700 font-medium">防幻觉抵抗能力 (Non-Hallucination Rate)</span>
                        <span className="font-mono font-bold text-amber-600">96.5%</span>
                      </div>
                      <div className="h-1.5 bg-gray-100 rounded-full mt-1 overflow-hidden">
                        <div className="h-full bg-amber-500 rounded-full" style={{ width: '96.5%' }} />
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </section>

            {/* 6. SECTION: Apps */}
            <section id="apps" className="space-y-3 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <Grid className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">热门生态应用</h2>
                </div>
                <p className="text-xs text-gray-500 mt-1">
                  调用该模型流量最多的公开应用与工作流。这能够清晰体现真实生产环境的实际工作负载，并为模型的最佳适用场景提供有力参考。
                </p>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-6 items-center">
                {/* Apps list */}
                <div className="space-y-2 text-xs">
                  {[
                    { rank: 1, name: 'Hermes Agent', desc: '开源全自主智能体框架与执行引擎', tokens: '1.86T tokens' },
                    { rank: 2, name: 'pi', desc: '个性化长程智能对话助手', tokens: '700B tokens' },
                    { rank: 3, name: 'DeepSeek Harness', desc: '自动化代码重构与测试驱动套件', tokens: '562B tokens' },
                    { rank: 4, name: 'omp', desc: '企业级大规模非结构化数据处理管道', tokens: '530B tokens' },
                    { rank: 5, name: 'Claude Code', desc: '开发者命令行终端智能体工具', tokens: '520B tokens' },
                  ].map((app) => (
                    <div
                      key={app.rank}
                      className="flex items-center justify-between p-2.5 rounded-lg border border-gray-100 hover:bg-gray-50 transition-colors"
                    >
                      <div className="flex items-center space-x-2.5">
                        <span className="text-gray-400 font-mono text-[11px] w-4">{app.rank}.</span>
                        <div>
                          <div className="font-semibold text-gray-900">{app.name}</div>
                          <div className="text-[10px] text-gray-400 truncate max-w-[200px]">{app.desc}</div>
                        </div>
                      </div>
                      <span className="font-mono text-[11px] text-gray-600">{app.tokens}</span>
                    </div>
                  ))}
                </div>

                {/* Apps Workload Bar Chart */}
                <div className="border border-gray-200 rounded-lg p-4 bg-white space-y-2 text-xs">
                  <div className="font-semibold text-gray-700 text-xs">随时间的负载流量分布趋势</div>
                  <div className="h-40 flex items-end justify-between gap-2 pt-4">
                    {[
                      { date: '9月10日', h: '45%' },
                      { date: '9月12日', h: '65%' },
                      { date: '9月14日', h: '60%' },
                      { date: '9月16日', h: '85%' },
                      { date: '9月18日', h: '100%' },
                    ].map((bar, i) => (
                      <div key={i} className="flex-1 flex flex-col items-center gap-1.5 h-full justify-end">
                        <div
                          className="w-full bg-purple-600/80 hover:bg-purple-600 rounded-t-sm transition-all"
                          style={{ height: bar.h }}
                        />
                        <span className="text-[10px] text-gray-400">{bar.date}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            </section>

            {/* 7. SECTION: Activity */}
            <section id="activity" className="space-y-3 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <TrendingUp className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">调用活跃度</h2>
                </div>
                <p className="text-xs text-gray-500 mt-1">
                  该模型随时间推移的整体 Token 吞吐消耗量与请求频次分布。
                </p>
              </div>

              <div className="border border-gray-200 rounded-lg p-4 bg-white space-y-3 text-xs">
                <div className="flex items-center justify-between">
                  <span className="px-2 py-1 bg-gray-100 rounded text-gray-700 text-xs font-medium border">
                    Tokens 体量 ▾
                  </span>
                  <div className="flex items-center space-x-4 text-[11px]">
                    <span className="flex items-center space-x-1">
                      <span className="w-2 h-2 rounded-full bg-blue-500" />
                      <span>Prompt 输入 (2.15T)</span>
                    </span>
                    <span className="flex items-center space-x-1">
                      <span className="w-2 h-2 rounded-full bg-pink-500" />
                      <span>Reasoning 思考 (24.5B)</span>
                    </span>
                    <span className="flex items-center space-x-1">
                      <span className="w-2 h-2 rounded-full bg-purple-600" />
                      <span>Completion 生成 (13.8B)</span>
                    </span>
                  </div>
                </div>

                {/* Stacked Bars Graph */}
                <div className="h-44 flex items-end justify-between gap-3 pt-6 border-b border-gray-100 pb-2">
                  {[
                    { date: '9月10日', p: 35, r: 5, c: 15 },
                    { date: '9月11日', p: 55, r: 8, c: 20 },
                    { date: '9月12日', p: 50, r: 7, c: 18 },
                    { date: '9月13日', p: 48, r: 6, c: 17 },
                    { date: '9月14日', p: 70, r: 10, c: 25 },
                    { date: '9月15日', p: 68, r: 9, c: 24 },
                    { date: '9月16日', p: 75, r: 12, c: 28 },
                    { date: '9月17日', p: 85, r: 14, c: 32 },
                  ].map((item, idx) => (
                    <div key={idx} className="flex-1 flex flex-col items-center h-full justify-end">
                      <div className="w-full flex flex-col justify-end space-y-0.5">
                        <div className="w-full bg-purple-600 rounded-t-xs" style={{ height: `${item.c}px` }} />
                        <div className="w-full bg-pink-500" style={{ height: `${item.r}px` }} />
                        <div className="w-full bg-blue-500" style={{ height: `${item.p}px` }} />
                      </div>
                      <span className="text-[10px] text-gray-400 mt-2">{item.date}</span>
                    </div>
                  ))}
                </div>
                <div className="text-[10px] text-gray-400 leading-normal">
                  Prompt Tokens 衡量用户输入与上下文体量；Reasoning Tokens 反映模型生成最终回复前的内部深度思考消耗；Completion Tokens 则代表模型实际输出的回答长度。
                </div>
              </div>
            </section>

            {/* 8. SECTION: FAQ */}
            <section id="faq" className="space-y-3 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <HelpCircle className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">常见问题解答</h2>
                </div>
              </div>

              <div className="divide-y divide-gray-200 border border-gray-200 rounded-lg overflow-hidden bg-white text-xs">
                {faqs.map((faq, idx) => {
                  const isOpen = openFaqIndex === idx;
                  return (
                    <div key={idx} className="transition-colors">
                      <button
                        onClick={() => setOpenFaqIndex(isOpen ? null : idx)}
                        className="w-full flex items-center justify-between p-3.5 text-left font-medium text-gray-800 hover:bg-gray-50/70 cursor-pointer"
                      >
                        <span>{faq.q}</span>
                        {isOpen ? (
                          <ChevronUp className="w-4 h-4 text-gray-400 shrink-0 ml-2" />
                        ) : (
                          <ChevronDown className="w-4 h-4 text-gray-400 shrink-0 ml-2" />
                        )}
                      </button>
                      {isOpen && (
                        <div className="p-3.5 pt-0 text-gray-600 text-xs leading-relaxed bg-gray-50/40 border-t border-gray-100">
                          {faq.a}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </section>

            {/* 9. SECTION: Explore */}
            <section id="explore" className="space-y-3 pt-2 scroll-mt-28">
              <div className="border-b border-gray-200 pb-3">
                <div className="flex items-center space-x-2">
                  <Compass className="w-4 h-4 text-purple-600" />
                  <h2 className="text-base font-bold text-gray-900">探索更多模型</h2>
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="flex items-center space-x-1.5 px-2.5 py-1 bg-gray-100 hover:bg-gray-200 rounded text-gray-700 cursor-pointer">
                  <span>具备视觉理解的多模态 LLM 模型专题</span>
                  <span className="text-[10px] text-gray-400 bg-white px-1 rounded">专题集合</span>
                </span>
                <span className="flex items-center space-x-1.5 px-2.5 py-1 bg-gray-100 hover:bg-gray-200 rounded text-gray-700 cursor-pointer">
                  <span>AI 大模型权威竞技场综合排行榜</span>
                  <span className="text-[10px] text-gray-400 bg-white px-1 rounded">排行榜</span>
                </span>
              </div>

              <div className="pt-2">
                <h3 className="text-xs font-bold text-gray-800 mb-2">
                  来自 <span className="text-purple-600 underline cursor-pointer">{model.providerDisplay}</span> 的更多模型
                </h3>
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                  {relatedModels.map((rel) => (
                    <div
                      key={rel.id}
                      onClick={() => onSelectOtherModel(rel)}
                      className="p-3 rounded-lg border border-gray-200 hover:border-purple-300 hover:shadow-xs transition-all cursor-pointer bg-white group"
                    >
                      <div className="flex items-center space-x-1.5">
                        <div className={`w-3.5 h-3.5 rounded text-[8px] flex items-center justify-center font-bold ${rel.iconBg}`}>
                          ▲
                        </div>
                        <h4 className="font-bold text-gray-900 text-xs group-hover:text-purple-600 truncate">
                          {rel.name}
                        </h4>
                      </div>
                      <p className="text-gray-500 text-[11px] line-clamp-2 mt-1">
                        {rel.description}
                      </p>
                      <div className="text-[10px] text-gray-400 mt-2 flex items-center justify-between">
                        <span>{rel.contextDisplay}</span>
                        <span className="font-mono text-gray-700">{rel.inputPriceDisplay}</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>
          </div>
        </div>
      </div>

      {/* Footer matching uFreeTokens layout */}
      <footer className="mt-16 border-t border-gray-200 bg-gray-50/50 py-12 px-4 lg:px-8 text-xs text-gray-600">
        <div className="max-w-7xl mx-auto space-y-10">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-8">
            {/* Logo column */}
            <div className="col-span-2 md:col-span-1 space-y-2">
              <div className="flex items-center space-x-1.5 font-bold text-gray-900">
                <Layers className="w-4 h-4 text-purple-600" />
                <span className="text-sm">uFreeTokens</span>
              </div>
              <p className="text-[11px] text-gray-400">
                © 2026 uFreeTokens, Inc. 保留所有权利
              </p>
            </div>

            {/* Product */}
            <div className="space-y-2">
              <div className="font-bold text-gray-900 text-xs">产品与服务</div>
              <div className="space-y-1.5 text-[11px] text-gray-500">
                <div className="hover:text-gray-900 cursor-pointer">Chat 对话</div>
                <div className="hover:text-gray-900 cursor-pointer">模型排行榜</div>
                <div className="hover:text-gray-900 cursor-pointer">基准测试</div>
                <div className="hover:text-gray-900 cursor-pointer">热门应用</div>
                <div className="hover:text-gray-900 cursor-pointer">发现探索</div>
                <div className="hover:text-gray-900 cursor-pointer" onClick={onBack}>模型库</div>
                <div className="hover:text-gray-900 cursor-pointer">价格方案</div>
              </div>
            </div>

            {/* Developer */}
            <div className="space-y-2">
              <div className="font-bold text-gray-900 text-xs">开发者生态</div>
              <div className="space-y-1.5 text-[11px] text-gray-500">
                <div className="hover:text-gray-900 cursor-pointer">开发接入文档</div>
                <div className="hover:text-gray-900 cursor-pointer">API 接口参考</div>
                <div className="hover:text-gray-900 cursor-pointer">开放平台</div>
                <div className="hover:text-gray-900 cursor-pointer">系统服务状态</div>
              </div>
            </div>

            {/* Connect */}
            <div className="space-y-2">
              <div className="font-bold text-gray-900 text-xs">社区与社交</div>
              <div className="space-y-1.5 text-[11px] text-gray-500">
                <div className="hover:text-gray-900 cursor-pointer">Discord</div>
                <div className="hover:text-gray-900 cursor-pointer">GitHub</div>
                <div className="hover:text-gray-900 cursor-pointer">LinkedIn</div>
                <div className="hover:text-gray-900 cursor-pointer">X (Twitter)</div>
                <div className="hover:text-gray-900 cursor-pointer">YouTube</div>
              </div>
            </div>
          </div>

          {/* Newsletter Subscribe */}
          <div className="pt-6 border-t border-gray-200 flex flex-wrap items-center justify-between gap-4">
            <div className="space-y-1">
              <div className="font-bold text-gray-900 text-xs">订阅 uFreeTokens 官方周刊</div>
              <div className="text-[11px] text-gray-500">获取最新模型动态、实测性能报告与前沿技术解读。每周一期。</div>
            </div>

            <div className="flex items-center space-x-2 w-full sm:w-auto">
              <input
                type="email"
                value={emailInput}
                onChange={(e) => setEmailInput(e.target.value)}
                placeholder="gaojing850063636@gmail.com"
                className="bg-white border border-gray-200 rounded-md px-3 py-1.5 text-xs w-60 focus:outline-none focus:border-purple-500"
              />
              <button
                onClick={() => {
                  setSubscribed(true);
                  setTimeout(() => setSubscribed(false), 3000);
                }}
                className="px-4 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-md text-xs font-semibold shadow-xs transition-colors cursor-pointer"
              >
                {subscribed ? '已订阅！' : '订阅'}
              </button>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
};
