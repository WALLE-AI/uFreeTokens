# frontend/web 文档模块优化技术方案

> 范围：`frontend/web` 的 `/docs/*`（`DocsPage.tsx`、`DocsSidebar.tsx`、`DocArticleView.tsx`、`data/docsContentData.ts`、`data/docsNavigationData.ts`），以及为文档提供事实来源所需的少量 `cmd/gateway` 改动。
> 读者：接入 uFreeTokens `/v1/*` 数据面的第三方开发者。
> 原则：**只写已经实现的功能**；**唯一事实来源是 Go 代码**（按 Go 代码 → OpenAPI → 文档 的顺序派生）；**文档中的每段代码都能直接运行**；**每个页面、每个锚点都能通过链接直接访问**。

---

## 1. 现状诊断

### 1.1 P0：内容与真实接口脱节（开发者照着文档写代码会直接失败）

| 问题 | 位置 | 真实情况（`docs/API.md` / `internal/app/gateway.go`） |
|---|---|---|
| API 参考列出的 10 个接口里，大部分并不存在：`/generation`、`/endpoints/zdr`、`/models/{}/endpoints`、`/files`、`/guardrails`、`/activity`、`/responses` | `DocsPage.tsx:72-770` `API_ENDPOINTS` | 实际开放的只有 `GET /v1/catalog`、`GET /v1/models`、`GET /v1/usage`、`POST /v1/chat/completions`、`POST /v1/embeddings`、`POST /v1/messages` |
| 把 TTS/STT 写成可用接口 | `post-speech` / `post-transcriptions` | `/v1/audio/*` 返回 `503 not_implemented` |
| Base URL 写成 `https://ufreetokens.com/api/v1` | 全部示例 | 实际路径是 `/v1`（dev 走 Vite proxy，prod 走 Nginx 反代，见 `deploy/nginx/web.conf`） |
| 以美元计价（"精确到 $0.000001"） | `usageNote`、FAQ、AI 回答 | 平台以人民币计价，金额字段单位为 int64 微元（1,000,000 = 1 元） |
| 示例使用 `anthropic/claude-3.7-sonnet`、`ufreetokens/auto` 等模型 ID，平台上并不存在 | 各处示例 | 真实模型 ID 形如 `deepseek-ai/DeepSeek-V4-Flash`，以 `/v1/catalog` 返回为准 |
| 导航约 100 项，基本照搬 OpenRouter 的目录（Ori、Stripe Projects、SCIM、Chatting with Interns……） | `docsNavigationData.ts` | 平台没有对应功能 |
| 找不到文章时由 `getDocArticle` 自动拼一篇假文章（带 RBAC、Auto Fallback 等功能宣称） | `docsContentData.ts:252` | 导航中大多数条目实际打开的都是这篇模板生成的文章 |
| 每篇文章底部都硬编码了"ZDR 零数据保留"承诺 | `DocArticleView.tsx:216` | 这是未经核实的合规承诺，存在法律风险 |
| Changelog 的日期与条目是编造的（2026-07-29 等） | `DocsPage.tsx:1212-1440` | 没有真实的变更记录 |

### 1.2 P0：交互是假的

- **Try it**：`handleTryIt`（`DocsPage.tsx:876`）用 `setTimeout` 加随机延迟返回写死的 JSON。
- **Ask AI**：`handleAskQuestion`（`DocsPage.tsx:897`）只做关键词匹配，返回写死的文案，其中还包含错误信息。
- **状态徽标**："API v1 · 运行正常"是硬编码的（`DocsPage.tsx:982`）。
- **外链**：顶部的"官方规范"外链指向 `https://ufreetokens.com/docs`，那里并没有这份文档。

### 1.3 P1：架构问题

