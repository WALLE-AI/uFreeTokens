# 运营后台 API（cmd/admin）约定与接口清单

面向 `frontend/admin` 与内部工具。网关的对外 API 见 [API.md](API.md)。本文前半部分是约定，后半部分的接口清单由 `internal/app.AdminAPIDoc()` 从路由表生成：路由表是接口与权限的唯一来源。

完整的请求/响应结构见 [admin-openapi.json](admin-openapi.json)（OpenAPI 3.1）。它由 `internal/app.AdminOpenAPI()` 从 Go 类型反射生成，每个接口都登记了请求体与响应体类型。同源生成的前端类型在 `frontend/admin/src/api/generated.ts`。`frontend/admin/src/api/contract.ts` 在编译期检查前端手写类型用到的字段后端都会返回。三者由 `TestAdminOpenAPI_UpToDate` 保证与代码一致。

## 1. 鉴权与权限

- **登录**：`POST /auth/login {email, password}` 返回 `{token, expires_at, user}`。之后每个请求都带 `Authorization: Bearer <token>`，令牌以 `uas_` 开头。
- **会话过期**：12 小时无操作过期（滑动续期），创建 7 天后无论是否活跃都过期。
- **登出**：`POST /auth/logout` 使令牌立即失效。
- **登录限流**：同一邮箱 15 分钟内失败 5 次后返回 `429 rate_limit_exceeded`，同一 IP 的上限是 20 次。
- **当前身份**：`GET /me` 返回当前管理员及其权限点列表。前端据此决定显示哪些菜单和按钮，最终仍以服务端裁决为准。
- **权限不足**：统一返回 `403 permission_denied`。
- **角色**：超级管理员 `super_admin`（拥有全部权限 `*`）、运营 `operator`、定价 `pricing`、财务 `finance`、客服 `support`。各角色的权限点见迁移 `00016_admin_identity.sql`。
- **字段级权限**：下面两个字段额外要求 `provider_key:write`，因为它们决定上游密钥会被发往哪里：
  - `PATCH /provider-accounts/{id}` 的 `base_url`
  - `PATCH /providers/{id}` 的 `allowed_hosts`
- **两步验证（TOTP）**：
  - 管理员可在「安全设置」里自愿启用：`POST /auth/totp/setup` 返回密钥，`POST /auth/totp/enable {code}` 确认启用，`POST /auth/totp/disable {code}` 停用。
  - 启用后，`/auth/login` 需要带 `totp_code`。缺少验证码返回 `401 totp_required`，验证码错误返回 `401 invalid_totp`。同一个验证码不能重复使用。
  - 超级管理员可以通过 `PATCH /admin-users/{id} {"reset_totp": true}` 清除某个管理员的绑定。
  - 密钥用 KEK 加密存储，服务端未配置 KEK 时不能启用。
- **base_url 刚改过**：上游账号的 `base_url` 修改后 24 小时内，`GET /provider-accounts/{id}/upstream-models`（会带着解密后的密钥请求上游）只允许超级管理员调用。
- **应急令牌**：`UFT_ADMIN_TOKEN`（可选）是共享的 break-glass 令牌。使用它时身份为 `system`（id=0），拥有全部权限，生产环境应关闭。
- **审计**：审计日志的操作人一律取自已认证身份。客户端发来的 `X-Actor-*` 请求头会被忽略。

## 2. 请求与响应

- **错误格式**：错误响应统一为 `{"error": {"message", "type", "code", "request_id"}}`。

| 状态码 | code | 含义 |
|---|---|---|
| 400 | `invalid_request` | 参数或请求体不合法，包括非法的枚举值、布尔值和数字 |
| 401 | `unauthorized` / `invalid_credentials` | 未登录、会话过期，或用户名密码错误 |
| 403 | `permission_denied` | 权限不足 |
| 404 | `not_found` | 资源不存在 |
| 409 | `conflict` / `balance_changed` / `retry` | 重复提交、状态不允许，或并发冲突 |
| 412 | `version_conflict` | `If-Match` 版本不一致：资源已被他人修改，需刷新后重试 |
| 422 | `invalid_reference` / `constraint_violation` / `unsafe_upstream_url` / `idempotency_key_reused` | 引用的对象不存在、违反数据约束、上游地址不安全，或 Idempotency-Key 被用于不同的请求 |
| 429 | `rate_limit_exceeded` | 登录失败次数过多 |
| 500 | `internal_error` | 服务端错误。响应中不含 SQL 或上游原文，排查时用 `request_id` 查日志 |
| 502 | `upstream_unavailable` | 上游接口不可用 |
| 503 | `kek_not_configured` / `not_implemented` | 服务端未配置密钥加密密钥（KEK），或未启用价格同步 |

