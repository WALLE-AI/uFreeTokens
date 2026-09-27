# cmd/admin 运营后台接口补全技术方案

> 背景：`frontend/admin/UI_DESIGN.md` §9 列出了运营后台 UI 落地所需、但 `cmd/admin`（:8081）目前缺失的接口（G0–G9）。本文把这些缺口写成可直接实现的接口规格。
>
> 现状基线：路由全部在 `internal/app/admin.go` 的 `NewAdminRouter`；业务逻辑在 `internal/admin`、`internal/pricesync`。现有接口以"创建"为主，没有列表、没有编辑、没有统计。
>
> 本文所有新增接口与现有接口一样挂在 `httpx.RequireBearerToken(AdminToken)` 分组内。

---

## 0. 通用约定（仅对新增/改造的接口生效）

### 0.1 JSON 字段命名：统一 snake_case（G8）

现状：`internal/admin` 的大部分结构体（`Account`、`Provider`、`VirtualModel`、`Channel`、`APIKey`……）以及 `pricesync.ChangeRequestSummary`、`PendingListingSummary`、`ComponentDiff` **没有 json tag**，响应是 `ID`/`CreditLimit` 这类 PascalCase；而 handler 自己声明的 body（`grantCreditRequest` 等）是 snake_case。请求体因为 `DisallowUnknownFields` + 大小写不敏感匹配，出现了"`CreditLimit` 能用、`credit_limit` 报 400"的情况。

方案：

1. **一次性给 `internal/admin` 的结构体补齐 snake_case json tag**，请求与响应同时切换。`internal/pricesync` 的 `PriceSpec`/`Component`/`ComponentDiff` 以及 `wallet.Receipt` **不加 tag**：前三者同时被序列化进 `proposed_spec`、`diff`、`observed_spec` 等 JSONB 列，历史数据是 PascalCase 键，加 tag 后读取历史行时 `UnitPrice`、`TierMinInput` 这类多词字段会静默丢失；`Receipt` 属于计费主链路。这些类型改为在 `internal/app/admin_dto.go` 里定义响应 DTO。
2. 这是破坏性变更，唯一的现存调用方是 `frontend/test_web/admin.html`，**同一个 PR 内同步修改**它（`admin.html:201-203, 346-373, 384-388` 等处）。`frontend/admin` 尚未开工，没有兼容负担，趁现在做成本最低。
3. 新增接口一律在 `internal/app` 定义响应 DTO，不直接序列化内部结构体，避免以后内部字段改名泄露到接口。
4. `price_change_requests.diff` 列里已经存量写入的 `ComponentDiff` 是 PascalCase JSON。读取时由 DTO 层做转换，**不迁移历史数据**。

### 0.2 分页

| 类型 | 适用 | 请求参数 | 响应 |
|---|---|---|---|
| 页码分页 | 配置类列表（供应商、渠道、模型、账户…），数据量小、需要总数和跳页 | `page`（默认 1）、`page_size`（默认 20，最大 100） | `{"data":[…], "total":123, "page":1, "page_size":20}` |
| 游标分页 | 追加型数据（调用日志、资金流水、审计日志） | `before`（不透明游标）、`limit`（默认 50，最大 100） | `{"data":[…], "next_cursor":"…"}`，没有下一页时为 `""` |

游标格式沿用 `internal/console/usage.go` 的 `encodeLogsCursor`（`<RFC3339Nano>|<id>`），前端只当不透明字符串使用。

### 0.3 排序与过滤

- 排序：`sort=field` 升序、`sort=-field` 降序；每个接口声明白名单，不在白名单内返回 400。
- 模糊搜索：统一用 `q` 参数，后端 `ILIKE '%q%'`；`q` 为纯数字时额外匹配 ID 精确值。
- 多值过滤：逗号分隔，如 `status=pending,blocked`。
- 时间：`from` / `to` 为 RFC3339 或 `YYYY-MM-DD`（按 UTC 解释）；区间语义与 `/console/usage` 一致（`to` 为日期时包含当天）。

### 0.4 金额与价格

- 余额/消费等**金额**：`int64` 微元，字段名统一加后缀 `_micro`（如 `cash_balance_micro`），与 web 侧 `/v1/usage`、`/console/*` 保持一致。
- **单价**（`price_components.unit_price` 是 `NUMERIC(20,10)`）：以十进制字符串返回（如 `"0.27"`），并带 `currency`，与 `/v1/catalog` 的 `sell_price` 同构。
- **比率**（毛利率、变化率）：十进制字符串，`"0.3610"` 表示 36.10%。

### 0.5 错误

沿用 `httpx.WriteError` 和 `writeAdminError` 的映射，新增两条：

| 情况 | 状态码 | code |
|---|---|---|
| `sort` / 过滤参数不合法、时间窗超限 | 400 | `invalid_request` |
| 唯一约束冲突（如 `providers.code` 重复）、人工调账 `ref_id` 重复 | **409** | `conflict` |

第二条是修正：目前唯一约束冲突落进默认分支返回 400。前端需要区分"重复提交"（409）和"参数错误"（400），才能给出"该单号已调过账"这类准确提示。实现上在 `writeAdminError` 里识别 pgx 的 `23505` 错误码，以及 `wallet.ErrDuplicateAdjustRef`。

