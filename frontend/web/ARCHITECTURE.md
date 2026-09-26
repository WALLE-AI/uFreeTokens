# uFreeTokens 前端（frontend/web）× Go 后端 集成技术架构

> 目的：把 `frontend/web`（React 19 + Vite + Tailwind 的"模型集市"UI，目前全部数据都是前端硬编码 mock）接到 `cmd/gateway` / `cmd/admin` 两个真实 Go 服务上。本文只覆盖**架构设计**，不改代码。
>
> 内部运营后台（`frontend/admin`）的技术栈/UI 风格与鉴权设计见 [`frontend/admin/ARCHITECTURE.md`](../admin/ARCHITECTURE.md)，两份文档配套阅读；`frontend/test_web` 仅为手工联调页面，不作为任何一方的 UI/工程参考。

---

## 0. 现状评估

| 层 | 现状 |
|---|---|
| `frontend/web` | 纯前端 SPA，**没有任何 `fetch` 调用**。`data/models.ts` 是写死的模型数组；`PlaygroundModal.tsx` 用 `setTimeout` 拼字符串伪造"AI 回复"；`PersonalDashboardPage.tsx` 的 API Key / 余额 / 调用日志全部是 `useState` 里的假数据。 |
| `frontend/test_web` | 已经是**真实联调页面**（`admin.html` + `user.html`），用原生 `fetch` 打真实的 `cmd/admin` / `cmd/gateway`，是本次接入最可靠的参考实现。 |
| `cmd/gateway`（:8080） | 数据面，OpenAI 兼容 `/v1/*`，鉴权用 `Authorization: Bearer sk-uft-...`（API Key）。 |
| `cmd/admin`（:8081） | 控制面，鉴权是**单一共享密钥** `UFT_ADMIN_TOKEN`，账户/Key/供应商/定价/审计全部在这里，**没有按用户区分的登录态**。 |
| 用户注册/登录 | **未实现**。`docs/uFreeTokens Go 后端优化技术方案 V2.md` 把"账户体系：注册登录"列在 Phase 1 路线图里，但当前代码里 `internal/auth` 只有 API Key 的 HMAC 校验中间件，没有密码登录、没有 Session/JWT。API Key 目前只能由持有 `UFT_ADMIN_TOKEN` 的人通过 `POST /accounts/{id}/api-keys` 创建。 |

**核心结论**：前端现在设计的"个人中心 → API 密钥 自助创建/删除、注册登录"这条路径，在后端**还没有对应的、可以安全暴露给浏览器的接口**——唯一存在的创建入口需要管理员密钥，这个密钥绝不能进前端 bundle。这是接入方案里第一个必须澄清的架构缺口，见 §3。

---

## 1. 目标架构总览

```
┌──────────────────────────────────────────────────────────────────────┐
│  frontend/web  (React SPA, 静态托管 / CDN)                            │
│                                                                        │
│   Presentation：现有组件基本不变（ModelCard/Table、PlaygroundModal、   │
│                 PersonalDashboardPage …）                              │
│            │                                                          │
│            ▼                                                          │
│   src/api/ 新增服务层（唯一允许发网络请求的地方）                      │
│     ├─ client.ts      fetch 封装：baseURL、超时、错误解析              │
│     ├─ auth.ts        本地 API Key 存取（Phase 0）/ 未来 session      │
│     ├─ models.ts      GET /v1/models（+ 目录增强，见 §4）             │
│     ├─ usage.ts       GET /v1/usage                                   │
│     ├─ chat.ts        POST /v1/chat/completions（SSE 流式解析）       │
│     └─ keys.ts        Phase 1 起：POST/GET /console/api-keys          │
└───────────────┬──────────────────────────────────┬───────────────────┘
                │ 直连（公网可暴露）                  │ Phase1 起新增 /console/*
                ▼                                    ▼
     ┌─────────────────────┐              ┌─────────────────────────┐
     │  cmd/gateway :8080   │              │  cmd/admin :8081         │
     │  数据面 /v1/*         │              │  控制面（当前=共享密钥）  │
     │  鉴权=API Key         │              │  Phase1 新增：           │
     │                       │              │  账户注册/登录 + 按账户  │
     │                       │              │  自助 Key CRUD（会话鉴权）│
     └─────────────────────┘              └─────────────────────────┘
```

