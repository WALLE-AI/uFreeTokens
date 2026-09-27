# uFreeTokens 前端（frontend/web）× Go 后端 集成技术架构

> 本文档最初是"接入前的差距分析"，Phase 0-2 已经全部实现（见
> `docs/frontend-web 与 Go 后端集成迭代执行方案.md` 的迭代 0-6）。现在改为
> "当前实际接入方式"的说明文档；仍然保留原有章节编号，方便和执行方案互相
> 对照。
>
> 内部运营后台（`frontend/admin`）的技术栈/UI 风格与鉴权设计见
> [`frontend/admin/ARCHITECTURE.md`](../admin/ARCHITECTURE.md)；
> `frontend/test_web` 仅为手工联调页面，不作为任何一方的 UI/工程参考。

---

## 0. 现状

| 层 | 现状 |
|---|---|
| `frontend/web` | React SPA，`src/api/` 是全项目唯一发起网络请求的地方（`client.ts`/`errors.ts`/`auth.ts`/`sse.ts`/`chat.ts`/`models.ts`/`usage.ts`/`catalog.ts`/`console.ts`）。模型库、Playground、个人中心的 API 密钥/余额/用量/调用日志都接了真实数据；`data/models.ts` 的 `INITIAL_MODELS` 现在只作为 `GET /v1/catalog` 不可用时的离线兜底，以及给运营还没录入元数据的模型补展示层字段（描述、系列、图标底色……）。 |
| `frontend/test_web` | 手工联调页面（`admin.html` + `user.html`），继续作为验证"后端本身是否工作正常"的独立参考，和 `frontend/web` 互不依赖。 |
| `cmd/gateway`（:8080） | 数据面 `/v1/*` + 控制台 `/console/*`（技术方案迭代 3 起，两者共用同一个进程，鉴权完全独立：`/v1/*` 用 `Authorization: Bearer sk-uft-...`，`/console/*` 用 httpOnly Cookie Session）。 |
| `cmd/admin`（:8081） | 控制面，鉴权是单一共享密钥 `UFT_ADMIN_TOKEN`，供应商/渠道/定价/审计/虚拟模型元数据在这里维护，继续没有按用户区分的登录态——这是运营内部工具，`frontend/web` 从不直连它。 |
| 用户注册/登录 | **已实现**（`internal/console`）：argon2id 密码哈希 + Redis Session（httpOnly Cookie），挂在 `cmd/gateway` 的 `/console/*`，不是独立进程，也不是 JWT。 |

---

## 1. 架构总览

```
┌──────────────────────────────────────────────────────────────────────┐
│  frontend/web  (React SPA, 静态托管 / CDN)                            │
│                                                                        │
│   Presentation：ModelCard/Table、PlaygroundModal、                     │
│                 PersonalDashboardPage、ConnectKeyModal、               │
│                 LoginModal/RegisterModal …                             │
│            │                                                          │
│            ▼                                                          │
│   src/api/（唯一允许发网络请求的地方）                                 │
│     ├─ client.ts   request()：baseURL 留空=同源，超时，错误解析        │
│     ├─ errors.ts   ApiError.fromResponse，code → 中文提示映射          │
│     ├─ auth.ts     authStore（BYOK Key，localStorage）+               │
│     │               consoleAuthStore（缓存 GET /console/me 的结果）    │
│     ├─ sse.ts       ReadableStream 版 SSE 解析器                       │
│     ├─ chat.ts      POST /v1/chat/completions（流式）                 │
│     ├─ models.ts    GET /v1/models（需要 API Key）                    │
│     ├─ catalog.ts   GET /v1/catalog（免鉴权公开目录）                 │
│     ├─ usage.ts      GET /v1/usage（需要 API Key）                     │
│     └─ console.ts   /console/*（注册/登录/Key/钱包/区间用量/日志）    │
└───────────────┬──────────────────────────────────┬───────────────────┘
                │ dev: Vite proxy 同源转发            │ prod: Nginx 反代同源
                ▼                                    ▼
                    ┌───────────────────────────┐
                    │      cmd/gateway :8080     │
                    │  /v1/*     鉴权=API Key     │
                    │  /console/* 鉴权=Cookie      │
                    │             Session          │
                    │  （两套鉴权完全独立）          │
                    └───────────────┬───────────┘
                                    │ 需要管理员密钥的运营操作
                                    ▼
                    ┌───────────────────────────┐
                    │      cmd/admin :8081        │
                    │  供应商/渠道/定价/审计/       │
                    │  虚拟模型元数据（共享密钥）    │
                    └───────────────────────────┘
```