> **实现时发现的问题（已修复）**：原以为 `ledger_entries` 的 `UNIQUE (ref_type, ref_id, type, balance_kind, grant_id)` 能防止重复调账，实际上人工调账的 `grant_id` 为 NULL，Postgres 把每个 NULL 视为互不相同，**同一个 `ref_id` 提交两次会重复入账**。又因为 `ledger_entries` 只追加、禁止删除，历史上一旦出现重复就无法再建唯一索引。修复方式：`wallet.Adjust` 在事务内先取 `(account_id, ref_id)` 的 advisory lock，再检查是否已存在同账户同 `ref_id` 的 `adjust` 流水，存在则返回 `ErrDuplicateAdjustRef`；配套普通部分索引 `idx_ledger_entries_admin_ref`。幂等范围是"同一账户"，同一个工单号给多个账户分别调账仍然允许。

### 0.6 操作人与审计

现状：

- `X-Actor-ID` 只接受数字（`actorIDFromRequest` 用 `ParseInt`），而后台目前没有管理员账号表，运营无从得知自己的"数字 ID"，导致审计日志里的操作人实际上全是 0。
- 只有 7 类操作写审计（`wallet.adjust`、`wallet.credit_grant`、`provider_key.add`、`virtual_model_metadata.set`、`sell_price.set`、`cost_price.set`、`fx_rate.set`）。**调价审批/驳回、API Key 吊销、待上架发布/忽略**这些高价值操作都没有审计。
- 所有审计记录的 `before` 都是 `nil`，UI 无法展示"改了什么"。

方案（在 RBAC 落地前的过渡，不替代 RBAC）：

1. 新增请求头 `X-Actor-Name`（URL 编码的 UTF-8 字符串，≤64 字符），写入 `admin_audit_logs.actor_name` 新列（见 §11 迁移）。`X-Actor-ID` 保持原样，RBAC 上线后两者都改为由服务端从鉴权上下文填写。
2. **所有写接口都必须审计**；本文新增的每个写接口在规格里都标了 `action` 名。
3. 编辑类接口（§2 的 PATCH）必须在同一事务里 `SELECT … FOR UPDATE` 取出变更前的行，作为 `before` 写入审计。

---

## 1. G0 列表与详情接口

### 1.1 供应商

**`GET /providers`**

| 参数 | 说明 |
|---|---|
| `q` | 匹配 `code` / `name` |
| `status` | `active` / `disabled` |
| `protocol` | `openai` / `anthropic` / `gemini` |
| `sort` | `code`（默认）、`-channel_count` |
| `page` / `page_size` | |

响应 `data[]`：

```json
{
  "id": 3, "code": "deepseek", "name": "DeepSeek", "protocol": "openai",
  "currency": "CNY", "status": "active",
  "account_count": 2, "active_key_count": 5, "channel_count": 14,
  "pending_listing_count": 1
}
```

计数用 `LEFT JOIN LATERAL (SELECT count(*) …)`；供应商量级在百以内，不需要冗余计数列。

**`GET /providers/{id}`**：返回上面的对象，外加 `accounts[]`（结构同 §1.2 列表行）和 `price_sources[]`（结构同 §1.6）。

### 1.2 上游账号与上游密钥

**`GET /provider-accounts?provider_id=&status=&q=`**（页码分页）

```json
{
  "id": 7, "provider_id": 3, "provider_code": "deepseek",
  "name": "ds-main", "base_url": "https://api.deepseek.com/v1", "region": null,
  "cost_multiplier": "1.0000", "status": "active",
  "key_count": 3, "active_key_count": 2, "channel_count": 9
}
```

**`GET /provider-accounts/{id}`**：上面的对象 + `keys[]`：

```json
{
  "id": 21, "last4": "a9f2", "weight": 100, "status": "active",
  "disabled_reason": null, "rpm_limit": null, "tpm_limit": null,
  "concurrency_limit": null, "created_at": "2026-09-01T08:00:00Z"
}
```

密钥明文和密文**永远不返回**，只有 `last4`。

### 1.3 虚拟模型

**`GET /virtual-models`**

兼容：请求带 `name` 参数时保持现有行为（精确查找，返回单个对象或 404），`test_web/admin.html` 的幂等导入逻辑依赖这一点。不带 `name` 时返回列表。

| 参数 | 说明 |
|---|---|
| `q` | 匹配 `name`、`aliases`、`virtual_model_metadata.display_name` |
| `status` | `active,hidden,deprecated` |
| `type` / `family` | 精确匹配 |
| `tier` | 该 tier 在 `visible_tiers` 内 |
| `missing` | `sell_price`：没有生效售价；`metadata`：没有元数据行；`channel`：没有 active 渠道 |
| `sort` | `name`（默认）、`id`、`channel_count`、`min_margin_ratio`（均可加 `-` 降序） |

响应 `data[]`：

```json
{
  "id": 12, "name": "deepseek-ai/DeepSeek-V4-Flash", "family": "deepseek",
  "type": "chat", "status": "active",
  "context_window": 128000, "max_output": 8192,
  "capabilities": ["stream", "tools"], "visible_tiers": ["free", "pro", "enterprise"],
  "aliases": [],
  "display_name": "DeepSeek V4 Flash",
  "has_metadata": true,
  "channel_count": 2, "active_channel_count": 2,
  "sell_price": {"price_book_id": 88, "currency": "CNY", "input": "1.5", "output": "3", "effective_from": "..."},
  "min_margin_ratio": "0.3610"
}
```