关键原则：**前端 bundle 永远不持有 `UFT_ADMIN_TOKEN`**。凡是需要管理员密钥的接口（供应商/渠道/定价/审计），不对 `frontend/web` 暴露，继续只用 `frontend/test_web` 或未来独立的运营后台访问。

---

## 2. Gateway 接入（现在就能做，Phase 0）

这部分后端已完整可用，直接照 `frontend/test_web/user.html` 的模式接：

| 前端场景 | 接口 | 鉴权 | 备注 |
|---|---|---|---|
| 模型库列表 | `GET /v1/models` | `Bearer <api-key>` | 返回 OpenAI 风格 `{object:"list", data:[{id, object, created, owned_by}]}`，**没有价格/评分/上下文长度等字段**（见 §4 的缺口） |
| 个人中心 → 余额与账单 | `GET /v1/usage` | `Bearer <api-key>` | 返回 `{wallet:{cash_balance_micro, bonus_balance_micro, frozen_micro}, usage:{total_requests, total_input_tokens, total_output_tokens, total_charged_amount_micro}}`；**是全量累计值，没有按时间段/按 Key 拆分**，"本月已消费""调用日志"这类需要区间数据的 UI 暂时接不了（见 §6） |
| Playground 对话 | `POST /v1/chat/completions`（`stream:true`） | `Bearer <api-key>` | SSE 流式；`Content-Type: text/event-stream`。**必须用 `fetch` + `ReadableStream` 手动解析**，不能用浏览器原生 `EventSource`（它不支持自定义 Header 和 POST body） |
| Embeddings / Anthropic 兼容 | `POST /v1/embeddings`、`POST /v1/messages` | `Bearer <api-key>` | 暂无对应前端 UI，先不接 |

金额单位：所有金额是 **int64 micro 单位（1,000,000 = 1 元/刀）**，前端要在 `src/api/usage.ts` 里统一做 `microToDisplay(amount, currency)` 转换，不要在组件里裸算。

错误处理：所有接口失败都是 OpenAI 风格 `{"error":{"message","type","code","request_id"}}`；`client.ts` 里统一 parse 成 `ApiError`，UI 层按 `code`（如 `insufficient_balance`、`rate_limit_exceeded`）做针对性提示（比如提示去充值、提示降低并发）。

**Phase 0 的鉴权方式**：既然没有登录系统，"个人中心"暂时退化成 OpenRouter/新 API 站点常见的 **BYOK 模式**——用户把已经拿到的 API Key 粘贴进一个"连接"输入框，前端把它存 `localStorage`，之后所有 `/v1/*` 请求带上它。这正是 `frontend/test_web/user.html` 已经验证过的路径，风险和 test_web README 里写的一致（Key 明文存浏览器），可以先上线但要在 UI 上提示。

---

## 3. 缺口：自助注册登录与 API Key 自助管理（需要后端配合，Phase 1）

现状：`POST /accounts/{id}/api-keys`（创建 Key）、`POST /accounts`（建账户）都挂在 `cmd/admin`，鉴权是**全局共享**的 `UFT_ADMIN_TOKEN`——这个接口组的设计前提是"运营人员手工操作"，不是"终端用户自助"。`frontend/web` 的"个人中心 → API 密钥"页面如果直接打这些接口，等于把管理员密钥下发给每一个访问者，是严重的权限漏洞。

**需要后端新增**（对应 V2 方案 §7.15 已规划、尚未实现的部分）：

1. `internal/auth` 增加账户级登录：`POST /console/register`、`POST /console/login`（argon2id 密码），签发短期 JWT + refresh token 或 Redis Session。
2. `cmd/admin`（或新拆一个 `cmd/console`，视规模决定）增加一组**按账户鉴权**（不是共享密钥）的自助接口：
   - `GET/POST/DELETE /console/api-keys` —— 只能操作 JWT 里的 `account_id` 名下的 Key，对应前端 `ApiKeyItem` 的创建/列表/撤销。
   - `GET /console/wallet` —— 复用 `internal/wallet`，等价于把 `/v1/usage` 的钱包部分单独暴露给未持有 API Key、只登录了控制台的用户。
