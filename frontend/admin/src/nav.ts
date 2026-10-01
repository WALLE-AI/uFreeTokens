import type { LucideIcon } from 'lucide-react';
import {
  ArrowLeftRight,
  BarChart3,
  Coins,
  Database,
  FileClock,
  KeyRound,
  LayoutDashboard,
  Layers,
  PackagePlus,
  Scale,
  ScrollText,
  ShieldCheck,
  Users,
} from 'lucide-react';
import { can } from './api/auth';
import type { AdminMe, Permission, TodoCounts } from './types';

export interface NavItem {
  path: string;
  label: string;
  icon: LucideIcon;
  group: string | null;
  gotoKey?: string; // "G 然后 X" 快捷跳转
  // 访问所需权限点（与后端路由表一致）；没有权限的菜单不显示
  perm?: Permission;
  // 侧栏待办徽标取值
  badge?: (c: TodoCounts) => { count: number; alert: boolean } | null;
}

// 侧栏、命令面板、快捷键共用同一份导航定义（UI_DESIGN.md §1.1）。
export const NAV_ITEMS: NavItem[] = [
  { path: '/', label: '工作台', icon: LayoutDashboard, group: null, gotoKey: 'd' },

  {
    path: '/pricing/changes',
    label: '调价审批',
    icon: Scale,
    group: '待办',
    gotoKey: 'p',
    perm: 'pricing:read',
    badge: (c) => ({ count: c.price_changes_pending + c.price_changes_blocked, alert: c.price_changes_blocked > 0 }),
  },
  {
    path: '/pricing/listings',
    label: '待上架模型',
    icon: PackagePlus,
    group: '待办',
    perm: 'pricing:read',
    badge: (c) => ({ count: c.listings_pending, alert: false }),
  },

  { path: '/providers', label: '供应商', icon: Database, group: '供给', perm: 'catalog:read' },
  { path: '/channels', label: '渠道', icon: ArrowLeftRight, group: '供给', perm: 'catalog:read' },

  { path: '/models', label: '虚拟模型', icon: Layers, group: '目录与定价', gotoKey: 'm', perm: 'catalog:read' },
  { path: '/pricing/sources', label: '价格源 & 汇率', icon: Coins, group: '目录与定价', perm: 'pricing:read' },

  { path: '/accounts', label: '账户', icon: Users, group: '用户与财务', gotoKey: 'a', perm: 'account:read' },
  { path: '/api-keys', label: 'API 密钥', icon: KeyRound, group: '用户与财务', perm: 'account:read' },

  { path: '/logs', label: '调用日志', icon: ScrollText, group: '可观测', gotoKey: 'l', perm: 'observe:read' },
  { path: '/analytics', label: '用量分析', icon: BarChart3, group: '可观测', perm: 'observe:read' },
  { path: '/audit', label: '审计日志', icon: FileClock, group: '可观测', perm: 'audit:read' },

  { path: '/admin-users', label: '管理员与角色', icon: ShieldCheck, group: '系统', perm: 'admin_user:manage' },
];

// visibleNavItems 过滤掉当前管理员没有权限的菜单。
export function visibleNavItems(me: AdminMe | null): NavItem[] {
  return NAV_ITEMS.filter((i) => can(me, i.perm));
}

export function navGroups(me: AdminMe | null): Array<{ group: string | null; items: NavItem[] }> {
  const out: Array<{ group: string | null; items: NavItem[] }> = [];
  for (const item of visibleNavItems(me)) {
    const last = out[out.length - 1];
    if (last && last.group === item.group) last.items.push(item);
    else out.push({ group: item.group, items: [item] });
  }
  return out;
}

export function findNavItem(pathname: string): NavItem | undefined {
  if (pathname === '/') return NAV_ITEMS[0];
  // 最长前缀匹配：/pricing/changes 优先于 /pricing
  return [...NAV_ITEMS]
    .filter((i) => i.path !== '/' && (pathname === i.path || pathname.startsWith(`${i.path}/`)))
    .sort((a, b) => b.path.length - a.path.length)[0];
}