- **金额**：金额字段一律是 `*_micro` 整数，单位为微元（1 元 = 1,000,000）。单价、汇率、比率是十进制字符串，比如 `"10.5"`。
  - 请求体里旧的 `amount` 字段仍兼容一个版本，请改用 `amount_micro`。
- **时间**：统一使用 RFC3339 格式。
  - 统计接口支持 `?tz=Asia/Shanghai`（IANA 时区名）。缺省时用 `UFT_ADMIN_STATS_TZ`，cmd/admin 默认 `Asia/Shanghai`。它决定两件事：纯日期 `from/to` 按哪个时区解释，以及按小时、按天分桶时的边界。
  - 按小时分桶的标签带时区偏移。
- **分页**：列表有三种形态。
  - 偏移分页 `{data, total, page, page_size}`。
  - 游标分页 `{data, next_cursor}`，`next_cursor` 为空表示没有下一页。
  - 小列表 `{data}`。有上限的小列表带 `truncated`。
  - `limit` 或 `page_size` 超过上限时截到上限，不会回落到默认值。
- **枚举**：所有枚举值由 `GET /meta/enums` 提供，前端不再硬编码。
- **乐观锁**：
  - 详情接口返回 `ETag: W/"<version>"`，`PATCH` 请求带 `If-Match` 头。
  - 版本不一致返回 412。
  - 不带 `If-Match` 时默认只记 WARN、不做版本校验（兼容旧客户端）。设置 `UFT_ADMIN_REQUIRE_IF_MATCH=true` 后，有详情页的资源（供应商、上游账号、虚拟模型、渠道、账户）缺少 `If-Match` 返回 `428 precondition_required`。
  - `PATCH` 成功后响应里带新的 `ETag`。
- **幂等**：
  - `POST` 可以带 `Idempotency-Key` 头（最长 128 字符，24 小时内有效）。同一管理员用同一个 Key 提交相同请求时，服务端直接重放第一次的响应，响应头带 `Idempotent-Replayed: true`。
  - 用同一个 Key 提交不同的请求返回 422，第一次请求还在处理中返回 409。
  - 人工调账和赠金本身还要求业务幂等键 `ref_id`（必填）。
- **事务**：每个写操作和它的审计记录在同一个数据库事务里，任一失败整体回滚。
  - 批量接口（批量审批、批量忽略、批量导入）每一条独立成一个事务，逐条返回结果，部分失败不影响其余条目。
- **渠道健康**：`GET /channels/health?window_minutes=15`。
  - 返回每个活跃渠道的请求量、错误率、P95，以及网关熔断器状态、上游 Key 冷却情况和最近的健康事件，判定阈值也由服务端给出。
  - 熔断与冷却状态来自 Redis。cmd/admin 连不上 Redis 时 `runtime_state_known=false`。
  - 运营吊销或重新启用上游 Key 时，服务端会清除该 Key 的冷却记录。
- **统计数据源**：统计接口响应里的 `source` 为 `raw`（直接统计请求日志）或 `rollup`（时间窗超过 48 小时，读小时汇总表）。
- **展示元数据自动填充**：`GET /virtual-models/{id}/metadata/suggestion` 返回 `display_name` / `provider_display` / `description` / `tags` 的建议值，只读不落库。
  - 每项带 `source`：`external`（外部目录，`detail` 是抓取器名，如 `openrouter_models`）、`vendor`（内置厂商表）、`derived`（由模型名、类型、能力推导）；没有建议时 `value` 为空。
  - 介绍文案只取外部目录（目前是 OpenRouter 的 `description`），不编造；评分不在建议范围内，由评测榜单发布投影写入。
  - `?llm=1`：介绍文案改由 LLM 结合模型事实与外部原文生成一两句中文（`source=llm`，`detail` 为所用模型）。LLM 与 worker 的优惠抽取共用配置段 `datasync`（`llm_base_url`、`llm_model`；密钥放在 `llm_api_key_env` 指向的环境变量里，默认 `UFT_DATASYNC_LLM_API_KEY`），cmd/admin 与 worker 读同一份配置文件，启动日志会说明是否启用；响应的 `llm_available` 表示是否可用。未配置返回 503 `llm_not_configured`，调用失败返回 502 `llm_unavailable`。
  - 批量：`POST /virtual-models/metadata/autofill`，body `{"virtual_model_ids":[...], "dry_run":true}`（1–200 个）。只用确定性来源（不调 LLM），只补空字段，不碰评分；逐条独立事务并记审计 `virtual_model_metadata.autofill`，逐条返回 `changes`（将要/已经写入的字段）与 `applied`。
  - 待上架候选一键上架（`POST /pending-model-listings/{id}/publish`）时，若虚拟模型还没有展示元数据，会按同样的建议值自动插入一条（评分留空），响应 `metadata_created=true`；已有记录一律不覆盖。
