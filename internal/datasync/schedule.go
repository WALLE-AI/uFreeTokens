package datasync

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NextRun 计算 schedule 在 from 之后的下一次触发时间。支持与运营后台校验一致的写法
// （internal/admin.validSchedule）：@hourly / @daily / @weekly、@every <Go duration>、
// 5 段 cron（分 时 日 月 周，支持 * 、a-b、a-b/n、*/n、逗号列表）。cron 按 from 的时区解释。
// schedule 为空返回 ok=false：该来源不自动调度。
func NextRun(schedule string, from time.Time) (next time.Time, ok bool, err error) {
	s := strings.TrimSpace(schedule)
	switch s {
	case "":
		return time.Time{}, false, nil
	case "@hourly":
		s = "0 * * * *"
	case "@daily":
		s = "0 0 * * *"
	case "@weekly":
		s = "0 0 * * 0"
	}
	if d, found := strings.CutPrefix(s, "@every "); found {
		dur, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil || dur < time.Minute {
			return time.Time{}, false, fmt.Errorf("datasync: invalid schedule %q", schedule)
		}
		return from.Add(dur), true, nil
	}
	c, err := parseCron(s)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("datasync: invalid schedule %q: %w", schedule, err)
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	// 最多向后找一年（逐分钟太慢：先按天跳过不匹配的日期）。
	limit := t.AddDate(1, 0, 1)
	for t.Before(limit) {
		if !c.month[int(t.Month())] || !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if c.minute[t.Minute()] {
			return t, true, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false, fmt.Errorf("datasync: schedule %q never fires", schedule)
}

type cronSpec struct {
	minute, hour, dom, month, dow map[int]bool
	domStar, dowStar              bool
}

// dayMatches 实现标准 cron 的日/周语义：两者都限定时任一满足即可，否则看被限定的那个。
func (c cronSpec) dayMatches(t time.Time) bool {
	d, w := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return w
	case c.dowStar:
		return d
	default:
		return d || w
	}
}

func parseCron(s string) (cronSpec, error) {
	f := strings.Fields(s)
	if len(f) != 5 {
		return cronSpec{}, fmt.Errorf("want 5 fields, got %d", len(f))
	}
	var c cronSpec
	var err error
	if c.minute, err = parseField(f[0], 0, 59); err != nil {
		return c, err
	}
	if c.hour, err = parseField(f[1], 0, 23); err != nil {
		return c, err
	}
	if c.dom, err = parseField(f[2], 1, 31); err != nil {
		return c, err
	}
	if c.month, err = parseField(f[3], 1, 12); err != nil {
		return c, err
	}
	if c.dow, err = parseField(f[4], 0, 7); err != nil {
		return c, err
	}
	if c.dow[7] {
		c.dow[0] = true // 7 也表示周日
	}
	c.domStar, c.dowStar = f[2] == "*", f[4] == "*"
	return c, nil
}

func parseField(field string, lo, hi int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if base, st, found := strings.Cut(part, "/"); found {
			n, err := strconv.Atoi(st)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("bad step in %q", part)
			}
			step, part = n, base
		}
		from, to := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			x, err1 := strconv.Atoi(a)
			y, err2 := strconv.Atoi(b)
			if err1 != nil || err2 != nil || x > y {
				return nil, fmt.Errorf("bad range %q", part)
			}
			from, to = x, y
		default:
			x, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("bad value %q", part)
			}
			from, to = x, x
			if step > 1 {
				to = hi
			}
		}
		if from < lo || to > hi {
			return nil, fmt.Errorf("%q out of range %d-%d", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			out[v] = true
		}
	}
	return out, nil
}
