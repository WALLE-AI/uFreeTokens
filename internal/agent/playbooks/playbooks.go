// Package playbooks 是运营智能体的剧本（设计 §3.4、§14.2）：每个剧本是一个 SKILL.md 风格的
// Markdown 文件，前置元数据声明名称、标题、允许的工具与预算，正文是目标、步骤与判定标准，
// 运行时注入系统提示。剧本随二进制发布（go:embed），修改剧本需附评测报告（实施方案 §10）。
package playbooks

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.md
var files embed.FS

// Playbook 是一个已解析的剧本。
type Playbook struct {
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	AllowedTools []string `json:"allowed_tools"`
	// TargetType 是剧本处理的对象类型（页面入口与行内建议用），如 price_change_request。
	TargetType   string `json:"target_type"`
	MaxTurns     int    `json:"max_turns"`
	MaxToolCalls int    `json:"max_tool_calls"`
	// Starter 是从页面入口启动时默认发送的第一条消息。
	Starter string `json:"starter"`
	Body    string `json:"-"`
}

// Parse 解析一个剧本文件：--- 包围的前置元数据（key: value，列表用逗号分隔）+ 正文。
func Parse(name string, data []byte) (*Playbook, error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, fmt.Errorf("playbook %s: missing front matter", name)
	}
	head, body, ok := strings.Cut(s[4:], "\n---\n")
	if !ok {
		return nil, fmt.Errorf("playbook %s: unterminated front matter", name)
	}
	p := &Playbook{Body: strings.TrimSpace(body)}
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("playbook %s: bad front matter line %q", name, line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "name":
			p.Name = v
		case "title":
			p.Title = v
		case "description":
			p.Description = v
		case "target_type":
			p.TargetType = v
		case "starter":
			p.Starter = v
		case "allowed_tools":
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					p.AllowedTools = append(p.AllowedTools, t)
				}
			}
		case "max_turns", "max_tool_calls":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("playbook %s: %s must be an integer", name, k)
			}
			if k == "max_turns" {
				p.MaxTurns = n
			} else {
				p.MaxToolCalls = n
			}
		default:
			return nil, fmt.Errorf("playbook %s: unknown front matter key %q", name, k)
		}
	}
	if p.Name == "" || p.Title == "" || len(p.AllowedTools) == 0 {
		return nil, fmt.Errorf("playbook %s: name, title and allowed_tools are required", name)
	}
	if p.Name+".md" != name {
		return nil, fmt.Errorf("playbook %s: name %q must match the file name", name, p.Name)
	}
	return p, nil
}

// All 返回全部内置剧本（按名称排序）；解析失败直接 panic——剧本随代码发布，坏剧本是构建错误，
// 由 TestPlaybooks_Parse 在 CI 拦截。
func All() []*Playbook {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}
	var out []*Playbook
	for _, e := range entries {
		data, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		p, err := Parse(e.Name(), data)
		if err != nil {
			panic(err)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get 按名称取剧本。
func Get(name string) (*Playbook, bool) {
	for _, p := range All() {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}
