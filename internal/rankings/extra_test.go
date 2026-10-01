package rankings

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

// TestRankings_SpeedToolsMultimodal：阶段 3 的三个模型维度榜单（数据直接写 usage_hourly 的新列）。
func TestRankings_SpeedToolsMultimodal(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	f := &fixture{t: t, pool: pool, suffix: fmt.Sprintf("%d", time.Now().UnixNano())}
	name := "rks" + f.suffix + "/fast"
	vm := f.model(name, []string{"free"})
	now := time.Now()
	at := now.Add(-2 * time.Hour).UTC().Truncate(time.Hour)
	var accts []int64
	for i := 0; i < 3; i++ {
		accts = append(accts, f.account(false))
	}
	for i, a := range accts {
		f.usage(at, a, name, vm, 100, 100)
		// 每个账户：4 个流式请求共 400 输出 token、用时 4 秒；1 个工具调用请求（50 token）；
		// 只有第一个账户发过图片（低于阈值，多模态榜不应出现）。
		img := int64(0)
		if i == 0 {
			img = 30
		}
		if _, err := pool.Exec(ctx,
			`UPDATE usage_hourly SET speed_requests = 4, speed_output_tokens = 400, speed_gen_ms = 4000, spd_b6 = 2, spd_b7 = 2,
				tool_requests = 1, tool_tokens = 50, image_requests = CASE WHEN $3 > 0 THEN 1 ELSE 0 END, image_tokens = $3
			 WHERE bucket = $1 AND account_id = $2 AND virtual_model = $4`, at, a, img, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RefreshDaily(ctx, pool, at, now); err != nil {
		t.Fatalf("RefreshDaily: %v", err)
	}
	opts := DefaultOptions()
	opts.ShowAbsolute = true
	svc := NewService(pool, catalog.NewStore(pool, testBox(t), 0), opts)
	svc.SetClock(func() time.Time { return now.AddDate(0, 0, 2) })

	speed, err := svc.Speed(ctx, PeriodWeek, 100)
	if err != nil {
		t.Fatalf("Speed: %v", err)
	}
	var got *SpeedRankingEntry
	for i := range speed.Models {
		if speed.Models[i].Model == name {
			got = &speed.Models[i]
		}
	}
	// 12 个样本：加权平均 1200 token / 12 秒 = 100 tok/s；直方图 (60,80] 6 个、(80,100] 6 个，
	// 中位数落在 (60,80] 桶的上沿 = 80 tok/s。
	if got == nil || math.Abs(got.TokensPerSecond-80) > 1e-9 || math.Abs(got.MeanTokensPerSecond-100) > 1e-9 {
		t.Errorf("speed entry = %+v, want P50 80 / mean 100 tok/s", got)
	}

	tools, err := svc.Tools(ctx, PeriodWeek, 100)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	found := false
	for _, e := range tools.Models {
		if e.Model == name {
			found = true
			// 3 个账户各 50：上限 30 → 90。
			if e.Tokens == nil || *e.Tokens != 90 {
				t.Errorf("tools tokens = %v, want 90", deref(e.Tokens))
			}
		}
	}
	if !found {
		t.Errorf("model missing from tools ranking")
	}
	multi, err := svc.Multimodal(ctx, PeriodWeek, 100)
	if err != nil {
		t.Fatalf("Multimodal: %v", err)
	}
	for _, e := range multi.Models {
		if e.Model == name {
			t.Error("only one account sent images: the model must stay below the privacy threshold")
		}
	}
}

// TestRankings_Apps：应用榜读 request_logs，按域名归并，运营规则（合并 / 屏蔽 / 改名）生效。
func TestRankings_Apps(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	f := &fixture{t: t, pool: pool, suffix: fmt.Sprintf("%d", time.Now().UnixNano())}
	host := "https://app" + f.suffix + ".example"
	alias := "name:alias-" + f.suffix
	blockedName := "Fake Claude " + f.suffix
	now := time.Now()
	at := now.Add(-time.Minute)
	if at.Before(Day(now)) {
		t.Skip("too close to midnight Asia/Shanghai")
	}
	n := 0
	logReq := func(acct int64, appName, appURL string, tokens int) {
		n++
		var url any
		if appURL != "" {
			url = appURL
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO request_logs (request_id, created_at, account_id, api_key_id, virtual_model, endpoint, is_stream, status,
				attempts, input_tokens, output_tokens, usage_source, app_name, app_url)
			 VALUES ($1, $2, $3, $3, 'm', 'chat.completions', false, 'success', 1, $4, 0, 'upstream', $5, $6)`,
			fmt.Sprintf("app-%s-%d", f.suffix, n), at, acct, tokens, appName, url); err != nil {
			t.Fatal(err)
		}
	}
	accts := []int64{f.account(false), f.account(false), f.account(false)}
	// 同一域名下两个不同的 X-Title：归为一个应用，展示请求更多的名称。
	logReq(accts[0], "My App "+f.suffix, host, 100)
	logReq(accts[0], "My App "+f.suffix, host, 100)
	logReq(accts[1], "my-app-cli", host, 100)
	// 没有域名、只有名称的"别名"应用（3 个账户），运营把它合并进上面的域名应用。
	for _, a := range accts {
		logReq(a, "Alias-"+f.suffix, "", 100)
	}
	// 冒用知名应用名的（3 个账户）：被屏蔽。
	for _, a := range accts {
		logReq(a, blockedName, "", 1000)
	}
	if _, err := RefreshDaily(ctx, pool, now, now); err != nil {
		t.Fatalf("RefreshDaily: %v", err)
	}

	opts := DefaultOptions()
	opts.ShowAbsolute = true
	opts.MaxAccountShare = 0
	svc := NewService(pool, catalog.NewStore(pool, testBox(t), 0), opts)
	svc.SetClock(func() time.Time { return now.AddDate(0, 0, 1) })

	find := func(resp *AppRanking, url string) *AppRankingEntry {
		for i := range resp.Apps {
			if resp.Apps[i].AppURL == url {
				return &resp.Apps[i]
			}
		}
		return nil
	}
	resp, err := svc.Apps(ctx, PeriodDay, 100)
	if err != nil {
		t.Fatalf("Apps: %v", err)
	}
	if e := find(resp, host); e != nil {
		t.Errorf("domain app has 2 accounts and must be below the threshold before merging: %+v", e)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO public_app_rules (app_key, action, merge_into) VALUES ($1, 'merge', $2)`, alias, "url:"+host); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public_app_rules (app_key, action) VALUES ($1, 'block')`, "name:"+lowerASCII(blockedName)); err != nil {
		t.Fatal(err)
	}
	resp, err = svc.Apps(ctx, PeriodDay, 100)
	if err != nil {
		t.Fatalf("Apps: %v", err)
	}
	e := find(resp, host)
	if e == nil || e.AppName != "My App "+f.suffix || e.Tokens == nil || *e.Tokens != 600 {
		t.Fatalf("merged app = %+v, want My App with 600 tokens", e)
	}
	for _, a := range resp.Apps {
		if a.AppName == blockedName {
			t.Error("blocked app must not be listed")
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public_app_rules (app_key, action, display_name) VALUES ($1, 'rename', 'Renamed')`, "url:"+host); err != nil {
		t.Fatal(err)
	}
	resp, _ = svc.Apps(ctx, PeriodDay, 100)
	if e := find(resp, host); e == nil || e.AppName != "Renamed" {
		t.Errorf("renamed app = %+v", e)
	}
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func TestHistogramMedian(t *testing.T) {
	bounds := []int64{10, 20, 30}
	if _, ok := histogramMedian([]int64{0, 0, 0, 0}, bounds); ok {
		t.Error("empty histogram must report ok=false")
	}
	// 10 个全在 (10,20]：中位数在桶内插值到 15。
	if v, _ := histogramMedian([]int64{0, 10, 0, 0}, bounds); math.Abs(v-15) > 1e-9 {
		t.Errorf("median = %v, want 15", v)
	}
	// 一半以上在无上界的桶：返回其下界。
	if v, _ := histogramMedian([]int64{1, 0, 0, 9}, bounds); v != 30 {
		t.Errorf("median = %v, want 30", v)
	}
}
