package db

import (
	"context"
	"database/sql"
)

// FindUser 查找用户密码哈希
func (d *DB) FindUser(ctx context.Context, orgID, userID string) (passwordHash string, exists bool, err error) {
	const q = `SELECT password_hash FROM users WHERE org_id = ? AND id = ?`
	err = d.conn.QueryRowContext(ctx, q, orgID, userID).Scan(&passwordHash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return passwordHash, err == nil, err
}

// UpsertUser 创建或更新用户密码
func (d *DB) UpsertUser(ctx context.Context, orgID, userID, passwordHash string) error {
	const q = `INSERT INTO users (id, org_id, password_hash, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(org_id, id) DO UPDATE SET password_hash=excluded.password_hash, updated_at=datetime('now')`
	_, err := d.conn.ExecContext(ctx, q, userID, orgID, passwordHash)
	return err
}

// EnsureOrg 确保组织存在
func (d *DB) EnsureOrg(ctx context.Context, orgID string) error {
	const q = `INSERT OR IGNORE INTO orgs (id) VALUES (?)`
	_, err := d.conn.ExecContext(ctx, q, orgID)
	return err
}

// HasAnyUser 判断系统是否已存在任意用户（用于 set-password 首次初始化判断）
func (d *DB) HasAnyUser(ctx context.Context) (bool, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count > 0, err
}

// GetOrgMembers 获取组织成员列表
func (d *DB) GetOrgMembers(ctx context.Context, orgID string) ([]string, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id FROM users WHERE org_id = ?`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		members = append(members, id)
	}
	return members, nil
}

// FindUserOrg 查找用户所属组织
func (d *DB) FindUserOrg(ctx context.Context, userID string) (string, error) {
	const q = `SELECT org_id FROM users WHERE id = ? LIMIT 1`
	var orgID string
	err := d.conn.QueryRowContext(ctx, q, userID).Scan(&orgID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return orgID, err
}
