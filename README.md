# uFreeTokens

AI 多 Provider API 平台：统一 OpenAI 兼容接口、多渠道路由、计费与钱包引擎、促销与上游价格实时同步。

设计文档：

- [`docs/uFreeTokens Go 后端优化技术方案 V2.md`](docs/uFreeTokens%20Go%20后端优化技术方案%20V2.md) —— 完整技术方案（架构、数据库设计、计费引擎、路由算法、价格同步等）

## 当前状态

Phase1 的核心链路已经打通并有端到端测试覆盖（见技术方案路线图）：

- `cmd/gateway`：数据面入口。`/v1/chat/completions` 已接入完整链路——鉴权 → 预扣费用
  → 路由选渠道/Key → 转发上游（OpenAI 兼容协议，支持流式/非流式）→ 失败时按错误类别
  换 Key/换渠道重试（§7.6-7.7，见下）→ 按实际用量结算。
  `/v1/completions`、`/v1/embeddings` 尚未实现，返回 `503 not_implemented`。
- `cmd/admin`：账户/API Key/Provider/渠道/虚拟模型/售价管理的 HTTP 接口
  （`internal/admin`）——创建账户会原子初始化一个空钱包；上游 Key 落库前用
  `internal/secretbox` 加密；改价格是发布新版本，不覆盖历史。鉴权已经接入
  一个共享密钥方案：所有业务路由（`/healthz` 除外）都要求
  `Authorization: Bearer <UFT_ADMIN_TOKEN>`（`httpx.RequireBearerToken`，
  常数时间比较），`cmd/admin` 启动时这个环境变量缺失/为空会直接拒绝启动，
  不会静默退化成不鉴权。这不是完整的多用户登录 + RBAC——知道这一个密钥的人
  能做任何操作，没有"谁在操作"的概念，仍然只应该部署在内网/加一层反向代理，
  见 `internal/admin` 包文档。
- `cmd/worker`：定时任务循环（§7.11、§7.13）——回收过期未结算的预扣（网关崩溃留下的
  孤儿 reservation）、保持 request_logs 未来分区就绪、内部一致性对账（钱包余额 vs
  账本、账本 vs 请求日志），发现问题只记日志上报，不自动"纠正"数据。
- `migrations/`：Phase1 核心表结构，已在真实 PostgreSQL 上跑通。
- `internal/pricing`：计价纯函数（多计量项、分档、时段计价、向上取整）。
- `internal/wallet`：Reserve/Settle/Release 两阶段计费引擎（§7.9），幂等、原子冻结，
  集成测试包含**并发透支回归测试**（§1.3 A1 场景：余额只够 1 笔请求时并发发起 50 笔，验证绝不超额冻结）。
  另外提供 `ReclaimExpired`：扫描并释放已过期仍处于 held 状态的预扣（§7.9.3 的孤儿
  reservation 回收），供 worker 定时调用。
- `internal/reconcile`：内部一致性对账（§7.11 第一、二类）——钱包 cash_balance/frozen/
  bonus_balance 是否分别等于账本流水、held 预扣、未过期赠款之和；某个时间窗口内账本
  消费总额与 request_logs 计费总额是否一致。只发现问题、上报，不自动修复。
  第三类对账（成本 vs 上游账单）还没做——不是缺数据了（渠道成本已经能算出来，
  见下），是缺"上游账单从哪来"这一环（§7.16.8 的 L1 实际成本回传/漂移检测，
  见 `internal/pricesync` 一节的范围限制）。
- `internal/secretbox`：上游 Key 的信封加密（AES-256-GCM，§7.15）。
- `internal/catalog`：虚拟模型/渠道/售价/成本价/汇率的内存快照（带 TTL 缓存的
  简化版，完整的 LISTEN/NOTIFY 热加载见 §7.3，留作后续）。成本价挂渠道（§6.4），
  currency='CNY' 直接用，非 CNY 会按 FXRates（§7.16.9，来自 `fx_rates` 表）折算，
  没有对应汇率的币种才会跳过成本记录。
