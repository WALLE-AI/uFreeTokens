package reqlog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

const defaultTestDSN = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("UFT_TEST_PG_DSN")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: cannot create postgres pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping: postgres not reachable at %s: %v", dsn, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type row struct {
	accountID, apiKeyID            int64
	channelID, providerKeyID       *int64
	httpStatus                     *int
	errorCode                      *string
	attempts                       int
	status                         string
	inputTokens, outputTokens      int64
	usageSource                    string
	sellBookID, listAmt, chargeAmt *int64
	clientIP, userAgent            *string
}

func fetchRow(t *testing.T, pool *pgxpool.Pool, requestID string) row {
	t.Helper()
	var r row
	err := pool.QueryRow(context.Background(),
		`SELECT account_id, api_key_id, channel_id, provider_key_id, http_status, error_code, attempts, status,
		        input_tokens, output_tokens, usage_source, sell_price_book_id, list_amount, charged_amount,
		        client_ip::text, user_agent
		 FROM request_logs WHERE request_id = $1`,
		requestID,
	).Scan(&r.accountID, &r.apiKeyID, &r.channelID, &r.providerKeyID, &r.httpStatus, &r.errorCode, &r.attempts, &r.status,
		&r.inputTokens, &r.outputTokens, &r.usageSource, &r.sellBookID, &r.listAmt, &r.chargeAmt,
		&r.clientIP, &r.userAgent)
	if err != nil {
		t.Fatalf("fetch request_logs row for %s: %v", requestID, err)
	}
	return r
}

func waitForRow(t *testing.T, pool *pgxpool.Pool, requestID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM request_logs WHERE request_id = $1`, requestID,
		).Scan(&n); err != nil {
			t.Fatalf("count request_logs: %v", err)
		}
		if n > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func int64p(v int64) *int64 { return &v }

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestWriter_WritesRecordAndFlushesOnTicker(t *testing.T) {
	pool := testPool(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	w := NewWriter(pool, logger)
	defer w.Close()

	reqID := fmt.Sprintf("reqlog-test-%d", time.Now().UnixNano())
	channelID, keyID := int64p(42), int64p(7)
	rec := Record{
		RequestID: reqID, CreatedAt: time.Now(),
		AccountID: 111, APIKeyID: 222,
		VirtualModel: "test-model", ChannelID: channelID, ProviderKeyID: keyID,
		Endpoint: "chat.completions", IsStream: false,
		Status: "success", HTTPStatus: 200, Attempts: 1,
		AttemptTrace:  []AttemptTraceEntry{{ChannelID: 42, KeyID: 7, Status: "success", LatencyMs: 120}},
		LatencyMillis: 150,
		Usage: schema.Usage{
			InputTokens: 100, OutputTokens: 50, Source: schema.UsageSourceUpstream,
		},
		SellBookID: int64p(9), ListAmount: int64p(1000), ChargedAmount: int64p(1000),
		ClientIP: "203.0.113.5", UserAgent: "test-agent/1.0",
	}
	w.Write(rec)

	if !waitForRow(t, pool, reqID, 3*time.Second) {
		t.Fatal("request log row did not appear within timeout (ticker flush failed?)")
	}

	got := fetchRow(t, pool, reqID)
	if got.accountID != 111 || got.apiKeyID != 222 {
		t.Errorf("account_id/api_key_id = %d/%d, want 111/222", got.accountID, got.apiKeyID)
	}
	if got.channelID == nil || *got.channelID != 42 {
		t.Errorf("channel_id = %v, want 42", got.channelID)
	}
	if got.providerKeyID == nil || *got.providerKeyID != 7 {
		t.Errorf("provider_key_id = %v, want 7", got.providerKeyID)
	}
	if got.httpStatus == nil || *got.httpStatus != 200 {
		t.Errorf("http_status = %v, want 200", got.httpStatus)
	}
	if got.status != "success" {
		t.Errorf("status = %q, want success", got.status)
	}
	if got.inputTokens != 100 || got.outputTokens != 50 {
		t.Errorf("tokens = %d/%d, want 100/50", got.inputTokens, got.outputTokens)
	}
	if got.usageSource != "upstream" {
		t.Errorf("usage_source = %q, want upstream", got.usageSource)
	}
	if got.chargeAmt == nil || *got.chargeAmt != 1000 {
		t.Errorf("charged_amount = %v, want 1000", got.chargeAmt)
	}
	// PostgreSQL 的 INET 类型转 text 时会带掩码（单个 IPv4 地址默认 /32），这是
	// 预期行为，不是 bug——只断言地址部分。
	if got.clientIP == nil || !strings.HasPrefix(*got.clientIP, "203.0.113.5") {
		t.Errorf("client_ip = %v, want to start with 203.0.113.5", derefStr(got.clientIP))
	}
}

func TestWriter_ClosesAndFlushesRemainingRecords(t *testing.T) {
	pool := testPool(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	w := NewWriter(pool, logger)

	reqID := fmt.Sprintf("reqlog-close-test-%d", time.Now().UnixNano())
	w.Write(Record{
		RequestID: reqID, CreatedAt: time.Now(),
		AccountID: 1, APIKeyID: 1, VirtualModel: "m", Endpoint: "chat.completions",
		Status: "rejected", HTTPStatus: 402, ErrorCode: "insufficient_balance",
		Usage: schema.Usage{Source: schema.UsageSourceEstimated},
	})
	w.Close() // 不依赖 1 秒定时器，Close 应该立刻 flush 剩余记录

	got := fetchRow(t, pool, reqID)
	if got.status != "rejected" {
		t.Errorf("status = %q, want rejected", got.status)
	}
	if got.httpStatus == nil || *got.httpStatus != 402 {
		t.Errorf("http_status = %v, want 402", got.httpStatus)
	}
	if got.errorCode == nil || *got.errorCode != "insufficient_balance" {
		t.Errorf("error_code = %v, want insufficient_balance", got.errorCode)
	}
	if got.channelID != nil {
		t.Errorf("channel_id = %v, want nil (rejected before routing)", got.channelID)
	}
}

func TestWriter_NilWriterIsNoOp(t *testing.T) {
	var w *Writer
	w.Write(Record{RequestID: "should-not-panic"}) // 不应该 panic
	w.Close()                                      // 也不应该 panic
}
