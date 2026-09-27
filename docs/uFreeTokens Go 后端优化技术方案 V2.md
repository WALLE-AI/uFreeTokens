# uFreeTokens：AI 多 Provider API 平台 Go 后端优化技术方案（V2.0）

> 基于《AI 多 Provider API 平台计费与模型路由技术执行方案 V1.0》的深度审阅与重构
>
> 后端语言：**Go 1.23+**
>
> 目标规模：40+ Provider、100+ 虚拟模型、1000+ 用户（可平滑扩展到 10 万级）、多租户、OpenAI 兼容 API

---

## 0. 结论先行

V1 的**业务抽象方向是对的**：Provider → Provider Key → Virtual Model → User Key 四层解耦、价格/促销配置化、Request Session 全链路追踪。但它还停留在"概念图"层面，**直接照做会在上线后出资金事故**。最关键的 6 个问题：

| # | 问题 | 后果 |
|---|------|------|
| 1 | **计费是"先调用、后扣费"，只做余额 > 0 检查，没有预扣/冻结** | 并发请求、长流式请求会把余额扣成大额负数，平台直接亏钱 |
| 2 | **金额精度、币种、舍入策略未定义** | 浮点误差累积、USD 采购 / CNY 售卖无法对账 |
| 3 | **"不做本地估算"在流式场景不成立** | 客户端断开、上游不回 usage 时，请求变成"免费"，被恶意利用 |
| 4 | **计价模型只有 input/output 两个价** | 无法表达缓存命中价、推理 token、图片/音频、按上下文长度分档、按次计费，毛利算错 |
| 5 | **路由打分公式未归一化且"选最高分"** | 流量全部压到一个渠道（羊群效应），触发 429 后雪崩 |
| 6 | **12 个微服务 vs 1000 用户** | 严重过度设计；分布式事务让钱包/计费更难做对 |

**V2 的核心调整：**

1. **架构**：12 个微服务 → **1 个 Go 仓库、3 个可执行程序（Gateway 数据面 / Admin 控制面 / Worker 异步任务）的模块化单体**，模块边界按领域划分，未来需要时再拆。
2. **计费**：改为 **预扣（Reserve）→ 调用 → 结算（Settle）→ 释放差额** 的两阶段模型，**以 request_id 做幂等**，金额统一用 **int64 微元（1e-6 CNY）** 存储，账本采用**只追加（append-only）流水 + 余额快照**。
3. **计价**：改为 **"价格表（Price Book）+ 计价分量（Price Component）"** 模型，售价挂在**虚拟模型**上、成本挂在**渠道（Channel）**上，每次请求**快照价格版本**。
4. **用量**：以上游 usage 为准，**缺失时用本地 tokenizer 兜底估算**，并标记 `usage_source`。
5. **路由**：**硬过滤 → 优先级分层 → 层内加权随机（P2C + EWMA）**，叠加**熔断器 + Key 冷却 + 提示缓存亲和**。
6. **协议**：增加**适配器层（Adapter）**，对外统一 OpenAI 协议（可选同时暴露 Anthropic `/v1/messages`），对内适配 OpenAI / Anthropic / Gemini / 各家兼容协议。
7. **多租户**：**从第一天起**钱包归属 `account`（个人或组织），而不是 `user`，避免 Phase4 大迁移。
8. **上游价格实时同步**：多源采集（结算回传 / 官方 API / 定价页 / 社区数据集 / 人工）→ 归一化 → 差异检测 → 按风险分级自动或审批生效 → 秒级热加载；配合**毛利守护**与**实际成本回传漂移检测**，保证上游调价、限时免费、闲时折扣能被及时发现且不会造成亏损（§7.16）。

---

## 1. 原方案深度审阅

### 1.1 值得保留的设计

- 用户永远接触不到上游 Key（原则 1）——正确，且要延伸到**响应内容也不泄漏上游信息**（`model` 字段、错误信息、响应头）。
- Virtual Model + Model Mapping 解耦——正确，是整个平台的基础。
- Promotion 与 Billing 解耦——正确，但需要明确"谁决定成本、谁决定售价、谁决定用户实付"。
- Request Session——正确，但应与 Usage 合并为一张请求日志，避免两份数据不一致。
- 价格配置化、带生效时间——正确，但需要**价格版本化**和**请求级快照**。

### 1.2 问题清单（按严重度）

> P0 = 上线前必须解决（资金/安全）；P1 = 影响稳定性与可扩展性；P2 = 体验/规范。

| 编号 | 级别 | 所在章节 | 问题 | 建议 |
|---|---|---|---|---|
| A1 | P0 | 七、十、十三 | 请求前只做 Wallet Check，调用后才扣费，无冻结；`freeze_amount` 字段存在却无任何流程使用 | 两阶段预扣/结算（§7.9） |
| A2 | P0 | 4.2、十 | 金额类型、精度、币种、舍入未定义；示例 4.978 实为 4.9781，舍入方向不明 | int64 微元 + decimal 计算 + 明确舍入规则（§7.9.2） |
| A3 | P0 | 十、原则 3 | 仅信任上游 usage；流式中断 / 上游不返回 usage / 客户端断开时无法计费 | 上游优先 + 本地 tokenizer 兜底 + `usage_source` 标记（§7.9.4） |
| A4 | P0 | 4.3、六 | Key 只说"保存 Hash"，未说明算法；若用 bcrypt 则每次请求鉴权 ~50ms 且无法索引查找 | 前缀 + HMAC-SHA256(pepper)，可索引、常数时间比较（§7.2） |
| A5 | P0 | 4.5 | `provider_api_key.secret` 明文存储风险；未提及加密 | AES-256-GCM 信封加密，KEK 来自 KMS/环境（§7.15） |
| A6 | P0 | 十、4.10 | 结算无幂等，重试/重复消费事件会重复扣费 | `request_id` 唯一约束 + 结算表幂等（§7.9.3） |
| A7 | P0 | 五、十二 | 注册送 5 元 + 每天 100 万免费 Token 无防刷设计 | 手机/邮箱验证、设备指纹、赠送余额单独账户且不可提现、全局预算上限（§7.10、§7.12） |
| B1 | P1 | 三 | 1000 用户规模拆 12 个微服务，引入 RPC、分布式事务、多套部署，得不偿失 | 模块化单体（§3） |
| B2 | P1 | 4.8 | 计价只有 input/output；缺缓存读/写、推理、图片、音频、按次、分档（如 >200K 上下文）、Batch 折扣 | Price Component 模型（§6.4） |
| B3 | P1 | 4.8 | `profit_rate` 与 `input_sell/output_sell` 同时存储，两者可能不一致；售价挂在 provider 上，同一虚拟模型走不同渠道用户价格会不同 | 售价挂虚拟模型，成本挂渠道；利润是"结果"不是"输入"（§6.4） |
| B4 | P1 | 八 | Score 各维度量纲不同（ms、元、比例）未归一化；"选择最高"导致全量打一个渠道 | 硬过滤 + 优先级分层 + 加权随机/P2C（§7.5） |
| B5 | P1 | 九 | 重试未区分错误类型；流式已向客户端输出后不可重试；无重试预算与总超时 | 错误分类表 + 首字节前可重试 + 总 deadline（§7.7） |
| B6 | P1 | 4.5 | `latency/success_rate/429_count/used` 作为 DB 列每请求更新，产生热点写 | 运行时指标放内存/Redis 滑动窗口，DB 只存配置（§7.6） |
| B7 | P1 | 一、4.7 | 只提"统一 OpenAI API"，未设计协议适配；各家上游模型名不同（如 SiliconFlow 的 `deepseek-ai/DeepSeek-V3`） | Adapter 层 + Channel 表记录 `upstream_model`（§7.4、§6.3） |
| B8 | P1 | 4.10、十四 | Usage 与 Request Session 字段重叠；日志量（每天百万行）放主库会拖垮 OLTP | 合并为 `request_logs`，按天分区，异步批量写，分析走 ClickHouse（§7.13） |
| B9 | P1 | 十七 | 多租户/Organization 放在 Phase4，但钱包、Key、Usage 都绑 user_id，后期迁移代价极大 | 第一天引入 `account`（§6.1） |
| B10 | P1 | 十一 | "Provider 免费"时平台成本为 0，但用户是否也免费属于**商业决策**，文档混为一谈 | 成本侧促销与售价侧促销分离（§7.10） |
| B11 | P1 | — | 无充值/支付/退款/对账设计；无上游账单对账 | 充值订单 + 回调幂等 + 日对账（§7.11） |
| B12 | P1 | 十五 | 风控只列维度，无执行机制；限流未说明算法与存储 | Redis GCRA + 并发租约 + 规则引擎（§7.12） |
| B13 | P1 | 七 | 请求链路缺 Capability 校验（tools、vision、json_schema、上下文长度） | 模型能力元数据 + 请求特征提取（§7.5.1） |
| B14 | P1 | 4.8、十七 | 只有 `effective_time` 字段，没有"上游价格如何获取、如何发现变化、谁来确认、多久生效"的机制；Phase2 的 Dynamic Pricing / Provider Discovery 无设计；不支持闲时折扣等按时段计价 | 价格同步流水线 + 分级生效策略 + 毛利守护 + 实际成本漂移检测（§7.16） |
| C1 | P2 | 十六 | Prometheus 若按 user_id 打标签会导致基数爆炸 | 用户维度走日志/ClickHouse，Prometheus 只到 model/channel 维度（§7.14） |
| C2 | P2 | 全文 | 大量"每行一个词 + 空行"的伪列表，表格缺字段类型与约束，难以直接指导开发 | 本文给出 DDL 与接口定义 |
| C3 | P2 | — | 缺少 SLO、容量假设、测试策略、部署方案、合规风险 | §2、§8、§9、§11 |

### 1.3 关键问题深度分析

#### A1：没有预扣导致的透支（最严重）

场景：用户余额 1 元，用同一个 Key 并发发起 50 个 `max_tokens=32000` 的推理请求。

- V1 流程：50 个请求在入口处都看到"余额 1 元 > 0"，全部放行。
- 每个请求实际花费 0.6 元 → 总消费 30 元，结算后余额 **-29 元**。
- 平台已向上游支付 29 元成本，且用户可以直接弃号（尤其是注册送的 5 元赠送号）。

这是 One-API / New-API 早期版本真实出现过的问题，它们后来都改成了"预扣额度 + 事后补差"。V2 采用同样思路并加强为**数据库原子冻结**（§7.9）。

#### A3：流式场景下 usage 缺失

- OpenAI 兼容协议在流式模式下，**只有请求中带 `stream_options.include_usage=true` 时**才会在最后一个 chunk 返回 usage；部分国内兼容接口不支持该参数。
- 客户端中途断开：我们会取消上游请求，此时上游可能**已经产生了成本**，但我们拿不到 usage。
- 上游超时/5xx 发生在流中段：同上。

若坚持"不做本地估算"，上面这些请求全部变成免费，而攻击者可以**刻意在最后一刻断开连接**白嫖。V2：上游 usage 优先，缺失时用 tokenizer 对"已发送给客户端的内容"估算输出、对请求体估算输入，并在日志中记录 `usage_source=estimated`，供对账。

#### B4：路由打分

V1 公式 `0.35·Health + 0.25·Latency + 0.20·Weight + 0.10·Cost + 0.10·RemainingQuota`：

1. Latency 越大分越高？Cost 越大分越高？方向未定义；量纲不同未归一化。
2. 确定性"选最高分"：所有实例在同一时刻都选同一个渠道 → 该渠道 429 → 分数下降 → 所有流量又一起切到第二名 → 振荡。
3. 业务上真正需要的是**确定性的主备（优先级）**+ **同级之间按权重分流**，而不是一个魔法分数。
4. 没考虑**提示缓存亲和性**：同一会话在同一渠道上才能命中上游 prompt cache（DeepSeek、Anthropic、OpenAI 均按缓存命中给出 1/10 左右的输入价），随机打散会让成本上升数倍。

---

## 2. 目标、非目标与容量假设

### 2.1 目标

- 对外：OpenAI 兼容 `/v1/chat/completions`、`/v1/completions`、`/v1/embeddings`、`/v1/models`；Phase2 起可选 `/v1/messages`（Anthropic 协议，便于 Claude Code 等工具直接接入）、`/v1/images/*`、`/v1/audio/*`。
- 计费准确性：**任何一笔请求的扣费可追溯到：用量 × 价格版本 × 促销规则**，账本与余额可逐笔对平。
- 可用性：单渠道/单 Key 故障对用户透明（首字节前自动切换）。

### 2.2 非目标（V2 不做）

- 自研模型推理、私有化部署模型托管。
- "AI Router"（按语义自动选模型）——放到 Phase3 以后，且需要单独评估。