- `sell_price` 是**当前生效的售价**，只取基础 `input`/`output`（`per_1m_tokens`、`service_tier=default`、`tier_min_input=0`、无时段窗口），用于列表展示；完整价格在详情接口。

> **实现时发现的问题（未修改运行时，待产品决策）**：`price_books.tier` 在运行时**不参与计价**。`internal/catalog` 按 `virtual_model_id` 取 `effective_from` 最新的一本售价，不看 `tier`；`POST /virtual-models/{id}/sell-price` 的 `tier` 字段只是被存下来。也就是说"分档售价"目前并不存在：给 `pro` 发布一个新售价，会立刻覆盖所有用户的售价。因此本接口与 `frontend/admin` 都按"每个模型只有一个当前售价"展示，**不提供按 tier 编辑售价的入口**，`tier` 只作为历史版本上的信息字段显示。若确需分档定价，需要先改 `internal/catalog` 与 `internal/relay` 的选价逻辑。
- `min_margin_ratio` 的计算口径见 §1.4 的"毛利率口径"，取该模型所有 active 渠道中最低的一个；没有成本价或售价时为 `null`。

**`GET /virtual-models/{id}`**：

```json
{
  "...": "列表行的全部字段",
  "metadata": {"display_name": "...", "description": "...", "provider_display": "...",
               "tags": ["reasoning"], "scores": {"intelligenceIndex": 39.5}, "updated_at": "..."},
  "sell_price_book": {"id": 88, "kind": "sell", "tier": null, "currency": "CNY", "effective_from": "...",
     "effective_to": null, "created_by": null, "note": null, "created_at": "...", "is_current": true,
     "components": [{"meter": "input", "unit": "per_1m_tokens", "service_tier": "default",
                     "tier_min_input": 0, "tier_max_input": null,
                     "window_start_min": null, "window_end_min": null, "unit_price": "1.5"}]},
  "channels": ["§1.4 列表行结构"]
}
```

`metadata` 没有录入时为 `null`（与 `/v1/catalog` "字段缺失"的语义对应）。

**`GET /virtual-models/{id}/price-books?limit=`**：售价历史版本，按 `effective_from DESC`，结构同上面的 `sell_price_book`；`is_current` 标出当前生效的那一本。渠道成本价历史对应 **`GET /channels/{id}/price-books?limit=`**。

### 1.4 渠道

**`GET /channels`**

兼容：`virtual_model_id` + `provider_account_id` + `upstream_model` **三个参数同时出现**时保持现有行为（精确查找返回单个对象）。否则返回列表。

| 参数 | 说明 |
|---|---|
| `virtual_model_id` / `provider_account_id` / `provider_id` | 精确过滤 |
| `status` | `active` / `disabled` |
| `q` | 匹配虚拟模型名、`upstream_model` |
| `margin` | `negative`：毛利率 < 0 |
| `missing_cost` | `true`：没有生效成本价 |
| `dedicated` | `true`：`allowed_account_ids` 非空（专属渠道） |
| `sort` | `id`（默认）、`priority`、`weight`、`margin_ratio`（均可加 `-` 降序；无法计算毛利的排在最后） |

响应 `data[]`：

```json
{
  "id": 41, "status": "active",
  "virtual_model_id": 12, "virtual_model_name": "deepseek-ai/DeepSeek-V4-Flash",
  "provider_account_id": 7, "provider_account_name": "ds-main",
  "provider_id": 3, "provider_code": "deepseek",
  "upstream_model": "deepseek-chat",
  "priority": 10, "weight": 100,
  "allowed_tiers": null, "allowed_account_ids": null,
  "experiment_key": null, "variant_label": null,
  "cost_multiplier": "1.2",
  "cost_price": {"price_book_id": 90, "currency": "USD", "input": "0.27", "output": "1.10", "effective_from": "..."},
  "cost_price_cny": {"input": "1.9440", "output": "7.92", "fx_rate": "7.2", "fx_date": "2026-09-26", "fx_missing": false},
  "sell_price": {"price_book_id": 88, "currency": "CNY", "input": "3", "output": "12", "effective_from": "..."},
  "margin_ratio": "0.352",
  "pending_change_request_id": 88
}
```

**毛利率口径**（`margin_ratio`、`min_margin_ratio` 共用，写成一个 SQL 函数或 Go 函数，别在多处各算各的）：

- 成本 = 渠道当前生效成本价 × `provider_accounts.cost_multiplier`，非 CNY 时按 `fx_rates` 中 `effective_date <= today` 的最近一条汇率折算。
- 售价 = 虚拟模型当前生效售价（不区分 tier，见 §1.3 的说明）。
- 分别计算 `input`、`output` 两个计量项的 `1 - 成本/售价`，**取较小值**（保守口径，任一项亏钱都要暴露）。
- 任一方缺失时为 `null`；`pending_change_request_id` 是该渠道当前 `pending`/`blocked` 的调价申请 ID，没有为 `null`。

**`GET /channels/{id}`**：上面的对象 +

- `cost_price_history[]`：成本价历史版本（同 §1.3 price-books 结构）；
- `recent_observations[]`：最近 20 条价格观测（`id, source_id, source_level, observed_at, spec`，不含 `raw_object`）；
- `change_requests[]`：该渠道最近 20 条调价申请（§5.1 列表行结构）。

### 1.5 汇率

