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
- `cmd/admin` / `cmd/worker`：进程骨架，仅健康检查与依赖连通性检查。
- `migrations/`：Phase1 核心表结构，已在真实 PostgreSQL 上跑通。
- `internal/pricing`：计价纯函数（多计量项、分档、时段计价、向上取整）。
- `internal/wallet`：Reserve/Settle/Release 两阶段计费引擎（§7.9），幂等、原子冻结，
  集成测试包含**并发透支回归测试**（§1.3 A1 场景：余额只够 1 笔请求时并发发起 50 笔，验证绝不超额冻结）。
- `internal/secretbox`：上游 Key 的信封加密（AES-256-GCM，§7.15）。
- `internal/catalog`：虚拟模型/渠道/价格的内存快照（带 TTL 缓存的简化版，完整的
  LISTEN/NOTIFY 热加载见 §7.3，留作后续）。
- `internal/adapter`：OpenAI 兼容协议适配器（请求改写、非流式/流式响应解析、用量提取、
  错误分类，§7.4）。
- `internal/router`：硬过滤（含熔断/冷却状态）→ 优先级分层 → 层内加权随机的渠道/Key
  选择算法（§7.5），支持按请求排除已失败的渠道/Key，用统计检验测试证明不会出现
  "全部流量挤到一个渠道"的羊群效应。
- `internal/health`：渠道熔断器（`sony/gobreaker`，进程内）+ 上游 Key 冷却
  （Redis 共享，429/配额耗尽/Key 失效时跨实例生效，§7.6）。
- `internal/reqlog`：把每次请求的用量/计费快照/重试轨迹异步批量写入 `request_logs`
  （§6.8/§7.13）——有界 channel + 后台 goroutine 每 500 条或 1 秒 flush 一次，
  队列满时丢弃并记日志，不会反过来拖慢请求处理。
- `internal/relay`：把以上全部串起来的请求编排层，实现换 Key/换渠道的重试循环
  （只在拿到上游响应之前重试，一旦开始向客户端转发内容就不再重试，见 §7.7），
  并在请求结束时把结果异步写入 request_logs。
  11 个端到端集成测试（真实 HTTP 请求 + mock 上游 + 真实 Postgres/Redis）覆盖：
  非流式/流式计费、余额不足拒绝、模型不存在、400 不重试、429 换 Key 成功、
  5xx 换渠道回退成功、重试预算耗尽、唯一 Key 失效后无可用渠道、
  成功/失败两种场景下 request_logs 落盘的完整性（含 attempt_trace）。

尚未接入：全局重试预算限流（§7.7 的"每实例每秒重试数 ≤ 正常请求数 20%"）、
基于实时延迟/成功率的动态路由权重（§7.5.2）、促销引擎、限流、Anthropic/Gemini 适配器。
另外 request_logs 目前只记录"预扣成功、进入路由/转发"之后的结果（成功或上游失败）；
鉴权失败、余额不足、模型不存在等预扣之前的拒绝还只有结构化访问日志，不落 request_logs。

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

# 3. 播种一个可用的测试账户 + API Key（pepper 需要和第 4 步网关用的一致）
go run ./tools/seed
# 输出会打印一个 sk-uft-... 的 API Key，记下来

# 4. 运行网关
UFT_KEY_PEPPER=dev-pepper-change-me go run ./cmd/gateway -config config/gateway.example.yaml

# 5. 冒烟测试
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
