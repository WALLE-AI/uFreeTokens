import { NavLink } from 'react-router';
import { UserRound } from 'lucide-react';
import { navGroups } from '../../nav';
import { cn } from '../../lib/cn';
import { ADMIN_ENV, ENV_BADGE_CLASS, ENV_DOT_CLASS, ENV_LABEL } from '../../lib/env';
import { useAuth } from '../../api/auth';
import { useAgentEnabled } from '../../agent/agentStore';
import { CountBadge } from '../ui';
import type { TodoCounts } from '../../types';

// AdminSidebar：按任务域分组（UI_DESIGN.md §1.1），样式对齐 web 个人中心左侧导航
// （w-56，选中 bg-purple-100/70 text-purple-700，分组标签 text-[11px] uppercase）。
export function AdminSidebar({ counts, onNavigate }: { counts: TodoCounts | null; onNavigate?: () => void }) {
  const { me } = useAuth();
  const agentEnabled = useAgentEnabled();
  return (
    <div className="w-56 h-full border-r border-gray-200 bg-white flex flex-col">
      <nav className="flex-1 overflow-y-auto px-2.5 py-3 space-y-4 text-[13px]">
        {navGroups(me, agentEnabled).map(({ group, items }) => (
          <div key={group ?? 'root'}>
            {group && <div className="px-2.5 mb-1 text-[11px] uppercase tracking-wider text-gray-400 font-semibold">{group}</div>}
            <ul className="space-y-0.5">
              {items.map((item) => {
                const Icon = item.icon;
                const badge = counts && item.badge ? item.badge(counts) : null;
                return (
                  <li key={item.path}>
                    <NavLink
                      to={item.path}
                      end={item.path === '/'}
                      onClick={onNavigate}
                      className={({ isActive }) =>
                        cn(
                          'flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg transition-colors',
                          isActive ? 'bg-purple-100/70 text-purple-700 font-medium' : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900',
                        )
                      }
                    >
                      <Icon className="w-4 h-4 shrink-0" />
                      <span className="flex-1 truncate">{item.label}</span>
                      {badge && <CountBadge count={badge.count} alert={badge.alert} />}
                    </NavLink>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </nav>
      <div className="border-t border-gray-100 px-3 py-3 space-y-2">
        <span className={cn('inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium border', ENV_BADGE_CLASS[ADMIN_ENV])}>
          <span className={cn('w-1.5 h-1.5 rounded-full', ENV_DOT_CLASS[ADMIN_ENV])} />
          {ENV_LABEL[ADMIN_ENV]}
        </span>
        <div className="flex items-center gap-1.5 text-[11px] text-gray-500" title="写操作以当前登录身份记入审计日志">
          <UserRound className="w-3 h-3" />
          <span className="truncate">
            {me?.name ?? '未登录'}
            {me && <span className="text-gray-400">（{me.break_glass ? '应急令牌' : me.roles.join('、')}）</span>}
          </span>
        </div>
      </div>
    </div>
  );
}
