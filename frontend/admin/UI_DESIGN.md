# uFreeTokens 运营管理后台（frontend/admin）UI 交互设计

> 配套阅读：[`ARCHITECTURE.md`](./ARCHITECTURE.md)（技术栈/鉴权/接口清单）、[`../web/ARCHITECTURE.md`](../web/ARCHITECTURE.md)。
> 本文回答的是 ARCHITECTURE.md 没回答的问题：**每个页面长什么样、运营人员怎么一步步完成工作、数据怎么展示和分析**。
> 视觉/组件模式尽量从 `frontend/web` 现有实现里取（文中标注了出处），后端接口缺口在 §9 统一列出。

---

## 0. 设计原则

1. **以运营任务为中心，而不是以接口为中心**。ARCHITECTURE.md §4 是按接口分组的（accounts / providers / channels…），但运营的真实工作是"接入一家上游并上架模型""处理今天的调价审批""给某个用户补偿余额"。导航和页面按任务组织，接口只是实现细节。
2. **先看后改**。当前 `cmd/admin` 几乎只有写接口（见 §9），test_web 的痛点正是"只能创建、看不到已有数据"。每个写操作都必须有一个能看到"现在是什么状态"的列表/详情页作为入口，写操作是从详情页发起的动作，而不是孤立的表单。
3. **钱相关操作防错优先于效率**。调账、赠送、改售价、审批调价都直接影响收入：必须展示"操作前 → 操作后"，必须二次确认，大额需要输入确认，全部可在审计日志追溯。
4. **每个对象都能深链**。后台是多人协作场景（"帮我看下 account 1234 的余额"），所以 admin 必须用 URL 路由（与 web 用 `activeNav` 字符串状态不同，这是有意的差异），任何详情页、筛选条件、审批项都能复制链接发给同事。
5. **信息密度对齐 web 的"控制台风格"**：`text-xs` 为主、数字一律 `font-mono` 右对齐、金额同时给出人类可读值。
6. **整体 UI 样式与 frontend/web 完全一致**：不引入新的颜色、字号、圆角、阴影或组件库，所有样式配方取自 web 现有代码，详见 §11。运营人员在两个系统间切换时，应感觉是同一个产品的两个区域。

---

## 1. 信息架构

### 1.1 左侧导航（AdminSidebar）

按任务域分组，组标题沿用 web `PersonalDashboardPage` 的分组标签样式（`text-[11px] uppercase tracking-wider text-gray-400`），导航项沿用其选中态（`bg-purple-100/70 text-purple-700 font-medium`）。**待办数量徽标**是后台与 web 最大的交互差异——运营每天打开后台第一眼要知道"有多少事等我处理"。

```
┌───────────────────────────┐
│ ◆ uFreeTokens Admin       │
├───────────────────────────┤
│ ▣ 工作台                   │  /
│                           │
│ 待办                       │
│ ⚖ 调价审批          [ 7 ]  │  /pricing/changes        ← 红点：有 blocked 项
│ ⊕ 待上架模型        [ 3 ]  │  /pricing/listings
│                           │
│ 供给                       │
│ ⛁ 供应商                   │  /providers
│ ⇄ 渠道                     │  /channels
│                           │
│ 目录与定价                  │
│ ◫ 虚拟模型                 │  /models
│ ¥ 价格源 & 汇率             │  /pricing/sources
│                           │
│ 用户与财务                  │
│ ☺ 账户                     │  /accounts
│ ⚿ API 密钥                 │  /api-keys
│                           │
│ 可观测                      │
│ ≋ 调用日志                  │  /logs
│ ▤ 用量分析                  │  /analytics
│ ⎙ 审计日志                  │  /audit
├───────────────────────────┤
│ ● 生产环境   actor: alice  │  ← 环境标识 + 当前操作人（X-Actor-ID）
└───────────────────────────┘
```

- 环境标识常驻底部：生产环境用 `bg-rose-50 text-rose-700`，测试环境用 `bg-amber-50 text-amber-700`，防止在生产误操作。Header 在生产环境额外加 2px 红色顶边。
- "当前操作人"在 Phase 0（共享 token）阶段由登录页填写姓名，作为所有写请求的 `X-Actor-Name` 头（现有的 `X-Actor-ID` 只接受数字，运营无从得知自己的 ID，见接口规格 §0.6）；RBAC 上线后改为服务端身份，UI 位置不变。
- 移动端：沿用 web `App.tsx` 的抽屉模式（`fixed inset-0 z-50` + `bg-black/40 backdrop-blur-xs` + `w-72 max-w-[85vw]`），但后台以桌面为主，移动端只保证"能查看、能审批"，复杂表单可以不适配。

### 1.2 AdminHeader

