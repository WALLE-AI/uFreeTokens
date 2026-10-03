// Package routes 是绑定现有管理路由的智能体工具（设计 §3.2）：每个工具都是 adminRouteTable 中
// 某个路由的薄封装，以发起者（读）或审批人（写）的身份在进程内调用，权限、审计、幂等与乐观锁
// 全部沿用路由本身的实现，不新增业务旁路。
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
)

// Param 是一个查询参数或路径参数的声明。
type Param struct {
	Type        string // string / integer / boolean / number
	Description string
	Enum        []string
}

// Target 描述写工具作用的对象：类型名与取 ID 的路径参数。
type Target struct {
	Type  string // price_change_request / pending_listing / upstream_offer / virtual_model / ...
	Param string // 路径参数名（Pattern 中的原名）；空 = 从 BodyKey 取
	// BodyKey 非空时目标 ID 取自请求体字段（如 PUT /model-aliases 的 namespace+name）。
	BodyKey []string
}

// Spec 是一个路由工具的声明。
type Spec struct {
	Name        string
	Description string
	Risk        kernel.Risk
	Method      string
	Pattern     string
	Query       map[string]Param
	// Body 为 true 时把路由请求体 Schema（app.RouteRequestSchema）展开到工具参数的顶层。
	Body bool
	// BodyFields 是暴露给模型的请求体字段白名单；nil = 全部。
	BodyFields []string
	// Required 是必填参数（路径参数总是必填，不用写）。
	Required []string
	// Source 非空表示结果含外部文本（不可信），值为来源标注。
	Source string
	// Shape 裁剪结果、生成摘要；nil = DefaultShape。
	Shape func(raw []byte) (any, string)

	// 以下仅写工具使用。
	Target Target
	// Before 是读取目标对象当前快照的 GET 路由（路径参数与本工具相同）；空 = 无快照。
	Before string
	// IfMatch 为 true 时执行带 If-Match（ETag 来自 Before 的响应头）。
	IfMatch bool
	// Summarize 生成提案的一句话描述；nil = "工具名 #目标"。
	Summarize func(args map[string]any) string
}

// BuildDeps 是构建注册表需要的外部能力（由 internal/app 提供，避免本包依赖 app 造成循环）。
type BuildDeps struct {
	// BodySchema 返回路由请求体的 JSON Schema。
	BodySchema func(method, pattern string) (map[string]any, bool)
	// RoutePermission 返回路由在 adminRouteTable 中的权限点；路由不存在 ok=false。
	RoutePermission func(method, pattern string) (adminauth.Permission, bool)
	Dispatcher      *Dispatcher
}

// Tool 是一个已绑定路由的工具，实现 kernel.Tool、kernel.Proposer 与 Executor。
type Tool struct {
	spec       Spec
	kspec      kernel.ToolSpec
	pathParams []pathParam
	bodyKeys   map[string]bool
	disp       *Dispatcher
}