### 2.3 容量假设与 SLO

| 指标 | 假设 / 目标 |
|---|---|
| 注册用户 | 1,000 → 100,000 |
| 峰值 QPS | 200（设计上限 2,000） |
| 峰值并发流 | 2,000（每个流式连接一个 goroutine，Go 单实例可轻松承载 1 万+） |
| 网关额外延迟（不含上游） | P99 < 15ms（鉴权、限流、预扣、路由） |
| 首字节时间（TTFT）额外开销 | P99 < 30ms |
| 可用性 | 网关 99.95%（不含上游不可控部分） |
| 计费一致性 | 账本 vs 余额 日对账差异 = 0；上游账单 vs 成本记录 差异 < 1% |

结论：**瓶颈永远在上游，不在网关**。单个 4C8G 的 Go 网关实例即可承担目标流量，部署 2–3 个实例只是为了高可用。

---

## 3. 总体架构（V2）

### 3.1 进程划分：模块化单体

```text
┌───────────────────────────────────────────────────────────────────────┐
│                        一个 Go Module（monorepo）                       │
│                                                                       │
│  cmd/gateway   数据面：/v1/*  鉴权·限流·预扣·路由·转发·结算         (无状态，水平扩展)│
│  cmd/admin     控制面：用户控制台 API + 运营后台 API + 支付回调       (无状态)          │
│  cmd/worker    异步：日志落库·用量聚合·对账·冻结回收·健康探测·价格同步              │
│                                                                       │
│  internal/*    领域模块（被三个进程按需引用，模块间只通过接口通信）      │
└───────────────────────────────────────────────────────────────────────┘
          │                    │                      │
     PostgreSQL 16         Redis 7              ClickHouse（Phase2，可选）
   （账本/配置/日志主库）  （限流/缓存/冷却/广播）   （请求日志分析）
```

为什么不是 12 个微服务：

- Wallet、Billing、Promotion、Pricing 在**一次结算**中必须原子完成；拆成服务就要上 Saga/TCC，复杂度与出错概率远高于收益。
- 1000 用户、单团队，微服务的独立部署/独立扩缩容收益几乎为零。
- 模块化单体在代码层面保持边界（`internal/billing` 不能直接访问 `internal/wallet` 的表，只能调用其接口），**未来真要拆时可以按模块直接切出去**，例如数据面 Gateway 天然已经独立。

### 3.2 数据面请求链路

```text
Client ──HTTPS──► LB(SLB/Nginx, 关闭缓冲) ──► gateway
  │
  ├─ 1. RequestID / Recover / AccessLog / OTel Trace
  ├─ 2. Authenticate        API Key → (account, key policy)          [本地 LRU + Redis + PG]
  ├─ 3. Parse & Normalize   解析请求体 → 统一内部结构 + 请求特征(Features)
  ├─ 4. Authorize           Key/Account 是否允许该虚拟模型、IP 白名单
  ├─ 5. RateLimit           RPM(GCRA) · TPM(预估) · 并发租约           [Redis]
  ├─ 6. Quote               价格版本 + 促销匹配 → 预估最大费用
  ├─ 7. Reserve             原子冻结预估费用                           [PG 单条 UPDATE]
  ├─ 8. Route               候选渠道 → 过滤 → 分层 → 加权选择 → 选 Key  [内存快照]
  ├─ 9. Relay               Adapter 转换 → 上游调用 → 流式透传 → 用量累计
  │      └─ 失败且未发出首字节 → 按错误分类重试/切换渠道（回到 8）
  ├─ 10. Settle             实际费用 → 账本流水 → 释放冻结差额（同一事务，幂等）
  └─ 11. Emit               请求日志/指标 异步投递（不阻塞响应）
```

---

## 4. Go 技术选型

| 领域 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.23+ | 使用 `log/slog`、`net/http` 增强路由、`iter`、泛型 |
| HTTP 框架 | `net/http` + `go-chi/chi/v5` | 流式代理需要对 `http.Flusher`、超时、连接取消精细控制，标准库最透明；chi 只做路由和中间件组合（也可选 Gin/Hertz，但不要用会缓冲响应的框架特性） |
| 上游 HTTP 客户端 | 标准库 `http.Transport`（按上游 host 独立配置） | 调 `MaxIdleConnsPerHost`、`ForceAttemptHTTP2`、`ResponseHeaderTimeout` |
| JSON | `encoding/json` 起步；热路径可换 `bytedance/sonic` 或 `goccy/go-json` | 先 profiling 再换 |
| 数据库 | PostgreSQL 16 + `jackc/pgx/v5` + `sqlc` | 类型安全 SQL；账本需要事务与行锁，PG 最合适 |
| 迁移 | `pressly/goose` 或 `ariga/atlas` | 版本化迁移，CI 中校验 |
| 缓存/限流 | Redis 7 + `redis/go-redis/v9` + Lua 脚本 | GCRA 限流可用 `go-redis/redis_rate` |
| 金额计算 | `shopspring/decimal`（计算）+ `int64`（存储） | 禁止 float64 参与金额 |
| Tokenizer | `pkoukk/tiktoken-go`（OpenAI 系）+ 按模型族的近似系数 | 仅作兜底估算 |
| 熔断 | `sony/gobreaker/v2` 或自研轻量版 | 每个渠道/每个 Key 一个 |
| 配置 | `knadh/koanf` 或 `spf13/viper` | 进程配置；业务配置在 DB |
| 可观测 | `prometheus/client_golang`、`go.opentelemetry.io/otel`、`slog`(JSON) | Trace 贯穿网关→上游 |
| 密码 | `golang.org/x/crypto/argon2`（argon2id） | 控制台登录密码 |
| 控制台鉴权 | 服务端 Session（Redis）或短期 JWT + Refresh | 与 API Key 鉴权完全分离 |
| 异步任务 | Phase1：PG Outbox + worker 轮询；Phase2：NATS JetStream（可选） | 1000 用户不需要 Kafka |
| 测试 | `testing` + `testify` + `testcontainers-go` + `httptest` | 真实 PG/Redis 集成测试 |
| 压测 | k6 / vegeta + 自研 mock 上游（可配置延迟、429、断流） | |
| Lint | `golangci-lint`（含 `errcheck`、`gosec`、`bodyclose`、`contextcheck`） | `bodyclose` 对代理场景非常关键 |

---

## 5. 工程目录结构

> 下面标注了每个模块的实际状态（✅ 已实现 / ⚠️ 实现方式和设计不同 / ❌ 未独立成包，
> 功能并入了别的模块 / 🆕 设计时没列出、后来新增），基于当前代码树核对，
> 供后续阅读本文档时校准预期——这份目录结构本身仍然是最初的设计意图，
> 不是每一处都照单实现，模块化单体阶段没必要为每个设计里的概念都单独开包。

```text
uFreeTokens/
├── cmd/
│   ├── gateway/main.go          # ✅ 数据面入口
│   ├── admin/main.go            # ✅ 控制面入口
│   └── worker/main.go           # ✅ 异步任务入口
├── internal/
│   ├── app/                     # ✅ 依赖装配、路由挂载（NewGatewayRouter/NewAdminRouter）
│   ├── config/                  # ✅ 进程配置
│   ├── httpx/                   # ✅ requestid/recover/accesslog/cors/错误响应
│   ├── auth/                    # ✅ 只有 API Key 生成/校验；"控制台会话"实际在 internal/console/（🆕，见下）
│   ├── account/                 # ❌ 未独立成包；账户/用户/成员的写路径在 internal/admin/、internal/console/ 里
│   ├── catalog/                 # ✅ 模型/渠道/价格 的只读内存快照 + TTL 重载（促销匹配单独在 internal/promotion/）
│   ├── schema/                  # ✅ 统一内部请求/响应结构
│   ├── adapter/                 # ⚠️ 协议适配器，但没有按协议拆子目录——
│   │                            #    openai.go/anthropic.go/gemini.go 平铺在包内，registry 在 adapter.go
│   ├── router/                  # ✅ 候选过滤、分层、加权选择
│   ├── keypool/                 # ❌ 未独立成包；Key 选择/冷却并入 internal/router + internal/health
│   ├── health/                  # ✅ 熔断器 + Key 冷却（Redis 共享）
│   ├── relay/                   # ✅ 请求编排：重试、流式透传、用量累计；也承担了下面 usage/billing 的职责
│   ├── usage/                   # ❌ 未独立成包；用量估算/来源判定内联在 internal/relay
│   ├── pricing/                 # ✅ 价格计算纯函数
│   ├── promotion/               # ✅ 促销规则匹配与额度计数
│   ├── billing/                 # ❌ 未独立成包；Quote/Reserve/Settle/Release 编排内联在 internal/relay，
│   │                            #    Reserve/Settle/Release 本身实现在 internal/wallet
│   ├── wallet/                  # ✅ 余额、冻结、账本流水（唯一允许写 wallet 表的模块）
│   ├── payment/                 # ❌ 未实现（Phase 3，充值/支付对接尚未开始）
│   ├── ratelimit/               # ✅ GCRA、TPM、并发租约，另有 AllowRPMStrict（fail-closed，给登录/注册防暴力破解用）
│   ├── risk/                    # ✅ 风控规则与封禁
│   ├── reqlog/                  # ✅ 请求日志批量异步写入
│   ├── reconcile/               # ✅ 对账任务
│   ├── pricesync/               # ✅ 上游价格同步（§7.16）
│   ├── secret/                  # ⚠️ 实际包名是 internal/secretbox/（上游 Key 信封加密）
│   ├── console/                 # 🆕 Phase 1 起新增：面向终端用户的自助注册/登录/API Key/钱包/
│   │                            #    区间用量/调用日志，挂在 cmd/gateway 的 /console/*（见 §7.1、
│   │                            #    §7.16.10 附近），鉴权是 httpOnly Cookie Session，和 /v1/* 的
│   │                            #    API Key 鉴权完全独立
│   ├── chsync/                  # 🆕 分析型存储（ClickHouse）同步状态，Phase 3/4 范围
│   ├── observability/           # ✅ metrics、logging 初始化（tracing 尚未接入）
│   └── store/                   # ⚠️ 手写的 Postgres/Redis 连接封装，不是 sqlc 生成代码
│                                #    （sqlc 引入被推迟，业务代码里直接手写 SQL + pgx）
├── migrations/                  # ✅ SQL 迁移（goose）
├── sql/queries/                 # ❌ 未引入（同上，sqlc 被推迟）
├── api/openapi/                 # ❌ 未生成 OpenAPI 规范；接口文档见 docs/API.md（迭代7新增）
├── deploy/                      # ⚠️ 目前只有 docker-compose.yml（本地依赖）+ nginx/web.conf（迭代7新增前端反代示例）
├── test/                        # ❌ 未独立成 test/ 目录；测试就近放在各 internal/<pkg>/*_test.go
└── Makefile                     # ✅
```

依赖方向（强制，可用 `go-arch-lint` / `depguard` 校验）：

```text
relay → router, keypool, adapter, billing, usage
billing → pricing, promotion, wallet
router → catalog, health
*      → store（仅通过各自模块的 repository 接口）
wallet 不依赖任何业务模块
```

---

## 6. 领域模型与数据库设计（PostgreSQL）

### 6.1 实体关系总览

```text
account (个人或组织，计费主体) 1───* user (通过 account_member)
account 1───1 wallet 1───* ledger_entry
account 1───* api_key
account 1───* credit_grant (赠送/活动余额，带过期)

provider 1───* provider_account (上游账号) 1───* provider_key
virtual_model 1───* channel (= 虚拟模型在某上游账号上的一个部署)
channel *───1 provider_account
price_book(sell, 挂 virtual_model) / price_book(cost, 挂 channel) 1───* price_component
promotion 1───* promotion_counter
request_log (每次请求一行，含计费快照)
```

**术语调整**：V1 的 "Model Mapping" 在 V2 中叫 **Channel（渠道）**，它是路由的最小单位：`某虚拟模型 × 某上游账号 × 上游真实模型名 × 成本价 × 能力 × 权重/优先级`。

### 6.2 账户、用户、Key