关键原则不变：**前端 bundle 永远不持有 `UFT_ADMIN_TOKEN`**，也不直连 `cmd/admin`。

---

## 2. Gateway 数据面接入（Phase 0，已实现）

| 前端场景 | 接口 | 鉴权 | 说明 |
|---|---|---|---|
| 模型库（已连接 Key 时标记"可调用"） | `GET /v1/models` | `Bearer <api-key>` | OpenAI 风格 `{object:"list", data:[{id, object, created, owned_by}]}` |
| 模型库主数据源 | `GET /v1/catalog` | 免鉴权 | 见 §4 |
| 个人中心 → 余额与账单 | `GET /v1/usage`（`?since=` 可选） | `Bearer <api-key>` | `{wallet:{cash_balance_micro, bonus_balance_micro, frozen_micro}, usage:{total_requests, total_input_tokens, total_output_tokens, total_charged_amount_micro}}` |
| Playground 对话 | `POST /v1/chat/completions`（`stream:true`） | `Bearer <api-key>` | SSE 流式，`fetch` + `ReadableStream` 手动解析（`src/api/sse.ts`），不用浏览器原生 `EventSource` |

金额单位：所有金额是 int64 micro 单位（1,000,000 = 1 元），`src/api/usage.ts` 的 `microToDisplay` 统一转换。

错误处理：`{"error":{"message","type","code","request_id"}}`，`src/api/errors.ts` 的 `ApiError.fromResponse` 统一解析，把已知 code（`insufficient_balance` 等）映射成中文提示。

跨域：dev 用 `vite.config.ts` 的 `server.proxy` 把 `/v1`、`/console` 转发到本地 gateway；prod 用 Nginx 反代同源（见 `deploy/nginx/web.conf`）。两者都是同源，正常情况下不需要 CORS；`internal/httpx.CORS` 只在 `gateway.cors_origins` 显式配置时才启用，供前后端分开部署、无法同源反代的场景使用，见 §9。

---

## 3. Console：自助注册登录与 API Key 管理（Phase 1，已实现）

`internal/console` 挂在 `cmd/gateway` 的 `/console/*`（**不是**独立的 `cmd/console` 进程，也不是原计划的 JWT）：

- `POST /console/register`：argon2id（m=64MiB, t=2, p=2）哈希密码，同一个数据库事务里创建 account（personal/free）+ wallet + user + owner 身份的 account_member。不自动登录，也不做邮箱验证（`users.email_verified` 保持 false——没有邮件基础设施，宁可不发注册赠送余额也不在未验证的前提下发钱）。
- `POST /console/login`：校验通过后签发会话——token 存 Redis（`uft:sess:<sha256(token)>`），TTL 7 天滑动续期，Cookie 名 `uft_session`，HttpOnly + SameSite=Lax，`console.cookie_secure` 配置项控制要不要加 Secure（本地 http 开发环境必须关）。
- `POST /console/logout`：删会话、清 Cookie。
- `GET /console/me`：需要会话，返回 `{user_id, email, email_verified, account_id, account_tier}`。
- `GET /console/api-keys`、`POST /console/api-keys`（明文只在创建响应里出现一次）、`POST /console/api-keys/{id}/revoke`：需要会话；写请求还需要 `X-UFT-CSRF: 1` 头（见下）。按 `account_id` 限定范围，A 账户无法吊销 B 账户的 Key（返回 404，不区分"不存在"和"越权"）。
- `GET /console/wallet`：需要会话，不需要 API Key——控制台鉴权与 API Key 鉴权完全分离，这是原始设计里就确认的决策。
- `GET /console/usage?from=&to=&group_by=day|model`、`GET /console/logs?before=&limit=&api_key_id=`：见 §4/§6。

**CSRF 防护**（`internal/console.CSRFGuard`）：非 GET/HEAD/OPTIONS 请求必须同时满足"带自定义头 `X-UFT-CSRF: 1`"和"Origin/Referer 与 Host 同源"，两个条件都不依赖 CORS 预检——`/console/*` 完全不开 CORS，跨站 fetch 想加这个自定义头会先触发预检，而预检必然因为没有 CORS 配置而失败。

**登录/注册限流**：`internal/ratelimit.AllowRPMStrict`（fail-closed，专为防暴力破解设计）。登录按 IP 10 次/分钟 + 按邮箱 5 次/分钟，注册按 IP 3 次/分钟。密码错误和邮箱不存在返回完全相同的响应（内容和近似耗时），避免用户名枚举。

