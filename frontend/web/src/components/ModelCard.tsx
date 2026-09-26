import React, { useState } from 'react';
import {
  ArrowUpRight,
  Info,
  Pin,
  Check,
  Copy,
  Scale,
  Play,
  Terminal,
  FileCode
} from 'lucide-react';
import { Model } from '../types';

interface ModelCardProps {
  model: Model;
  isPinned: boolean;
  isInCompare: boolean;
  onTogglePin: (id: string) => void;
  onToggleCompare: (model: Model) => void;
  onSelectModel: (model: Model) => void;
  onOpenPlayground: (model: Model) => void;
  onSelectProvider: (provider: string) => void;
}

export const ModelCard: React.FC<ModelCardProps> = ({
  model,
  isPinned,
  isInCompare,
  onTogglePin,
  onToggleCompare,
  onSelectModel,
  onOpenPlayground,
  onSelectProvider,
}) => {
  const [copied, setCopied] = useState(false);

  const handleCopyId = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard.writeText(model.id);
    setCopied(true);
    setTimeout(() => setCopied(false), 1800);
  };

  return (
    <div className="group relative py-3.5 px-3 -mx-2 rounded-lg hover:bg-gray-50/80 transition-all border border-transparent hover:border-gray-200/70">
      <div className="flex items-start justify-between gap-4">
        {/* Left main info */}
        <div className="space-y-1.5 max-w-3xl flex-1">
          {/* Header row: Icon + Title + Badges */}
          <div className="flex items-center space-x-2 flex-wrap gap-y-1">
            <div
              className={`w-4 h-4 rounded flex items-center justify-center text-[9px] font-bold shadow-xs shrink-0 ${model.iconBg}`}
            >
              ▲
            </div>

            <h3
              onClick={() => onSelectModel(model)}
              className="font-bold text-gray-900 text-xs hover:text-purple-600 cursor-pointer flex items-center space-x-1 transition-colors"
            >
              <span>{model.name}</span>
              <ArrowUpRight className="w-3 h-3 text-gray-300 group-hover:text-purple-400 transition-colors" />
            </h3>

            {model.badge && (
              <span
                className={`px-1.5 py-0.5 text-[10px] rounded border font-medium leading-none ${
                  model.badgeColor || 'bg-purple-100 text-purple-700 border-purple-200'
                }`}
              >
                {model.badge}
              </span>
            )}

            {model.hasDiscount && !model.badge && (
              <span className="px-1.5 py-0.5 text-[10px] rounded border font-medium leading-none bg-emerald-50 text-emerald-700 border-emerald-200">
                优惠 {model.discountPercent}%
              </span>
            )}

            {/* Quick copy ID pill */}
            <button
              onClick={handleCopyId}
              title={`复制模型标识: ${model.id}`}
              className="opacity-0 group-hover:opacity-100 transition-opacity flex items-center space-x-1 text-[10px] text-gray-400 hover:text-gray-700 bg-white border border-gray-200 hover:border-gray-300 px-1.5 py-0.5 rounded cursor-pointer"
            >
              {copied ? (
                <>
                  <Check className="w-2.5 h-2.5 text-emerald-600" />
                  <span className="text-emerald-600 font-medium">已复制</span>
                </>
              ) : (
                <>
                  <Copy className="w-2.5 h-2.5" />
                  <span>{model.id}</span>
                </>
              )}
            </button>
          </div>

          {/* Description */}
          <p
            onClick={() => onSelectModel(model)}
            className="text-gray-500 text-xxs leading-relaxed cursor-pointer hover:text-gray-700 line-clamp-2"
          >
            {model.description}
          </p>

          {/* Meta line */}
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xxs text-gray-400 pt-0.5">
            <span>
              来自{' '}
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onSelectProvider(model.provider);
                }}
                className="underline hover:text-purple-700 text-gray-500 font-medium"
              >
                {model.provider}
              </button>
            </span>
            <span>•</span>
            <span>{model.date}</span>

            {model.contextDisplay && (
              <>
                <span>•</span>
                <span className="text-gray-600 font-medium">{model.contextDisplay}</span>
              </>
            )}

            <span>•</span>
            <span className="text-gray-700 font-semibold">{model.inputPriceDisplay}</span>

            {model.outputPriceDisplay && (
              <>
                <span>•</span>
                <span className="text-gray-700 font-semibold">{model.outputPriceDisplay}</span>
              </>
            )}

            {/* Modalities badges */}
            <span className="hidden sm:inline-flex items-center space-x-1 pl-1">
              {model.modalities.map((m) => (
                <span
                  key={m}
                  className="px-1 py-0.2 bg-gray-100 text-gray-500 rounded text-[9px] uppercase tracking-wider"
                >
                  {m}
                </span>
              ))}
            </span>
          </div>
        </div>

        {/* Right tokens stats + quick action buttons */}
        <div className="flex flex-col items-end space-y-2 shrink-0">
          {model.tokensDisplay && (
            <div className="flex items-center space-x-1 text-xxs text-gray-400 shrink-0">
              <span className="font-mono">{model.tokensDisplay}</span>
              <Info className="w-3 h-3 text-gray-300 hover:text-gray-500 cursor-pointer" />
            </div>
          )}

          {/* Action buttons toolbar (pinned, compare, playground, details) */}
          <div className="flex items-center space-x-1 opacity-80 sm:opacity-0 group-hover:opacity-100 transition-opacity">
            <button
              onClick={(e) => {
                e.stopPropagation();
                onTogglePin(model.id);
              }}
              title={isPinned ? '取消固定' : '固定该模型'}
              className={`p-1 rounded text-xs transition-colors cursor-pointer ${
                isPinned
                  ? 'bg-purple-100 text-purple-700'
                  : 'text-gray-400 hover:text-gray-800 hover:bg-gray-100'
              }`}
            >
              <Pin className="w-3.5 h-3.5" />
            </button>

            <button
              onClick={(e) => {
                e.stopPropagation();
                onToggleCompare(model);
              }}
              title={isInCompare ? '从对比中移除' : '加入模型对比'}
              className={`flex items-center space-x-1 px-1.5 py-1 rounded text-xs transition-colors cursor-pointer ${
                isInCompare
                  ? 'bg-purple-600 text-white font-medium'
                  : 'text-gray-500 hover:text-gray-900 hover:bg-gray-100 border border-gray-200 bg-white'
              }`}
            >
              <Scale className="w-3 h-3" />
              <span className="text-[10px] hidden sm:inline">
                {isInCompare ? '已对比' : '对比'}
              </span>
            </button>

            <button
              onClick={(e) => {
                e.stopPropagation();
                onOpenPlayground(model);
              }}
              title="在 Playground 中实时测试"
              className="flex items-center space-x-1 px-2 py-1 rounded text-xs bg-purple-50 text-purple-700 hover:bg-purple-100 border border-purple-200 font-medium transition-colors cursor-pointer"
            >
              <Play className="w-3 h-3 fill-purple-600" />
              <span className="text-[10px] hidden sm:inline">测试</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
