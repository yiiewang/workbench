package db

// 编排模板 CRUD（pipeline_templates 表）与版本历史（pipeline_template_versions）。
// 模板只保存纯 Definition；定时配置属于调度领域，见 cron_triggers.go。
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

// PipelineTemplateVersion 模板版本（列表不含 definition，回滚详情才有）
type PipelineTemplateVersion struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	Definition string `json:"definition,omitempty"`
	CreatedAt  string `json:"createdAt"`
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

// CreatePipelineTemplate 新建模板并写入版本 1（事务）。
func (d *DB) CreatePipelineTemplate(ctx context.Context, orgID, userID int64, name, definition string) (int64, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO pipeline_templates (org_id, user_id, name, definition) VALUES (?, ?, ?, ?)`,
		orgID, userID, name, definition)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pipeline_template_versions (template_id, org_id, user_id, version, name, definition)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		id, orgID, userID, name, definition); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdatePipelineTemplate 覆盖名称与 Definition，并写入下一版本（事务）。
func (d *DB) UpdatePipelineTemplate(ctx context.Context, orgID, userID, id int64, name, definition string) error {
	return d.updatePipelineTemplate(ctx, orgID, userID, id, name, definition)
}

func (d *DB) updatePipelineTemplate(ctx context.Context, orgID, userID, id int64, name, definition string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM pipeline_template_versions WHERE template_id = ?`,
		id).Scan(&next); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pipeline_template_versions (template_id, org_id, user_id, version, name, definition)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, orgID, userID, next, name, definition); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE pipeline_templates SET name = ?, definition = ?, updated_at = datetime('now')
		 WHERE id = ? AND org_id = ? AND user_id = ?`,
		name, definition, id, orgID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePipelineTemplate 删除模板（版本历史一并删除）
func (d *DB) DeletePipelineTemplate(ctx context.Context, orgID, userID, id int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pipeline_template_versions WHERE template_id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pipeline_templates WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListPipelineTemplateVersions 版本列表（不含 definition，倒序）
func (d *DB) ListPipelineTemplateVersions(ctx context.Context, orgID, userID, templateID int64) ([]PipelineTemplateVersion, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT version, name, created_at FROM pipeline_template_versions
		 WHERE template_id = ? AND org_id = ? AND user_id = ?
		 ORDER BY version DESC LIMIT 50`,
		templateID, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PipelineTemplateVersion
	for rows.Next() {
		var v PipelineTemplateVersion
		if err := rows.Scan(&v.Version, &v.Name, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.CreatedAt = sqliteTimeToRFC3339(v.CreatedAt)
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetPipelineTemplateVersion 指定版本详情（含 definition）；不存在返回 ok=false
func (d *DB) GetPipelineTemplateVersion(ctx context.Context, orgID, userID, templateID int64, version int) (PipelineTemplateVersion, bool, error) {
	var v PipelineTemplateVersion
	err := d.conn.QueryRowContext(ctx,
		`SELECT version, name, definition, created_at FROM pipeline_template_versions
		 WHERE template_id = ? AND org_id = ? AND user_id = ? AND version = ?`,
		templateID, orgID, userID, version).
		Scan(&v.Version, &v.Name, &v.Definition, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	v.CreatedAt = sqliteTimeToRFC3339(v.CreatedAt)
	return v, true, nil
}

// RestorePipelineTemplateVersion 回滚到指定版本：以该版本内容写一个新版本，返回新版本号。
func (d *DB) RestorePipelineTemplateVersion(ctx context.Context, orgID, userID, templateID int64, version int) (int, error) {
	v, ok, err := d.GetPipelineTemplateVersion(ctx, orgID, userID, templateID, version)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, sql.ErrNoRows
	}
	if err := d.updatePipelineTemplate(ctx, orgID, userID, templateID, v.Name, v.Definition); err != nil {
		return 0, err
	}
	var latest int
	if err := d.conn.QueryRowContext(ctx,
		`SELECT MAX(version) FROM pipeline_template_versions WHERE template_id = ?`,
		templateID).Scan(&latest); err != nil {
		return 0, err
	}
	return latest, nil
}
