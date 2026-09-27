# frontend/web × Go 后端（V2）集成：迭代执行方案
claude --resume d858d3ae-bf83-4b77-9048-f89cfb2da06d
## Context

`frontend/web` 目前是纯 mock 的 React SPA：没有任何 fetch，模型列表写死，Playground 用 setTimeout 伪造回复，个人中心的 Key、余额、日志都是假数据。后端 gateway 已经提供 `/v1/models`、`/v1/usage`、`/v1/chat/completions`（流式），但存在几个会直接阻塞集成的问题：

- 没有 CORS。
- 不注入 `stream_options.include_usage`，流式请求缺 usage 时按预扣上限（`fallbackUsage`）收费，用户中途断开也按上限扣。
- 没有面向终端用户的注册、登录和自助 Key 接口。
- 没有公开的模型目录和定价接口。
- 没有按时间区间的用量和日志查询。

目标：在**当前目录、当前分支 `feature/go-backend-scaffold`** 上原地迭代（不开 worktree，不调整现有目录结构），分 7 个迭代把 Phase 0–2 全部打通。每个迭代结束时都能跑通、测试通过、可以单独提交。

已确认的决策：
- 范围：Phase 0–2 全部。
- Console 接口挂在 **gateway 进程的 `/console/*`**。
- 会话方案：**httpOnly Cookie + Redis Session**。
- 跨域：**同源反代为主（dev 用 Vite proxy，prod 用 Nginx）**，另加一个可选的 CORS 白名单，只作用于 `/v1`。

---

## 迭代 0：准备与清理（半天）

- **test_web 迁移落地**：把根目录 `test_web/` 已删除、`frontend/test_web/` 未跟踪的状态整理好。同步修正以下几处里的 `UFT_TEST_WEB_DIR=test_web`，改为 `frontend/test_web`：
  - `README.md:60-66`
  - `frontend/test_web/README.md:10`
  - `scripts/dev-web-up.sh`
- **frontend/web 去掉 AI Studio 模板残留**：
  - `package.json`：改名为 `ufreetokens-web`；删除 `@google/genai`、`express`、`dotenv`、`tsx`、`@types/express`（src 里都没有引用）。
  - `.env.example` 改为 `VITE_GATEWAY_BASE_URL=`（留空表示同源）。
  - 重写 `README.md`，删除 `metadata.json`。
- **修复 `PlaygroundModal.tsx:27`**：它在 hooks 之前提前 `return null`，违反 hooks 规则。改成由 App 条件渲染，或者把 return 移到所有 hooks 之后。

## 迭代 1：后端 Phase 0 修复（计费正确性 + 跨域）

1. **注入 include_usage**
   - 在 `internal/relay/relay.go:184`（解析出 `stream` 之后）记录 `clientWantsUsage := reqMap["stream_options"].include_usage == true`。
   - 在 `internal/adapter/openai.go` 的 `BuildRequest`（68-96 行）里，**在 ParamOverrides 循环之前**，对 stream 请求合并写入 `stream_options.include_usage=true`。不支持该参数的渠道可以用 `param_overrides: {"stream_options": null}` 剔除。anthropic 和 gemini 适配器不受影响。
2. **吞掉 usage-only chunk**：在 `relay.go` 的流式循环（509-518 行）中，如果 `!clientWantsUsage`，且当前 chunk 是 `choices` 为空、只带 `usage` 的 chunk，就不转发给客户端。判断逻辑新增 helper `isUsageOnlyChunk([]byte)`，放在 `relay/helpers.go`。对应 V2 §7.4 的要求：不改变客户端看到的协议行为。
3. **客户端断开时按已输出内容估算**：
   - 流式循环里累计已转发 delta 的字节数。
   - 当 `dec.Usage().IsZero()` 时，不再用 `reserveOutput`，改为 `min(estimateTokens(forwardedBytes), reserveOutput)`。`usage_source` 仍记为 `estimated`。
   - 非流式路径保持原有的保守兜底不变。
   - 同步更新 `fallbackUsage` 的注释。
