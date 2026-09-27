# frontend/admin — uFreeTokens 运营后台

内部运营 / 风控 / 供应链人员使用的管理后台，对接 `cmd/admin`（:8081）。

- 技术架构与鉴权：[`ARCHITECTURE.md`](./ARCHITECTURE.md)
- 页面与交互设计、视觉规范：[`UI_DESIGN.md`](./UI_DESIGN.md)
- 后端接口补全方案：[`docs/cmd-admin 运营后台接口补全技术方案.md`](../../docs/)

## 启动

```bash
# 1. 启动后端 cmd/admin（默认 :8081），需要设置 UFT_ADMIN_TOKEN
make run-admin

# 2. 启动前端（http://localhost:3001）
cd frontend/admin
npm install
npm run dev
```

打开后在登录页输入 `UFT_ADMIN_TOKEN` 和自己的姓名。令牌只保存在当前标签页的 sessionStorage，关闭标签页即失效；姓名作为 `X-Actor-Name` 头写入审计日志。

其它脚本：`npm run lint`（`tsc --noEmit`）、`npm run build`、`npm run preview`。

## 接口代理约定

`cmd/admin` 的接口挂在根路径（`/accounts`、`/providers`……），与前端页面路由同名。因此前端所有请求统一加前缀 **`/admin-api`**：

| 环境 | 转发方式 |
|---|---|
| dev | `vite.config.ts` 的 `server.proxy`：`/admin-api/*` → `http://localhost:8081/*`（去掉前缀） |
| prod | Nginx，见 [`deploy-nginx.example.conf`](./deploy-nginx.example.conf)；同时配置 SPA `try_files` 回退 |

环境变量见 `.env.example`：`VITE_ADMIN_API_BASE`（默认 `/admin-api`）、`ADMIN_API_TARGET`（dev 代理目标，默认 `http://localhost:8081`）、`VITE_ADMIN_ENV`（`production` / `staging` / `dev`，控制环境标识）。**任何情况下都不要把 `UFT_ADMIN_TOKEN` 写进 env 或代码。**

## 目录结构

```
src/
  main.tsx              入口：ToastProvider + RouterProvider
  router.tsx            路由表（react-router v7 data router，按路由拆包）+ 鉴权守卫
  nav.ts                侧栏 / 命令面板 / 快捷键共用的导航定义
  types.ts              全部接口类型（snake_case，金额 *_micro；注释标注对应的 Go 结构体）
  api/                  唯一允许发网络请求的地方
    client.ts           request()：前缀、令牌、X-Actor-Name、错误解析、401 回登录页
    auth.ts             sessionStorage 登录态 + useAuth
    errors.ts           ApiError，code → 中文提示，describeError
    catalog.ts          供应商 / 上游账号 / 密钥 / 虚拟模型 / 渠道 / 价格版本 / 汇率
    pricing.ts          调价审批 / 待上架 / 价格源 / 参考价
    accounts.ts         账户 / 流水 / 赠送 / 调账 / API Key
    stats.ts            用量统计 / 全局调用日志
    audit.ts / todo.ts  审计日志 / 待办计数
  components/
    layout/             AdminLayout / AdminHeader / AdminSidebar / ShortcutHelp
    ui/                 通用组件（UI_DESIGN.md §2、§11.5）：表格、弹窗、抽屉、表单控件、详情页部件……
    audit/              AuditTimeline（详情页的"操作记录"段落）
    pricing/            PriceComponentEditor（售价 / 成本价编辑器）
    stats/              UsageTrend、StackedBars、指标定义
  hooks/                useAsync / useQueryState / useDismiss（浮层栈）/ useHotkeys / useTodoCounts
  lib/                  money（精确元 ↔ micro 换算）/ time / env / cn（tailwind-merge）/ audit（动作文案与跳转）
  pages/
    DashboardPage.tsx   工作台
    pricing/            调价审批（收件箱）、待上架模型
    supply/             供应商列表 / 详情、接入向导（wizard/）、价格源 & 汇率
    catalog/            虚拟模型、渠道（列表 / 详情）
    accounts/           账户列表 / 详情（调账、赠送、代开 Key）、API 密钥
    observe/            调用日志、用量分析
    audit/              审计日志
```

## 约定

- 样式与 `frontend/web` 保持一致，规范见 `UI_DESIGN.md` §11；页面不手写长 class，使用 `components/ui` 里的组件。
- 列表页的筛选、排序、分页一律存在 URL query（`useQueryState`），刷新和分享链接不丢状态。
- 金额输入用 `MoneyInput`（输入"元"，精确转 micro），展示用 `Money`。