- **基准测试与公开榜单**（docs/基准测试与排行榜数据服务技术方案.md）：
  - `PUT /virtual-models/{id}/metadata` 的 `scores` 按白名单校验，只接受数字型的 `intelligence_index`、`coding_index`、`agentic_index` 与嵌套对象 `design_arena.{code,ui_component,game_dev,data_viz,three_d,image,video,svg}`，其他键返回 400；允许的键也在 `GET /meta/enums` 的 `score_keys` / `design_arena_keys` 里。
  - 基准：`/benchmarks`（定义，PATCH 走 If-Match）、`POST /benchmarks/{id}/runs`（一次录入或批量导入整批结果，全部成功或全部回滚；`publish: true` 时立即发布）、`POST /benchmark-runs/{id}/publish`（同基准此前发布的 run 自动取消发布，保留为历史）、`DELETE /benchmark-runs/{id}`（只有从未发布过的 run 能删，否则 409）。公开接口只展示 `status=published` 的基准的最新已发布 run。
  - 账户的 `exclude_from_public_stats`（创建、`PATCH /accounts/{id}`）：内部测试、压测、评测账户的流量不计入公开排行榜；用户也可在个人中心自行关闭（`PUT /console/settings/public-stats`）。
  - 应用榜治理：`GET /public-apps?days=` 列出声明过的应用（不受隐私阈值限制）；`/public-app-rules` 按 `app_key` 屏蔽（`block`）、合并（`merge` + `merge_into`）或改名（`rename` + `display_name`），网关读榜时实时生效（公开缓存 5 分钟）。
- **已废弃**：下面两种用法仍可用，但响应带 `Deprecation: true` 头，请改用新接口：
  - `GET /virtual-models?name=` 改用 `GET /virtual-models/lookup?name=`
  - `GET /channels?virtual_model_id=&provider_account_id=&upstream_model=` 改用 `GET /channels/lookup`

## 3. 接口清单（自动生成，勿手改）

更新方法：`UPDATE_ADMIN_API_DOC=1 go test ./internal/app -run TestAdminAPIDoc_UpToDate`