- `internal/adapter`：协议适配器（请求改写、非流式/流式响应解析、用量提取、
  错误分类，§7.4）。openai 协议是直通（覆盖绝大多数 OpenAI 兼容上游）；
  anthropic 和 gemini 协议是真正的双向翻译：
  - anthropic：system 消息拆分、stop→stop_sequences、Messages API 的
    content blocks ↔ OpenAI 的 choices/delta、具名 SSE 事件流
    ↔ chat.completion.chunk、error.type ↔ ErrorClass。
  - gemini：system 消息拆分成 systemInstruction、assistant→model 角色映射、
    generationConfig 字段映射（temperature/topP/maxOutputTokens/
    stopSequences）、非流式走 :generateContent / 流式走
    :streamGenerateContent?alt=sse（Gemini 流没有具名事件也没有结束哨兵，
    就是连接关闭）、error.status ↔ ErrorClass、prompt 被安全策略拦截
    （promptFeedback.blockReason，没有 candidates）映射成 content_filter。

  两者已知限制一致：不支持 tool/function calling（请求里出现 tools 或
  role=tool 直接报错，不做可能错误的静默转换），多模态内容只做直通不做字段
  翻译；gemini 额外不上报 cache 写入量（Gemini 的显式上下文缓存创建走单独
  API，不在每次请求的 usageMetadata 里报告）。
- `internal/pricesync`：上游价格同步流水线（§7.16）里"来了一条价格观测之后"
  的确定性部分——归一化（PriceSpec/Component，含一个 OpenRouter 归一化函数 +
  golden fixture 测试）、校验（数量级异常拦截、零价无到期时间强制审批、缓存价/
  分档单调性告警、L3 来源要求连续两次观测一致、跨来源价格冲突强制审批）、
  比较（Diff：up/down/mixed/new/removed + 最大变化比例）、生效策略
  （§7.16.7 的成本价策略表：L2/L5 降价自动生效，L2 涨价 ≤20% 自动生效，
  其余一律 pending）、发布（自动通过或人工批准后调用 `internal/admin` 发布
  新的成本价版本，支持预约生效）、Mapper（按 provider+upstream_model 找现有
  渠道；一个都找不到时进"新模型发现"队列而不是自动上架，Phase 3）。`cmd/admin`
  暴露了完整的 HTTP 接口：`POST /price-sources`、
  `POST /channels/{id}/price-observations`（已知渠道，跑完整条流水线）、
  `POST /providers/{id}/price-observations`（不知道渠道，走 Mapper：能匹配到
  就对每个匹配渠道跑流水线，匹配不到就排队待发现）、
  `GET /price-change-requests`、`POST /price-change-requests/{id}/approve|reject`、
  `GET /pending-model-listings`、`POST /pending-model-listings/{id}/publish`
  （一键上架：新建虚拟模型 + 渠道 + 成本价 + 按 `sell_markup` 加价算出的售价）、
  `POST /pending-model-listings/{id}/dismiss`。

  已知范围限制（§7.16 本身是个很大的独立子系统，这里先把确定性流水线做对、
  做全，见 `internal/pricesync` 包级注释）：没有真正对接外部数据源的
  Fetcher（不发 HTTP 请求抓 OpenRouter/官网/账单 API，也没有 HTML 抓取/LLM
  辅助抽取）；没有调度器（cron + PG advisory lock 选主未实现，谁在什么时候
  调 `Engine.Ingest` 由调用方决定，目前就是上面那个 HTTP 接口）；不做"模型
  消失"检测（需要定期扫描，依赖尚未实现的调度器）；跨来源冲突检测是简化版
  （不区分具体是 L2 与 L4，笼统按"任意其它来源"比较）；不估算 impact_7d；
  没有毛利守护（Margin Guard）联动路由权重。
