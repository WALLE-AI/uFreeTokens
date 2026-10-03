// Package admin 是控制面的业务逻辑层：账户、API Key、Provider/渠道/虚拟模型/
// 价格的管理。在这个包出现之前，搭起一套可用的配置（账户、Key、渠道、价格）
// 只能靠手写 SQL——所有测试 fixture 都是这么干的。这个包让这些操作变成
// 可以通过 cmd/admin 的 HTTP 接口完成的正常操作。
//
// 鉴权与审计：cmd/admin 的每个接口都要求管理员会话（internal/adminauth）或
// 应急令牌，并按权限点收口（internal/app/admin_routes.go）；写操作的审计由
// HTTP 层用已认证身份写入，关键写操作（钱包、审批、编辑）与业务同事务。
//
// 价格/渠道的更新都是"新增一条"，不能内联编辑历史版本（这是故意的——价格
// 版本化本身要求历史不可篡改，见技术方案 §6.4）。
package admin

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

type Service struct {
	pool      *pgxpool.Pool
	wallet    *wallet.Service
	box       *secretbox.Box
	pepper    []byte
	urlPolicy UpstreamURLPolicy    // 零值 = 最严格（生产）策略，见 urlpolicy.go
	descGen   DescriptionGenerator // nil = 不能用 LLM 生成模型介绍，见 metadata_llm.go
}

func New(pool *pgxpool.Pool, walletSvc *wallet.Service, box *secretbox.Box, pepper []byte) *Service {
	return &Service{pool: pool, wallet: walletSvc, box: box, pepper: pepper}
}

// Wallet 暴露内部持有的 wallet.Service，供余额调整这类"属于钱包、但由控制面
// 发起"的操作使用（比如手工充值）。真正执行写入的仍然是 wallet 包自己的方法，
// 这里只是把已经装配好的实例递出去，不是让 admin 包自己获得写钱包表的能力。
func (s *Service) Wallet() *wallet.Service { return s.wallet }

type actorCtxKey struct{}

// WithActor 把发起操作的管理员 ID 放进 ctx：价格版本的 created_by 等"谁做的"字段
// 从这里取（HTTP 层的 audited 负责放入）。没有时记为 NULL（系统自动操作）。
func WithActor(ctx context.Context, adminID int64) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, adminID)
}

func actorFrom(ctx context.Context) *int64 {
	if id, ok := ctx.Value(actorCtxKey{}).(int64); ok {
		return &id
	}
	return nil
}

// db 返回 ctx 里的环境事务（store.RunInTx 开启的），没有时返回连接池——
// 这样本包的写操作可以和调用方的其他写操作（例如审计日志）组合进同一个事务。
func (s *Service) db(ctx context.Context) store.Querier { return store.Q(ctx, s.pool) }

// RunInTx 在一个事务里执行 fn；fn 内调用本包（以及 wallet、pricesync）的方法都会
// 加入这个事务，任一步失败整体回滚。
func (s *Service) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return store.RunInTx(ctx, s.pool, fn)
}