<!-- routes:begin -->
| 方法 | 路径 | 所需权限 |
|---|---|---|
| POST | /auth/login | （公开，限流） |
| GET | /accounts | `account:read` |
| POST | /accounts | `account:write` |
| GET | /accounts/{accountID} | `account:read` |
| PATCH | /accounts/{accountID} | `account:write` |
| GET | /accounts/{accountID}/api-keys | `account:read` |
| POST | /accounts/{accountID}/api-keys | `account:write` |
| GET | /accounts/{accountID}/credit-grants | `account:read` |
| POST | /accounts/{accountID}/credit-grants | `wallet:adjust` |
| GET | /accounts/{accountID}/ledger | `account:read` |
| POST | /accounts/{accountID}/members | `account:write` |
| DELETE | /accounts/{accountID}/members/{userID} | `account:write` |
| PATCH | /accounts/{accountID}/members/{userID} | `account:write` |
| GET | /accounts/{accountID}/usage | `observe:read` |
| POST | /accounts/{accountID}/wallet/adjust | `wallet:adjust` |
| GET | /admin-roles | `admin_user:manage` |
| GET | /admin-users | `admin_user:manage` |
| POST | /admin-users | `admin_user:manage` |
| PATCH | /admin-users/{adminUserID} | `admin_user:manage` |
| GET | /api-keys | `account:read` |
| PATCH | /api-keys/{apiKeyID} | `account:write` |
| POST | /api-keys/{apiKeyID}/revoke | `account:write` |
| GET | /audit-logs | `audit:read` |
| POST | /auth/logout | `（已登录即可）` |
| POST | /auth/password | `（已登录即可）` |
| POST | /auth/totp/disable | `（已登录即可）` |
| POST | /auth/totp/enable | `（已登录即可）` |
| POST | /auth/totp/setup | `（已登录即可）` |
| DELETE | /benchmark-runs/{runID} | `catalog:write` |
| GET | /benchmark-runs/{runID} | `catalog:read` |
| POST | /benchmark-runs/{runID}/publish | `catalog:write` |
| GET | /benchmarks | `catalog:read` |
| POST | /benchmarks | `catalog:write` |
| GET | /benchmarks/{benchmarkID} | `catalog:read` |
| PATCH | /benchmarks/{benchmarkID} | `catalog:write` |
| POST | /benchmarks/{benchmarkID}/runs | `catalog:write` |
| GET | /catalog/counts | `catalog:read` |
| GET | /channels | `catalog:read` |
| POST | /channels | `catalog:write` |
| GET | /channels/health | `observe:read` |
| GET | /channels/lookup | `catalog:read` |
| GET | /channels/{channelID} | `catalog:read` |
| PATCH | /channels/{channelID} | `catalog:write` |
| POST | /channels/{channelID}/cost-price | `pricing:write` |
| GET | /channels/{channelID}/price-books | `pricing:read` |
| POST | /channels/{channelID}/price-observations | `pricing:write` |
| GET | /fx-rates | `pricing:read` |
| POST | /fx-rates | `pricing:write` |
| GET | /fx-rates/latest | `pricing:read` |
| GET | /me | `（已登录即可）` |
| GET | /meta/codecs | `（已登录即可）` |
| GET | /meta/dialect-presets | `（已登录即可）` |
| GET | /meta/enums | `（已登录即可）` |
| GET | /model-aliases | `catalog:read` |
| PUT | /model-aliases | `catalog:write` |
| GET | /model-aliases/namespaces | `catalog:read` |
| GET | /pending-model-listings | `pricing:read` |
| POST | /pending-model-listings/batch-dismiss | `pricing:write` |
| POST | /pending-model-listings/{listingID}/dismiss | `pricing:write` |
| POST | /pending-model-listings/{listingID}/publish | `pricing:write` |
| GET | /price-change-requests | `pricing:read` |
| POST | /price-change-requests/batch-approve | `price_change:approve` |
| GET | /price-change-requests/{changeRequestID} | `pricing:read` |
| POST | /price-change-requests/{changeRequestID}/approve | `price_change:approve` |
| POST | /price-change-requests/{changeRequestID}/reject | `price_change:approve` |
| GET | /price-sources | `pricing:read` |
| POST | /price-sources | `pricing:write` |
| GET | /price-sources/{priceSourceID} | `pricing:read` |
| PATCH | /price-sources/{priceSourceID} | `pricing:write` |
| POST | /price-sources/{priceSourceID}/run | `pricing:write` |
| GET | /price-sources/{priceSourceID}/runs | `pricing:read` |
| GET | /pricesync/price-comparison | `pricing:read` |
| POST | /pricesync/reference-price-lookup | `pricing:read` |
| POST | /pricing/preview | `pricing:read` |
| GET | /provider-accounts | `catalog:read` |
| POST | /provider-accounts | `provider_key:write` |
| GET | /provider-accounts/{providerAccountID} | `catalog:read` |
| PATCH | /provider-accounts/{providerAccountID} | `catalog:write` |
| GET | /provider-accounts/{providerAccountID}/dialect | `catalog:read` |
| PUT | /provider-accounts/{providerAccountID}/dialect | `provider_key:write` |
| POST | /provider-accounts/{providerAccountID}/import-models | `catalog:read` |
| POST | /provider-accounts/{providerAccountID}/keys | `provider_key:write` |
| GET | /provider-accounts/{providerAccountID}/upstream-models | `provider_key:write` |
| PATCH | /provider-keys/{providerKeyID} | `provider_key:write` |
| POST | /provider-keys/{providerKeyID}/revoke | `provider_key:write` |
| GET | /providers | `catalog:read` |
| POST | /providers | `catalog:write` |
| GET | /providers/{providerID} | `catalog:read` |
| PATCH | /providers/{providerID} | `catalog:write` |
| POST | /providers/{providerID}/price-observations | `pricing:write` |
| GET | /public-app-rules | `catalog:read` |
| POST | /public-app-rules | `catalog:write` |
| DELETE | /public-app-rules/{ruleID} | `catalog:write` |
| GET | /public-apps | `catalog:read` |
| GET | /request-logs | `observe:read` |
| GET | /request-logs/{requestID} | `observe:read` |
| GET | /stats/overview | `observe:read` |
| GET | /stats/usage | `observe:read` |
| GET | /todo-counts | `（已登录即可）` |
| GET | /upstream-offers | `pricing:read` |
| GET | /upstream-offers/{offerID} | `pricing:read` |
| POST | /upstream-offers/{offerID}/adopt | `pricing:write` |
| POST | /upstream-offers/{offerID}/status | `pricing:write` |
| GET | /virtual-models | `catalog:read` |
| POST | /virtual-models | `catalog:write` |
| GET | /virtual-models/lookup | `catalog:read` |
| POST | /virtual-models/metadata/autofill | `catalog:write` |
| GET | /virtual-models/{virtualModelID} | `catalog:read` |
| PATCH | /virtual-models/{virtualModelID} | `catalog:write` |
| PUT | /virtual-models/{virtualModelID}/metadata | `catalog:write` |
| GET | /virtual-models/{virtualModelID}/metadata/suggestion | `catalog:read` |
| GET | /virtual-models/{virtualModelID}/price-books | `pricing:read` |
| POST | /virtual-models/{virtualModelID}/sell-price | `pricing:write` |
<!-- routes:end -->
