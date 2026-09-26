// Package chsync 实现技术方案 §7.13 描述的"Phase2 起 worker 把 request_logs
// 分区数据同步到 ClickHouse，控制台用量分析全部走 ClickHouse"这条 ETL 流水线
// 的骨架：从 Postgres 增量拉取（游标持久化在 analytics_sync_state 表，断点
// 续传）、转成扁平化的分析行形状（UsageRow）、交给一个 Sink 写出去。
//
// 这里没有接真正的 ClickHouse——技术方案原文把 ClickHouse 标注为"Phase2，
// 可选"（见 docs 架构图），没有一个可以在自动化测试里安全连接、验证过的真实
// 实例；写一个连不上任何真实服务的 clickhouse-go 客户端代码不会比不写更有
// 价值，还会往 go.mod 里加一个完全没被验证过的依赖。Sink 是一个接口——真正
// 接入时只需要补一个实现了 InsertBatch 的 ClickHouse 版本（官方 clickhouse-go
// 驱动，原生协议批量 INSERT，不要逐行写，ClickHouse 的写入吞吐几乎完全取决
// 于批大小）。本包的测试用一个内存 Sink 验证"从 Postgres 拉数据、去重续传、
// 转换行形状、失败重试不丢不重"这部分逻辑——这部分完全不依赖 ClickHouse 本身，
// 可以用真实 Postgres 完整测试，见 chsync_test.go。
//
// 目标表设想（供接入时参考，不是本包强制的契约）：按 created_at 天分区的
// MergeTree，ORDER BY (created_at, account_id, virtual_model)，支撑控制台
// "用量分析"按时间/账户/模型聚合查询（§7.14 提到的高基数分析场景，不适合
// 直接查 Prometheus 或 request_logs 主库）。
package chsync

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UsageRow 是同步给分析型存储的一条扁平记录，字段直接取自 request_logs——
// 有意不做任何跨表 join（渠道/provider 维度的枚举放进 ClickHouse 侧的维度表
// 还是直接展开，是 ClickHouse schema 设计本身的问题，留给真正接入时决定）。
type UsageRow struct {
	RequestID        string
	CreatedAt        time.Time
	AccountID        int64
	VirtualModel     string
	ChannelID        *int64
	Endpoint         string
	IsStream         bool
	Status           string
	HTTPStatus       *int
	InputTokens      *int
	CacheReadTokens  *int
	CacheWriteTokens *int
	OutputTokens     *int
	ReasoningTokens  *int
	ChargedAmount    *int64
	CostAmount       *int64
	LatencyMs        *int
}

// Sink 把一批 UsageRow 写进下游分析存储。
type Sink interface {
	InsertBatch(ctx context.Context, rows []UsageRow) error
}

// Syncer 从 Postgres request_logs 增量拉取。游标是 (created_at, request_id)
// 元组（用行值比较做 keyset pagination，不用 OFFSET——分区表 + 持续写入下
// OFFSET 分页在并发写入时会跳行/重复行），持久化在 analytics_sync_state 表，
// 按 name 区分：同一个 name 断点续传，worker 重启不会重新同步全量数据，也不
// 会跳过 worker 掉线期间产生的新行。
type Syncer struct {
	pool *pgxpool.Pool
	sink Sink
	name string
}

func New(pool *pgxpool.Pool, sink Sink, name string) *Syncer {
	return &Syncer{pool: pool, sink: sink, name: name}
}

type cursor struct {
	createdAt time.Time
	requestID string
}

// loadCursor 用 INSERT ... ON CONFLICT DO UPDATE 原子地"取到就返回，不存在就
// 以默认值创建后返回"——避免先 SELECT 判断存在性再 INSERT 的竟态。
func (s *Syncer) loadCursor(ctx context.Context) (cursor, error) {
	var c cursor
	err := s.pool.QueryRow(ctx,
		`INSERT INTO analytics_sync_state (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name = analytics_sync_state.name
		 RETURNING last_created_at, last_request_id`,
		s.name,
	).Scan(&c.createdAt, &c.requestID)
	if err != nil {
		return cursor{}, fmt.Errorf("chsync: load cursor: %w", err)
	}
	return c, nil
}

func (s *Syncer) saveCursor(ctx context.Context, c cursor) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE analytics_sync_state SET last_created_at = $2, last_request_id = $3, updated_at = now() WHERE name = $1`,
		s.name, c.createdAt, c.requestID,
	); err != nil {
		return fmt.Errorf("chsync: save cursor: %w", err)
	}
	return nil
}

// SyncOnce 拉取游标之后的一批（最多 batchSize 条）request_logs，交给 Sink
// 批量写出去，写成功后才推进游标——先写下游、再动游标，如果 Sink 写失败，
// 下次调用还会从同样的位置重新拉取同一批，这是 at-least-once，不是
// exactly-once；ClickHouse 侧需要用 ReplacingMergeTree 之类的引擎语义去重
// 重复写入。返回本次实际同步的行数，0 表示没有新数据。
func (s *Syncer) SyncOnce(ctx context.Context, batchSize int) (int, error) {
	cur, err := s.loadCursor(ctx)
	if err != nil {
		return 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT request_id, created_at, account_id, virtual_model, channel_id, endpoint, is_stream, status,
		       http_status, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens,
		       charged_amount, cost_amount, latency_ms
		FROM request_logs
		WHERE (created_at, request_id) > ($1, $2)
		ORDER BY created_at, request_id
		LIMIT $3
	`, cur.createdAt, cur.requestID, batchSize)
	if err != nil {
		return 0, fmt.Errorf("chsync: query request_logs: %w", err)
	}

	var batch []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.RequestID, &r.CreatedAt, &r.AccountID, &r.VirtualModel, &r.ChannelID, &r.Endpoint, &r.IsStream, &r.Status,
			&r.HTTPStatus, &r.InputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.OutputTokens, &r.ReasoningTokens,
			&r.ChargedAmount, &r.CostAmount, &r.LatencyMs); err != nil {
			rows.Close()
			return 0, fmt.Errorf("chsync: scan row: %w", err)
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("chsync: iterate rows: %w", err)
	}
	rows.Close()

	if len(batch) == 0 {
		return 0, nil
	}

	if err := s.sink.InsertBatch(ctx, batch); err != nil {
		return 0, fmt.Errorf("chsync: sink insert batch: %w", err)
	}

	last := batch[len(batch)-1]
	if err := s.saveCursor(ctx, cursor{createdAt: last.CreatedAt, requestID: last.RequestID}); err != nil {
		return 0, err
	}
	return len(batch), nil
}
