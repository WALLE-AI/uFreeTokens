package app_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/app"
)

const gatewaySpecPath = "../../docs/gateway-openapi.json"

// gatewaySpec 是测试里关心的那部分 OpenAPI 结构。
type gatewaySpec struct {
	Paths      map[string]map[string]map[string]any `json:"paths"`
	Components struct {
		Schemas    map[string]any `json:"schemas"`
		ErrorCodes []struct {
			Code      string `json:"code"`
			Status    int    `json:"status"`
			Retryable bool   `json:"retryable"`
		} `json:"x-error-codes"`
	} `json:"components"`
}

func loadGatewaySpec(t *testing.T) ([]byte, gatewaySpec) {
	t.Helper()
	raw, err := app.GatewayOpenAPI()
	if err != nil {
		t.Fatalf("GatewayOpenAPI: %v", err)
	}
	var spec gatewaySpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("generated spec is not valid JSON: %v", err)
	}
	return raw, spec
}

// TestGatewayOpenAPI_UpToDate 保证 docs/gateway-openapi.json 与代码一致；
// UPDATE_GATEWAY_API_DOC=1 时直接重写（同 admin 的 UPDATE_ADMIN_API_DOC 机制）。
func TestGatewayOpenAPI_UpToDate(t *testing.T) {
	raw, spec := loadGatewaySpec(t)

	// operationId 是前端依赖的稳定标识。
	wantOps := map[string]string{
		"GET /v1/catalog":               "getCatalog",
		"GET /v1/models":                "listModels",
		"GET /v1/usage":                 "getUsage",
		"POST /v1/chat/completions":     "createChatCompletion",
		"POST /v1/embeddings":           "createEmbedding",
		"POST /v1/messages":             "createMessage",
		"POST /v1/completions":          "createCompletion",
		"POST /v1/images/generations":   "createImage",
		"POST /v1/audio/transcriptions": "createTranscription",
		"POST /v1/audio/speech":         "createSpeech",
	}
	got := map[string]string{}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			id, _ := op["operationId"].(string)
			got[strings.ToUpper(method)+" "+path] = id
			for _, k := range []string{"tags", "summary", "description", "x-i18n", "responses"} {
				if op[k] == nil {
					t.Errorf("%s %s: missing %s", method, path, k)
				}
			}
		}
	}
	for k, id := range wantOps {
		if got[k] != id {
			t.Errorf("%s: operationId = %q, want %q", k, got[k], id)
		}
	}
	if len(got) != len(wantOps) {
		t.Errorf("spec has %d operations, want %d", len(got), len(wantOps))
	}
	for _, name := range []string{"Error", "CatalogListResponse", "ChatCompletionRequest", "MessageResponse"} {
		if spec.Components.Schemas[name] == nil {
			t.Errorf("schema %s missing", name)
		}
	}

	want := append(raw, '\n')
	cur, err := os.ReadFile(gatewaySpecPath)
	if err == nil && string(cur) == string(want) {
		return
	}
	if os.Getenv("UPDATE_GATEWAY_API_DOC") == "1" {
		if err := os.WriteFile(gatewaySpecPath, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", gatewaySpecPath, err)
		}
		return
	}
	t.Errorf("%s is out of date; run UPDATE_GATEWAY_API_DOC=1 go test ./internal/app -run TestGatewayOpenAPI_UpToDate", gatewaySpecPath)
}

// 数据面源码：/v1/* 请求可能经过的所有写错误的地方（不含 admin_*、console）。
var gatewaySourceDirs = []string{"../relay", "../auth", "../httpx", "../ratelimit"}
var gatewaySourceFiles = []string{"gateway.go", "models.go", "usage.go", "catalog.go"}

// nonDataPlaneCodes 是扫描范围内出现、但不会出现在 /v1/* 上的错误码。
var nonDataPlaneCodes = map[string]string{
	"unauthorized": "httpx.RequireBearerToken，只用于 cmd/admin",
}

// extraStatuses 允许某个错误码以登记状态之外的 HTTP 状态出现（目前没有）。
var extraStatuses = map[string][]int{}

