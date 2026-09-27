import { isRouteErrorResponse, Link, useRouteError } from 'react-router';
import { FileQuestion } from 'lucide-react';
import { EmptyState, ErrorCard } from '../components/ui';

export function NotFoundPage() {
  return (
    <EmptyState
      icon={<FileQuestion className="w-8 h-8" />}
      title="页面不存在"
      description="链接可能已失效，或者你没有访问该页面的权限。"
      action={
        <Link to="/" className="text-xs text-purple-600 hover:text-purple-700">
          返回工作台
        </Link>
      }
    />
  );
}

// 路由级错误边界：页面渲染/懒加载失败时显示，不让整个后台白屏。
export function RouteErrorPage() {
  const err = useRouteError();
  if (isRouteErrorResponse(err) && err.status === 404) return <NotFoundPage />;
  return (
    <div className="p-8 max-w-2xl">
      <ErrorCard error={err instanceof Error ? err : new Error('页面加载失败')} onRetry={() => window.location.reload()} />
    </div>
  );
}
