package rankings

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
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

func TestCapped(t *testing.T) {
	// 1000 + 3×100：原始 1300，单账户上限 20% = 260 → 260 + 300 = 560。
	raw, counted := capped(map[int64]int64{1: 1000, 2: 100, 3: 100, 4: 100}, 0.2)
	if raw != 1300 || counted != 560 {
		t.Errorf("capped = %d/%d, want 1300/560", raw, counted)
	}
	if raw, counted := capped(map[int64]int64{1: 50}, 0); raw != 50 || counted != 50 {
		t.Errorf("maxShare=0 must disable the cap, got %d/%d", raw, counted)
	}
}

func TestChange(t *testing.T) {
	if change(10, 0) != nil {
		t.Error("change must be nil when the previous period is 0")
	}
	if c := change(15, 10); c == nil || math.Abs(*c-0.5) > 1e-9 {
		t.Errorf("change(15,10) = %v, want 0.5", c)
	}
}

func TestParsePeriodAndWindow(t *testing.T) {
	if p, err := ParsePeriod(""); err != nil || p != PeriodWeek {
		t.Errorf("default period = %q, %v", p, err)
	}
	if _, err := ParsePeriod("year"); err == nil {
		t.Error("year must be rejected")
	}
	// 2026-10-01 01:30 +08:00 仍是东八区的 10 月 1 日（UTC 还是 9 月 30 日）。
	now := time.Date(2026, 9, 30, 17, 30, 0, 0, time.UTC)
	cur, prev := windowFor(PeriodWeek, now)
	if got := cur.From.Format(time.DateOnly) + ".." + cur.To.Format(time.DateOnly); got != "2026-09-24..2026-10-01" {
		t.Errorf("week window = %s", got)
	}
	if got := prev.From.Format(time.DateOnly) + ".." + prev.To.Format(time.DateOnly); got != "2026-09-17..2026-09-24" {
		t.Errorf("previous week window = %s", got)
	}
}

// TestAuthorExprMatchesGo：物化 SQL 里的作者推导与 catalog.AuthorOf 必须一致。
func TestAuthorExprMatchesGo(t *testing.T) {
	pool := testPool(t)
	for _, name := range []string{"deepseek/deepseek-pro", "gpt-4o", "/leading-slash", "a/b/c", ""} {
		var got string
		if err := pool.QueryRow(context.Background(), `SELECT `+fmt.Sprintf(authorExpr, "$1::text"), name).Scan(&got); err != nil {
			t.Fatalf("author expr: %v", err)
		}
		if want := catalog.AuthorOf(name); got != want {
			t.Errorf("SQL author(%q) = %q, Go AuthorOf = %q", name, got, want)
		}
	}
}

// fixture 在 usage_hourly 里造一组可控的公开榜单数据。
type fixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	suffix string
}

func (f *fixture) account(exclude bool) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO accounts (type, name, exclude_from_public_stats) VALUES ('personal', $1, $2) RETURNING id`,
		"rk-"+f.suffix, exclude).Scan(&id); err != nil {
		f.t.Fatalf("insert account: %v", err)
	}
	return id
}

func (f *fixture) model(name string, tiers []string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, visible_tiers)
		 VALUES ($1, 'test', 'chat', 8192, 1024, $2) RETURNING id`, name, tiers).Scan(&id); err != nil {
		f.t.Fatalf("insert virtual model: %v", err)
	}
	return id
}

