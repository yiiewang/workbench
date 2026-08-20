package db

import (
	"context"
	"database/sql"
)

// 角色 id 常量（与 roles 表预置数据对应，见 migrate.go seedRoles）
const (
	RoleIDAdmin    = 1
	RoleIDUser     = 2
	RoleIDOrgAdmin = 3
)

// Member 组织成员
type Member struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Role 角色
type Role struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UserInfo 用户完整信息（admin 用户管理用，跨 org）
type UserInfo struct {
	ID        int64  `json:"id"`
	OrgID     int64  `json:"orgId"`
	OrgName   string `json:"orgName"`
	Name      string `json:"name"`
	Mobile    string `json:"mobile"`
	RoleID    int64  `json:"roleId"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
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

// UpsertUser 创建或更新用户密码（按全局唯一 name），返回用户整数 id。
// roleID 仅在新建时生效；更新已有用户密码时不改角色。
func (d *DB) UpsertUser(ctx context.Context, orgID int64, name, passwordHash string, roleID int64) (int64, error) {
	const q = `INSERT INTO users (org_id, name, password_hash, role_id, updated_at) VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(name) DO UPDATE SET password_hash=excluded.password_hash, updated_at=datetime('now')`
	if _, err := d.conn.ExecContext(ctx, q, orgID, name, passwordHash, roleID); err != nil {
		return 0, err
	}
	return d.FindUserIDByName(ctx, name)
}

// FindUserID 按 org_id + name 查用户整数 id，不存在返回 0。
// 用户名已全局唯一，org_id 仅为兼容旧调用方保留的冗余过滤条件。
func (d *DB) FindUserID(ctx context.Context, orgID int64, name string) (int64, error) {
	const q = `SELECT id FROM users WHERE org_id = ? AND name = ?`
	var id int64
	err := d.conn.QueryRowContext(ctx, q, orgID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// FindUserIDByName 按全局唯一 name 查用户整数 id，不存在返回 0
func (d *DB) FindUserIDByName(ctx context.Context, name string) (int64, error) {
	const q = `SELECT id FROM users WHERE name = ?`
	var id int64
	err := d.conn.QueryRowContext(ctx, q, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// FindUserByName 按 org name + user name 查登录凭据，返回整数 id、密码哈希与角色名
func (d *DB) FindUserByName(ctx context.Context, orgName, userName string) (orgID, userID int64, passwordHash, role string, exists bool, err error) {
	const q = `SELECT o.id, u.id, u.password_hash, COALESCE(r.name, 'user')
		FROM users u JOIN orgs o ON o.id = u.org_id
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE o.name = ? AND u.name = ?`
	err = d.conn.QueryRowContext(ctx, q, orgName, userName).Scan(&orgID, &userID, &passwordHash, &role)
	if err == sql.ErrNoRows {
		return 0, 0, "", "", false, nil
	}
	if err != nil {
		return 0, 0, "", "", false, err
	}
	return orgID, userID, passwordHash, role, true, nil
}

// FindUserByGlobalName 按全局唯一 name 查登录凭据（不依赖 org），返回 orgID/orgName/userID、密码哈希与角色名
func (d *DB) FindUserByGlobalName(ctx context.Context, userName string) (orgID int64, orgName string, userID int64, passwordHash, role string, exists bool, err error) {
	const q = `SELECT u.org_id, o.name, u.id, u.password_hash, COALESCE(r.name, 'user')
		FROM users u JOIN orgs o ON o.id = u.org_id
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE u.name = ?`
	err = d.conn.QueryRowContext(ctx, q, userName).Scan(&orgID, &orgName, &userID, &passwordHash, &role)
	if err == sql.ErrNoRows {
		return 0, "", 0, "", "", false, nil
	}
	if err != nil {
		return 0, "", 0, "", "", false, err
	}
	return orgID, orgName, userID, passwordHash, role, true, nil
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

// GetUserIdentity 按整数 id 查 org/user 的 name 与角色名（token 校验后写入上下文）
func (d *DB) GetUserIdentity(ctx context.Context, orgID, userID int64) (orgName, userName, role string, err error) {
	const q = `SELECT o.name, u.name, COALESCE(r.name, 'user')
		FROM users u JOIN orgs o ON o.id = u.org_id
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE u.org_id = ? AND u.id = ?`
	err = d.conn.QueryRowContext(ctx, q, orgID, userID).Scan(&orgName, &userName, &role)
	return orgName, userName, role, err
}

// GetUserNames 按整数 id 查 org/user 的 name（不关心角色的场景，委托 GetUserIdentity）
func (d *DB) GetUserNames(ctx context.Context, orgID, userID int64) (orgName, userName string, err error) {
	orgName, userName, _, err = d.GetUserIdentity(ctx, orgID, userID)
	return orgName, userName, err
}

// SetUserPassword 按 org_id + user_id 更新密码（改密/激活场景，定位不跨 org）
func (d *DB) SetUserPassword(ctx context.Context, orgID, userID int64, passwordHash string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE org_id = ? AND id = ?`, passwordHash, orgID, userID)
	return err
}

