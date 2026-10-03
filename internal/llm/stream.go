package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// 多轮 + 工具调用 + 流式（运营后台智能体用，见《运营后台 Agent 模块（Harness 智能体）技术架构设计方案》§3.6）。
// 与 ChatJSON 相互独立：ChatJSON 的行为不变。

// Message 是一条对话消息（OpenAI 兼容格式）。Role 为 system / user / assistant / tool。
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall // 仅 assistant
	ToolCallID string     // 仅 tool
}

// ToolCall 是模型发起的一次函数调用；Arguments 是 JSON 文本（模型生成，未必合法）。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef 描述一个可供模型调用的函数；Parameters 是 JSON Schema。
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Usage 是一次调用的 Token 用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ChatOptions 是 ChatTools 的可选参数；零值表示用客户端默认值。
type ChatOptions struct {
	Model     string // 空 = Client.Model
	MaxTokens int    // 0 = 不传
	// NoStream 为 true 时用非流式请求（上游不支持 tools + stream 组合时的降级）。
	NoStream bool
}

// StreamEvent 是流式过程中的增量事件；一次只有一个字段非空。
type StreamEvent struct {
	TextDelta string
	// ReasoningDelta 是推理模型的思考增量（reasoning_content / reasoning 字段或 <think>…</think> 段）。
	ReasoningDelta string
}

// Completion 是一轮调用的完整结果。
type Completion struct {
	Content      string
	Reasoning    string // 推理模型的思考内容（不含在 Content 里）
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
}

// HTTPError 是上游返回非 200 时的错误，Status 供调用方区分可重试/不可重试。
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("llm status %d: %s", e.Status, e.Body) }

// ErrStreamInterrupted 表示流在收到结束标记前断开。
var ErrStreamInterrupted = errors.New("llm: stream interrupted")

type wireToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

func toWire(msgs []Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		wm := wireMessage{Role: m.Role, Content: &content, ToolCallID: m.ToolCallID}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && content == "" {
			wm.Content = nil // 部分上游要求只有 tool_calls 时 content 为 null
		}
		for _, tc := range m.ToolCalls {
			w := wireToolCall{ID: tc.ID, Type: "function"}
			w.Function.Name = tc.Name
			w.Function.Arguments = tc.Arguments
			if w.Function.Arguments == "" {
				w.Function.Arguments = "{}"
			}
			wm.ToolCalls = append(wm.ToolCalls, w)
		}
		out = append(out, wm)
	}
	return out
}

// ChatTools 发一轮带工具定义的对话。流式模式下每段文本增量通过 onEvent 回调（可为 nil）；
// 推理模型的 reasoning_content 与 <think>…</think> 段与正文分开，通过 ReasoningDelta 回调并放进 Completion.Reasoning。
// 返回完整的文本、思考、工具调用与用量。
func (c *Client) ChatTools(ctx context.Context, msgs []Message, tools []ToolDef, opt ChatOptions, onEvent func(StreamEvent)) (*Completion, error) {
	model := opt.Model
	if model == "" {
		model = c.Model
	}
	payload := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages":    toWire(msgs),
	}
	if len(tools) > 0 {
		defs := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			defs = append(defs, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": params,
			}})
		}
		payload["tools"] = defs
	}
	if opt.MaxTokens > 0 {
		payload["max_tokens"] = opt.MaxTokens
	}
	if !opt.NoStream {
		payload["stream"] = true
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if !opt.NoStream {
		req.Header.Set("Accept", "text/event-stream")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	if !opt.NoStream && hc.Timeout > 0 {
		// 流式响应的总时长由 ctx 控制；http.Client.Timeout 会把读 body 也算进去，长输出会被误杀。
		cp := *hc
		cp.Timeout = 0
		hc = &cp
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, &HTTPError{Status: resp.StatusCode, Body: Truncate(string(data), 300)}
	}
	ct := resp.Header.Get("Content-Type")
	if opt.NoStream || !strings.Contains(ct, "text/event-stream") {
		// 上游忽略了 stream:true 直接返回 JSON 时也能处理。
		return parseNonStream(resp.Body, onEvent)
	}
	return parseStream(resp.Body, onEvent)
}

func parseNonStream(r io.Reader, onEvent func(StreamEvent)) (*Completion, error) {
	data, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, err
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content          string         `json:"content"`
				ReasoningContent string         `json:"reasoning_content"`
				Reasoning        string         `json:"reasoning"`
				ToolCalls        []wireToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &cr); err != nil || len(cr.Choices) == 0 {
		return nil, fmt.Errorf("llm: unexpected response: %s", Truncate(string(data), 300))
	}
	ch := cr.Choices[0]
	f := &thinkFilter{}
	text, inline := f.feed(ch.Message.Content)
	tailText, tailThink := f.flush()
	out := &Completion{
		FinishReason: ch.FinishReason,
		Content:      text + tailText,
		Reasoning:    ch.Message.ReasoningContent + ch.Message.Reasoning + inline + tailThink,
	}
	for _, tc := range ch.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	if cr.Usage != nil {
		out.Usage = *cr.Usage
	}
	if onEvent != nil && out.Reasoning != "" {
		onEvent(StreamEvent{ReasoningDelta: out.Reasoning})
	}
	if onEvent != nil && out.Content != "" {
		onEvent(StreamEvent{TextDelta: out.Content})
	}
	return out, nil
}