// usage 写一行 usage_hourly；vmID 为 0 表示历史上没记 ID（只有名称）。
func (f *fixture) usage(bucket time.Time, account int64, name string, vmID int64, input, output int64) {
	f.t.Helper()
	var id any
	if vmID != 0 {
		id = vmID
	}
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO usage_hourly (bucket, account_id, api_key_id, virtual_model, virtual_model_id, channel_id, provider_id,
			requests, success, estimated, input_tokens, output_tokens, cache_read_tokens, reasoning_tokens,
			charged_micro, list_micro, cost_micro)
		 VALUES ($1, $2, $2, $3, $4, NULL, NULL, 1, 1, 0, $5, $6, 0, 0, 0, 0, 0)
		 ON CONFLICT (bucket, account_id, api_key_id, virtual_model, channel_id) DO UPDATE
		   SET input_tokens = usage_hourly.input_tokens + EXCLUDED.input_tokens,
		       output_tokens = usage_hourly.output_tokens + EXCLUDED.output_tokens`,
		bucket.UTC().Truncate(time.Hour), account, name, id, input, output); err != nil {
		f.t.Fatalf("insert usage_hourly: %v", err)
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	box, err := secretbox.NewBox(base64.StdEncoding.EncodeToString(b))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

// TestRankings_EndToEnd 覆盖公开口径的每一条规则：内部账户排除、隐私阈值、单账户上限、
// 非公开模型不上榜、按 ID 归并改名前后的数据、环比、序列与榜单一致、物化幂等。
func TestRankings_EndToEnd(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	f := &fixture{t: t, pool: pool, suffix: fmt.Sprintf("%d", time.Now().UnixNano())}
	author := "rk" + f.suffix
	nameA, nameB, nameHidden := author+"/model-a", author+"/model-b", author+"/model-hidden"
	vmA := f.model(nameA, []string{"free", "pro"})
	vmB := f.model(nameB, []string{"free"})
	vmHidden := f.model(nameHidden, []string{"enterprise"})

	big, s1, s2, s3 := f.account(false), f.account(false), f.account(false), f.account(false)
	internal := f.account(true)

	// 本期（时钟拨到两天后，周榜窗口覆盖"今天"）：A 由 4 个账户使用，其中 big 一家独大。
	now := time.Now()
	cur := now.Add(-2 * time.Hour)
	f.usage(cur, big, nameA, vmA, 600, 400)
	f.usage(cur, s1, nameA, vmA, 60, 40)
	f.usage(cur, s2, nameA, vmA, 60, 40)
	// s3 的数据来自改名前、没记 ID 的历史行：按名称映射回同一个模型。
	f.usage(cur, s3, nameA, 0, 60, 40)
	f.usage(cur, internal, nameA, vmA, 1_000_000, 0) // 内部账户：不计入
	// B 只有 2 个账户：低于阈值，归入"其他"。
	f.usage(cur, s1, nameB, vmB, 500, 500)
	f.usage(cur, s2, nameB, vmB, 500, 500)
	// 非公开模型：即使账户够多也不上榜。
	for _, a := range []int64{big, s1, s2, s3} {
		f.usage(cur, a, nameHidden, vmHidden, 1000, 0)
	}
	// 上一期：A 由 3 个账户各 100。
	prev := now.AddDate(0, 0, -6)
	for _, a := range []int64{s1, s2, s3} {
		f.usage(prev, a, nameA, vmA, 50, 50)
	}

	for i := 0; i < 2; i++ { // 第二次执行验证幂等
		if _, err := RefreshDaily(ctx, pool, now.AddDate(0, 0, -8), now); err != nil {
			t.Fatalf("RefreshDaily: %v", err)
		}
	}

	opts := DefaultOptions()
	opts.ShowAbsolute = true
	svc := NewService(pool, catalog.NewStore(pool, testBox(t), 0), opts)
	svc.SetClock(func() time.Time { return now.AddDate(0, 0, 2) })

	resp, err := svc.Models(ctx, PeriodWeek, 100, true)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	var a *ModelRankingEntry
	for i := range resp.Models {
		switch resp.Models[i].Model {
		case nameA:
			a = &resp.Models[i]
		case nameB, nameHidden:
			t.Errorf("%s must not be listed (below threshold / not public)", resp.Models[i].Model)
		}
	}
	if a == nil {
		t.Fatalf("model A missing from rankings: %+v", resp.Models)
	}
	// 原始 1000+100+100+100 = 1300，上限 260 → 260 + 300 = 560。
	if a.Tokens == nil || *a.Tokens != 560 {
		t.Errorf("A tokens = %v, want 560", a.Tokens)
	}
	// 上一期 3×100 = 300，上限 60 → 180；环比 560/180 − 1。
	if a.Change == nil || math.Abs(*a.Change-(560.0/180-1)) > 1e-9 {
		t.Errorf("A change = %v, want %v", a.Change, 560.0/180-1)
	}
	if a.Author != author || a.Deprecated {
		t.Errorf("A author/deprecated = %q/%v", a.Author, a.Deprecated)
	}
	var seriesSum int64
	for _, p := range a.Series {
		seriesSum += *p.Tokens
	}
	if len(a.Series) != 7 || seriesSum < 558 || seriesSum > 560 {
		t.Errorf("A series = %d points summing to %d, want 7 points summing to ~560", len(a.Series), seriesSum)
	}
	// B：2 个账户各 1000，上限 400 → 800；非公开模型不做单账户上限，原样 4000 计入"其他"。
	if resp.Others.Tokens == nil || *resp.Others.Tokens < 800+4000 {
		t.Errorf("others = %v, want to include B (800) and the hidden model (4000)", deref(resp.Others.Tokens))
	}

	authors, err := svc.Authors(ctx, PeriodWeek)
	if err != nil {
		t.Fatalf("Authors: %v", err)
	}
	found := false
	for _, e := range authors.Authors {
		if e.Author == author {
			found = true
			// 作者口径：A（560）+ B（800，模型级不上榜但作者级有 4 个账户）。
			if e.Tokens == nil || *e.Tokens != 1360 || e.Models != 2 {
				t.Errorf("author entry = %+v (tokens %v), want 1360 tokens over 2 models", e, deref(e.Tokens))
			}
		}
	}
	if !found && len(authors.Authors) < maxAuthors {
		t.Errorf("author %s missing: %+v", author, authors.Authors)
	}

	// 不公开绝对值时不带 tokens 字段。
	svc.opts.ShowAbsolute = false
	hidden, err := svc.Models(ctx, PeriodWeek, 100, false)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if hidden.TotalTokens != nil || hidden.Others.Tokens != nil {
		t.Error("absolute numbers must be omitted when ShowAbsolute is false")
	}
	for _, e := range hidden.Models {
		if e.Tokens != nil {
			t.Fatalf("model %s exposes tokens", e.Model)
		}
	}

	// 内部账户被取消排除后，重算即计入（这里只验证物化层）。
	if _, err := pool.Exec(ctx, `UPDATE accounts SET exclude_from_public_stats = false WHERE id = $1`, internal); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshDaily(ctx, pool, cur, now); err != nil {
		t.Fatalf("RefreshDaily: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public_model_account_daily WHERE account_id = $1`, internal).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("account no longer excluded must appear after refresh")
	}
}
