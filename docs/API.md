# uFreeTokens API 参考（当前实际可用的接口）

> 记录 `cmd/gateway`（:8080）当前**真正实现**的接口，作为 `frontend/web`
> 的唯一接口依据（技术方案迭代7）。没有出现在这里的路径要么返回
> `503 not_implemented`（比如 `/v1/completions`），要么根本不存在。
> `cmd/admin`（:8081）是内部运营工具，鉴权是共享密钥
> `UFT_ADMIN_TOKEN`，`frontend/web` 从不直连它，这里不展开列举它的接口。
>
> `/v1/*` 的字段级细节（请求/响应结构、错误码全集、示例）以
> [gateway-openapi.json](gateway-openapi.json) 为准：它由
> `internal/app.GatewayOpenAPI()` 从代码生成，`TestGatewayOpenAPI_UpToDate`
> 保证与实现一致，并驱动 `frontend/web` 开发者文档的 API 参考页。本文件是给人
> 读的概述。

## 通用约定

- **金额**：所有金额字段都是 `int64` 微元单位（1,000,000 = 1 元，CNY）。
- **错误格式**（OpenAI 兼容）：

  ```json
  {"error": {"message": "...", "type": "...", "code": "...", "request_id": "..."}}
  ```

  `code` 是程序应该判断的稳定标识（如 `insufficient_balance`），`message`
  是给人看的英文描述，前端按 `code` 做本地化提示（见
  `frontend/web/src/api/errors.ts`）。
- **请求 ID**：所有响应都带 `X-Request-Id` 头（客户端传了就透传，否则网关
  生成一个 ULID），排障时用它在 `request_logs` 里定位。
- **鉴权**：`/v1/*` 用 `Authorization: Bearer sk-uft-...`（API Key）；
  `/console/*` 用 httpOnly Cookie（`uft_session`），两者完全独立，一个都不
  依赖另一个。
- **跨域**：默认前后端同源（dev 用 Vite proxy，prod 用 Nginx 反代，见
  `deploy/nginx/web.conf`），不需要 CORS。只有 `/v1/*` 支持可选的 CORS
  （`gateway.cors_origins` 配置非空时启用），`/console/*` 永远不开 CORS。

---

## `/v1/*`：数据面（OpenAI 兼容）

### `GET /v1/catalog` — 公开模型目录

**鉴权**：无。**限流**：按 IP 60 次/分钟。**缓存**：`Cache-Control: public, max-age=60`。

