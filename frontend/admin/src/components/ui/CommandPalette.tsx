import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { CornerDownLeft, Hash, Search, Zap } from 'lucide-react';
import { cn } from '../../lib/cn';

export interface Command {
  id: string;
  group: string; // "页面" / "跳转到对象" / "操作"
  label: string;
  hint?: string;
  icon?: ReactNode;
  keywords?: string;
  run: () => void;
}

export interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
  commands: Command[];
  // 根据输入动态生成的命令（如输入数字 → 账户 #1234 / API Key #1234 / 渠道 #1234）
  dynamicCommands?: (query: string) => Command[];
}

// CommandPalette：在 web CommandPalette 基础上补上 ↑↓ 选择 + Enter 执行（UI_DESIGN.md §1.2）。
export function CommandPalette({ open, onClose, commands, dynamicCommands }: CommandPaletteProps) {
  const [query, setQuery] = useState('');
  const [index, setIndex] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open) {
      setQuery('');
      setIndex(0);
    }
  }, [open]);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    const dyn = q && dynamicCommands ? dynamicCommands(query.trim()) : [];
    const stat = q
      ? commands.filter((c) => `${c.label} ${c.keywords ?? ''} ${c.hint ?? ''}`.toLowerCase().includes(q))
      : commands;
    return [...dyn, ...stat];
  }, [query, commands, dynamicCommands]);

  useEffect(() => setIndex(0), [query]);

  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[data-idx="${index}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [index]);

  if (!open) return null;

  const run = (c: Command | undefined) => {
    if (!c) return;
    onClose();
    c.run();
  };

  let lastGroup = '';
  return createPortal(
    <div
      className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-start justify-center pt-20 px-4 animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="bg-white rounded-xl w-full max-w-xl shadow-2xl border border-gray-200 overflow-hidden animate-in fade-in zoom-in-95 duration-150">
        <div className="flex items-center gap-2 px-4 border-b border-gray-100">
          <Search className="w-4 h-4 text-gray-400" />
          <input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') {
                e.preventDefault();
                setIndex((i) => Math.min(i + 1, results.length - 1));
              } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                setIndex((i) => Math.max(i - 1, 0));
              } else if (e.key === 'Enter') {
                e.preventDefault();
                run(results[index]);
              } else if (e.key === 'Escape') {
                e.preventDefault();
                onClose();
              }
            }}
            placeholder="跳转到页面，或输入账户 ID / 模型名 / 渠道 ID…"
            className="flex-1 py-3 text-sm text-gray-900 placeholder-gray-400 focus:outline-none bg-transparent"
          />
        </div>
        <div ref={listRef} className="max-h-80 overflow-y-auto py-1">
          {results.length === 0 ? (
            <div className="px-4 py-8 text-center text-xs text-gray-400">没有匹配的结果</div>
          ) : (
            results.map((c, i) => {
              const header = c.group !== lastGroup ? c.group : null;
              lastGroup = c.group;
              return (
                <div key={c.id}>
                  {header && <div className="px-4 pt-2 pb-1 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{header}</div>}
                  <button
                    type="button"
                    data-idx={i}
                    onMouseEnter={() => setIndex(i)}
                    onClick={() => run(c)}
                    className={cn(
                      'w-full px-4 py-2 flex items-center gap-2.5 text-left text-xs cursor-pointer',
                      i === index ? 'bg-purple-50 text-purple-700' : 'text-gray-700',
                    )}
                  >
                    <span className={cn('shrink-0', i === index ? 'text-purple-600' : 'text-gray-400')}>
                      {c.icon ?? (c.group === '操作' ? <Zap className="w-3.5 h-3.5" /> : <Hash className="w-3.5 h-3.5" />)}
                    </span>
                    <span className="flex-1 truncate">{c.label}</span>
                    {c.hint && <span className="text-[11px] text-gray-400 font-mono">{c.hint}</span>}
                    {i === index && <CornerDownLeft className="w-3 h-3 text-purple-500" />}
                  </button>
                </div>
              );
            })
          )}
        </div>
        <div className="px-4 py-2 border-t border-gray-100 flex items-center gap-3 text-[11px] text-gray-400">
          <span>
            <kbd className="font-mono bg-gray-100 px-1 rounded">↑↓</kbd> 选择
          </span>
          <span>
            <kbd className="font-mono bg-gray-100 px-1 rounded">Enter</kbd> 执行
          </span>
          <span>
            <kbd className="font-mono bg-gray-100 px-1 rounded">Esc</kbd> 关闭
          </span>
          <span className="ml-auto">{results.length} 项</span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