```sql
-- 金额统一：BIGINT，单位"微元"（1 元 = 1_000_000）
CREATE TABLE accounts (
    id            BIGSERIAL PRIMARY KEY,
    type          TEXT NOT NULL CHECK (type IN ('personal','organization')),
    name          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active',     -- active / suspended / closed
    tier          TEXT NOT NULL DEFAULT 'free',       -- 用户分组，用于渠道可见性与价格分组
    credit_limit  BIGINT NOT NULL DEFAULT 0,          -- 企业授信额度（允许透支上限）
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    email          CITEXT UNIQUE,
    phone          TEXT UNIQUE,
    password_hash  TEXT NOT NULL,                     -- argon2id
    status         TEXT NOT NULL DEFAULT 'active',
    email_verified BOOLEAN NOT NULL DEFAULT false,
    phone_verified BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_members (
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    user_id    BIGINT NOT NULL REFERENCES users(id),
    role       TEXT   NOT NULL,                       -- owner / admin / developer / billing / viewer
    PRIMARY KEY (account_id, user_id)
);

CREATE TABLE api_keys (
    id             BIGSERIAL PRIMARY KEY,
    account_id     BIGINT NOT NULL REFERENCES accounts(id),
    created_by     BIGINT REFERENCES users(id),
    name           TEXT NOT NULL,
    prefix         TEXT NOT NULL,                     -- 明文前 12 位，用于展示与定位，如 "sk-uft-a1B2c3"
    key_hmac       BYTEA NOT NULL UNIQUE,             -- HMAC-SHA256(pepper, full_key)
    status         TEXT NOT NULL DEFAULT 'active',
    allowed_models TEXT[],                            -- NULL = 不限制
    allowed_ips    CIDR[],
    rpm_limit      INT,                               -- NULL = 继承账户
    tpm_limit      INT,
    concurrency_limit INT,
    budget_limit   BIGINT,                            -- 该 Key 的累计消费上限（微元）
    budget_period  TEXT,                              -- none / daily / monthly
    expires_at     TIMESTAMPTZ,
    last_used_at   TIMESTAMPTZ,                       -- 由 worker 批量回写，不在热路径更新
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON api_keys (account_id);
```

### 6.3 Provider、上游 Key、虚拟模型、渠道

```sql
CREATE TABLE providers (
    id          BIGSERIAL PRIMARY KEY,
    code        TEXT UNIQUE NOT NULL,                 -- deepseek / openai / anthropic / siliconflow / volcengine / dashscope
    name        TEXT NOT NULL,
    protocol    TEXT NOT NULL,                        -- openai / anthropic / gemini（决定使用哪个 Adapter）
    currency    TEXT NOT NULL DEFAULT 'CNY',          -- 上游结算币种
    status      TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE provider_accounts (                       -- 一个上游平台下可以有多个账号（不同合同/折扣/区域）
    id           BIGSERIAL PRIMARY KEY,
    provider_id  BIGINT NOT NULL REFERENCES providers(id),
    name         TEXT NOT NULL,
    base_url     TEXT NOT NULL,                       -- 仅允许管理员配置，需做 SSRF 白名单校验
    region       TEXT,
    extra        JSONB NOT NULL DEFAULT '{}',         -- api_version、组织 ID 等
    status       TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE provider_keys (
    id                  BIGSERIAL PRIMARY KEY,
    provider_account_id BIGINT NOT NULL REFERENCES provider_accounts(id),
    secret_ciphertext   BYTEA NOT NULL,              -- AES-256-GCM 密文
    secret_dek_wrapped  BYTEA NOT NULL,              -- 被 KEK 加密的数据密钥
    secret_last4        TEXT NOT NULL,
    rpm_limit           INT,
    tpm_limit           INT,
    concurrency_limit   INT,
    weight              INT NOT NULL DEFAULT 100,
    status              TEXT NOT NULL DEFAULT 'active', -- active / disabled / exhausted / revoked
    disabled_reason     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 注意：latency / success_rate / 429_count / cooldown_until 不再作为 DB 列（见 §7.6）

CREATE TABLE virtual_models (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT UNIQUE NOT NULL,              -- 对用户暴露：deepseek-v4-flash
    family         TEXT NOT NULL,                     -- deepseek / gpt / claude / qwen …（用于 tokenizer 选择）
    type           TEXT NOT NULL,                     -- chat / embedding / image / audio / rerank
    context_window INT  NOT NULL,
    max_output     INT  NOT NULL,
    capabilities   TEXT[] NOT NULL DEFAULT '{}',      -- stream / tools / vision / json_schema / reasoning / prompt_cache
    visible_tiers  TEXT[] NOT NULL DEFAULT '{free,pro,enterprise}',
    status         TEXT NOT NULL DEFAULT 'active',    -- active / deprecated / hidden
    aliases        TEXT[] NOT NULL DEFAULT '{}'
);

CREATE TABLE channels (
    id                  BIGSERIAL PRIMARY KEY,
    virtual_model_id    BIGINT NOT NULL REFERENCES virtual_models(id),
    provider_account_id BIGINT NOT NULL REFERENCES provider_accounts(id),
    upstream_model      TEXT NOT NULL,                -- 上游真实模型名，如 deepseek-ai/DeepSeek-V3
    priority            INT  NOT NULL DEFAULT 0,      -- 数字越小越优先（主/备）
    weight              INT  NOT NULL DEFAULT 100,    -- 同优先级内权重
    capabilities        TEXT[],                       -- NULL = 继承虚拟模型；渠道可能不支持某些能力
    context_window      INT,                          -- 渠道可能比官方小
    param_overrides     JSONB NOT NULL DEFAULT '{}',  -- 参数改写/剔除（某些上游不支持某参数）
    allowed_tiers       TEXT[],                       -- 例：免费渠道只给 free 用户
    status              TEXT NOT NULL DEFAULT 'active',
    UNIQUE (virtual_model_id, provider_account_id, upstream_model)
);
```

### 6.4 价格：Price Book + Price Component

设计原则：

- **售价（sell）挂在虚拟模型上**：用户看到的价格与底层走哪个渠道无关（否则用户账单不可预期）。
- **成本（cost）挂在渠道上**：同一虚拟模型不同渠道成本不同，路由可据此优化毛利。
- **利润率不是存储字段，而是配置工具**：运营可以用"成本 × (1+利润率)"生成售价，但**落库的是最终售价**，避免双重事实来源。
- **价格版本化**：修改价格 = 插入新版本；请求日志记录 `sell_price_version_id` 和 `cost_price_version_id`。

```sql
CREATE TABLE price_books (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('sell','cost')),
    virtual_model_id BIGINT REFERENCES virtual_models(id),   -- kind=sell
    channel_id      BIGINT REFERENCES channels(id),          -- kind=cost
    tier            TEXT,                                    -- 售价可按用户分组区分，NULL=默认
    currency        TEXT NOT NULL,                           -- sell 固定 CNY；cost 为上游币种
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    created_by      BIGINT,
    note            TEXT,
    CHECK ((kind='sell' AND virtual_model_id IS NOT NULL) OR (kind='cost' AND channel_id IS NOT NULL))
);

CREATE TABLE price_components (
    id             BIGSERIAL PRIMARY KEY,
    price_book_id  BIGINT NOT NULL REFERENCES price_books(id),
    meter          TEXT NOT NULL,          -- 见下表
    unit           TEXT NOT NULL,          -- per_1m_tokens / per_request / per_image / per_second
    service_tier   TEXT NOT NULL DEFAULT 'default',  -- default / batch / priority / flex
    -- 分档：按本次请求输入 token 数（上下文长度）选择档位
    tier_min_input INT NOT NULL DEFAULT 0,
    tier_max_input INT,
    -- 时段计价（闲时折扣等）：UTC 当日分钟数 [start, end)，允许跨零点；NULL = 全天
    window_start_min SMALLINT,
    window_end_min   SMALLINT,
    unit_price     NUMERIC(20,10) NOT NULL,
    UNIQUE (price_book_id, meter, service_tier, tier_min_input, window_start_min)
);
-- 匹配优先级：service_tier 精确匹配 → 时段匹配（请求开始时间）→ 分档匹配（输入 token 数）
```

合同折扣：`provider_accounts` 增加 `cost_multiplier NUMERIC(6,4) NOT NULL DEFAULT 1`（如 0.85 表示该上游账号享受 85 折）。**价格同步写入的是"官方挂牌价"，渠道成本 = 挂牌价 × cost_multiplier**，这样上游调价时合同折扣无需重新录入（§7.16）。

计量项（meter）枚举：

| meter | 说明 |
|---|---|
| `input` | 未命中缓存的输入 token |
| `input_cache_read` | 命中缓存的输入 token（通常为 input 的 10%–50%） |
| `input_cache_write` | 写缓存的输入 token（Anthropic 等，通常为 input 的 125%） |
| `output` | 输出 token（含推理 token，除非上游单独计价） |
| `output_reasoning` | 若上游单独计价推理 token |
| `input_image` / `input_audio` / `output_audio` | 多模态 |
| `request` | 按次费用（如联网搜索、图像生成） |

> V1 示例：采购价 input 4.5 / output 18（元/百万 token），利润 3% → 售价 4.635 / 18.54。在 V2 中表现为：cost book 两个 component（4.5、18），sell book 两个 component（4.635、18.54）。

### 6.5 钱包与账本

```sql
CREATE TABLE wallets (
    account_id      BIGINT PRIMARY KEY REFERENCES accounts(id),
    cash_balance    BIGINT NOT NULL DEFAULT 0,   -- 充值余额（可退款）
    bonus_balance   BIGINT NOT NULL DEFAULT 0,   -- 赠送余额合计（= 未过期 credit_grants.remaining 之和，冗余便于原子判断）
    frozen          BIGINT NOT NULL DEFAULT 0,   -- 当前在途请求冻结总额
    version         BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (frozen >= 0)
);
-- 可用余额 = cash_balance + bonus_balance + accounts.credit_limit - frozen

CREATE TABLE credit_grants (                    -- 注册赠送、活动赠送、补偿
    id            BIGSERIAL PRIMARY KEY,
    account_id    BIGINT NOT NULL REFERENCES accounts(id),
    source        TEXT NOT NULL,                 -- signup / promotion / compensation / invite
    promotion_id  BIGINT,
    amount        BIGINT NOT NULL,
    remaining     BIGINT NOT NULL,
    model_scope   TEXT[],                        -- NULL=全部模型可用
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON credit_grants (account_id, expires_at) WHERE remaining > 0;

CREATE TABLE ledger_entries (                   -- 只追加，禁止 UPDATE/DELETE（用权限 + 触发器保证）
    id            BIGSERIAL PRIMARY KEY,
    account_id    BIGINT NOT NULL,
    type          TEXT NOT NULL,                 -- recharge / consume / refund / grant / grant_expire / adjust
    amount        BIGINT NOT NULL,               -- 带符号：收入为正，支出为负
    balance_kind  TEXT NOT NULL,                 -- cash / bonus
    grant_id      BIGINT,
    cash_after    BIGINT NOT NULL,
    bonus_after   BIGINT NOT NULL,
    ref_type      TEXT NOT NULL,                 -- request / payment_order / promotion / admin
    ref_id        TEXT NOT NULL,                 -- request_id / order_no …
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (ref_type, ref_id, type, balance_kind, grant_id)  -- 幂等
);
CREATE INDEX ON ledger_entries (account_id, created_at DESC);
```

> 为什么小额消费不每笔都写 ledger？——**要写**。1000 用户规模下每天几十万行，PG 完全可以承受；按月分区即可。只有到了千万级/天才考虑"按分钟聚合消费流水"，届时再引入。

### 6.6 冻结与结算

```sql
CREATE TABLE reservations (
    request_id    TEXT PRIMARY KEY,
    account_id    BIGINT NOT NULL,
    amount        BIGINT NOT NULL,
    status        TEXT NOT NULL,           -- held / settled / released
    expires_at    TIMESTAMPTZ NOT NULL,    -- 兜底回收：网关崩溃时由 worker 释放
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON reservations (expires_at) WHERE status = 'held';
```

### 6.7 促销

```sql
CREATE TABLE promotions (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    side          TEXT NOT NULL CHECK (side IN ('cost','sell')),  -- 见 §7.10
    type          TEXT NOT NULL,     -- cost_free / price_discount / free_quota / credit_grant
    priority      INT  NOT NULL DEFAULT 0,
    stackable     BOOLEAN NOT NULL DEFAULT false,
    scope         JSONB NOT NULL,    -- {"models":["deepseek-v4-flash"],"channels":[..],"tiers":["free"],"new_user_days":7}
    params        JSONB NOT NULL,    -- {"discount":0.5} / {"tokens_per_day":1000000} / {"amount":5000000,"expire_days":30}
    budget_total  BIGINT,            -- 活动总预算（微元），防止被刷穿
    budget_used   BIGINT NOT NULL DEFAULT 0,
    starts_at     TIMESTAMPTZ NOT NULL,
    ends_at       TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE promotion_counters (       -- free_quota 类的用量计数（Redis 为热计数，PG 为持久化）
    promotion_id  BIGINT NOT NULL,
    account_id    BIGINT NOT NULL,
    period_key    TEXT NOT NULL,        -- 2026-09-25 / 2026-09
    used_tokens   BIGINT NOT NULL DEFAULT 0,
    used_amount   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (promotion_id, account_id, period_key)
);
```

