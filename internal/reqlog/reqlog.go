// Package reqlog 把每次 /v1/chat/completions 请求的完整轨迹（用量、计费快照、
// 重试轨迹）异步批量写入 request_logs（技术方案 §6.8、§7.13，合并了 V1 方案里
// Usage 与 Request Session 两张表的信息，是唯一事实来源）。
//
// 写入方式：网关把 Record 放进一个有界 channel，后台 goroutine 每 500 条或 1 秒
// 用一个 pgx.Batch 批量落库。channel 满时直接丢弃并记日志/计数——技术方案 §7.13
// 的原则是"计费相关数据绝不能丢，但那是 wallet 的 ledger_entries 在 Settle 事务里
// 同步保证的；request_logs 是访问日志性质的审计记录，允许在极端积压下降级丢弃"。
package reqlog

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// AttemptTraceEntry 记录一次路由尝试的结果，对应技术方案 §7.9.3/§14 的 attempt_trace。
type AttemptTraceEntry struct {
	ChannelID int64  `json:"channel_id"`
	KeyID     int64  `json:"key_id"`
	Status    string `json:"status"` // success / rate_limited / key_exhausted / key_invalid / upstream_unavailable / bad_request / content_filtered / connection_error
	LatencyMs int64  `json:"latency_ms"`
}

// Record 是一行 request_logs。字段命名与数据库列一一对应，方便对照 migrations/00007。
type Record struct {
	RequestID     string
	CreatedAt     time.Time
	AccountID     int64
	APIKeyID      int64
	VirtualModel  string
	ChannelID     *int64 // 最终成功（或最后一次尝试）所用的渠道；从未路由成功时为 nil
	ProviderKeyID *int64
	Endpoint      string
	IsStream      bool
	Status        string // success / upstream_error / rejected
	HTTPStatus    int
	ErrorCode     string
	Attempts      int
	AttemptTrace  []AttemptTraceEntry
	TTFTMillis    *int64
	LatencyMillis int64
	Usage         schema.Usage
	SellBookID    *int64
	PromotionIDs  []int64 // 命中的促销 ID（本阶段最多 1 个，见 internal/promotion 包注释）
	ListAmount    *int64
	ChargedAmount *int64
	ClientIP      string
	UserAgent     string
}

// Writer 是唯一的 request_logs 写入路径。
type Writer struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	ch     chan Record
	done   chan struct{}
}

const (
	defaultBufferSize = 2000
	batchSize         = 500
	flushInterval     = time.Second
)

func NewWriter(pool *pgxpool.Pool, logger *slog.Logger) *Writer {
	w := &Writer{
		pool:   pool,
		logger: logger,
		ch:     make(chan Record, defaultBufferSize),
		done:   make(chan struct{}),
	}
	go w.loop()
	return w
}

// Write 把一条记录放进写入队列；非阻塞——队列满时丢弃并记一条 warning 日志，
// 不能因为日志积压反过来拖慢请求处理（这本身就是访问日志类组件的常见设计）。
func (w *Writer) Write(rec Record) {
	if w == nil {
		return
	}
	select {
	case w.ch <- rec:
	default:
		w.logger.Warn("request log dropped: buffer full", "request_id", rec.RequestID)
	}
}

// Close 停止后台写入 goroutine，flush 掉队列里剩余的记录。
func (w *Writer) Close() {
	if w == nil {
		return
	}
	close(w.ch)
	<-w.done
}

func (w *Writer) loop() {
	defer close(w.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]Record, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		w.insertBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case rec, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, rec)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

const insertSQL = `
INSERT INTO request_logs (
    request_id, created_at, account_id, api_key_id, virtual_model, channel_id, provider_key_id,
    endpoint, is_stream, status, http_status, error_code, attempts, attempt_trace,
    ttft_ms, latency_ms,
    input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens, usage_source,
    sell_price_book_id, promotion_ids, list_amount, charged_amount, client_ip, user_agent
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14,
    $15, $16,
    $17, $18, $19, $20, $21, $22,
    $23, $24, $25, $26, $27, $28
)`

func (w *Writer) insertBatch(records []Record) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	batch := &pgx.Batch{}
	for _, r := range records {
		trace, err := json.Marshal(r.AttemptTrace)
		if err != nil {
			w.logger.Error("marshal attempt_trace failed", "request_id", r.RequestID, "error", err)
			trace = []byte("[]")
		}
		source := string(r.Usage.Source)
		if source == "" {
			source = string(schema.UsageSourceEstimated) // 未知来源时的保守占位，见包注释
		}

		batch.Queue(insertSQL,
			r.RequestID, r.CreatedAt, r.AccountID, r.APIKeyID, r.VirtualModel, r.ChannelID, r.ProviderKeyID,
			r.Endpoint, r.IsStream, r.Status, nullIfZero(r.HTTPStatus), nullIfEmpty(r.ErrorCode), r.Attempts, trace,
			r.TTFTMillis, r.LatencyMillis,
			r.Usage.InputTokens, r.Usage.CacheReadTokens, r.Usage.CacheWriteTokens, r.Usage.OutputTokens, r.Usage.ReasoningTokens, source,
			r.SellBookID, r.PromotionIDs, r.ListAmount, r.ChargedAmount, nullIfEmpty(r.ClientIP), nullIfEmpty(r.UserAgent),
		)
	}

	br := w.pool.SendBatch(ctx, batch)
	defer br.Close()
	for _, r := range records {
		if _, err := br.Exec(); err != nil {
			w.logger.Error("insert request_logs failed", "request_id", r.RequestID, "error", err)
		}
	}
}

func nullIfZero(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
