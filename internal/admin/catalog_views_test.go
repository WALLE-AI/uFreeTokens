package admin

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

// TestCatalogViews_MatchGoMargins 保证 SQL 视图（用于列表过滤/排序/计数）与 Go 的
// 毛利口径（用于列表展示，pricectx.go）对库里每个渠道、每个模型给出相同的结果。
func TestCatalogViews_MatchGoMargins(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)
	ctx := context.Background()

	chans, err := s.queryChannels(ctx, "")
	if err != nil {
		t.Fatalf("queryChannels: %v", err)
	}
	view := map[int64]*decimal.Decimal{}
	missingCost := map[int64]bool{}
	rows, err := pool.Query(ctx, `SELECT channel_id, margin_ratio, cost_book_id IS NULL FROM v_admin_channel_margin`)
	if err != nil {
		t.Fatalf("query view: %v", err)
	}
	for rows.Next() {
		var id int64
		var m *decimal.Decimal
		var miss bool
		if err := rows.Scan(&id, &m, &miss); err != nil {
			t.Fatalf("scan: %v", err)
		}
		view[id], missingCost[id] = m, miss
	}
	rows.Close()
	for _, c := range chans {
		got, want := view[c.ID], c.MarginRatio
		if (got == nil) != (want == nil) || (got != nil && !got.Equal(*want)) {
			t.Errorf("channel %d margin: view=%v go=%v", c.ID, got, want)
		}
		if missingCost[c.ID] != (c.CostPrice == nil) {
			t.Errorf("channel %d missing cost: view=%v go=%v", c.ID, missingCost[c.ID], c.CostPrice == nil)
		}
	}

	vms, err := s.queryVirtualModels(ctx, "")
	if err != nil {
		t.Fatalf("queryVirtualModels: %v", err)
	}
	rows, err = pool.Query(ctx, `SELECT virtual_model_id, min_margin_ratio, has_sell_price, has_metadata, active_channel_count FROM v_admin_model_summary`)
	if err != nil {
		t.Fatalf("query model view: %v", err)
	}
	type row struct {
		m          *decimal.Decimal
		sell, meta bool
		active     int
	}
	mv := map[int64]row{}
	for rows.Next() {
		var id int64
		var r row
		if err := rows.Scan(&id, &r.m, &r.sell, &r.meta, &r.active); err != nil {
			t.Fatalf("scan: %v", err)
		}
		mv[id] = r
	}
	rows.Close()
	for _, v := range vms {
		r := mv[v.ID]
		if (r.m == nil) != (v.MinMarginRatio == nil) || (r.m != nil && !r.m.Equal(*v.MinMarginRatio)) {
			t.Errorf("model %d min margin: view=%v go=%v", v.ID, r.m, v.MinMarginRatio)
		}
		if r.sell != (v.SellPrice != nil) || r.meta != v.HasMetadata || r.active != v.ActiveChannelCount {
			t.Errorf("model %d flags: view=%+v go sell=%v meta=%v active=%d", v.ID, r, v.SellPrice != nil, v.HasMetadata, v.ActiveChannelCount)
		}
	}
	if len(chans) == 0 || len(vms) == 0 {
		t.Skip("no catalog data to compare")
	}
}
