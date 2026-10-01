import React from 'react';
import { X, Check, Minus, Play, Trash2, Scale } from 'lucide-react';
import { Model } from '../types';
import { ProviderIcon } from './ProviderIcon';
import { EXTERNAL_SCORE_FIELDS, EXTERNAL_SCORE_META, formatExternalScore } from '../data/models';

interface ModelCompareModalProps {
  models: Model[];
  onClose: () => void;
  onRemoveModel: (id: string) => void;
  onClearAll: () => void;
  onOpenPlayground: (model: Model) => void;
}

export const ModelCompareModal: React.FC<ModelCompareModalProps> = ({
  models,
  onClose,
  onRemoveModel,
  onClearAll,
  onOpenPlayground,
}) => {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3 sm:p-4 bg-black/40 backdrop-blur-xs">
      <div
        className="bg-white rounded-xl shadow-2xl max-w-5xl w-full max-h-[92vh] flex flex-col overflow-hidden border border-gray-200 animate-in fade-in zoom-in-95 duration-150"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 py-3.5 border-b border-gray-200 flex items-center justify-between bg-gray-50/80">
          <div className="flex items-center space-x-2">
            <Scale className="w-4 h-4 text-purple-600" />
            <h2 className="text-sm font-bold text-gray-900">
              模型多维对比 ({models.length})
            </h2>
            <span className="text-[11px] text-gray-400">最多支持对比 4 个模型</span>
          </div>

          <div className="flex items-center space-x-3">
            {models.length > 0 && (
              <button
                onClick={onClearAll}
                className="text-xs text-rose-600 hover:text-rose-700 flex items-center space-x-1"
              >
                <Trash2 className="w-3 h-3" />
                <span>清空对比项</span>
              </button>
            )}
            <button
              onClick={onClose}
              className="p-1 rounded-lg text-gray-400 hover:text-gray-700 hover:bg-gray-200/60"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Content table */}
        <div className="flex-1 overflow-y-auto p-5">
          {models.length === 0 ? (
            <div className="py-16 text-center text-gray-400 space-y-2">
              <Scale className="w-10 h-10 mx-auto text-gray-300 stroke-1" />
              <div className="text-sm font-medium text-gray-600">暂未选择任何对比模型</div>
              <div className="text-xs">请在模型列表或表格中点击“对比”按钮添加需要评测的模型</div>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-xs text-left border-collapse">
                <thead>
                  <tr className="border-b border-gray-200">
                    <th className="py-3 px-3 w-36 font-semibold text-gray-400 uppercase tracking-wider text-[11px] bg-gray-50/50">
                      对比维度
                    </th>
                    {models.map((m) => (
                      <th key={m.id} className="py-3 px-3 min-w-[200px] align-top bg-white">
                        <div className="flex items-start justify-between">
                          <div className="space-y-1">
                            <div className="flex items-center space-x-1.5">
                              <ProviderIcon
                                provider={m.provider}
                                className="w-3.5 h-3.5 rounded"
                                fallbackBg={m.iconBg}
                                fallbackTextClassName="text-[8px]"
                              />
                              <span className="font-bold text-gray-900 text-xs">{m.name}</span>
                            </div>
                            <div className="text-[10px] text-gray-400 font-mono">{m.provider}</div>
                          </div>
                          <button
                            onClick={() => onRemoveModel(m.id)}
                            className="text-gray-400 hover:text-rose-600 p-1"
                            title="移除"
                          >
                            <X className="w-3.5 h-3.5" />
                          </button>
                        </div>
                        <button
                          onClick={() => onOpenPlayground(m)}
                          className="mt-2 w-full py-1 bg-purple-50 text-purple-700 hover:bg-purple-100 rounded text-[11px] font-medium flex items-center justify-center space-x-1 border border-purple-200"
                        >
                          <Play className="w-3 h-3 fill-purple-600" />
                          <span>测试对话</span>
                        </button>
                      </th>
                    ))}
                  </tr>
                </thead>

                <tbody className="divide-y divide-gray-100">
                  {/* Context Length */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">上下文容量</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3 font-semibold text-gray-800">
                        {m.contextDisplay || '未注明'}
                      </td>
                    ))}
                  </tr>

                  {/* Input Price */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">输入单价</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3 font-mono font-bold text-purple-700">
                        {m.inputPriceDisplay}
                      </td>
                    ))}
                  </tr>

                  {/* Output Price */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">输出单价</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3 font-mono font-bold text-purple-700">
                        {m.outputPriceDisplay || '免费 / 不适用'}
                      </td>
                    ))}
                  </tr>

                  {/* Modalities */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">支持模态</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3">
                        <div className="flex flex-wrap gap-1">
                          {m.modalities.map((mod) => (
                            <span
                              key={mod}
                              className="px-1.5 py-0.5 bg-gray-100 text-gray-700 rounded text-[10px] uppercase font-mono"
                            >
                              {mod}
                            </span>
                          ))}
                        </div>
                      </td>
                    ))}
                  </tr>

                  {/* Intelligence Score */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">综合智能指数</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3">
                        <div className="flex items-center space-x-2">
                          <span className="font-bold font-mono text-gray-900">{m.scores.intelligenceIndex}</span>
                          <div className="flex-1 max-w-[80px] bg-gray-200 h-1.5 rounded-full overflow-hidden">
                            <div
                              className="bg-purple-600 h-full rounded-full"
                              style={{ width: `${(m.scores.intelligenceIndex / 60) * 100}%` }}
                            />
                          </div>
                        </div>
                      </td>
                    ))}
                  </tr>

                  {/* Coding Score */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">编程能力指数</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3">
                        <div className="flex items-center space-x-2">
                          <span className="font-bold font-mono text-gray-900">{m.scores.codingIndex}</span>
                          <div className="flex-1 max-w-[80px] bg-gray-200 h-1.5 rounded-full overflow-hidden">
                            <div
                              className="bg-blue-600 h-full rounded-full"
                              style={{ width: `${(m.scores.codingIndex / 90) * 100}%` }}
                            />
                          </div>
                        </div>
                      </td>
                    ))}
                  </tr>

                  {/* Agentic Score */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">智能体编排</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3">
                        <div className="flex items-center space-x-2">
                          <span className="font-bold font-mono text-gray-900">{m.scores.agenticIndex}</span>
                          <div className="flex-1 max-w-[80px] bg-gray-200 h-1.5 rounded-full overflow-hidden">
                            <div
                              className="bg-emerald-600 h-full rounded-full"
                              style={{ width: `${(m.scores.agenticIndex / 70) * 100}%` }}
                            />
                          </div>
                        </div>
                      </td>
                    ))}
                  </tr>

                  {/* 公开评测榜单成绩：只列出参与对比的模型里至少有一个有成绩的项，
                      同一行最好成绩高亮（这些指标都是越高越好）。 */}
                  {EXTERNAL_SCORE_FIELDS.filter((key) => models.some((m) => m.scores[key] !== undefined)).map((key) => {
                    const meta = EXTERNAL_SCORE_META[key];
                    const values = models.map((m) => m.scores[key]).filter((v): v is number => v !== undefined);
                    const best = values.length > 1 ? Math.max(...values) : undefined;
                    return (
                      <tr key={key}>
                        <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50" title={`数据来源：${meta.source}`}>
                          {meta.label}
                          <div className="text-[9px] font-normal text-gray-400">{meta.source}</div>
                        </td>
                        {models.map((m) => {
                          const v = m.scores[key];
                          return (
                            <td
                              key={m.id}
                              className={`py-2.5 px-3 font-mono ${
                                v === undefined
                                  ? 'text-gray-300'
                                  : v === best
                                    ? 'font-bold text-emerald-700'
                                    : 'font-medium text-gray-900'
                              }`}
                            >
                              {formatExternalScore(key, v)}
                            </td>
                          );
                        })}
                      </tr>
                    );
                  })}

                  {/* Tool calling */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">工具调用成熟度</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3 font-medium text-gray-700">
                        {m.toolCallingCapability}%
                      </td>
                    ))}
                  </tr>

                  {/* Zero Data Retention */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">零数据保留 (ZDR)</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3">
                        {m.zeroDataRetention ? (
                          <span className="text-emerald-600 flex items-center space-x-1 font-medium">
                            <Check className="w-3.5 h-3.5" />
                            <span>支持</span>
                          </span>
                        ) : (
                          <span className="text-gray-400 flex items-center space-x-1">
                            <Minus className="w-3.5 h-3.5" />
                            <span>不支持</span>
                          </span>
                        )}
                      </td>
                    ))}
                  </tr>

                  {/* Distillable */}
                  <tr>
                    <td className="py-2.5 px-3 font-medium text-gray-500 bg-gray-50/50">支持模型蒸馏</td>
                    {models.map((m) => (
                      <td key={m.id} className="py-2.5 px-3">
                        {m.distillable ? (
                          <span className="text-purple-600 font-medium">允许</span>
                        ) : (
                          <span className="text-gray-400">禁止</span>
                        )}
                      </td>
                    ))}
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="p-4 border-t border-gray-200 bg-gray-50 flex items-center justify-end">
          <button
            onClick={onClose}
            className="px-4 py-1.5 rounded-lg text-xs bg-gray-900 hover:bg-black text-white font-medium"
          >
            完成查看
          </button>
        </div>
      </div>
    </div>
  );
};
