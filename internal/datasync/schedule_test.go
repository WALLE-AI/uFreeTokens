package datasync

import (
	"testing"
	"time"
)

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	from := time.Date(2026, 10, 1, 10, 17, 30, 0, loc) // 周四
	cases := []struct {
		schedule string
		want     time.Time
		ok       bool
	}{
		{"", time.Time{}, false},
		{"@every 6h", from.Add(6 * time.Hour), true},
		{"@hourly", time.Date(2026, 10, 1, 11, 0, 0, 0, loc), true},
		{"@daily", time.Date(2026, 10, 2, 0, 0, 0, 0, loc), true},
		{"@weekly", time.Date(2026, 10, 4, 0, 0, 0, 0, loc), true}, // 周日
		{"*/15 * * * *", time.Date(2026, 10, 1, 10, 30, 0, 0, loc), true},
		{"5 3 * * *", time.Date(2026, 10, 2, 3, 5, 0, 0, loc), true},
		{"0 9-18/3 * * 1-5", time.Date(2026, 10, 1, 12, 0, 0, 0, loc), true},
		{"0 0 1 * *", time.Date(2026, 11, 1, 0, 0, 0, 0, loc), true},
		{"0 0 * * 7", time.Date(2026, 10, 4, 0, 0, 0, 0, loc), true},
		{"30 2 15 * 1", time.Date(2026, 10, 5, 2, 30, 0, 0, loc), true}, // 日、周都限定时任一满足即可
	}
	for _, c := range cases {
		got, ok, err := NextRun(c.schedule, from)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.schedule, err)
			continue
		}
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("%q: got %v (ok=%v), want %v (ok=%v)", c.schedule, got, ok, c.want, c.ok)
		}
	}
	for _, bad := range []string{"@every 10s", "61 * * * *", "* * *", "0 0 31 2 *", "a b c d e"} {
		if _, _, err := NextRun(bad, from); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute}
	for i, w := range want {
		if got := Backoff(i); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i, got, w)
		}
	}
	if got := Backoff(30); got != maxBackoff {
		t.Errorf("Backoff(30) = %v, want cap %v", got, maxBackoff)
	}
}
