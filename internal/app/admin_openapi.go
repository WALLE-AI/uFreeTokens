package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
)

// 运营后台的 OpenAPI 3.1 文档与前端 TypeScript 类型，都从同一份描述生成：
//   - 路由与权限来自 adminRouteTable（唯一来源）；
//   - 请求/响应的结构来自下面 routeSchemas 登记的 Go 类型，用反射转成 JSON Schema，
//     字段名、可空性与 encoding/json 的实际输出一致（嵌入字段展开、指针可空、
//     decimal 是字符串、time 是 date-time 字符串）。
// 产物是 docs/admin-openapi.json 与 frontend/admin/src/api/generated.ts，
// TestAdminOpenAPI_UpToDate 保证它们与代码一致。

// ---------- 文档用的通用响应形状（与 handler 实际写出的 JSON 一致） ----------

type listData[T any] struct {
	Data []T `json:"data"`
}

type cursorPage[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor"`
}

type statusResponse struct {
	Status string `json:"status"`
}

type idResponse struct {
	ID int64 `json:"id"`
}

type priceBookIDResponse struct {
	PriceBookID int64 `json:"price_book_id"`
}

type appliedBookResponse struct {
	AppliedBookID int64 `json:"applied_book_id"`
}

type creditGrantsResponse struct {
	Data      []admin.CreditGrantInfo `json:"data"`
	Truncated bool                    `json:"truncated"`
}

type batchApproveResponse struct {
	Results []batchApproveResult `json:"results"`
}

type batchItemsResponse struct {
	Results []batchItemResult `json:"results"`
}

type referencePriceLookupResponse struct {
	Data                map[string]referencePriceLookupResult `json:"data"`
	Currency            string                                `json:"currency"`
	LiteLLMDatasetURL   string                                `json:"litellm_dataset_url"`
	OpenRouterModelsURL string                                `json:"openrouter_models_url"`
	OpenRouterError     *string                               `json:"openrouter_error"`
	LiteLLMError        *string                               `json:"litellm_error"`
}

type routeSchema struct {
	req    any // nil = 无请求体
	resp   any // nil = 无响应体（204）或未登记
	status int // 成功状态码，0 = 200
}