对齐 web `Header.tsx`（`h-12 sticky top-0 z-40 border-b`）：

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 供应商 / DeepSeek / 账号 ds-main          [🔍 跳转到… 账户ID/模型/渠道  ⌘K]  alice ▾ │
└────────────────────────────────────────────────────────────────────────────┘
```

- 左侧是**面包屑**而不是 web 的顶部 tab 导航（导航已在侧栏）。
- ⌘K 命令面板是后台的"万能跳转"：输入数字 → 候选"账户 #1234 / API Key #1234 / 渠道 #1234"；输入文本 → 匹配虚拟模型名、供应商 code；另有动作类命令（"新建供应商""发放赠送余额"）。在 web `CommandPalette.tsx` 基础上**补上方向键 ↑↓ 选择 + Enter 执行**（web 版目前没有键盘导航）。

---

## 2. 通用交互组件

从 web 各页面中抽取，在 admin 里做成真正复用的组件（web 目前是各文件内联复制，见 §10）。

| 组件 | 来源 | admin 中的约定 |
|---|---|---|
| `PageHeader` | — | 标题 `text-xl font-bold` + 一行说明 + 右侧主操作按钮（紫色实心，每页最多一个）+ 次要操作（灰色描边） |
| `StatCard` / `KpiStrip` | `PersonalDashboardPage` KPI 卡（`grid sm:grid-cols-3`，主卡 `bg-purple-50/60`） | 增加：环比变化（`▲ 12.3%` emerald / `▼` rose，参照 `RankingsPage` 的涨跌样式）、可点击跳转到对应明细 |
| `FilterBar` | `App.tsx` 筛选 tag 胶囊 + 可移除条件 chip + "清空全部条件" | 所有筛选同步到 URL query，刷新/分享不丢 |
| `DataTable` | `ModelTable.tsx` + `PersonalDashboardPage` 密钥表 | 表头可点击排序；勾选列 + 批量操作栏（勾选后在表头上方浮出 `N 项已选 · 批量操作…`）；行点击打开右侧 `DetailDrawer`；操作列 `MoreVertical` 菜单**必须处理点外关闭和 Esc**（web 版缺失）；数字列 `font-mono text-right` |
| `DetailDrawer` | 新增 | 右侧滑出 `w-[560px]` 面板，用于"看一眼不离开列表"（日志详情、审批详情、密钥详情）；顶部有"在新页面打开"按钮跳到完整详情页 |
| `FormModal` | web 新建 API 密钥弹窗（`rounded-2xl max-w-md p-6 shadow-2xl`，header/footer `border-gray-100` 分隔） | 创建类操作；提交中按钮 loading 且禁止重复提交；后端 400 错误就地显示在对应字段下 |
| `ConfirmDialog` | 新增 | 三级：普通确认 / 危险确认（rose 按钮）/ **输入确认**（需输入账户名或金额才能点确认，用于大额调账、批量审批、吊销 Key） |
| `SecretReveal` | web 创建 Key 成功视图（明文只展示一次 + 复制按钮 2s "已复制"） | 用于"代用户创建 API Key"的 RawKey；关闭前二次提示"关闭后无法再次查看" |
| `Money` | web `microToDisplay` | 统一显示 `¥12.345678`；hover 显示原始 micro 值；USD 成本价同时显示折算 CNY（按当前汇率，标注汇率日期） |
| `MoneyInput` | 新增 | 运营输入"元"，组件内部转 micro；输入框下方实时回显 `= 12,000,000 micro`，杜绝"少写 6 个 0"类事故 |
| `StatusBadge` | web 日志状态胶囊（`bg-emerald-50 text-emerald-700` 等） | 所有枚举集中映射，见 §2.1 |
| `TrendBars` | `PersonalDashboardPage` 消费趋势（`flex items-end gap-1 h-24` div 柱） | 补上：坐标轴刻度、hover 浮层（参照 `RankingsPage` 的 `group-hover:opacity-100` 深色浮层）、时间范围切换 |
| `StackedBars` / `ShareBar` | `RankingsPage` 堆叠柱图、100% 占比条 | 用于"按模型/渠道拆分的用量" |
| `SegmentedToggle` | `RankingsPage` 线性/对数切换（`bg-gray-100 p-1 rounded-lg`，选中 `bg-white shadow-xs`） | 时间范围（今日 / 7 天 / 30 天）、指标切换（请求数 / 收入 / 毛利） |
| `AnchorNav` | `ModelDetailPage`（`w-48 sticky top-28`，选中 `border-l-2 border-purple-600`） | 长详情页（虚拟模型、账户）的段落导航 |
| `JsonDiff` | 新增 | 审计日志 Before/After、价格变更前后对比；新增字段 emerald 底、删除 rose 底、修改的值并排显示 |
| `DataState` | web 的加载/错误/空态约定 | 包一层：`loading` → 骨架行；`error` → `bg-rose-50 border-rose-200` 卡片 + "重试"按钮 + request_id（可复制，便于查后端日志）；`empty` → 虚线空态卡片 + 引导动作 |
| `Toast` | web `Header.tsx` 局部 toast | 提升为全局 `useToast()`；成功 2.2s 自动消失，**失败不自动消失**，需手动关闭 |

### 2.1 状态徽标字典

| 对象 | 状态 → 颜色 |
|---|---|
| 账户 | active 绿 / suspended 琥珀 / closed 灰 |
| API Key | active 绿 / disabled 琥珀 / revoked 灰删除线 |
| 供应商、上游账号、渠道 | active 绿 / disabled 灰 |
| 上游密钥 | active 绿 / disabled 灰 / exhausted 琥珀 / revoked 灰删除线 |
| 虚拟模型 | active 绿 / hidden 蓝灰（"未公开"）/ deprecated 琥珀（"已废弃"，与 web 模型库的废弃可见性保持一致文案） |
| 调价申请 | pending 紫 / blocked rose（"超阈值被拦截"）/ approved、auto_approved、applied 绿 / rejected 灰 / superseded 灰斜体（"已被新申请取代"） |
| 调价方向 | up rose ▲ / down emerald ▼ / mixed 琥珀 ⇅ / new 蓝 ✦ / removed 灰 ✕ |
| 待上架 | pending 紫 / published 绿 / dismissed 灰 |
| 调用日志 | success 绿 / upstream_error rose；`usage_source=estimated` 额外加琥珀"估算"角标（与 web 调用日志一致） |

---

## 3. 通用页面模板

所有模块页面只用两种模板，降低学习成本。

### 3.1 列表页模板

```
┌ PageHeader ────────────────────────────────────────────────────────────┐
│ 渠道                                              [导出]  [+ 新建渠道]  │
│ 虚拟模型 → 上游账号的路由，决定请求实际打到哪里                          │
├ KpiStrip（可选，每页 3-4 个）───────────────────────────────────────────┤
│ 活跃渠道 128 │ 近 24h 错误率 0.8% ▲0.2 │ 负毛利渠道 2 ⚠ │ 未设成本价 5 ⚠ │
├ FilterBar ─────────────────────────────────────────────────────────────┤
│ 🔍 搜索…   供应商 ▾  状态 ▾  Tier ▾   [供应商: DeepSeek ✕] 清空全部条件 │
├ DataTable ─────────────────────────────────────────────────────────────┤
│ ☐ ID  虚拟模型          上游账号   上游模型      优先级 权重  毛利率  状态 ⋮ │
│ ☐ 41  deepseek-v4-flash ds-main   deepseek-chat   10   100  38.2%  ●   ⋮ │
│ ...                                                                     │
├────────────────────────────────────────────────────────────────────────┤
│ 共 128 条                                           ‹ 1 2 3 … 7 ›  50/页 │
└────────────────────────────────────────────────────────────────────────┘
```

- KPI 里的"⚠ 异常计数"可点击，等价于一键加上对应筛选条件——**把"发现问题"和"定位问题"连成一次点击**，这是后台数据展示最核心的交互。
- 分页：管理类列表用页码分页（运营需要"跳到第 N 页"和总数）；日志类用 web 已有的 keyset 无限滚动（IntersectionObserver 哨兵）。

### 3.2 详情页模板

沿用 `ModelDetailPage` 结构：

```
┌ 粘性操作栏 ─────────────────────────────────────────────────────────────┐
│ ← 虚拟模型 / deepseek-v4-flash   ● active      [调整售价] [编辑元数据] ⋮ │
├ Hero：4 个统计块（grid-cols-2 sm:grid-cols-4）─────────────────────────┤
│ 近7天请求 1.2M │ 近7天收入 ¥3,412 │ 毛利率 36.1% │ 错误率 0.4%          │
├──────────┬─────────────────────────────────────────────────────────────┤
│ AnchorNav│ section: 基本信息                                           │
│ · 基本信息 │ section: 用量趋势                                           │
│ · 用量趋势 │ section: 售价                                               │
│ · 售价     │ section: 渠道                                               │
│ · 渠道     │ section: 展示元数据                                         │
│ · 元数据   │ section: 操作记录（该对象的审计日志，时间线）                 │
│ · 操作记录 │                                                             │
└──────────┴─────────────────────────────────────────────────────────────┘
```

- **每个详情页最后一段都是"操作记录"**：调用 `GET /audit-logs?target_type=&target_id=`，把审计日志嵌入到对象上下文里，而不是只能去审计页面筛选。

---

## 4. 工作台（首页）

运营打开后台的第一屏，回答三个问题：**今天有什么要处理？平台运转正常吗？钱赚得怎么样？**

```
┌ 待办 ──────────────────────────────────────────────────────────────────┐
│ ⚖ 7 个调价待审批（2 个被拦截）→   ⊕ 3 个新模型待上架 →   ⚠ 2 个渠道负毛利 → │
└────────────────────────────────────────────────────────────────────────┘
┌ 时间范围 [今日|7天|30天] ──────────────────────────────────────────────┐
│ 请求数       收入(售价)     成本          毛利率        错误率     P95延迟 │
│ 1.24M ▲8%   ¥12,340 ▲5%   ¥7,880 ▲6%   36.1% ▼0.6   0.8% ▲0.2  2.1s    │
└────────────────────────────────────────────────────────────────────────┘
┌ 收入 / 成本 趋势（按天，堆叠：毛利 + 成本）──┐ ┌ 收入 Top 模型 ──────────────┐
│  ▇                                         │ │ 1 deepseek-v4-flash ¥3.4k ▲ │
│  █ ▇   ▇ █                                 │ │ 2 qwen3-max        ¥2.1k ▼ │
│  █ █ ▇ █ █ ...                             │ │ ...                  更多 → │
└────────────────────────────────────────────┘ └─────────────────────────────┘
┌ 渠道健康（按错误率降序，只列异常）─────────┐ ┌ 最近操作（审计日志）──────────┐
│ #41 ds-main/deepseek-chat  错误率 12% 🔴   │ │ 10:02 alice 批准调价 #88     │
│ #17 or-01/qwen-max         P95 9.2s  🟡   │ │ 09:40 bob   调账 账户#1234   │
└────────────────────────────────────────────┘ └─────────────────────────────┘
```

交互要点：

- 待办条永远在最上面；数量为 0 时显示"🎉 暂无待办"，不隐藏（让运营确信"真的没事"而不是"没加载出来"）。
- KPI 环比对比的是"上一个同等长度的时间窗"，hover 显示对比区间。
- 趋势图柱子 hover 浮层显示当天：请求数 / 收入 / 成本 / 毛利 / 错误率；点击某一天 → 跳转 `/analytics?date=…` 看当天拆分。
- Top 模型卡复用 `RankingsPage` 的排名卡（带涨跌），点击跳虚拟模型详情。
- 渠道健康卡只展示异常项（错误率 > 5% 或 P95 > 阈值），全部正常时显示"全部渠道正常"。
- **依赖后端**：这一页的数据全部需要新增统计接口（§9 G1），Phase 0 可以先只上"待办条 + 最近操作"两块（已有接口可支撑）。

---

## 5. 核心流程

### 5.1 供应商接入向导（替代 test_web 三步流程）

入口：供应商列表页主按钮"接入新供应商"。全屏向导（不是 modal，因为步骤多、信息量大），顶部步骤条：

```
 ①基本信息 ──── ②上游账号与密钥 ──── ③选择模型 ──── ④定价 ──── ⑤确认导入
    ✓               ✓                  ●
