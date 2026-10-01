package app_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/app"
)

// checkGenerated 比较生成内容与仓库里的文件；UPDATE_ADMIN_API_DOC=1 时直接重写。
func checkGenerated(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err == nil && string(got) == string(want) {
		return
	}
	if os.Getenv("UPDATE_ADMIN_API_DOC") == "1" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	t.Errorf("%s is out of date; run UPDATE_ADMIN_API_DOC=1 go test ./internal/app -run 'TestAdminOpenAPI|TestAdminAPIDoc'", path)
}

// TestAdminOpenAPI_UpToDate 保证 docs/admin-openapi.json 与前端生成的类型
// frontend/admin/src/api/generated.ts 与代码一致（每个路由都登记了请求/响应类型，
// 否则生成直接失败）。
func TestAdminOpenAPI_UpToDate(t *testing.T) {
	spec, err := app.AdminOpenAPI()
	if err != nil {
		t.Fatalf("AdminOpenAPI: %v", err)
	}
	var doc struct {
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatalf("generated spec is not valid JSON: %v", err)
	}
	ops := 0
	for _, m := range doc.Paths {
		ops += len(m)
	}
	if ops != len(app.AdminRouteTable())+1 { // +1 = POST /auth/login
		t.Errorf("spec has %d operations, route table has %d routes (+login)", ops, len(app.AdminRouteTable()))
	}
	for _, name := range []string{"Page_AccountSummary", "ChannelDetail", "LoginResult", "ErrorEnvelope"} {
		if doc.Components.Schemas[name] == nil {
			t.Errorf("schema %s missing", name)
		}
	}
	checkGenerated(t, "../../docs/admin-openapi.json", append(spec, '\n'))
	ts, err := app.AdminTypeScript()
	if err != nil {
		t.Fatalf("AdminTypeScript: %v", err)
	}
	checkGenerated(t, "../../frontend/admin/src/api/generated.ts", []byte(ts))
}
