# uFreeTokens 运营管理后台（frontend/admin）技术架构

> 目的：`frontend/admin` 是给内部运营/风控/供应链人员用的管理平台 UI，对接 `cmd/admin`（:8081）的真实控制面接口。技术栈与视觉风格**直接复用 `frontend/web`**（同一套设计语言，两个应用而已）；`frontend/test_web/admin.html` 只是联调用的单文件手工页面，不作为 UI/工程参考，功能覆盖完后可以下线。本文补充在 `frontend/web/ARCHITECTURE.md` 之外，两者配套阅读。

---

## 0. 定位澄清

| 项目 | 定位 | 使用者 | 鉴权 |
|---|---|---|---|
| `frontend/web` | 面向终端用户的模型集市 + 个人中心 | 公网任何访客 | API Key（BYOK），**不能**持有 `UFT_ADMIN_TOKEN` |
| `frontend/admin`（本文档） | 面向运营/风控/供应链的正式管理后台 | 内部员工，走内网/VPN/公司 SSO | 管理员登录态（见 §3），后端仍是 `UFT_ADMIN_TOKEN` 校验，但不让浏览器直接持有裸 token |
| `frontend/test_web` | 手工联调页面（原生 HTML + `fetch`，无构建） | 开发自测 | 直接填 `UFT_ADMIN_TOKEN` 明文，仅本机/测试环境用 |

`frontend/admin` 要解决的问题：把 `frontend/test_web/admin.html` 里"一次性验证链路"的原型，做成一个可长期维护、多人使用、有列表/搜索/审批流的正式后台，UI 观感和工程结构对齐 `frontend/web`，而不是照抄 test_web 的裸表单风格。

---

## 1. 技术栈（与 frontend/web 完全一致）

| 分类 | 选型 | 依据 |
|---|---|---|
| 框架 | React 19 + TypeScript | 与 `frontend/web` 一致，组件写法、hooks 习惯可以直接复用 |
| 构建 | Vite 8（`@vitejs/plugin-react`） | 同 `frontend/web/vite.config.ts` 的 dev/build 脚本模式 |
| 样式 | Tailwind CSS 4（`@tailwindcss/vite`），无额外 UI 组件库 | `frontend/web` 没有引入 shadcn/MUI 之类的库，全部是 Tailwind 原子类手写组件，保持两个项目风格一致、体积小 |
| 图标 | `lucide-react` | 与 `frontend/web` 同款图标集，避免两套后台视觉语言不一致 |
| 状态管理 | React 内置 `useState`/`useMemo`/`useEffect`，不引入 Redux/Zustand | `frontend/web` 现状如此（App.tsx 的筛选状态就是纯 useState），后台页面复杂度类似，没必要加状态库 |
| 动效 | `motion`（按需，仅用于 modal/toast 过渡） | `frontend/web` 依赖里已有 `motion`，保持一致 |
| 包管理/脚本 | `npm`，`dev`/`build`/`preview`/`lint`（`tsc --noEmit`） | 完全照抄 `frontend/web/package.json` 的 scripts 命名 |

**新建项目时直接从 `frontend/web` 复制以下基础设施，而不是重新造：**
- `vite.config.ts`、`tsconfig.json`、`index.css`（Tailwind 入口 + 少量全局样式变量，如自定义 `text-xxs`、`purple-track` 滚动条样式）
- `package.json` 的依赖版本锁定（React 19 / Vite 8 / Tailwind 4 三者版本要对齐，避免后台和前台构建工具链分裂）

## 2. UI 设计系统（复用 frontend/web 的视觉语言）

直接沿用 `frontend/web` 已经确立的规范，不要另起一套：