// routeSchemas 按 "METHOD pattern" 登记请求/响应类型；TestAdminOpenAPI_EveryRouteDocumented
// 保证路由表里的每个接口都在这里登记了。
var routeSchemas = map[string]routeSchema{
	"POST /auth/login":                 {req: loginRequest{}, resp: adminauth.LoginResult{}},
	"POST /auth/logout":                {status: http.StatusNoContent},
	"GET /me":                          {resp: meResponse{}},
	"POST /auth/password":              {req: changePasswordRequest{}, status: http.StatusNoContent},
	"POST /auth/totp/setup":            {resp: adminauth.TOTPSetup{}},
	"POST /auth/totp/enable":           {req: totpCodeRequest{}, status: http.StatusNoContent},
	"POST /auth/totp/disable":          {req: totpCodeRequest{}, status: http.StatusNoContent},
	"GET /admin-users":                 {resp: listData[adminauth.AdminUser]{}},
	"POST /admin-users":                {req: adminauth.CreateAdminInput{}, resp: adminauth.AdminUser{}, status: http.StatusCreated},
	"PATCH /admin-users/{adminUserID}": {req: adminauth.UpdateAdminInput{}, resp: adminauth.AdminUser{}},
	"GET /admin-roles":                 {resp: listData[adminauth.Role]{}},
	"GET /meta/enums":                  {resp: metaEnumsResponse{}},

	"GET /accounts":                                              {resp: admin.Page[admin.AccountSummary]{}},
	"POST /accounts":                                             {req: admin.CreateAccountInput{}, resp: admin.Account{}, status: http.StatusCreated},
	"GET /accounts/{accountID}":                                  {resp: accountDetailResponse{}},
	"PATCH /accounts/{accountID}":                                {req: admin.UpdateAccountInput{}, resp: accountWithWallet{}},
	"GET /accounts/{accountID}/ledger":                           {resp: cursorPage[admin.LedgerEntry]{}},
	"GET /accounts/{accountID}/credit-grants":                    {resp: creditGrantsResponse{}},
	"POST /accounts/{accountID}/credit-grants":                   {req: grantCreditRequest{}, resp: admin.GrantedCredit{}},
	"GET /accounts/{accountID}/usage":                            {resp: admin.UsageResult{}},
	"POST /accounts/{accountID}/wallet/adjust":                   {req: adjustWalletRequest{}, resp: walletAdjustDTO{}},
	"POST /accounts/{accountID}/api-keys":                        {req: createAPIKeyRequest{}, resp: admin.CreatedAPIKey{}, status: http.StatusCreated},
	"GET /accounts/{accountID}/api-keys":                         {resp: admin.Page[admin.APIKeyListItem]{}},
	"GET /api-keys":                                              {resp: admin.Page[admin.APIKeyListItem]{}},
	"PATCH /api-keys/{apiKeyID}":                                 {req: admin.UpdateAPIKeyInput{}, resp: admin.APIKeyListItem{}},
	"POST /api-keys/{apiKeyID}/revoke":                           {resp: statusResponse{}},
	"POST /accounts/{accountID}/members":                         {req: addMemberRequest{}, resp: admin.AccountMember{}, status: http.StatusCreated},
	"PATCH /accounts/{accountID}/members/{userID}":               {req: updateMemberRequest{}, resp: admin.AccountMember{}},
	"DELETE /accounts/{accountID}/members/{userID}":              {status: http.StatusNoContent},
	"GET /providers":                                             {resp: admin.Page[admin.ProviderSummary]{}},
	"POST /providers":                                            {req: admin.CreateProviderInput{}, resp: admin.Provider{}, status: http.StatusCreated},
	"GET /providers/{providerID}":                                {resp: admin.ProviderDetail{}},
	"PATCH /providers/{providerID}":                              {req: admin.UpdateProviderInput{}, resp: providerUpdateResult{}},
	"GET /provider-accounts":                                     {resp: admin.Page[admin.ProviderAccountSummary]{}},
	"POST /provider-accounts":                                    {req: createProviderAccountRequest{}, resp: admin.ProviderAccount{}, status: http.StatusCreated},
	"GET /provider-accounts/{providerAccountID}":                 {resp: admin.ProviderAccountDetail{}},
	"PATCH /provider-accounts/{providerAccountID}":               {req: admin.UpdateProviderAccountInput{}, resp: admin.ProviderAccountDetail{}},
	"POST /provider-accounts/{providerAccountID}/keys":           {req: addProviderKeyRequest{}, resp: admin.ProviderKeySummary{}, status: http.StatusCreated},
	"GET /provider-accounts/{providerAccountID}/upstream-models": {resp: listData[admin.UpstreamModel]{}},
	"POST /provider-accounts/{providerAccountID}/import-models":  {req: importModelsRequest{}, resp: importModelsResponse{}},
	"PATCH /provider-keys/{providerKeyID}":                       {req: admin.UpdateProviderKeyInput{}, resp: admin.ProviderKeyInfo{}},
	"POST /provider-keys/{providerKeyID}/revoke":                 {resp: admin.ProviderKeyInfo{}},
	"GET /virtual-models":                                        {resp: admin.Page[admin.VirtualModelSummary]{}},
	"GET /virtual-models/lookup":                                 {resp: admin.VirtualModel{}},
	"POST /virtual-models":                                       {req: admin.CreateVirtualModelInput{}, resp: admin.VirtualModel{}, status: http.StatusCreated},
	"GET /virtual-models/{virtualModelID}":                       {resp: admin.VirtualModelDetail{}},
	"PATCH /virtual-models/{virtualModelID}":                     {req: admin.UpdateVirtualModelInput{}, resp: admin.VirtualModelDetail{}},
	"PUT /virtual-models/{virtualModelID}/metadata":              {req: setMetadataRequest{}, resp: statusResponse{}},
	"GET /virtual-models/{virtualModelID}/price-books":           {resp: listData[admin.PriceBookInfo]{}},
	"POST /virtual-models/{virtualModelID}/sell-price":           {req: setSellPriceRequest{}, resp: priceBookIDResponse{}, status: http.StatusCreated},
	"GET /channels":                                              {resp: admin.Page[admin.ChannelSummary]{}},
	"GET /channels/lookup":                                       {resp: admin.Channel{}},
	"POST /channels":                                             {req: admin.CreateChannelInput{}, resp: admin.Channel{}, status: http.StatusCreated},
	"GET /channels/{channelID}":                                  {resp: admin.ChannelDetail{}},
	"PATCH /channels/{channelID}":                                {req: admin.UpdateChannelInput{}, resp: admin.ChannelDetail{}},
	"GET /channels/{channelID}/price-books":                      {resp: listData[admin.PriceBookInfo]{}},
	"POST /channels/{channelID}/cost-price":                      {req: setCostPriceRequest{}, resp: priceBookIDResponse{}, status: http.StatusCreated},
	"GET /catalog/counts":                                        {resp: admin.CatalogCounts{}},
	"GET /channels/health":                                       {resp: admin.ChannelHealthReport{}},
	"GET /benchmarks":                                            {resp: listData[admin.BenchmarkSummary]{}},
	"POST /benchmarks":                                           {req: admin.CreateBenchmarkInput{}, resp: admin.Benchmark{}, status: http.StatusCreated},
	"GET /benchmarks/{benchmarkID}":                              {resp: admin.BenchmarkDetail{}},
	"PATCH /benchmarks/{benchmarkID}":                            {req: admin.UpdateBenchmarkInput{}, resp: admin.Benchmark{}},
	"POST /benchmarks/{benchmarkID}/runs":                        {req: admin.CreateBenchmarkRunInput{}, resp: admin.BenchmarkRunDetail{}, status: http.StatusCreated},
	"GET /benchmark-runs/{runID}":                                {resp: admin.BenchmarkRunDetail{}},
	"POST /benchmark-runs/{runID}/publish":                       {resp: admin.BenchmarkRunDetail{}},
	"DELETE /benchmark-runs/{runID}":                             {status: http.StatusNoContent},
	"GET /public-apps":                                           {resp: publicAppsResponse{}},
	"GET /public-app-rules":                                      {resp: listData[admin.PublicAppRule]{}},
	"POST /public-app-rules":                                     {req: admin.CreatePublicAppRuleInput{}, resp: admin.PublicAppRule{}, status: http.StatusCreated},
	"DELETE /public-app-rules/{ruleID}":                          {status: http.StatusNoContent},
	"GET /fx-rates":                                              {resp: listData[admin.FXRateInfo]{}},
	"GET /fx-rates/latest":                                       {resp: listData[admin.FXRateInfo]{}},
	"POST /fx-rates":                                             {req: setFXRateRequest{}, resp: admin.FXRateInfo{}},
	"POST /pricing/preview":                                      {req: admin.PricingPreviewInput{}, resp: admin.PricingPreviewResult{}},
	"POST /pricesync/reference-price-lookup":                     {req: referencePriceLookupRequest{}, resp: referencePriceLookupResponse{}},
	"GET /price-sources":                                         {resp: listData[admin.PriceSourceInfo]{}},
	"GET /price-sources/{priceSourceID}":                         {resp: admin.PriceSourceInfo{}},
	"POST /price-sources":                                        {req: createPriceSourceRequest{}, resp: idResponse{}, status: http.StatusCreated},
	"PATCH /price-sources/{priceSourceID}":                       {req: admin.UpdatePriceSourceInput{}, resp: admin.PriceSourceInfo{}},
	"POST /price-sources/{priceSourceID}/run":                    {resp: admin.PriceSourceInfo{}, status: http.StatusAccepted},
	"GET /price-sources/{priceSourceID}/runs":                    {resp: listData[admin.DataSourceRun]{}},
	"GET /upstream-offers":                                       {resp: admin.Page[offers.Offer]{}},
	"GET /upstream-offers/{offerID}":                             {resp: offers.Offer{}},
	"POST /upstream-offers/{offerID}/status":                     {req: setOfferStatusRequest{}, resp: offers.Offer{}},
	"POST /upstream-offers/{offerID}/adopt":                      {req: adoptOfferRequest{}, resp: adoptOfferResponse{}, status: http.StatusCreated},
	"GET /pricesync/price-comparison":                            {resp: admin.Page[admin.PriceComparisonRow]{}},
	"GET /model-aliases":                                         {resp: admin.Page[admin.ModelAlias]{}},
	"GET /model-aliases/namespaces":                              {resp: listData[string]{}},
	"PUT /model-aliases":                                         {req: admin.SetModelAliasInput{}, resp: admin.SetModelAliasResult{}},
	"POST /channels/{channelID}/price-observations":              {req: priceObservationRequest{}, resp: ingestResultDTO{}, status: http.StatusCreated},
	"POST /providers/{providerID}/price-observations":            {req: unmappedObservationRequest{}, resp: unmappedIngestResultDTO{}, status: http.StatusCreated},
	"GET /price-change-requests":                                 {resp: admin.Page[admin.ChangeRequestSummary]{}},
	"GET /price-change-requests/{changeRequestID}":               {resp: admin.ChangeRequestDetail{}},
	"POST /price-change-requests/{changeRequestID}/approve":      {req: decideChangeRequestBody{}, resp: appliedBookResponse{}},
	"POST /price-change-requests/{changeRequestID}/reject":       {req: decideChangeRequestBody{}, resp: statusResponse{}},
	"POST /price-change-requests/batch-approve":                  {req: batchApproveRequest{}, resp: batchApproveResponse{}},
	"GET /pending-model-listings":                                {resp: admin.Page[admin.PendingListing]{}},
	"POST /pending-model-listings/{listingID}/publish":           {req: publishListingRequest{}, resp: publishListingResultDTO{}, status: http.StatusCreated},
	"POST /pending-model-listings/{listingID}/dismiss":           {req: dismissListingRequest{}, resp: statusResponse{}},
	"POST /pending-model-listings/batch-dismiss":                 {req: batchDismissRequest{}, resp: batchItemsResponse{}},
	"GET /stats/overview":                                        {resp: admin.StatsOverview{}},
	"GET /stats/usage":                                           {resp: admin.UsageResult{}},
	"GET /request-logs":                                          {resp: cursorPage[admin.RequestLogItem]{}},
	"GET /request-logs/{requestID}":                              {resp: admin.RequestLogDetail{}},
	"GET /audit-logs":                                            {resp: cursorPage[admin.AuditLogEntry]{}},
	"GET /todo-counts":                                           {resp: admin.TodoCounts{}},
}

