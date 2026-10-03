package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/pgstore"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/tools/analytics"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 助手报表（《运营后台全局助手执行方案》P3）：create_report 生成，Dock「报表」页签查看。
//   - 可见性：本人；visibility=shared 时所有 agent:use 管理员；有 audit:read 的管理员可看全部；
//   - 修改 / 删除：本人；后台作业生成的报表（owner 是 agent-bot）由 agent:admin 管理；
//   - 导出：?format=md（Markdown）/ xlsx（摘要 + 每个数据集一张工作表）。PDF 由前端打印样式生成。

type agentReportDetail struct {
	Report   pgstore.Report    `json:"report"`
	Datasets []pgstore.Dataset `json:"datasets"`
	CanEdit  bool              `json:"can_edit"`
}

type agentUpdateReportRequest struct {
	Title      *string `json:"title,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
}

func canViewReport(p *adminauth.Principal, r *pgstore.Report) bool {
	return r.OwnerAdminID == p.AdminID || r.Visibility == "shared" || p.Can(adminauth.PermAuditRead)
}

func canEditReport(p *adminauth.Principal, r *pgstore.Report) bool {
	return r.OwnerAdminID == p.AdminID || (r.JobID != nil && p.Can(adminauth.PermAgentAdmin))
}

// loadReport 读取报表并校验可见性（不暴露他人私有报表是否存在）。
func (h *adminHandlers) loadReport(w http.ResponseWriter, r *http.Request) (*pgstore.Report, bool) {
	if !h.requireAgent(w, r) {
		return nil, false
	}
	id, ok := pathID(w, r, "reportID", "report")
	if !ok {
		return nil, false
	}
	rep, err := h.agent.Store.GetReport(r.Context(), id)
	if errors.Is(err, pgstore.ErrNotFound) || (err == nil && !canViewReport(actor(r), rep)) {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Report not found.")
		return nil, false
	}
	if err != nil {
		writeAgentError(w, r, h, err)
		return nil, false
	}
	return rep, true
}

// listAgentReports 是 GET /agent/reports?scope=mine|all&q=&before=&limit=。
// 默认返回我的与共享的报表；scope=mine 只看我的；scope=all 需要 audit:read。
func (h *adminHandlers) listAgentReports(w http.ResponseWriter, r *http.Request) {
	if !h.requireAgent(w, r) {
		return
	}
	p := actor(r)
	q := &queryParser{r: r}
	in := pgstore.ListReportsInput{ViewerID: p.AdminID, Q: q.str("q"), Before: q.str("before"), Limit: q.int("limit")}
	scope := q.enum("scope", "mine", "all")
	if !q.ok(w) {
		return
	}
	switch scope {
	case "mine":
		in.Mine = true
	case "all":
		if !p.Can(adminauth.PermAuditRead) {
			forbid(w, r, adminauth.PermAuditRead)
			return
		}
		in.All = true
	}
	list, next, err := h.agent.Store.ListReports(r.Context(), in)
	if err != nil {
		if err.Error() == "invalid cursor" {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'before' cursor")
			return
		}
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, cursorPage[pgstore.Report]{Data: list, NextCursor: next})
}

// getAgentReport 是 GET /agent/reports/{reportID}：报表 + 引用的数据集。
func (h *adminHandlers) getAgentReport(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.loadReport(w, r)
	if !ok {
		return
	}
	ds, err := h.agent.Store.GetDatasets(r.Context(), rep.DatasetIDs)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, agentReportDetail{Report: *rep, Datasets: ds, CanEdit: canEditReport(actor(r), rep)})
}

// updateAgentReport 是 PATCH /agent/reports/{reportID}：改标题、分享 / 取消分享。
func (h *adminHandlers) updateAgentReport(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.loadReport(w, r)
	if !ok {
		return
	}
	if !canEditReport(actor(r), rep) {
		httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", "Only the report owner can change it.")
		return
	}
	var in agentUpdateReportRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" || len([]rune(t)) > 120 {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'title' must be 1-120 characters")
			return
		}
		in.Title = &t
	}
	if in.Visibility != nil && *in.Visibility != "private" && *in.Visibility != "shared" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'visibility' must be private or shared")
		return
	}
	if err := h.agent.Store.UpdateReport(r.Context(), rep.ID, in.Title, in.Visibility); err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	updated, err := h.agent.Store.GetReport(r.Context(), rep.ID)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updated)
}

// deleteAgentReport 是 DELETE /agent/reports/{reportID}。
func (h *adminHandlers) deleteAgentReport(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.loadReport(w, r)
	if !ok {
		return
	}
	if !canEditReport(actor(r), rep) {
		httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", "Only the report owner can delete it.")
		return
	}
	if err := h.agent.Store.DeleteReport(r.Context(), rep.ID); err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// exportAgentReport 是 GET /agent/reports/{reportID}/export?format=md|xlsx。
func (h *adminHandlers) exportAgentReport(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.loadReport(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "xlsx"
	}
	if format != "md" && format != "xlsx" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'format' must be md or xlsx")
		return
	}
	list, err := h.agent.Store.GetDatasets(r.Context(), rep.DatasetIDs)
	if err != nil {
		writeAgentError(w, r, h, err)
		return
	}
	doc, err := newReportDoc(rep, list)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	if format == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%d.md"`, rep.ID))
		_, _ = w.Write([]byte(doc.markdown()))
		return
	}
	buf, err := doc.xlsx()
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%d.xlsx"`, rep.ID))
	_, _ = w.Write(buf.Bytes())
}

