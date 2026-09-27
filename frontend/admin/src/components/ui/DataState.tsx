import { useState, type ReactNode } from 'react';
import { AlertCircle, Copy, Inbox, Loader2, RotateCw } from 'lucide-react';
import { ApiError, errorMessage } from '../../api/errors';
import { Button } from './Button';

export interface DataStateProps {
  loading?: boolean;
  error?: unknown;
  empty?: boolean;
  onRetry?: () => void;
  emptyIcon?: ReactNode;
  emptyTitle?: ReactNode;
  emptyDescription?: ReactNode;
  emptyAction?: ReactNode;
  skeleton?: 'table' | 'cards' | 'text';
  children?: ReactNode;
}

// DataState：加载骨架 / 错误卡（重试 + 可复制 request_id）/ 空态，其余情况渲染 children。
// 已有数据时的刷新不走这里（保留旧数据不闪白屏，UI_DESIGN.md §6）。
export function DataState({
  loading,
  error,
  empty,
  onRetry,
  emptyIcon,
  emptyTitle = '暂无数据',
  emptyDescription,
  emptyAction,
  skeleton = 'table',
  children,
}: DataStateProps) {
  if (loading) return <Skeleton kind={skeleton} />;
  if (error) return <ErrorCard error={error} onRetry={onRetry} />;
  if (empty) return <EmptyState icon={emptyIcon} title={emptyTitle} description={emptyDescription} action={emptyAction} />;
  return <>{children}</>;
}

export function EmptyState({ icon, title, description, action }: { icon?: ReactNode; title: ReactNode; description?: ReactNode; action?: ReactNode }) {
  return (
    <div className="bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center">
      <div className="flex justify-center text-gray-300 mb-2">{icon ?? <Inbox className="w-8 h-8" />}</div>
      <div className="text-xs font-medium text-gray-700">{title}</div>
      {description && <div className="text-[11px] text-gray-400 mt-1 max-w-md mx-auto">{description}</div>}
      {action && <div className="mt-3 flex justify-center">{action}</div>}
    </div>
  );
}

export function ErrorCard({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const [copied, setCopied] = useState(false);
  const apiErr = error instanceof ApiError ? error : null;
  return (
    <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-4 text-xs flex items-start gap-3">
      <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
      <div className="flex-1 min-w-0 space-y-1">
        <div className="font-medium">{errorMessage(error, '加载失败')}</div>
        {apiErr?.detail && apiErr.detail !== apiErr.message && <div className="text-rose-600/80 font-mono break-all">{apiErr.detail}</div>}
        {apiErr?.requestId && (
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard?.writeText(apiErr.requestId ?? '');
              setCopied(true);
              setTimeout(() => setCopied(false), 2000);
            }}
            className="inline-flex items-center gap-1 text-[11px] text-rose-600/80 hover:text-rose-700 font-mono cursor-pointer"
            title="复制 request_id，用于在后端日志中定位"
          >
            <Copy className="w-3 h-3" />
            request_id: {apiErr.requestId}
            {copied && <span className="font-sans ml-1">已复制</span>}
          </button>
        )}
      </div>
      {onRetry && (
        <Button size="sm" onClick={onRetry} icon={<RotateCw className="w-3 h-3" />}>
          重试
        </Button>
      )}
    </div>
  );
}

function Skeleton({ kind }: { kind: 'table' | 'cards' | 'text' }) {
  if (kind === 'text') {
    return (
      <div className="text-xs text-gray-400 py-6 text-center flex items-center justify-center gap-2">
        <Loader2 className="w-3.5 h-3.5 animate-spin" />
        正在加载...
      </div>
    );
  }
  if (kind === 'cards') {
    return (
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="bg-gray-50 border border-gray-200 rounded-xl p-4 space-y-2">
            <div className="h-2.5 w-16 bg-gray-100 rounded animate-pulse" />
            <div className="h-6 w-24 bg-gray-100 rounded animate-pulse" />
          </div>
        ))}
      </div>
    );
  }
  return (
    <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-hidden">
      <div className="h-9 bg-gray-50 border-b border-gray-200" />
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 px-4 py-3 border-b border-gray-100 last:border-b-0">
          <div className="h-3 w-12 bg-gray-100 rounded animate-pulse" />
          <div className="h-3 flex-1 bg-gray-100 rounded animate-pulse" />
          <div className="h-3 w-20 bg-gray-100 rounded animate-pulse" />
          <div className="h-3 w-16 bg-gray-100 rounded animate-pulse" />
        </div>
      ))}
    </div>
  );
}
