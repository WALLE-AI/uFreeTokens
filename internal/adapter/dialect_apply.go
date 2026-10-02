package adapter

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"net/http"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
)

// 方言在请求侧的应用（多供应商接口统一技术实施方案 §3、§15.2）。没有方言的账号
// （Dialect == nil）走到这里全部是 no-op，行为与引入方言之前完全一致。

// dialectEndpoint 把逻辑端点路径映射成方言里的端点名。
func dialectEndpoint(endpoint string) string {
	switch endpoint {
	case EndpointChat:
		return dialect.EndpointChat
	case EndpointEmbeddings:
		return dialect.EndpointEmbeddings
	case EndpointRerank:
		return dialect.EndpointRerank
	case EndpointImages:
		return dialect.EndpointImages
	case EndpointSpeech:
		return dialect.EndpointSpeech
	case EndpointTranscriptions:
		return dialect.EndpointTranscriptions
	}
	return ""
}

func accountDialect(t Target) *dialect.Dialect {
	if t.Account == nil {
		return nil
	}
	return t.Account.Dialect
}

// upstreamURL 计算某端点的上游地址（方言 url / path 覆盖）。
func upstreamURL(t Target, endpoint string) string {
	return accountDialect(t).ResolveURL(t.Account.BaseURL, dialectEndpoint(endpoint), endpoint)
}

// setAuthAndTransportHeaders 设置鉴权头与方言附加头。keyless 的供应商不发 Authorization。
func setAuthAndTransportHeaders(req *http.Request, t Target) {
	d := accountDialect(t)
	if d == nil || !d.Transport.Keyless || (t.Key != nil && t.Key.Secret != "" && t.Key.Secret != "no-key") {
		if t.Key != nil {
			req.Header.Set("Authorization", "Bearer "+t.Key.Secret)
		}
	}
	if d != nil {
		for k, v := range d.Transport.ExtraHeaders {
			req.Header.Set(k, v)
		}
	}
}

// requestTransforms 是请求侧的具名变换（方言 transforms 引用的名字）。响应侧的
// 变换见 ApplyResponseTransforms。
var requestTransforms = map[string]func(payload map[string]any, t Target){
	"voice_prefix_upstream_model": func(p map[string]any, t Target) {
		if v, ok := p["voice"].(string); ok && v != "" && !strings.Contains(v, ":") {
			p["voice"] = t.Channel.UpstreamModel + ":" + v
		}
	},
	// 火山方舟 Seedream 不认 n：多图用 sequential_image_generation。
	"ark_n_to_sequential": func(p map[string]any, _ Target) {
		n, _ := p["n"].(float64)
		delete(p, "n")
		if n > 1 {
			p["sequential_image_generation"] = "auto"
			p["sequential_image_generation_options"] = map[string]any{"max_images": n}
		}
	},
	// 上游只支持 float 向量：请求改成 float，响应阶段再由 ApplyResponseTransforms 编码。
	"embedding_base64_encode": func(p map[string]any, _ Target) {
		if p["encoding_format"] == "base64" {
			p["encoding_format"] = "float"
		}
	},
}

// applyRequestDialect 依次应用 defaults → rename → drop → force → 映射表 → 具名变换，
// 对话端点再应用 chat 方言。调用方负责在之后叠加渠道 param_overrides。
func applyRequestDialect(p map[string]any, t Target, endpoint string) {
	d := accountDialect(t)
	if d == nil {
		return
	}
	name := dialectEndpoint(endpoint)
	if ep := d.Endpoint(name); ep != nil {
		for k, v := range ep.Defaults {
			if _, ok := p[k]; !ok {
				p[k] = v
			}
		}
		for from, to := range ep.Rename {
			if v, ok := p[from]; ok {
				delete(p, from)
				p[to] = v
			}
		}
		for _, k := range ep.Drop {
			delete(p, k)
		}
		for k, v := range ep.Force {
			p[k] = v
		}
		if v, ok := p["voice"].(string); ok {
			if m, ok := ep.VoiceMap[v]; ok {
				p["voice"] = m
			}
		}
		if v, ok := p["size"].(string); ok {
			if m, ok := ep.SizeMap[v]; ok {
				p["size"] = m
			}
		}
		for _, tr := range ep.Transforms {
			if fn, ok := requestTransforms[tr]; ok {
				fn(p, t)
			}
		}
	}
	if name == dialect.EndpointChat {
		applyChatDialect(p, d.Chat)
	}
}

// standardMessageKeys 是 strict_messages 时保留的消息字段（OpenAI 标准）。
var standardMessageKeys = map[string]bool{"role": true, "content": true, "name": true, "tool_calls": true, "tool_call_id": true}

func applyChatDialect(p map[string]any, c dialect.Chat) {
	for k, v := range c.ThinkingDefault {
		if _, ok := p[k]; !ok {
			p[k] = v
		}
	}
	if c.ForceSingleToolCall {
		if tools, _ := p["tools"].([]any); len(tools) > 0 {
			p["parallel_tool_calls"] = false
		}
	}
	// 复制一份消息切片再改：p 是请求体的浅拷贝，消息切片与客户端原始请求共享，
	// 重试时会再次用到原始请求。
	msgs, _ := p["messages"].([]any)
	msgs = append([]any(nil), msgs...)
	if c.StrictMessages {
		p["messages"] = msgs
		for i, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			clean := make(map[string]any, len(msg))
			for k, v := range msg {
				if standardMessageKeys[k] {
					clean[k] = v
				}
			}
			msgs[i] = clean
		}
	}
	if c.SingleSystemMessage && len(msgs) > 0 {
		var systems []string
		rest := make([]any, 0, len(msgs))
		for _, m := range msgs {
			msg, _ := m.(map[string]any)
			if msg != nil && msg["role"] == "system" {
				if s, ok := msg["content"].(string); ok && s != "" {
					systems = append(systems, s)
				}
				continue
			}
			rest = append(rest, m)
		}
		if len(systems) > 0 {
			msgs = append([]any{map[string]any{"role": "system", "content": strings.Join(systems, "\n\n")}}, rest...)
		}
		p["messages"] = msgs
	}
	if c.MinMaxTokens > 0 {
		for _, k := range []string{"max_tokens", "max_completion_tokens"} {
			if v, ok := p[k].(float64); ok && int(v) < c.MinMaxTokens {
				p[k] = float64(c.MinMaxTokens)
			}
		}
	}
}

// ApplyResponseTransforms 应用响应侧的具名变换（目前只有 embedding_base64_encode）。
// clientReq 是客户端的原始请求体。
func ApplyResponseTransforms(t Target, endpoint string, clientReq, resp map[string]any) {
	d := accountDialect(t)
	if d == nil {
		return
	}
	name := dialectEndpoint(endpoint)
	if d.HasTransform(name, "embedding_base64_encode") && clientReq["encoding_format"] == "base64" {
		encodeEmbeddingsBase64(resp)
	}
}

// encodeEmbeddingsBase64 把 data[].embedding 的 float 数组编码成 little-endian float32
// 的 base64（与 OpenAI encoding_format=base64 的格式一致）。
func encodeEmbeddingsBase64(resp map[string]any) {
	data, _ := resp["data"].([]any)
	for _, item := range data {
		m, _ := item.(map[string]any)
		vec, ok := m["embedding"].([]any)
		if !ok {
			continue
		}
		buf := make([]byte, 4*len(vec))
		for i, v := range vec {
			f, _ := v.(float64)
			binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(float32(f)))
		}
		m["embedding"] = base64.StdEncoding.EncodeToString(buf)
	}
}
