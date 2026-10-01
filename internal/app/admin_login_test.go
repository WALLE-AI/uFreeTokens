// 管理员登录、会话与权限（B5 安全基线）的端到端测试：
// docs/运营后台接口与数据库设计问题分析及执行方案.md §3 B5。
package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
)

var adminSeq atomic.Int64

// testAdmin 是 loginAs 创建并登录的测试管理员。
type testAdmin struct {
	ID       int64
	Email    string
	Password string
}

// loginAs 用给定名称与角色新建一个管理员，经 POST /auth/login 登录，并让后续
// 请求都带上这个会话令牌。
func (c *adminClient) loginAs(pool *pgxpool.Pool, name string, roles ...string) testAdmin {
	c.t.Helper()
	email := fmt.Sprintf("admin-%d-%d@test.local", time.Now().UnixNano(), adminSeq.Add(1))
	password := "correct-horse-battery"
	u, err := adminauth.New(pool, adminauth.Config{}).CreateAdmin(c.t.Context(), adminauth.CreateAdminInput{
		Email: email, Name: name, Password: password, Roles: roles,
	})
	if err != nil {
		c.t.Fatalf("create admin: %v", err)
	}
	c.token = ""
	status, body := c.do(http.MethodPost, "/auth/login", map[string]any{"email": email, "password": password}, nil)
	if status != http.StatusOK {
		c.t.Fatalf("login: status = %d, body = %s", status, body)
	}
	var res struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &res); err != nil || !strings.HasPrefix(res.Token, "uas_") {
		c.t.Fatalf("login response = %s (err=%v), want a uas_ session token", body, err)
	}
	c.token = res.Token
	return testAdmin{ID: u.ID, Email: email, Password: password}
}

func TestAdminLogin_SessionLifecycle(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, false)
	defer done()
	me := ac.loginAs(pool, "赵六", "support")

	var who struct {
		ID          int64                  `json:"id"`
		Name        string                 `json:"name"`
		Roles       []string               `json:"roles"`
		Permissions []adminauth.Permission `json:"permissions"`
		BreakGlass  bool                   `json:"break_glass"`
	}
	ac.get("/me", &who)
	if who.ID != me.ID || who.Name != "赵六" || who.BreakGlass || len(who.Roles) != 1 || who.Roles[0] != "support" {
		t.Fatalf("/me = %+v, want the logged-in support admin", who)
	}

	// 登出后令牌立即失效。
	if status, body := ac.do(http.MethodPost, "/auth/logout", nil, nil); status != http.StatusNoContent {
		t.Fatalf("logout: %d %s", status, body)
	}
	if status, _ := ac.do(http.MethodGet, "/me", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("/me after logout: status = %d, want 401", status)
	}

	// 错误密码 → 401；同一邮箱连续失败 5 次后 → 429（即使密码正确）。
	ac.token = ""
	for i := range 5 {
		if status, _ := ac.do(http.MethodPost, "/auth/login", map[string]any{"email": me.Email, "password": "wrong-password-" + fmt.Sprint(i)}, nil); status != http.StatusUnauthorized {
			t.Fatalf("wrong password #%d: status = %d, want 401", i, status)
		}
	}
	if status, _ := ac.do(http.MethodPost, "/auth/login", map[string]any{"email": me.Email, "password": me.Password}, nil); status != http.StatusTooManyRequests {
		t.Fatalf("login after 5 failures: status = %d, want 429", status)
	}
	// 伪造的会话令牌 → 401。
	ac.token = "uas_forged"
	if status, _ := ac.do(http.MethodGet, "/me", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("forged session: status = %d, want 401", status)
	}
}

func TestAdminRBAC_SupportCannotWrite(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	ac.loginAs(pool, "客服小王", "support")

	// 只读接口可以访问。
	ac.get("/accounts?page_size=1", nil)
	ac.get("/audit-logs?limit=1", nil)

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/accounts"},
		{http.MethodPost, "/accounts/1/wallet/adjust"},
		{http.MethodPost, "/accounts/1/credit-grants"},
		{http.MethodPost, "/provider-accounts/1/keys"},
		{http.MethodGet, "/provider-accounts/1/upstream-models"},
		{http.MethodPost, "/price-change-requests/1/approve"},
		{http.MethodPatch, "/channels/1"},
		{http.MethodGet, "/admin-users"},
	} {
		status, body := ac.do(c.method, c.path, map[string]any{}, nil)
		if status != http.StatusForbidden || !strings.Contains(string(body), "permission_denied") {
			t.Errorf("%s %s as support: status = %d body = %s, want 403 permission_denied", c.method, c.path, status, body)
		}
	}
}