- `internal/router`：硬过滤（含熔断/冷却状态）→ 优先级分层 → 层内加权随机的渠道/Key
  选择算法（§7.5），支持按请求排除已失败的渠道/Key，用统计检验测试证明不会出现
  "全部流量挤到一个渠道"的羊群效应。毛利守护的路由降权（§7.16.7）已经接入：
  `internal/catalog` 每次刷新快照时用 SellPriceBooks/CostPriceBooks/FXRates
  纯内存算出每个渠道是否有任一计量项挂牌价结构性亏钱（`Channel.NegativeMargin`，
  缺成本价/缺汇率不算亏钱，只是判断不了），router 对这类渠道的层内有效权重打
  10% 折扣（不是硬性排除，只是同层有健康渠道时流量会被自动挤过去）。这是"挂牌价"
  毛利，不是按近 7 天实际用量加权的"典型毛利"，也没有"无替代渠道时自动降级
  cost_plus/拒绝新请求"那部分（见 `internal/pricesync` 包注释的范围限制）。

  A/B 路由（Phase 3）已经接入：`channels` 表新增 `experiment_key`/
  `variant_label`（要么都填、要么都空，DB 有 CHECK 约束），`cmd/admin` 建渠道
  时可以指定；分流不需要新机制，本来就可以给同一个 `experiment_key` 下的几个
  渠道配相同 `priority`、用现有的 `weight` 分比例。这里补的是"事后能看出哪个
  请求走了哪个分组"——命中渠道的标签会原样记进 `request_logs.experiment_key`/
  `variant_label`（只在请求成功结算时记；失败请求仍然可以用已有的
  `request_logs.channel_id` 关联 `channels` 表查到，不需要额外字段），供按组
  聚合对比成本/延迟/成功率。

  专属渠道（Phase 4 企业 SaaS）也已经接入：`channels.allowed_account_ids`，
  语义和现有的 `allowed_tiers` 完全对称——空 = 公共渠道，非空则只有白名单里的
  账户能路由到它。给企业客户配一条独享渠道，不会影响其它账户原有的可用性
  （它们看不到这条渠道，会落到其它公共渠道，就像它不存在一样）。
- `internal/health`：渠道熔断器（`sony/gobreaker`，进程内）+ 上游 Key 冷却
  （Redis 共享，429/配额耗尽/Key 失效时跨实例生效，§7.6）。
- `internal/reqlog`：把每次请求的用量/计费快照/重试轨迹异步批量写入 `request_logs`
  （§6.8/§7.13）——有界 channel + 后台 goroutine 每 500 条或 1 秒 flush 一次，
  队列满时丢弃并记日志，不会反过来拖慢请求处理。另外提供 `EnsureFuturePartitions`：
  按天补建未来的分区表（迁移只预建了执行当天起 14 天），供 worker 定时调用。
- `internal/ratelimit`：按 API Key 的 RPM（GCRA，`go-redis/redis_rate`）、TPM（固定窗口，
  Lua 脚本原子扣减）、并发（有序集合模拟租约，Lua 脚本原子获取/回收）三种限流（§7.12）。
  全部 fail-open：Redis 不可用时放行而不是拒绝所有请求。
- `internal/promotion`：促销引擎的售价侧实现（§7.10）——`price_discount`（按比例打折，
  可选预算上限）和 `free_quota`（每日/每月一定金额内免费，超出正常计费），两者的
  额度/预算扣减都用数据库事务 + 条件更新原子完成，并发下不会超发（并发回归测试：
  quota=10000、20 个并发请求各申请 1000，覆盖总量精确等于 10000）。
