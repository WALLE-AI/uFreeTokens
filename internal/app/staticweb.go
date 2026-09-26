package app

import (
	"net/http"
	"path/filepath"
)

// serveStaticHTML 返回一个 handler，把 dir/name 这一个固定文件整个读出来
// 写回响应。dir/name 都是调用方在代码里写死的字面量，不是任何用户输入，
// 不存在路径穿越风险。
//
// 这不是给生产环境用的——是为了让 test_web/ 下手工联调用的 admin.html /
// user.html 能直接由 cmd/admin / cmd/gateway 自己的进程在根路径同源提供，
// 不用另外起一个静态文件服务器、也不用在两个业务 API 上加 CORS 中间件
// （见 test_web/README 或对应的规划记录）。dir 为空表示这个部署没有开启
// 测试页面（cmd/*/main.go 默认关闭；只有设了 UFT_TEST_WEB_DIR 才会传非空
// 值），此时返回一个正常的 404，和这个路径原本没被注册时的行为一致。
func serveStaticHTML(dir, name string) http.HandlerFunc {
	if dir == "" {
		return http.NotFound
	}
	path := filepath.Join(dir, name)
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, path)
	}
}