- **配色**：主色 `purple-600`（`#7C3AED` 系，按钮/高亮/选中态），中性灰 `gray-50/100/200/700/900`，成功 `emerald-*`，警告/危险 `rose-*`/`amber-*`。
- **字号密度**：全局以 `text-xs`（12px）/ `text-[11px]` 为主，标题用 `text-lg`~`text-2xl font-bold`，是一个信息密度很高的"控制台风格"，不是营销站风格。
- **布局骨架**：`Header`（顶部 12 高导航条，含搜索/⌘K）+ 左侧 `Sidebar`（可折叠，移动端抽屉）+ 主内容区表格/卡片，完全对应 `frontend/web` 的 `App.tsx` 结构，`frontend/admin` 应该有对等的 `AdminHeader` + `AdminSidebar` + 路由式主内容区。
- **表格组件**：参考 `ModelTable.tsx` 的写法（`divide-y divide-gray-100`、悬浮 `hover:bg-gray-50/70`、操作列右对齐 + `MoreVertical` 下拉菜单），后台的 Provider/Channel/账户列表全部照此模式实现，不要用第三方 DataGrid。
- **表单/弹窗**：参考 `PersonalDashboardPage.tsx` 里"新建 API 密钥"弹窗（`fixed inset-0 bg-black/40 backdrop-blur-xs` 遮罩 + 居中卡片 + `animate-in fade-in zoom-in-95`），后台的"创建供应商""设置价格"等表单弹窗统一用这个模式。
- **状态反馈**：`Header.tsx` 里的 toast 模式（右上角浮层 2.2s 自动消失）复用为全局操作反馈（创建成功/审批成功/失败提示）。

## 3. 鉴权策略（后台专属，区别于 frontend/web 的 BYOK）

`cmd/admin` 目前只有一个**全局共享** `UFT_ADMIN_TOKEN`，没有按运营人员区分身份的登录系统（`X-Actor-ID` 是调用方自报的，不校验）。这对一个多人协作、需要审计"谁审批了这次调价"的后台是不够的，`frontend/admin` 上线前需要后端配合：

1. **短期（Phase 0，先让后台能用起来）**：`frontend/admin` 做一个登录页，运营人员输入被下发的 `UFT_ADMIN_TOKEN`，前端存入 `sessionStorage`（关闭标签页即失效，比 `localStorage` 更安全），所有请求带 `Authorization: Bearer <token>`。同时**必须**把 `frontend/admin` 部署在内网/VPN/需要公司 SSO 才能访问的域名下，不能公网直接开放——因为 token 本身不区分人。这一步和 `frontend/test_web/admin.html` 的鉴权方式一样，区别只是从裸 HTML 表单升级成正式 UI，本质风险不变，靠网络层访问控制兜底。
2. **中期（对应后端 V2 路线图 §7.15 的 RBAC 规划）**：后端给 `cmd/admin` 加真正的运营账号体系（`super_admin`/`operator`/`finance`/`support` 角色 + 各自密码登录 + JWT/Session），`X-Actor-ID` 从"自报"变成"服务端从鉴权上下文里取"，审计日志才可信。届时 `frontend/admin` 只需要把 §7 的 `authStore` 换掉登录方式，页面基本不用改。

`frontend/admin` 的服务层要按这个演进路径设计（见 §7），不要把 token 直接硬编码在组件里。

## 4. 后端接口清单（`cmd/admin`，均需 `Authorization: Bearer <UFT_ADMIN_TOKEN>`）

对应前端应该规划的功能模块：

| 功能模块（前端页面） | 接口 | 说明 |
|---|---|---|
| **账户管理** | `POST /accounts`、`GET /accounts/{id}` | 创建/查看用户账户（个人或组织），返回含钱包快照 |
| **API 密钥管理**（代用户操作） | `POST /accounts/{id}/api-keys`、`GET /accounts/{id}/api-keys`、`POST /api-keys/{id}/revoke` | 在 §3 的 console 自助体系（`frontend/web` 侧）上线前，这里是运营帮用户开通/吊销 Key 的唯一入口 |
| **钱包/充值** | `POST /accounts/{id}/wallet/adjust`（人工调账）、`POST /accounts/{id}/credit-grants`（发放赠送余额） | 对应"人工充值""活动赠送"运营操作 |
| **供应商管理** | `POST /providers`、`POST /provider-accounts`、`POST /provider-accounts/{id}/keys`、`GET /provider-accounts/{id}/upstream-models` | 对应 test_web 的"接入上游"三步：建供应商 → 建账号+填密钥 → 拉取上游模型列表 |
| **虚拟模型 & 渠道** | `POST /virtual-models`、`GET /virtual-models?name=`、`POST /channels`、`GET /channels?...`、`POST /channels/{id}/cost-price`、`POST /virtual-models/{id}/sell-price` | 对应"选模型 → 定成本价/售价 → 导入上架"，是后台最核心、字段最多的表单场景 |
| **汇率** | `POST /fx-rates` | 简单表单 |
| **价格同步** | `POST /pricesync/reference-price-lookup`（多源比价）、`POST /price-sources`、`POST /providers/{id}/price-observations`、`GET/POST /price-change-requests`（**审批流**）、`GET/POST /pending-model-listings`（**待上架队列**） | 需要专门的"审批"UI：列表 + 通过/驳回按钮 + 差异对比展示，是后台里业务价值最高的模块之一 |
| **审计日志** | `GET /audit-logs?target_type=&target_id=&limit=` | 只读列表 + 筛选，配合 §3 的 RBAC 演进，长期是合规/追责的关键页面 |

