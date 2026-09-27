import React, { useState } from 'react';
import { Info, Pin, Check, Copy, Scale, Play, Zap } from 'lucide-react';
import { Model } from '../types';
import { ProviderIcon } from './ProviderIcon';

interface ModelGridCardProps {
  model: Model;
  isPinned: boolean;
  isInCompare: boolean;
  onTogglePin: (id: string) => void;
  onToggleCompare: (model: Model) => void;
  onSelectModel: (model: Model) => void;
  onOpenPlayground: (model: Model) => void;
  onSelectProvider: (provider: string) => void;
}

// ModelGridCard 是模型市场主视图（模型库）的默认卡片：竖排、定高，适合在一个
// 响应式多列网格里横向铺开浏览，和 ModelCard（横向列表行，信息密度更高、
// 适合快速扫读很多条）是两种不同场景，互不替代——ModelCard 仍然是"列表视图"
// 用的组件。
export const ModelGridCard: React.FC<ModelGridCardProps> = ({
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
    <div className="group relative flex flex-col h-full rounded-xl border border-gray-200 bg-white p-3.5 hover:border-purple-300 hover:shadow-md transition-all">
      {/* Pin toggle, top-right corner */}
      <button
        onClick={(e) => {
          e.stopPropagation();
          onTogglePin(model.id);
        }}
        title={isPinned ? '取消固定' : '固定该模型'}
        className={`absolute top-2.5 right-2.5 p-1 rounded transition-colors cursor-pointer ${
          isPinned
            ? 'text-purple-600'
            : 'text-gray-300 opacity-0 group-hover:opacity-100 hover:text-gray-600'
        }`}
      >
        <Pin className="w-3.5 h-3.5" fill={isPinned ? 'currentColor' : 'none'} />
      </button>

      {/* Header: icon + provider */}
      <div className="flex items-center gap-1.5 pr-5">
        <ProviderIcon
          provider={model.provider}
          className="w-5 h-5 rounded"
          fallbackBg={model.iconBg}
          fallbackTextClassName="text-[10px]"
        />
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onSelectProvider(model.provider);
          }}
          className="text-[11px] text-gray-400 hover:text-purple-700 hover:underline truncate"
        >
          {model.providerDisplay}
        </button>
      </div>

      {/* Title */}
      <h3
        onClick={() => onSelectModel(model)}
        title={model.name}
        className="mt-1.5 font-bold text-gray-900 text-xs leading-snug line-clamp-2 cursor-pointer hover:text-purple-600 transition-colors"
      >
        {model.name}
      </h3>

      {/* Badges */}
      {(model.badge || model.isCallable || (model.hasDiscount && !model.badge)) && (
        <div className="flex flex-wrap items-center gap-1 mt-1.5">
          {model.badge && (
            <span
              className={`px-1.5 py-0.5 text-[10px] rounded border font-medium leading-none ${
                model.badgeColor || 'bg-purple-100 text-purple-700 border-purple-200'
              }`}
            >
              {model.badge}
            </span>
          )}
          {model.isCallable && (
            <span
              title="已连接的 API Key 可以直接调用该模型"
              className="flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] rounded border font-medium leading-none bg-emerald-50 text-emerald-700 border-emerald-200"
            >
              <Zap className="w-2.5 h-2.5" />
              <span>可调用</span>
            </span>
          )}
          {model.hasDiscount && !model.badge && (
            <span className="px-1.5 py-0.5 text-[10px] rounded border font-medium leading-none bg-emerald-50 text-emerald-700 border-emerald-200">
              优惠 {model.discountPercent}%
            </span>
          )}
        </div>
      )}

      {/* Description — flex-1 so every card's footer lines up regardless of description length */}
      <p
        onClick={() => onSelectModel(model)}
        className="mt-1.5 text-gray-500 text-xxs leading-relaxed line-clamp-3 cursor-pointer hover:text-gray-700 flex-1"
      >
        {model.description}
      </p>

      {/* Meta: context + modalities */}
      <div className="flex flex-wrap items-center gap-1.5 mt-2.5 text-xxs text-gray-400">
        {model.contextDisplay && (
          <span className="px-1.5 py-0.5 bg-gray-100 text-gray-600 rounded font-medium">
            {model.contextDisplay}
          </span>
        )}
        {model.modalities.map((m) => (
          <span key={m} className="px-1.5 py-0.5 bg-gray-100 text-gray-500 rounded uppercase tracking-wider text-[9px]">
            {m}
          </span>
        ))}
        {model.tokensDisplay && (
          <span className="ml-auto flex items-center gap-0.5 font-mono">
            {model.tokensDisplay}
            <Info className="w-3 h-3 text-gray-300" />
          </span>
        )}
      </div>

      {/* Price — stacked (not side-by-side) so the "¥X / 百万 ... Token" strings
          have the full card width and don't get cut off mid-word */}
      <div className="mt-2 pt-2 border-t border-gray-100 text-[11px] text-gray-700 font-semibold space-y-0.5">
        <div className="truncate">{model.inputPriceDisplay}</div>
        {model.outputPriceDisplay && <div className="truncate">{model.outputPriceDisplay}</div>}
      </div>

      {/* Copy ID */}
      <button
        onClick={handleCopyId}
        title={`复制模型标识: ${model.id}`}
        className="mt-1.5 flex items-center gap-1 text-[10px] text-gray-400 hover:text-gray-700 self-start cursor-pointer"
      >
        {copied ? (
          <>
            <Check className="w-2.5 h-2.5 text-emerald-600" />
            <span className="text-emerald-600 font-medium">已复制</span>
          </>
        ) : (
          <>
            <Copy className="w-2.5 h-2.5" />
            <span className="truncate max-w-[10rem]">{model.id}</span>
          </>
        )}
      </button>

      {/* Actions */}
      <div className="flex items-center gap-1.5 mt-2.5">
        <button
          onClick={(e) => {
            e.stopPropagation();
            onToggleCompare(model);
          }}
          title={isInCompare ? '从对比中移除' : '加入模型对比'}
          className={`flex-1 flex items-center justify-center gap-1 px-2 py-1.5 rounded text-[11px] transition-colors cursor-pointer ${
            isInCompare
              ? 'bg-purple-600 text-white font-medium'
              : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-gray-200 bg-white'
          }`}
        >
          <Scale className="w-3 h-3" />
          <span>{isInCompare ? '已对比' : '对比'}</span>
        </button>

        <button
          onClick={(e) => {
            e.stopPropagation();
            onOpenPlayground(model);
          }}
          title="在 Playground 中实时测试"
          className="flex-1 flex items-center justify-center gap-1 px-2 py-1.5 rounded text-[11px] bg-purple-50 text-purple-700 hover:bg-purple-100 border border-purple-200 font-medium transition-colors cursor-pointer"
        >
          <Play className="w-3 h-3 fill-purple-600" />
          <span>测试</span>
        </button>
      </div>
    </div>
  );
};
