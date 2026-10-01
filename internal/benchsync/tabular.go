package benchsync

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
	"go.yaml.in/yaml/v3"
)

// tabular 抓取器的配置（price_sources.config，fetcher=tabular）。一个来源可以产出多个榜单
// （boards），每个榜单对应一个 benchmarks 行（按 key 关联 external_key）。
//
//	{"format": "parquet|csv|json|yaml|zip_csv",   // 来源级默认，board 可覆盖
//	 "alias_namespace": "lmarena",                  // 模型名映射的命名空间
//	 "rows_path": "data",                           // json/yaml：行数组所在的点分路径
//	 "auth_header": "x-api-key", "auth_header_env": "UFT_DATASYNC_AA_API_KEY",  // 鉴权头，值取自 worker 环境变量
//	 "boards": [{
//	   "key": "lmarena:text:overall",               // 全局唯一，写进 benchmarks.external_key
//	   "url": "...",                                // 为空用来源 url（同一 url 只下载一次）
//	   "file": "gpqa_diamond.csv",                  // zip_csv：压缩包里的文件
//	   "filter": {"category": "overall"},           // 只保留这些列等于给定值的行
//	   "label": "model_name", "score": "rating",    // 列名，json 可用点分路径
//	   "scale": 100,                                // 分数乘数（0~1 的准确率转百分比）
//	   "cost_usd": "Cost per task",                 // 可选：单题成本（美元）
//	   "duration_seconds": "Time per case (seconds)",  // 可选：单题耗时（秒）
//	   "run_at": "leaderboard_publish_date",        // 可选：评测日期列（取最大值）
//	   "extra": {"rank": "rank"},                   // 额外字段 -> benchmark_results.extra
//	   "score_key": "arena_text",                   // 可选：发布时投影进 virtual_model_metadata.scores
//	   "min_rows": 10,                              // 可选：少于这么多行判为数据异常（默认 5）
//	   "benchmark": {"slug": ..., "name": ..., "category": ..., "metric_name": ..., "metric_unit": ...,
//	                 "higher_is_better": true, "description": ..., "source_url": ..., "sort_order": 0}
//	 }]}

type tabularConfig struct {
	Format         string        `json:"format"`
	AliasNamespace string        `json:"alias_namespace"`
	RowsPath       string        `json:"rows_path"`
	AuthHeader     string        `json:"auth_header"`
	AuthHeaderEnv  string        `json:"auth_header_env"`
	Boards         []boardConfig `json:"boards"`
}

type boardConfig struct {
	Key             string            `json:"key"`
	URL             string            `json:"url"`
	File            string            `json:"file"`
	Format          string            `json:"format"`
	RowsPath        string            `json:"rows_path"`
	Filter          map[string]string `json:"filter"`
	Label           string            `json:"label"`
	Score           string            `json:"score"`
	Scale           float64           `json:"scale"`
	CostUSD         string            `json:"cost_usd"`
	DurationSeconds string            `json:"duration_seconds"`
	RunAt           string            `json:"run_at"`
	Extra           map[string]string `json:"extra"`
	ScoreKey        string            `json:"score_key"`
	MinRows         int               `json:"min_rows"`
	Benchmark       benchmarkDef      `json:"benchmark"`
}

type benchmarkDef struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	MetricName     string `json:"metric_name"`
	MetricUnit     string `json:"metric_unit"`
	HigherIsBetter *bool  `json:"higher_is_better"`
	SourceURL      string `json:"source_url"`
	SortOrder      int    `json:"sort_order"`
}

func (b boardConfig) higherIsBetter() bool {
	return b.Benchmark.HigherIsBetter == nil || *b.Benchmark.HigherIsBetter
}

func decodeConfig(cfg map[string]any) (tabularConfig, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return tabularConfig{}, err
	}
	var c tabularConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return tabularConfig{}, fmt.Errorf("benchsync: decode tabular config: %w", err)
	}
	if len(c.Boards) == 0 {
		return c, errors.New("benchsync: tabular config needs at least one board")
	}
	seen := map[string]bool{}
	for i, b := range c.Boards {
		switch {
		case b.Key == "" || b.Label == "" || b.Score == "":
			return c, fmt.Errorf("benchsync: boards[%d] needs key, label and score", i)
		case seen[b.Key]:
			return c, fmt.Errorf("benchsync: duplicate board key %q", b.Key)
		case b.Benchmark.Slug == "" || b.Benchmark.Name == "" || b.Benchmark.Category == "" || b.Benchmark.MetricName == "":
			return c, fmt.Errorf("benchsync: boards[%d].benchmark needs slug, name, category and metric_name", i)
		}
		seen[b.Key] = true
	}
	return c, nil
}