// ListRoles 列出所有角色
func (d *DB) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, name, description FROM roles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Description); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, nil
}

// ListUsers 跨 org 列出所有用户（admin 用户管理）；orgID > 0 时仅列该 org
func (d *DB) ListUsers(ctx context.Context, orgID int64) ([]UserInfo, error) {
	const q = `SELECT u.id, u.org_id, o.name, u.name, COALESCE(u.mobile, ''), u.role_id, COALESCE(r.name, 'user'), u.created_at
		FROM users u
		JOIN orgs o ON o.id = u.org_id
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE (? <= 0 OR u.org_id = ?)
		ORDER BY u.id`
	rows, err := d.conn.QueryContext(ctx, q, orgID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserInfo
	for rows.Next() {
		var u UserInfo
		if err := rows.Scan(&u.ID, &u.OrgID, &u.OrgName, &u.Name, &u.Mobile, &u.RoleID, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// GetUserByID 按全局用户 id 查完整信息，不存在返回 nil
func (d *DB) GetUserByID(ctx context.Context, userID int64) (*UserInfo, error) {
	const q = `SELECT u.id, u.org_id, o.name, u.name, COALESCE(u.mobile, ''), u.role_id, COALESCE(r.name, 'user'), u.created_at
		FROM users u
		JOIN orgs o ON o.id = u.org_id
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE u.id = ?`
	var u UserInfo
	err := d.conn.QueryRowContext(ctx, q, userID).Scan(&u.ID, &u.OrgID, &u.OrgName, &u.Name, &u.Mobile, &u.RoleID, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser 新建用户（admin 专用），用户名在 org 内冲突时返回唯一约束错误
func (d *DB) CreateUser(ctx context.Context, orgID int64, name, mobile, passwordHash string, roleID int64) (int64, error) {
	var mobileVal any
	if mobile == "" {
		mobileVal = nil
	} else {
		mobileVal = mobile
	}
	const q = `INSERT INTO users (org_id, name, mobile, password_hash, role_id) VALUES (?, ?, ?, ?, ?)`
	if _, err := d.conn.ExecContext(ctx, q, orgID, name, mobileVal, passwordHash, roleID); err != nil {
		return 0, err
	}
	return d.FindUserID(ctx, orgID, name)
}

// UpdateUserPasswordByID 重置用户密码（admin 专用，按全局 id）
func (d *DB) UpdateUserPasswordByID(ctx context.Context, userID int64, passwordHash string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?`, passwordHash, userID)
	return err
}

// UpdateUserRoleByID 修改用户角色（admin 专用，按全局 id）
func (d *DB) UpdateUserRoleByID(ctx context.Context, userID, roleID int64) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET role_id = ?, updated_at = datetime('now') WHERE id = ?`, roleID, userID)
	return err
}

// UpdateUserNameByID 修改用户名（admin 专用，按全局 id）
func (d *DB) UpdateUserNameByID(ctx context.Context, userID int64, name string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET name = ?, updated_at = datetime('now') WHERE id = ?`, name, userID)
	return err
}

// UpdateUserMobileByID 修改用户手机号（admin 专用），空串清空为 NULL 以避开唯一约束
func (d *DB) UpdateUserMobileByID(ctx context.Context, userID int64, mobile string) error {
	var mobileVal any
	if mobile == "" {
		mobileVal = nil
	} else {
		mobileVal = mobile
	}
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET mobile = ?, updated_at = datetime('now') WHERE id = ?`, mobileVal, userID)
	return err
}

// DeleteUserByID 删除用户（admin 专用），tasks/shares 由外键级联删除
func (d *DB) DeleteUserByID(ctx context.Context, userID int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	return err
}

// CountAdmins 统计 admin 用户数（用于防止删除/降级最后一个 admin）
func (d *DB) CountAdmins(ctx context.Context) (int, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role_id = ?`, RoleIDAdmin).Scan(&count)
	return count, err
}

// UserDashboard 用户看板统计（按 org+user 聚合，供 admin 看板展示）
type UserDashboard struct {
	TotalTasks int `json:"totalTasks"`
	DoneTasks  int `json:"doneTasks"`
	ShareCount int `json:"shareCount"`
}

// GetUserDashboard 统计某用户的任务总数、已完成数与分享数
func (d *DB) GetUserDashboard(ctx context.Context, orgID, userID int64) (*UserDashboard, error) {
	var dash UserDashboard
	if err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE org_id = ? AND user_id = ?`, orgID, userID).Scan(&dash.TotalTasks); err != nil {
		return nil, err
	}
	if err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE org_id = ? AND user_id = ? AND status = 'done'`, orgID, userID).Scan(&dash.DoneTasks); err != nil {
		return nil, err
	}
	if err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM shares WHERE owner_org_id = ? AND owner_user_id = ?`, orgID, userID).Scan(&dash.ShareCount); err != nil {
		return nil, err
	}
	return &dash, nil
}
