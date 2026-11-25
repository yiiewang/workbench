package db

// 编排模板 CRUD（pipeline_templates 表）。模板只保存纯 Definition：
// 定时配置属于调度领域，见 cron_triggers.go / schedule_triggers 表。
// db 层不解析 Definition，按 org+user 隔离，列表查询不取 definition。

import (
	"context"
	"database/sql"
)

// PipelineTemplate 模板（列表接口不含 definition，详情才有）
type PipelineTemplate struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Definition string `json:"definition,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

func scanPipelineTemplate(s interface{ Scan(...any) error }, full bool) (PipelineTemplate, error) {
	var t PipelineTemplate
	var err error
	if full {
		err = s.Scan(&t.ID, &t.Name, &t.Definition, &t.CreatedAt, &t.UpdatedAt)
	} else {
		err = s.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.UpdatedAt)
	}
	if err != nil {
		return t, err
	}
	t.CreatedAt = sqliteTimeToRFC3339(t.CreatedAt)
	t.UpdatedAt = sqliteTimeToRFC3339(t.UpdatedAt)
	return t, nil
}

// ListPipelineTemplates 用户模板列表（不含 definition）
func (d *DB) ListPipelineTemplates(ctx context.Context, orgID, userID int64, limit int) ([]PipelineTemplate, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, name, created_at, updated_at
		 FROM pipeline_templates WHERE org_id = ? AND user_id = ? ORDER BY id DESC LIMIT ?`,
		orgID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PipelineTemplate
	for rows.Next() {
		t, err := scanPipelineTemplate(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetPipelineTemplate 模板详情（含 definition）；不存在返回 ok=false
func (d *DB) GetPipelineTemplate(ctx context.Context, orgID, userID, id int64) (PipelineTemplate, bool, error) {
	t, err := scanPipelineTemplate(d.conn.QueryRowContext(ctx,
		`SELECT id, name, definition, created_at, updated_at
		 FROM pipeline_templates WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID), true)
	if err == sql.ErrNoRows {
		return t, false, nil
	}
	return t, err == nil, err
}

// CreatePipelineTemplate 新建模板，返回 id
func (d *DB) CreatePipelineTemplate(ctx context.Context, orgID, userID int64, name, definition string) (int64, error) {
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO pipeline_templates (org_id, user_id, name, definition) VALUES (?, ?, ?, ?)`,
		orgID, userID, name, definition)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePipelineTemplate 覆盖名称与 Definition
func (d *DB) UpdatePipelineTemplate(ctx context.Context, orgID, userID, id int64, name, definition string) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE pipeline_templates SET name = ?, definition = ?, updated_at = datetime('now')
		 WHERE id = ? AND org_id = ? AND user_id = ?`,
		name, definition, id, orgID, userID)
	return err
}

// DeletePipelineTemplate 删除模板
func (d *DB) DeletePipelineTemplate(ctx context.Context, orgID, userID, id int64) error {
	_, err := d.conn.ExecContext(ctx,
		`DELETE FROM pipeline_templates WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID)
	return err
}