`src/api/auth.ts` 因此维护两套完全独立的状态：`authStore`（BYOK 模式的 API Key，localStorage）和 `consoleAuthStore`（登录态，纯内存缓存 `GET /console/me` 的结果，页面刷新后自动重新探测）。一个用户可以只连 Key 不登录控制台，也可以登录了控制台但还没在这台设备连 Key。

---

## 4. 模型目录（Phase 2，已实现）

`GET /v1/catalog`（免鉴权，`internal/app/catalog.go`）：只返回 `status=active` 且对 `free` tier 可见的虚拟模型，按 IP 限流（60 次/分钟）+ `Cache-Control: public, max-age=60`。响应形状：

```json
{
  "object": "list",
  "data": [
    {
      "name": "deepseek-ai/DeepSeek-V4-Flash",
      "family": "deepseek",
      "type": "chat",
      "context_window": 128000,
      "max_output": 8192,
      "capabilities": ["stream", "tools"],
      "sell_price": {
        "currency": "CNY",
        "components": [
          {"meter": "input", "unit": "per_1m_tokens", "unit_price": "1.5"},
          {"meter": "output", "unit": "per_1m_tokens", "unit_price": "3"}
        ]
      },
      "display_name": "DeepSeek V4 Flash",
      "description": "运营录入的介绍文案",
      "provider_display": "DeepSeek",
      "tags": ["reasoning", "coding"],
      "scores": {"intelligenceIndex": 39.5, "codingIndex": 82, "agenticIndex": 68}
    }
  ]
}
```

`display_name`/`description`/`provider_display`/`tags`/`scores` 来自运营在 `virtual_model_metadata` 表（迁移 00014）录入的展示层数据，通过 `cmd/admin` 的 `PUT /virtual-models/{id}/metadata` 维护；没录入时这几个字段直接缺失（不是空字符串）。`scores` 是自由格式 JSON，后端不解析其内部结构，前端约定用 `intelligenceIndex`/`codingIndex`/`agenticIndex` 三个键（`src/data/models.ts` 的 `normalizeScores`），运营录入时需要遵循这个约定。

`src/data/models.ts` 的 `modelFromCatalog(cm, mockOverride)` 把一条 catalog 数据转成前端 `Model` 类型：硬性字段（上下文窗口、价格、能力）永远来自 catalog，展示层字段缺失时按 `id` 从 `INITIAL_MODELS` 找同名条目回退。`GET /v1/models`（需要 API Key）额外用来标记"这把 Key 具体能调用哪些模型"（`isCallable`），和"模型是否公开存在于目录"是两个维度——`/v1/catalog` 只对 free tier 可见，已连接的 Key 可能是更高 tier，能调用目录里看不到的模型，这种情况下 `synthesizeCallableModel` 会生成一张打了"演示数据"角标的最小卡片。

---

## 5. Playground 流式对话（已实现）

`src/components/PlaygroundModal.tsx` 调用 `src/api/chat.ts` 的 `streamChat`：

1. 请求体用设置面板的 `temperature`/`maxTokens`/`systemPrompt` 组出 OpenAI 格式 `messages`，`stream: true`。
2. `fetch` 拿到 `response.body`，`src/api/sse.ts` 的 `parseSSE` 按 `\n\n` 切分事件，不假设单行长度上限（和后端 `bufio.Reader` 而非 `Scanner` 的假设一致）。
3. 每个 chunk 的增量拼进当前 assistant message；回复末尾展示 usage（token 数）。
4. **计费透明度（已确认，不再是待定项）**：网关（`adapter/openai.go` 的 `BuildRequest`）对所有流式请求无条件向上游注入 `stream_options.include_usage=true`，不需要前端自己声明就能保证按真实 usage 计费；`streamChat` 仍然主动带上这个参数，是为了让客户端自己也能看到那个 usage chunk（网关只有在客户端自己请求了 `include_usage` 时才会转发这个 chunk，见 `internal/relay.isUsageOnlyChunk`——不请求就看不到，协议行为和不使用这个参数完全一致）。
5. **断开连接的计费方式（已确认）**：用户主动关闭弹窗或清空对话会 `AbortController.abort()`。网关这一侧：流式请求在客户端断开、上游还没来得及吐出 usage chunk 时，不再按预扣上限 `reserveOutput` 计费（那会让"看了两个字就断开"和"跑满整个 max_tokens"付一样的钱），而是按已经转发给客户端的内容字节数估算（仍不超过 `reserveOutput`），`request_logs.usage_source` 记为 `estimated`。

---

## 6. 个人中心各 Tab 的数据来源

