package db

import (
	"context"
	"database/sql"
)

// Member 组织成员
type Member struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// EnsureOrg 确保组织存在（按 name），返回组织整数 id
func (d *DB) EnsureOrg(ctx context.Context, name string) (int64, error) {
	const q = `INSERT INTO orgs (name) VALUES (?) ON CONFLICT(name) DO NOTHING`
	if _, err := d.conn.ExecContext(ctx, q, name); err != nil {
		return 0, err
	}
	return d.FindOrgID(ctx, name)
}

// FindOrgID 按 name 查组织整数 id，不存在返回 0
func (d *DB) FindOrgID(ctx context.Context, name string) (int64, error) {
	const q = `SELECT id FROM orgs WHERE name = ?`
	var id int64
	err := d.conn.QueryRowContext(ctx, q, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// UpsertUser 创建或更新用户（按 org_id + name），返回用户整数 id
func (d *DB) UpsertUser(ctx context.Context, orgID int64, name, passwordHash string) (int64, error) {
	const q = `INSERT INTO users (org_id, name, password_hash, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(org_id, name) DO UPDATE SET password_hash=excluded.password_hash, updated_at=datetime('now')`
	if _, err := d.conn.ExecContext(ctx, q, orgID, name, passwordHash); err != nil {
		return 0, err
	}
	return d.FindUserID(ctx, orgID, name)
}

// FindUserID 按 org_id + name 查用户整数 id，不存在返回 0
func (d *DB) FindUserID(ctx context.Context, orgID int64, name string) (int64, error) {
	const q = `SELECT id FROM users WHERE org_id = ? AND name = ?`
	var id int64
	err := d.conn.QueryRowContext(ctx, q, orgID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// FindUserByName 按 org name + user name 查登录凭据，返回整数 id 与密码哈希
func (d *DB) FindUserByName(ctx context.Context, orgName, userName string) (orgID, userID int64, passwordHash string, exists bool, err error) {
	const q = `SELECT o.id, u.id, u.password_hash FROM users u JOIN orgs o ON o.id = u.org_id WHERE o.name = ? AND u.name = ?`
	err = d.conn.QueryRowContext(ctx, q, orgName, userName).Scan(&orgID, &userID, &passwordHash)
	if err == sql.ErrNoRows {
		return 0, 0, "", false, nil
	}
	if err != nil {
		return 0, 0, "", false, err
	}
	return orgID, userID, passwordHash, true, nil
}

// HasAnyUser 判断系统是否已存在任意用户（用于 set-password 首次初始化判断）
func (d *DB) HasAnyUser(ctx context.Context) (bool, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count > 0, err
}

// GetOrgMembers 获取组织成员列表（含 id 与 name）
func (d *DB) GetOrgMembers(ctx context.Context, orgID int64) ([]Member, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, name FROM users WHERE org_id = ? ORDER BY id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

// GetUserNames 按整数 id 查 org/user 的 name（token 校验后写入上下文）
func (d *DB) GetUserNames(ctx context.Context, orgID, userID int64) (orgName, userName string, err error) {
	const q = `SELECT o.name, u.name FROM users u JOIN orgs o ON o.id = u.org_id WHERE u.org_id = ? AND u.id = ?`
	err = d.conn.QueryRowContext(ctx, q, orgID, userID).Scan(&orgName, &userName)
	return orgName, userName, err
}