### 6.8 请求日志（合并 V1 的 Usage + Request Session）

```sql
CREATE TABLE request_logs (
    request_id          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    account_id          BIGINT NOT NULL,
    api_key_id          BIGINT NOT NULL,
    virtual_model       TEXT NOT NULL,
    channel_id          BIGINT,
    provider_key_id     BIGINT,
    endpoint            TEXT NOT NULL,          -- chat.completions / embeddings …
    is_stream           BOOLEAN NOT NULL,
    status              TEXT NOT NULL,          -- success / upstream_error / client_cancel / rejected
    http_status         INT,
    error_code          TEXT,
    attempts            SMALLINT NOT NULL,      -- 尝试次数（含切换）
    attempt_trace       JSONB,                  -- [{channel_id, key_id, status, latency_ms, error_class}]
    ttft_ms             INT,
    latency_ms          INT,
    -- 用量
    input_tokens        INT, cache_read_tokens INT, cache_write_tokens INT,
    output_tokens       INT, reasoning_tokens INT,
    usage_source        TEXT NOT NULL,          -- upstream / estimated / mixed
    -- 计费快照
    sell_price_book_id  BIGINT, cost_price_book_id BIGINT,
    promotion_ids       BIGINT[],
    list_amount         BIGINT,                 -- 原价
    charged_amount      BIGINT,                 -- 实扣
    cost_amount         BIGINT,                 -- 成本（已折算 CNY，按本地价格表计算）
    upstream_cost       NUMERIC(20,10),         -- 上游在响应中回传的实际费用（如有，原币种），用于漂移检测 §7.16.8
    fx_rate             NUMERIC(12,6),
    client_ip           INET,
    user_agent          TEXT,
    PRIMARY KEY (request_id, created_at)
) PARTITION BY RANGE (created_at);             -- 按天分区，保留 90 天热数据，历史转 ClickHouse/对象存储
```

> **默认不存储 prompt / completion 正文。** 如需"重放"（V1 十四章），必须由账户显式开启，单独存储、加密、设定保留期（见 §11 合规）。

---

## 7. 核心模块设计

### 7.1 Gateway 中间件链

```go
r := chi.NewRouter()
r.Use(httpx.RequestID, httpx.Recover, httpx.AccessLog(logger), otelhttp.NewMiddleware("gateway"))

r.Route("/v1", func(r chi.Router) {
    // /v1/catalog 是唯一免鉴权的端点（公开模型目录，Phase 2 实现），
    // 不挂 auth.APIKey；其余端点都要求 API Key。
    r.Get("/catalog", h.PublicCatalog)

    r.Group(func(r chi.Router) {
        r.Use(auth.APIKey(authSvc))         // 401
        r.Use(risk.Guard(riskSvc))          // 403 封禁/IP
        r.Get("/models", h.ListModels)
        r.Get("/usage", h.Usage)                       // 自助查询钱包余额 + 累计用量
        r.Post("/chat/completions", h.Relay(schema.EndpointChat))
        r.Post("/completions", h.Relay(schema.EndpointCompletion))
        r.Post("/embeddings", h.Relay(schema.EndpointEmbedding))
        r.Post("/messages", h.Relay(schema.EndpointMessages)) // Anthropic 兼容入口，内部转译成 chat.completions 走同一条计费管线
    })
})

// /console/* 是面向终端用户的自助控制台（Phase 1 起），和 /v1/* 共用这个
// gateway 进程，但鉴权完全独立：httpOnly Cookie Session，不是 API Key，
// 不挂 auth.APIKey，也不开 CORS（Cookie 会话只信任同源请求）。
r.Route("/console", func(r chi.Router) {
    r.Post("/register", h.ConsoleRegister)
    r.Post("/login", h.ConsoleLogin)
    r.Post("/logout", h.ConsoleLogout)

    r.Group(func(r chi.Router) {
        r.Use(console.RequireSession(sessionStore))
        r.Get("/me", h.ConsoleMe)
        r.Get("/api-keys", h.ConsoleListKeys)
        r.Get("/wallet", h.ConsoleWallet)
        r.Get("/usage", h.ConsoleUsageInterval)   // 按天/按模型聚合的区间用量
        r.Get("/logs", h.ConsoleLogs)             // keyset 分页的调用日志

        r.Group(func(r chi.Router) {
            r.Use(console.CSRFGuard) // 非 GET 请求要求同源 + 自定义头
            r.Post("/api-keys", h.ConsoleCreateKey)
            r.Post("/api-keys/{id}/revoke", h.ConsoleRevokeKey)
        })
    })
})
```

`Relay` 内部按 §3.2 步骤 3–11 执行（限流、预扣依赖请求体中的 model 与 max_tokens，所以放在 handler 内而非通用中间件）。

HTTP Server 超时设置（流式场景特别注意）：

```go
srv := &http.Server{
    Addr:              cfg.Addr,
    Handler:           r,
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       30 * time.Second,   // 请求体读取
    WriteTimeout:      0,                  // 流式响应不能设全局写超时，改为在 relay 内做空闲超时
    IdleTimeout:       120 * time.Second,
    MaxHeaderBytes:    1 << 20,
}
// 请求体限制：http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes) —— 多模态场景建议 20MB
```

### 7.2 API Key 生成与校验

格式：`sk-uft-` + 32 字节随机数 base62 编码（约 43 字符）+ 可选 CRC32 校验尾（便于 GitHub Secret Scanning 等工具识别，且能在查库前拒绝伪造 Key）。

```go
// 生成
raw := "sk-uft-" + base62(randBytes(32))
mac := hmacSHA256(pepper, raw)      // pepper 来自 KMS/环境变量，不入库
store(prefix = raw[:13], key_hmac = mac)
return raw                           // 只展示一次

// 校验（热路径）
mac := hmacSHA256(pepper, presented)
if k, ok := lru.Get(mac); ok { return k }                 // 进程内 LRU，TTL 60s
k := redisOrPG.FindByHMAC(mac)                            // 索引查找
```

- 为什么不用 bcrypt/argon2：API Key 本身是 256 bit 高熵随机数，不存在字典攻击问题，HMAC 足够安全且可**索引直查**；bcrypt 需要先按前缀取出候选再逐个比对，每次 ~50ms。
- Key 吊销：写 DB 后通过 Redis Pub/Sub 广播，所有网关实例立即淘汰本地缓存。

### 7.3 Catalog：路由配置的内存快照与热加载

路由热路径**绝不查数据库**。所有虚拟模型、渠道、价格、促销、上游 Key（解密后）加载为一个**不可变快照**：

```go
type Snapshot struct {
    Version      int64
    Models       map[string]*VirtualModel     // 含别名
    ChannelsByVM map[int64][]*Channel
    SellPrices   map[priceKey]*PriceBook       // (vm, tier) → 当前生效版本
    CostPrices   map[int64]*PriceBook          // channel → 当前生效版本
    Promotions   []*Promotion                  // 已按 priority 排序
    KeysByPA     map[int64][]*ProviderKey
}

type Catalog struct{ cur atomic.Pointer[Snapshot] }

func (c *Catalog) Get() *Snapshot { return c.cur.Load() }
```

刷新机制：

1. 控制面修改配置 → 同一事务写 `config_version` 自增 → `NOTIFY catalog_changed`。
2. 网关 `LISTEN catalog_changed`（`pgx` 支持）→ 重新全量加载 → 校验 → `atomic.Store`。
3. 兜底：每 30 秒比对 `config_version`，防止丢通知。
4. 带生效时间的价格/促销：快照中保存"未来版本"，按请求时间选择，避免整点切换时依赖刷新时机。价格数据的来源与自动更新见 §7.16。

规模评估：100 模型 × 平均 5 渠道 × 若干价格，快照 < 5MB，全量重建 < 100ms，无需增量。

### 7.4 协议适配层（Adapter）

内部统一结构 `schema.ChatRequest` 以 OpenAI Chat Completions 为基础，扩展字段承载其他协议独有语义（如 Anthropic 的 `cache_control`、`thinking`）。

```go
package adapter

type Adapter interface {
    // BuildRequest 把统一请求转换成上游 HTTP 请求（含鉴权头、模型名替换、参数改写）。
    BuildRequest(ctx context.Context, t Target, req *schema.ChatRequest) (*http.Request, error)
    // DecodeResponse 解析非流式响应。
    DecodeResponse(resp *http.Response) (*schema.ChatResponse, error)
    // NewStream 返回流式解码器，逐个产出统一格式的 chunk。
    NewStream(resp *http.Response) (StreamDecoder, error)
    // ClassifyError 把上游错误映射为内部错误类别（决定是否重试/冷却/熔断）。
    ClassifyError(resp *http.Response, body []byte) ErrorClass
}

type StreamDecoder interface {
    Next() (*schema.ChatChunk, error) // io.EOF 表示正常结束
    Close() error
}

type Target struct {
    Channel  *catalog.Channel
    Key      *catalog.ProviderKey   // 已解密
    BaseURL  string
}
```

实现要点：

- `openai` 适配器覆盖绝大多数国内外兼容上游（DeepSeek、SiliconFlow、火山方舟、阿里百炼、OpenRouter、Moonshot、智谱…），差异通过 `channels.param_overrides` 配置（剔除不支持的参数、改写 `max_tokens` → `max_completion_tokens` 等），**不为每家写代码**。
- 流式请求强制注入 `stream_options: {"include_usage": true}`（若渠道支持）；如果客户端本身没要求 usage，则网关**吞掉**最后的 usage chunk，不改变客户端看到的协议行为。
- 响应中的 `model` 字段改写为虚拟模型名；`id` 改写为平台 request_id；过滤上游特有响应头。
- 错误响应统一为 OpenAI 错误格式，**不透传上游错误原文**（可能包含上游账号信息），原文只进日志。
- 上游用量字段映射到统一 `schema.Usage`：

```go
type Usage struct {
    InputTokens      int  // 不含缓存命中部分
    CacheReadTokens  int
    CacheWriteTokens int
    OutputTokens     int  // 含推理
    ReasoningTokens  int  // 仅用于展示/单独计价
    Source           UsageSource // upstream / estimated / mixed
}
```

  - OpenAI：`prompt_tokens_details.cached_tokens` → CacheRead，`InputTokens = prompt_tokens - cached`。
  - DeepSeek：`prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`。
  - Anthropic：`input_tokens`（已不含缓存）、`cache_read_input_tokens`、`cache_creation_input_tokens`。
  - **每个适配器必须有 golden file 测试**覆盖这些映射，这是计费正确性的第一道关。

### 7.5 路由器

#### 7.5.1 请求特征提取

```go
type Features struct {
    Stream          bool
    NeedTools       bool
    NeedVision      bool
    NeedJSONSchema  bool
    NeedReasoning   bool
    EstInputTokens  int      // 本地快速估算（字节数/3 或 tokenizer）
    MaxOutputTokens int
    AffinityKey     uint64   // 提示缓存亲和：hash(account_id, system prompt + 前 N 条消息)
}
```

#### 7.5.2 选择算法

```text
候选 = snapshot.ChannelsByVM[vm]
① 硬过滤（任何一条不满足即剔除）
   - channel.status = active，且其 provider_account/provider 可用
   - 能力满足 Features（tools/vision/json_schema/stream）
   - EstInputTokens + MaxOutputTokens ≤ channel.context_window
   - account.tier ∈ channel.allowed_tiers
   - 熔断器非 Open；至少有一个可用 Key（未冷却、未达本地 RPM/并发上限）
   - 本次请求已失败过的渠道排除
② 优先级分层：取 priority 最小的非空层（确定性主备）
③ 亲和性：若 AffinityKey 在该层命中上次使用的渠道且其健康 → 直接使用（命中上游缓存，省钱）
④ 层内加权随机（Power of Two Choices）：
   有效权重 w_i = weight_i × health_i × latency_i × cost_i
     health_i  = 近 60s 成功率^2（0~1，熔断半开时固定 0.1）
     latency_i = clamp(ref_latency / ewma_ttft_i, 0.2, 2)   // 以同层中位数为参照，归一化
     cost_i    = 1 + α × (median_cost - cost_i)/median_cost  // α 默认 0.3，按需开启
   按 w_i 加权随机抽 2 个，取 inflight/capacity 更低者
⑤ 在渠道的 provider_account 下选 Key（§7.6）
```

与 V1 相比：

- **主备语义显式化**（priority），运营可预期；同层内才做"智能"分流。
- 加权随机天然避免羊群效应；P2C 利用各实例的本地 inflight 信息进一步均衡。
- 所有因子都是**无量纲乘数**，含义清晰，可单独开关。
- Phase3 的 A/B Routing = 在 ② 之前按 `hash(request_id) % 100` 选择实验组的渠道集合，架构上无需改动。