4. **CORS**：
   - 新增中间件 `httpx.CORS(origins []string)`，放在 `internal/httpx/middleware.go`。
   - 只挂在 `/v1` 路由组上，并且放在 `auth.APIKey` 之前，这样预检 OPTIONS 不会被 401。
   - 允许的 Header：`Authorization, Content-Type, X-Request-Id`。暴露的 Header：`X-Request-Id, Retry-After`。
   - 不开 credentials。
   - 配置项 `gateway.cors_origins`（环境变量 `UFT_GATEWAY_CORS_ORIGINS`，逗号分隔）；为空则不启用。
   - `/console` **不开 CORS**（Cookie 会话只允许同源）。
5. **测试**：
   - relay 单测覆盖三种情况：注入字段后上游返回 usage 时按真实用量计费；客户端没要 usage 时不转发 usage chunk；客户端断开时按已输出内容估算。
   - 复用 `internal/app/usage_test.go` 里的假上游 `httptest.Server` 模式。
   - `httpx/middleware_test.go` 增加 CORS 用例。

## 迭代 2：前端 Phase 0（BYOK 模式接入真实数据）

新增 `frontend/web/src/api/`，这是全项目唯一发起网络请求的地方：

| 文件 | 职责 |
|---|---|
| `client.ts` | `request(path, opts)`：baseURL 取 `import.meta.env.VITE_GATEWAY_BASE_URL ?? ''`；注入 Bearer；超时控制；`credentials: 'same-origin'` |
| `errors.ts` | `ApiError.fromResponse`：解析 `{error:{message,type,code,request_id}}`，再把 code 映射成中文提示（`insufficient_balance` → "余额不足，请充值"，等等） |
| `auth.ts` | `authStore`：用 localStorage 保存 `uft.apiKey`，提供 get/set/clear/subscribe。为迭代 4 预留 `mode: 'byok' \| 'session'` |
| `sse.ts` | 基于 ReadableStream 的 SSE 解析器：按 `\n\n` 切分事件，不假设单行长度上限，处理 `[DONE]` |
| `chat.ts` | `streamChat({model, messages, temperature, max_tokens, signal}, onDelta, onUsage)`：始终带上 `stream_options.include_usage=true` |
| `models.ts` | `listModels()` 调用 `GET /v1/models` |
| `usage.ts` | `getUsage()`，以及 `microToDisplay(micro, 'CNY')`（除以 1e6，保留 4 位小数） |

UI 改造（尽量不动现有组件结构）：
- **Header 用户菜单**：新增"连接 API Key"弹窗 `ConnectKeyModal.tsx`，复用 PersonalDashboard 里的弹窗样式。弹窗内提示"仅保存在本机浏览器"，并提供"断开并清除"按钮。
- **App.tsx 模型列表**：已连接时，把 `/v1/models` 返回的 id 和 `INITIAL_MODELS` 合并。能匹配上的模型标记"可调用"；真实存在但 mock 里没有的模型，生成一张只含基本字段的卡片；价格和评分仍用 mock，并加"演示数据"角标。
- **PlaygroundModal**：`handleSend`（48-98 行）改为调用 `streamChat`，逐块把 delta 拼进最后一条 assistant 消息。每次发送前 new 一个 AbortController；关闭弹窗或清空对话时 abort。回复末尾显示 usage（token 数）。未连接 Key 时，引导用户打开 ConnectKeyModal。
- **PersonalDashboard 的 credits tab**（638 行起）：把硬编码的 `$24.50` 换成 `/v1/usage` 返回的 `cash/bonus/frozen` 和累计消费。货币符号统一改为 ¥（V2 规定售价为 CNY）。
- **Vite proxy**：`vite.config.ts` 增加 `server.proxy`，把 `/v1` 和 `/console` 转发到 `http://localhost:8080`。

## 迭代 3：后端 Phase 1（Console：注册、登录、自助 Key）

- **依赖**：go.mod 增加 `golang.org/x/crypto`，使用 argon2id。
- **新包 `internal/console`**：

