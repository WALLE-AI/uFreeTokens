import React, { useState } from 'react';
import {
  X,
  Copy,
  Check,
  Play,
  Scale,
  Cpu,
  ShieldCheck,
  Globe,
  Tag,
  Zap,
  DollarSign,
  Calculator,
  Code2
} from 'lucide-react';
import { Model } from '../types';
import { ProviderIcon } from './ProviderIcon';

interface ModelDetailModalProps {
  model: Model | null;
  onClose: () => void;
  onOpenPlayground: (model: Model) => void;
  onToggleCompare: (model: Model) => void;
  isInCompare: boolean;
}

export const ModelDetailModal: React.FC<ModelDetailModalProps> = ({
  model,
  onClose,
  onOpenPlayground,
  onToggleCompare,
  isInCompare,
}) => {
  if (!model) return null;

  const [activeCodeTab, setActiveCodeTab] = useState<'curl' | 'python' | 'node'>('python');
  const [copiedCode, setCopiedCode] = useState(false);
  const [copiedId, setCopiedId] = useState(false);

  // Cost calculator states
  const [calcInputTokens, setCalcInputTokens] = useState<number>(100000);
  const [calcOutputTokens, setCalcOutputTokens] = useState<number>(20000);

  const calculatedInputCost = (calcInputTokens / 1_000_000) * model.inputPricePerM;
  const calculatedOutputCost = (calcOutputTokens / 1_000_000) * model.outputPricePerM;
  const totalEstimatedCost = calculatedInputCost + calculatedOutputCost;

  const handleCopyId = () => {
    navigator.clipboard.writeText(model.id);
    setCopiedId(true);
    setTimeout(() => setCopiedId(false), 2000);
  };

  const getCodeSnippet = () => {
    if (activeCodeTab === 'curl') {
      return `curl https://ufreetokens.com/api/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\
  -d '{
    "model": "${model.id}",
    "messages": [
      {
        "role": "user",
        "content": "请简要分析该模型的性能与适用场景"
      }
    ]
  }'`;
    }

    if (activeCodeTab === 'python') {
      return `from openai import OpenAI
import os

client = OpenAI(
  base_url="https://ufreetokens.com/api/v1",
  api_key=os.environ.get("UFREETOKENS_API_KEY"),
)

completion = client.chat.completions.create(
  model="${model.id}",
  messages=[
    {
      "role": "user",
      "content": "请用一段话总结该模型的优势",
    }
  ],
)

print(completion.choices[0].message.content)`;
    }

    return `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://ufreetokens.com/api/v1",
  apiKey: process.env.UFREETOKENS_API_KEY,
});

async function main() {
  const completion = await client.chat.completions.create({
    model: "${model.id}",
    messages: [
      { role: "user", content: "请用简洁的代码演示如何使用该接口" }
    ],
  });

  console.log(completion.choices[0].message.content);
}

main();`;
  };

  const handleCopyCode = () => {
    navigator.clipboard.writeText(getCodeSnippet());
    setCopiedCode(true);
    setTimeout(() => setCopiedCode(false), 2000);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3 sm:p-4 bg-black/40 backdrop-blur-xs">
      <div
        className="bg-white rounded-xl shadow-2xl max-w-3xl w-full max-h-[90vh] flex flex-col overflow-hidden border border-gray-200 animate-in fade-in zoom-in-95 duration-150"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 py-4 border-b border-gray-100 flex items-start justify-between bg-gray-50/70">
          <div className="flex items-start space-x-3">
            <ProviderIcon
              provider={model.provider}
              className="w-6 h-6 rounded mt-0.5"
              fallbackBg={model.iconBg}
              fallbackTextClassName="text-xs"
            />
            <div>
              <div className="flex items-center space-x-2 flex-wrap gap-y-1">
                <h2 className="text-base font-bold text-gray-900">{model.name}</h2>
                {model.badge && (
                  <span
                    className={`px-1.5 py-0.5 text-[10px] rounded border font-medium ${
                      model.badgeColor || 'bg-purple-100 text-purple-700 border-purple-200'
                    }`}
                  >
                    {model.badge}
                  </span>
                )}
              </div>
              <div className="flex items-center space-x-2 text-xs text-gray-500 mt-1">
                <span className="font-mono bg-white px-1.5 py-0.5 rounded border border-gray-200 text-gray-700">
                  {model.id}
                </span>
                <button
                  onClick={handleCopyId}
                  className="flex items-center space-x-1 text-purple-600 hover:text-purple-700"
                >
                  {copiedId ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
                  <span>{copiedId ? '已复制' : '复制ID'}</span>
                </button>
                <span>•</span>
                <span>厂商: {model.providerDisplay}</span>
                <span>•</span>
                <span>发布于: {model.date}</span>
              </div>
            </div>
          </div>

          <button
            onClick={onClose}
            className="p-1.5 rounded-lg text-gray-400 hover:text-gray-700 hover:bg-gray-100 transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Modal Scrollable Body */}
        <div className="flex-1 overflow-y-auto p-5 space-y-6 text-xs">
          {/* Overview */}
          <div>
            <h3 className="text-xs font-bold text-gray-900 uppercase tracking-wider mb-1.5">
              模型简介
            </h3>
            <p className="text-gray-600 leading-relaxed text-xs sm:text-[13px]">
              {model.description}
            </p>
          </div>

          {/* Quick Specs Grid */}
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 bg-gray-50 p-3.5 rounded-lg border border-gray-200/80">
            <div>
              <span className="text-gray-400 text-[11px] block">上下文容量</span>
              <span className="text-gray-900 font-bold text-sm">
                {model.contextDisplay || '未标明'}
              </span>
            </div>
            <div>
              <span className="text-gray-400 text-[11px] block">单次最大输出</span>
              <span className="text-gray-900 font-bold text-sm">
                {model.maxOutputTokens.toLocaleString()} tokens
              </span>
            </div>
            <div>
              <span className="text-gray-400 text-[11px] block">输入价格</span>
              <span className="text-gray-900 font-bold text-sm">{model.inputPriceDisplay}</span>
            </div>
            <div>
              <span className="text-gray-400 text-[11px] block">输出价格</span>
              <span className="text-gray-900 font-bold text-sm">
                {model.outputPriceDisplay || '免费 / 不适用'}
              </span>
            </div>
          </div>

          {/* Cost Calculator */}
          <div className="border border-purple-100 bg-purple-50/40 rounded-lg p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-1.5 font-semibold text-gray-900">
                <Calculator className="w-4 h-4 text-purple-600" />
                <span>Token 成本预估器</span>
              </div>
              <div className="text-right">
                <span className="text-gray-500 text-[11px] mr-2">预计总成本:</span>
                <span className="text-base font-bold text-purple-900 font-mono">
                  ${totalEstimatedCost.toFixed(4)} USD
                </span>
              </div>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 pt-1">
              <div>
                <div className="flex justify-between text-gray-600 mb-1 font-medium">
                  <span>Input Tokens: {calcInputTokens.toLocaleString()}</span>
                  <span className="font-mono text-purple-700">${calculatedInputCost.toFixed(4)}</span>
                </div>
                <input
                  type="range"
                  min={1000}
                  max={2000000}
                  step={5000}
                  value={calcInputTokens}
                  onChange={(e) => setCalcInputTokens(Number(e.target.value))}
                  className="purple-track"
                />
              </div>

              <div>
                <div className="flex justify-between text-gray-600 mb-1 font-medium">
                  <span>Output Tokens: {calcOutputTokens.toLocaleString()}</span>
                  <span className="font-mono text-purple-700">${calculatedOutputCost.toFixed(4)}</span>
                </div>
                <input
                  type="range"
                  min={1000}
                  max={500000}
                  step={1000}
                  value={calcOutputTokens}
                  onChange={(e) => setCalcOutputTokens(Number(e.target.value))}
                  className="purple-track"
                />
              </div>
            </div>
          </div>

          {/* Capabilities & Security */}
          <div className="space-y-2">
            <h3 className="text-xs font-bold text-gray-900 uppercase tracking-wider">
              特性与合规支持
            </h3>
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
              <div className="flex items-center space-x-2 p-2 rounded border border-gray-100 bg-white">
                <ShieldCheck
                  className={`w-4 h-4 ${
                    model.zeroDataRetention ? 'text-emerald-600' : 'text-gray-300'
                  }`}
                />
                <div>
                  <div className="font-medium text-gray-800">零数据保留</div>
                  <div className="text-[10px] text-gray-400">
                    {model.zeroDataRetention ? '已通过 ZDR 认证' : '常规服务条款'}
                  </div>
                </div>
              </div>

              <div className="flex items-center space-x-2 p-2 rounded border border-gray-100 bg-white">
                <Globe className="w-4 h-4 text-blue-600" />
                <div>
                  <div className="font-medium text-gray-800">区域合规路由</div>
                  <div className="text-[10px] text-gray-400">
                    支持: {model.inRegionRouting.join(', ')}
                  </div>
                </div>
              </div>

              <div className="flex items-center space-x-2 p-2 rounded border border-gray-100 bg-white">
                <Zap className="w-4 h-4 text-purple-600" />
                <div>
                  <div className="font-medium text-gray-800">工具调用能力</div>
                  <div className="text-[10px] text-gray-400">
                    评测得分: {model.toolCallingCapability}%
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* Benchmarks Scores */}
          <div className="space-y-2">
            <h3 className="text-xs font-bold text-gray-900 uppercase tracking-wider">
              基准评测成绩 (Artificial Analysis)
            </h3>
            <div className="grid grid-cols-3 gap-3">
              <div className="p-3 rounded-lg border border-gray-200 bg-gray-50 text-center">
                <div className="text-xl font-bold text-gray-900 font-mono">
                  {model.scores.intelligenceIndex}
                </div>
                <div className="text-[11px] text-gray-500 font-medium mt-0.5">综合智能指数</div>
              </div>
              <div className="p-3 rounded-lg border border-gray-200 bg-gray-50 text-center">
                <div className="text-xl font-bold text-gray-900 font-mono">
                  {model.scores.codingIndex}
                </div>
                <div className="text-[11px] text-gray-500 font-medium mt-0.5">代码编程指数</div>
              </div>
              <div className="p-3 rounded-lg border border-gray-200 bg-gray-50 text-center">
                <div className="text-xl font-bold text-gray-900 font-mono">
                  {model.scores.agenticIndex}
                </div>
                <div className="text-[11px] text-gray-500 font-medium mt-0.5">智能体编排指数</div>
              </div>
            </div>
          </div>

          {/* Integration Code Snippets */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-1.5 font-bold text-gray-900 text-xs uppercase tracking-wider">
                <Code2 className="w-4 h-4 text-gray-500" />
                <span>快速接入代码</span>
              </div>
              <div className="flex items-center space-x-1 bg-gray-100 p-0.5 rounded">
                {(['python', 'node', 'curl'] as const).map((tab) => (
                  <button
                    key={tab}
                    onClick={() => setActiveCodeTab(tab)}
                    className={`px-2 py-0.5 rounded text-[11px] font-medium transition-colors ${
                      activeCodeTab === tab
                        ? 'bg-white text-purple-700 shadow-xs'
                        : 'text-gray-500 hover:text-gray-900'
                    }`}
                  >
                    {tab.toUpperCase()}
                  </button>
                ))}
              </div>
            </div>

            <div className="relative group">
              <pre className="p-3.5 bg-gray-900 text-gray-100 rounded-lg overflow-x-auto text-[11px] font-mono leading-relaxed max-h-48">
                <code>{getCodeSnippet()}</code>
              </pre>
              <button
                onClick={handleCopyCode}
                className="absolute right-2 top-2 px-2 py-1 rounded bg-gray-800/80 hover:bg-gray-700 text-gray-200 text-[10px] flex items-center space-x-1 border border-gray-700 cursor-pointer backdrop-blur-xs"
              >
                {copiedCode ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
                <span>{copiedCode ? '已复制代码' : '复制代码'}</span>
              </button>
            </div>
          </div>
        </div>

        {/* Footer Actions */}
        <div className="p-4 border-t border-gray-200 bg-gray-50 flex items-center justify-between">
          <button
            onClick={() => onToggleCompare(model)}
            className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border transition-colors ${
              isInCompare
                ? 'bg-purple-600 text-white border-purple-600'
                : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-100'
            }`}
          >
            <Scale className="w-3.5 h-3.5" />
            <span>{isInCompare ? '已加入模型对比' : '加入模型对比'}</span>
          </button>

          <div className="flex items-center space-x-2">
            <button
              onClick={onClose}
              className="px-3.5 py-1.5 rounded-lg text-xs text-gray-600 hover:bg-gray-200 transition-colors"
            >
              关闭
            </button>
            <button
              onClick={() => {
                onClose();
                onOpenPlayground(model);
              }}
              className="flex items-center space-x-1.5 px-4 py-1.5 rounded-lg text-xs bg-purple-600 hover:bg-purple-700 text-white font-medium shadow-xs transition-colors"
            >
              <Play className="w-3.5 h-3.5 fill-white" />
              <span>在 Playground 中测试</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