// ---------- 导出 ----------

type reportDataset struct {
	meta     pgstore.Dataset
	columns  []admin.AnalyticsColumn
	byKey    map[string]admin.AnalyticsColumn
	rows     []map[string]any
	totals   map[string]any
	previous map[string]any
}

type reportDoc struct {
	rep      *pgstore.Report
	sections []analytics.ReportSection
	datasets map[int64]*reportDataset
	order    []int64
}

func newReportDoc(rep *pgstore.Report, list []pgstore.Dataset) (*reportDoc, error) {
	doc := &reportDoc{rep: rep, datasets: map[int64]*reportDataset{}}
	if err := json.Unmarshal(rep.Sections, &doc.sections); err != nil {
		return nil, fmt.Errorf("decode report sections: %w", err)
	}
	for _, d := range list {
		rd := &reportDataset{meta: d, byKey: map[string]admin.AnalyticsColumn{}}
		if err := json.Unmarshal(d.Columns, &rd.columns); err != nil {
			return nil, fmt.Errorf("decode dataset columns: %w", err)
		}
		for _, c := range rd.columns {
			rd.byKey[c.Key] = c
		}
		if err := json.Unmarshal(d.Rows, &rd.rows); err != nil {
			return nil, fmt.Errorf("decode dataset rows: %w", err)
		}
		_ = json.Unmarshal(d.Totals, &rd.totals)
		_ = json.Unmarshal(d.Previous, &rd.previous)
		doc.datasets[d.ID] = rd
		doc.order = append(doc.order, d.ID)
	}
	return doc, nil
}

// chartColumns 是一张图在表格形式下要展示的列。
func (d *reportDataset) chartColumns(c *analytics.ChartSpec) []admin.AnalyticsColumn {
	var keys []string
	switch c.Type {
	case "table":
		keys = c.Columns
	case "kpi":
		keys = c.Y
	default:
		keys = append([]string{c.X}, c.Y...)
		if c.Series != "" {
			keys = append([]string{c.X, c.Series}, c.Y...)
		}
	}
	if len(keys) == 0 {
		return d.columns
	}
	out := make([]admin.AnalyticsColumn, 0, len(keys))
	for _, k := range keys {
		if col, ok := d.byKey[k]; ok {
			out = append(out, col)
		}
	}
	return out
}

