package research

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DomainSource 是 fetch_page 的出站白名单（实施方案 D3）：配置项附加域名 + 已登记供应商的域名
// （providers.allowed_hosts 的可注册域名，api.deepseek.com → deepseek.com）+ 已配置数据源的页面域名。
// 不开放通用网页搜索。结果缓存 5 分钟。
type DomainSource struct {
	Pool  *pgxpool.Pool
	Extra []string

	mu   sync.Mutex
	at   time.Time
	list []string
}

// SplitDomains 把逗号分隔的配置项拆成域名列表。
func SplitDomains(s string) []string {
	var out []string
	for _, d := range strings.Split(s, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// Domains 返回当前白名单。
func (d *DomainSource) Domains(ctx context.Context) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.list != nil && time.Since(d.at) < 5*time.Minute {
		return d.list, nil
	}
	set := map[string]bool{}
	for _, x := range d.Extra {
		set[x] = true
	}
	if d.Pool != nil {
		rows, err := d.Pool.Query(ctx,
			`SELECT h FROM providers, unnest(allowed_hosts) AS h
			 UNION SELECT url FROM price_sources WHERE url IS NOT NULL AND url <> ''`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			host := v
			if strings.Contains(v, "://") {
				if u, err := url.Parse(v); err == nil {
					host = u.Hostname()
				}
			}
			if host = strings.ToLower(strings.TrimSpace(host)); host != "" && strings.Contains(host, ".") {
				set[RegistrableDomain(host)] = true
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	list := make([]string, 0, len(set))
	for k := range set {
		list = append(list, k)
	}
	sort.Strings(list)
	d.list, d.at = list, time.Now()
	return list, nil
}