// TestAdminRBAC_BaseURLRequiresProviderKeyPermission：catalog:write 可以改上游账号
// 的名称，但改 base_url 需要 provider_key:write（它决定上游密钥发往哪里）。
func TestAdminRBAC_BaseURLRequiresProviderKeyPermission(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, false)
	defer done()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var provider, pa struct {
		ID int64 `json:"id"`
	}
	ac.post("/providers", map[string]any{"code": "rbac-" + suffix, "name": "x", "protocol": "openai"}, &provider)
	ac.post("/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "acc", "base_url": "https://api.example.com/v1"}, &pa)

	// 自定义一个只有 catalog:write 的角色。
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO admin_roles (code, name, permissions) VALUES ('catalog_only_test', 'test', ARRAY['catalog:read','catalog:write']) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	ac.loginAs(pool, "目录编辑", "catalog_only_test")
	path := fmt.Sprintf("/provider-accounts/%d", pa.ID)
	if status, body := ac.do(http.MethodPatch, path, map[string]any{"name": "renamed"}, nil); status != http.StatusOK {
		t.Fatalf("rename: %d %s", status, body)
	}
	if status, _ := ac.do(http.MethodPatch, path, map[string]any{"base_url": "https://evil.example.org"}, nil); status != http.StatusForbidden {
		t.Fatalf("change base_url without provider_key:write: status = %d, want 403", status)
	}
}

func TestAdminUsers_Manage(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, false)
	defer done()
	root := ac.loginAs(pool, "根管理员", "super_admin")

	var created adminauth.AdminUser
	ac.post("/admin-users", map[string]any{
		"email": fmt.Sprintf("ops-%d@test.local", time.Now().UnixNano()), "name": "运营甲", "password": "a-long-password", "roles": []string{"operator"},
	}, &created)
	if created.ID == 0 || len(created.Roles) != 1 || created.Roles[0] != "operator" {
		t.Fatalf("created admin = %+v", created)
	}
	// 太短的密码 → 400；重复邮箱 → 409。
	if status, _ := ac.do(http.MethodPost, "/admin-users", map[string]any{"email": "x@test.local", "name": "x", "password": "short", "roles": []string{"support"}}, nil); status != http.StatusBadRequest {
		t.Errorf("short password: status = %d, want 400", status)
	}
	if status, _ := ac.do(http.MethodPost, "/admin-users", map[string]any{"email": created.Email, "name": "x", "password": "a-long-password", "roles": []string{"support"}}, nil); status != http.StatusConflict {
		t.Errorf("duplicate email: status = %d, want 409", status)
	}
	// 停用 → 其会话被注销。
	status, body := ac.do(http.MethodPatch, fmt.Sprintf("/admin-users/%d", created.ID), map[string]any{"status": "disabled"}, nil)
	if status != http.StatusOK {
		t.Fatalf("disable admin: %d %s", status, body)
	}
	var audit struct {
		Data []admin.AuditLogEntry `json:"data"`
	}
	ac.get(fmt.Sprintf("/audit-logs?target_type=admin_user&target_id=%d", created.ID), &audit)
	if len(audit.Data) != 2 || audit.Data[0].ActorID != root.ID {
		t.Errorf("admin_user audit = %+v, want create+update by root", audit.Data)
	}
	// system 身份不能改。
	if status, _ := ac.do(http.MethodPatch, "/admin-users/0", map[string]any{"name": "x"}, nil); status != http.StatusBadRequest {
		t.Errorf("patch system: status = %d, want 400", status)
	}
}

// TestAdminRoutes_EveryRouteHasPermission 保证路由表里除了少数"仅需登录"的接口外，
// 每个接口都声明了具体权限点，且权限点都是已知的。
func TestAdminRoutes_EveryRouteHasPermission(t *testing.T) {
	known := map[adminauth.Permission]bool{}
	for _, p := range adminauth.AllPermissions {
		known[p] = true
	}
	authOnly := map[string]bool{"POST /auth/logout": true, "GET /me": true, "POST /auth/password": true, "GET /meta/enums": true, "GET /todo-counts": true,
		"POST /auth/totp/setup": true, "POST /auth/totp/enable": true, "POST /auth/totp/disable": true}
	seen := map[string]bool{}
	for _, rt := range app.AdminRouteTable() {
		key := rt.Method + " " + rt.Pattern
		if seen[key] {
			t.Errorf("duplicate route %s", key)
		}
		seen[key] = true
		if rt.Permission == "" {
			if !authOnly[key] {
				t.Errorf("route %s has no permission", key)
			}
			continue
		}
		if !known[rt.Permission] {
			t.Errorf("route %s uses unknown permission %q", key, rt.Permission)
		}
		if strings.HasSuffix(string(rt.Permission), ":read") && rt.Method != http.MethodGet && rt.Pattern != "/pricing/preview" && rt.Pattern != "/pricesync/reference-price-lookup" &&
			rt.Pattern != "/provider-accounts/{providerAccountID}/import-models" {
			t.Errorf("write route %s only requires read permission %q", key, rt.Permission)
		}
	}
}

