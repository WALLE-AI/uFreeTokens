package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// 公开排行榜的请求链路采集（docs/基准测试与排行榜数据服务技术方案.md §2.1、§4，阶段 3）：
// 应用归因、图片输入计数、工具调用计数、流式生成耗时。这些只写进 request_logs，
// 不影响路由与计费；解析失败一律按"没有"处理，绝不让采集逻辑拒绝或拖慢请求。

// maxAppNameRunes 是 X-Title 保留的最大字符数，防止被用来塞垃圾内容。
const maxAppNameRunes = 64

// appAttribution 读取调用方主动声明的应用（OpenRouter 惯例的请求头）：X-Title 是应用名，
// HTTP-Referer 是应用地址。应用名去掉控制字符、合并空白、截断到 64 个字符；地址只保留
// scheme://host（小写，去掉路径、查询串、用户信息与默认端口以外的部分），避免记录个人信息。
// 只给了 HTTP-Referer、没有 X-Title 的请求不算声明了应用（应用榜只展示主动声明的应用）。
func appAttribution(r *http.Request) (name, appURL string) {
	name = normalizeAppName(r.Header.Get("X-Title"))
	if name == "" {
		return "", ""
	}
	return name, normalizeAppURL(r.Header.Get("HTTP-Referer"))
}

func normalizeAppName(raw string) string {
	raw = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && !unicode.IsSpace(c) {
			return -1
		}
		return c
	}, strings.ToValidUTF8(raw, ""))
	name := []rune(strings.Join(strings.Fields(raw), " "))
	if len(name) > maxAppNameRunes {
		name = name[:maxAppNameRunes]
	}
	return strings.TrimSpace(string(name))
}

func normalizeAppURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if p := u.Port(); p != "" && !(u.Scheme == "http" && p == "80") && !(u.Scheme == "https" && p == "443") {
		host += ":" + p
	}
	return u.Scheme + "://" + host
}

// countImageInputs 数请求消息里 OpenAI 形状的图片内容块（{"type":"image_url",...}）。
func countImageInputs(reqMap map[string]any) int {
	msgs, _ := reqMap["messages"].([]any)
	n := 0
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		parts, _ := msg["content"].([]any)
		for _, p := range parts {
			if part, _ := p.(map[string]any); part != nil && part["type"] == "image_url" {
				n++
			}
		}
	}
	return n
}

// countToolCalls 数非流式响应里各候选 message.tool_calls 的条数之和。
func countToolCalls(resp map[string]any) int {
	choices, _ := resp["choices"].([]any)
	n := 0
	for _, c := range choices {
		choice, _ := c.(map[string]any)
		msg, _ := choice["message"].(map[string]any)
		calls, _ := msg["tool_calls"].([]any)
		n += len(calls)
	}
	return n
}

// streamStats 在转发流式响应时顺带统计生成耗时与工具调用，供 request_logs 使用。
type streamStats struct {
	first, last time.Time
	toolCalls   map[[2]int]bool // (choice index, tool_call index) 去重：同一个调用分多个 chunk 下发
}

var toolCallsMarker = []byte(`"tool_calls"`)

// observe 记录一个上游 chunk 的到达时间；只有含 "tool_calls" 的 chunk 才解析 JSON，
// 普通文本 chunk 不额外付出解析开销。
func (st *streamStats) observe(chunk []byte, at time.Time) {
	if st.first.IsZero() {
		st.first = at
	}
	st.last = at
	if !bytes.Contains(chunk, toolCallsMarker) {
		return
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(chunk), []byte("data:")))
	var m struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				ToolCalls []struct {
					Index int `json:"index"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &m) != nil {
		return
	}
	for _, c := range m.Choices {
		for _, tc := range c.Delta.ToolCalls {
			if st.toolCalls == nil {
				st.toolCalls = map[[2]int]bool{}
			}
			st.toolCalls[[2]int{c.Index, tc.Index}] = true
		}
	}
}

// genMillis 是第一个到最后一个 chunk 的间隔；少于两个 chunk 时无法衡量生成速度，返回 nil。
func (st *streamStats) genMillis() *int64 {
	if st.first.IsZero() || !st.last.After(st.first) {
		return nil
	}
	ms := st.last.Sub(st.first).Milliseconds()
	if ms <= 0 {
		return nil
	}
	return &ms
}