返回对 `free` tier 可见的虚拟模型，包括 `status=active` 和 `status=deprecated`（每条带 `status` 字段；deprecated 模型只用于展示，调用会返回 404 `model_not_found`）。

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
      "description": "运营录入的介绍文案（可能缺失）",
      "provider_display": "DeepSeek",
      "tags": ["reasoning", "coding"],
      "scores": {"intelligence_index": 39.5, "coding_index": 82, "agentic_index": 68,
                 "design_arena": {"code": 1320, "ui_component": 1335}}
    }
  ]
}
```

`display_name`/`description`/`provider_display`/`tags`/`scores`
是运营通过 `cmd/admin` 的 `PUT /virtual-models/{id}/metadata` 录入的，
可能整体缺失（没录入过）。`scores` 的键名由后端按白名单校验（未知键写入时 400）：
`intelligence_index`、`coding_index`、`agentic_index`，以及嵌套对象 `design_arena`
（`code`、`ui_component`、`game_dev`、`data_viz`、`three_d`、`image`、`video`、`svg`），
值都是数字。迁移 00026 已把历史数据里的 camelCase 键改写为 snake_case。

### `GET /v1/models` — 模型列表（需要 API Key）

**鉴权**：`Bearer <api-key>`。返回该 Key 所属账户 tier 可见的、状态为
`active` 的虚拟模型，OpenAI 兼容格式：

```json
{"object": "list", "data": [{"id": "deepseek-ai/DeepSeek-V4-Flash", "object": "model", "created": 0, "owned_by": "ufreetokens"}]}
```

不含价格/评分/上下文长度——要这些字段用 `GET /v1/catalog`。这个接口的作用
是"这把 Key 具体能调用哪些模型 ID"，和 `/v1/catalog` 的"模型是否公开存在"
是两个维度。

### `GET /v1/usage` — 自助用量查询（需要 API Key）

**鉴权**：`Bearer <api-key>`。可选 `?since=<RFC3339>`，留空表示全量历史。

```json
{
  "wallet": {"cash_balance_micro": 5000000, "bonus_balance_micro": 0, "frozen_micro": 0},
  "usage": {"total_requests": 12, "total_input_tokens": 3400, "total_output_tokens": 1800, "total_charged_amount_micro": 15000}
}
```

`usage.*` 只统计 `status='success'` 的请求。这是全量/区间累计值，**不是**
按天拆分的时间序列——按天/按模型拆分用 `GET /console/usage`（需要登录控制台，
不是 API Key）。

### `POST /v1/chat/completions` — 对话补全

**鉴权**：`Bearer <api-key>`。OpenAI 兼容请求/响应格式。

- `stream: true` 时返回 `text/event-stream`；网关无条件向上游注入
  `stream_options.include_usage=true`（不需要客户端自己带），但只有客户端
  自己也在请求体里带了 `stream_options.include_usage=true` 时，网关才会把
  那个 usage-only chunk 转发给客户端——协议行为和"客户端自己发起、不带这个
  参数的请求"完全一致，网关内部需要 usage 计费不代表客户端也要看到它。
- 客户端中途断开：网关按已经转发给客户端的内容字节数估算实际输出计费
  （不超过预扣上限），`request_logs.usage_source` 记为 `estimated`。
- 错误码参考：`invalid_api_key`（401）、`model_not_allowed`/`model_not_found`
  （403/404）、`insufficient_balance`（402）、`rate_limit_exceeded`/
  `concurrency_limit_exceeded`（429）、`no_available_channel`（503）、
  `upstream_error`（502）。

### `POST /v1/embeddings` — 向量嵌入

**鉴权**：`Bearer <api-key>`。同上计费管线，只有 `input` 用量，没有 `output`。

### `POST /v1/messages` — Anthropic 兼容入口

**鉴权**：`Bearer <api-key>`（只认 `Authorization` 头，不认 Anthropic 的
`x-api-key`）。请求/成功响应是 Anthropic Messages API 的形状；网关内部转译成
`chat.completions` 走同一条鉴权/预扣/路由/结算管线，计费口径和
`/v1/chat/completions` 完全一致。与官方 API 的差异：错误响应仍是上面的
OpenAI 兼容格式；只支持文本内容块；只转发 `model`、`messages`、`system`、
`max_tokens`、`temperature`、`top_p`、`stream`、`stop_sequences`，其余字段丢弃。

### 公开排行榜与基准测试（免鉴权）

`GET /v1/rankings/models|authors|speed|tools|multimodal|apps`、`GET /v1/benchmarks`、
`GET /v1/benchmarks/{slug}`，完整字段见 `docs/gateway-openapi.json`（web 文档站的 API
参考页由它渲染），口径见 `docs/基准测试与排行榜数据服务技术方案.md`。

**鉴权**：无（带不带 API Key 返回相同内容）。**限流**：按 IP 60 次/分钟（这几个接口共享）。
**缓存**：`Cache-Control: public, max-age=300`，进程内缓存 5 分钟。

- 榜单参数：`period=day|week|month`（默认 `week`）= 截至昨天的最近 1/7/30 个完整自然日，
  按 `Asia/Shanghai` 切日；`limit=1..100`（默认 20）；`/v1/rankings/models` 另有
  `series=none|day`。数据由 worker 每 30 分钟从 `usage_hourly` 物化（`public_*_daily` 表）。
- 口径：只统计成功请求的 input + output token（output 已含 reasoning）；排除
  `accounts.exclude_from_public_stats` 的账户；统计期内独立账户数 < 3 的条目归入 `others`；
  单账户最多计入某条目当期原始总量的 20%；只列出公开目录可见的模型。速度榜按成功流式
  请求的单请求吞吐（输出 token ÷ 生成耗时）取中位数排名（另给加权平均），模型需 ≥ 10 个样本。应用榜只统计声明了 `X-Title` 的请求。
- 配置（环境变量）：`UFT_PUBLIC_RANKINGS_ENABLED=false` 时 `/v1/rankings/*` 返回
  `503 service_unavailable`；`UFT_PUBLIC_RANKINGS_SHOW_ABSOLUTE=true` 才在响应里带绝对 token
  数（`tokens`/`total_tokens`），默认只有份额与排名；`UFT_PUBLIC_RANKINGS_MIN_ACCOUNTS` 改隐私阈值。
- 基准测试只返回 `status=published` 的基准与其最新一次已发布的 run；未发布或不存在的 slug
  返回 `404 not_found`。

### 应用归因请求头

`/v1/chat/completions`、`/v1/messages`、`/v1/embeddings` 可以带 `X-Title: <应用名>`
（去掉控制字符、合并空白后截断到 64 个字符）和可选的 `HTTP-Referer: <应用地址>`（只保留
`scheme://host`），写入 `request_logs.app_name` / `app_url`，用于公开的"热门应用"榜。
只带 `HTTP-Referer` 不带 `X-Title` 的请求不算声明了应用。运营可在后台屏蔽或合并冒用的应用名。

### 尚未实现（返回 `503 not_implemented`）

`POST /v1/completions`、`POST /v1/images/generations`、
`POST /v1/audio/transcriptions`、`POST /v1/audio/speech`。

---

## `/console/*`：自助控制台

全部路径都不开 CORS（Cookie 会话只信任同源请求）；非 GET 请求都要求带
`X-UFT-CSRF: 1` 头，否则返回 `403 csrf_check_failed`（见
`internal/console` 包文档的 CSRF 防护说明）。

### `POST /console/register` — 注册

**鉴权**：无。**限流**：按 IP 3 次/分钟。

请求：`{"email": "...", "password": "..."}`（密码至少 8 位）。
成功：`201 {"user_id": 1, "account_id": 1}`。不自动登录，也不做邮箱验证。

失败：`400 invalid_email` / `400 weak_password` / `409 email_taken`。

### `POST /console/login` — 登录

**鉴权**：无。**限流**：按 IP 10 次/分钟 + 按邮箱 5 次/分钟（超限
`429 rate_limit_exceeded`，`Redis` 故障时 fail-closed，拒绝而不是放行）。

请求同注册。成功：`200 {"user_id": 1, "account_id": 1}`，并通过
`Set-Cookie: uft_session=...` 签发会话（HttpOnly，7 天滑动过期）。

密码错误和邮箱不存在都返回完全相同的 `401 invalid_credentials`（避免用户名
枚举）。

### `POST /console/logout` — 登出

**鉴权**：无（没有有效会话也返回成功）。清会话 + 清 Cookie，`204`。

### `GET /console/me` — 当前用户

**鉴权**：会话。

```json
{"user_id": 1, "email": "you@example.com", "email_verified": false, "account_id": 1, "account_tier": "free",
 "exclude_from_public_stats": false}
```

### `PUT /console/settings/public-stats` — 不计入公开排行榜

**鉴权**：会话 + `X-UFT-CSRF: 1`；只有账户的 owner / admin 成员能改（否则 `403 permission_denied`）。

请求：`{"exclude_from_public_stats": true}`。成功：`200 {"exclude_from_public_stats": true}`。
与运营后台 `PATCH /accounts/{id}` 的同名字段是同一个值；今天与昨天的公开榜单约 30 分钟内
生效，历史数据在 worker 每天一次的重建后生效。

### `GET /console/api-keys` — 列出自己的 API Key

**鉴权**：会话。

```json
{"data": [{"id": 1, "name": "my-key", "display_prefix": "sk-uft-a1B2c3", "status": "active", "allowed_models": null, "rpm_limit": null, "tpm_limit": null, "concurrency_limit": null, "created_at": "2026-09-01T00:00:00Z"}]}
```

### `POST /console/api-keys` — 建一把新 Key

**鉴权**：会话 + CSRF。请求：`{"name": "my-key"}`。

响应在 `GET /console/api-keys` 的单条形状基础上多一个 `key` 字段——**明文
只在这次响应里出现一次**，之后无法再次查看。

### `POST /console/api-keys/{id}/revoke` — 吊销一把 Key

**鉴权**：会话 + CSRF。按 `account_id` 限定范围，`id` 属于别的账户返回
`404`（不区分"不存在"和"越权"）。成功 `204`。

### `GET /console/wallet` — 钱包余额

**鉴权**：会话（不需要 API Key）。

```json
{"cash_balance_micro": 5000000, "bonus_balance_micro": 0, "frozen_micro": 0}
```

### `GET /console/usage?from=&to=&group_by=day|model` — 区间用量

**鉴权**：会话。`from`/`to` 格式 `YYYY-MM-DD`，留空默认本月至今；区间最长
90 天，超出返回 `400 invalid_request`。

```json
{"data": [{"group": "2026-09-27", "requests": 3, "input_tokens": 900, "output_tokens": 450, "charged_amount_micro": 1350}]}
```

`group_by=day` 时 `group` 是日期字符串；`group_by=model` 时是虚拟模型名。

### `GET /console/logs?before=&limit=&api_key_id=` — 调用日志

**鉴权**：会话。`(created_at, request_id)` keyset 分页，倒序；`limit` 默认/
最大 100；时间窗最长 30 天；`api_key_id` 可选，过滤到某一把 Key（属于别的
账户的 `api_key_id` 只会查出空结果，不会跨账户泄露数据）。

```json
{
  "data": [
    {
      "request_id": "01M3...", "created_at": "2026-09-27T10:00:00Z", "api_key_id": 1,
      "virtual_model": "deepseek-ai/DeepSeek-V4-Flash", "status": "success", "http_status": 200,
      "input_tokens": 300, "output_tokens": 150, "charged_amount_micro": 450,
      "latency_ms": 820, "usage_source": "upstream"
    }
  ],
  "next_cursor": "2026-09-27T10:00:00Z|01M3..."
}
```

`next_cursor` 传给下一次请求的 `before` 参数；空字符串表示已经翻到最后一页。
只有元数据，**不含**请求/响应正文。

---

## 网关自身探针

- `GET /healthz` — 存活探针，不检查依赖。
- `GET /readyz` — 检查 Postgres/Redis 连通性，任一失败返回 `503`。
