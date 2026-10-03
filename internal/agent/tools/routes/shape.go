package routes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// 结果裁剪与脱敏（设计 §3.5、实施方案 M2-B05）：工具结果在交给模型、写入 agent_tool_calls 之前
// 去掉密钥/凭据类字段、对邮箱与手机号打码；外部文本字段（优惠文案、模型介绍）标注来源由
// ToolSpec.Source 负责。

// sensitiveKeys 中的字段整个剔除（按小写子串匹配字段名）。
var sensitiveKeys = []string{"secret", "password", "api_key", "apikey", "token", "key_hash", "ciphertext", "pepper", "totp", "user_agent", "authorization"}

// maskKeys 中的字段保留但打码。
var maskKeys = []string{"email", "phone", "mobile", "ip"}

var (
	emailRe = regexp.MustCompile(`([A-Za-z0-9._%+-])[A-Za-z0-9._%+-]*@([A-Za-z0-9.-]+\.[A-Za-z]{2,})`)
	phoneRe = regexp.MustCompile(`(^|[^0-9])(1[3-9][0-9])[0-9]{4}([0-9]{4})($|[^0-9])`)
)

// Redact 递归脱敏一个 JSON 值。
func Redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			lk := strings.ToLower(k)
			if matchAny(lk, sensitiveKeys) && !strings.HasSuffix(lk, "_count") && !strings.HasSuffix(lk, "_last4") && !strings.Contains(lk, "tokens") {
				continue
			}
			if matchKey(lk, maskKeys) {
				if s, ok := val.(string); ok {
					out[k] = maskString(s)
					continue
				}
			}
			out[k] = Redact(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = Redact(val)
		}
		return out
	case string:
		return redactText(t)
	}
	return v
}

func matchAny(k string, list []string) bool {
	for _, s := range list {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// matchKey 匹配整词（ip 不能命中 "description"/"zip"）。
func matchKey(k string, list []string) bool {
	for _, s := range list {
		if k == s || strings.HasSuffix(k, "_"+s) || strings.HasPrefix(k, s+"_") {
			return true
		}
	}
	return false
}

func maskString(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(s, "@") {
		return emailRe.ReplaceAllString(s, "$1***@$2")
	}
	r := []rune(s)
	if len(r) <= 4 {
		return "***"
	}
	return string(r[:2]) + "***" + string(r[len(r)-2:])
}

func redactText(s string) string {
	if strings.Contains(s, "@") {
		s = emailRe.ReplaceAllString(s, "$1***@$2")
	}
	return phoneRe.ReplaceAllString(s, "$1$2****$3$4")
}

// DefaultShape 是默认的结果裁剪：解析 JSON → 脱敏；摘要按列表/详情给出一句话。
func DefaultShape(raw []byte) (any, string) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		s := string(raw)
		if len(s) > 2000 {
			s = s[:2000]
		}
		return map[string]any{"raw": s}, "非 JSON 响应"
	}
	v = Redact(v)
	return v, summarize(v)
}

func summarize(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		if arr, ok := v.([]any); ok {
			return fmt.Sprintf("共 %d 条", len(arr))
		}
		return "已读取"
	}
	if data, ok := m["data"].([]any); ok {
		if total, ok := m["total"].(float64); ok {
			return fmt.Sprintf("共 %d 条，本页 %d 条", int(total), len(data))
		}
		return fmt.Sprintf("返回 %d 条", len(data))
	}
	if results, ok := m["results"].([]any); ok {
		return fmt.Sprintf("返回 %d 项结果", len(results))
	}
	for _, k := range []string{"name", "upstream_model", "title", "status"} {
		if s, ok := m[k].(string); ok && s != "" {
			return "已读取：" + truncRunes(s, 40)
		}
	}
	return "已读取"
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// errorContent 把业务接口的错误响应整理成给模型看的结构。
func errorContent(status int, raw []byte) map[string]any {
	out := map[string]any{"http_status": status}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
		out["error_code"], out["error"] = env.Error.Code, env.Error.Message
	} else {
		out["error"] = truncRunes(string(raw), 300)
	}
	return out
}