- `DocsPage.tsx` 共 1851 行，数据、状态（20+ 个 `useState`）和五个视图全塞在一个组件里。
- 虽然 `App.tsx:1012` 注册了 `/docs/*`，但页面状态并不写入 URL：无法深链或分享，浏览器后退失效，"复制页面链接"永远只复制到 `/docs`。
- `topTab` 与 `isViewingApiEndpoint` 两份状态互相覆盖。初始状态是 `api-ref` 标签页，而侧边栏高亮的是 `provider-selection`，两者本身就不一致。
- 内容以 TS 字符串形式写在代码里：不支持 Markdown、没有语法高亮，每改一个字都要改代码。
- `DocsPage` 在 `App.tsx` 中被静态 import，没有做按需加载，这部分体积会进入首屏包。

### 1.4 P1：体验问题

- 搜索只匹配标题和描述，不覆盖正文，也没有 `Cmd+K` 快捷键。
- 缺少页内目录（TOC）、标题锚点、上一篇/下一篇导航。
- 侧边栏是深色（`#090a0f`），正文是浅色，视觉割裂。
- "客户端 SDK / Agent SDK / Cookbook"三个标签页介绍的是并不存在的 `@ufreetokens/sdk`、CLI 等。

---

## 2. 目标

1. 开发者可以在 **5 分钟内**按照快速开始跑通第一次真实调用。
2. API 参考**从 Go 代码生成**，CI 保证文档与实现一致，不会出现"文档里有、接口不存在"的情况。
3. Try it 发起的是**真实请求**：展示真实状态码、`X-Request-Id`、耗时和流式输出。
4. 每个页面、每个标题都有稳定的 URL，可以分享、收藏，也能被搜索引擎和 LLM 抓取。
5. 编写内容只需新增或修改 `.mdx` 文件，不用改组件。

---

## 3. 新的信息架构

所有文档 URL 都带语言前缀：`/docs/{zh|en}/...`（见 4.9）。下表中省略了这个前缀。

文档站点只保留两个顶部标签页：**开发文档**和 **API 参考**。SDK、Agent、Cookbook 三个标签页先下线，等有真实产物后再以"集成"文章的形式补回来。

```
开发文档 /docs
├─ 入门
│  ├─ 概览                    /docs                     平台定位、能做什么、不能做什么
│  ├─ 快速开始                /docs/quickstart          注册 → 建 Key → curl/Python/Node 各一段
│  ├─ 鉴权与 API Key          /docs/authentication      Bearer sk-uft-...；Key 只展示一次；吊销；BYOK 浏览器存储说明
│  ├─ 模型与模型 ID           /docs/models              /v1/catalog vs /v1/models 的区别；tier 可见性；实时模型列表组件
│  └─ 计费与余额              /docs/billing             CNY、微元、预扣/结算、现金与赠送余额、流中断时的估算计费
├─ 指南
│  ├─ 流式输出 (SSE)          /docs/streaming           stream_options.include_usage 的转发规则；断流计费
│  ├─ Anthropic 兼容接口      /docs/anthropic-messages  /v1/messages 的基础非流式调用；Anthropic SDK 的 base_url 配置（stream/tools 本期不写）
│  ├─ 工具调用                /docs/tool-calling        只针对 capabilities 含 tools 的模型
│  ├─ Embeddings              /docs/embeddings
│  ├─ 用量查询                /docs/usage               /v1/usage；与控制台用量的区别
│  ├─ 错误处理与重试          /docs/errors              完整错误码表（生成）、可重试错误、退避建议
│  ├─ 限流与并发              /docs/rate-limits         rate_limit_exceeded / concurrency_limit_exceeded
│  └─ 排障：请求 ID           /docs/request-id          X-Request-Id 的透传与生成、提工单时需要提供的信息
├─ 集成（只收录已实测可用的）
│  ├─ OpenAI SDK (Python / Node)
│  ├─ Anthropic SDK（基础非流式调用）
│  ├─ LangChain
│  └─ Vercel AI SDK
└─ 更新日志                   /docs/changelog           真实记录，手写 MDX，按版本倒序

API 参考 /docs/api
├─ 概述（Base URL、鉴权、错误格式、金额单位）
├─ GET  /v1/catalog
├─ GET  /v1/models
├─ GET  /v1/usage
├─ POST /v1/chat/completions
├─ POST /v1/embeddings
├─ POST /v1/messages
└─ 尚未实现（返回 503）：completions / images / audio
```

