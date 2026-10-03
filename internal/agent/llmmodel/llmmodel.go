// Package llmmodel 把 internal/llm 的 OpenAI 兼容客户端适配为 kernel.Model。
package llmmodel

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/llm"
)

// Model 包装 llm.Client。上游不支持 tools + stream 组合（400/422）时自动降级为非流式并记住。
type Model struct {
	Client    *llm.Client
	MaxTokens int // 单轮输出上限；0 = 不传
	noStream  atomic.Bool
}

func New(c *llm.Client) *Model { return &Model{Client: c, MaxTokens: 4096} }

func (m *Model) Stream(ctx context.Context, req kernel.Request, onDelta func(kernel.Delta)) (*kernel.Turn, error) {
	msgs := make([]llm.Message, 0, len(req.Messages))
	for _, km := range req.Messages {
		lm := llm.Message{Role: km.Role, Content: km.Content, ToolCallID: km.ToolCallID}
		switch km.Role {
		case kernel.RoleSummary:
			lm.Role, lm.Content = "user", "【早期对话摘要】\n"+km.Content
		case kernel.RoleReport:
			lm.Role = "assistant"
		}
		for _, tc := range km.ToolCalls {
			lm.ToolCalls = append(lm.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		msgs = append(msgs, lm)
	}
	tools := make([]llm.ToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, llm.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	var onEvent func(llm.StreamEvent)
	if onDelta != nil {
		onEvent = func(e llm.StreamEvent) { onDelta(kernel.Delta{Text: e.TextDelta, Reasoning: e.ReasoningDelta}) }
	}
	opt := llm.ChatOptions{Model: req.Model, MaxTokens: req.MaxTokens, NoStream: m.noStream.Load()}
	if opt.MaxTokens == 0 {
		opt.MaxTokens = m.MaxTokens
	}
	out, err := m.Client.ChatTools(ctx, msgs, tools, opt, onEvent)
	var he *llm.HTTPError
	if err != nil && !opt.NoStream && errors.As(err, &he) && (he.Status == http.StatusBadRequest || he.Status == http.StatusUnprocessableEntity) {
		m.noStream.Store(true)
		opt.NoStream = true
		out, err = m.Client.ChatTools(ctx, msgs, tools, opt, onEvent)
	}
	if err != nil {
		return nil, err
	}
	turn := &kernel.Turn{Text: out.Content, Reasoning: out.Reasoning, Usage: kernel.Usage{In: out.Usage.PromptTokens, Out: out.Usage.CompletionTokens}}
	for _, tc := range out.ToolCalls {
		turn.ToolCalls = append(turn.ToolCalls, kernel.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return turn, nil
}
