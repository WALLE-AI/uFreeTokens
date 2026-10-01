// 集成测试：连真实 PostgreSQL（tools/devdb）；连不上时跳过。
package benchmarks

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// 外部来源许可不允许对外展示的基准不出现在公开接口里；ForModel 每个基准取模型最好的档位。
func TestStore_PublicDisplayAndForModel(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	sfx := fmt.Sprint(time.Now().UnixNano())
	vm := "acme/m-" + sfx

	var vmID, pubSrc, privSrc int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO virtual_models (name, family, type, context_window, max_output) VALUES ($1, 'x', 'chat', 1000, 100) RETURNING id`, vm).Scan(&vmID))
	must(pool.QueryRow(ctx, `INSERT INTO price_sources (domain, name, level, kind, fetcher, public_display, license)
		VALUES ('benchmark', 'pub', 'L4', 'dataset', 'tabular', true, 'CC-BY-4.0') RETURNING id`).Scan(&pubSrc))
	must(pool.QueryRow(ctx, `INSERT INTO price_sources (domain, name, level, kind, fetcher, public_display)
		VALUES ('benchmark', 'priv', 'L4', 'dataset', 'tabular', false) RETURNING id`).Scan(&privSrc))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM benchmarks WHERE slug LIKE '%' || $1`, sfx)
		_, _ = pool.Exec(ctx, `DELETE FROM price_sources WHERE id IN ($1, $2)`, pubSrc, privSrc)
		_, _ = pool.Exec(ctx, `DELETE FROM virtual_models WHERE id = $1`, vmID)
	})

	mk := func(slug string, src *int64, scores map[string]float64) {
		t.Helper()
		var bid, rid int64
		must(pool.QueryRow(ctx, `INSERT INTO benchmarks (slug, name, category, metric_name, status, source_id)
			VALUES ($1, $1, 'coding', 'Score', 'published', $2) RETURNING id`, slug, src).Scan(&bid))
		must(pool.QueryRow(ctx, `INSERT INTO benchmark_runs (benchmark_id, origin, run_at, published, published_at)
			VALUES ($1, 'import', now(), true, now()) RETURNING id`, bid).Scan(&rid))
		for label, score := range scores {
			var link *int64
			if label != "other" {
				link = &vmID
			}
			_, err := pool.Exec(ctx, `INSERT INTO benchmark_results (run_id, model_label, virtual_model_id, score) VALUES ($1, $2, $3, $4)`,
				rid, label, link, score)
			must(err)
		}
	}
	mk("pub-"+sfx, &pubSrc, map[string]float64{"m-high": 90, "m-low": 70, "other": 95})
	mk("priv-"+sfx, &privSrc, map[string]float64{"m-high": 50, "other": 60})
	mk("manual-"+sfx, nil, map[string]float64{"m": 10})

	st := NewStore(pool)
	if _, err := st.Get(ctx, "priv-"+sfx); err != ErrNotFound {
		t.Errorf("private-source benchmark must be hidden, got %v", err)
	}
	pub, err := st.Get(ctx, "pub-"+sfx)
	if err != nil || pub.License == nil || *pub.License != "CC-BY-4.0" {
		t.Fatalf("public benchmark: %+v %v", pub, err)
	}
	if _, err := st.Get(ctx, "manual-"+sfx); err != nil {
		t.Errorf("manually entered benchmark (no source) must stay visible: %v", err)
	}

	list, err := st.ForModel(ctx, vm)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ForModel returned %d entries, want 2 (private source hidden): %+v", len(list), list)
	}
	for _, m := range list {
		switch m.Slug {
		case "pub-" + sfx:
			if m.ModelLabel != "m-high" || m.Score != 90 || m.Rank != 2 || m.ModelsCount != 3 {
				t.Errorf("public entry = %+v, want best variant m-high, rank 2 of 3", m)
			}
		case "manual-" + sfx:
			if m.Rank != 1 || m.ModelsCount != 1 {
				t.Errorf("manual entry = %+v", m)
			}
		default:
			t.Errorf("unexpected benchmark %s", m.Slug)
		}
	}
	if list, err := st.ForModel(ctx, "no-such-model-"+sfx); err != nil || len(list) != 0 {
		t.Errorf("unknown model: %v %v", list, err)
	}
}