// Row 是一行原始数据：列名 -> 值（string / float64 / int64 / bool / nil / 嵌套 map）。
type Row map[string]any

// parseRows 把下载内容按格式解析成行。file 只对 zip_csv 有意义。
func parseRows(body []byte, format, file, rowsPath string) ([]Row, error) {
	switch format {
	case "csv":
		return parseCSV(body)
	case "zip_csv":
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, fmt.Errorf("benchsync: open zip: %w", err)
		}
		for _, f := range zr.File {
			if f.Name == file || path.Clean(f.Name) == path.Clean(file) {
				if f.UncompressedSize64 > 64<<20 {
					return nil, fmt.Errorf("benchsync: zip member %s too large", file)
				}
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
				rc.Close()
				if err != nil {
					return nil, err
				}
				return parseCSV(data)
			}
		}
		return nil, fmt.Errorf("benchsync: %s not found in zip", file)
	case "json":
		var v any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("benchsync: decode json: %w", err)
		}
		return rowsAt(v, rowsPath)
	case "yaml":
		var v any
		if err := yaml.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("benchsync: decode yaml: %w", err)
		}
		return rowsAt(v, rowsPath)
	case "parquet":
		return parseParquet(body)
	default:
		return nil, fmt.Errorf("benchsync: unsupported format %q", format)
	}
}

func parseCSV(data []byte) ([]Row, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("benchsync: parse csv: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	out := make([]Row, 0, len(records)-1)
	for _, rec := range records[1:] {
		row := Row{}
		for i, h := range header {
			if i < len(rec) {
				row[strings.TrimSpace(h)] = rec[i]
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func rowsAt(v any, dotted string) ([]Row, error) {
	if dotted != "" {
		v = lookup(v, dotted)
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("benchsync: rows_path %q is not an array", dotted)
	}
	out := make([]Row, 0, len(arr))
	for _, item := range arr {
		if m := asMap(item); m != nil {
			out = append(out, Row(m))
		}
	}
	return out, nil
}

func asMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case Row:
		return m
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out
	}
	return nil
}

// lookup 按点分路径取值；列名本身含点时（CSV 不会嵌套）先尝试整段匹配。
func lookup(v any, dotted string) any {
	if m := asMap(v); m != nil {
		if val, ok := m[dotted]; ok {
			return val
		}
	}
	for _, part := range strings.Split(dotted, ".") {
		m := asMap(v)
		if m == nil {
			return nil
		}
		v = m[part]
	}
	return v
}

func parseParquet(body []byte) ([]Row, error) {
	f, err := parquet.OpenFile(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("benchsync: open parquet: %w", err)
	}
	cols := f.Schema().Columns()
	var out []Row
	buf := make([]parquet.Row, 512)
	for _, rg := range f.RowGroups() {
		rows := rg.Rows()
		for {
			n, err := rows.ReadRows(buf)
			for _, r := range buf[:n] {
				row := Row{}
				for _, v := range r {
					c := v.Column()
					if c < 0 || c >= len(cols) {
						continue
					}
					row[strings.Join(cols[c], ".")] = parquetValue(v)
				}
				out = append(out, row)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("benchsync: read parquet rows: %w", err)
			}
		}
		rows.Close()
	}
	return out, nil
}

func parquetValue(v parquet.Value) any {
	if v.IsNull() {
		return nil
	}
	switch v.Kind() {
	case parquet.Boolean:
		return v.Boolean()
	case parquet.Int32:
		return int64(v.Int32())
	case parquet.Int64:
		return v.Int64()
	case parquet.Float:
		return float64(v.Float())
	case parquet.Double:
		return v.Double()
	case parquet.ByteArray, parquet.FixedLenByteArray:
		return string(v.ByteArray())
	}
	return v.String()
}

// toFloat 把单元格转成数字；空串 / 非数字 / NaN 返回 ok=false。支持 "45.2%"。
func toFloat(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case nil:
		return 0, false
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		x, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = x
	case string:
		s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(n), "%"))
		s = strings.ReplaceAll(s, ",", "")
		if s == "" {
			return 0, false
		}
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		f = x
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func toString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(s)
	case json.Number:
		return s.String()
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(s))
	}
}

// parseDate 尽量把评测日期列解析出来（2026-09-30 / RFC3339 / 2026-09-30T22:00:34.000Z）。
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.DateOnly, time.RFC3339, "2006-01-02T15:04:05.000Z", "2006/01/02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