对应生成一套 TS 类型（放 `frontend/admin/src/types.ts`），字段直接对齐后端 handler 的 JSON（如 `Provider`、`ProviderAccount`、`VirtualModel`、`Channel`、`PriceComponent`、`Account`、`Wallet`、`ApiKeyItem`、`AuditLogEntry`、`PriceChangeRequest`、`PendingModelListing`），不要用 `any`。

## 5. 与 frontend/web 的关系：能不能共享代码？

建议**不共享构建产物**（两个独立的 Vite 应用、独立部署、独立域名），但**共享设计 token 和少量纯展示组件**，避免长期风格漂移：

- 可以把 `frontend/web` 里和业务无关的纯 UI 组件（比如 Toast、Modal 外壳、表格骨架）抽成一个内部包 `@ufreetokens/ui`（或先简单地在两边各自维护、定期人工对齐，视团队规模决定要不要现在就上 monorepo workspace）。
- Tailwind 配置（颜色变量、`text-xxs` 这类自定义 utility）应该抽成共享的 `tailwind.config` 预设，两个项目 `@import` 同一份，避免后台的紫色和前台的紫色哪天调色调岔了。
- 图标使用规范、按钮/输入框的圆角和阴影这些"设计 token"级别的东西，写进一份共享的 `DESIGN_SYSTEM.md`（可以后续从本文档和 `frontend/web/ARCHITECTURE.md` 里提炼），两边 PR review 时对照。

## 6. 页面结构规划

```
frontend/admin/src/
  App.tsx                 顶层路由（Header + Sidebar + 内容区，结构对齐 frontend/web/App.tsx）
  components/
    AdminHeader.tsx        对齐 frontend/web/Header.tsx
    AdminSidebar.tsx        导航：账户 / 供应商 / 虚拟模型与渠道 / 价格同步 / 审计日志
    DataTable.tsx           通用表格骨架（分页/排序/操作列），抽出来供各模块复用
    ConfirmDialog.tsx        危险操作二次确认（吊销 Key、驳回价格变更）
  pages/
    AccountsPage.tsx
    ApiKeysPage.tsx
    ProvidersPage.tsx        含"接入向导"（复刻 test_web 三步流程，但做成正式表单+进度条）
    VirtualModelsPage.tsx    虚拟模型 + 渠道 + 成本价/售价 一体化页面
    PriceSyncPage.tsx        价格变更审批队列 + 待上架队列
    AuditLogsPage.tsx
  api/                      与 frontend/web 同构的服务层（见下）
    client.ts
    auth.ts                 §3 的 sessionStorage token 存取，Phase 中期换成 JWT
    accounts.ts / providers.ts / catalog.ts / pricesync.ts / audit.ts
  types.ts
```

`api/client.ts` 的封装方式与 `frontend/web/ARCHITECTURE.md` §7 描述的 `client.ts` 同构（统一错误解析 `{"error":{message,type,code,request_id}}`、统一注入 `Authorization` 头），区别只是 `baseURL` 指向 `:8081` 且 token 来源是 §3 的登录态，不是 API Key。

## 7. 落地顺序建议

1. 先脚手架：从 `frontend/web` 拷贝 Vite/Tailwind/TS 配置骨架到 `frontend/admin`，建好 Header+Sidebar 空壳和登录页（§3 短期方案）。
2. 优先做"供应商接入 + 虚拟模型/渠道定价"（§4 中间两个模块）——这是当前 `frontend/test_web/admin.html` 覆盖、但用起来最痛苦（裸表单、无列表、无法编辑）的部分，做成正式后台收益最大。
3. 再做"价格同步审批队列"——目前后端已经有接口但完全没有 UI，运营只能靠直接调接口，是当前实际的运营痛点。
4. 账户/API 密钥管理、审计日志放最后，因为前端侧的自助体系（`frontend/web` Phase 1）一旦上线，运营代开 Key 的需求会下降。
5. `frontend/test_web/admin.html` 在 §2/§3 对应功能迁移完成后即可下线，仅保留给"新增后端接口时的最小手工验证"用途。