`/console/*` 是 Web 前端自己的会话接口，**不对第三方开放**，不写进开发者文档。

---

## 4. 技术方案

### 4.1 路由与代码结构

```
src/docs/
  DocsLayout.tsx          顶栏（含语言切换）+ 侧栏 + <Outlet/> + 右侧 TOC，负责三栏响应式布局
  routes.tsx              /docs → 重定向到 /docs/{lang}；/docs/:lang, /docs/:lang/:slug, /docs/:lang/api, /docs/:lang/api/:operationId
  manifest.ts             import.meta.glob('./content/**/*.mdx', { eager: false }) + frontmatter → 按语言分组的导航树
  i18n/
    ui.ts                 界面文案字典 { zh: {...}, en: {...} } + useDocsLocale()
    locale.ts             语言解析：URL > localStorage('uft.docs.lang') > navigator.language > zh
  components/
    Sidebar.tsx           由 manifest 生成，不再手写导航数据
    Toc.tsx               rehype-slug 生成的 h2/h3 + IntersectionObserver 滚动高亮
    PrevNext.tsx
    SearchDialog.tsx      Cmd+K
    mdx/                  供 MDX 使用的组件
      Callout.tsx  Steps.tsx  CodeTabs.tsx  ParamTable.tsx
      LiveModelList.tsx   调用 /v1/catalog 渲染当前真实可用的模型
      ErrorCodeTable.tsx  读取生成的错误码表
  api-reference/
    OperationPage.tsx     根据 OpenAPI operation 渲染：说明、参数、请求体 schema、响应、示例
    SchemaView.tsx        可折叠的 JSON Schema 树
    snippets.ts           根据 operation + example 生成 curl / Python / Node 代码
    TryIt.tsx             真实请求面板（见 4.4）
  content/
    zh/  index.mdx  quickstart.mdx  authentication.mdx ...  changelog.mdx
    en/  index.mdx  quickstart.mdx  authentication.mdx ...  changelog.mdx
```

- `App.tsx` 改为 `const DocsRoutes = lazy(() => import('./docs/routes'))`，把文档从首屏包中拆出去。
- 所有页面状态都来自 URL，包括当前页面、当前 operation 和代码语言。代码语言存在 `?lang=` 与 localStorage 中，全站同步。
- 删除：`components/DocsPage.tsx`、`DocsSidebar.tsx`、`DocArticleView.tsx`、`data/docsContentData.ts`、`data/docsNavigationData.ts`。
- 旧的 `activeDocId` 格式（例如 `multimodal:multimodal-vision`）从未进入 URL，因此不需要做兼容重定向。

### 4.2 内容管线：MDX

采用 MDX，不采用 VitePress 或 Docusaurus 这类独立站点，原因是文档需要和主站共用顶栏、`authStore`（API Key）以及 `/v1/catalog` 数据，同一个 SPA 最简单。

依赖（仅 devDependencies，构建期处理）：

- `@mdx-js/rollup`：编译 `.mdx`，以 Vite 插件形式接入 `vite.config.ts`
- `remark-gfm`：表格、任务列表
- `remark-frontmatter` + `remark-mdx-frontmatter`：解析 frontmatter
- `rehype-slug` + `rehype-autolink-headings`：为标题生成锚点
- `@shikijs/rehype`：**构建期**语法高亮，运行时零开销，支持浅色和深色两套主题

frontmatter 约定：

```yaml
---
title: 流式输出 (SSE)
description: 如何接收 text/event-stream，以及 usage chunk 的转发规则
section: guides        # getting-started | guides | integrations | changelog
order: 10
since: v0.7            # 可选，对应 changelog
---
```