| 文件 | 内容 |
|---|---|
| `password.go` | argon2id 以 PHC 字符串格式编码（m=64MiB, t=2, p=2）；常数时间校验；未知邮箱时用 dummy hash 走一遍校验，抹平响应时间差 |
| `session.go` | Redis 存 `uft:sess:<sha256(token)>` → `{user_id, account_id, created_at}`，TTL 7 天、滑动续期；另建集合 `uft:user_sess:<uid>`，支持"全部下线"。Cookie 名 `uft_session`，HttpOnly、SameSite=Lax、Path=/console；是否加 Secure 由配置 `console.cookie_secure` 决定 |
| `service.go` | `Register(email, password)`：**在同一个事务里**创建 account（personal/free）+ wallet + user + account_member(owner)。其余方法：`Login`、`Logout`、`Me`、`ListKeys`、`CreateKey`、`RevokeKey`（按 account 限定范围）、`Wallet` |
| `middleware.go` | `RequireSession`：从 Cookie 解析出会话，放进 context。`CSRFGuard`：对非 GET 请求，要求 `Origin` 或 `Sec-Fetch-Site` 为同源，并且带 `X-UFT-CSRF: 1` 头 |
| `handlers.go` | HTTP 入口；错误统一走 `httpx.WriteError` |

- **复用和小幅重构 `internal/admin`**：
  - 从 `CreateAccount`（`accounts.go:39`）抽出 `createAccountTx(ctx, tx, in)`，让 console 的注册在同一事务里完成建账户和建钱包。
  - `CreateAPIKeyInput` 增加 `CreatedBy *int64`（写入 `api_keys.created_by`）。
  - 新增 `RevokeAPIKeyForAccount(ctx, accountID, keyID)`，带 `WHERE account_id=$1`，防止越权吊销别的账户的 Key。现有的 `RevokeAPIKey` 本身不按账户限定范围。
  - Key 的生成和 HMAC 直接复用 `auth.GenerateAPIKey`（`internal/auth/apikey.go:40`）。吊销立即生效，因为 `auth.PostgresStore` 每次请求都查库、没有缓存。
- **路由**（挂在 `internal/app/gateway.go` 的 `r.Route("/console")` 下；`GatewayDeps` 增加 `Console *console.Service`，为 nil 时不挂载）：
  - 无需登录：`POST /console/register`、`POST /console/login`、`POST /console/logout`。
  - 需要会话：`GET /console/me`、`GET /console/api-keys`、`POST /console/api-keys`（明文 Key 只在创建响应里返回这一次）、`POST /console/api-keys/{id}/revoke`、`GET /console/wallet`。
  - 审计：Key 的创建和吊销调用 `admin.Service.RecordAudit`，actor 记为 user_id。
- **限流**：
  - `internal/ratelimit` 新增 `AllowRPMStrict`：Redis 出错时 **fail-closed**（现有的 `AllowRPM` 是 fail-open，不适合防暴力破解）。
  - 登录限流两个维度：按 IP 10 次/分钟，按邮箱 5 次/分钟。
  - 注册限流：按 IP 3 次/分钟。
- **暂不实现：邮箱验证和注册赠送**。没有邮件基础设施，所以注册赠送的余额（V2 A7 要求验证通过后才发放）不在本迭代发。`users.email_verified` 保持 false，在文档中列为 TODO。
- **测试**：新建 `internal/app/console_e2e_test.go`，沿用 `admin_gateway_e2e_test.go` 的 `testPool`/`testRedis` 模式，覆盖：
  - 注册 → 登录 → 建 Key → 用这把 Key 调 `/v1/models` 成功 → 吊销后返回 401；
  - A 账户吊销 B 账户的 Key 返回 404；
  - 没带 CSRF 头返回 403；
  - 登录限流返回 429；
  - 密码错误和邮箱不存在返回完全相同的响应。
  - 另外 `console/password_test.go` 做单测。

## 迭代 4：前端 Phase 1

