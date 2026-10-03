// Package llm 是后台辅助任务（优惠文案抽取、模型介绍生成）共用的 OpenAI 兼容
// Chat Completions 客户端。只做一件事：发一轮 system + user 消息、要求 JSON 输出、
// 取回文本内容；提示词与输出解析由调用方负责。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
)

// envName 是合法的环境变量名（大写字母、数字、下划线）。
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Client 是 OpenAI 兼容接口的最小客户端。
type Client struct {
	BaseURL, APIKey, Model string
	HTTP                   *http.Client
}

// FromConfig 按共享配置 datasync.*（config.DataSyncConfig）构造客户端，密钥从
// llm_api_key_env 指向的环境变量读取。未配置完整时返回 nil 与缺了哪几项（供启动日志提示）。
// worker 与 cmd/admin 都用它，保证两边读的是同一份配置。
func FromConfig(c config.DataSyncConfig) (*Client, []string) {
	var missing []string
	key := ""
	switch {
	case c.LLMAPIKeyEnv == "":
		missing = append(missing, "datasync.llm_api_key_env")
	case !envName.MatchString(c.LLMAPIKeyEnv):
		// 常见误填：把密钥本身写进了 llm_api_key_env。不能把这个值写进日志。
		missing = append(missing, "datasync.llm_api_key_env must be an environment variable name (e.g. UFT_DATASYNC_LLM_API_KEY), not the key itself")
	default:
		if key = os.Getenv(c.LLMAPIKeyEnv); key == "" {
			missing = append(missing, "env "+c.LLMAPIKeyEnv)
		}
	}
	if c.LLMBaseURL == "" {
		missing = append(missing, "datasync.llm_base_url (UFT_DATASYNC_LLM_BASE_URL)")
	}
	if c.LLMModel == "" {
		missing = append(missing, "datasync.llm_model (UFT_DATASYNC_LLM_MODEL)")
	}
	if len(missing) > 0 {
		return nil, missing
	}
	return &Client{BaseURL: strings.TrimRight(c.LLMBaseURL, "/"), APIKey: key, Model: c.LLMModel, HTTP: &http.Client{Timeout: 2 * time.Minute}}, nil
}

// ChatJSON 发一轮对话（temperature 0、response_format=json_object），返回模型输出的文本。
// 不少 OpenAI 兼容端点（包括本网关：没有 json_schema 能力的渠道会返回 503
// no_available_channel）不支持 JSON 模式，被拒（400/422/503）时去掉 response_format 重试一次；
// 提示词本身要求只输出 JSON，解析端用 StripCodeFence 容忍多余包装。
func (c *Client) ChatJSON(ctx context.Context, system, user string) (string, error) {
	content, status, err := c.chat(ctx, system, user, true)
	if err != nil && (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity || status == http.StatusServiceUnavailable) {
		content, _, err = c.chat(ctx, system, user, false)
	}
	return content, err
}

// chat 发一次请求；返回的 status 是上游 HTTP 状态码（网络错误时为 0）。
func (c *Client) chat(ctx context.Context, system, user string, jsonMode bool) (string, int, error) {
	payload := map[string]any{
		"model":       c.Model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if jsonMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("llm status %d: %s", resp.StatusCode, Truncate(string(data), 300))
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &cr); err != nil || len(cr.Choices) == 0 {
		return "", resp.StatusCode, fmt.Errorf("llm: unexpected response: %s", Truncate(string(data), 300))
	}
	return cr.Choices[0].Message.Content, resp.StatusCode, nil
}

// StripCodeFence 从模型输出里取出 JSON 对象：去掉推理模型的 <think>…</think> 段、
// ```json 代码块，以及对象前后的多余文字（不在 JSON 模式下时常见）。
func StripCodeFence(content string) string {
	s := strings.TrimSpace(content)
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		s = strings.TrimSpace(s[i+len("</think>"):])
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	return strings.TrimSpace(s)
}

// Truncate 按字节截断（用于错误信息里的响应片段）。
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