- `internal/relay`：把以上全部串起来的请求编排层——鉴权 → 限流 → 预扣费用 → 路由 →
  转发（失败时换 Key/换渠道重试，只在拿到上游响应之前重试，见 §7.7）→ 促销匹配 →
  结算 → 异步写入 request_logs（含按渠道成本价算出的 cost_amount，配了合同折扣
  `cost_multiplier` 的渠道会按折扣后的金额记——没配成本价的渠道记 `nil`，不是一个
  会误导毛利报表的假 0）。
  20 个端到端集成测试（真实 HTTP 请求 + mock 上游 + 真实 Postgres/Redis）覆盖：
  非流式/流式计费、余额不足拒绝、模型不存在、400 不重试、429 换 Key 成功、
  5xx 换渠道回退成功、单请求 MaxAttempts 耗尽、全局重试预算耗尽后提前放弃重试
  （§7.7）、唯一 Key 失效后无可用渠道、
  成功/失败两种场景下 request_logs 落盘的完整性（含 attempt_trace）、
  RPM 限流（带 Retry-After）、并发限流（含释放后恢复正常）、
  打折促销降低实扣金额、免费额度促销覆盖用量、按合同折扣算出渠道成本、
  一条 providers.protocol='anthropic' 的渠道走非流式/流式两条完整链路、
  一条 providers.protocol='gemini' 的渠道走非流式完整链路
  （证明多协议路由真的能跑通，不只是 openai 一家）。
- `internal/app`：有一个"从控制面到数据面全打通"的端到端测试——账户、API Key、
  Provider、渠道、售价全部通过 `cmd/admin` 的真实 HTTP 接口创建（不写一行手工
  SQL），再用生成的 API Key 打一个真实的 `/v1/chat/completions` 请求到网关，
  验证成功并按正确价格扣费。这条测试专门用来发现 admin 写数据和 relay/catalog
  读数据之间的字段/格式不一致（这类问题在两边各自的单元测试里发现不了）。

`credit_grant` 类促销（赠送余额）已经接入：`wallet.Service.Grant` 发放
到 `credit_grants` 表并累加 `wallets.bonus_balance`，`wallet.Service.Settle`
结算时会先按到期时间从近到远（FIFO）扣 bonus_balance（受 `model_scope` 限制、
支持过期跳过、并发安全），扣完才落到 cash_balance；`cmd/admin` 暴露了
`POST /accounts/{id}/credit-grants` 作为发放入口（还没有自动触发的注册赠送/
活动赠送流程，得靠这个接口手工/由外部系统调用）。

全局重试预算（§7.7 的"每实例每秒重试数 ≤ 正常请求数 20%"）已经接入：
`relay.RetryBudget` 用令牌桶近似这个比例（每个正常请求攒 0.2 个令牌、每次重试
耗 1 个令牌、桶从满值开始允许冷启动突发），而不是严格的按秒计数窗口——理由和
实现细节见 `internal/relay/retrybudget.go`。

`stackable=true` 的多促销叠加已经接入：`promotion.Engine.Quote` 按 priority
从高到低依次尝试候选，一条命中并成功应用后只有它自己标了 `stackable=true`
才会继续把折后价喂给下一条候选叠加（不同类型可以混着叠，比如先打折再扣免费
额度）；命中但不可叠加、或链中间某条候选预算/额度不足，链就停在那里，保留
已经叠加成功的部分，不会去尝试更低优先级的下一条（依然不做失败降级链）。
`Quote` 现在返回促销 ID 列表而不是单个 ID，`request_logs.promotion_ids`
按应用顺序记录全部命中的促销。

尚未接入：基于实时延迟/成功率的动态路由权重（§7.5.2）、促销的 cost 面（上游免费/折扣，
现在 catalog 已经会加载成本价了，缺的是促销引擎那边用它来联动调整路由权重
这一步）、账户级限流默认值继承（api_keys 的 rpm/tpm/concurrency_limit 为 NULL 时按
"不限制"处理，而不是继承账户级配置）。三种协议适配器（openai/anthropic/gemini）
都已接入，见上面 `internal/adapter` 一节的已知限制。另外 request_logs
目前只记录"预扣成功、进入路由/转发"之后的结果（成功或上游失败）；鉴权失败、
余额不足、模型不存在、限流拒绝等预扣之前的拒绝还只有结构化访问日志，不落 request_logs。
另外 `cmd/admin` 还没有用户控制台、支付回调、促销管理，充值目前只能靠
`POST /accounts/{id}/wallet/adjust` 手工调整。

