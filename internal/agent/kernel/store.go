package kernel

import (
	"context"
	"encoding/json"
	"errors"
)

// 工具调用状态（agent_tool_calls.status）。
const (
	CallRunning         = "running"
	CallDone            = "done"
	CallError           = "error"
	CallDenied          = "denied"
	CallPendingApproval = "pending_approval"
	CallApproved        = "approved"
	CallRejected        = "rejected"
	CallStale           = "stale"
	CallExecuted        = "executed"
	CallFailed          = "failed"
	CallSuperseded      = "superseded"
)

// ToolCallRecord 对应一行 agent_tool_calls。
type ToolCallRecord struct {
	ID           string          `json:"id"`
	SessionID    int64           `json:"session_id"`
	RunID        string          `json:"run_id"`
	Tool         string          `json:"tool"`
	Risk         Risk            `json:"risk"`
	Args         json.RawMessage `json:"args"`
	ArgsHash     string          `json:"-"`
	Status       string          `json:"status"`
	Summary      string          `json:"summary"`
	RequiredPerm string          `json:"required_perm"`
	ETag         string          `json:"-"`
	Before       json.RawMessage `json:"before,omitempty"`
	After        json.RawMessage `json:"after,omitempty"`
	HTTPStatus   int             `json:"http_status,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	DurationMS   int             `json:"duration_ms,omitempty"`
}

// ProposalRecord 对应一行 agent_proposals。
type ProposalRecord struct {
	ID           int64           `json:"id"`
	ToolCallID   string          `json:"tool_call_id"`
	SessionID    int64           `json:"session_id"`
	JobID        *int64          `json:"job_id"`
	Playbook     string          `json:"playbook"`
	Tool         string          `json:"tool"`
	TargetType   string          `json:"target_type"`
	TargetID     string          `json:"target_id"`
	Summary      string          `json:"summary"`
	Rationale    string          `json:"rationale"`
	Evidence     json.RawMessage `json:"evidence"`
	Confidence   *float64        `json:"confidence"`
	RequiredPerm string          `json:"required_perm"`
}

// ErrDuplicateProposal 表示同一目标对象已有同类待处理提案（agent_proposals 部分唯一索引）。
var ErrDuplicateProposal = errors.New("agent: a pending proposal for this target already exists")

// Store 是内核需要的持久化能力；pgstore 实现它，测试用内存实现。
type Store interface {
	// AppendMessage 追加一条消息并回填 m.Seq。
	AppendMessage(ctx context.Context, sessionID int64, m *Message) error
	// SaveToolCall 插入一次工具调用记录。
	SaveToolCall(ctx context.Context, rec *ToolCallRecord) error
	// FinishToolCall 更新工具调用的最终状态与结果。
	FinishToolCall(ctx context.Context, rec *ToolCallRecord) error
	// SaveProposal 在同一事务里插入 pending_approval 的工具调用与提案，回填 p.ID。
	SaveProposal(ctx context.Context, rec *ToolCallRecord, p *ProposalRecord) error
	// AddUsage 累加会话的 Token 用量与轮数。
	AddUsage(ctx context.Context, sessionID int64, u Usage, turns int) error
	// CancelRequested 返回会话是否已被请求取消。
	CancelRequested(ctx context.Context, sessionID int64) (bool, error)
}

// Event 是运行过程中推给前端（SSE）/日志的事件，见设计 §5.1。
type Event struct {
	Type string
	Data any
}

// Sink 接收事件。
type Sink interface {
	Emit(Event)
}

// SinkFunc 把函数适配为 Sink。
type SinkFunc func(Event)

func (f SinkFunc) Emit(e Event) { f(e) }

// NopSink 丢弃所有事件（批处理运行）。
var NopSink Sink = SinkFunc(func(Event) {})

// Hooks 是可选的扩展点（借鉴 pi 的 transformContext 与 dsh 的插件化）。
type Hooks struct {
	// TransformContext 在每轮发给模型前改写消息序列（压缩、注入），不影响已持久化的消息。
	TransformContext func(msgs []Message) []Message
	// BeforePropose 在提案入库前校验参数（如证据校验）；返回错误则提案被退回给模型重写。
	BeforePropose func(ctx context.Context, env *Env, spec ToolSpec, meta ProposalMeta, args json.RawMessage) error
	// AfterToolCall 在每次工具调用结束后调用（指标、日志）。
	AfterToolCall func(ctx context.Context, env *Env, rec *ToolCallRecord)
}
