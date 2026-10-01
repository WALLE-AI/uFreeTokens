import React, { useState, useEffect } from 'react';
import { Search, X, ArrowRight, ArrowUpRight, Cpu } from 'lucide-react';
import { Model } from '../types';
import { ProviderIcon } from './ProviderIcon';

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  models: Model[];
  onSelectModel: (model: Model) => void;
  onNavigate?: (nav: string) => void;
}

export const CommandPalette: React.FC<CommandPaletteProps> = ({
  isOpen,
  onClose,
  models,
  onSelectModel,
  onNavigate,
}) => {
  const [query, setQuery] = useState('');

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        if (isOpen) {
          onClose();
        } else {
          // Open
        }
      } else if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const filtered = models.filter((m) => {
    const q = query.toLowerCase();
    return (
      m.name.toLowerCase().includes(q) ||
      m.id.toLowerCase().includes(q) ||
      m.provider.toLowerCase().includes(q) ||
      m.description.toLowerCase().includes(q)
    );
  });

  const pageMatches = [
    { name: 'Harness', label: 'Ori Harness (CLI & Coding Agent)', desc: 'ori claude, ori codex, ori eval 智能体集成套件', keywords: ['harness', 'ori', 'cli', 'code', 'eval', 'agent'] },
    { name: '基准测试', label: '基准跑分评估 (Benchmarks)', desc: '综合智能指数与前沿基准评测矩阵', keywords: ['基准', '跑分', 'benchmark', 'eval'] },
    { name: '排行榜', label: '用量与热度排行榜 (Rankings)', desc: '真实开发者实时调用与 Token 吞吐分析', keywords: ['排行', '榜单', 'ranking', 'top'] },
    { name: '文档', label: '开发文档与 API 参考 (Docs & Reference)', desc: '快速开始、接入指南、接口参考与在线调试', keywords: ['文档', 'api', 'docs', 'reference', 'curl', 'changelog', 'quickstart'] },
  ].filter((p) => {
    if (!query.trim()) return false;
    const q = query.toLowerCase();
    return p.name.toLowerCase().includes(q) || p.label.toLowerCase().includes(q) || p.keywords.some(k => k.includes(q));
  });

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-20 p-4 bg-black/40 backdrop-blur-xs">
      <div
        className="bg-white rounded-xl shadow-2xl max-w-xl w-full border border-gray-200 overflow-hidden flex flex-col animate-in fade-in zoom-in-95 duration-100"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="p-3 border-b border-gray-200 flex items-center space-x-2.5">
          <Search className="w-4 h-4 text-gray-400 shrink-0" />
          <input
            type="text"
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索模型名称、厂商、型号或关键词..."
            className="w-full text-xs text-gray-900 placeholder-gray-400 focus:outline-none"
          />
          <button
            onClick={onClose}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="max-h-80 overflow-y-auto p-2 text-xs divide-y divide-gray-50">
          {pageMatches.length > 0 && (
            <div className="pb-2 mb-1 space-y-1">
              <div className="text-[10px] font-semibold text-purple-600 px-2 uppercase tracking-wider">
                功能页面快捷跳转
              </div>
              {pageMatches.map((page) => (
                <div
                  key={page.name}
                  onClick={() => {
                    if (onNavigate) onNavigate(page.name);
                    onClose();
                  }}
                  className="p-2.5 bg-purple-50/50 hover:bg-purple-100/60 rounded-lg cursor-pointer flex items-center justify-between group transition-colors border border-purple-100"
                >
                  <div className="flex items-center space-x-2.5">
                    <div className="w-5 h-5 rounded bg-purple-600 text-white flex items-center justify-center text-[10px] font-bold">
                      ⚡
                    </div>
                    <div>
                      <div className="font-semibold text-purple-900 group-hover:text-purple-700">
                        {page.label}
                      </div>
                      <div className="text-[10px] text-gray-500">
                        {page.desc}
                      </div>
                    </div>
                  </div>
                  <ArrowRight className="w-3.5 h-3.5 text-purple-400 group-hover:text-purple-600 transition-colors" />
                </div>
              ))}
            </div>
          )}

          {filtered.length === 0 && pageMatches.length === 0 ? (
            <div className="p-6 text-center text-gray-400 text-xs">
              未找到匹配“{query}”的内容
            </div>
          ) : (
            filtered.map((model) => (
              <div
                key={model.id}
                onClick={() => {
                  onSelectModel(model);
                  onClose();
                }}
                className="p-2.5 hover:bg-purple-50/60 rounded-lg cursor-pointer flex items-center justify-between group transition-colors"
              >
                <div className="flex items-center space-x-2.5">
                  <ProviderIcon
                    provider={model.provider}
                    className="w-4 h-4 rounded"
                    fallbackBg={model.iconBg}
                    fallbackTextClassName="text-[8px]"
                  />
                  <div>
                    <div className="font-semibold text-gray-900 group-hover:text-purple-700 flex items-center space-x-1.5">
                      <span>{model.name}</span>
                      {model.badge && (
                        <span className="text-[9px] bg-purple-100 text-purple-700 px-1 rounded">
                          {model.badge}
                        </span>
                      )}
                    </div>
                    <div className="text-[10px] text-gray-400 font-mono">
                      {model.id} • {model.inputPriceDisplay}
                    </div>
                  </div>
                </div>

                <ArrowRight className="w-3.5 h-3.5 text-gray-300 group-hover:text-purple-600 transition-colors" />
              </div>
            ))
          )}
        </div>

        <div className="p-2.5 bg-gray-50 border-t border-gray-100 text-[11px] text-gray-400 flex items-center justify-between">
          <span>
            按 <kbd className="font-mono bg-white border border-gray-200 px-1 rounded">ESC</kbd> 退出
          </span>
          <span>共找到 {filtered.length} 个可用模型</span>
        </div>
      </div>
    </div>
  );
};
