import type { ReactNode } from 'react';

export interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  // 右侧操作：次要操作在前，主操作（每页最多一个紫色实心按钮）在最后
  actions?: ReactNode;
  badge?: ReactNode;
}

export function PageHeader({ title, description, actions, badge }: PageHeaderProps) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 mb-6">
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <h1 className="text-xl font-bold text-gray-900 truncate">{title}</h1>
          {badge}
        </div>
        {description && <p className="text-xs text-gray-500 mt-1">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
    </div>
  );
}

export function SectionTitle({ children, actions, id }: { children: ReactNode; actions?: ReactNode; id?: string }) {
  return (
    <div id={id} className="flex items-center justify-between mb-3 scroll-mt-28">
      <h2 className="text-sm font-semibold text-gray-900">{children}</h2>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Card({ children, className = '', padding = 'p-5' }: { children: ReactNode; className?: string; padding?: string }) {
  return <div className={`bg-white border border-gray-200 rounded-xl shadow-xs ${padding} ${className}`}>{children}</div>;
}