`manifest.ts` 按 `语言` → `section` → `order` 生成侧栏。同一篇文档在 `zh/` 和 `en/` 下使用**相同的文件名（slug）**，语言切换时只替换 URL 中的语言段。**导航和内容合为一处**：新增一篇文档就是新增一个文件。

代码块约定：

- 统一用 `$UFREETOKENS_API_KEY` 占位，**绝不**把用户的真实 Key 注入可复制的代码。
- Base URL 用占位符 `{{BASE_URL}}`，由 `CodeBlock` 组件在运行时替换为 `GATEWAY_BASE_URL || window.location.origin`（复用 `src/api/client.ts`），这样 dev、预发、生产环境各自显示正确的地址。构建期产物（`llms.txt`、meta 标签）因为拿不到运行时 origin，固定使用生产域名 `https://ufreetokens.com`，通过 `VITE_DOCS_PUBLIC_ORIGIN` 配置。
- 代码本身两种语言共用；只有注释和示例 prompt 的文字随语言变化。

### 4.3 API 参考：从 Go 代码生成 OpenAPI

后台已经有成熟做法：`internal/app/admin_openapi.go` 通过反射 Go 类型生成 `docs/admin-openapi.json`，并由 `TestAdminOpenAPI_UpToDate` 保证与代码一致。数据面照搬这一做法：

1. **后端**：新增 `internal/app/gateway_openapi.go`，提供 `GatewayOpenAPI()`，登记 6 个已实现的 operation，以及它们的请求/响应类型、鉴权方式、限流说明和 `x-status: not_implemented` 的占位接口。
   - chat/embeddings/messages 的请求体是 OpenAI/Anthropic 的协议形状，网关大多是透传。这些 schema 不靠反射，而是手写一份**只包含网关会读取或改写的字段**的精简 schema（`model`、`messages`、`stream`、`stream_options`、`tools`、`max_tokens`……），其余字段用 `additionalProperties: true` 并注明"原样透传给上游"。
   - 错误码：把 `invalid_api_key`、`insufficient_balance` 等 code 常量集中到一处（如果目前分散在各包中，这一步先做收敛），同时生成 `components.x-error-codes`，包含 HTTP 状态码、是否可重试和说明。
2. **产物**：`docs/gateway-openapi.json`，并新增 `TestGatewayOpenAPI_UpToDate`，接口有变动而未重新生成时 CI 直接失败。
   - 多语言：`summary`/`description` 写英文（OpenAPI 的通用做法），中文放在扩展字段 `x-i18n: { zh: { summary, description } }` 里，字段级说明同理。前端按当前语言优先读取 `x-i18n`，没有时回退到英文。错误码说明也按这个规则提供中英两份。
   - `/v1/messages` 本期只登记非流式调用的基础字段，不写 stream、tools、thinking。
3. **前端**：`OperationPage` 在构建期 `import spec from '../../../../docs/gateway-openapi.json'`，不在运行时请求。多语言代码示例由 `snippets.ts` 根据 operation 和 `example` 生成，避免三种语言各自手写后逐渐不一致。
4. `docs/API.md` 保留，改为"人读版概述"，并在文件开头注明接口细节以 `gateway-openapi.json` 为准。

### 4.4 真实的 Try it