**`GET /fx-rates?base=&quote=&limit=`**：按 `effective_date DESC`，默认 30 条。另加 **`GET /fx-rates/latest`**：每个 `(base, quote)` 取最新一条，给定价编辑器做折算。

### 1.6 价格源

**`GET /price-sources?provider_id=&enabled=`**：

```json
{"id": 5, "provider_id": 3, "level": "L1", "kind": "api", "fetcher": "deepseek_api",
 "url": "https://…", "schedule": "0 */6 * * *", "enabled": true,
 "last_success_at": "2026-09-27T06:00:00Z", "observation_count_7d": 28}
```

---

## 2. G5 编辑与状态变更接口

统一语义：

- `PATCH`，body 只包含要改的字段（Go 侧用指针字段区分"未传"和"传了零值"），`DisallowUnknownFields` 继续保留。
- 同一事务内 `SELECT … FOR UPDATE` 读取变更前的行 → 更新 → 返回更新后的完整对象（结构同对应的详情/列表行）。
- 审计：`before` / `after` 只包含本次改动的字段。
- **价格不走 PATCH**：价格版本化要求历史不可改（`internal/admin` 包文档已有说明），改价继续用现有的 `POST …/sell-price`、`POST …/cost-price` 追加新版本。

| 接口 | 可改字段 | 审计 action | 备注 |
|---|---|---|---|
| `PATCH /providers/{id}` | `name`, `status` | `provider.update` | 停用供应商**不级联**停用其账号/渠道，响应里附带 `affected_active_channels` 数量，前端据此提示 |
| `PATCH /provider-accounts/{id}` | `name`, `base_url`, `region`, `cost_multiplier`, `status` | `provider_account.update` | `cost_multiplier` 变化会影响所有下属渠道的毛利率 |
| `PATCH /provider-keys/{id}` | `weight`, `status`（仅 `active` ↔ `disabled`）, `disabled_reason`, `rpm_limit`, `tpm_limit`, `concurrency_limit` | `provider_key.update` | `exhausted` 由系统设置，人工只能把它改回 `active` |
| `POST /provider-keys/{id}/revoke` | — | `provider_key.revoke` | 不可逆，状态改为 `revoked`；同时清除 Redis 中的冷却记录 |
| `PATCH /virtual-models/{id}` | `status`, `visible_tiers`, `capabilities`, `context_window`, `max_output`, `aliases` | `virtual_model.update` | `name` 不可改（是对外 API 的模型 ID，改名等于下线旧模型）。改为 `hidden`/`deprecated` 后何时从 `/v1/catalog` 消失，取决于 gateway 加载目录快照的方式，实现时需确认并在响应中说明 |
| `PATCH /channels/{id}` | `priority`, `weight`, `status`, `allowed_tiers`, `allowed_account_ids`, `param_overrides` | `channel.update` | 停用某模型的**最后一个** active 渠道时返回 409，除非 body 带 `"force": true`——防止误操作导致模型不可用 |
| `PATCH /accounts/{id}` | `name`, `status`, `tier`, `credit_limit` | `account.update` | `tier` 取值限定 `free` / `pro` / `enterprise` |
| `PATCH /price-sources/{id}` | `enabled`, `url`, `schedule`, `config` | `price_source.update` | |

同时**给现有写接口补审计**：

| 现有接口 | 补充的 action |
|---|---|
| `POST /accounts` | `account.create` |
| `POST /accounts/{id}/api-keys` | `api_key.create`（`after` 不含 `RawKey`） |
| `POST /api-keys/{id}/revoke` | `api_key.revoke` |
| `POST /providers`、`/provider-accounts`、`/virtual-models`、`/channels`、`/price-sources` | `*.create` |
| `POST /price-change-requests/{id}/approve` / `reject` | `price_change.approve` / `price_change.reject` |
| `POST /pending-model-listings/{id}/publish` / `dismiss` | `listing.publish` / `listing.dismiss` |

---

## 3. G1 统计接口

数据源：`request_logs`（按天分区）。当前只有 `(account_id, created_at DESC)` 一个索引，全局按模型/渠道聚合需要新增索引（§11）。

### 3.1 统计口径（所有统计接口共用，写成一个 Go 包 `internal/stats`）

| 指标 | 字段 | 口径 |
|---|---|---|
| 请求数 | `requests` | `count(*)`，含失败 |
| 成功数 | `success` | `status = 'success'` |
| 错误率 | `error_rate` | `1 - success / requests` |
| tokens | `input_tokens`, `output_tokens`, `cache_read_tokens`, `reasoning_tokens` | 仅成功请求求和 |
| 收入 | `revenue_micro` | `sum(charged_amount)` |
| 原价收入 | `list_amount_micro` | `sum(list_amount)`；与收入之差 = 促销让利 |
| 成本 | `cost_micro` | `sum(cost_amount)` |
| 毛利 | `gross_profit_micro` | `revenue - cost` |
| 毛利率 | `gross_margin` | `gross_profit / revenue`，收入为 0 时 `null` |
| 延迟 | `p50_latency_ms`, `p95_latency_ms` | 仅成功请求，`percentile_cont` |
| 首字延迟 | `p95_ttft_ms` | 仅成功的流式请求 |
| 估算占比 | `estimated_ratio` | `usage_source = 'estimated'` 的请求占比，过高说明大量客户端中途断开 |
| 活跃账户 | `active_accounts` | `count(DISTINCT account_id)` |

