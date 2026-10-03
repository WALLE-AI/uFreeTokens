package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Dataset 是一行 agent_datasets（迁移 00037）：query_analytics 的完整结果，
// 图表与报表只能引用这里的数字。
type Dataset struct {
	ID         int64           `json:"id"`
	SessionID  int64           `json:"session_id"`
	ToolCallID string          `json:"tool_call_id"`
	Title      string          `json:"title"`
	Query      json.RawMessage `json:"query"`
	Columns    json.RawMessage `json:"columns"`
	Rows       json.RawMessage `json:"rows"`
	Totals     json.RawMessage `json:"totals"`
	Previous   json.RawMessage `json:"previous"`
	Notes      json.RawMessage `json:"notes"`
	RowCount   int             `json:"row_count"`
	CreatedAt  time.Time       `json:"created_at"`
}

// SaveDataset 写入一份数据集，回填 ID 与创建时间。
func (s *Store) SaveDataset(ctx context.Context, d *Dataset) error {
	err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO agent_datasets (session_id, tool_call_id, title, query, columns, rows, totals, previous, notes, row_count)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at`,
		d.SessionID, d.ToolCallID, d.Title, nullJSON(d.Query), nullJSON(d.Columns), nullJSON(d.Rows),
		nullJSON(d.Totals), nullJSON(d.Previous), nullJSON(d.Notes), d.RowCount,
	).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return fmt.Errorf("agent: save dataset: %w", err)
	}
	return nil
}

// GetDataset 读取一份数据集；不存在返回 ErrNotFound。
func (s *Store) GetDataset(ctx context.Context, id int64) (*Dataset, error) {
	var d Dataset
	err := s.db(ctx).QueryRow(ctx,
		`SELECT id, session_id, tool_call_id, title, query, columns, rows, totals, previous, notes, row_count, created_at
		 FROM agent_datasets WHERE id = $1`, id,
	).Scan(&d.ID, &d.SessionID, &d.ToolCallID, &d.Title, &d.Query, &d.Columns, &d.Rows, &d.Totals, &d.Previous, &d.Notes, &d.RowCount, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent: get dataset: %w", err)
	}
	return &d, nil
}
