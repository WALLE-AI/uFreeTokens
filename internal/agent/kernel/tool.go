package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
)

// Mode 是运行模式（设计 §15.2）。
type Mode string

const (
	// ModeInteractive：写工具生成提案后暂停，等当场审批后继续循环。
	ModeInteractive Mode = "interactive"
	// ModeBatch：写工具只落提案、不暂停，继续处理下一条；审批时只执行该工具，不再回到模型。
	ModeBatch Mode = "batch"
)

// Env 是一次运行的环境：发起者身份、会话/运行标识、模式与本次运行抓取过的页面。
type Env struct {
	Principal *adminauth.Principal
	SessionID int64
	RunID     string
	Mode      Mode
	Playbook  string
	JobID     *int64
	// Pages 记录本次运行中 fetch_page 抓到的页面正文，供证据校验（§13.4）。
	Pages *PageCache
}

// Result 是工具执行结果。Content 会被 JSON 编码后作为 tool 消息交给模型。
type Result struct {
	HTTPStatus int
	Content    any
	Summary    string // 一句话摘要（时间线 / 工具卡上展示）
	IsError    bool
}

// Tool 是一个可被模型调用的工具。
type Tool interface {
	Spec() ToolSpec
	Call(ctx context.Context, env *Env, args json.RawMessage) (Result, error)
}

// Proposal 是写工具在执行前生成的操作提案：目标对象、当前快照与预期结果。
type Proposal struct {
	TargetType string
	TargetID   string
	Summary    string
	Before     any
	After      any
	ETag       string
}

// Proposer 由写工具实现：只读取目标对象、生成提案，绝不执行写入。
type Proposer interface {
	Propose(ctx context.Context, env *Env, args json.RawMessage) (*Proposal, error)
}

// Decision 是策略对一次工具调用的裁决。
type Decision int

const (
	Allow   Decision = iota // 直接执行（只读）
	Propose                 // 生成提案，等人工审批
	Deny                    // 不允许（无权限 / 被禁用）
)

// Policy 决定一次工具调用如何处理。
type Policy interface {
	Decide(env *Env, spec ToolSpec) Decision
}

// DefaultPolicy：write → Propose；read 且调用者有绑定路由的权限 → Allow，否则 Deny。
// 批处理模式下写工具不要求发起者有写权限：只读服务主体（agent-bot）只能产生提案，
// 执行永远以审批人身份、经审批人的权限校验（设计 §15.2）。交互模式下发起者缺写权限同样 Deny，
// 避免产生自己无法处理、只能等别人审批的提案。
type DefaultPolicy struct{}

func (DefaultPolicy) Decide(env *Env, spec ToolSpec) Decision {
	permitted := spec.Permission == "" || (env.Principal != nil && env.Principal.Can(adminauth.Permission(spec.Permission)))
	switch {
	case spec.Risk == RiskWrite && (permitted || env.Mode == ModeBatch):
		return Propose
	case permitted && spec.Risk != RiskWrite:
		return Allow
	}
	return Deny
}

// ProposalMeta 是所有写工具参数里额外允许的三个字段：模型给出的理由、置信度与证据。
// 它们在执行前被剥离（业务接口拒绝未知字段）。
type ProposalMeta struct {
	Rationale  string     `json:"rationale,omitempty"`
	Confidence *float64   `json:"confidence,omitempty"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

// Evidence 是一条外部证据：url 必须是本次运行抓取过的页面，quote 必须逐字出现在正文中。
type Evidence struct {
	URL   string `json:"url"`
	Quote string `json:"quote"`
}

// SplitProposalMeta 从写工具参数里取出 ProposalMeta，返回剩余参数。
func SplitProposalMeta(args json.RawMessage) (ProposalMeta, json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return ProposalMeta{}, nil, err
	}
	var meta ProposalMeta
	for _, k := range []string{"rationale", "confidence", "evidence"} {
		if v, ok := m[k]; ok {
			sub, _ := json.Marshal(map[string]json.RawMessage{k: v})
			_ = json.Unmarshal(sub, &meta)
			delete(m, k)
		}
	}
	if meta.Confidence != nil && (*meta.Confidence < 0 || *meta.Confidence > 1) {
		meta.Confidence = nil
	}
	rest, err := json.Marshal(m)
	return meta, rest, err
}

// ProposalMetaSchema 是追加到每个写工具参数 Schema 里的三个字段。
func ProposalMetaSchema() map[string]any {
	return map[string]any{
		"rationale":  map[string]any{"type": "string", "description": "提出该操作的理由（会展示给审批人）"},
		"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "对该建议的置信度 0~1"},
		"evidence": map[string]any{"type": "array", "description": "来自外部网页的依据：url 必须先用 fetch_page 抓取，quote 必须逐字摘自页面正文",
			"items": map[string]any{"type": "object", "required": []string{"url", "quote"}, "properties": map[string]any{
				"url": map[string]any{"type": "string"}, "quote": map[string]any{"type": "string"}}}},
	}
}

// HashArgs 是参数的规范化哈希（审批时校验提案参数未被篡改）。
func HashArgs(args json.RawMessage) string {
	var v any
	if err := json.Unmarshal(args, &v); err == nil {
		if b, err := json.Marshal(v); err == nil { // map 键有序，得到规范形式
			args = b
		}
	}
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:])
}

// PageCache 是一次运行内抓取过的页面（url → 正文）。并发安全。
type PageCache struct {
	mu    sync.Mutex
	pages map[string]string
}

func NewPageCache() *PageCache { return &PageCache{pages: map[string]string{}} }

func (c *PageCache) Put(url, text string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pages[url] = text
}

func (c *PageCache) Get(url string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.pages[url]
	return t, ok
}

// CallRef 标识正在执行的一次工具调用，放进请求 ctx 供审计关联（admin_audit_logs.agent_*）。
type CallRef struct {
	SessionID  int64
	RunID      string
	ToolCallID string
}

type callCtxKey struct{}

// WithCall 把工具调用标识放进 ctx。
func WithCall(ctx context.Context, ref CallRef) context.Context {
	return context.WithValue(ctx, callCtxKey{}, ref)
}

// CallFrom 取出 ctx 中的工具调用标识；不是经由智能体的请求返回 ok=false。
func CallFrom(ctx context.Context) (CallRef, bool) {
	ref, ok := ctx.Value(callCtxKey{}).(CallRef)
	return ref, ok
}