与 `/console/usage` 的差异要写进代码注释：console 只统计 `status='success'`，这里 `requests` 含失败（运营需要看错误率）。`internal/console.UsageInterval` 后续可改为调用 `internal/stats`，保证两边口径不漂移。

### 3.2 `GET /stats/overview?from=&to=`

工作台 KPI。自动计算"上一个等长时间窗"作为对比。

```json
{
  "from": "2026-09-20T00:00:00Z", "to": "2026-09-27T00:00:00Z",
  "current":  {"requests": 1240000, "error_rate": "0.0080", "revenue_micro": 12340000000,
               "cost_micro": 7880000000, "gross_margin": "0.3610", "p95_latency_ms": 2100,
               "active_accounts": 342, "estimated_ratio": "0.0120"},
  "previous": {"...": "同结构"}
}
```

### 3.3 `GET /stats/usage`

工作台趋势图、用量分析页、各详情页"用量趋势"共用。

| 参数 | 说明 |
|---|---|
| `from` / `to` | 必填。`interval=hour` 时最长 7 天，`interval=day` 时最长 90 天 |
| `interval` | `hour` / `day`（默认）/ `none`（不分时间桶，只按维度汇总，用于排行表） |
| `group_by` | `none`（默认）/ `virtual_model` / `channel` / `provider` / `account` / `api_key` |
| `top` | 按 `order_by` 取前 N 个分组（默认 8，最大 50），其余合并为 `"__other__"` |
| `order_by` | 排名指标，默认 `revenue_micro` |
| `virtual_model` / `channel_id` / `provider_id` / `account_id` / `api_key_id` | 过滤，可组合 |

```json
{
  "interval": "day", "group_by": "virtual_model",
  "groups": [
    {"key": "deepseek-ai/DeepSeek-V4-Flash", "label": "DeepSeek V4 Flash",
     "totals": {"requests": 520000, "revenue_micro": 3412000000, "...": "…§3.1 全部指标"}}
  ],
  "series": [
    {"bucket": "2026-09-26", "group": "deepseek-ai/DeepSeek-V4-Flash", "requests": 74000, "revenue_micro": 488000000, "...": "…"}
  ]
}
```

- `group_by=provider` 需要 `channel_id → channels → provider_accounts → providers` 的关联；为避免大表 JOIN，先按 `channel_id` 聚合，再在 Go 里用渠道 → 供应商映射二次汇总。
- `label` 给前端直接显示：模型取 `display_name`，渠道取 `provider_account_name/upstream_model`，账户取 `name`。
- 百分位指标在 `interval=hour` 且 `group_by≠none` 时开销较大；**先实现、压测，超出 2s 再引入下面的汇总表**。

### 3.4 性能分期

| 阶段 | 做法 |
|---|---|
| A（随本方案上线） | 直接查 `request_logs`，依靠 `created_at` 分区裁剪 + §11 新增索引；强制时间窗上限；响应加 `Cache-Control: private, max-age=60`，并在进程内按参数缓存 60s |
| B（数据量上来后） | 新增 `usage_rollup_hourly`（小时 × account_id × api_key_id × virtual_model × channel_id，存计数与金额求和），由 worker 每 5 分钟增量汇总最近 2 小时；除百分位外的指标全部改查汇总表。百分位仍查原表，并限制时间窗在 7 天内 |
| C（长期） | 走 `internal/chsync` 同步到 ClickHouse（目前只有骨架），统计接口换数据源，接口契约不变 |

---

## 4. G2 账户、资金与 API Key

### 4.1 `GET /accounts`

| 参数 | 说明 |
|---|---|
| `q` | 纯数字 → 匹配 `accounts.id`；含 `@` → 匹配 owner 用户的 `users.email`；否则匹配 `accounts.name` |
| `status` / `tier` / `type` | 精确过滤 |
| `sort` | `-created_at`（默认）、`-cash_balance`、`-last_active_at` |

```json
{
  "id": 1234, "type": "organization", "name": "张三的团队", "status": "active",
  "tier": "pro", "credit_limit_micro": 0, "created_at": "...",
  "owner_email": "zhang@example.com",
  "cash_balance_micro": 23500000, "bonus_balance_micro": 0, "frozen_micro": 0,
  "active_key_count": 3, "last_active_at": "2026-09-27T09:12:00Z"
}
```

`last_active_at` 取该账户 API Key 的 `max(last_used_at)`，不去扫 `request_logs`。

### 4.2 `GET /accounts/{id}`（扩展现有接口）

在现有 `{account, wallet}` 基础上增加：

- `members[]`：`{user_id, email, email_verified, role, created_at}`；
- `active_grants_summary`：`{count, remaining_micro, nearest_expires_at}`。

字段按 §0.1 统一为 snake_case。

### 4.3 `GET /accounts/{id}/ledger`

游标分页（`(created_at, id)`），过滤 `type`、`balance_kind`、`from`/`to`。

```json
{"id": 9912, "type": "adjust", "amount_micro": 100000000, "balance_kind": "cash",
 "cash_after_micro": 123500000, "bonus_after_micro": 0,
 "ref_type": "admin", "ref_id": "TICKET-5521", "grant_id": null, "created_at": "..."}
```

`ref_type=request` 的行前端可跳转到调用日志详情。

### 4.4 `GET /accounts/{id}/credit-grants?active=true`

返回 `{id, source, promotion_id, amount_micro, remaining_micro, model_scope, expires_at, created_at}`。