3. 这组接口上线前，`frontend/web` 的注册/登录/自助建 Key UI 只能停留在 **UI 原型态**（当前状态），不要接后端。

**过渡方案（如果业务上必须马上有"自助建 Key"体验）**：在 `cmd/admin` 前面加一层极薄的、只做"限流 + 验证码/邮箱验证"的公开代理端点，服务端持有 `UFT_ADMIN_TOKEN` 调用真正的 admin 接口，前端只拿到该代理端点地址——本质是抢跑一个最小 Console 服务，仍然建议按 V2 路线图正式做（argon2id + JWT），避免后续迁移。

---

## 4. 缺口：模型目录的定价/评分数据（Phase 2）

`frontend/web` 的 `Model` 类型（`types.ts`）字段远比 `GET /v1/models` 丰富：价格（`inputPricePerM`/`outputPricePerM`）、评分（`scores.intelligenceIndex` 等）、上下文长度、变体（免费版/思考版）、折扣标记……这些目前**只存在于前端 mock 数据里**，后端对应的真实数据（`virtual_models` 售价、`internal/pricing` Price Book）**只通过 admin 接口暴露**，没有面向客户端的 `/v1/prices` 或增强版 `/v1/models`。

建议：
- 后端在 `cmd/gateway` 增加一个**只读、可选鉴权（或免鉴权）**的目录接口，例如 `GET /v1/catalog`，在 `internal/catalog` 快照的基础上 join 虚拟模型售价（tier 相关）、能力元数据（`supportedParameters`、`toolCallingCapability`），返回结构对齐前端 `Model` 类型的子集。
- 评分类字段（intelligenceIndex/codingIndex/agenticIndex/DesignArena）是纯运营/评测数据，后端没有也不会自动产生，需要业务方决定是否引入一张 `model_benchmarks` 配置表，短期可以继续留空或前端本地维护一份"评分覆盖表"叠加在真实目录之上。
- 在这个接口上线前，模型库页面可以先只替换"是否上架""模型 ID/名称""上下文长度"这类能从 `/v1/models` + `internal/catalog` 拿到的字段，价格/评分继续用 mock，UI 上用一个"演示数据"角标提示用户。

---

## 5. Playground 流式对话改造要点

现状 `PlaygroundModal.tsx` 用 `setTimeout` 拼假回复。真实接入：

1. 请求：`POST /v1/chat/completions`，body 用现有设置面板的 `temperature`/`maxTokens`/`systemPrompt` 组出 OpenAI 格式 `messages` 数组，`stream: true`。
2. 用 `fetch(url, {method:'POST', headers, body})` 拿到 `response.body`（`ReadableStream`），手写一个按行分割 `data: {...}\n\n` 的 SSE parser（后端用的是 `bufio.Reader` 而非 `Scanner`，说明上游可能吐出很长的单行 SSE，前端 parser 也不能假设行长度上限）。
3. 每个 chunk 增量拼进当前 assistant message 的 `content`，做打字机效果；`[DONE]` 或流结束时停止。
4. 错误/中断：网络异常或 4xx/5xx 直接走 `client.ts` 统一的 `ApiError` 展示；用户主动关闭弹窗时 `AbortController.abort()` 取消请求，避免后端按"客户端断开"走计费兜底估算（V2 方案 §7.9.4）。
5. 计费透明度：回复展示区可以选择性展示这次调用消耗的 token/费用——但 `/v1/chat/completions` 的非流式响应里带 `usage`，流式响应的 usage 在最后一个 chunk（需要请求里带 `stream_options.include_usage=true`），要不要展示取决于后端是否支持该参数，需与后端确认。

---

## 6. 个人中心其余 Tab 的数据可行性一览

