package rankings_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/rankings"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// TestRankings_MatchAdminStats 是方案阶段 1 的验收用例：同一批请求日志，经 usage_hourly →
// 公开日表 → /v1/rankings/models 算出的模型 token 量，与运营后台 /stats/usage
// （group_by=virtual_model，tz=Asia/Shanghai）的 input+output 一致（单账户上限关闭时）。
func TestRankings_MatchAdminStats(t *testing.T) {
	dsn := os.Getenv("UFT_TEST_PG_DSN")
	if dsn == "" {
		dsn = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil || pool.Ping(ctx) != nil {
		t.Skipf("skipping: postgres not reachable at %s", dsn)
	}
	defer pool.Close()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, err := secretbox.NewBox(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "rkc" + suffix + "/model"
	var vmID int64
	if err := pool.QueryRow(ctx, `INSERT INTO virtual_models (name, family, type, context_window, max_output, visible_tiers)
		VALUES ($1, 'test', 'chat', 8192, 1024, '{free}') RETURNING id`, name).Scan(&vmID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	dayStart := rankings.Day(now)
	at := now.Add(-time.Minute)
	if at.Before(dayStart) {
		t.Skip("too close to midnight Asia/Shanghai")
	}
	// 5 个账户，每个账户 2 条成功 + 1 条失败（失败的 token 不计入）。
	for i := 0; i < 5; i++ {
		var acct int64
		if err := pool.QueryRow(ctx, `INSERT INTO accounts (type, name) VALUES ('personal', $1) RETURNING id`, "rkc-"+suffix).Scan(&acct); err != nil {
			t.Fatal(err)
		}
		for j, status := range []string{"success", "success", "upstream_error"} {
			if _, err := pool.Exec(ctx,
				`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, virtual_model_id, endpoint, is_stream,
				   status, attempts, latency_ms, input_tokens, output_tokens, reasoning_tokens, usage_source, gen_ms)
				 VALUES ($1, $2, $3, $3, $4, $5, '/v1/chat/completions', true, $6, 1, 100, $7, $8, 5, 'upstream', 200)`,
				fmt.Sprintf("rkc-%s-%d-%d", suffix, i, j), at, acct, name, vmID, status, 100*(i+1), 10*(j+1)); err != nil {
				t.Fatalf("seed request_log: %v", err)
			}
		}
	}
	if _, err := reqlog.RollupUsage(ctx, pool, dayStart, now); err != nil {
		t.Fatalf("RollupUsage: %v", err)
	}
	if _, err := rankings.RefreshDaily(ctx, pool, now, now); err != nil {
		t.Fatalf("RefreshDaily: %v", err)
	}

	svc := rankings.NewService(pool, catalog.NewStore(pool, box, 0),
		rankings.Options{MinDistinctAccounts: 3, MaxAccountShare: 0, ShowAbsolute: true})
	svc.SetClock(func() time.Time { return now.AddDate(0, 0, 1) }) // 日榜窗口 = 今天
	resp, err := svc.Models(ctx, rankings.PeriodDay, 100, false)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	var got int64 = -1
	for _, m := range resp.Models {
		if m.Model == name {
			got = *m.Tokens
		}
	}

	adminSvc := admin.New(pool, wallet.New(pool), box, nil)
	stats, err := adminSvc.Usage(ctx, admin.UsageInput{
		StatsFilter: admin.StatsFilter{From: dayStart, To: now.Add(time.Minute), VirtualModel: name},
		TZ:          rankings.Location, Interval: "none", GroupBy: "virtual_model",
	})
	if err != nil {
		t.Fatalf("admin Usage: %v", err)
	}
	want := stats.Totals.InputTokens + stats.Totals.OutputTokens
	if want == 0 || got != want {
		t.Errorf("rankings tokens = %d, /stats/usage input+output = %d", got, want)
	}

	// 速度榜：10 个成功流式请求（失败的不计），输出 10 / 20 token、生成耗时都是 200ms，
	// 吞吐分别为 50 与 100 tok/s 各 5 个 → 中位数落在 (40,50] 桶上沿 = 50。
	speed, err := svc.Speed(ctx, rankings.PeriodDay, 100)
	if err != nil {
		t.Fatalf("Speed: %v", err)
	}
	found := false
	for _, m := range speed.Models {
		if m.Model == name {
			found = true
			if m.TokensPerSecond != 50 || m.MeanTokensPerSecond < 50 || m.MeanTokensPerSecond > 100 {
				t.Errorf("speed = %+v, want P50 50 tok/s", m)
			}
		}
	}
	if !found {
		t.Errorf("model missing from speed ranking: %+v", speed.Models)
	}
}