- `src/api/console.ts`：`register/login/logout/me/listKeys/createKey/revokeKey/getWallet`，所有写请求都带上 `X-UFT-CSRF: 1`。
- `authStore` 扩展为双模式：session 表示控制台登录态，只存在 Cookie 里，前端只缓存 `me`；byok 表示 `/v1` 调用所用的 Key。
- 新增 `LoginModal.tsx` 和 `RegisterModal.tsx`。Header 按 `me` 显示登录/退出，替换掉硬编码的 `userEmail` 和只弹 toast 的"退出登录"。
- PersonalDashboard 的 **api-keys tab**（79-157 行的 state 和 756-880 行的弹窗）接入 console API：
  - `ApiKeyItem` 映射：`maskedKey` 取 `display_prefix + '…'`；`fullKey` 只在创建后展示一次。
  - guardrails/limit 这类后端没有的字段，要么隐藏，要么映射到 `rpm_limit`。
  - 创建成功的弹窗里增加一个勾选项"在本浏览器用于 Playground"，勾选后把 Key 写进 byok store。这样 `/v1` 调用始终走 API Key，符合 V2"控制台鉴权与 API Key 鉴权完全分离"的原则。
- credits tab 在已登录时改用 `/console/wallet`（不需要 API Key）。

## 迭代 5：后端 Phase 2（公开目录 + 区间用量 + 调用日志）

1. **公开目录 `GET /v1/catalog`（免鉴权）**：
   - 在 `gateway.go` 里把 `/v1` 拆成两部分：`v1.Get("/catalog")` 不挂中间件；原有接口放进 `v1.Group` 挂 `auth.APIKey`。
   - 数据来自 `catalog.Store` 快照（`internal/catalog/catalog.go:140`，`GatewayDeps` 需要新增 `Catalog`）。只返回 `status=active` 且 `free` tier 可见的模型，字段包括：`name, family, type, context_window, max_output, capabilities`，以及售价 `sell_price{currency, components[{meter, unit, unit_price}]}`（取自 `SellPriceBooks`）。
   - 加 `Cache-Control: public, max-age=60`，并按 IP 限流。
   - 如果需要登录后按用户 tier 显示，再扩展 `?tier=`（依赖会话）。本迭代不做。
2. **运营元数据**：
   - 新迁移 `migrations/00014_virtual_model_metadata.sql`，表 `virtual_model_metadata(virtual_model_id PK, display_name, description, provider_display, tags TEXT[], scores JSONB, updated_at)`。
   - admin 新增 `PUT /virtual-models/{id}/metadata`（写审计）。
   - catalog 查询时 LEFT JOIN 这张表。评分由运营录入，后端不自动生成。
3. **区间用量 `GET /console/usage?from=&to=&group_by=day|model`**：
   - 在 `request_logs` 上按 `created_at` 区间聚合（能触发分区裁剪）。区间最长 90 天，默认本月。
   - 返回每个分组的 `requests, input_tokens, output_tokens, charged_amount_micro`。
4. **调用日志 `GET /console/logs?before=&limit=&api_key_id=`**：
   - 用 `(created_at, request_id)` 做 keyset 分页，倒序，每页最多 100 条，时间窗最长 30 天。
   - 只返回元数据：模型、状态、tokens、费用、延迟、`usage_source`，不含任何正文。
5. **索引与旧接口**：
   - 检查 `request_logs` 是否已有 `(account_id, created_at DESC)` 索引；没有就在 00014 迁移里一并加上（分区表需要在父表上建）。
   - `/v1/usage` 增加可选的 `?since=`，默认值仍保持全量，兼容 test_web。
6. **测试**：`console_e2e_test.go` 扩展用例，覆盖区间边界、分页游标，以及日志不能跨账户查看；另加 catalog handler 测试。

## 迭代 6：前端 Phase 2

