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
  Users,
} from 'lucide-react';
import type { TodoCounts } from './types';

export interface NavItem {
  path: string;
  label: string;
  icon: LucideIcon;
  group: string | null;
  gotoKey?: string; // "G 然后 X" 快捷跳转
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
    badge: (c) => ({ count: c.price_changes_pending + c.price_changes_blocked, alert: c.price_changes_blocked > 0 }),
  },
  {
    path: '/pricing/listings',
    label: '待上架模型',
    icon: PackagePlus,
    group: '待办',
    badge: (c) => ({ count: c.listings_pending, alert: false }),
  },

  { path: '/providers', label: '供应商', icon: Database, group: '供给' },
  { path: '/channels', label: '渠道', icon: ArrowLeftRight, group: '供给' },

  { path: '/models', label: '虚拟模型', icon: Layers, group: '目录与定价', gotoKey: 'm' },
  { path: '/pricing/sources', label: '价格源 & 汇率', icon: Coins, group: '目录与定价' },

  { path: '/accounts', label: '账户', icon: Users, group: '用户与财务', gotoKey: 'a' },
  { path: '/api-keys', label: 'API 密钥', icon: KeyRound, group: '用户与财务' },

  { path: '/logs', label: '调用日志', icon: ScrollText, group: '可观测', gotoKey: 'l' },
  { path: '/analytics', label: '用量分析', icon: BarChart3, group: '可观测' },
  { path: '/audit', label: '审计日志', icon: FileClock, group: '可观测' },
];

export function navGroups(): Array<{ group: string | null; items: NavItem[] }> {
  const out: Array<{ group: string | null; items: NavItem[] }> = [];
  for (const item of NAV_ITEMS) {
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