// ---------- 反射 → JSON Schema ----------

type schemaGen struct {
	defs  map[string]any          // components.schemas
	types map[string]reflect.Type // 名字 → 类型（生成 TS 用）
}

var (
	decimalType = reflect.TypeOf(decimal.Decimal{})
	timeType    = reflect.TypeOf(time.Time{})
	rawType     = reflect.TypeOf(json.RawMessage{})
	genericArgs = regexp.MustCompile(`\[(.*)\]$`)
)

// schemaName 把 Go 类型名转成文档里的名字：包路径去掉，泛型参数拼进名字
// （admin.Page[admin.AccountSummary] → Page_AccountSummary）。
func schemaName(t reflect.Type) string {
	name := t.Name()
	base, _, _ := strings.Cut(name, "[")
	base = strings.ToUpper(base[:1]) + base[1:]
	if m := genericArgs.FindStringSubmatch(name); m != nil {
		parts := strings.Split(m[1], ",")
		for i, p := range parts {
			p = p[strings.LastIndex(p, ".")+1:]
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		return base + "_" + strings.Join(parts, "_")
	}
	return base
}

func (g *schemaGen) schema(t reflect.Type) any {
	switch t {
	case decimalType:
		return map[string]any{"type": "string", "format": "decimal"}
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case rawType:
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		inner := g.schema(t.Elem())
		return map[string]any{"anyOf": []any{inner, map[string]any{"type": "null"}}}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		if t.Name() == "" { // 匿名结构体：内联
			props, required := map[string]any{}, []string{}
			g.fields(t, props, &required)
			sort.Strings(required)
			return map[string]any{"type": "object", "properties": props, "required": required}
		}
		name := schemaName(t)
		if _, ok := g.defs[name]; !ok {
			g.defs[name] = nil // 先占位，处理递归类型
			g.types[name] = t
			props, required := map[string]any{}, []string{}
			g.fields(t, props, &required)
			sort.Strings(required)
			g.defs[name] = map[string]any{"type": "object", "properties": props, "required": required}
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

// jsonField 按 encoding/json 的规则解析字段名；返回 skip=true 表示不输出。
func jsonField(f reflect.StructField) (name string, omitempty, skip bool) {
	if !f.IsExported() {
		return "", false, true
	}
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	name, opts, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	return name, strings.Contains(opts, "omitempty"), false
}

func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Tag.Get("json") == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				g.fields(ft, props, required)
				continue
			}
		}
		name, omitempty, skip := jsonField(f)
		if skip {
			continue
		}
		props[name] = g.schema(f.Type)
		if !omitempty {
			*required = append(*required, name)
		}
	}
}