### 4.5 `GET /accounts/{id}/usage`

参数与响应同 §3.3，服务端固定 `account_id={id}`。

### 4.6 `GET /api-keys`（全局检索）

参数：`q`（匹配 `name`、`display_prefix`；运营拿到用户发来的 `sk-uft-xxxx` 前缀即可定位）、`account_id`、`status`；页码分页。响应在现有 `APIKey` 字段基础上增加 `account_name`、`last_used_at`、`expires_at`、`budget_limit_micro`、`budget_period`。

### 4.7 改造 `POST /accounts/{id}/wallet/adjust`

- 新增**必填** `reason`（≤200 字），写入审计 `after`。
- 新增可选 `expected_cash_balance_micro`：传了就在事务内校验当前现金余额与之相等，不等返回 409 `balance_changed`。防止运营照着一份过时的余额页面做调账。
- 人工扣减不允许把现金余额扣成负数（400）。
- 实现：`wallet.AdjustChecked`（`wallet.Adjust` 保持原签名，内部调用它），在同一事务内完成"advisory lock → 重复单号检查 → `SELECT … FOR UPDATE` 锁钱包并核对余额 → 更新 → 写流水"。
- 同一账户重复 `ref_id` 返回 409（`wallet.ErrDuplicateAdjustRef`，见 §0.5 的说明），防止重复提交。
- 审计 `before` 写入调账前的钱包快照。

`POST /accounts/{id}/credit-grants` 同样新增必填 `reason`。

---

## 5. G4 调价审批与待上架

### 5.1 `GET /price-change-requests`（扩展现有接口）

| 参数 | 说明 |
|---|---|
| `status` | 默认 `pending,blocked`（与现状一致）；`all` 表示全部 |
| `direction` / `channel_id` / `provider_id` | 过滤 |
| `sort` | `created_at`（默认，最早的优先处理）、`-max_change_ratio` |
| `page` / `page_size` | **新增**：历史 tab 需要分页；不传时保持"返回全部待处理项"的现有行为 |

响应 key 从 `change_requests` 改为统一的 `data`（同时属于 §0.1 的破坏性变更）。每行在现有字段上增加：

```json
{
  "id": 88, "status": "pending", "direction": "up", "max_change_ratio": "0.1820",
  "effective_from": "...", "created_at": "...",
  "channel_id": 41, "virtual_model_name": "deepseek-ai/DeepSeek-V4-Flash",
  "provider_code": "deepseek", "provider_account_name": "ds-main", "upstream_model": "deepseek-chat",
  "issue_count": 1, "blocked_reason": null,
  "decided_by": null, "decided_by_name": null, "decided_at": null, "decision_reason": null
}
```

`blocked_reason` 取 `diff.issues` 中导致拦截的那条告警的文案，列表页直接展示"为什么被拦截"。

### 5.2 `GET /price-change-requests/{id}`（新增）

数据库已有全部原始数据（`current_book_id`、`proposed_spec`、`diff`、`evidence`），只是没有接口读出来。

```json
{
  "...": "§5.1 列表行全部字段",
  "currency": "USD",
  "components": [
    {"meter": "input", "unit": "per_1m_tokens", "service_tier": "default", "tier_min_input": 0,
     "old_price": "0.27", "new_price": "0.32", "change_ratio": "0.1852"},
    {"meter": "output", "unit": "per_1m_tokens", "service_tier": "default", "tier_min_input": 0,
     "old_price": "1.10", "new_price": "1.30", "change_ratio": "0.1818"}
  ],
  "issues": [{"rule": "cross_source_conflict", "severity": "force_review", "message": "…"}],
  "proposed_spec": {"currency": "USD", "components": ["…"], "effective_from": null, "expires_at": null},
  "current_book": {"id": 90, "…": "同 §1.3 price book 结构"},
  "evidence": [
    {"observation_id": 5521, "source_id": 5, "source_level": "L1", "source_kind": "api",
     "source_url": "https://…", "observed_at": "...", "raw_excerpt": "前 2KB 原文"}
  ],
  "impact": {
    "window_days": 7,
    "cost_before_micro": 1230000000, "cost_after_micro": 1455000000, "cost_delta_micro": 225000000,
    "sell_price": {"price_book_id": 88, "currency": "CNY", "input": "3", "output": "12"},
    "margin_before": "0.38", "margin_after": "0.27", "fx_missing": false
  }
}
```

- `components` 来自 `diff` 列（读取时把历史的 PascalCase 转为 snake_case）。
- `impact` **在读取时计算**：取该渠道近 7 天成功请求的各计量 token 总量，分别乘以新旧价格（含 `cost_multiplier` 与汇率）。`price_change_requests.impact_7d` 列目前恒为 NULL；本接口可以顺带回填，但以实时计算结果为准。
- 成本估算只计入基础计量项（`input` / `output` / `input_cache_read` / `input_cache_write` 的 `per_1m_tokens` 默认档），分档、时段价与 reasoning 不计入——这是审批参考值，不是账单。
- `margin_before` / `margin_after` 使用 §1.4 的毛利率口径，对比对象是当前生效售价（不区分 tier）。

### 5.3 改造 approve / reject

- body 新增可选 `reason`（reject 时前端要求必填），写入新列 `decision_reason`（§11）。
- `decided_by` 保留在 body 中以兼容，但**优先使用 `X-Actor-ID`**；`decided_by_name` 取 `X-Actor-Name`。
- 补审计（§2 末表）。
- 批准 `blocked` 项时，body 必须带 `"confirm_blocked": true`，否则返回 409——把 UI 上的"输入确认"在服务端再兜一层。