```go
type Router interface {
    Pick(ctx context.Context, s *catalog.Snapshot, vm *catalog.VirtualModel,
        f Features, acct *auth.Principal, exclude ChannelSet) (Target, error)
}
```

### 7.6 Key 池、健康度与熔断

运行时状态**不写数据库**：

| 状态 | 存储 | 说明 |
|---|---|---|
| EWMA TTFT / 总延迟 | 进程内 | 每实例独立即可，统计意义足够 |
| 成功率滑动窗口 | 进程内环形数组（6×10s 桶） | |
| 熔断器（渠道级、Key 级） | 进程内 `gobreaker` | 连续失败/失败率触发，半开探测 |
| Key 冷却（429/配额耗尽） | **Redis** `SET cooldown:key:{id} 1 EX {retry_after}` + 进程内缓存 | 需要跨实例共享，否则每个实例都要撞一次 429 |
| Key RPM/TPM/并发 | 进程内令牌桶，按 `limit / 实例数` 切分；或 Redis GCRA（精确但多一次 RTT） | 默认本地切分，渠道配额紧张时切换 Redis 模式 |
| Key 永久失效（401/余额不足） | 写 DB `status=exhausted/revoked` + 告警 | 需要人工处理 |

Key 选择：在可用 Key 中按 `weight × 剩余令牌比例` 加权随机。

上游错误 → 动作映射：

| 上游表现 | 错误类别 | 动作 |
|---|---|---|
| 429 + Retry-After | `RateLimited` | Key 冷却 `retry_after`（默认 30s，指数退避到 5min）；换 Key 重试 |
| 429 insufficient_quota / 402 / 余额不足 | `KeyExhausted` | Key 标记 exhausted，告警；换 Key 重试 |
| 401 / 403 invalid key | `KeyInvalid` | Key 标记 revoked，告警；换 Key 重试 |
| 5xx / 连接错误 / 首字节超时 | `UpstreamUnavailable` | 计入渠道熔断；换渠道重试 |
| 400 参数错误 / context_length_exceeded | `BadRequest` | **不重试**，返回用户（改写为平台错误码） |
| 内容安全拦截 | `ContentFiltered` | **不重试**（换渠道重试可能构成规避审核），返回用户 |
| 流中途断开 | `StreamBroken` | 不重试（已向客户端输出）；按已产生内容计费 |

### 7.7 重试与故障转移

```go
type RetryPolicy struct {
    MaxAttempts     int           // 默认 3（含首次）
    TotalDeadline   time.Duration // 默认 = 首字节总预算，如 60s；推理模型可按渠道配置更长
    PerAttemptTTFB  time.Duration // 单次首字节超时，按渠道配置（推理模型需更长）
}
```

规则：

1. **只在尚未向客户端写出任何字节前重试**。流式请求在拿到上游第一个有效 chunk 后才向客户端写响应头，这样首字节前的失败对用户透明。
2. 同一请求内：Key 级错误 → 同渠道换 Key；渠道级错误 → 排除该渠道重新 `Router.Pick`。
3. 重试间隔：0（换 Key/换渠道本身就是规避），同 Key 不重试。
4. 全局重试预算：每实例每秒重试数 ≤ 正常请求数的 20%，防止上游整体故障时重试放大流量。
5. 每次尝试记录进 `attempt_trace`。

### 7.8 流式代理

```go
func (h *Relay) pipeStream(ctx context.Context, w http.ResponseWriter, dec adapter.StreamDecoder,
    first *schema.ChatChunk, acc *usage.Accumulator, idle time.Duration) error {

    fl, ok := w.(http.Flusher)
    if !ok {
        return errors.New("streaming unsupported")
    }
    hdr := w.Header()
    hdr.Set("Content-Type", "text/event-stream")
    hdr.Set("Cache-Control", "no-cache")
    hdr.Set("X-Accel-Buffering", "no") // 告诉 Nginx 不要缓冲
    w.WriteHeader(http.StatusOK)

    timer := time.NewTimer(idle)
    defer timer.Stop()

    chunk := first
    for {
        acc.Observe(chunk) // 累计输出文本/usage，用于兜底估算
        if acc.ShouldForward(chunk) { // 客户端未要求 usage 时吞掉 usage-only chunk
            if err := writeSSE(w, chunk); err != nil {
                return errClientGone // 客户端断开：ctx 取消会同时中止上游
            }
            fl.Flush()
        }

        next := make(chan result, 1)
        go func() { c, err := dec.Next(); next <- result{c, err} }()
        timer.Reset(idle)
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-timer.C:
            return errStreamIdle // 上游卡住：按已输出内容计费
        case r := <-next:
            if errors.Is(r.err, io.EOF) {
                writeDone(w)
                fl.Flush()
                return nil
            }
            if r.err != nil {
                return r.err
            }
            chunk = r.chunk
        }
    }
}
```

> 实际实现中为避免每个 chunk 起一个 goroutine，可以用 `resp.Body` 的读超时封装（`SetReadDeadline` 通过自定义 `net.Conn` 或 `http.ResponseController`），上面为了表达清楚做了简化。

要点：

- 解析 SSE 用 `bufio.Reader.ReadSlice('\n')` 并设置足够大的缓冲（单行可能 > 64KB，`bufio.Scanner` 默认上限会截断）。
- 客户端断开 → `r.Context()` 取消 → 上游请求被取消（`http.NewRequestWithContext`），避免继续消耗上游成本。
- **无论成功、失败、断开，`defer` 中都执行结算**（§7.9），且结算使用独立的 `context.WithoutCancel(ctx)` + 超时，不能因为客户端断开而跳过结算。
- 网关优雅退出：收到 SIGTERM → 停止接收新连接 → 等待在途流最多 N 分钟 → 超时后强制结算并关闭。

### 7.9 计费引擎（核心）

#### 7.9.1 两阶段流程

```text
             ┌──────────── Quote ────────────┐
请求 ──► 解析 ──► 匹配售价版本 + 促销 ──► 估算最大费用 E
                                              │
                           Reserve(E)：UPDATE wallets SET frozen = frozen + E
                                      WHERE account_id = ? AND 可用余额 ≥ E
                                      并 INSERT reservations(request_id, E, held)
                                              │ 失败 → 402 insufficient_balance
                                              ▼
                                        上游调用（含重试）
                                              │
                                              ▼
                        得到实际用量 U（上游 or 估算）→ 计算实际费用 A
                                              │
          Settle（单事务，幂等）：
            1) UPDATE reservations SET status='settled' WHERE request_id=? AND status='held'
               → 影响 0 行说明已结算/已释放，直接返回（幂等）
            2) 扣减顺序：免费额度(promotion counter) → 赠送余额(按过期时间先到先扣) → 现金余额
            3) UPDATE wallets SET frozen = frozen - E, bonus_balance -= b, cash_balance -= c
            4) INSERT ledger_entries（每个扣减来源一条）
            5) INSERT billing outbox 事件（请求日志、用量统计由 worker 消费）
```

预估费用 E 的计算：

```text
E = price(input) × EstInputTokens + price(output) × ReserveOutputTokens
ReserveOutputTokens = min(req.max_tokens ?? model.max_output, cfg.reserve_output_cap)
```

`reserve_output_cap` 是一个运营参数（如 8K）：若严格按 `max_output`（某些推理模型 64K+）冻结，低余额用户将无法发起任何请求。取舍：

- 实际费用 A 可能 > E：允许现金余额出现**小额负数**（上限 = 单请求超出部分，且账户随即被下次预扣拦截），企业账户由 `credit_limit` 覆盖。
- 同时启用**账户级并发上限**，把最坏透支限制在 `并发数 × (A_max − E)` 以内。

#### 7.9.2 金额单位与舍入

- 存储单位：**int64 微元（1e-6 CNY）**，上限约 9.2 万亿元，足够。
- 价格：`NUMERIC(20,10)`，单位"元/百万 token"。
- 计算：`shopspring/decimal`，全部计量项相加后**一次性舍入**（不要每项分别舍入），规则：**向上取整到 1 微元**（ROUND_CEILING），并在文档/用户协议中公开。
- 外币成本：cost book 用上游币种记录，结算时按**当日汇率快照**（`fx_rates` 表，worker 每日同步）折算为 CNY 写入 `cost_amount` 并记录 `fx_rate`。

```go
// pricing 是纯函数包，零 IO，便于穷举测试
// q 为 Quote 阶段锁定的上下文（价格版本、服务等级、请求开始时间），结算时复用，保证请求中途不换价
func Charge(book *catalog.PriceBook, q QuoteCtx, u usage.Usage, disc Discount) (listAmt, chargedAmt int64) {
    total := decimal.Zero
    for _, m := range meters(u) { // [(meter, qty)]
        // 按服务等级、请求开始时间（时段价）、输入总 token（分档）选取单价
        p := book.UnitPrice(m.Meter, q.ServiceTier, q.StartedAt, u.InputTokens+u.CacheReadTokens+u.CacheWriteTokens)
        total = total.Add(p.Mul(decimal.NewFromInt(int64(m.Qty))).Div(million))
    }
    list := toMicroCeil(total)
    return list, disc.Apply(list)
}
```

**V1 示例复算**：输入 1250、输出 870，售价 4.635 / 18.54 元/百万 token：

```text
1250 × 4.635 / 1e6 = 0.00579375 元
 870 × 18.54 / 1e6 = 0.0161298  元
合计               = 0.02192355 元 → 向上取整 21,924 微元
余额 5.000000 → 4.978076 元（V1 写作 4.978，为展示截断）
成本 1250×4.5/1e6 + 870×18/1e6 = 0.02128125 元 → 毛利 0.00064 元（≈3%）
```

#### 7.9.3 幂等与一致性

- `request_id` 由网关生成（ULID，时间有序），贯穿 reservation、ledger、request_log。
- 结算幂等靠 `reservations` 的状态迁移（`held → settled` 只能成功一次）+ `ledger_entries` 唯一约束双保险。
- **崩溃恢复**：网关在预扣后崩溃 → reservation 停留在 `held` → worker 扫描 `expires_at < now()` 的记录：
  - 若 `request_logs` 有该请求的用量 → 补结算；
  - 否则 → 释放冻结（平台承担这笔可能的上游成本，并计入"未结算损失"指标，异常时告警）。
  - `expires_at` = 请求开始 + 最大请求时长（如 30 分钟）。

#### 7.9.4 用量来源优先级

```text
1. 上游返回 usage                         → usage_source = upstream
2. 流式无 usage / 客户端断开 / 流中断：
   输入 = tokenizer(请求消息)              → 对应模型族的 tokenizer 或 字节数 × 系数
   输出 = tokenizer(已从上游收到的全部文本，含 reasoning_content、tool_calls 参数)
                                          → usage_source = estimated
3. 上游在首字节前失败（4xx/5xx 未产出内容） → 不计费，释放冻结
```

**当前实现（`internal/relay`）与上面设计的差异，是有意的阶段性简化，不是遗漏**：

- 网关（`adapter/openai.go` 的 `BuildRequest`）对所有流式请求无条件向上游注入
  `stream_options.include_usage=true`，所以绝大多数走 openai 兼容协议的上游
  都会命中"1. 上游返回 usage"这条路径；`usage_source=estimated` 主要出现在
  "客户端在上游吐出最终 usage chunk 之前就断开连接"这一种场景。
- 估算算法目前是**字节数 × 系数**（`estimateTokens`：约 4 字节/token 的经验值），
  不是"对应模型族的 tokenizer"——按模型族接入真正的 tokenizer（如
  tiktoken/sentencepiece）留作后续，字节估算的误差在预扣/兜底场景下可接受，
  但不适合作为最终账单的精确依据，仅用于：(a) 预扣费用时的输入 token 估算，
  (b) 客户端断开时按"已转发给客户端的内容字节数"估算已产生的输出（见下）。
- **客户端断开时**：不再按预扣上限 `reserveOutput`（"最多可能用掉多少"）计费，
  而是按已经转发给客户端的内容字节数估算实际输出（仍不超过
  `reserveOutput`，防止估算函数本身异常时超收）——用户看了两个字就断开和跑满
  整个 `max_tokens` 不应该付一样的钱。

对账任务比较 `estimated` 请求占比与上游账单差异，若某渠道估算占比异常高，说明其 usage 回传有问题，应调整适配器。

#### 7.9.5 Go 接口

```go
package billing

type Service interface {
    Quote(ctx context.Context, p *auth.Principal, vm *catalog.VirtualModel, f router.Features) (*Quote, error)
    Reserve(ctx context.Context, requestID string, q *Quote) (*Hold, error)          // 402 on insufficient
    Settle(ctx context.Context, h *Hold, ch *catalog.Channel, u usage.Usage) (*Receipt, error)
    Release(ctx context.Context, h *Hold) error                                        // 未产生费用
}
```