var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

// AdminOpenAPI 生成 OpenAPI 3.1 文档（JSON，键有序、输出稳定）。
func AdminOpenAPI() ([]byte, error) {
	doc, _, err := buildOpenAPI()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

func buildOpenAPI() (map[string]any, *schemaGen, error) {
	g := &schemaGen{defs: map[string]any{}, types: map[string]reflect.Type{}}
	paths := map[string]map[string]any{}
	routes := append([]AdminRoute{{Method: http.MethodPost, Pattern: "/auth/login", Permission: "public"}}, AdminRouteTable()...)
	for _, rt := range routes {
		key := rt.Method + " " + rt.Pattern
		rs, ok := routeSchemas[key]
		if !ok {
			return nil, nil, fmt.Errorf("route %s has no entry in routeSchemas", key)
		}
		op := map[string]any{"operationId": operationID(rt.Method, rt.Pattern), "x-permission": string(rt.Permission)}
		if rt.Permission == "public" {
			op["security"] = []any{}
			op["x-permission"] = ""
		}
		var params []any
		for _, m := range pathParam.FindAllStringSubmatch(rt.Pattern, -1) {
			ptype := "integer"
			if m[1] == "requestID" {
				ptype = "string"
			}
			params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": ptype}})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if rs.req != nil {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{
				"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(rs.req))}}}
		}
		status := rs.status
		if status == 0 {
			status = http.StatusOK
		}
		resp := map[string]any{"description": http.StatusText(status)}
		if rs.resp != nil {
			resp["content"] = map[string]any{"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(rs.resp))}}
		}
		op["responses"] = map[string]any{
			fmt.Sprint(status): resp,
			"default":          map[string]any{"description": "Error", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorEnvelope"}}}},
		}
		if paths[rt.Pattern] == nil {
			paths[rt.Pattern] = map[string]any{}
		}
		paths[rt.Pattern][strings.ToLower(rt.Method)] = op
	}
	g.defs["ErrorEnvelope"] = map[string]any{"type": "object", "required": []string{"error"}, "properties": map[string]any{
		"error": map[string]any{"type": "object", "required": []string{"message", "type", "code"}, "properties": map[string]any{
			"message": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"},
			"code": map[string]any{"type": "string"}, "request_id": map[string]any{"type": "string"}}}}}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "uFreeTokens Admin API", "version": "1",
			"description": "运营后台接口（cmd/admin）。约定见 docs/admin-api.md；本文件由 internal/app.AdminOpenAPI 生成，勿手改。"},
		"security":   []any{map[string]any{"bearer": []any{}}},
		"paths":      paths,
		"components": map[string]any{"schemas": g.defs, "securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}}},
	}
	return doc, g, nil
}

func operationID(method, pattern string) string {
	parts := []string{strings.ToLower(method)}
	for _, seg := range strings.Split(strings.Trim(pattern, "/"), "/") {
		seg = strings.Trim(seg, "{}")
		for _, w := range strings.FieldsFunc(seg, func(r rune) bool { return r == '-' || r == '_' }) {
			parts = append(parts, strings.ToUpper(w[:1])+w[1:])
		}
	}
	return strings.Join(parts, "")
}

// ---------- JSON Schema → TypeScript ----------

// AdminTypeScript 生成前端类型声明（frontend/admin/src/api/generated.ts）。
func AdminTypeScript() (string, error) {
	_, g, err := buildOpenAPI()
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(g.types))
	for n := range g.types {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("// 由 internal/app.AdminTypeScript 从 Go 类型生成（与 docs/admin-openapi.json 同源），勿手改。\n")
	b.WriteString("// 更新：UPDATE_ADMIN_API_DOC=1 go test ./internal/app -run TestAdminOpenAPI_UpToDate\n")
	b.WriteString("/* eslint-disable */\n\n")
	for _, n := range names {
		t := g.types[n]
		fmt.Fprintf(&b, "export interface %s {\n", n)
		tsFields(&b, t)
		b.WriteString("}\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func tsFields(b *strings.Builder, t reflect.Type) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Tag.Get("json") == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				tsFields(b, ft)
				continue
			}
		}
		name, omitempty, skip := jsonField(f)
		if skip {
			continue
		}
		opt := ""
		if omitempty {
			opt = "?"
		}
		fmt.Fprintf(b, "  %s%s: %s;\n", name, opt, tsType(f.Type))
	}
}

func tsType(t reflect.Type) string {
	switch t {
	case decimalType, timeType:
		return "string"
	case rawType:
		return "unknown"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return tsType(t.Elem()) + " | null"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string"
		}
		inner := tsType(t.Elem())
		if strings.Contains(inner, " | ") {
			inner = "(" + inner + ")"
		}
		// nil 切片会编码成 null
		return inner + "[] | null"
	case reflect.Map:
		return "Record<string, " + tsType(t.Elem()) + "> | null"
	case reflect.Interface:
		return "unknown"
	case reflect.Struct:
		if t.Name() == "" { // 匿名结构体：内联
			var inner strings.Builder
			tsFields(&inner, t)
			return "{ " + strings.ReplaceAll(strings.TrimSpace(inner.String()), "\n  ", " ") + " }"
		}
		return schemaName(t)
	}
	return "unknown"
}
