// Package kernel 是运营智能体（Harness）的内核：与 HTTP、数据库无关的接口与默认 ReAct 循环，
// 见《运营后台 Agent 模块（Harness 智能体）技术架构设计方案》§15.1。
//
// 内核只定义接口（Model / Tool / Policy / Store / Sink / Hooks），实现全部可替换：
// 模型由 llmmodel 包装 internal/llm，工具由 tools/routes 绑定现有管理路由，存储由 pgstore 落 PG。
package kernel

import (
	"context"
	"encoding/json"
	"time"
)

// 消息角色。summary 是上下文压缩生成的摘要，发给模型时作为 user 消息；report 是批处理运行报告。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSummary   = "summary"
	RoleReport    = "report"
)

// Message 是一条对话消息。
type Message struct {
	Seq        int        `json:"seq"`
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Compacted  bool       `json:"compacted,omitempty"`
	// Reasoning 是 assistant 这一轮的思考内容：只用于展示，不再发回给模型。
	Reasoning string `json:"reasoning,omitempty"`
	// CreatedAt 由存储层回填（读取时），用于前端计算运行耗时。
	CreatedAt time.Time `json:"created_at"`
}

// ToolCall 是模型发起的一次工具调用；Arguments 是模型生成的 JSON 文本（未必合法）。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage 是 Token 用量。
type Usage struct {
	In  int `json:"tokens_in"`
	Out int `json:"tokens_out"`
}

// Request 是发给模型的一轮请求。
type Request struct {
	Model     string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
}

// Turn 是模型一轮的输出。
type Turn struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCall
	Usage     Usage
}

// Delta 是一次流式增量；Text 与 Reasoning 一次只有一个非空。
type Delta struct {
	Text      string
	Reasoning string
}

// Model 是 LLM 的抽象。onDelta 收到流式正文 / 思考增量（可能为 nil）。
type Model interface {
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Turn, error)
}

// ToolSpec 是发给模型的工具声明 + 内核需要的元数据。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Risk        Risk            `json:"risk"`
	// Permission 是绑定路由要求的权限点，用于按 Principal 过滤工具集与审批时校验审批人。
	Permission string `json:"permission"`
	// Source 是结果的来源标注（注入防护用），如 "upstream_offer"；空 = 内部数据。
	Source string `json:"source,omitempty"`
	// Trusted 为 false 时结果包含外部文本，按不可信数据包裹。
	Trusted bool `json:"trusted"`
}

// Risk 是工具的风险级别。
type Risk string

const (
	RiskRead  Risk = "read"
	RiskWrite Risk = "write"
	// RiskArtifact：只产出助手自己的成果物（报表），不改业务数据——直接执行、不走审批，但会落库。
	RiskArtifact Risk = "artifact"
)
