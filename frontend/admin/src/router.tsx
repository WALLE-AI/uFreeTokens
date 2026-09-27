import type React from 'react';
import { createBrowserRouter, redirect, type LoaderFunctionArgs } from 'react-router';
import { authStore } from './api/auth';
import { AdminLayout } from './components/layout/AdminLayout';
import type { RouteHandle } from './components/layout/AdminHeader';
import { NotFoundPage, RouteErrorPage } from './pages/ErrorPages';

// 鉴权守卫：sessionStorage 里没有令牌时跳登录页，登录后回到原路径（UI_DESIGN.md §8）
function requireAuth({ request }: LoaderFunctionArgs) {
  if (!authStore.isAuthenticated()) {
    const url = new URL(request.url);
    throw redirect(`/login?next=${encodeURIComponent(url.pathname + url.search)}`);
  }
  return null;
}

const crumb = (c: RouteHandle['crumb']): RouteHandle => ({ crumb: c });

// page 把一个 default export 的页面模块包装成路由 lazy 函数（按路由拆包）。
const page = (load: () => Promise<{ default: React.ComponentType }>) => async () => ({ Component: (await load()).default });

// 路由表（UI_DESIGN.md §8）。列表页的筛选/排序/分页存 URL query，不在这里声明。
export const router = createBrowserRouter([
  {
    path: '/login',
    lazy: async () => ({ Component: (await import('./pages/LoginPage')).default }),
  },
  {
    path: '/',
    loader: requireAuth,
    Component: AdminLayout,
    errorElement: <RouteErrorPage />,
    handle: crumb('工作台'),
    children: [
      { index: true, lazy: async () => ({ Component: (await import('./pages/DashboardPage')).default }) },
      {
        path: 'pricing/changes',
        handle: crumb('调价审批'),
        lazy: page(() => import('./pages/pricing/PriceChangesPage')),
      },
      { path: 'pricing/listings', handle: crumb('待上架模型'), lazy: page(() => import('./pages/pricing/ListingsPage')) },
      { path: 'pricing/sources', handle: crumb('价格源 & 汇率'), lazy: page(() => import('./pages/supply/SourcesPage')) },
      {
        path: 'providers',
        handle: crumb('供应商'),
        children: [
          { index: true, lazy: page(() => import('./pages/supply/ProvidersPage')) },
          { path: 'new', handle: crumb('接入新供应商'), lazy: page(() => import('./pages/supply/ProviderNewPage')) },
          { path: ':id', handle: crumb((p) => `#${p.id}`), lazy: page(() => import('./pages/supply/ProviderDetailPage')) },
        ],
      },
      {
        path: 'channels',
        handle: crumb('渠道'),
        children: [
          { index: true, lazy: page(() => import('./pages/catalog/ChannelsPage')) },
          { path: ':id', handle: crumb((p) => `#${p.id}`), lazy: page(() => import('./pages/catalog/ChannelDetailPage')) },
        ],
      },
      {
        path: 'models',
        handle: crumb('虚拟模型'),
        children: [
          { index: true, lazy: page(() => import('./pages/catalog/ModelsPage')) },
          { path: ':id', handle: crumb((p) => `#${p.id}`), lazy: page(() => import('./pages/catalog/ModelDetailPage')) },
        ],
      },
      {
        path: 'accounts',
        handle: crumb('账户'),
        children: [
          { index: true, lazy: page(() => import('./pages/accounts/AccountsPage')) },
          { path: ':id', handle: crumb((p) => `#${p.id}`), lazy: page(() => import('./pages/accounts/AccountDetailPage')) },
        ],
      },
      { path: 'api-keys', handle: crumb('API 密钥'), lazy: page(() => import('./pages/accounts/ApiKeysPage')) },
      { path: 'logs', handle: crumb('调用日志'), lazy: page(() => import('./pages/observe/LogsPage')) },
      { path: 'analytics', handle: crumb('用量分析'), lazy: page(() => import('./pages/observe/AnalyticsPage')) },
      { path: 'audit', handle: crumb('审计日志'), lazy: page(() => import('./pages/audit/AuditPage')) },
      { path: '*', handle: crumb('页面不存在'), Component: NotFoundPage },
    ],
  },
]);