性能：每请求 2 次 PG 写事务（Reserve、Settle），在 200 QPS 下约 400 TPS，PG 单机游刃有余；同一账户高并发时 `wallets` 行锁是潜在热点，但单行 UPDATE 仅持锁微秒级，实测可到数千 TPS/行。**到 1 万+ QPS 再考虑** Redis Lua 预扣 + 异步落库的方案。

### 7.10 促销引擎

V1 把所有活动叫 Promotion，但它们作用在**两个不同的面**，必须分开：

| 面 | 类型 | 例子 | 影响 |
|---|---|---|---|
| **成本面（side=cost）** | `cost_free`、`cost_discount` | DeepSeek 9 月官方免费；上游给我们打 7 折 | 只改变 `cost_amount`（平台毛利），**用户价格不变**；路由的 cost 因子可以利用它把流量导向免费渠道 |
| **售价面（side=sell）** | `price_discount` | 限时 5 折、VIP 8 折、企业协议价 | 改变 `charged_amount` |
| | `free_quota` | 每天 100 万 Token 免费 | 在额度内 `charged_amount=0`，超出部分正常计费 |
| | `credit_grant` | 注册送 5 元、邀请送 | 发放 `credit_grants`，作为赠送余额扣费 |

"上游免费时是否让用户也免费"是**运营决策**：运营需要同时配置一条 cost 面 `cost_free` 和一条 sell 面 `price_discount(1.0)`。系统不会自动传递。

匹配与叠加规则：

1. 在 Quote 阶段根据 `(模型, 渠道集合, 账户 tier, 注册天数, 时间)` 选出候选促销，按 `priority` 排序。
2. 非 stackable 的促销只取最高优先级一条；stackable 的折扣相乘，但**最低不低于成本价**（可配置保护）。
3. `free_quota` 计数：Redis `INCRBY promo:{id}:{account}:{day}` 做实时判断，Settle 时同步写入 `promotion_counters`（同一事务）。
4. 活动总预算 `budget_total`：Settle 时 `UPDATE promotions SET budget_used = budget_used + x WHERE budget_used + x <= budget_total`，失败则该促销对本请求失效（用户按原价），防止被刷穿。
5. 注意：如果 cost 面免费的渠道恰好是 `free` 分组专用渠道，要单独配置限流——上游免费模型通常有严格速率限制。

### 7.11 钱包、充值与对账

- `internal/wallet` 是**唯一**能写 `wallets` / `ledger_entries` / `credit_grants` 的模块，对外只暴露 `Hold / Capture / Release / Credit / Refund` 等方法。
- 充值：`payment_orders(order_no UNIQUE, account_id, amount, channel, status)`；支付回调**验签 + 按 order_no 幂等**，成功后在同一事务中 `cash_balance += amount` 并写 ledger。
- 退款：只退现金余额，赠送余额不可退/不可提现。
- 赠送余额过期：worker 每小时扫描过期 grant，写 `grant_expire` 流水并扣减 `bonus_balance`。
- **三类对账（worker 每日执行）**：
  1. 内部一致性：`wallets.cash_balance = Σ ledger(cash)`，`bonus_balance = Σ grants.remaining`，`frozen = Σ reservations(held)`。
  2. 请求 vs 账本：`Σ request_logs.charged_amount = Σ ledger(consume)`。
  3. 成本 vs 上游账单：按渠道、按天对比 `Σ cost_amount` 与上游控制台/账单 API 数据，差异 > 1% 告警。

### 7.12 限流与风控

| 维度 | 算法 | 存储 |
|---|---|---|
| Key / 账户 RPM | GCRA（`redis_rate`） | Redis |
| Key / 账户 TPM | 入口按 `EstInputTokens + ReserveOutputTokens` 扣减，结算时按实际值修正（多退少补） | Redis |
| 并发数 | 租约：`ZADD concur:{acct} {expire_ts} {request_id}`，结束时 ZREM，过期自动清理 | Redis |
| IP 级（控制台注册/登录） | 滑动窗口 | Redis |
| Redis 不可用 | **降级为进程内令牌桶**（按实例数切分限额），不要因为限流组件故障导致全站 5xx | 内存 |

风控（Phase1 规则引擎即可，不需要"Risk Score 模型"）：

- 注册：手机号/邮箱验证 + 图形验证码 + 同 IP/设备 24h 注册数限制；赠送余额仅在验证通过后发放。
- 赠送余额可用模型范围（`credit_grants.model_scope`），避免新用户拿 5 元全部刷昂贵模型。
- 异常检测（worker 每分钟聚合）：单账户消费速率突增、大量 `client_cancel`（疑似断连白嫖）、大量 400（疑似探测）→ 自动降级 tier 或冻结并通知人工。
- 封禁名单写 Redis，网关本地缓存 10s。

### 7.13 请求日志与审计

- 网关在请求结束后把 `RequestLog` 结构放入**有界 channel**，后台 goroutine 每 500 条或 1 秒用 `pgx.CopyFrom` 批量写入。
- channel 满时：**计费相关数据绝不能丢**——计费已在 Settle 事务中同步落库（ledger + outbox），日志丢失可从 outbox 重建；普通访问日志可降级丢弃并计数告警。
- Phase2 起 worker 把分区数据同步到 ClickHouse，控制台的"用量分析"查询全部走 ClickHouse。
- **管理员操作审计**：`admin_audit_logs(actor, action, target, before, after, ip, at)`，价格/促销/Key/余额调整全部记录，且余额调整必须走 `adjust` 类型流水，禁止直接改表。

### 7.14 可观测性

Prometheus 指标（**标签只到 model / channel / provider 级，不带 user/key**）：

```text
gateway_requests_total{endpoint, model, status}
gateway_request_duration_seconds{endpoint, model}              # histogram
gateway_ttft_seconds{model, channel}
upstream_requests_total{provider, channel, error_class}
upstream_key_cooldowns_total{provider}
breaker_state{channel}                                          # 0 closed / 1 half / 2 open
billing_reserve_rejected_total{reason}
billing_settle_duration_seconds
billing_unsettled_reservations                                  # held 且超时的数量，> 0 告警
billing_usage_estimated_ratio{channel}
revenue_micro_total{model} / cost_micro_total{channel}          # 实时毛利
tokens_total{model, meter}
```

用户维度排行、账户消费趋势等高基数分析：走 `request_logs` / ClickHouse，由 Grafana 的 SQL 数据源展示。

Trace：OpenTelemetry，span 覆盖 auth → reserve → route → upstream(每次尝试一个 span) → settle；上游调用 span 属性带 channel_id、key_id（不带密钥）。

告警（最小集）：渠道熔断打开、Key 批量失效、`unsettled_reservations > 0`、对账差异、毛利率为负的模型、5xx 率 > 1%、P99 网关开销 > 50ms。

### 7.15 安全

- **上游 Key 加密**：信封加密。每个 Key 一个随机 DEK（AES-256-GCM），DEK 用 KEK 加密后存库；KEK 来自云 KMS 或启动时注入的环境变量/Secret。网关启动时解密到内存，**日志、错误、pprof、panic 堆栈中禁止出现明文**（为 Key 类型实现 `String()`/`LogValue()` 返回掩码）。
- **SSRF**：`provider_accounts.base_url` 只允许管理员配置，并校验域名白名单、禁止内网地址；上游 HTTP 客户端的 Dialer 做 IP 黑名单检查（防 DNS rebinding）。
- **管理后台**：RBAC（super_admin / operator / finance / support），关键操作（改价、调余额、导出 Key）二次确认 + 审计；后台与网关使用不同域名与鉴权体系。
- **控制台**：argon2id 密码、登录限速、可选 TOTP；Session 存 Redis，支持强制下线。
- **请求体**：大小限制、JSON 深度限制；`user` 字段等透传给上游的标识符替换为平台内的匿名化 ID。
- **依赖与构建**：`govulncheck` 进 CI；镜像使用 distroless，非 root 运行。

### 7.16 上游模型价格实时同步

#### 7.16.1 问题与目标

上游调价、限时免费、闲时折扣、新模型上架、旧模型下线都是常态，而且直接影响毛利。难点在于：

| 难点 | 说明 |
|---|---|
| 没有统一价格接口 | OpenAI、Anthropic、DeepSeek、国内大部分厂商只在网页公布价格，没有机器可读的价格 API |
| 单位与币种各不相同 | 美元/token（字符串）、元/百万 token、元/千 token、按次、按秒 |
| 计价维度多 | 缓存读写、推理、按上下文分档、按时段（闲时折扣）、Batch/Priority 等服务等级 |
| 提前公告、定时生效 | "10 月 1 日起调整"——需要**预约生效**，不能等到当天再改 |
| 挂牌价 ≠ 我们的实际价 | 合同折扣、充值返点、代金券 |
| 来源可能出错 | 网页改版导致解析错位、少一个 0，**一次错误的自动生效就可能造成大额亏损** |
| 风险不对称 | 上游降价没同步 = 少赚；上游涨价没同步 = 每笔请求都亏钱 |

"实时"在这里的准确含义：

- **发现延迟**：结构化来源 ≤ 15 分钟；网页来源 ≤ 1 小时；实际成本漂移 ≤ 10 分钟（基于请求级成本回传）。
- **生效延迟**：变更确认后，全部网关 **≤ 1 秒**切换到新价格（复用 §7.3 的 Catalog 热加载）。
- **兜底**：在变更被发现或审批之前，**毛利守护**保证不会持续亏损（§7.16.7）。

**核心原则：成本价（cost）可以高度自动化；售价（sell）变动面向用户，必须受策略与公告约束，二者分开治理。**

#### 7.16.2 价格来源分级

| 级别 | 来源 | 示例 | 可信度 | 用途 |
|---|---|---|---|---|
| **L1** 实际结算回传 | 响应中直接带费用；上游账单/用量明细接口 | OpenRouter 响应 `usage.cost`（以其 credits 计，BYOK 场景另有 `usage.cost_details.upstream_inference_cost`）；各云厂商账单明细 API | 最高（反映**我们账号**的真实价格） | 漂移检测、对账；不直接生成价格表 |
| **L2** 官方机器可读 API | 上游模型/价格接口 | OpenRouter `GET /api/v1/models`（公开无需鉴权，`pricing.prompt / completion / input_cache_read / input_cache_write / internal_reasoning / web_search / image …`，单位 USD/token；`pricing.overrides` 表达按 `min_prompt_tokens` 分档和按 UTC 时段计价；`expiration_date` 表示模型下线日期）；`/api/v1/models/{id}/endpoints` 给出各底层供应商的价格与折扣 | 高 | 可自动生效（受阈值约束） |
| **L3** 官方定价网页 / 公告 | HTML 页面、更新日志、公告 RSS、邮件 | DeepSeek、OpenAI、Anthropic、阿里百炼、火山方舟、SiliconFlow 定价页 | 中（解析易错） | 生成变更提案，**默认需人工审批** |
| **L4** 社区数据集 | 开源维护的价格库 | LiteLLM `model_prices_and_context_window.json`（`input_cost_per_token`、`output_cost_per_token`、`cache_read_input_token_cost`、`*_batches`、`*_priority` 等）、models.dev `api.json`（`cost.input/output/cache_read`，单位 USD/百万 token） | 中低（有滞后，非官方） | **只做交叉校验**，永不单独生效 |
| **L5** 人工录入 | 商务合同价、渠道经理通知、控制台手工修改 | 协议价、代金券、临时免费 | 高（有责任人） | 走审批流生效 |

> 各厂商是否提供价格/账单 API、字段格式都可能变化，接入每个来源前需按其最新文档确认；上表字段已按 2026-09 时 OpenRouter 公开接口的实际返回核对。抓取网页须遵守对方的服务条款与 robots 规则，控制频率。

#### 7.16.3 同步流水线架构