| 项 | 方案 |
|---|---|
| 发请求 | 复用 `src/api/client.ts` 的 `resolveURL`；同源请求，不涉及 CORS |
| 凭证 | 优先使用 `useApiKey()`（`src/api/auth.ts`，也就是用户已经在 Playground 连接过的 Key）；没有 Key 时提供临时输入框，**只保存在组件 state 中**，不写 localStorage |
| 免鉴权接口 | `/v1/catalog` 无需 Key，直接可调，作为"第一次体验"的入口 |
| 计费接口 | chat/embeddings/messages 会产生真实扣费：按钮旁常驻提示"本次调用将从余额扣费"；默认请求体里带 `max_tokens: 64`；模型下拉框从 `/v1/models` 拉取 |
| 流式 | `stream: true` 时复用 `src/api/sse.ts` 的 `parseSSE`，逐 chunk 渲染 |
| 结果展示 | 状态码、耗时、`X-Request-Id`（一键复制）、响应头、响应体。错误时按 `src/api/errors.ts` 的 code 显示本地化说明，并链接到 `/docs/errors#<code>` |
| 复制为 curl | 生成的 curl 中 Key 一律以 `$UFREETOKENS_API_KEY` 代替 |
| 未实现接口 | 不显示 Try it，改为显示"尚未实现（503）"标记 |

### 4.5 Ask AI：本期下线

目前的 Ask AI 是关键词匹配出来的假回答，还会给出错误信息，对开发者是负面价值，本期直接删除。后续如果要做，建议实现为：用户用自己的 Key 走 `/v1/chat/completions`，把当前页面和 `llms-full.txt` 作为上下文。这项放到第 5 阶段之后单独评估。

### 4.6 搜索

- 构建期由 Vite 插件遍历 MDX 和 OpenAPI，**按语言各生成一份**：`search-index.zh.json` 和 `search-index.en.json`，内容包括 title、各级标题、正文纯文本和 URL 锚点。运行时只加载当前语言的索引。
- 运行时使用 `minisearch`（约 7KB gzip），并自定义 tokenizer：中文按单字和二元组（bigram）切分，英文按词切分，以支持中英文混合检索。
- 通过 `Cmd/Ctrl+K` 打开搜索对话框，结果按"页面 › 小节"分组，可用键盘导航。

### 4.7 面向开发者与 LLM 的增强

- **复制为 Markdown**：每页右上角提供按钮，复制该页 MDX 源文本，方便粘贴给 AI 编码助手。
- **`/llms.txt` 与 `/llms-full.txt`**：构建期从 MDX 和 OpenAPI 生成并放入 `dist/`，便于 Cursor、Claude Code 等工具抓取。以英文版为主，另外生成 `/llms-full.zh.txt`。
- **标题锚点**：鼠标悬停时显示 `#`，点击即复制带锚点的链接。
- **页面级 `<title>` 和 `<meta description>`**：取自 frontmatter。

### 4.8 视觉统一

- 侧栏改为与正文一致的浅色主题，复用主站 `Sidebar` 的 token。代码块保留深色背景（shiki 深色主题）。
- 布局为三栏：侧栏 `w-64` / 正文 `max-w-3xl` / TOC `w-56`。`xl` 以下隐藏 TOC，`lg` 以下侧栏改为抽屉。原先"导航 / 正文 / 调试终端"三个移动端切换按钮删除。
- 在 API 参考页面中，`xl` 及以上把 Try it 固定在右栏；更窄的屏幕上放到正文末尾。

### 4.9 中英文切换

主站目前没有 i18n 基础设施，而文档的界面文案不多（约 60 条），因此**不引入 react-i18next**，自己用一个类型化字典实现：`ui.ts` 导出 `const UI = { zh: {...}, en: {...} } satisfies Record<Locale, Record<UIKey, string>>`。缺少任何一个 key 都会在 `tsc` 阶段报错。

| 项 | 方案 |
|---|---|
| URL | `/docs/zh/quickstart`、`/docs/en/quickstart`；语言写在 URL 里，保证链接可分享，也利于 SEO |
| 默认语言 | 访问 `/docs` 或旧链接时按 URL > localStorage > `navigator.language`（`zh*` → zh，其余 → en）解析后重定向 |
| 切换入口 | 文档顶栏右侧提供 `中文 / English` 下拉；切换时保持当前的 slug 和 `#anchor`，并把选择写入 localStorage |
| 缺失译文 | 如果 `en/` 下没有对应文件，就回退显示中文正文，并在顶部显示 Callout："This page is not yet translated"。manifest 在侧栏中给这类条目加上 `ZH` 标记 |
| 译文过期 | frontmatter 增加 `translatedFrom: <中文文件内容 hash>`，`check-docs.mjs` 比对中文源文件的 hash，不一致时输出警告（不阻断构建） |
| API 参考 | 读取 OpenAPI 中的 `x-i18n`（见 4.3）；schema 字段名和代码不翻译 |
| `<html lang>` | 在 DocsLayout 中随语言设置 `document.documentElement.lang`，离开文档页时恢复 |
| 写作流程 | 中文为源语言，先写中文；英文由 LLM 初翻加人工校对，同一个 PR 提交或跟进 |

