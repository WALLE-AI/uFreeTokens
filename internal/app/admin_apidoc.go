package app

import (
	"fmt"
	"sort"
	"strings"
)

// AdminAPIDoc 从路由表生成运营后台接口清单（docs/admin-api.md 的自动生成部分）。
// 路由表是接口与权限的唯一来源；TestAdminAPIDoc_UpToDate 保证文档不会落后于代码。
func AdminAPIDoc() string {
	routes := AdminRouteTable()
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Pattern != routes[j].Pattern {
			return routes[i].Pattern < routes[j].Pattern
		}
		return routes[i].Method < routes[j].Method
	})
	var b strings.Builder
	b.WriteString("| 方法 | 路径 | 所需权限 |\n|---|---|---|\n")
	b.WriteString("| POST | /auth/login | （公开，限流） |\n")
	for _, rt := range routes {
		perm := string(rt.Permission)
		if perm == "" {
			perm = "（已登录即可）"
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` |\n", rt.Method, rt.Pattern, perm)
	}
	return b.String()
}
