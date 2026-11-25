package db

// 编排运行 CRUD（pipeline_runs 表）。状态与逐步结果由服务端落库，
// 按 org+user 隔离；Definition/StepResult 均以 JSON 直存，db 层不解析。

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PipelineRun 一次编排运行（列表接口不返回 definition/steps，避免大载荷）
type PipelineRun struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition,omitempty"`
	Status     string          `json:"status"` // running | done | failed
	Steps      json.RawMessage `json:"steps,omitempty"`
	Error      string          `json:"error"`
	StartedAt  string          `json:"startedAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

// CreatePipelineRun 开跑时插一条 running
func (d *DB) CreatePipelineRun(ctx context.Context, orgID, userID int64, id, name, definitionJSON string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO pipeline_runs (id, org_id, user_id, name, definition, status) VALUES (?, ?, ?, ?, ?, 'running')`,
		id, orgID, userID, name, definitionJSON)
	return err
}

// SavePipelineRun 覆盖状态/步骤结果（单次运行由单个 Runner 串行写）
func (d *DB) SavePipelineRun(ctx context.Context, orgID, userID int64, r PipelineRun) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE pipeline_runs SET status = ?, steps = ?, err_msg = ?, updated_at = datetime('now')
		 WHERE id = ? AND org_id = ? AND user_id = ?`,
		r.Status, string(r.Steps), r.Error, r.ID, orgID, userID)
	return err
}

// GetPipelineRun 单条详情（含 definition 与逐步结果）；不存在返回 ok=false
func (d *DB) GetPipelineRun(ctx context.Context, orgID, userID int64, id string) (PipelineRun, bool, error) {
	var r PipelineRun
	var definition, steps, startedAt, updatedAt string
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, name, definition, status, steps, err_msg, started_at, updated_at
		 FROM pipeline_runs WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID).
		Scan(&r.ID, &r.Name, &definition, &r.Status, &steps, &r.Error, &startedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.Definition = json.RawMessage(definition)
	r.Steps = json.RawMessage(steps)
	r.StartedAt = sqliteTimeToRFC3339(startedAt)
	r.UpdatedAt = sqliteTimeToRFC3339(updatedAt)
	return r, true, nil
}

// ListPipelineRuns 最近运行列表（不含 definition/steps）
func (d *DB) ListPipelineRuns(ctx context.Context, orgID, userID int64, limit int) ([]PipelineRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, name, status, err_msg, started_at, updated_at
		 FROM pipeline_runs WHERE org_id = ? AND user_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`,
		orgID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PipelineRun
	for rows.Next() {
		var r PipelineRun
		var startedAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.Status, &r.Error, &startedAt, &updatedAt); err != nil {
			return nil, err
		}
		r.StartedAt = sqliteTimeToRFC3339(startedAt)
		r.UpdatedAt = sqliteTimeToRFC3339(updatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}