### 5.4 `POST /price-change-requests/batch-approve`（新增）

```json
{"ids": [88, 87, 85], "reason": "例行小幅调价", "max_abs_change_ratio": "0.10"}
```

- 服务端逐条校验：必须是 `pending`（**不允许**批量批准 `blocked`）、`|max_change_ratio| ≤ max_abs_change_ratio`（上限 0.2）。
- 逐条独立事务，返回每条的结果，部分失败不回滚已成功的：

```json
{"results": [
  {"id": 88, "ok": true,  "applied_book_id": 131},
  {"id": 85, "ok": false, "error": {"code": "conflict", "message": "change request is blocked"}}
]}
```

### 5.5 待上架模型

- `GET /pending-model-listings` 增加参数 `status`（默认 `pending`）、`provider_id`、分页；响应 key 改为 `data`；每行增加 `provider_code`、`provider_name`、`source_level`、`published_virtual_model_id`、`decided_at`。
- `ObservedSpec` 按 §0.1 输出为 snake_case 的 `observed_spec`，并额外给出解析好的 `suggested`：`{name, family, currency, input_price, output_price}`，供上架表单预填。`family` 的推断规则放在后端（取上游模型名去掉组织前缀后的第一个词，如 `Qwen/Qwen3-8B` → `qwen3`；取不到用供应商 code），避免前后端各写一套。价格观测里没有上下文窗口和最大输出，这两项由运营在上架表单里填写。
- publish / dismiss 补审计；dismiss 增加可选 `reason`。

> **实现时发现的问题（已修复）**：`pricesync.PublishListing` 原来直接用"观测单价 × (1 + sell_markup)"作为**人民币**售价发布，既不做汇率换算、也不乘上游账号的 `cost_multiplier`——USD 报价的模型按常规 30% 加价上架会直接低于成本。现改为：售价(CNY) = 观测单价 × 汇率（quote=CNY、effective_date <= 今天的最新一条）× `cost_multiplier` × (1 + `sell_markup`)，四舍五入到 6 位小数；非 CNY 且没有汇率时返回 400 `pricesync: no CNY exchange rate…`，并且汇率与账号检查放在创建任何对象之前，不会留下半上架的虚拟模型。

---

## 6. G3 全局调用日志

### 6.1 `GET /request-logs`

| 参数 | 说明 |
|---|---|
| `from` / `to` | 默认最近 24 小时；**不带 `account_id` 时时间窗最长 7 天，带 `account_id` 时最长 30 天**（前者只能靠分区裁剪 + 新索引，后者可走现有账户索引） |
| `account_id` / `api_key_id` / `virtual_model` / `channel_id` / `provider_key_id` | 精确过滤 |
| `status` | `success` / `upstream_error` / … |
| `error_code` / `http_status` | 精确过滤 |
| `min_latency_ms` | 慢请求排查 |
| `usage_source` | `estimated` 等 |
| `request_id` | 精确定位（忽略其它过滤） |
| `before` / `limit` | 游标分页，同 `/console/logs` |

响应 `data[]`：在 `console.LogEntry` 字段基础上增加 `account_id`、`channel_id`、`provider_key_id`、`endpoint`、`is_stream`、`error_code`、`attempts`、`ttft_ms`、`cost_micro`、`list_amount_micro`。**不含** `attempt_trace`、`client_ip`、`user_agent`（留给详情，控制列表体积）。

实现：把 `internal/console.ListLogs` 的 keyset 查询抽到 `internal/reqlog`，console 固定 `account_id`，admin 放开过滤条件，两边共用。

### 6.2 `GET /request-logs/{request_id}?created_at=`

- `created_at` 可选；带上时能精确命中分区（前端从列表跳转时总是带上）。不带时在最近 30 天分区内查找。
- 返回完整行，包括：
  - `attempt_trace`：原样返回 JSONB，前端据此渲染重试时间线；
  - 价格：`sell_price_book_id`、`cost_price_book_id`、`promotion_ids`、`upstream_cost`、`fx_rate`；
  - tokens 分项；`client_ip`、`user_agent`；
  - 关联对象的展示名：`account_name`、`api_key_name`、`channel_label`、`provider_code`，省去前端多次请求。

---

## 7. G6 待办计数

### 7.1 `GET /todo-counts`

侧栏徽标与工作台待办条使用，前端每 60 秒轮询一次。

```json
{
  "price_changes_pending": 5, "price_changes_blocked": 2,
  "listings_pending": 3,
  "channels_negative_margin": 2, "channels_missing_cost": 5,
  "models_missing_sell_price": 1
}
```

前三项走现有 `status` 索引，是轻量查询。后三项需要毛利率计算，实现时单独缓存 5 分钟。

---

## 8. G7 审计日志

### 8.1 `GET /audit-logs`（扩展现有接口）

| 参数 | 说明 |
|---|---|
| `target_type` / `target_id` | 现有 |
| `actor_id` / `actor_name` | 新增 |
| `action` | 新增，前缀匹配（`price_change.` 匹配全部审批类操作） |
| `from` / `to` | 新增 |
| `before` / `limit` | 新增游标分页（`(created_at, id)`）；`limit` 语义不变，最大 500 |

