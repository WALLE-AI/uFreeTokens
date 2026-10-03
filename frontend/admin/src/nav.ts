import type { LucideIcon } from 'lucide-react';
import {
  AppWindow,
  ArrowLeftRight,
  Inbox,
  Timer,
  BarChart3,
  Coins,
  Database,
  FileClock,
  FlaskConical,
  GitCompareArrows,
  Link2,
  KeyRound,
  LayoutDashboard,
  Layers,
  PackagePlus,
  Radar,
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
  // 'agent'：只有智能体启用（/agent/meta.enabled）时才显示
  requires?: 'agent';
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
  {
    path: '/pricing/offers',
    label: '优惠雷达',
    icon: Radar,
    group: '待办',
    perm: 'pricing:read',
    badge: (c) => ({ count: c.offers_new ?? 0, alert: false }),
  },

  // 智能体（位于"待办"之后：提案本质上也是待办，设计 §19.4）。助手本身没有独立页面，入口是全局 Dock（⌘J / G I）。
  {
    path: '/agent/inbox',
    label: '提案收件箱',
    icon: Inbox,
    group: '智能体',
    perm: 'agent:use',
    requires: 'agent',
    badge: (c) => ({ count: c.agent_pending_approvals ?? 0, alert: false }),
  },
  { path: '/agent/jobs', label: '智能作业', icon: Timer, group: '智能体', perm: 'agent:admin', requires: 'agent' },

  { path: '/providers', label: '供应商', icon: Database, group: '供给', perm: 'catalog:read' },
  { path: '/channels', label: '渠道', icon: ArrowLeftRight, group: '供给', perm: 'catalog:read' },

  { path: '/models', label: '虚拟模型', icon: Layers, group: '目录与定价', gotoKey: 'm', perm: 'catalog:read' },
  { path: '/pricing/comparison', label: '比价看板', icon: GitCompareArrows, group: '目录与定价', perm: 'pricing:read' },
  { path: '/benchmarks', label: '基准测试', icon: FlaskConical, group: '目录与定价', perm: 'catalog:read' },
  {
    path: '/catalog/model-aliases',
    label: '榜单模型映射',
    icon: Link2,
    group: '目录与定价',
    perm: 'catalog:read',
    badge: (c) => ({ count: c.aliases_suggested ?? 0, alert: false }),
  },
  { path: '/public-apps', label: '公开应用榜', icon: AppWindow, group: '目录与定价', perm: 'catalog:read' },
  {
    path: '/pricing/sources',
    label: '数据源 & 汇率',
    icon: Coins,
    group: '目录与定价',
    perm: 'pricing:read',
    // 只有失败时才显示（红色）
    badge: (c) => (c.data_sources_failing ? { count: c.data_sources_failing, alert: true } : null),
  },

  { path: '/accounts', label: '账户', icon: Users, group: '用户与财务', gotoKey: 'a', perm: 'account:read' },
  { path: '/api-keys', label: 'API 密钥', icon: KeyRound, group: '用户与财务', perm: 'account:read' },

  { path: '/logs', label: '调用日志', icon: ScrollText, group: '可观测', gotoKey: 'l', perm: 'observe:read' },
  { path: '/analytics', label: '用量分析', icon: BarChart3, group: '可观测', perm: 'observe:read' },
  { path: '/audit', label: '审计日志', icon: FileClock, group: '可观测', perm: 'audit:read' },

  { path: '/admin-users', label: '管理员与角色', icon: ShieldCheck, group: '系统', perm: 'admin_user:manage' },
];

// visibleNavItems 过滤掉当前管理员没有权限的菜单；智能体未启用时隐藏智能体分组。
export function visibleNavItems(me: AdminMe | null, agentEnabled = false): NavItem[] {
  return NAV_ITEMS.filter((i) => can(me, i.perm) && (i.requires !== 'agent' || agentEnabled));
}

export function navGroups(me: AdminMe | null, agentEnabled = false): Array<{ group: string | null; items: NavItem[] }> {
  const out: Array<{ group: string | null; items: NavItem[] }> = [];
  for (const item of visibleNavItems(me, agentEnabled)) {
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