---

## 5. 一致性保障（CI）

| 检查 | 实现 |
|---|---|
| OpenAPI 与代码一致 | `go test ./internal/app -run TestGatewayOpenAPI_UpToDate` |
| MDX 里引用的接口必须存在 | `scripts/check-docs.mjs`：用正则提取 MDX 中的 `/v1/...` 路径，与 `gateway-openapi.json` 的 paths 比对，出现未知路径即失败 |
| 内部链接不失效 | 同一脚本：校验 `/docs/...#anchor` 形式的链接都能对应到 manifest 中的页面和锚点（中英文分别校验） |
| 中英文结构一致 | 同一脚本：`en/` 缺少的页面列为警告；`en/` 中出现 `zh/` 没有的 slug 则报错；译文 hash 过期列为警告 |
| 类型检查 | `npm run lint`（`tsc --noEmit`），覆盖 MDX 组件的 props |
| 冒烟（可选，本地） | `scripts/docs-smoke.sh`：按 `docs/本地联调与测试手册.md` 启动网关，提取快速开始中的 curl 示例执行，断言返回 200 |

`package.json` 新增脚本 `"docs:check": "node scripts/check-docs.mjs"`，并串入 `lint`。

---

## 6. 实施阶段

| 阶段 | 内容 | 交付物 | 预估 |
|---|---|---|---|
| **0 止血** | 在当前代码上先删除假内容：隐藏没有真实文章的导航项、删除 Try it/Ask AI/状态徽标/Changelog 等 mock、修正 Base URL 与币种、删除 ZDR 承诺卡片、下线 SDK/Agent/Cookbook 标签页 | 一个小 PR，页面上不再出现错误信息 | 0.5 天 |
| **1 骨架** | 加入 MDX 管线、`src/docs/` 目录结构、带语言前缀的 URL 路由、语言解析与切换、UI 文案字典、按需加载、`DocsLayout`/Sidebar/TOC/PrevNext、代码块组件（shiki + 复制 + Base URL 替换） | 用中英各 2 篇示例 MDX 跑通；旧组件全部删除 | 2.5 天 |
| **2 内容** | 编写第 3 节"入门"和"指南"下的 13 篇文章 + 2 篇集成文章（中文），每篇的示例都在本地网关上实测；然后翻译成英文并校对 | `content/zh/*.mdx`、`content/en/*.mdx` | 中文 2–3 天 + 英文 1.5 天 |
| **3 API 参考** | 后端 `GatewayOpenAPI()`（含 `x-i18n`）+ UpToDate 测试 + 错误码收敛（中英文说明）；前端 `OperationPage`/`SchemaView`/`snippets`/`TryIt` | `docs/gateway-openapi.json`，6 个 operation 页面，可进行真实调用 | 3.5 天 |
| **4 体验** | Cmd+K 搜索、复制为 Markdown、`llms.txt`、meta 标签、移动端抽屉、真实 Changelog | — | 1.5 天 |
| **5 护栏与文档同步** | `check-docs.mjs` 接入 CI；更新 `frontend/web/ARCHITECTURE.md`、`README.md`，以及 `docs/API.md` 开头的说明 | — | 0.5 天 |

