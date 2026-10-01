package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 环境事务（ambient transaction）：让多个业务模块的写操作可以组合进同一个
// 数据库事务，而不用给每个方法都加一个 tx 参数。
//
// 用法：调用方用 RunInTx 开启事务，事务放进 ctx；被调用的方法用 Querier(ctx, pool)
// 做查询、用 BeginOrJoin(ctx, pool) 开启"自己的"事务——ctx 里已有事务时，
// BeginOrJoin 返回一个基于 SAVEPOINT 的嵌套事务：内层 Commit 只是释放保存点，
// 内层 Rollback 只回滚到保存点，真正的提交/回滚由最外层 RunInTx 决定。
//
// 典型场景：运营后台的一次写操作 = 业务变更 + 审计日志，二者必须同成败；
// 一键上架 = 建虚拟模型 + 建渠道 + 发布成本价 + 发布售价 + 更新候选状态，
// 任何一步失败都不能留下半成品。

// Querier 是 *pgxpool.Pool 与 pgx.Tx 的公共子集。
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txCtxKey struct{}

// WithTx 把事务放进 ctx。
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txCtxKey{}, tx)
}

// TxFromContext 取出 ctx 里的环境事务；没有时返回 nil。
func TxFromContext(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(txCtxKey{}).(pgx.Tx)
	return tx
}

// Q 返回 ctx 里的环境事务，没有时返回连接池。
func Q(ctx context.Context, pool *pgxpool.Pool) Querier {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return pool
}

// BeginOrJoin 开启一个事务：ctx 里已有环境事务时返回基于 SAVEPOINT 的嵌套事务，
// 否则从连接池开启新事务。调用方照常 defer Rollback、最后 Commit 即可。
func BeginOrJoin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	if tx := TxFromContext(ctx); tx != nil {
		return tx.Begin(ctx)
	}
	return pool.Begin(ctx)
}

// RunInTx 在一个事务里执行 fn（ctx 里已有环境事务时用保存点嵌套）：fn 返回
// 错误则回滚，否则提交。fn 收到的 ctx 携带该事务。
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context) error) error {
	tx, err := BeginOrJoin(ctx, pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 提交后 Rollback 是 no-op
	if err := fn(WithTx(ctx, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