```text
          ┌───────────── worker（leader 选举，单实例执行）─────────────┐
          │                                                         │
 调度器 ──► Fetcher（按来源插件：openrouter / litellm / html:deepseek / billing:xxx …）
 (cron)   │     │  原始内容存对象存储（证据留存，content_hash 去重）          │
          │     ▼                                                   │
          │ Normalizer：统一为 PriceSpec（元/百万 token 或按次；原币种；    │
          │             分档/时段/服务等级；预约生效时间；过期时间）          │
          │     ▼                                                   │
          │ Mapper：上游模型 ID → provider_account + upstream_model → 渠道  │
          │         匹配不到 → "新模型发现"队列（不自动上架）               │
          │     ▼                                                   │
          │ Validator：合法性检查 + 多来源交叉校验 + 连续确认                │
          │     ▼                                                   │
          │ Differ：与当前生效及已预约的 cost book 比较 → 生成变更提案        │
          │     ▼                                                   │
          │ Policy：自动生效 / 待审批 / 拦截（§7.16.7）                     │
          │     ▼                                                   │
          │ Publisher：同一事务插入新 price_book 版本(effective_from)       │
          │            + config_version++ + NOTIFY catalog_changed        │
          └─────────────────────────────┬───────────────────────────┘
                                        ▼
          gateway 热加载快照（≤1s） → 毛利守护重算（§7.16.7） → 通知运营
                                        ▲
          L1 实际成本回传 ──► 漂移检测（§7.16.8）──► 触发重新抓取 / 生成提案
```

各来源的调度频率建议：L2 每 10 分钟；L3 每 1 小时；L4 每 6 小时；L1 账单接口按上游支持的粒度（小时/天）；另外任何漂移告警都会**立即触发**对应模型的重新抓取。

#### 7.16.4 数据表

```sql
CREATE TABLE price_sources (
    id            BIGSERIAL PRIMARY KEY,
    provider_id   BIGINT REFERENCES providers(id),
    level         TEXT NOT NULL,            -- L1 / L2 / L3 / L4 / L5
    kind          TEXT NOT NULL,            -- api / html / dataset / billing / manual
    fetcher       TEXT NOT NULL,            -- 插件名：openrouter_models / litellm / html_deepseek …
    url           TEXT,
    schedule      TEXT NOT NULL,            -- cron 表达式
    config        JSONB NOT NULL DEFAULT '{}',  -- 解析规则、模型名映射表、单位声明
    enabled       BOOLEAN NOT NULL DEFAULT true,
    last_success_at TIMESTAMPTZ,
    last_content_hash BYTEA
);

CREATE TABLE price_observations (           -- 只追加：每次抓取到的"某模型某时刻的价格"
    id            BIGSERIAL PRIMARY KEY,
    source_id     BIGINT NOT NULL REFERENCES price_sources(id),
    upstream_model TEXT NOT NULL,
    spec          JSONB NOT NULL,           -- 归一化后的 PriceSpec
    spec_hash     BYTEA NOT NULL,
    raw_object    TEXT,                     -- 原始内容在对象存储中的路径（审计证据）
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON price_observations (source_id, upstream_model, observed_at DESC);

CREATE TABLE price_change_requests (
    id              BIGSERIAL PRIMARY KEY,
    channel_id      BIGINT NOT NULL REFERENCES channels(id),
    current_book_id BIGINT REFERENCES price_books(id),
    proposed_spec   JSONB NOT NULL,
    diff            JSONB NOT NULL,         -- 每个计量项的旧价/新价/变化率
    max_change_ratio NUMERIC(10,4) NOT NULL,
    direction       TEXT NOT NULL,          -- up / down / mixed / new / removed
    evidence        BIGINT[] NOT NULL,      -- price_observations.id 列表
    impact_7d       BIGINT,                 -- 按近 7 天用量估算的成本变化（微元）
    effective_from  TIMESTAMPTZ NOT NULL,
    status          TEXT NOT NULL,          -- pending / auto_approved / approved / rejected / applied / superseded / blocked
    decided_by      BIGINT,
    decided_at      TIMESTAMPTZ,
    applied_book_id BIGINT REFERENCES price_books(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE fx_rates (
    base          TEXT NOT NULL,            -- USD
    quote         TEXT NOT NULL,            -- CNY
    rate          NUMERIC(12,6) NOT NULL,
    source        TEXT NOT NULL,
    effective_date DATE NOT NULL,
    PRIMARY KEY (base, quote, effective_date)
);
```

价格表本身永远**不修改、只新增版本**：回滚 = 以旧价格再发布一个新版本。

#### 7.16.5 Go 实现要点

模块：`internal/pricesync`（worker 进程内运行）。

```go
package pricesync

// PriceSpec 是所有来源归一化后的统一表示。
type PriceSpec struct {
    Currency      string        // 原币种：USD / CNY
    Components    []Component
    EffectiveFrom *time.Time    // 预约生效；nil = 立即
    ExpiresAt     *time.Time    // 模型下线时间 / 限时价格结束时间
}

type Component struct {
    Meter          string          // input / input_cache_read / output / request …
    Unit           string          // per_1m_tokens / per_request / per_image / per_second
    ServiceTier    string          // default / batch / priority / flex
    MinInputTokens int             // 分档下限
    WindowStartMin *int16          // 时段计价（UTC 分钟）
    WindowEndMin   *int16
    UnitPrice      decimal.Decimal // 原币种
}

type Observation struct {
    UpstreamModel string
    Spec          PriceSpec
    RawObject     string
}

// Fetcher 每个来源一个实现，只负责"取数 + 归一化"，不做任何决策。
type Fetcher interface {
    Name() string
    Fetch(ctx context.Context, src *Source) ([]Observation, error)
}
```

OpenRouter 归一化示例（USD/token 字符串 → USD/百万 token，全程 decimal，禁止 float）：

```go
var orMeters = map[string]string{
    "prompt":             "input",
    "completion":         "output",
    "input_cache_read":   "input_cache_read",
    "input_cache_write":  "input_cache_write",
    "internal_reasoning": "output_reasoning",
    "image":              "input_image",
    "web_search":         "request_web_search", // 按次
}

func normalizeOpenRouter(p map[string]json.RawMessage) ([]Component, error) {
    var out []Component
    for field, meter := range orMeters {
        raw, ok := p[field]
        if !ok {
            continue
        }
        var s string
        if err := json.Unmarshal(raw, &s); err != nil {
            return nil, fmt.Errorf("pricing.%s: %w", field, err)
        }
        v, err := decimal.NewFromString(s)
        if err != nil {
            return nil, fmt.Errorf("pricing.%s=%q: %w", field, s, err)
        }
        c := Component{Meter: meter, ServiceTier: "default", UnitPrice: v}
        if strings.HasPrefix(meter, "request_") {
            c.Unit = "per_request"
        } else {
            c.Unit, c.UnitPrice = "per_1m_tokens", v.Mul(decimal.NewFromInt(1_000_000))
        }
        out = append(out, c)
    }
    // pricing.overrides：min_prompt_tokens → 分档；utc_start/utc_end → 时段。
    // 时段字段的编码格式（观测到如 1600 / 0）按 OpenRouter 文档解析并以单测锁定。
    return out, nil
}
```

HTML 来源：`PuerkitoBio/goquery` 按每个站点配置的选择器提取表格，存到 `price_sources.config`；页面需要 JS 渲染时才用 `chromedp`。**可以**用 LLM 辅助从页面抽取结构化价格作为 L3 的候选，但结果只进入审批流，永不自动生效。

调度：`robfig/cron/v3`，每个来源独立超时与重试；多 worker 实例通过 PG advisory lock 选主，避免重复抓取。

#### 7.16.6 校验规则（Validator）

| 规则 | 处理 |
|---|---|
| 单位/数量级异常：相对当前价变化 ≥ 10 倍或 ≤ 1/10 | **拦截**（大概率是单位或解析错误），告警人工处理 |
| 价格为 0 | 不直接视为免费：必须来源显式标注免费 + 带结束时间；否则进入审批。生效后在成本侧等价于一条 `cost_free` 促销 |
| 模型在来源中消失 | **不当作价格为 0**；标记 `removed`，告警（可能是下线），不修改价格 |
| L3 网页来源 | 需**连续 2 次抓取结果一致**才生成提案（防止灰度页面、临时错误） |
| 多来源冲突 | 同一模型 L2 与 L4 差异 > 5% → 提案附加"来源冲突"标记，强制人工审批 |
| 结构性检查 | 缓存读价应 ≤ 输入价；分档价格应随档位单调不减；违反则仅告警 |
| 抓取失败 / 结构变化 | 解析出的模型数骤降 > 30% 视为页面改版，整批丢弃并告警，**不产生任何变更** |

#### 7.16.7 生效策略（Policy）与毛利守护

**成本价（cost book）生效策略**（每个来源、每个渠道可覆盖）：

| 变化 | 来源 | 默认策略 |
|---|---|---|
| 降价（所有计量项都不升） | L2 / L5 | 自动生效，通知运营 |
| 降价 | L3 | 审批（防止解析错误导致成本被低估、毛利报表失真） |
| 涨价 ≤ 20% | L2 | **自动生效**（宁可先按高成本计算），同时触发毛利守护 |
| 涨价 > 20% 或来源为 L3 | — | 审批；审批前毛利守护已经在保护 |
| 预约生效（公告"X 日起调整"） | 任意 | 审批后写入 `effective_from = X`，快照提前加载，到点无缝切换 |
| 新模型 | 任意 | 进入"待上架"，由运营创建虚拟模型/渠道和售价后才对用户可见 |
| 模型将下线（`expiration_date`） | L2 | 提前 14 天告警；提前 3 天把该渠道权重逐步降到 0；到期自动停用 |

**售价（sell book）策略**——每个虚拟模型配置一种：

| 策略 | 行为 | 适用 |
|---|---|---|
| `fixed`（默认） | 售价只由人工修改；成本变化只触发告警 | 大多数模型 |
| `cost_plus` | 售价 = 所有启用渠道中**最高**成本 × (1 + markup)，向上取整到价格刻度；**降价立即生效，涨价按 `notice_period`（如 72 小时）预约生效**并通知用户 | 长尾模型、自动上架模型 |
| `pass_through` | 售价 = 成本 × (1 + markup)，随成本同步变化（含免费） | 明确告知用户"跟随上游"的模型 |

售价变更同样写入新版本并走公告；请求开始时锁定价格版本（Quote/Hold 中记录 `price_book_id`），**请求进行中不会切换价格**，跨越生效时间点的长请求按开始时间计价。时段价格按请求开始时间（UTC）匹配。

**毛利守护（Margin Guard）**——每次快照重建、每次汇率更新时对每个渠道计算：

```text
对每个计量项 m：margin_m = sell_m − cost_m × fx_rate × cost_multiplier
典型毛利 = 按该虚拟模型近 7 天实际 token 结构加权的毛利率
```

| 条件 | 动作（可按模型配置） |
|---|---|
| 某计量项 margin_m < 0，或典型毛利 < `min_margin` | 告警 |
| 典型毛利 < 0 且同优先级有其他健康渠道 | 该渠道的路由 cost 因子降到 0.1（流量自动转向），告警 |
| 典型毛利 < 0 且无替代渠道 | 按配置：继续服务（运营承担）/ 对新请求返回 503 / 自动按 `cost_plus` 预约调价 |
| 结算时单笔 `cost_amount > charged_amount`（且非促销请求） | 计数 `billing_negative_margin_total{channel}`，超过阈值告警 |

这样即使上游**未公告**就调价，平台最多在"发现延迟"窗口内少量亏损，而不是一直亏到有人看报表时才发现。

#### 7.16.8 实际成本回传与漂移检测（L1 闭环）

挂牌价会错、合同价会漏、还有未建模的计量项——唯一真实的是**上游实际收了多少钱**：

1. **请求级**：适配器从响应中提取上游回传的费用（如 OpenRouter 的 `usage.cost`），写入 `request_logs.upstream_cost`。网关内存中按渠道维护滑动窗口（最近 200 次请求），计算 `drift = Σupstream_cost / Σ本地计算成本 − 1`。
2. **账单级**：对提供账单/用量明细 API 的上游，worker 按小时/天拉取，按"渠道 × 模型 × 天"与 `Σ cost_amount` 比较（即 §7.11 的第三类对账）。
3. **触发**：|drift| > 2%（请求级，样本量足够）或 > 1%（账单级）→ 立即重新抓取该模型的 L2/L3 来源；若来源价格未变化，则生成 `direction=unknown` 的提案，附带漂移证据，交由人工排查（常见原因：合同折扣未配置、缓存计价规则变化、新增计量项）。

指标：`price_cost_drift_ratio{channel}`（gauge）。

#### 7.16.9 汇率

- `fx_rates` 每日（或每小时）从可靠来源同步（如中国外汇交易中心人民币汇率中间价、银行或支付渠道公布的汇率），与价格同样走校验（单日变化 > 3% 告警并暂停自动应用）。
- 结算时使用当日汇率快照，记录在 `request_logs.fx_rate`。
- 美元成本的渠道，`cost_plus` 策略的 markup 中应包含汇率缓冲（如 2%）；汇率变化同样触发毛利守护重算。

#### 7.16.10 控制台与运维