| Tab | 当前可行性 | 说明 |
|---|---|---|
| API 密钥 | **阻塞**，见 §3 | 需要 console 自助鉴权体系 |
| 余额与账单（概览部分） | **可行（Phase 0）** | `GET /v1/usage` 的 `wallet.*` 字段 |
| 余额与账单（"本月已消费"分月统计） | **阻塞** | 后端目前只给全量累计，没有区间聚合接口，需要后端在 `request_logs`（或 ClickHouse 同步表）上加一个按时间范围聚合的查询接口 |
| 调用日志 / 活动记录 | **阻塞** | 需要一个分页查询 `request_logs` 的接口（当前完全没有暴露给客户端），V2 方案里提到"控制台用量报表"在 Phase 2 |
| 安全护栏 / BYOK / 模型路由 / 系统预设 / 分类器 | **不接** | 后端没有对应领域模型，属于纯前端占位功能，先保留静态展示 |
| 工作区（多工作区切换） | **不接** | 后端账户模型目前是 `account`，没有"工作区"这一级概念，如果要做需要先在后端设计，超出当前后端范围 |

---

## 7. 客户端服务层设计（`src/api/`）

```ts
// client.ts —— 唯一发请求的地方
const GATEWAY_BASE = import.meta.env.VITE_GATEWAY_BASE_URL // 默认 http://localhost:8080
// 不要出现 VITE_ADMIN_BASE_URL / 管理员 token 相关的任何 env，防止被打进前端 bundle

async function request(path, opts) {
  const key = authStore.getApiKey() // localStorage，Phase 0
  const res = await fetch(`${GATEWAY_BASE}${path}`, {
    ...opts,
    headers: { ...opts.headers, ...(key ? { Authorization: `Bearer ${key}` } : {}) },
  })
  if (!res.ok) throw await ApiError.fromResponse(res) // 解析 {error:{message,type,code,request_id}}
  return res
}
```

- `authStore`：Phase 0 只管 API Key 的存取（对应 test_web 的"连接"流程）；Phase 1 切换成 JWT/refresh 时只改这一个模块，组件不用动。
- 所有组件里现在直接 `useState` 造假数据的地方（`INITIAL_MODELS`、`apiKeys`、余额卡片数字），改造成 `useEffect` 调 `src/api/*` + loading/error 状态，不再需要改动 UI 结构本身。
- 环境变量：新增 `frontend/web/.env.example` 条目 `VITE_GATEWAY_BASE_URL=http://localhost:8080`；**明确不引入任何指向 `:8081`（admin）的前端可读 env**。

---

## 8. 分阶段落地计划

| 阶段 | 内容 | 前置条件 |
|---|---|---|
| **Phase 0**（现在可做） | 接 `GET /v1/models`（仅替换可用字段）、`GET /v1/usage`（余额卡片）、`POST /v1/chat/completions`（Playground 真实对话）；鉴权用 BYOK（粘贴 API Key，localStorage） | 无需后端改动，`frontend/test_web/user.html` 已验证同一套接口 |
| **Phase 1** | 后端新增 console 注册/登录 + 按账户自助 API Key CRUD（§3）；前端接通个人中心的注册/登录、API 密钥自助创建/删除 | 需要后端排期实现 `internal/auth` 的密码登录与自助 Key 接口 |
| **Phase 2** | 后端新增只读目录/定价接口（§4）；前端模型库页面价格/上下文长度改用真实数据；新增区间用量聚合接口，接通"本月已消费""调用日志" | 需要后端在 catalog/pricing、request_logs 聚合上补接口 |
| **Phase 3** | 充值/支付对接（对应 V2 路线图 Phase 2 的在线支付），接通"余额与账单"的充值入口 | 依赖后端支付网关落地 |

---

## 9. 安全注意事项

1. `UFT_ADMIN_TOKEN` 任何时候都不能出现在 `frontend/web` 的构建产物、网络请求或 localStorage 里。
2. Gateway 需要为 `frontend/web` 的部署域名开放 CORS（当前 `cmd/gateway` 是否已配置 CORS 中间件需要另行确认，若未配置需要后端补充，允许的 Header 至少包含 `Authorization`、`Content-Type`）。
3. BYOK 模式下 API Key 明文存 `localStorage` 是已知取舍（和 test_web 一致），上线前应在 UI 上做"该密钥仅保存在本机浏览器"的提示，并提供"退出并清除本地密钥"的入口。
4. Phase 1 引入登录态后，`localStorage` 里的短期 JWT 需要设置合理过期时间，`refresh` 流程走 `client.ts` 统一拦截 401 自动刷新，避免每个调用点重复处理。