type pathParam struct {
	name string // Pattern 中的原名，如 changeRequestID
	arg  string // 参数名，如 change_request_id
	str  bool
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// Build 校验每个声明都绑定到真实存在的路由并构造工具；任何不一致都返回错误（启动即失败）。
func Build(specs []Spec, d BuildDeps) ([]*Tool, error) {
	seen := map[string]bool{}
	out := make([]*Tool, 0, len(specs))
	for _, s := range specs {
		if seen[s.Name] {
			return nil, fmt.Errorf("agent tool %s: duplicate name", s.Name)
		}
		seen[s.Name] = true
		perm, ok := d.RoutePermission(s.Method, s.Pattern)
		if !ok {
			return nil, fmt.Errorf("agent tool %s: route %s %s is not in adminRouteTable", s.Name, s.Method, s.Pattern)
		}
		t := &Tool{spec: s, disp: d.Dispatcher, bodyKeys: map[string]bool{}}
		props := map[string]any{}
		required := append([]string(nil), s.Required...)
		for _, m := range pathParamRe.FindAllStringSubmatch(s.Pattern, -1) {
			pp := pathParam{name: m[1], arg: snake(m[1]), str: m[1] == "requestID"}
			t.pathParams = append(t.pathParams, pp)
			typ := "integer"
			if pp.str {
				typ = "string"
			}
			props[pp.arg] = map[string]any{"type": typ, "description": "路径参数 " + m[1]}
			required = append(required, pp.arg)
		}
		for name, p := range s.Query {
			if _, dup := props[name]; dup {
				return nil, fmt.Errorf("agent tool %s: query param %s collides with a path param", s.Name, name)
			}
			props[name] = paramSchema(p)
		}
		if s.Body {
			bs, ok := d.BodySchema(s.Method, s.Pattern)
			if !ok {
				return nil, fmt.Errorf("agent tool %s: route %s %s has no request body schema", s.Name, s.Method, s.Pattern)
			}
			bp, _ := bs["properties"].(map[string]any)
			for name, sch := range bp {
				if s.BodyFields != nil && !slices.Contains(s.BodyFields, name) {
					continue
				}
				if _, dup := props[name]; dup {
					return nil, fmt.Errorf("agent tool %s: body field %s collides with a path/query param", s.Name, name)
				}
				props[name] = sch
				t.bodyKeys[name] = true
			}
			for _, f := range s.BodyFields {
				if !t.bodyKeys[f] {
					return nil, fmt.Errorf("agent tool %s: body field %s does not exist on the route", s.Name, f)
				}
			}
		}
		if s.Risk == kernel.RiskWrite {
			for k, v := range kernel.ProposalMetaSchema() {
				if _, dup := props[k]; dup {
					return nil, fmt.Errorf("agent tool %s: field %s collides with proposal metadata", s.Name, k)
				}
				props[k] = v
			}
			if s.Target.Type == "" {
				return nil, fmt.Errorf("agent tool %s: write tool needs a Target", s.Name)
			}
			if s.Before != "" {
				if _, ok := d.RoutePermission(http.MethodGet, s.Before); !ok {
					return nil, fmt.Errorf("agent tool %s: before route GET %s is not in adminRouteTable", s.Name, s.Before)
				}
			}
		}
		for _, r := range s.Required {
			if _, ok := props[r]; !ok {
				return nil, fmt.Errorf("agent tool %s: required param %s is not declared", s.Name, r)
			}
		}
		sort.Strings(required)
		params, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": slices.Compact(required)})
		t.kspec = kernel.ToolSpec{
			Name: s.Name, Description: s.Description, Parameters: params, Risk: s.Risk,
			Permission: string(perm), Source: s.Source, Trusted: s.Source == "",
		}
		out = append(out, t)
	}
	return out, nil
}

func paramSchema(p Param) map[string]any {
	m := map[string]any{"type": p.Type}
	if p.Description != "" {
		m["description"] = p.Description
	}
	if len(p.Enum) > 0 {
		m["enum"] = p.Enum
	}
	return m
}

// snake 把路径参数名转为 snake_case：changeRequestID → change_request_id。
func snake(s string) string {
	s = strings.ReplaceAll(s, "ID", "Id")
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Spec 返回发给模型的声明。
func (t *Tool) Spec() kernel.ToolSpec { return t.kspec }

// Route 返回绑定的方法与路由模式（测试用）。
func (t *Tool) Route() (string, string) { return t.spec.Method, t.spec.Pattern }

// Decl 返回原始声明（测试用）。
func (t *Tool) Decl() Spec { return t.spec }

// request 是由工具参数还原出的 HTTP 请求。
type request struct {
	path     string
	query    url.Values
	body     []byte
	pathArgs map[string]string
	bodyMap  map[string]any
}

func (t *Tool) build(args json.RawMessage) (*request, error) {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, errors.New("参数不是合法的 JSON 对象")
	}
	for _, r := range t.spec.Required {
		if v, ok := m[r]; !ok || v == nil || v == "" {
			return nil, fmt.Errorf("缺少必填参数 %s", r)
		}
	}
	req := &request{path: t.spec.Pattern, query: url.Values{}, pathArgs: map[string]string{}, bodyMap: map[string]any{}}
	for _, pp := range t.pathParams {
		v, ok := m[pp.arg]
		if !ok {
			return nil, fmt.Errorf("缺少路径参数 %s", pp.arg)
		}
		s := scalar(v)
		if !pp.str {
			if _, err := strconv.ParseInt(s, 10, 64); err != nil {
				return nil, fmt.Errorf("参数 %s 必须是整数", pp.arg)
			}
		}
		if s == "" {
			return nil, fmt.Errorf("参数 %s 不能为空", pp.arg)
		}
		req.pathArgs[pp.name] = s
		req.path = strings.Replace(req.path, "{"+pp.name+"}", url.PathEscape(s), 1)
	}
	for name := range t.spec.Query {
		if v, ok := m[name]; ok && v != nil {
			if s := scalar(v); s != "" {
				req.query.Set(name, s)
			}
		}
	}
	if t.spec.Body {
		for k := range t.bodyKeys {
			if v, ok := m[k]; ok {
				req.bodyMap[k] = v
			}
		}
		b, err := json.Marshal(req.bodyMap)
		if err != nil {
			return nil, err
		}
		req.body = b
	}
	// 未声明的参数直接忽略（模型偶尔会多给字段），不透传给接口。
	return req, nil
}

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (t *Tool) shape(raw []byte) (any, string) {
	if t.spec.Shape != nil {
		return t.spec.Shape(raw)
	}
	return DefaultShape(raw)
}

// Call 以发起者身份执行只读工具。
func (t *Tool) Call(ctx context.Context, env *kernel.Env, args json.RawMessage) (kernel.Result, error) {
	if t.spec.Risk != kernel.RiskRead {
		return kernel.Result{}, errors.New("写工具必须经过审批")
	}
	req, err := t.build(args)
	if err != nil {
		return kernel.Result{IsError: true, Content: map[string]any{"error": err.Error()}, Summary: "参数错误"}, nil
	}
	ref, _ := kernel.CallFrom(ctx)
	resp, err := t.disp.Do(ctx, env.Principal, ref, t.spec.Method, req.path, req.query, req.body, nil)
	if err != nil {
		return kernel.Result{}, err
	}
	return t.result(resp), nil
}

func (t *Tool) result(resp *Response) kernel.Result {
	if resp.Status >= 400 {
		c := errorContent(resp.Status, resp.Body)
		sum := fmt.Sprintf("HTTP %d", resp.Status)
		if code, _ := c["error_code"].(string); code != "" {
			sum += " " + code
		}
		return kernel.Result{HTTPStatus: resp.Status, IsError: true, Content: c, Summary: sum}
	}
	content, sum := t.shape(resp.Body)
	return kernel.Result{HTTPStatus: resp.Status, Content: content, Summary: sum}
}

// Propose 生成写操作提案：读取目标对象当前快照与 ETag，不执行写入。
func (t *Tool) Propose(ctx context.Context, env *kernel.Env, args json.RawMessage) (*kernel.Proposal, error) {
	req, err := t.build(args)
	if err != nil {
		return nil, err
	}
	p := &kernel.Proposal{TargetType: t.spec.Target.Type, After: req.bodyMap}
	if t.spec.Target.Param != "" {
		p.TargetID = req.pathArgs[t.spec.Target.Param]
	} else {
		var parts []string
		for _, k := range t.spec.Target.BodyKey {
			parts = append(parts, scalar(req.bodyMap[k]))
		}
		p.TargetID = strings.Join(parts, ":")
	}
	if p.TargetID == "" || p.TargetID == ":" {
		return nil, errors.New("无法确定操作对象")
	}
	if t.spec.Before != "" {
		path := t.spec.Before
		for name, v := range req.pathArgs {
			path = strings.Replace(path, "{"+name+"}", url.PathEscape(v), 1)
		}
		resp, err := t.disp.Do(ctx, env.Principal, kernel.CallRef{}, http.MethodGet, path, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.Status == http.StatusNotFound:
			return nil, fmt.Errorf("对象 %s #%s 不存在", p.TargetType, p.TargetID)
		case resp.Status < 300:
			before, _ := DefaultShape(resp.Body)
			p.Before = before
			p.ETag = resp.Header.Get("ETag")
		}
		// 无权读取快照（403）时不阻止提案：审批人会以自己的身份执行并再次校验。
	}
	if t.spec.Summarize != nil {
		p.Summary = t.spec.Summarize(withPath(req))
	} else {
		p.Summary = fmt.Sprintf("%s #%s", t.spec.Name, p.TargetID)
	}
	return p, nil
}

func withPath(req *request) map[string]any {
	m := map[string]any{}
	for k, v := range req.bodyMap {
		m[k] = v
	}
	for name, v := range req.pathArgs {
		m[snake(name)] = v
	}
	return m
}

// ExecOutcome 是审批后执行的结果分类。
type ExecOutcome string

const (
	ExecOK     ExecOutcome = "executed"
	ExecStale  ExecOutcome = "stale"  // 412 / 409：对象在审批期间已变化
	ExecFailed ExecOutcome = "failed" // 其他错误
)

// Execute 以审批人身份执行写工具（设计 §15.2 核心安全不变式）。args 是库中提案的参数（不含
// 提案元数据），Idempotency-Key 固定为 agent:{callID}，重复审批只执行一次。
func (t *Tool) Execute(ctx context.Context, approver *adminauth.Principal, ref kernel.CallRef, args json.RawMessage, etag string) (ExecOutcome, kernel.Result, error) {
	if t.spec.Risk != kernel.RiskWrite {
		return ExecFailed, kernel.Result{}, errors.New("not a write tool")
	}
	req, err := t.build(args)
	if err != nil {
		return ExecFailed, kernel.Result{IsError: true, Content: map[string]any{"error": err.Error()}, Summary: "参数错误"}, nil
	}
	hdr := http.Header{}
	if t.spec.Method == http.MethodPost {
		hdr.Set("Idempotency-Key", "agent:"+ref.ToolCallID)
	}
	if t.spec.IfMatch && etag != "" {
		hdr.Set("If-Match", etag)
	}
	resp, err := t.disp.Do(ctx, approver, ref, t.spec.Method, req.path, req.query, req.body, hdr)
	if err != nil {
		return ExecFailed, kernel.Result{}, err
	}
	res := t.result(resp)
	switch {
	case resp.Status < 300:
		return ExecOK, res, nil
	case resp.Status == http.StatusPreconditionFailed || resp.Status == http.StatusConflict:
		return ExecStale, res, nil
	default:
		return ExecFailed, res, nil
	}
}
