package fxsync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

type fakeWriter struct {
	latest map[string]admin.FXRateInfo
	set    []admin.SetFXRateInput
	audits int
}

func (f *fakeWriter) ListFXRates(_ context.Context, base, quote string, _ bool, _ int) ([]admin.FXRateInfo, error) {
	if r, ok := f.latest[base+"/"+quote]; ok {
		return []admin.FXRateInfo{r}, nil
	}
	return nil, nil
}

func (f *fakeWriter) SetFXRate(_ context.Context, in admin.SetFXRateInput) (*admin.FXRateInfo, *admin.FXRateInfo, error) {
	f.set = append(f.set, in)
	return nil, &admin.FXRateInfo{Base: in.Base, Quote: in.Quote, Rate: in.Rate, EffectiveDate: in.EffectiveDate}, nil
}

func (f *fakeWriter) RecordAudit(context.Context, admin.AuditLogInput) (int64, error) {
	f.audits++
	return 1, nil
}

func (f *fakeWriter) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func serve(t *testing.T, body string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFXJob(t *testing.T) {
	env := datasync.NewEnv(datasync.EnvOptions{AllowPrivateNetworks: true, MinHostInterval: -1})
	ctx := context.Background()
	main := serve(t, `{"result":"success","base_code":"USD","rates":{"CNY":7.1234,"EUR":0.9}}`)
	now := func() time.Time { return time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC) }

	// 正常：无历史汇率，写入并审计。
	w := &fakeWriter{latest: map[string]admin.FXRateInfo{}}
	res, err := (&Job{Admin: w, Now: now}).Run(ctx, env, datasync.Source{URL: main, Name: "er-api"})
	if err != nil || res.ItemsChanged != 1 || len(w.set) != 1 || !w.set[0].Rate.Equal(decimal.RequireFromString("7.1234")) || w.audits != 1 {
		t.Fatalf("normal: res=%+v err=%v set=%+v", res, err, w.set)
	}

	// 偏离上次 >2%：拒收，不写入。
	w = &fakeWriter{latest: map[string]admin.FXRateInfo{"USD/CNY": {Rate: decimal.RequireFromString("6.8")}}}
	_, err = (&Job{Admin: w, Now: now}).Run(ctx, env, datasync.Source{URL: main})
	if !errors.Is(err, datasync.ErrRejected) || len(w.set) != 0 {
		t.Fatalf("deviation: err=%v set=%+v", err, w.set)
	}

	// 两源分歧：拒收。
	other := serve(t, `{"base":"USD","rates":{"CNY":7.5}}`)
	w = &fakeWriter{latest: map[string]admin.FXRateInfo{}}
	_, err = (&Job{Admin: w, Now: now}).Run(ctx, env, datasync.Source{URL: main, Config: map[string]any{"cross_check_url": other}})
	if !errors.Is(err, datasync.ErrRejected) || len(w.set) != 0 {
		t.Fatalf("cross-check: err=%v", err)
	}

	// 源格式变化：拒收。
	broken := serve(t, `{"data":[1,2,3]}`)
	_, err = (&Job{Admin: w, Now: now}).Run(ctx, env, datasync.Source{URL: broken})
	if !errors.Is(err, datasync.ErrRejected) {
		t.Fatalf("format change: err=%v", err)
	}
	// base 不一致：拒收。
	eur := serve(t, `{"base":"EUR","rates":{"CNY":7.8}}`)
	if _, err = (&Job{Admin: w, Now: now}).Run(ctx, env, datasync.Source{URL: eur}); !errors.Is(err, datasync.ErrRejected) {
		t.Fatalf("base mismatch: err=%v", err)
	}
}