```

| 步骤 | 交互 |
|---|---|
| ① 基本信息 | 选择"已有供应商"或"新建"（Code/Name/Protocol）。Code 失焦时校验唯一性。Protocol 用三个带图标的单选卡（openai / anthropic / gemini），图标复用 web `ProviderIcon` |
| ② 上游账号与密钥 | 填 base_url、cost_multiplier（带说明"上游计费倍率，1 = 原价"）、密钥（`type=password` + 显示切换）。**"测试连接"按钮**：调用 upstream-models 接口，成功显示"✓ 连通，发现 86 个模型"，失败展示上游错误原文。测试通过前"下一步"可点但会弹出提示 |
| ③ 选择模型 | 表格列出上游模型（ID / OwnedBy / 本平台状态）。"本平台状态"列：`已上架`（灰，默认不勾选，不可重复导入）、`新模型`。顶部搜索 + "全选新模型"。**这一步自动并行发起参考价查询**，查询结果作为额外列回填（`OpenRouter $0.27 / $1.10`，未匹配显示"—"） |
| ④ 定价 | 每个勾选模型一行：参考成本（USD，可编辑）→ 折算 CNY → 加价率（全局滑杆 + 单行覆盖）→ 售价（每个模型一个售价，运行时不区分 tier，见接口规格 §1.3）。**实时计算毛利率**，毛利 < 0 行标红且阻止提交；未匹配到参考价的行标琥珀"需手动填写"。售价列支持"按参考价取整"快捷操作 |
| ⑤ 确认导入 | 汇总："将创建 12 个虚拟模型、12 条渠道、24 条价格"。点击导入后**逐行显示进度**（✓ / ✗ + 错误原因）；部分失败时提供"仅重试失败项"。完成后给两个出口："查看已导入模型"、"继续接入" |

- 向导状态存 `sessionStorage`，意外刷新可恢复；离开时如有未提交数据弹确认。
- 步骤间可回退修改，回退不丢失后续步骤已填写的数据。
- 以上各步的幂等逻辑（先查后建）沿用 test_web 已验证过的做法，UI 只负责把结果展示清楚。

### 5.2 调价审批（业务价值最高的页面）

采用**收件箱布局**（左列表 + 右详情），因为运营处理审批是"一条接一条"的连续动作，不应每次都打开/关闭弹窗。

```
┌ 调价审批 ── [待审批 7] [已拦截 2] [历史] ──────── 筛选: 方向▾ 供应商▾ ──┐
├─────────────────────────┬──────────────────────────────────────────────┤
│ ▲ #88 ds-main/deepseek… │ #88  deepseek-v4-flash  ·  渠道 #41 ds-main  │
│   +18.2%   2h 前   ●    │ 方向 ▲ 涨价   来源 L1 官方API   生效 10-01   │
│ ▼ #87 or-01/qwen-max    │                                              │
│   -5.0%    3h 前        │ 成本价变化                                    │
│ ⇅ #86 …  🔴 blocked     │ 计量          当前       新价格     变化       │
│   +62% 超阈值 50%       │ input/1M    $0.27  →  $0.32    +18.5% ▲    │
│ ...                     │ output/1M   $1.10  →  $1.30    +18.2% ▲    │
│                         │                                              │
│                         │ 对售价的影响                                  │
│                         │ free 售价 ¥3.00/1M，调整后毛利率 38% → 27% ⚠   │
│                         │ 近7天该渠道成本 ¥1,230 → 预计 ¥1,455 (+¥225)   │
│                         │                                              │
│                         │ [同时调整售价…]                                │
│                         │ 驳回原因(可选) [____________________]         │
│                         │           [驳回 R]      [批准 A]              │
└─────────────────────────┴──────────────────────────────────────────────┘
```

交互要点：

- **键盘优先**：`J/K` 上下切换、`A` 批准、`R` 驳回（驳回会聚焦原因输入框）、`Enter` 打开渠道详情。处理完一条自动跳到下一条，列表项带 200ms 淡出。
- **blocked 项置顶并解释原因**："变化幅度 +62% 超过阈值 50%，系统已拦截，需人工确认"；批准 blocked 项需要输入确认（ConfirmDialog 第三级）。
- **影响评估是审批的核心依据**：展示调价后毛利率变化 + 按近 7 天用量估算的成本变化金额；若调价后某档售价出现负毛利，红色警告，并提供"同时调整售价"入口（打开售价编辑器，批准和改售价在一次操作中完成）。
- **批量审批**：勾选多条后"批量批准"，仅允许对 `pending` 且变化幅度 < 10% 的项使用，确认框中列出全部条目。
- "历史" tab：已处理的申请，含处理人、处理时间、驳回原因。
- **依赖后端**：详情需要"变更前后价格组件"数据（§9 G4）；当前列表接口只有 `MaxChangeRatio` 和方向，Phase 0 只能展示这两个字段 + 跳转渠道详情。

### 5.3 待上架模型

卡片列表（每条是"上游出现了一个我们没有的模型"），信息来自 `ObservedSpec`：

```
┌──────────────────────────────────────────────────────────────┐
│ ✦ deepseek-v4-pro    DeepSeek    首次发现 3 天前 · 最近 1h 前   │
│ 上下文 128K · 最大输出 8K · 上游价 $0.55 / $2.19               │
│                                  [忽略]   [上架…]              │
└──────────────────────────────────────────────────────────────┘
```

- "上架…" 打开侧滑面板：虚拟模型字段**根据 ObservedSpec 预填**（name、family 由上游名推断、context_window、max_output），选择上游账号，加价率滑杆 + 实时预览售价和毛利率，底部预览"用户在模型库里看到的卡片"（直接复用 web `ModelGridCard` 的样式渲染）。
- "忽略"需要确认，并说明"忽略后该模型再次被观测到也不会重新进入队列"（需与后端确认这一语义）。
- 支持多选批量忽略；上架不支持批量（每个模型的定价需要单独判断）。

### 5.4 虚拟模型详情（目录与定价的中心页）

用 §3.2 详情页模板，各段：

1. **基本信息**：name / family / type / 上下文 / 能力 / 可见 tier。状态切换（active ↔ hidden ↔ deprecated）放在操作栏 `⋮` 菜单里，切换到 hidden / deprecated 时提示"将从公开模型库消失/标记为废弃，已接入用户仍可调用"。
2. **用量趋势**：`TrendBars` + 指标切换（请求 / 收入 / 毛利）+ 时间范围。
3. **售价**：当前生效售价的价格组件表 + 历史版本（运行时每个模型只有一个生效售价，不按 tier 区分，见接口规格 §1.3，因此这里不提供分档编辑）；"调整售价"打开**价格组件编辑器**：
   - 表格化编辑 meter / unit / unit_price，常用组合（input + output per_1m_tokens）一键添加；
   - 每行右侧实时显示"当前成本（取主渠道）"和毛利率，售价低于成本时整行标红并禁止提交；
   - 提交前展示新旧价格 diff（复用 `JsonDiff` 的表格形态），确认后生效。
4. **渠道**：该模型下所有渠道的表格——上游账号、上游模型、优先级、权重、成本价、毛利率、近 24h 错误率。权重列用一条横向占比条可视化流量分配（复用 `RankingsPage` 100% 占比条）。"添加渠道"在此处发起，虚拟模型字段自动带入。
5. **展示元数据**：左侧表单（display_name / description / provider_display / tags / scores 三项指数），右侧**实时预览 web 模型卡片**（所见即所得）。scores 用三个数字输入框而不是自由 JSON，从而强制遵守 web 约定的 `intelligenceIndex/codingIndex/agenticIndex` 键名。
6. **操作记录**：审计日志时间线。

### 5.5 账户详情与资金操作

账户列表支持按 ID / 邮箱 / 名称搜索（依赖 §9 G2）。详情页段落：概览（余额 KPI：现金 / 赠送 / 冻结，复用 web KPI 卡）→ 用量趋势 → API 密钥 → 资金流水 → 赠送余额 → 操作记录。

**人工调账**（风险最高的操作）：

```
┌ 人工调账 · 账户 #1234 张三的团队 ─────────────────┐
│ 类型   (●) 充值/补偿    ( ) 扣减                   │
│ 金额   [ 100.00 ] 元    = 100,000,000 micro        │
│ 关联单号 ref_id [ TICKET-5521 ]  *必填               │
│ 原因   [ 客户投诉补偿，工单 5521 ]  *必填             │
│                                                    │
│ 现金余额   ¥23.50  →  ¥123.50                       │
├────────────────────────────────────────────────────┤
│                         [取消]  [确认调账]           │
└────────────────────────────────────────────────────┘
```

- 用"类型"单选决定正负号，而不是让运营在金额里输入负数。
- ref_id 和原因必填；金额 ≥ ¥1,000（阈值可配置）时确认按钮变为"输入账户名确认"。
- 扣减后余额为负时阻止提交。
- 提交成功后 toast + 流水段落自动刷新并高亮新行。

**发放赠送余额**：source 用单选卡（注册 / 活动 / 补偿 / 邀请）；有效期用快捷选项（7 天 / 30 天 / 90 天 / 自定义）；model_scope 用模型多选（带搜索，复用 web `Sidebar` 的 `CheckboxGroup` "更多..." 模式），空 = 全部模型。

**代开 API Key**：沿用 web 创建 Key 弹窗；多出 allowed_models、RPM / TPM / 并发限制字段（折叠在"高级限制"里，默认不限制）。成功后走 `SecretReveal`。

### 5.6 调用日志与用量分析

**调用日志**（`/logs`）：沿用 web 调用日志的 keyset 无限滚动，但筛选更多——账户、API Key、虚拟模型、渠道、状态、error_code、时间窗。点击行打开 `DetailDrawer`：

- 计费明细：list_amount → 促销 → charged_amount，cost_amount / upstream_cost / fx_rate，毛利。
- **重试轨迹**（`attempt_trace`）：竖向时间线，每次尝试显示渠道、上游密钥 Last4、HTTP 状态、耗时，失败项 rose，最终成功项 emerald——排查"为什么这个请求慢/贵"的关键视图。
- 性能：TTFT、总延迟、tokens 分项（input / cache_read / cache_write / output / reasoning）。
- 跨链接：账户、渠道、虚拟模型全部可点击跳转。

**用量分析**（`/analytics`）：一个"透视"页面——

- 维度切换（SegmentedToggle）：按模型 / 按渠道 / 按供应商 / 按账户。
- 指标切换：请求数 / tokens / 收入 / 成本 / 毛利 / 错误率 / P95。
- 上半部分：按天堆叠柱图（Top 8 + 其他）；下半部分：排名表格（可排序），每行带占比条。
- 所有选择同步到 URL，便于分享"上周 DeepSeek 渠道的毛利分析"。

### 5.7 审计日志

- 时间线列表：`时间 · 操作人 · 动作 · 对象（可点击）`，动作用人话描述（"alice 将 渠道 #41 的成本价 input 从 $0.27 改为 $0.32"），而不是只显示 action code。
- 点击展开 `JsonDiff`（Before / After）。
- 筛选：对象类型、对象 ID、操作人、时间窗、动作类型。
- 只读，不提供任何写操作入口。

---

## 6. 反馈与异常状态

| 场景 | 交互 |
|---|---|
| 首次加载 | 骨架屏（表格灰色占位行、KPI 灰块），不用 web 的"正在加载..."纯文字——后台页面数据块多，骨架能保持布局稳定 |
| 刷新数据 | 保留旧数据，右上角小转圈；不闪白屏 |
| 接口 4xx | 表单类：字段级错误就地显示；非表单类：toast（不自动消失）+ request_id |
| 接口 5xx / 网络错误 | `DataState` 错误卡 + 重试按钮 |
| 401 | token 失效 → 清空 sessionStorage，跳登录页，登录后回到原 URL |
| 503（pricesync 未接线） | 相关页面显示"价格同步服务未启用"的说明卡，而不是报错 |
| 乐观更新 | 只用于低风险操作（如切换筛选）；所有资金/价格操作都等服务端返回成功后再更新 UI |
| 离开未保存页面 | `beforeunload` + 路由拦截确认 |

---

## 7. 快捷键

| 快捷键 | 作用 |
|---|---|
| `⌘K` / `Ctrl+K` | 命令面板 |
| `G` 然后 `D` / `P` / `M` / `A` / `L` | 跳转工作台 / 调价审批 / 虚拟模型 / 账户 / 日志 |
| `/` | 聚焦当前页搜索框 |
| `J` / `K`、`A` / `R` | 审批收件箱内上下切换、批准 / 驳回 |
| `Esc` | 关闭 Drawer / Modal / 菜单 |

快捷键在输入框聚焦时全部失效；按 `?` 显示快捷键帮助面板。

---

## 8. 路由表

```
/login
/                              工作台
/pricing/changes[?status=&id=]  调价审批（id 为收件箱当前选中项）
/pricing/listings               待上架模型
/pricing/sources                价格源 & 汇率
/providers                      供应商列表
/providers/new                  接入向导
/providers/:id                  供应商详情（上游账号、密钥、价格源）
/channels                       渠道列表
/channels/:id                   渠道详情（成本价、价格观测、健康、日志）
/models                         虚拟模型列表
/models/:id                     虚拟模型详情
/accounts                       账户列表
/accounts/:id                   账户详情
/api-keys                       API 密钥（全局检索）
/logs[?…filters]                调用日志
/analytics[?dim=&metric=&range=] 用量分析
/audit[?…filters]               审计日志
```

**已决定引入 `react-router`**（web 没有路由库，admin 因原则 4 需要深链）：

- 使用 v7 的 data router（`createBrowserRouter`），根路由为 `AdminLayout`（Header + Sidebar + `<Outlet/>`）；`/login` 在布局之外。
- 鉴权守卫放在根路由的 `loader`：`sessionStorage` 中没有 token 时 `redirect('/login?next=' + 当前路径)`。
- 列表页筛选、排序、分页一律存 `useSearchParams`，不存组件 state，刷新和分享都不丢。
- 审批收件箱的当前选中项用 `?id=`，而不是嵌套路由，保证 `J/K` 切换时列表不重新挂载。
- 各页面用 `lazy` 按路由拆包。
- 生产部署需要 Nginx `try_files $uri /index.html` 回退（与 web 的 `deploy/nginx/web.conf` 同理）。

---

## 9. 后端接口缺口（按阻塞程度排序）

当前 `cmd/admin`（`internal/app/admin.go:39-98`）以写接口为主：**没有任何列表接口可以浏览供应商/上游账号/虚拟模型/渠道/账户，也没有任何统计接口**。以下缺口决定了上文哪些页面能落地。

完整接口规格（参数、响应结构、口径、迁移、实施批次）见 [`docs/cmd-admin 运营后台接口补全技术方案.md`](../../docs/cmd-admin%20运营后台接口补全技术方案.md)。

| # | 缺口 | 阻塞的页面 | 建议 |
|---|---|---|---|
| G0 | 列表接口：`GET /providers`、`/provider-accounts`、`/virtual-models`（现在只能按 name 精确查单条）、`/channels`（现在要求三个参数全填、只返回单条）、`/price-sources`、`/fx-rates` | 几乎所有列表页、接入向导的"本平台状态"列 | 统一 `?q=&status=&page=&page_size=&sort=`，返回 `{data, total}` |
| G1 | 统计接口：基于 `request_logs` 按时间 × 维度（模型/渠道/供应商/账户）聚合请求数、tokens、收入、成本、错误率、P95 | 工作台、用量分析、各详情页的"用量趋势"与 KPI、渠道毛利/错误率列 | `GET /stats/usage?from=&to=&group_by=day,virtual_model&metrics=…`；与 console `/console/usage` 共用聚合逻辑 |
| G2 | 账户检索：`GET /accounts?q=`（ID/邮箱/名称） + `GET /accounts/{id}/ledger`（资金流水分页） + 赠送余额列表 | 账户列表、账户详情的流水/赠送段 | — |
| G3 | 全局调用日志：`GET /request-logs?account_id=&virtual_model=&channel_id=&status=&before=&limit=`，含 `attempt_trace` | 调用日志页、日志详情 Drawer | 复用 console logs 的 keyset 分页 |
| G4 | 调价申请详情：`GET /price-change-requests/{id}`，返回当前价格组件 vs 申请价格组件；列表支持 `?status=` 查询历史 | 审批收件箱右侧 diff、历史 tab | — |
| G5 | 编辑/停用：渠道改优先级/权重/状态、虚拟模型改状态、上游密钥停用、供应商停用 | 各详情页的状态切换和编辑 | 目前只有 metadata PUT 和 API Key 吊销 |
| G6 | 计数接口：待审批数、blocked 数、待上架数（或由列表接口的 `total` 提供） | 侧栏徽标、工作台待办条 | 可由 G0/G4 顺带解决 |
| G7 | 审计日志分页：当前只有 `limit`（≤500），需要 `before` 游标 + 按 actor / action 过滤 | 审计日志页 | — |
| G8 | JSON 字段命名统一：`internal/admin` 结构体大多没有 json tag（响应是 `ID`/`CreditLimit` 这种 PascalCase），自定义 body 却是 snake_case | 所有页面（前端需要一层字段归一化） | 后端补 json tag 最干净；否则在 `src/api/*` 里集中做 PascalCase → camelCase 转换，组件层不感知 |
| G9 | 渠道健康状态：熔断/冷却只在进程内存和 Redis，没有查询接口 | 工作台渠道健康卡、渠道详情的实时健康 | Phase 0 先用 G1 的错误率代替 |

---

## 10. 分阶段落地

与 ARCHITECTURE.md §7 的顺序对齐，按"已有接口能支撑多少"来切分：

| 阶段 | 页面 | 依赖的后端 |
|---|---|---|
| **P0 骨架**（已完成） | 登录页、Header / Sidebar / 路由、通用组件（§2）、全局 toast、⌘K（先只做页面跳转） | 无 |
| **P1 接入与定价**（已完成） | 供应商接入向导（§5.1）、待上架模型（§5.3）、调价审批收件箱的基础版（无 diff）、审计日志（只有 limit） | 现有接口即可 |
| **P2 可浏览**（已完成） | 供应商/渠道/虚拟模型列表与详情、价格组件编辑器、元数据编辑 + 卡片预览、调价 diff 与影响评估 | G0、G4、G5、G8 |
| **P3 用户与财务**（已完成） | 账户列表/详情、调账/赠送/代开 Key、资金流水 | G2 |
| **P4 可观测**（已完成，渠道实时健康 G9 仍用错误率代替） | 工作台完整版、用量分析、全局调用日志 + 重试轨迹 | G1、G3、G6、G9 |

### 顺带修正 web 中不应被复制到 admin 的问题

调研 `frontend/web` 时发现以下问题，admin 实现时直接规避（web 侧可另行修复）：

- `animate-in fade-in zoom-in-95` 等类名在项目里没有对应插件，**实际不生效**；admin 要么引入 `tw-animate-css`，要么用已安装但未使用的 `motion`。
- `MoreVertical` 行菜单没有点外关闭；命令面板没有方向键导航；toast 只存在于 Header 内部而非全局——admin 的通用组件里一并补齐。
- KPI 卡、导航项、加载/错误态在 web 各页面内联复制，admin 从一开始就抽成组件（§2），将来可按 ARCHITECTURE.md §5 的思路回流为共享的 `@ufreetokens/ui`。

---

## 11. 视觉样式规范（与 frontend/web 保持一致）

本节把 `frontend/web` 里**实际在用**的样式整理成 admin 必须遵守的规范。所有 class 配方都从 web 源码中统计得出，不是新设计。admin 相对 web 只有 §11.9 列出的几处差异，每处都由 web 已有的样式组合而成。

### 11.1 全局基础

| 项 | 规范 | 来源 |
|---|---|---|
| 全局样式 | **原样复制** `frontend/web/src/index.css`：`@import "tailwindcss"`、`.text-xxs`（11px）、紫色 range 滑杆、5px 细滚动条 | `web/src/index.css` |
| `<html>` | `lang="zh-CN"` | `web/index.html` |
| `<body>` | `bg-white text-gray-900 antialiased selection:bg-purple-100` | `web/index.html` |
| 字体 | Tailwind 默认 `font-sans` 字体栈，不引入 Web 字体；数字、ID、价格、Key 前缀、request_id 用 `font-mono` | web 全局 |
| 图标 | `lucide-react`，只用 className 控制尺寸：正文和按钮内 `w-3.5 h-3.5`（最常用），导航和标题 `w-4 h-4`，行内小图标 `w-3 h-3`，空态插图 `w-8 h-8` | web 组件统计 |
| 依赖版本 | 与 `web/package.json` 锁定一致：React 19、Vite 8、Tailwind 4（`@tailwindcss/vite`）、`lucide-react`、TypeScript。admin 只额外引入 `react-router`（§8）和 `tw-animate-css`（§11.7） | `web/package.json` |

### 11.2 色彩

| 角色 | Tailwind 类 | 用法 |
|---|---|---|
| 主色 | `purple-600`（`#7C3AED`），hover `purple-700` | 主按钮、选中态、链接、聚焦边框、滑杆 |
| 主色浅底 | `purple-50` / `purple-50/60` / `purple-100/70`，边框 `purple-100` / `purple-200`，文字 `purple-700` | 主 KPI 卡、导航选中项、hover 高亮行（`hover:bg-purple-50/30`） |
| 深色强调 | `gray-900`，hover `gray-800` | 胶囊筛选的选中态、toast 背景（`bg-gray-900/90`）、次级深色按钮 |
| 正文 | `gray-900`（标题/主数据）、`gray-700`（正文）、`gray-500`（次要）、`gray-400`（说明、占位、表头标签） | |
| 边框与分割 | 边框 `gray-200`，hover `gray-300`；分割线 `gray-100`（`divide-gray-100`、`border-gray-100`） | |
| 底色 | 页面 `white`；表头、次级卡片 `gray-50`；hover 行 `gray-50/70` | |
| 成功 | `emerald-50` 底 / `emerald-700` 字 / `emerald-200` 边框；实心点 `emerald-500` | 成功、成本下降、正毛利 |
| 危险 | `rose-50` 底 / `rose-600`、`rose-700` 字 / `rose-200` 边框 | 失败、涨价、负毛利、危险操作 |
| 警告 | `amber-50`（或 `amber-50/80`）底 / `amber-700`、`amber-900` 字 / `amber-200` 边框；实心点 `amber-500` | 被拦截、估算用量、需要注意 |
| 信息 | `blue-50` 底 / `blue-700` 字 / `blue-200` 边框；实心点 `blue-500` | 仅用于"新模型""未公开"两类中性提示，不扩大使用范围 |

规则：

- 语义色只用于表达状态，不作装饰；同一屏的主色实心按钮只能有一个。
- §2.1 状态徽标字典中的颜色必须全部落在上表范围内。
- 图表配色：单序列用 `purple-500`（与 web 消费趋势图一致）；多序列依次用 `purple-600`、`purple-400`、`purple-200`、`gray-300`，"其他"分组用 `gray-200`；毛利与成本的堆叠用 `emerald-500` 与 `gray-300`。

### 11.3 字号与字重

| 层级 | 类 | 用在 |
|---|---|---|
| 页面标题 | `text-xl font-bold text-gray-900`（详情页 Hero 可用 `text-2xl`） | PageHeader |
| 区块标题 | `text-sm font-semibold text-gray-900` | 详情页 section、卡片标题 |
| 正文 / 表格 | `text-xs`（12px） | 全局默认 |
| 侧栏导航 | `text-[13px]` | AdminSidebar 导航项 |
| 辅助说明 | `text-[11px] text-gray-400`（或 `.text-xxs`） | KPI 卡副行、表单说明、时间戳 |
| 分组标签 | `text-[10px] text-gray-400 uppercase tracking-wider font-semibold` | 表头、Hero 统计块标签、侧栏分组名 |
| KPI 数值 | `text-2xl font-bold`，金额和数字额外加 `font-mono` | StatCard |

### 11.4 圆角、阴影、间距

| 元素 | 圆角 | 阴影 |
|---|---|---|
| 按钮、输入框、下拉、菜单 | `rounded-lg` | 按钮 `shadow-xs` 或 `shadow-2xs`；下拉菜单 `shadow-lg` |
| 卡片、表格外框、提示框 | `rounded-xl` | `shadow-xs` |
| 弹窗 | `rounded-2xl` | `shadow-2xl` |
| 胶囊、徽标、状态点 | `rounded-full` | 无 |

间距：页面主区域 `px-8 py-6`，内容最大宽度 `max-w-7xl`（后台页面以表格为主，统一放宽；web 个人中心是 `max-w-6xl`）；卡片内边距 `p-4`（紧凑）/ `p-5`（常规）；区块之间 `space-y-6`；KPI 网格 `gap-4`。

### 11.5 组件样式配方

以下是 admin 通用组件（§2）的基础 class。实现时封装在组件内部，页面代码不再手写这些长串。

**按钮**

```
主按钮     bg-purple-600 hover:bg-purple-700 disabled:opacity-50 text-white rounded-lg text-xs font-medium px-3 py-1.5 shadow-xs cursor-pointer
次按钮     border border-gray-200 text-gray-700 rounded-lg text-xs font-medium px-3 py-1.5 hover:bg-gray-50 shadow-2xs cursor-pointer
深色按钮   bg-gray-900 hover:bg-gray-800 text-white text-xs rounded-lg px-3 py-1.5 cursor-pointer
危险文字   text-xs text-rose-600 hover:text-rose-700
危险按钮   bg-rose-600 hover:bg-rose-700 disabled:opacity-50 text-white rounded-lg text-xs font-medium px-3 py-1.5 shadow-xs cursor-pointer
图标按钮   p-1 text-gray-400 hover:text-gray-700（危险操作 hover:text-rose-600）
```

"危险按钮"是 web 中没有的唯一按钮变体，只用于 ConfirmDialog 的最终确认，由主按钮配方换成 rose 色得到。

**表单**

```
输入框     w-full border border-gray-200 rounded-lg px-3 py-2 text-xs focus:outline-none focus:border-purple-500
等宽输入   输入框 + font-mono（金额、ID、URL、Key）
搜索框     w-full bg-gray-50 border border-gray-200 rounded-xl px-3.5 py-2 text-xs text-gray-900 placeholder-gray-400 focus:outline-none focus:border-purple-400 focus:bg-white transition-colors（左侧放 Search 图标）
下拉选择   border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 focus:outline-none focus:border-purple-400 shadow-xs cursor-pointer
字段标签   text-xs font-medium text-gray-700 mb-1
字段错误   text-[11px] text-rose-600 mt-1；同时输入框边框改为 border-rose-300
```

**卡片与提示框**

```
普通卡片   bg-white border border-gray-200 rounded-xl p-5 shadow-xs
可点卡片   普通卡片 + hover:bg-purple-50/30 hover:border-purple-200 transition-all cursor-pointer
主 KPI 卡  bg-purple-50/60 border border-purple-100 rounded-xl p-4
次 KPI 卡  bg-gray-50 border border-gray-200 rounded-xl p-4
警告框     bg-amber-50/80 border border-amber-200 rounded-xl p-4 flex items-start gap-3 text-xs text-amber-900
错误框     bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-4 text-xs
空态       bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center
加载       text-xs text-gray-400 py-6 text-center；骨架块 bg-gray-100 rounded animate-pulse
```

**表格**（对齐 `ModelTable.tsx`）

```
外框       overflow-x-auto bg-white border border-gray-200 rounded-xl shadow-xs
表头行     bg-gray-50 text-[10px] text-gray-400 uppercase tracking-wider font-semibold
单元格     px-4 py-2.5 text-xs
数据行     divide-y divide-gray-100；hover:bg-gray-50/70 group cursor-pointer
数字列     font-mono text-right text-gray-900
次要 ID    font-mono text-[11px] text-gray-400
```

**徽标、胶囊、切换**

```
状态徽标   inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium border + 语义色三件套（如 bg-emerald-50 text-emerald-700 border-emerald-200），前置 w-1.5 h-1.5 rounded-full 实心点
计数徽标   min-w-4 h-4 px-1 rounded-full bg-purple-600 text-white text-[10px] font-semibold（存在 blocked 项时改为 bg-rose-600）
筛选胶囊   px-3 py-1 rounded-full text-xs border border-gray-200 text-gray-600 hover:border-gray-300；选中 bg-gray-900 text-white border-gray-900 shadow-xs
条件 chip  沿用 web App.tsx 的可移除筛选条件样式（带 X 图标），末尾"清空全部条件"为 text-xs text-purple-600 链接
分段切换   容器 bg-gray-100 p-1 rounded-lg；选项 px-2.5 py-1 rounded-md text-xs text-gray-500；选中 bg-white text-gray-900 shadow-xs
```

**浮层**

```
下拉菜单   absolute bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-30 text-xs
  菜单项   w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-gray-50
  危险项   hover:bg-rose-50 text-rose-600 border-t border-gray-100
弹窗遮罩   fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4
弹窗面板   bg-white rounded-2xl w-full max-w-md shadow-2xl
  头部     px-6 pt-6 pb-4 border-b border-gray-100
  底部     px-6 py-4 border-t border-gray-100 flex justify-end gap-2
抽屉       fixed inset-y-0 right-0 z-50 w-[560px] max-w-[95vw] bg-white border-l border-gray-200 shadow-2xl（遮罩同弹窗，改用 bg-black/20）
Toast      fixed top-28 right-4 z-50 bg-gray-900/90 backdrop-blur-md text-white px-3.5 py-2 rounded-lg shadow-lg text-xs flex items-center gap-2（前置紫色圆点；失败时圆点换成 rose-400）
Tooltip    bg-gray-900 text-white text-[11px] rounded-md px-2 py-1 shadow-lg（沿用 RankingsPage 的 group-hover:opacity-100 做法）
```

### 11.6 布局骨架

| 区域 | 规范 | 来源 |
|---|---|---|
| Header | `h-12 border-b border-gray-200 px-3 md:px-4 flex items-center justify-between text-xs bg-white sticky top-0 z-40 select-none` | `Header.tsx` |
| Sidebar | `w-56 border-r border-gray-200 bg-white`，导航区 `px-2.5 py-3 space-y-4 text-[13px]`；导航项 `px-2.5 py-1.5 rounded-lg text-gray-600 hover:bg-gray-50`，选中 `bg-purple-100/70 text-purple-700 font-medium` | `PersonalDashboardPage.tsx` 左侧导航 |
| 主内容 | `flex-1 overflow-y-auto h-[calc(100vh-3rem)]`，内层 `px-8 py-6 max-w-6xl` | `App.tsx`、`PersonalDashboardPage.tsx` |
| 粘性二级栏 | `sticky top-12 z-30 bg-white/95 backdrop-blur-md border-b border-gray-100` | `RankingsPage.tsx`、`ModelDetailPage.tsx` |
| 锚点导航 | `w-48 sticky top-28`，选中 `border-l-2 border-purple-600 text-purple-700` | `ModelDetailPage.tsx` |
| 移动端抽屉 | 遮罩 `fixed inset-0 z-50 bg-black/40 backdrop-blur-xs`，面板 `w-72 max-w-[85vw]` | `App.tsx` |

z-index 层级沿用 web：内容内浮层 `z-20`/`z-30`，Header `z-40`，弹窗、抽屉、toast `z-50`。

### 11.7 动效

- web 中的 `animate-in fade-in zoom-in-95 slide-in-from-top-2` 类名目前**不生效**（缺少对应插件）。admin 引入 `tw-animate-css`（在 `index.css` 中 `@import "tw-animate-css"`），让**同样的类名**生效。web 可用同一方式修复，两边类名保持一致。
- 统一时长：hover 与颜色过渡用 `transition-colors`（默认 150ms）；弹窗和 toast 进入用 `duration-200`；抽屉用 `slide-in-from-right duration-200`。
- 不做页面切换动画和数字滚动动画——后台以效率为先。

### 11.8 共享方式

分两步，避免两边样式长期漂移（对应 ARCHITECTURE.md §5）：

1. **P0（立即）**：admin 复制 web 的 `index.css`；本节作为唯一规范，两边 PR review 时对照。
2. **P2 之前**：抽出 `frontend/shared/theme.css`。Tailwind 4 用 CSS 做配置，文件内容为 `.text-xxs`、滑杆、滚动条、`tw-animate-css` 引入，以及以后需要的 `@theme` 变量。web 和 admin 的 `index.css` 都改为 `@import "../../shared/theme.css"`。通用 UI 组件是否上升为共享包，等 admin 组件稳定后再决定。

### 11.9 admin 相对 web 的样式差异（仅此几处）

| 差异 | 样式 | 理由 |
|---|---|---|
| 环境标识 | 侧栏底部徽标：生产 `bg-rose-50 text-rose-700 border-rose-200`，测试 `bg-amber-50 text-amber-700 border-amber-200`；生产环境 Header 额外加 `border-t-2 border-rose-500` | 防止在生产环境误操作 |
| 待办计数徽标 | 见 §11.5 计数徽标 | web 没有待办概念 |
| 右侧抽屉 | 见 §11.5 浮层 | 后台"看一眼不离开列表"的高频需求 |
| 危险按钮 | 见 §11.5 按钮 | 资金、价格类操作的最终确认 |
| 差异对比色 | 新增 `bg-emerald-50`；删除 `bg-rose-50 line-through`；修改时旧值 `text-gray-400 line-through`，新值 `text-gray-900 font-medium` | 调价审批和审计日志的前后对比 |
| Toast 位置 | `top-28`（web 是 `top-14`） | 后台详情页有 `sticky top-12` 的操作栏，`top-14` 的 toast 会正好盖住主操作按钮；失败 toast 不自动消失，会一直挡住。浏览器联调时发现 |
| 网页标题 | `<title>uFreeTokens 运营后台</title>`，不设 SEO / OG meta，并加 `<meta name="robots" content="noindex">` | 内部系统 |

### 11.10 PR 自查清单

- [ ] 没有出现 §11.2 以外的颜色（`indigo`、`violet`、`green`、`red`、`yellow` 应分别改用 `purple`、`emerald`、`rose`、`amber`）。
- [ ] 数字、金额、ID 都用了 `font-mono`，表格数字列右对齐。
- [ ] 没有手写 §11.5 中已有配方的长 class，而是用了对应组件。
- [ ] 下拉菜单能点外关闭、能按 Esc 关闭；弹窗能按 Esc 关闭；提交中按钮已禁用。
- [ ] 加载、空、错误三种状态都有处理，样式符合 §11.5。
- [ ] 与 web 对应页面并排看，字号、圆角、阴影、间距一致。