响应 key 改为 `data` + `next_cursor`；每条增加 `actor_name`。

---

## 9. G8 JSON 命名统一

见 §0.1。需要补 tag 的结构体清单：

- `internal/admin`：`Account`、`WalletSummary`、`CreateAccountInput`、`APIKey`、`CreatedAPIKey`（`RawKey` → `raw_key`）、`Provider`、`CreateProviderInput`、`ProviderAccount`、`ProviderKeySummary`、`VirtualModel`、`CreateVirtualModelInput`、`Channel`、`CreateChannelInput`、`GrantedCredit`、`AuditLogEntry`、`UpstreamModel`；
- `internal/wallet`、`internal/pricesync`：**不加 tag**（原因见 §0.1），由 `internal/app/admin_dto.go` 的 DTO 转换：`Receipt`（调账回执，`amount_micro` 为调账方向，即 `-ChargedAmount`）、`IngestResult`、`UnmappedIngestResult`、`ChangeRequestSummary`、`PendingListingSummary`（含 `observed_spec`）、`PublishListingResult`。

金额字段按 §0.4 加 `_micro` 后缀（如 `CreditLimit` → `credit_limit_micro`）。

列表响应的键统一为 `data`：`/price-change-requests` 原 `change_requests`、`/pending-model-listings` 原 `pending_listings`、`/audit-logs` 原 `audit_logs`。

验收：`internal/app` 现有的 admin e2e 测试（`admin_gateway_e2e_test.go` 等）全部改为 snake_case 断言后通过；`test_web/admin.html` 四步联调手工走通。

---

## 10. G9 渠道实时健康

熔断器状态在 gateway 进程内存中，上游密钥冷却在 Redis 中（`internal/health`）。admin 进程读不到 gateway 的内存。

方案：gateway 每 15 秒把各渠道熔断状态快照写入 Redis（`uft:health:channel:<id>`，TTL 60 秒，内容 `{state, failures, opened_at}`）；admin 新增 **`GET /channels/health?ids=`**，直接读 Redis 快照与密钥冷却记录。多个 gateway 实例时每个实例分别写入 `uft:health:channel:<id>:<instance>`，admin 汇总后返回"最差"状态。

优先级最低：在它实现之前，UI 用 §3 的错误率代替实时健康状态。

---

## 11. 数据库迁移（`00015_admin_console.sql`）

```sql
-- +goose Up
-- 全局统计与调用日志检索（§3、§6）：request_logs 只有 (account_id, created_at) 索引。
-- 在分区父表上建索引会自动下发到现有和未来的分区。
CREATE INDEX idx_request_logs_model_time   ON request_logs (virtual_model, created_at DESC);
CREATE INDEX idx_request_logs_channel_time ON request_logs (channel_id, created_at DESC);
CREATE INDEX idx_request_logs_status_time  ON request_logs (created_at DESC) WHERE status <> 'success';

-- 操作人名称（§0.6，RBAC 落地前的过渡）
ALTER TABLE admin_audit_logs ADD COLUMN actor_name TEXT;
CREATE INDEX idx_admin_audit_logs_time ON admin_audit_logs (created_at DESC, id DESC);

-- 审批理由（§5.3）
ALTER TABLE price_change_requests ADD COLUMN decision_reason TEXT;
ALTER TABLE price_change_requests ADD COLUMN decided_by_name TEXT;

-- 账户检索（§4.1）；00001_extensions.sql 只启用了 citext、pgcrypto
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_accounts_name_trgm ON accounts USING gin (name gin_trgm_ops);
```

- `pg_trgm` 需要数据库账号有建扩展的权限；托管数据库上如不允许，先改用普通 `ILIKE` 顺序扫描（账户量级在十万以内时可接受）。
- 在已有大量数据的分区表上建索引会锁表。生产环境需要按分区分别 `CREATE INDEX CONCURRENTLY` 后再挂到父索引上，这一步写进上线手册，不放进 goose 迁移。

---

## 12. 实施顺序

与 `frontend/admin/UI_DESIGN.md` §10 的前端阶段对齐：

| 后端批次 | 内容 | 解锁的前端阶段 |
|---|---|---|
| **B1**（已完成） | §0（snake_case、409 映射、`X-Actor-Name`、补审计）+ §9 + §8 审计日志过滤/游标 + §5.3 审批理由与 `confirm_blocked` + 迁移 00015 | 前端 P1 能以统一字段开工 |
| **B2**（已完成） | §1 全部列表/详情 + §2 编辑接口（含 `PATCH /accounts/{id}`）+ §5 调价/待上架增强 + §7 待办计数 | 前端 P1、P2 |
| **B3**（已完成） | §4 账户/流水/Key 检索 + 调账改造 | 前端 P3 |
| **B4**（已完成） | §3 统计（阶段 A，含 60 秒进程内缓存）+ §6 全局调用日志 + 迁移 00015 中的 `request_logs` 索引 | 前端 P4 |
| **B5** | §10 渠道实时健康、§3.4 阶段 B 汇总表 | 按需 |

每个批次的测试要求沿用现有 `internal/app/*_test.go` 的 e2e 风格：

- 列表接口：覆盖分页边界、每个过滤参数、非法 `sort` 返回 400。
- 写接口：断言审计日志的 `before`/`after` 内容。
- 统计接口：用固定的 `request_logs` fixture 断言每个指标的计算结果，**把 §3.1 的口径固化为测试**。
