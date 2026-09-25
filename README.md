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
  `internal/secretbox` 加密；改价格是发布新版本，不覆盖历史。**目前完全没有
  鉴权/权限控制**，只应该部署在内网，这是部署前必须解决的安全缺口。
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
  见下），是缺"上游账单从哪来"这一环（§7.16 的价格/账单同步，整个都还没做）。
- `internal/secretbox`：上游 Key 的信封加密（AES-256-GCM，§7.15）。
- `internal/catalog`：虚拟模型/渠道/售价/成本价的内存快照（带 TTL 缓存的简化版，
  完整的 LISTEN/NOTIFY 热加载见 §7.3，留作后续）。成本价挂渠道（§6.4），只有
  currency='CNY' 的版本会被使用——汇率同步（§7.16.9）没做，外币成本价加载了但
  算不出来。
- `internal/adapter`：OpenAI 兼容协议适配器（请求改写、非流式/流式响应解析、用量提取、
  错误分类，§7.4）。
- `internal/router`：硬过滤（含熔断/冷却状态）→ 优先级分层 → 层内加权随机的渠道/Key
  选择算法（§7.5），支持按请求排除已失败的渠道/Key，用统计检验测试证明不会出现
  "全部流量挤到一个渠道"的羊群效应。
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
  17 个端到端集成测试（真实 HTTP 请求 + mock 上游 + 真实 Postgres/Redis）覆盖：
  非流式/流式计费、余额不足拒绝、模型不存在、400 不重试、429 换 Key 成功、
  5xx 换渠道回退成功、单请求 MaxAttempts 耗尽、全局重试预算耗尽后提前放弃重试
  （§7.7）、唯一 Key 失效后无可用渠道、
  成功/失败两种场景下 request_logs 落盘的完整性（含 attempt_trace）、
  RPM 限流（带 Retry-After）、并发限流（含释放后恢复正常）、
  打折促销降低实扣金额、免费额度促销覆盖用量、按合同折扣算出渠道成本。
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
"不限制"处理，而不是继承账户级配置）、Anthropic/Gemini 适配器。另外 request_logs
目前只记录"预扣成功、进入路由/转发"之后的结果（成功或上游失败）；鉴权失败、
余额不足、模型不存在、限流拒绝等预扣之前的拒绝还只有结构化访问日志，不落 request_logs。
另外 `cmd/admin` 还没有用户控制台、支付回调、促销管理、审计日志（`admin_audit_logs`
表已建好但没有写入），充值目前只能靠 `POST /accounts/{id}/wallet/adjust` 手工调整。

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