- 模型库改为以 `/v1/catalog` 作为主数据源，匿名访客也能看到。
  - 映射到 `Model` 类型的字段：`contextTokens`、`maxOutputTokens`、`input/outputPricePerM`（取 meter=input/output、unit=per_1m_tokens 的价格）、`supportedParameters`（来自 capabilities）。
  - `scores`、`description`、`badge` 优先取 metadata；缺失时回退到 `data/models.ts` 里按 id 对应的覆盖表。
  - 以后 `INITIAL_MODELS` 只作为离线兜底和评分覆盖表使用。
- credits tab：用 `/console/usage` 显示"本月已消费"和按天的柱状图（遵循现有 Tailwind 风格）。
- activity/logs tab（695 行起）：接 `/console/logs`，用无限滚动加载。
- 保持占位、不接后端：guardrails/byok/routing/presets 等 tab 后端没有对应的领域模型，工作区同理。

## 迭代 7：文档同步

- **`frontend/web/ARCHITECTURE.md`**：
  - 修正 §5.4：include_usage 由后端注入；断开连接时按已输出内容估算。
  - 修正 §9.2：CORS 已实现。
  - §3 改为 console 挂在 gateway、使用 Cookie Session。
  - §4 写明 `/v1/catalog` 的实际结构。
  - 更新 §6 的可行性表和 §8 的阶段表。
- **V2 文档**：
  - §7.1 路由补上 `/v1/usage`、`/v1/messages`、`/v1/catalog`、`/console/*`。
  - §7.16.10 改为"对外价格走 `/v1/catalog`，`/v1/models` 保持 OpenAI 格式"。
  - §5 的模块目录标注实际存在的包。
  - §7.9.4 写明当前实现的是字节估算，tokenizer 待做。
- **新增 `docs/API.md`**：记录当前实际可用的接口，包括路径、鉴权方式、请求/响应示例和实现状态，作为前端的唯一接口依据。
- 新增 `deploy/nginx/web.conf` 示例：静态文件托管 dist；`/v1` 和 `/console` 反代到 gateway，并设置 `proxy_buffering off`、`proxy_read_timeout 600s`。

---

## 关键文件

- 后端：
  - 修改：`internal/relay/relay.go`、`internal/relay/helpers.go`、`internal/adapter/openai.go`、`internal/httpx/middleware.go`、`internal/app/gateway.go`、`internal/admin/{accounts,apikeys}.go`、`internal/ratelimit/ratelimit.go`、`internal/config/*`、`cmd/gateway/main.go`
  - 新增：`internal/console/*`、`migrations/00014_*.sql`
- 前端：
  - 新增：`frontend/web/src/api/*`、`ConnectKeyModal.tsx`、`LoginModal.tsx`、`RegisterModal.tsx`
  - 修改：`App.tsx`、`Header.tsx`、`PlaygroundModal.tsx`、`PersonalDashboardPage.tsx`、`vite.config.ts`、`package.json`、`.env.example`

## 验证

每个迭代都执行：
1. 后端：`make deps-up && make migrate-up`，然后 `go test ./...`（需要本地 PG 和 Redis；如果 e2e 因为连不上被 skip，必须如实说明）。
2. 前端：在 `frontend/web` 下执行 `npm run lint`（tsc）和 `npm run build`。
3. 联调：
   - 用 `scripts/dev-web-up.sh` 启动 gateway 和 admin，用 test_web 的 admin 页面配好一个上游渠道和售价。
   - `frontend/web` 执行 `npm run dev`，走完整链路：连接 Key 或注册登录 → 建 Key → 模型库显示真实目录 → Playground 流式对话，中途关闭弹窗 → 查看余额、本月用量和日志。
   - 用 `psql` 核对对应 `request_logs` 行的 `usage_source`（正常结束应为 `upstream`，中途断开应为 `estimated`），以及 `charged_amount` 是否合理。
4. 迭代 1 额外检查：curl 发一个不带 `stream_options` 的流式请求，确认返回的 SSE 里没有 usage-only chunk，同时 `request_logs.usage_source=upstream`。

提交方式：每个迭代完成后，经用户确认再提交一次，提交信息注明对应的迭代编号。