审计日志（Phase 4）已经接入一部分：`internal/admin/audit.go` 的 `RecordAudit`/
`ListAuditLogs` 写读 `admin_audit_logs`，`cmd/admin` 在钱包调账/赠款、上游 Key
添加、成本价/售价/汇率发布这几个高价值操作成功后各记一条（`before`/`after`
JSON 快照 + 调用方 IP），`GET /audit-logs?target_type=&target_id=` 查询/导出。
不是每个写操作都审计；`actor_id` 只能靠调用方在 `X-Actor-ID` 请求头里自己声明——
`cmd/admin` 的共享密钥鉴权只知道"这个请求带了对的密钥"，不知道"是哪个人"，
没有真正的"当前操作者"概念（见上面 `cmd/admin` 一节）。

## 快速开始（无 Docker）

本项目默认假设你有 PostgreSQL + Redis；如果没有 Docker，可以用下面的方式在本机直接跑：

```bash
# 1a. Redis：如果已有 Memurai（Windows 上的 Redis 兼容实现）或原生 Redis 在跑，跳过这一步。
#     否则请自行安装一个本地 Redis/Memurai，监听 6379。

# 1b. PostgreSQL：用 tools/devdb 启动一个真实的本地 Postgres 子进程（首次运行会下载官方二进制，
#     数据存在 .data/pgdata，Ctrl+C 优雅停止）。
go run ./tools/devdb

# 2. 执行数据库迁移（新开一个终端；需要先安装 goose）
go install github.com/pressly/goose/v3/cmd/goose@latest
goose -dir migrations postgres "postgres://uft:uft@localhost:5432/uft?sslmode=disable" up

# 3. 播种一个最小可用的测试账户 + API Key（只够跑 /v1/models；pepper 需要和
#    第 5 步网关用的一致）——想要一条能真正转发请求、扣费的完整链路（账户/Key/
#    Provider/渠道/售价），用第 4 步的 cmd/admin 接口配置，不要手写 SQL。
go run ./tools/seed
# 输出会打印一个 sk-uft-... 的 API Key，记下来

# 4. （可选）运行控制面，配置账户/Key/Provider/渠道/售价。KEK 用于加密落库的
#    上游 Key，本地开发随便生成一个 32 字节 base64（生产环境应来自 KMS）：
#    openssl rand -base64 32
UFT_KEY_PEPPER=dev-pepper-change-me UFT_KEK=<32-byte-base64> go run ./cmd/admin
# curl -X POST localhost:8081/accounts -d '{"Type":"personal","Name":"acme"}'
# 完整流程（建账户 -> 充值 -> 建 Key -> 建 Provider/渠道/售价）可以参考
# internal/app/admin_gateway_e2e_test.go，那是一个从头到尾都走 HTTP 接口、
# 不写 SQL 的真实示例。

# 5. 运行网关（KEK 要和第 4 步一致，否则解不出上游 Key）
UFT_KEY_PEPPER=dev-pepper-change-me UFT_KEK=<32-byte-base64> go run ./cmd/gateway -config config/gateway.example.yaml

# 6. 冒烟测试
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl -H "Authorization: Bearer sk-uft-xxx" http://localhost:8080/v1/models
```

如果你更喜欢 Docker，也可以用 `make deps-up`（`deploy/docker-compose.yml`）代替第 1b 步。

## 开发

```bash
make build       # go build ./...
make test        # go test ./...          （纯逻辑单测 + 需要本地 Postgres 的集成测试，
                  #                          连不上数据库时集成测试会自动 Skip，不会失败）
make test-race   # go test ./... -race     （需要 cgo + C 工具链，本机没装的话用不了）
make vet         # go vet ./...
```

## 目录结构

参见技术方案 §5「工程目录结构」。
