import React from 'react';
import { ArrowUpRight, Pin, Scale, Play, Copy, Check } from 'lucide-react';
import { Model } from '../types';

interface ModelTableProps {
  models: Model[];
  pinnedModelIds: string[];
  compareModels: Model[];
  onTogglePin: (id: string) => void;
  onToggleCompare: (model: Model) => void;
  onSelectModel: (model: Model) => void;
  onOpenPlayground: (model: Model) => void;
}

export const ModelTable: React.FC<ModelTableProps> = ({
  models,
  pinnedModelIds,
  compareModels,
  onTogglePin,
  onToggleCompare,
  onSelectModel,
  onOpenPlayground,
}) => {
  return (
    <div className="overflow-x-auto border border-gray-200 rounded-lg bg-white shadow-xs">
      <table className="w-full text-left border-collapse text-xs">
        <thead>
          <tr className="bg-gray-50 border-b border-gray-200 text-gray-500 font-medium">
            <th className="py-2.5 px-3">模型名称</th>
            <th className="py-2.5 px-3">厂商</th>
            <th className="py-2.5 px-3">模态支持</th>
            <th className="py-2.5 px-3">上下文长度</th>
            <th className="py-2.5 px-3">输入单价 ($/M)</th>
            <th className="py-2.5 px-3">输出单价 ($/M)</th>
            <th className="py-2.5 px-3 text-center">综合评测</th>
            <th className="py-2.5 px-3 text-right">操作</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {models.map((model) => {
            const isPinned = pinnedModelIds.includes(model.id);
            const isInCompare = compareModels.some((m) => m.id === model.id);

            return (
              <tr
                key={model.id}
                className="hover:bg-gray-50/70 transition-colors group cursor-pointer"
                onClick={() => onSelectModel(model)}
              >
                {/* Model name & icon */}
                <td className="py-3 px-3">
                  <div className="flex items-center space-x-2">
                    <div
                      className={`w-4 h-4 rounded flex items-center justify-center text-[9px] font-bold shadow-xs shrink-0 ${model.iconBg}`}
                    >
                      ▲
                    </div>
                    <div>
                      <div className="font-semibold text-gray-900 group-hover:text-purple-600 flex items-center space-x-1">
                        <span>{model.name}</span>
                        {model.badge && (
                          <span
                            className={`px-1.5 py-0.2 text-[9px] rounded border font-medium ${
                              model.badgeColor || 'bg-purple-100 text-purple-700 border-purple-200'
                            }`}
                          >
                            {model.badge}
                          </span>
                        )}
                      </div>
                      <div className="text-[10px] text-gray-400 font-mono">{model.id}</div>
                    </div>
                  </div>
                </td>

                {/* Provider */}
                <td className="py-3 px-3 text-gray-600 font-medium capitalize">
                  {model.providerDisplay}
                </td>

                {/* Modalities */}
                <td className="py-3 px-3">
                  <div className="flex flex-wrap gap-1">
                    {model.modalities.map((m) => (
                      <span
                        key={m}
                        className="px-1.5 py-0.5 bg-gray-100 text-gray-600 rounded text-[10px] font-mono capitalize"
                      >
                        {m}
                      </span>
                    ))}
                  </div>
                </td>

                {/* Context */}
                <td className="py-3 px-3 text-gray-700 font-medium">
                  {model.contextDisplay || 'N/A'}
                </td>

                {/* Input Price */}
                <td className="py-3 px-3 text-gray-800 font-semibold font-mono">
                  {model.isHourly ? model.inputPriceDisplay : `$${model.inputPricePerM.toFixed(2)}`}
                </td>

                {/* Output Price */}
                <td className="py-3 px-3 text-gray-800 font-semibold font-mono">
                  {model.outputPriceDisplay ? (
                    model.isHourly ? '免费' : `$${model.outputPricePerM.toFixed(2)}`
                  ) : (
                    <span className="text-gray-400">-</span>
                  )}
                </td>

                {/* Benchmarks */}
                <td className="py-3 px-3 text-center">
                  <div className="inline-flex items-center space-x-2 text-[11px]">
                    <span className="bg-purple-50 text-purple-700 px-1.5 py-0.5 rounded border border-purple-100 font-mono" title="Intelligence Index">
                      智: {model.scores.intelligenceIndex}
                    </span>
                    <span className="bg-blue-50 text-blue-700 px-1.5 py-0.5 rounded border border-blue-100 font-mono" title="Coding Index">
                      码: {model.scores.codingIndex}
                    </span>
                  </div>
                </td>

                {/* Actions */}
                <td className="py-3 px-3 text-right" onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center justify-end space-x-1.5">
                    <button
                      onClick={() => onTogglePin(model.id)}
                      title={isPinned ? '取消固定' : '固定模型'}
                      className={`p-1.5 rounded transition-colors ${
                        isPinned
                          ? 'bg-purple-100 text-purple-700'
                          : 'text-gray-400 hover:text-gray-700 hover:bg-gray-100'
                      }`}
                    >
                      <Pin className="w-3.5 h-3.5" />
                    </button>

                    <button
                      onClick={() => onToggleCompare(model)}
                      title={isInCompare ? '取消对比' : '加入对比'}
                      className={`p-1.5 rounded transition-colors ${
                        isInCompare
                          ? 'bg-purple-600 text-white'
                          : 'text-gray-400 hover:text-gray-700 hover:bg-gray-100 border border-gray-200'
                      }`}
                    >
                      <Scale className="w-3.5 h-3.5" />
                    </button>

                    <button
                      onClick={() => onOpenPlayground(model)}
                      title="快速测试"
                      className="px-2 py-1 rounded bg-purple-50 text-purple-700 hover:bg-purple-100 font-medium text-xs border border-purple-200 flex items-center space-x-1"
                    >
                      <Play className="w-3 h-3 fill-purple-600" />
                      <span>测试</span>
                    </button>
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
};
