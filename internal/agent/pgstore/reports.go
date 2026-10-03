package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Report 是一行 agent_reports（迁移 00038）：助手用 create_report 组装的报表。
type Report struct {
	ID           int64           `json:"id"`
	SessionID    int64           `json:"session_id"`
	ToolCallID   string          `json:"tool_call_id"`
	OwnerAdminID int64           `json:"owner_admin_id"`
	OwnerName    string          `json:"owner_name"`
	JobID        *int64          `json:"job_id"`
	Title        string          `json:"title"`
	Summary      string          `json:"summary"`
	Sections     json.RawMessage `json:"sections"`
	DatasetIDs   []int64         `json:"dataset_ids"`
	Visibility   string          `json:"visibility"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

const reportCols = `r.id, r.session_id, r.tool_call_id, r.owner_admin_id, COALESCE(u.name, ''), r.job_id, r.title, r.summary,
	r.sections, r.dataset_ids, r.visibility, r.created_at, r.updated_at`

const reportFrom = `FROM agent_reports r LEFT JOIN admin_users u ON u.id = r.owner_admin_id`

func scanReport(row pgx.Row) (*Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.SessionID, &r.ToolCallID, &r.OwnerAdminID, &r.OwnerName, &r.JobID, &r.Title, &r.Summary,
		&r.Sections, &r.DatasetIDs, &r.Visibility, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent: scan report: %w", err)
	}
	if r.DatasetIDs == nil {
		r.DatasetIDs = []int64{}
	}
	return &r, nil
}

// SaveReport 写入一份报表，回填 ID 与时间。
func (s *Store) SaveReport(ctx context.Context, r *Report) error {
	if r.DatasetIDs == nil {
		r.DatasetIDs = []int64{}
	}
	if r.Visibility == "" {
		r.Visibility = "private"
	}
	err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO agent_reports (session_id, tool_call_id, owner_admin_id, job_id, title, summary, sections, dataset_ids, visibility)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, created_at, updated_at`,
		r.SessionID, r.ToolCallID, r.OwnerAdminID, r.JobID, r.Title, r.Summary, []byte(r.Sections), r.DatasetIDs, r.Visibility,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("agent: save report: %w", err)
	}
	return nil
}

// GetReport 读取一份报表；不存在返回 ErrNotFound。
func (s *Store) GetReport(ctx context.Context, id int64) (*Report, error) {
	return scanReport(s.db(ctx).QueryRow(ctx, `SELECT `+reportCols+` `+reportFrom+` WHERE r.id = $1`, id))
}

// ListReportsInput：ViewerID 能看到自己的与共享的报表；All=true（有 audit:read）时看到全部。
// Before 是上一页最后一条的 id。
type ListReportsInput struct {
	ViewerID int64
	All      bool
	Mine     bool
	Q        string
	Before   string
	Limit    int
}

// ListReports 按 id 倒序分页（不含 sections，列表只需要标题与摘要）。
func (s *Store) ListReports(ctx context.Context, in ListReportsInput) ([]Report, string, error) {
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 30
	}
	conds := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	switch {
	case in.Mine:
		add("r.owner_admin_id = $%d", in.ViewerID)
	case !in.All:
		add("(r.owner_admin_id = $%d OR r.visibility = 'shared')", in.ViewerID)
	}
	if q := strings.TrimSpace(in.Q); q != "" {
		add("r.title ILIKE '%%' || $%d || '%%'", q)
	}
	if in.Before != "" {
		id, err := strconv.ParseInt(in.Before, 10, 64)
		if err != nil {
			return nil, "", errors.New("invalid cursor")
		}
		add("r.id < $%d", id)
	}
	args = append(args, in.Limit+1)
	rows, err := s.db(ctx).Query(ctx, fmt.Sprintf(`SELECT %s %s WHERE %s ORDER BY r.id DESC LIMIT $%d`,
		strings.Replace(reportCols, "r.sections", "'[]'::jsonb", 1), reportFrom, strings.Join(conds, " AND "), len(args)), args...)
	if err != nil {
		return nil, "", fmt.Errorf("agent: list reports: %w", err)
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > in.Limit {
		out = out[:in.Limit]
		next = strconv.FormatInt(out[len(out)-1].ID, 10)
	}
	return out, next, nil
}

// UpdateReport 修改标题与可见性（nil = 不改）。
func (s *Store) UpdateReport(ctx context.Context, id int64, title, visibility *string) error {
	tag, err := s.db(ctx).Exec(ctx,
		`UPDATE agent_reports SET title = COALESCE($2, title), visibility = COALESCE($3, visibility), updated_at = now() WHERE id = $1`,
		id, title, visibility)
	if err != nil {
		return fmt.Errorf("agent: update report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteReport 删除一份报表（数据集随会话保留）。
func (s *Store) DeleteReport(ctx context.Context, id int64) error {
	tag, err := s.db(ctx).Exec(ctx, `DELETE FROM agent_reports WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("agent: delete report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDatasets 按 ID 读取多份数据集（报表展示用），不存在的 ID 忽略。
func (s *Store) GetDatasets(ctx context.Context, ids []int64) ([]Dataset, error) {
	out := []Dataset{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT id, session_id, tool_call_id, title, query, columns, rows, totals, previous, notes, row_count, created_at
		 FROM agent_datasets WHERE id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("agent: get datasets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.SessionID, &d.ToolCallID, &d.Title, &d.Query, &d.Columns, &d.Rows, &d.Totals, &d.Previous, &d.Notes, &d.RowCount, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("agent: scan dataset: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