// formatValue 把单元格格式化为人类可读文本（Markdown 导出用）。
func formatValue(v any, typ string) string {
	f, isNum := v.(float64)
	if v == nil {
		return "—"
	}
	if !isNum {
		return fmt.Sprint(v)
	}
	switch typ {
	case "money":
		return "¥" + groupThousands(strconv.FormatFloat(f, 'f', 2, 64))
	case "percent":
		return strconv.FormatFloat(f*100, 'f', 2, 64) + "%"
	case "pp":
		s := strconv.FormatFloat(f*100, 'f', 2, 64) + "pp"
		if f > 0 {
			s = "+" + s
		}
		return s
	case "ms":
		return groupThousands(strconv.FormatFloat(f, 'f', 0, 64)) + " ms"
	case "integer":
		return groupThousands(strconv.FormatFloat(f, 'f', 0, 64))
	}
	if f == math.Trunc(f) {
		return groupThousands(strconv.FormatFloat(f, 'f', 0, 64))
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func groupThousands(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := sign + b.String()
	if hasFrac {
		out += "." + frac
	}
	return out
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

func (doc *reportDoc) markdown() string {
	var b strings.Builder
	rep := doc.rep
	fmt.Fprintf(&b, "# %s\n\n", rep.Title)
	fmt.Fprintf(&b, "_生成：%s · %s_\n\n", rep.CreatedAt.Format("2006-01-02 15:04"), rep.OwnerName)
	if rep.Summary != "" {
		fmt.Fprintf(&b, "> %s\n\n", strings.ReplaceAll(rep.Summary, "\n", "\n> "))
	}
	for _, sec := range doc.sections {
		if sec.Type == "markdown" {
			b.WriteString(strings.TrimSpace(sec.Text) + "\n\n")
			continue
		}
		c := sec.Chart
		if c == nil {
			continue
		}
		d := doc.datasets[c.DatasetID]
		fmt.Fprintf(&b, "### %s\n\n", c.Title)
		if d == nil {
			fmt.Fprintf(&b, "（数据集 #%d 已不存在）\n\n", c.DatasetID)
			continue
		}
		if c.Type == "kpi" {
			for _, col := range d.chartColumns(c) {
				line := fmt.Sprintf("- **%s**：%s", col.Label, formatValue(num(d.totals[col.Key]), col.Type))
				if d.previous != nil {
					line += fmt.Sprintf("（上期 %s）", formatValue(num(d.previous[col.Key]), col.Type))
				}
				b.WriteString(line + "\n")
			}
			b.WriteString("\n")
			continue
		}
		cols := d.chartColumns(c)
		header := make([]string, len(cols))
		sep := make([]string, len(cols))
		for i, col := range cols {
			header[i] = mdEscape(col.Label)
			sep[i] = "---"
		}
		b.WriteString("| " + strings.Join(header, " | ") + " |\n| " + strings.Join(sep, " | ") + " |\n")
		for _, row := range d.rows {
			cells := make([]string, len(cols))
			for i, col := range cols {
				cells[i] = mdEscape(formatValue(num(row[col.Key]), col.Type))
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
		fmt.Fprintf(&b, "\n_数据来源：数据集 #%d（%s）_\n\n", d.meta.ID, d.meta.Title)
	}
	return b.String()
}

// num 把 JSON 数字统一成 float64（json.Unmarshal 到 any 时本来就是 float64）。
func num(v any) any {
	switch t := v.(type) {
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return v
}

func (doc *reportDoc) xlsx() (*bytes.Buffer, error) {
	f := excelize.NewFile()
	defer f.Close()
	const summary = "报表"
	if err := f.SetSheetName("Sheet1", summary); err != nil {
		return nil, err
	}
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	title, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	wrap, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	numFmt := map[string]int{}
	for typ, code := range map[string]string{"money": "#,##0.00", "percent": "0.00%", "pp": "+0.00%;-0.00%;0.00%", "integer": "#,##0", "ms": "#,##0", "number": "#,##0.####"} {
		code := code
		id, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &code})
		numFmt[typ] = id
	}
	_ = f.SetColWidth(summary, "A", "A", 100)
	row := 1
	put := func(text string, style int) {
		cell, _ := excelize.CoordinatesToCellName(1, row)
		_ = f.SetCellValue(summary, cell, text)
		if style != 0 {
			_ = f.SetCellStyle(summary, cell, cell, style)
		}
		row++
	}
	put(doc.rep.Title, title)
	put(fmt.Sprintf("生成：%s · %s", doc.rep.CreatedAt.Format("2006-01-02 15:04"), doc.rep.OwnerName), 0)
	if doc.rep.Summary != "" {
		put(doc.rep.Summary, wrap)
	}
	row++
	sheetName := func(id int64) string { return fmt.Sprintf("数据集 %d", id) }
	for _, sec := range doc.sections {
		if sec.Type == "markdown" {
			put(strings.TrimSpace(sec.Text), wrap)
			continue
		}
		if sec.Chart != nil {
			put(fmt.Sprintf("【%s】见工作表「%s」", sec.Chart.Title, sheetName(sec.Chart.DatasetID)), bold)
		}
	}
	for _, id := range doc.order {
		d := doc.datasets[id]
		name := sheetName(id)
		if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		_ = f.SetCellValue(name, "A1", d.meta.Title)
		_ = f.SetCellStyle(name, "A1", "A1", title)
		for i, col := range d.columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, 2)
			_ = f.SetCellValue(name, cell, col.Label)
			_ = f.SetCellStyle(name, cell, cell, bold)
			colName, _ := excelize.ColumnNumberToName(i + 1)
			_ = f.SetColWidth(name, colName, colName, 16)
		}
		for r, rowVals := range d.rows {
			for i, col := range d.columns {
				cell, _ := excelize.CoordinatesToCellName(i+1, r+3)
				v := num(rowVals[col.Key])
				if v == nil {
					continue
				}
				_ = f.SetCellValue(name, cell, v)
				if st, ok := numFmt[col.Type]; ok {
					if _, isNum := v.(float64); isNum {
						_ = f.SetCellStyle(name, cell, cell, st)
					}
				}
			}
		}
	}
	return f.WriteToBuffer()
}