type partialCall struct {
	id, name string
	args     strings.Builder
}

func parseStream(r io.Reader, onEvent func(StreamEvent)) (*Completion, error) {
	out := &Completion{}
	calls := map[int]*partialCall{}
	var text, reasoning strings.Builder
	think := &thinkFilter{}
	emitReasoning := func(s string) {
		if s == "" {
			return
		}
		reasoning.WriteString(s)
		if onEvent != nil {
			onEvent(StreamEvent{ReasoningDelta: s})
		}
	}
	emit := func(s, thought string) {
		emitReasoning(thought)
		if s == "" {
			return
		}
		text.WriteString(s)
		if onEvent != nil {
			onEvent(StreamEvent{TextDelta: s})
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	done := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // 空行、event:、注释行（: ping）
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string         `json:"content"`
					ReasoningContent string         `json:"reasoning_content"`
					Reasoning        string         `json:"reasoning"`
					ToolCalls        []wireToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 容忍个别无法解析的行
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("llm: stream error: %s", Truncate(chunk.Error.Message, 300))
		}
		if chunk.Usage != nil {
			out.Usage = *chunk.Usage
		}
		for _, ch := range chunk.Choices {
			emitReasoning(ch.Delta.ReasoningContent + ch.Delta.Reasoning)
			emit(think.feed(ch.Delta.Content))
			for i, tc := range ch.Delta.ToolCalls {
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				pc := calls[idx]
				if pc == nil {
					pc = &partialCall{}
					calls[idx] = pc
				}
				if tc.ID != "" {
					pc.id = tc.ID
				}
				if tc.Function.Name != "" {
					pc.name += tc.Function.Name
				}
				pc.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				out.FinishReason = *ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStreamInterrupted, err)
	}
	if !done && out.FinishReason == "" {
		return nil, ErrStreamInterrupted
	}
	emit(think.flush())
	out.Content = text.String()
	out.Reasoning = reasoning.String()
	idxs := make([]int, 0, len(calls))
	for i := range calls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		pc := calls[i]
		if pc.name == "" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: pc.id, Name: pc.name, Arguments: pc.args.String()})
	}
	return out, nil
}

// StripThink 去掉推理模型输出中的 <think>…</think> 段。
func StripThink(s string) string {
	f := &thinkFilter{}
	text, _ := f.feed(s)
	tail, _ := f.flush()
	return text + tail
}

// thinkFilter 在流式文本里把 <think>…</think> 段与正文分开；标签可能被切在两个增量之间，
// 所以末尾可能是标签前缀的几个字符先扣住，等下一段再判断。
type thinkFilter struct {
	inThink bool
	pending string
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// feed 返回这段增量里的正文与思考内容。
func (f *thinkFilter) feed(s string) (string, string) {
	s = f.pending + s
	f.pending = ""
	var out, thought strings.Builder
	for s != "" {
		tag := thinkOpen
		dst := &out
		if f.inThink {
			tag = thinkClose
			dst = &thought
		}
		if i := strings.Index(s, tag); i >= 0 {
			dst.WriteString(s[:i])
			f.inThink = !f.inThink
			s = s[i+len(tag):]
			continue
		}
		// 末尾是否是标签的前缀
		keep := 0
		for n := min(len(tag)-1, len(s)); n > 0; n-- {
			if strings.HasPrefix(tag, s[len(s)-n:]) {
				keep = n
				break
			}
		}
		dst.WriteString(s[:len(s)-keep])
		f.pending = s[len(s)-keep:]
		break
	}
	return out.String(), thought.String()
}

// flush 返回扣住的尾部（未闭合的 <think> 段算作思考）。
func (f *thinkFilter) flush() (string, string) {
	p := f.pending
	f.pending = ""
	if f.inThink {
		return "", p
	}
	return p, ""
}
