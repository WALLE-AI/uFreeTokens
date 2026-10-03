package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
)

// 成果物工具（《运营后台全局助手执行方案》P3）：
//   - render_chart：在对话里画一张图（只读）。只校验，不落库——前端按工具参数 + 数据集渲染；
//   - create_report：把文字结论、图表、表格、指标卡组装成报表落库（RiskArtifact：不改业务数据，直接执行）。
// 图表、表格、指标卡都只能引用本会话的数据集，列名必须存在，数值列必须是数值类型。

// ChartSpec 是一张图（或表格 / 指标卡）的声明。
type ChartSpec struct {
	DatasetID int64    `json:"dataset_id"`
	Type      string   `json:"type"`
	Title     string   `json:"title,omitempty"`
	X         string   `json:"x,omitempty"`
	Y         []string `json:"y,omitempty"`
	Series    string   `json:"series,omitempty"`
	Columns   []string `json:"columns,omitempty"`
}

var chartTypes = []string{"line", "bar", "stacked_bar", "pie", "table", "kpi"}

var numericTypes = []string{"integer", "number", "money", "percent", "pp", "ms"}

func chartSchema() map[string]any {
	return map[string]any{
		"type": "object", "required": []string{"dataset_id", "type"},
		"properties": map[string]any{
			"dataset_id": map[string]any{"type": "integer", "description": "query_analytics 返回的 dataset_id（必须是本会话的）"},
			"type":       map[string]any{"type": "string", "enum": chartTypes, "description": "line=趋势（x 为时间列）；bar=对比/排名；stacked_bar=构成随时间变化；pie=占比（≤8 个分类）；table=明细表；kpi=指标卡（取数据集合计与环比）"},
			"title":      map[string]any{"type": "string"},
			"x":          map[string]any{"type": "string", "description": "横轴/分类列名，如 bucket（时间）或 label（分组名）。line/bar/stacked_bar/pie 必填"},
			"y":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "数值列名。line/bar 可多列；stacked_bar、pie 恰好 1 列；kpi 为要展示的合计指标"},
			"series":     map[string]any{"type": "string", "description": "长表的分组列（如 label）：每个分组一条线/一组柱。分组 + 时间粒度的数据集画趋势时使用"},
			"columns":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "table 显示的列，空 = 全部"},
		},
	}
}

type datasetColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

// loadOwnDataset 读取本会话的数据集与列定义。
func loadOwnDataset(ctx context.Context, store Store, env *kernel.Env, id int64) (*pgstore.Dataset, map[string]datasetColumn, error) {
	if id <= 0 {
		return nil, nil, errors.New("dataset_id is required")
	}
	d, err := store.GetDataset(ctx, id)
	if errors.Is(err, pgstore.ErrNotFound) || (err == nil && d.SessionID != env.SessionID) {
		return nil, nil, fmt.Errorf("数据集 #%d 不存在或不属于本会话，请先用 query_analytics 查询", id)
	}
	if err != nil {
		return nil, nil, err
	}
	var cols []datasetColumn
	if err := json.Unmarshal(d.Columns, &cols); err != nil {
		return nil, nil, fmt.Errorf("数据集 #%d 的列定义无法解析", id)
	}
	byKey := make(map[string]datasetColumn, len(cols))
	for _, c := range cols {
		byKey[c.Key] = c
	}
	return d, byKey, nil
}

