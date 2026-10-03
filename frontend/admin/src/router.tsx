import type React from 'react';
import { createBrowserRouter, Outlet, redirect, type LoaderFunctionArgs } from 'react-router';
import { ToastProvider } from './components/ui';
import { authStore } from './api/auth';
import { AdminLayout } from './components/layout/AdminLayout';
import type { RouteHandle } from './components/layout/AdminHeader';
import { NotFoundPage, RouteErrorPage } from './pages/ErrorPages';

// 鉴权守卫：sessionStorage 里没有会话（令牌 + /me 身份）时跳登录页，登录后回到原路径（UI_DESIGN.md §8）
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

// 根布局：ToastProvider 放在路由树内部，toast 内容才能使用 <Link> 等依赖路由上下文的组件。
function RootLayout() {
  return (
    <ToastProvider>
      <Outlet />
    </ToastProvider>
  );
}

// 路由表（UI_DESIGN.md §8）。列表页的筛选/排序/分页存 URL query，不在这里声明。
export const router = createBrowserRouter([
  {
    Component: RootLayout,
    children: [
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
          { path: 'pricing/offers', handle: crumb('优惠雷达'), lazy: page(() => import('./pages/pricing/OffersPage')) },
          { path: 'pricing/comparison', handle: crumb('比价看板'), lazy: page(() => import('./pages/pricing/PriceComparisonPage')) },
          { path: 'catalog/model-aliases', handle: crumb('榜单模型映射'), lazy: page(() => import('./pages/catalog/ModelAliasesPage')) },
          { path: 'public-apps', handle: crumb('公开应用榜'), lazy: page(() => import('./pages/catalog/PublicAppsPage')) },
          { path: 'pricing/sources', handle: crumb('数据源 & 汇率'), lazy: page(() => import('./pages/supply/SourcesPage')) },
          {
            path: 'providers',
            handle: crumb('供应商'),
            children: [
              { index: true, lazy: page(() => import('./pages/supply/ProvidersPage')) },
              { path: 'new', handle: crumb('接入新供应商'), lazy: page(() => import('./pages/supply/ProviderNewPage')) },
              {
                path: ':id',
                handle: crumb((p) => `#${p.id}`),
                children: [
                  { index: true, lazy: page(() => import('./pages/supply/ProviderDetailPage')) },
                  { path: 'models/add', handle: crumb('添加模型'), lazy: page(() => import('./pages/supply/ProviderAddModelsPage')) },
                ],
              },
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
            path: 'benchmarks',
            handle: crumb('基准测试'),
            children: [
              { index: true, lazy: page(() => import('./pages/catalog/BenchmarksPage')) },
              {
                path: ':id',
                handle: crumb((p) => `#${p.id}`),
                children: [
                  { index: true, lazy: page(() => import('./pages/catalog/BenchmarkDetailPage')) },
                  { path: 'runs/new', handle: crumb('录入 run'), lazy: page(() => import('./pages/catalog/BenchmarkRunNewPage')) },
                ],
              },
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
          { path: 'admin-users', handle: crumb('管理员与角色'), lazy: page(() => import('./pages/system/AdminUsersPage')) },
          {
            path: 'agent',
            handle: crumb('运营助手'),
            children: [
              { index: true, lazy: page(() => import('./pages/agent/AgentPage')) },
              { path: 'inbox', handle: crumb('提案收件箱'), lazy: page(() => import('./pages/agent/InboxPage')) },
              { path: 'jobs', handle: crumb('智能作业'), lazy: page(() => import('./pages/agent/JobsPage')) },
              { path: ':sessionId', handle: crumb((p) => `会话 #${p.sessionId}`), lazy: page(() => import('./pages/agent/AgentPage')) },
            ],
          },
          { path: '*', handle: crumb('页面不存在'), Component: NotFoundPage },
        ],
      },
    ],
  },
]);