| Tab | 状态 | 说明 |
|---|---|---|
| API 密钥 | **已实现** | `/console/api-keys`，需要登录控制台（不是连 API Key），见 §3 |
| 余额与账单（总额度/累计消费/冻结中金额） | **已实现** | 连了 Key 用 `GET /v1/usage`（有完整用量统计）；只登录未连 Key 用 `GET /console/wallet`（只有余额，没有用量统计） |
| 余额与账单（本月每日消费趋势） | **已实现** | `GET /console/usage?group_by=day`，登录控制台即可看，不需要连 API Key |
| 调用日志 / 活动记录 | **已实现** | `GET /console/logs`，`(created_at, request_id)` keyset 分页，`IntersectionObserver` 触发无限滚动，时间窗最长 30 天，只有元数据（模型/状态/tokens/费用/延迟/usage_source），不含请求/响应正文 |
| 安全护栏 / BYOK / 模型路由 / 系统预设 / 分类器 | **不接** | 后端没有对应领域模型，纯前端占位功能，保留静态展示 |
| 工作区（多工作区切换） | **不接** | 后端账户模型是 `account`，没有"工作区"这一级概念 |

---

## 7. 客户端服务层（`src/api/`，已实现）

实际文件（不再是设计草图）：

| 文件 | 职责 |
|---|---|
| `client.ts` | `request(path, opts)`：baseURL 留空表示同源；`headers` 选项供 `console.ts` 的写请求带 `X-UFT-CSRF: 1`；不设置全局超时之外的重试逻辑 |
| `errors.ts` | `ApiError.fromResponse`，code → 中文提示映射表 |
| `auth.ts` | `authStore`（BYOK Key，localStorage）+ `consoleAuthStore`/`useConsoleUser`（登录态，纯内存） |
| `sse.ts` | `parseSSE`：`ReadableStream<Uint8Array>` → 逐条 `data:` 负载字符串 |
| `chat.ts` | `streamChat`：POST `/v1/chat/completions`，始终带 `stream_options.include_usage=true` |
| `models.ts` | `listModels(apiKey)`：GET `/v1/models` |
| `catalog.ts` | `listCatalog()`：GET `/v1/catalog`（免鉴权） |
| `usage.ts` | `getUsage(apiKey)` + `microToDisplay` |
| `console.ts` | `register/login/logout/getMe/listKeys/createKey/revokeKey/getWallet/getUsageInterval/getLogs` |

环境变量：`frontend/web/.env.example` 只有 `VITE_GATEWAY_BASE_URL`（留空=同源）；**没有、也不应该有**任何指向 `:8081`（admin）的前端可读 env。

---

## 8. 分阶段落地计划（Phase 0-2 已完成）

| 阶段 | 内容 | 状态 |
|---|---|---|
| **Phase 0** | `GET /v1/models`、`GET /v1/usage`、`POST /v1/chat/completions`（Playground 真实对话）；BYOK 鉴权 | ✅ 已完成（迭代 0-2） |
| **Phase 1** | Console 注册/登录 + 按账户自助 API Key CRUD；前端接通个人中心的注册/登录、API 密钥自助创建/吊销 | ✅ 已完成（迭代 3-4） |
| **Phase 2** | `GET /v1/catalog` 公开目录 + 运营元数据；区间用量聚合 `/console/usage`；调用日志 `/console/logs`；前端模型库改用真实数据、credits tab 消费趋势图、activity/logs tab 无限滚动 | ✅ 已完成（迭代 5-6） |
| **Phase 3** | 充值/支付对接，接通"余额与账单"的充值入口 | ⏳ 未开始，依赖后端支付网关落地 |

---

## 9. 安全注意事项

1. `UFT_ADMIN_TOKEN` 任何时候都不能出现在 `frontend/web` 的构建产物、网络请求或 localStorage 里；`frontend/web` 从不直连 `cmd/admin`。
2. **CORS 已实现**（`internal/httpx.CORS`）：只挂在 `/v1`，且在 `auth.APIKey` 之前（预检 OPTIONS 不带 Authorization，先过 CORS 中间件应答，不会被鉴权中间件拦成 401）。默认不开启（`gateway.cors_origins` 为空），dev/prod 都走同源反代，不需要它；只有前后端分开部署、无法同源反代时才需要显式配置。不开 credentials，`/console/*` 完全不挂这个中间件——Cookie 会话只信任同源请求。
3. BYOK 模式下 API Key 明文存 `localStorage` 是已知取舍，`ConnectKeyModal` 已经在 UI 上提示"仅保存在本机浏览器"，并提供"断开并清除"入口。
4. Console 会话是 httpOnly Cookie（前端 JS 拿不到 token 本身），`consoleAuthStore` 只缓存 `GET /console/me` 的返回值；写操作的 CSRF 防护见 §3。