- **价格变更中心**：待审批列表，展示逐计量项 diff、来源证据（原始页面快照/接口响应链接）、多来源对照、近 7 天影响估算、毛利变化；支持批准（可修改生效时间）、驳回、批量处理。
- **价格时间线**：每个渠道/虚拟模型的全部价格版本、生效区间、操作人、来源，可一键"以某历史版本重新发布"。
- **通知**：飞书/钉钉/企业微信/邮件；售价变更可对用户发送公告（控制台站内信、邮件，Phase2 起支持 Webhook）。
- **对外价格**：走 `GET /v1/catalog`（Phase 2 已实现，免鉴权公开目录，见 §7.1、§7.16.4 附近的 `virtual_model_metadata`），返回当前生效的售价（`sell_price.components`）；已预约但尚未生效的未来售价暂不通过这个接口暴露（`internal/catalog.Store` 只加载 `effective_from <= now()` 的最新版本），要看价格时间线仍然走 `cmd/admin` 的价格变更中心。`GET /v1/models` 保持纯 OpenAI 兼容格式（`{id, object, created, owned_by}`），不叠加售价字段——两个接口分工明确：`/v1/models` 给已持有 API Key 的客户端做"这把 Key 能调用哪些模型 ID"查询，`/v1/catalog` 给未注册的访客/控制台模型库做展示，鉴权方式也不同（前者需要 API Key，后者免鉴权）。

监控与告警：

```text
pricesync_fetch_total{source, status}
pricesync_last_success_timestamp{source}      # 超过 3 个周期未成功 → 告警（防止"静默失效"）
pricesync_change_requests_pending              # 待审批积压 > 0 且超过 4 小时 → 提醒
pricesync_blocked_total{source, rule}
channel_margin_ratio{channel}
price_cost_drift_ratio{channel}
```

测试：每个 Fetcher 使用录制的真实响应/页面作为 fixture（golden）；单位换算、时段跨零点、分档边界、10 倍拦截、模型消失、页面改版（模型数骤降）均需单测覆盖；Publisher 与 Catalog 热加载做集成测试，验证预约生效在边界时刻前后的请求分别使用新旧价格。

---

## 8. 部署与运维

### 8.1 Phase1 部署拓扑

```text
            ┌──────────────┐
Internet ──►│ SLB / Nginx  │  proxy_buffering off; proxy_read_timeout 600s;
            └──────┬───────┘  HTTP/1.1 keepalive 到后端
         ┌─────────┼─────────┐
     gateway×2  admin×2   worker×1(leader 选举：PG advisory lock)
         │         │         │
         └────┬────┴────┬────┘
        PostgreSQL 16   Redis 7
        (主从+PITR)     (主从/哨兵，或云托管)
```

- 网关对外与上游之间建议部署在**离上游近的区域**（国内上游与海外上游分别部署网关实例，由路由层的 `region` 字段区分，Phase3 多区域时再做完整方案）。
- 配置：进程配置用环境变量/配置文件；业务配置全部在 DB，由控制台管理。
- 发布：滚动发布 + 优雅退出（等待在途流）；数据库迁移遵循"先加后删"的向后兼容原则。

### 8.2 容量与调优要点

- `GOMAXPROCS` 与容器 CPU 配额对齐（Go 1.25+ 默认感知 cgroup；旧版本用 `automaxprocs`）。
- 上游 Transport：`MaxIdleConnsPerHost` 设为预期并发（如 256），否则高并发下频繁建连导致 TTFT 抖动。
- 每个流式连接的内存开销主要在缓冲区，控制在 ~64KB 以内，1 万并发 ≈ 640MB。
- PG 连接池 `pgxpool` `MaxConns` = 实例数 × 20 以内，配合 PgBouncer（事务模式注意 `LISTEN` 需要独立直连）。

---

## 9. 测试策略

| 层级 | 内容 | 工具 |
|---|---|---|
| 单元 | `pricing` 纯函数：所有 meter、分档、舍入、折扣下限；`router` 选择分布（统计检验权重比例） | `testing`、表驱动、`testing/quick` |
| 适配器 golden | 各上游真实响应样本（非流式/流式/错误/含缓存与推理 token）→ 统一结构 | golden files |
| 计费属性测试 | 随机并发 Reserve/Settle/Release 序列后，不变式成立：`frozen = Σ held`、`balance = Σ ledger`、无重复结算 | `testcontainers-go` + 真实 PG |
| 集成 | 网关 + mock 上游：429 切 Key、5xx 切渠道、首字节后断流、客户端断开、上游不返回 usage | `test/mockupstream` |
| 混沌/压测 | 2000 并发流、上游 30% 故障、Redis 宕机降级、网关滚动重启时在途流结算 | k6 / vegeta |
| 对账回归 | 构造一天流量，跑对账任务，差异必须为 0 | e2e |
| 价格同步 | Fetcher golden、单位换算、时段跨零点、10 倍拦截、页面改版丢弃、预约生效边界、毛利守护触发 | fixture + testcontainers |

上线门槛：计费不变式测试 + 并发透支测试（§1.3 A1 场景）必须通过。

---

## 10. 实施路线图（重新划分）

原路线图把"多 Provider"放在 Phase2，但平台的核心价值就是多 Provider 路由，且适配器/路由是第一天就要定型的抽象。调整如下：

### Phase 1：可收费的 MVP（约 6–8 周，2–3 名 Go 工程师）

- 工程骨架：三进程、配置、日志、指标、迁移、CI（lint、test、govulncheck）
- 账户体系：account / user / member、注册登录、邮箱/手机验证
- API Key：生成、HMAC 校验、吊销广播、模型/IP 限制
- Catalog 快照与热加载
- 适配器：`openai`（覆盖 DeepSeek、SiliconFlow、火山、百炼、OpenRouter）+ `anthropic`
- 路由：硬过滤 + 优先级 + 加权随机；Key 冷却；熔断；首字节前重试
- 计费：Price Book/Component、Reserve/Settle/Release、ledger、reservation 回收、tokenizer 兜底
- 钱包：现金 + 赠送余额；注册赠送（验证后发放）；手动充值（后台调账）
- 限流：RPM / TPM / 并发
- 请求日志（PG 分区）+ 基础 Grafana 看板 + 核心告警
- 对账：内部一致性对账
- 价格同步最小闭环：价格变更中心（L5 人工录入 + 审批 + 预约生效）、OpenRouter L2 Fetcher、汇率同步、毛利守护（告警 + 路由降权）、请求级成本回传漂移检测

**验收标准**：并发透支测试通过；账本与余额对账为 0 差异；单渠道故障用户无感；网关 P99 开销 < 15ms。

### Phase 2：运营能力（约 4–6 周）

- 促销引擎：cost 面/sell 面、free_quota、活动预算
- 在线支付（微信/支付宝/Stripe）与退款、发票信息
- Gemini 适配器、Embeddings / Images / Audio 端点、`/v1/messages` 入口
- 提示缓存亲和路由、成本因子路由
- ClickHouse 用量分析、控制台用量报表
- 上游账单对账（L1 账单级漂移检测）
- 价格同步扩展：主要厂商定价页 L3 Fetcher、LiteLLM/models.dev L4 交叉校验、`cost_plus` 售价策略与用户调价公告、模型下线自动处置
- 风控规则引擎（异常检测、自动降级）

### Phase 3：智能与规模

- A/B Routing、基于实时毛利的 Cost Optimizer
- 多区域部署（国内/海外网关分离，数据按区域驻留）
- 新模型自动发现与一键上架（基于 §7.16 的"待上架"队列，自动生成虚拟模型/渠道/售价草稿，人工确认后发布）
- 高 QPS 优化：Redis 预扣 + 异步落库（仅在单库成为瓶颈时）

### Phase 4：企业 SaaS

- 组织内 Team / 项目级预算、审批流、授信额度与月结账单
- SSO（OIDC/SAML）、审计导出、专属渠道、SLA

> 由于 V2 从第一天起就有 `account` 与 `account_members`，Phase4 只是增加功能而不是数据迁移。

---

## 11. 风险与合规（务必在立项阶段评估）

| 风险 | 说明 | 应对 |
|---|---|---|
| 上游服务条款 | 多数模型厂商（OpenAI、Anthropic、Google 等）的条款对**转售、多账号池化规避速率限制、向不支持地区提供服务**有明确限制；违反可能导致账号批量封禁、资金冻结 | 与上游签署经销/企业协议，或通过官方授权的聚合方（如云厂商模型市场、OpenRouter）接入；Key 池只用于合同允许的配额，不用于规避限制 |
| 国内监管 | 面向境内公众提供生成式 AI 服务涉及《生成式人工智能服务管理暂行办法》、算法/大模型备案、实名制、内容安全、日志留存等要求；境外模型对境内提供服务存在额外限制 | 法务前置评估；境内只接入已备案模型；接入内容安全审核；按要求留存日志 |
| 数据与隐私 | 用户 prompt 可能含个人信息/商业机密，经过平台转发到多个第三方 | 默认不存正文；隐私政策明确列出子处理方；企业客户可限定渠道/区域 |
| 资金 | 预付费余额涉及资金沉淀；赠送余额、退款规则需在协议中明确 | 用户协议、退款政策、财务对账流程 |
| 上游价格变动 | 上游涨价而售价未及时调整 → 负毛利 | 毛利为负自动告警；成本价同步任务；可配置"成本高于售价时暂停该渠道" |

---

## 附录 A：统一错误码（OpenAI 兼容格式）

```json
{"error": {"message": "Insufficient balance.", "type": "insufficient_quota", "code": "insufficient_balance", "request_id": "01J8Z..."}}
```

| HTTP | code | 场景 |
|---|---|---|
| 400 | `invalid_request` / `context_length_exceeded` / `unsupported_capability` | 参数错误、超长、模型不支持 tools/vision |
| 401 | `invalid_api_key` | Key 不存在/已吊销/过期 |
| 402 | `insufficient_balance` | 预扣失败 |
| 403 | `model_not_allowed` / `account_suspended` / `ip_not_allowed` | 权限与风控 |
| 404 | `model_not_found` | 虚拟模型不存在或对该 tier 不可见 |
| 429 | `rate_limit_exceeded` / `concurrency_limit_exceeded` | 平台侧限流（带 `Retry-After`） |
| 502 | `upstream_error` | 所有候选渠道均失败 |
| 503 | `no_available_channel` | 无健康渠道 |
| 504 | `upstream_timeout` | 超过总 deadline |

## 附录 B：网关进程配置示例

```yaml
gateway:
  addr: ":8080"
  max_body_bytes: 20971520
  stream_idle_timeout: 120s
  shutdown_grace: 300s
retry:
  max_attempts: 3
  total_deadline: 90s
  budget_ratio: 0.2
billing:
  reserve_output_cap: 8192
  reservation_ttl: 30m
  rounding: ceil_micro
ratelimit:
  redis_fail_open_local: true
postgres:
  dsn: ${PG_DSN}
  max_conns: 40
redis:
  addr: ${REDIS_ADDR}
secrets:
  kek_source: env          # env / aliyun-kms / aws-kms
  kek_env: UFT_KEK
  api_key_pepper_env: UFT_KEY_PEPPER
observability:
  otlp_endpoint: ${OTLP_ENDPOINT}
  metrics_addr: ":9090"
```

## 附录 C：V1 → V2 概念对照

| V1 | V2 | 变化 |
|---|---|---|
| 12 个微服务 | 3 个进程的模块化单体 | 降低复杂度，保留模块边界 |
| users.plan_id / wallet.user_id | accounts + account_members + wallets(account_id) | 多租户前置 |
| user_api_key.key_hash | api_keys.key_hmac + prefix + 策略字段 | 可索引、可展示、细粒度策略 |
| provider_api_key（含运行时统计） | provider_accounts + provider_keys（仅配置）+ 内存/Redis 运行时状态 | 消除热点写 |
| Model Mapping | channels（含 upstream_model、priority、weight、能力） | 路由最小单元 |
| model_pricing（profit_rate + sell 冗余） | price_books(sell/cost) + price_components（多计量项、分档、时段、服务等级、版本） | 可表达真实计价 |
| effective_time（手工改价） | price_sources → observations → change_requests → 版本发布 + 毛利守护 + 漂移检测 | 价格自动同步、分级生效、可审计 |
| promotion（混合语义） | promotions(side=cost/sell) + promotion_counters + credit_grants | 成本面/售价面分离，带预算 |
| Usage + Request Session | request_logs（分区，含计费快照与尝试轨迹） | 单一事实来源 |
| Transaction | ledger_entries（只追加、幂等、区分现金/赠送） | 可对账 |
| Wallet Check → 调用 → 扣费 | Quote → Reserve → Relay → Settle/Release | 杜绝透支 |
| Score 取最高 | 过滤 → 优先级 → 亲和 → 加权 P2C | 稳定、可解释 |
