import { useCallback, useRef, useState, type ReactNode } from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown, CheckSquare, ChevronLeft, ChevronRight, MinusSquare, MoreVertical, Square } from 'lucide-react';
import { cn } from '../../lib/cn';
import { useDismiss } from '../../hooks/useDismiss';

export interface Column<T> {
  key: string;
  header: ReactNode;
  render: (row: T) => ReactNode;
  align?: 'left' | 'right' | 'center';
  numeric?: boolean; // 数字列：font-mono + 右对齐（§11.5）
  sortable?: boolean; // 可排序时 key 即排序字段名
  width?: string; // 例如 'w-24'
  className?: string;
}

export interface RowAction<T> {
  label: string;
  icon?: ReactNode;
  danger?: boolean;
  hidden?: (row: T) => boolean;
  onClick: (row: T) => void;
}

export interface DataTableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string | number;
  onRowClick?: (row: T) => void;
  // 排序：sort 形如 "field" / "-field"（接口方案 §0.3）
  sort?: string;
  onSortChange?: (sort: string) => void;
  selectable?: boolean;
  selected?: Set<string | number>;
  onSelectedChange?: (s: Set<string | number>) => void;
  bulkActions?: ReactNode; // 勾选后显示在表格上方
  rowActions?: RowAction<T>[];
  highlightKey?: string | number | null;
  empty?: ReactNode;
  footer?: ReactNode;
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  sort,
  onSortChange,
  selectable,
  selected,
  onSelectedChange,
  bulkActions,
  rowActions,
  highlightKey,
  empty,
  footer,
}: DataTableProps<T>) {
  const sel = selected ?? new Set<string | number>();
  const allKeys = rows.map(rowKey);
  const allSelected = allKeys.length > 0 && allKeys.every((k) => sel.has(k));
  const someSelected = !allSelected && allKeys.some((k) => sel.has(k));

  const toggleAll = () => {
    if (!onSelectedChange) return;
    onSelectedChange(allSelected ? new Set() : new Set(allKeys));
  };
  const toggleOne = (k: string | number) => {
    if (!onSelectedChange) return;
    const next = new Set(sel);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    onSelectedChange(next);
  };

  const sortField = sort?.replace(/^-/, '');
  const sortDesc = sort?.startsWith('-');
  const onHeaderClick = (col: Column<T>) => {
    if (!col.sortable || !onSortChange) return;
    if (sortField !== col.key) onSortChange(col.key);
    else if (!sortDesc) onSortChange(`-${col.key}`);
    else onSortChange('');
  };

  const alignClass = (c: Column<T>) =>
    c.align === 'right' || c.numeric ? 'text-right' : c.align === 'center' ? 'text-center' : 'text-left';

  return (
    <div>
      {selectable && sel.size > 0 && (
        <div className="flex items-center gap-3 mb-2 px-3 py-2 bg-purple-50/60 border border-purple-100 rounded-lg text-xs animate-in fade-in duration-200">
          <span className="text-purple-700 font-medium">{sel.size} 项已选</span>
          <div className="flex items-center gap-2">{bulkActions}</div>
          <button type="button" onClick={() => onSelectedChange?.(new Set())} className="ml-auto text-gray-500 hover:text-gray-700 cursor-pointer">
            取消选择
          </button>
        </div>
      )}
      <div className="overflow-x-auto bg-white border border-gray-200 rounded-xl shadow-xs">
        <table className="w-full text-xs">
          <thead>
            <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
              {selectable && (
                <th className="w-10 px-4 py-2.5 text-left">
                  <button type="button" onClick={toggleAll} className="text-gray-400 hover:text-purple-600 cursor-pointer" aria-label="全选">
                    {allSelected ? (
                      <CheckSquare className="w-3.5 h-3.5 text-purple-600" />
                    ) : someSelected ? (
                      <MinusSquare className="w-3.5 h-3.5 text-purple-600" />
                    ) : (
                      <Square className="w-3.5 h-3.5" />
                    )}
                  </button>
                </th>
              )}
              {columns.map((c) => (
                <th key={c.key} className={cn('px-4 py-2.5 font-semibold whitespace-nowrap', alignClass(c), c.width)}>
                  {c.sortable && onSortChange ? (
                    <button
                      type="button"
                      onClick={() => onHeaderClick(c)}
                      className={cn(
                        'inline-flex items-center gap-1 uppercase tracking-wider cursor-pointer hover:text-gray-700',
                        sortField === c.key && 'text-gray-700',
                      )}
                    >
                      {c.header}
                      {sortField === c.key ? (
                        sortDesc ? <ArrowDown className="w-3 h-3" /> : <ArrowUp className="w-3 h-3" />
                      ) : (
                        <ArrowUpDown className="w-3 h-3 opacity-40" />
                      )}
                    </button>
                  ) : (
                    c.header
                  )}
                </th>
              ))}
              {rowActions && rowActions.length > 0 && <th className="w-10 px-2 py-2.5" />}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {rows.length === 0 ? (
              <tr>
                <td colSpan={columns.length + (selectable ? 1 : 0) + (rowActions?.length ? 1 : 0)} className="px-4 py-10 text-center text-gray-400">
                  {empty ?? '暂无数据'}
                </td>
              </tr>
            ) : (
              rows.map((row) => {
                const k = rowKey(row);
                const isSel = sel.has(k);
                return (
                  <tr
                    key={k}
                    onClick={onRowClick ? () => onRowClick(row) : undefined}
                    className={cn(
                      'group transition-colors',
                      onRowClick && 'cursor-pointer',
                      isSel ? 'bg-purple-50/40' : highlightKey === k ? 'bg-purple-50/30' : 'hover:bg-gray-50/70',
                    )}
                  >
                    {selectable && (
                      <td className="px-4 py-2.5" onClick={(e) => e.stopPropagation()}>
                        <button type="button" onClick={() => toggleOne(k)} className="text-gray-400 hover:text-purple-600 cursor-pointer" aria-label="选择">
                          {isSel ? <CheckSquare className="w-3.5 h-3.5 text-purple-600" /> : <Square className="w-3.5 h-3.5" />}
                        </button>
                      </td>
                    )}
                    {columns.map((c) => (
                      <td
                        key={c.key}
                        className={cn('px-4 py-2.5 text-gray-700', alignClass(c), c.numeric && 'font-mono text-gray-900 whitespace-nowrap', c.className)}
                      >
                        {c.render(row)}
                      </td>
                    ))}
                    {rowActions && rowActions.length > 0 && (
                      <td className="px-2 py-2.5 text-right" onClick={(e) => e.stopPropagation()}>
                        <RowMenu row={row} actions={rowActions} />
                      </td>
                    )}
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
      {footer}
    </div>
  );
}

function RowMenu<T>({ row, actions }: { row: T; actions: RowAction<T>[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(ref, open, close);
  const visible = actions.filter((a) => !a.hidden?.(row));
  if (visible.length === 0) return null;
  const normal = visible.filter((a) => !a.danger);
  const danger = visible.filter((a) => a.danger);
  return (
    <div ref={ref} className="relative inline-block">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="p-1 rounded-md text-gray-400 hover:text-gray-700 hover:bg-gray-100 cursor-pointer"
        aria-label="更多操作"
      >
        <MoreVertical className="w-3.5 h-3.5" />
      </button>
      {open && (
        <div className="absolute right-0 top-7 bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-30 text-xs w-36 text-left animate-in fade-in zoom-in-95 duration-100">
          {normal.map((a) => (
            <button
              key={a.label}
              type="button"
              onClick={() => {
                setOpen(false);
                a.onClick(row);
              }}
              className="w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-gray-50 text-gray-700 cursor-pointer"
            >
              {a.icon}
              {a.label}
            </button>
          ))}
          {danger.map((a, i) => (
            <button
              key={a.label}
              type="button"
              onClick={() => {
                setOpen(false);
                a.onClick(row);
              }}
              className={cn(
                'w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-rose-50 text-rose-600 cursor-pointer',
                i === 0 && normal.length > 0 && 'border-t border-gray-100',
              )}
            >
              {a.icon}
              {a.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export interface PaginationProps {
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onPageSizeChange?: (size: number) => void;
  pageSizes?: number[];
}

// 页码分页器（管理类列表需要总数和跳页，UI_DESIGN.md §3.1）
export function Pagination({ page, pageSize, total, onPageChange, onPageSizeChange, pageSizes = [20, 50, 100] }: PaginationProps) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const nums = pageWindow(page, pages);
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 mt-3 text-xs text-gray-500">
      <span>
        共 <span className="font-mono text-gray-700">{total.toLocaleString('en-US')}</span> 条
      </span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
          className="p-1 rounded-md hover:bg-gray-100 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
          aria-label="上一页"
        >
          <ChevronLeft className="w-3.5 h-3.5" />
        </button>
        {nums.map((n, i) =>
          n === null ? (
            <span key={`gap-${i}`} className="px-1 text-gray-400">
              …
            </span>
          ) : (
            <button
              key={n}
              type="button"
              onClick={() => onPageChange(n)}
              className={cn(
                'min-w-7 h-7 px-1.5 rounded-md font-mono cursor-pointer',
                n === page ? 'bg-gray-900 text-white' : 'hover:bg-gray-100 text-gray-600',
              )}
            >
              {n}
            </button>
          ),
        )}
        <button
          type="button"
          disabled={page >= pages}
          onClick={() => onPageChange(page + 1)}
          className="p-1 rounded-md hover:bg-gray-100 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
          aria-label="下一页"
        >
          <ChevronRight className="w-3.5 h-3.5" />
        </button>
        {onPageSizeChange && (
          <select
            value={pageSize}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
            className="ml-2 border border-gray-200 rounded-lg px-2 py-1 text-xs text-gray-600 focus:outline-none focus:border-purple-400 cursor-pointer bg-white"
          >
            {pageSizes.map((s) => (
              <option key={s} value={s}>
                {s}/页
              </option>
            ))}
          </select>
        )}
      </div>
    </div>
  );
}

function pageWindow(page: number, pages: number): Array<number | null> {
  if (pages <= 7) return Array.from({ length: pages }, (_, i) => i + 1);
  const out: Array<number | null> = [1];
  const start = Math.max(2, page - 1);
  const end = Math.min(pages - 1, page + 1);
  if (start > 2) out.push(null);
  for (let i = start; i <= end; i++) out.push(i);
  if (end < pages - 1) out.push(null);
  out.push(pages);
  return out;
}