// TestAdminErrors_ConstraintViolationsDoNotLeakSQL：引用不存在的对象返回 422
// invalid_reference，响应里不出现 Postgres 原文。
func TestAdminErrors_ConstraintViolationsDoNotLeakSQL(t *testing.T) {
	ac, done := newAdminTestServer(t, false)
	defer done()
	status, body := ac.do(http.MethodPost, "/channels", map[string]any{
		"virtual_model_id": 999999999, "provider_account_id": 999999999, "upstream_model": "x",
	}, nil)
	if status != http.StatusUnprocessableEntity || !strings.Contains(string(body), "invalid_reference") {
		t.Fatalf("status = %d body = %s, want 422 invalid_reference", status, body)
	}
	if strings.Contains(string(body), "SQLSTATE") || strings.Contains(string(body), "INSERT") {
		t.Errorf("response leaks SQL details: %s", body)
	}
	// 非法的 X-Request-Id 被替换，而不是原样回显。
	req, _ := http.NewRequest(http.MethodGet, ac.baseURL+"/healthz", nil)
	req.Header.Set("X-Request-Id", "bad id <script>")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Request-Id"); got == "" || strings.ContainsAny(got, " <>") {
		t.Errorf("X-Request-Id = %q, want a freshly generated id", got)
	}
}

// TestAdminAPIDoc_UpToDate 保证 docs/admin-api.md 的接口清单与路由表一致。
// UPDATE_ADMIN_API_DOC=1 时直接重写文档。
func TestAdminAPIDoc_UpToDate(t *testing.T) {
	const path = "../../docs/admin-api.md"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(raw)
	const begin, end = "<!-- routes:begin -->\n", "<!-- routes:end -->"
	i, j := strings.Index(doc, begin), strings.Index(doc, end)
	if i < 0 || j < i {
		t.Fatalf("%s is missing the routes markers", path)
	}
	want := doc[:i+len(begin)] + app.AdminAPIDoc() + doc[j:]
	if want == doc {
		return
	}
	if os.Getenv("UPDATE_ADMIN_API_DOC") == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	t.Errorf("%s is out of date; run UPDATE_ADMIN_API_DOC=1 go test ./internal/app -run TestAdminAPIDoc_UpToDate", path)
}

// TestAdminRBAC_Matrix 从路由表自动生成"角色 × 接口"矩阵：没有权限点的角色必须
// 拿到 403 permission_denied，有权限点的角色一定不会被路由层拒绝（业务层可以因为
// 参数、对象不存在等返回其他错误）。路径参数一律填一个不存在的 ID，请求体为空对象，
// 所以不会产生真实的写入。
func TestAdminRBAC_Matrix(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	rows, err := pool.Query(t.Context(), `SELECT code, permissions FROM admin_roles WHERE code IN ('operator','pricing','finance','support')`)
	if err != nil {
		t.Fatalf("load roles: %v", err)
	}
	roles := map[string][]string{}
	for rows.Next() {
		var code string
		var perms []string
		if err := rows.Scan(&code, &perms); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		roles[code] = perms
	}
	rows.Close()
	// handler 内还有额外权限检查的接口：有路由权限点也可能 403，只校验"无权限点必 403"。
	extraChecked := map[string]bool{"POST /provider-accounts/{providerAccountID}/import-models": true}
	param := regexp.MustCompile(`\{[^}]+\}`)

	for role, perms := range roles {
		ac.loginAs(pool, "矩阵-"+role, role)
		has := func(p adminauth.Permission) bool { return slices.Contains(perms, string(p)) }
		for _, rt := range app.AdminRouteTable() {
			if strings.HasPrefix(rt.Pattern, "/auth/") || rt.Permission == "" {
				continue
			}
			path := param.ReplaceAllString(rt.Pattern, "999999999")
			var body any
			if rt.Method != http.MethodGet && rt.Method != http.MethodDelete {
				body = map[string]any{}
			}
			status, raw := ac.do(rt.Method, path, body, nil)
			denied := status == http.StatusForbidden && strings.Contains(string(raw), "permission_denied")
			key := rt.Method + " " + rt.Pattern
			switch {
			case !has(rt.Permission) && !denied:
				t.Errorf("%s as %s (lacks %s): status = %d, want 403 permission_denied", key, role, rt.Permission, status)
			case has(rt.Permission) && denied && !extraChecked[key]:
				t.Errorf("%s as %s (has %s): got permission_denied", key, role, rt.Permission)
			}
		}
	}
}