var (
	// httpx.WriteError(w, r, http.StatusXxx, "code", ...)
	reWriteErrorLiteral = regexp.MustCompile(`WriteError\(\s*w,\s*r,\s*http\.(Status\w+),\s*"([a-z_]+)"`)
	// writeRateLimited(w, r, res, "code", ...) 固定 429
	reRateLimited = regexp.MustCompile(`writeRateLimited\(\s*w,\s*r,\s*\w+,\s*"([a-z_]+)"`)
	// classifyRelayError / clientFacingError：return http.StatusXxx, "code"
	reReturnStatusCode = regexp.MustCompile(`return\s+http\.(Status\w+),\s*"([a-z_]+)"`)
	// 状态码/错误码是变量的 WriteError 调用：只允许上面两类来源（分类函数的返回值、writeRateLimited 的 429）。
	reWriteErrorDynamic = regexp.MustCompile(`WriteError\(\s*w,\s*r,\s*(status, code|http\.StatusTooManyRequests, code),`)
	reWriteErrorAny     = regexp.MustCompile(`WriteError\(`)
)

func httpStatusByName(name string) int {
	for s := 100; s < 600; s++ {
		text := http.StatusText(s)
		if text == "" {
			continue
		}
		if "Status"+strings.Join(strings.Fields(strings.ReplaceAll(text, "-", " ")), "") == name {
			return s
		}
	}
	return 0
}

// TestGatewayErrorCodes_Registered 扫描数据面源码里所有写出错误码的地方，
// 保证每个错误码都登记在 components["x-error-codes"] 里且 HTTP 状态一致，
// 同时登记表里没有源码中已不存在的错误码。
func TestGatewayErrorCodes_Registered(t *testing.T) {
	_, spec := loadGatewaySpec(t)
	registry := map[string]int{}
	var order []string
	for _, c := range spec.Components.ErrorCodes {
		registry[c.Code] = c.Status
		order = append(order, c.Code)
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("x-error-codes is not sorted by code: %v", order)
	}

	var files []string
	for _, dir := range gatewaySourceDirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	files = append(files, gatewaySourceFiles...)

	seen := map[string]bool{}
	check := func(file, code string, status int) {
		if _, skip := nonDataPlaneCodes[code]; skip {
			return
		}
		seen[code] = true
		want, ok := registry[code]
		if !ok {
			t.Errorf("%s: error code %q (HTTP %d) is not registered in gatewayErrorCodes", file, code, status)
			return
		}
		if status == want {
			return
		}
		for _, s := range extraStatuses[code] {
			if s == status {
				return
			}
		}
		t.Errorf("%s: error code %q emitted with HTTP %d, registered as %d", file, code, status, want)
	}

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, m := range reWriteErrorLiteral.FindAllStringSubmatch(text, -1) {
			status := httpStatusByName(m[1])
			if status == 0 {
				t.Errorf("%s: unknown status constant %s", file, m[1])
			}
			check(file, m[2], status)
		}
		for _, m := range reRateLimited.FindAllStringSubmatch(text, -1) {
			check(file, m[1], http.StatusTooManyRequests)
		}
		for _, m := range reReturnStatusCode.FindAllStringSubmatch(text, -1) {
			status := httpStatusByName(m[1])
			if status == 0 {
				t.Errorf("%s: unknown status constant %s", file, m[1])
			}
			check(file, m[2], status)
		}
		// 任何 WriteError 调用都必须能被上面的规则识别，否则新加的写法会漏检。
		for _, loc := range reWriteErrorAny.FindAllStringIndex(text, -1) {
			if loc[0] >= 5 && text[loc[0]-5:loc[0]] == "func " {
				continue // WriteError 的定义本身
			}
			rest := text[loc[0]:]
			if reWriteErrorLiteral.FindStringIndex(rest) == nil || reWriteErrorLiteral.FindStringIndex(rest)[0] != 0 {
				if m := reWriteErrorDynamic.FindStringIndex(rest); m == nil || m[0] != 0 {
					line := strings.Count(text[:loc[0]], "\n") + 1
					t.Errorf("%s:%d: unrecognised WriteError call; register its code and extend this test", file, line)
				}
			}
		}
	}

	for code := range registry {
		if !seen[code] {
			t.Errorf("error code %q is registered but never emitted by the data-plane sources", code)
		}
	}
}