// validateChart 校验图表声明与数据集列是否匹配；返回的错误都是参数问题（原样回给模型重写）。
func validateChart(ctx context.Context, store Store, env *kernel.Env, c *ChartSpec) (*pgstore.Dataset, error) {
	if !slices.Contains(chartTypes, c.Type) {
		return nil, fmt.Errorf("type 必须是 %s 之一", strings.Join(chartTypes, " / "))
	}
	d, cols, err := loadOwnDataset(ctx, store, env, c.DatasetID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(cols))
	for k := range cols {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	need := func(name, what string) error {
		if _, ok := cols[name]; !ok {
			return fmt.Errorf("%s 列 %q 不在数据集 #%d 中（可用列：%s）", what, name, d.ID, strings.Join(keys, ", "))
		}
		return nil
	}
	numeric := func(name string) error {
		if err := need(name, "y"); err != nil {
			return err
		}
		if !slices.Contains(numericTypes, cols[name].Type) {
			return fmt.Errorf("y 列 %q 不是数值列（类型 %s）", name, cols[name].Type)
		}
		return nil
	}
	switch c.Type {
	case "table":
		for _, k := range c.Columns {
			if err := need(k, "columns"); err != nil {
				return nil, err
			}
		}
		return d, nil
	case "kpi":
		if len(c.Y) == 0 {
			return nil, errors.New("kpi 需要 y（要展示的合计指标列）")
		}
		for _, y := range c.Y {
			if err := numeric(y); err != nil {
				return nil, err
			}
		}
		return d, nil
	}
	if c.X == "" {
		return nil, fmt.Errorf("%s 需要 x（横轴/分类列）", c.Type)
	}
	if err := need(c.X, "x"); err != nil {
		return nil, err
	}
	if len(c.Y) == 0 {
		return nil, fmt.Errorf("%s 需要至少一个 y 数值列", c.Type)
	}
	if (c.Type == "pie" || c.Type == "stacked_bar") && len(c.Y) != 1 {
		return nil, fmt.Errorf("%s 只能有一个 y 列", c.Type)
	}
	if c.Type == "stacked_bar" && c.Series == "" {
		return nil, errors.New("stacked_bar 需要 series（堆叠的分组列，如 label）")
	}
	if c.Series != "" {
		if err := need(c.Series, "series"); err != nil {
			return nil, err
		}
		if len(c.Y) != 1 {
			return nil, errors.New("指定 series 时只能有一个 y 列")
		}
	}
	for _, y := range c.Y {
		if err := numeric(y); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// RenderChart 是 render_chart 工具。
type RenderChart struct {
	Store Store
}

func (t *RenderChart) Spec() kernel.ToolSpec {
	params, _ := json.Marshal(chartSchema())
	return kernel.ToolSpec{
		Name: "render_chart", Risk: kernel.RiskRead, Trusted: true, Parameters: params,
		Description: "在对话中渲染一张图表（折线、柱状、堆叠柱、饼图、明细表、指标卡），数据取自本会话的数据集，你不需要也不能自己提供数字。" +
			"趋势用 line（x=bucket），排名/对比用 bar（x=label），占比用 pie。",
	}
}

func (t *RenderChart) Call(ctx context.Context, env *kernel.Env, args json.RawMessage) (kernel.Result, error) {
	var c ChartSpec
	if err := json.Unmarshal(args, &c); err != nil {
		return errResult(http.StatusBadRequest, "arguments must match the chart schema"), nil
	}
	d, err := validateChart(ctx, t.Store, env, &c)
	if err != nil {
		return errResult(http.StatusBadRequest, err.Error()), nil
	}
	if c.Title == "" {
		c.Title = d.Title
	}
	return kernel.Result{
		HTTPStatus: http.StatusOK,
		Content:    map[string]any{"rendered": true, "chart": c, "dataset_title": d.Title, "row_count": d.RowCount},
		Summary:    fmt.Sprintf("图表：%s（数据集 #%d）", c.Title, d.ID),
	}, nil
}

// ReportStore 是报表的持久化（pgstore.Store 实现）。
type ReportStore interface {
	SaveReport(ctx context.Context, r *pgstore.Report) error
}

// ReportSection 是报表的一节：markdown 文字或一张图（含表格、指标卡）。
type ReportSection struct {
	Type  string     `json:"type"`
	Text  string     `json:"text,omitempty"`
	Chart *ChartSpec `json:"chart,omitempty"`
}

const (
	maxReportSections = 30
	maxReportText     = 30000
)

// CreateReport 是 create_report 工具。
type CreateReport struct {
	Store   Store
	Reports ReportStore
}

func (t *CreateReport) Spec() kernel.ToolSpec {
	params, _ := json.Marshal(map[string]any{
		"type": "object", "required": []string{"title", "sections"},
		"properties": map[string]any{
			"title":   map[string]any{"type": "string", "description": "报表标题，如「9 月第 4 周经营周报」"},
			"summary": map[string]any{"type": "string", "description": "一段话摘要（列表页展示）"},
			"visibility": map[string]any{"type": "string", "enum": []string{"private", "shared"},
				"description": "private=仅本人（默认）；shared=所有运营可见。后台作业生成的报表默认 shared"},
			"sections": map[string]any{"type": "array", "description": "按顺序排列的报表内容",
				"items": map[string]any{"type": "object", "required": []string{"type"}, "properties": map[string]any{
					"type":  map[string]any{"type": "string", "enum": []string{"markdown", "chart"}},
					"text":  map[string]any{"type": "string", "description": "type=markdown 时的正文（结论、解读、建议）"},
					"chart": chartSchema(),
				}}},
		},
	})
	return kernel.ToolSpec{
		Name: "create_report", Risk: kernel.RiskArtifact, Trusted: true, Parameters: params,
		Description: "把分析结果保存为报表（文字结论 + 图表 + 明细表 + 指标卡），用户可以在「报表」页签查看、分享与导出 Excel / Markdown / PDF。" +
			"图表与表格只能引用本会话 query_analytics 生成的数据集；文字中的数字必须与数据集一致。只产出报表，不修改任何业务数据。",
	}
}

func (t *CreateReport) Call(ctx context.Context, env *kernel.Env, args json.RawMessage) (kernel.Result, error) {
	var a struct {
		Title      string          `json:"title"`
		Summary    string          `json:"summary"`
		Visibility string          `json:"visibility"`
		Sections   []ReportSection `json:"sections"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult(http.StatusBadRequest, "arguments must match the report schema"), nil
	}
	a.Title = strings.TrimSpace(a.Title)
	if a.Title == "" || utf8.RuneCountInString(a.Title) > 120 {
		return errResult(http.StatusBadRequest, "title 必填且不超过 120 字"), nil
	}
	if utf8.RuneCountInString(a.Summary) > 1000 {
		return errResult(http.StatusBadRequest, "summary 不超过 1000 字"), nil
	}
	if len(a.Sections) == 0 || len(a.Sections) > maxReportSections {
		return errResult(http.StatusBadRequest, fmt.Sprintf("sections 需要 1–%d 节", maxReportSections)), nil
	}
	switch a.Visibility {
	case "":
		a.Visibility = "private"
		if env.Mode == kernel.ModeBatch {
			a.Visibility = "shared"
		}
	case "private", "shared":
	default:
		return errResult(http.StatusBadRequest, "visibility 必须是 private 或 shared"), nil
	}
	textLen := 0
	var datasetIDs []int64
	for i := range a.Sections {
		sec := &a.Sections[i]
		switch sec.Type {
		case "markdown":
			if strings.TrimSpace(sec.Text) == "" {
				return errResult(http.StatusBadRequest, fmt.Sprintf("sections[%d] 的 text 为空", i)), nil
			}
			sec.Chart = nil
			textLen += utf8.RuneCountInString(sec.Text)
		case "chart":
			if sec.Chart == nil {
				return errResult(http.StatusBadRequest, fmt.Sprintf("sections[%d] 缺少 chart", i)), nil
			}
			d, err := validateChart(ctx, t.Store, env, sec.Chart)
			if err != nil {
				return errResult(http.StatusBadRequest, fmt.Sprintf("sections[%d]：%s", i, err.Error())), nil
			}
			if sec.Chart.Title == "" {
				sec.Chart.Title = d.Title
			}
			sec.Text = ""
			if !slices.Contains(datasetIDs, d.ID) {
				datasetIDs = append(datasetIDs, d.ID)
			}
		default:
			return errResult(http.StatusBadRequest, fmt.Sprintf("sections[%d] 的 type 必须是 markdown 或 chart", i)), nil
		}
	}
	if textLen > maxReportText {
		return errResult(http.StatusBadRequest, fmt.Sprintf("报表正文不超过 %d 字", maxReportText)), nil
	}
	sections, _ := json.Marshal(a.Sections)
	ref, _ := kernel.CallFrom(ctx)
	r := &pgstore.Report{
		SessionID: env.SessionID, ToolCallID: ref.ToolCallID, OwnerAdminID: env.Principal.AdminID, JobID: env.JobID,
		Title: a.Title, Summary: strings.TrimSpace(a.Summary), Sections: sections, DatasetIDs: datasetIDs, Visibility: a.Visibility,
	}
	if err := t.Reports.SaveReport(ctx, r); err != nil {
		return kernel.Result{}, err
	}
	return kernel.Result{
		HTTPStatus: http.StatusCreated,
		Content:    map[string]any{"report_id": r.ID, "title": r.Title, "visibility": r.Visibility, "sections": len(a.Sections), "datasets": datasetIDs},
		Summary:    fmt.Sprintf("报表 #%d：%s", r.ID, r.Title),
	}, nil
}
