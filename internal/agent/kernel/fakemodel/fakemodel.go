// Package fakemodel 是脚本化的假模型：按顺序返回预先写好的文本 / 工具调用 / 用量，
// 可注入错误与延迟。Harness 的单测与集成测试都用它驱动，CI 不调用真实模型。
package fakemodel

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
)

// Step 是一轮脚本。Err 非空时这一轮返回错误；Delay 在返回前等待（期间响应 ctx 取消）。
type Step struct {
	Text      string
	Calls     []kernel.ToolCall
	Usage     kernel.Usage
	Err       error
	Delay     time.Duration
	TextParts []string // 非空时按片段流式回调（模拟增量）；否则整段回调 Text
	Reasoning string   // 思考内容，在正文之前整段回调
}

// Text 构造一轮纯文本回复。
func Text(s string) Step { return Step{Text: s, Usage: kernel.Usage{In: 100, Out: 10}} }

// Call 构造一轮单个工具调用。args 可以是任意可 JSON 编码的值或 JSON 字符串。
func Call(name string, args any) Step {
	return Calls(kernel.ToolCall{Name: name, Arguments: encode(args)})
}

// Calls 构造一轮多个工具调用。
func Calls(calls ...kernel.ToolCall) Step {
	return Step{Calls: calls, Usage: kernel.Usage{In: 100, Out: 10}}
}

// TC 是 kernel.ToolCall 的便捷构造。
func TC(name string, args any) kernel.ToolCall {
	return kernel.ToolCall{Name: name, Arguments: encode(args)}
}

func encode(args any) string {
	if s, ok := args.(string); ok {
		return s
	}
	b, _ := json.Marshal(args)
	return string(b)
}

// ErrScriptExhausted 表示脚本已用完（测试写少了步骤）。
var ErrScriptExhausted = errors.New("fakemodel: script exhausted")

// Model 是脚本化模型。并发安全；Requests 记录收到的每一轮请求。
type Model struct {
	mu       sync.Mutex
	steps    []Step
	Requests []kernel.Request
}

// New 用给定脚本构造模型。
func New(steps ...Step) *Model { return &Model{steps: steps} }

// Push 追加脚本步骤（审批后恢复运行前补充脚本）。
func (m *Model) Push(steps ...Step) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, steps...)
}

// Remaining 返回尚未消费的步骤数。
func (m *Model) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps)
}

func (m *Model) Stream(ctx context.Context, req kernel.Request, onDelta func(kernel.Delta)) (*kernel.Turn, error) {
	m.mu.Lock()
	m.Requests = append(m.Requests, req)
	if len(m.steps) == 0 {
		m.mu.Unlock()
		return nil, ErrScriptExhausted
	}
	st := m.steps[0]
	m.steps = m.steps[1:]
	m.mu.Unlock()

	if st.Delay > 0 {
		select {
		case <-time.After(st.Delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if st.Err != nil {
		return nil, st.Err
	}
	if onDelta != nil {
		if st.Reasoning != "" {
			onDelta(kernel.Delta{Reasoning: st.Reasoning})
		}
		parts := st.TextParts
		if len(parts) == 0 && st.Text != "" {
			parts = []string{st.Text}
		}
		for _, p := range parts {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			onDelta(kernel.Delta{Text: p})
		}
	}
	text := st.Text
	if text == "" && len(st.TextParts) > 0 {
		for _, p := range st.TextParts {
			text += p
		}
	}
	calls := append([]kernel.ToolCall(nil), st.Calls...)
	return &kernel.Turn{Text: text, Reasoning: st.Reasoning, ToolCalls: calls, Usage: st.Usage}, nil
}