合计约 12–13 人天，比原方案多出的约 2.5 天用于中英文支持。第 0 阶段可以立即单独上线；第 1 和第 3 阶段的后端部分可以并行推进。

---

## 7. 验收标准

- [ ] 文档中出现的每一个 `/v1/*` 路径都存在于 `gateway-openapi.json` 中（由 CI 校验）。
- [ ] 全站搜索 `$`、`USD`、`api/v1`、`claude-3.7`、`ZDR`，除解释性上下文外无命中。
- [ ] 新用户按快速开始操作，从注册到拿到第一次 chat 响应 ≤ 5 分钟（找 1–2 名未参与开发的同事实测）。
- [ ] 任何页面和标题刷新后都能回到原位置；浏览器前进、后退正常。
- [ ] Try it 对 `/v1/catalog` 无需 Key 即可返回 200；对 chat 用真实 Key 返回真实响应和 `X-Request-Id`；错误码能跳转到对应说明。
- [ ] 首屏（`/models`）JS 体积不包含文档代码（通过 `vite build` 的 chunk 报告核对）。
- [ ] 在任意页面切换语言后停留在同一篇文档的同一锚点；刷新后语言保持不变；`en/` 下没有缺失页面（或者缺失页面有明确的回退提示）。
- [ ] `npm run lint`、`go test ./internal/app/...` 通过。

---

## 8. 决策记录

| # | 事项 | 结论 |
|---|---|---|
| 1 | 生产环境对外域名 | 使用 ufreetokens 域名，构建期固定为 `https://ufreetokens.com`（`VITE_DOCS_PUBLIC_ORIGIN`）；示例代码中的 Base URL 仍在运行时按 origin 替换 |
| 2 | `/v1/messages` 的 stream/tools/thinking | 暂不需要，本期只写基础非流式调用 |
| 3 | 多语言 | 支持中英文切换，方案见 4.9 |
| 4 | SDK/Agent/Cookbook 标签页 | 暂未确认，按默认方案下线 |

---

## 9. 执行状态（2026-10-01）

第 0–5 阶段已全部落地：

- 后端：`internal/app/gateway_openapi.go` → `docs/gateway-openapi.json`；`TestGatewayOpenAPI_UpToDate`、`TestGatewayErrorCodes_Registered`（扫描 /v1 链路上的全部错误码，保证都已登记）。
- 前端：`src/docs/`（路由、布局、MDX 组件、API 参考、Try it、搜索、中英文切换），`tools/docsPlugin.ts`（manifest / 搜索索引 / OpenAPI 虚拟模块，llms.txt），`scripts/check-docs.ts`（已串进 `npm run lint`）。旧的 `DocsPage.tsx` 等 5 个文件已删除。
- 内容：17 篇 × 中英文 = 34 个 MDX 页面，示例已对照代码核实。
- 文档同步：`frontend/web/ARCHITECTURE.md` §10、`frontend/web/README.md`、`docs/API.md`。

编写文档时从代码中发现、需要后端跟进的问题（不在本次范围内）：

1. **计费漏洞（高优先级）**：客户端传入的合法 `X-Request-Id` 会被直接用作钱包预扣的幂等键（全局主键）。重复使用一个已结算的 ID，`Reserve` 会返回已有的预扣、`Settle` 直接返回，导致请求可能不扣费；不同账户使用相同 ID 也会互相冲突。建议把预扣键改为网关内部生成的 ID，或按账户加前缀。文档目前只要求"每个请求使用唯一的 ID"。
2. 流式响应不会向客户端转发 `data: [DONE]`，而是直接关闭连接；部分 SDK 依赖这个结束标记。
3. 结算金额不受预扣上限约束，现金余额可能变为负数。
4. `/v1/chat/completions` 不校验模型类型，embedding 模型也能被调用。
5. `/v1/models` 不按 Key 的 `allowed_models` 过滤。
6. 网站"连接 Key"弹窗写着"不会上传到任何服务器"，但 Key 会随每次调用发送到网关，这个提示需要改。
