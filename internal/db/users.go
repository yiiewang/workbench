package db

import (
	"context"
	"database/sql"
)

// 对外角色编码常量（roles 表预置数据 + 前端下拉/权限判断使用）。
// 存储层已迁移为 is_platform_admin（平台超管）+ user_orgs.role（组织角色），
// 此编码仅作为对外 API 的兼容语义，由内部状态推导。
const (
	RoleIDAdmin    = 1 // 平台超级管理员
	RoleIDUser     = 2 // 普通用户
	RoleIDOrgAdmin = 3 // 组织管理员
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

// UserInfo 用户完整信息（admin 用户管理用）。
// OrgID/Role/RoleID 为用户「主组织」（第一个 active user_orgs）的对外语义，
// 从 is_platform_admin + user_orgs.role 推导。
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

// roleIDFromInternal 内部角色状态 → 对外角色编码（1=admin / 2=user / 3=org_admin）
func roleIDFromInternal(isPlatformAdmin bool, orgRole string) int64 {
	if isPlatformAdmin {
		return RoleIDAdmin
	}
	if orgRole == RoleOwner || orgRole == RoleAdmin {
		return RoleIDOrgAdmin
	}
	return RoleIDUser
}

// roleNameFromInternal 内部角色状态 → 对外角色名（admin / org_admin / user）
func roleNameFromInternal(isPlatformAdmin bool, orgRole string) string {
	if isPlatformAdmin {
		return "admin"
	}
	if orgRole == RoleOwner || orgRole == RoleAdmin {
		return "org_admin"
	}
	return "user"
}

// EnsureOrg 确保组织存在（按 name），返回组织整数 id。
// 功能预置不再在此进行——功能是 per-user-per-org 的，改在用户加入组织时（EnsureUserOrg）预置。
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

// FindOrgName 按组织整数 id 查 name，不存在返回空串
func (d *DB) FindOrgName(ctx context.Context, orgID int64) (string, error) {
	const q = `SELECT name FROM orgs WHERE id = ?`
	var name string
	err := d.conn.QueryRowContext(ctx, q, orgID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

// UpsertUser 创建或更新用户密码（按全局唯一 name），返回用户整数 id。
// roleID 仅在新建时生效（决定 is_platform_admin 标志与组织关系）；更新已有用户密码时不改角色。
func (d *DB) UpsertUser(ctx context.Context, orgID int64, name, passwordHash string, roleID int64) (int64, error) {
	isAdmin := 0
	if roleID == RoleIDAdmin {
		isAdmin = 1
	}
	const q = `INSERT INTO users (name, password_hash, is_platform_admin, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(name) DO UPDATE SET password_hash=excluded.password_hash, updated_at=datetime('now')`
	if _, err := d.conn.ExecContext(ctx, q, name, passwordHash, isAdmin); err != nil {
		return 0, err
	}
	userID, err := d.FindUserIDByName(ctx, name)
	if err != nil {
		return 0, err
	}
	// 同步组织关系（幂等，已存在则不覆盖 role/status）
	if err := d.EnsureUserOrg(ctx, userID, orgID, roleToOrgRole(roleID)); err != nil {
		return 0, err
	}
	return userID, nil
}

// FindUserID 按 name 查用户整数 id（name 全局唯一，orgID 参数仅为兼容旧调用方保留），不存在返回 0。
func (d *DB) FindUserID(ctx context.Context, orgID int64, name string) (int64, error) {
	return d.FindUserIDByName(ctx, name)
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

// FindUserByName 按 org name + user name 查登录凭据。
// 用户-组织关系从 user_orgs 推导；role 为对外角色名。
func (d *DB) FindUserByName(ctx context.Context, orgName, userName string) (orgID, userID int64, passwordHash, role string, exists bool, err error) {
	const q = `
		SELECT uo.org_id, u.id, u.password_hash, u.is_platform_admin, uo.role
		FROM users u
		JOIN user_orgs uo ON uo.user_id = u.id AND uo.status = 'active'
		JOIN orgs o ON o.id = uo.org_id
		WHERE o.name = ? AND u.name = ?
		ORDER BY uo.id LIMIT 1`
	var isAdmin int
	var orgRole string
	err = d.conn.QueryRowContext(ctx, q, orgName, userName).Scan(&orgID, &userID, &passwordHash, &isAdmin, &orgRole)
	if err == sql.ErrNoRows {
		return 0, 0, "", "", false, nil
	}
	if err != nil {
		return 0, 0, "", "", false, err
	}
	return orgID, userID, passwordHash, roleNameFromInternal(isAdmin == 1, orgRole), true, nil
}

// FindUserByGlobalName 按全局唯一 name 查登录凭据（不依赖 org），返回用户默认组织（第一个 active）。
func (d *DB) FindUserByGlobalName(ctx context.Context, userName string) (orgID int64, orgName string, userID int64, passwordHash, role string, exists bool, err error) {
	const q = `
		SELECT uo.org_id, o.name, u.id, u.password_hash, u.is_platform_admin, uo.role
		FROM users u
		JOIN user_orgs uo ON uo.user_id = u.id AND uo.status = 'active'
		JOIN orgs o ON o.id = uo.org_id
		WHERE u.name = ?
		ORDER BY uo.id LIMIT 1`
	var isAdmin int
	var orgRole string
	err = d.conn.QueryRowContext(ctx, q, userName).Scan(&orgID, &orgName, &userID, &passwordHash, &isAdmin, &orgRole)
	if err == sql.ErrNoRows {
		return 0, "", 0, "", "", false, nil
	}
	if err != nil {
		return 0, "", 0, "", "", false, err
	}
	return orgID, orgName, userID, passwordHash, roleNameFromInternal(isAdmin == 1, orgRole), true, nil
}

// HasAnyUser 判断系统是否已存在任意用户（用于 set-password 首次初始化判断）
func (d *DB) HasAnyUser(ctx context.Context) (bool, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count > 0, err
}

// GetOrgMembers 获取组织成员列表（含 id 与 name），成员关系从 user_orgs 推导
func (d *DB) GetOrgMembers(ctx context.Context, orgID int64) ([]Member, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT u.id, u.name FROM users u
		JOIN user_orgs uo ON uo.user_id = u.id AND uo.org_id = ? AND uo.status = 'active'
		ORDER BY u.id`, orgID)
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

// findUserNameByID 按整数 id 查用户名，不存在返回空串
func (d *DB) findUserNameByID(ctx context.Context, userID int64) (string, error) {
	const q = `SELECT name FROM users WHERE id = ?`
	var name string
	err := d.conn.QueryRowContext(ctx, q, userID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

// GetUserNames 按整数 id 查 org/user 的 name（分享场景反查目录路径，不关心角色）
func (d *DB) GetUserNames(ctx context.Context, orgID, userID int64) (orgName, userName string, err error) {
	orgName, err = d.FindOrgName(ctx, orgID)
	if err != nil {
		return "", "", err
	}
	userName, err = d.findUserNameByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	return orgName, userName, nil
}

// SetUserPassword 按全局 id 更新密码（改密/激活场景）
func (d *DB) SetUserPassword(ctx context.Context, orgID, userID int64, passwordHash string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?`, passwordHash, userID)
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

// ListUsers 列出用户（admin 用户管理）。
// orgID > 0 时仅列该组织成员（OrgID/Role 为该组织内语义）；orgID <= 0 时列所有用户（每个取主组织）。
func (d *DB) ListUsers(ctx context.Context, orgID int64) ([]UserInfo, error) {
	var rows *sql.Rows
	var err error
	if orgID > 0 {
		rows, err = d.conn.QueryContext(ctx, `
			SELECT u.id, uo.org_id, o.name, u.name, COALESCE(u.mobile, ''), u.is_platform_admin, uo.role, COALESCE(u.created_at, '')
			FROM users u
			JOIN user_orgs uo ON uo.user_id = u.id AND uo.org_id = ? AND uo.status = 'active'
			JOIN orgs o ON o.id = uo.org_id
			ORDER BY u.id`, orgID)
	} else {
		rows, err = d.conn.QueryContext(ctx, `
			SELECT u.id, uo.org_id, o.name, u.name, COALESCE(u.mobile, ''), u.is_platform_admin, uo.role, COALESCE(u.created_at, '')
			FROM users u
			LEFT JOIN user_orgs uo ON uo.id = (
				SELECT uo2.id FROM user_orgs uo2 WHERE uo2.user_id = u.id AND uo2.status = 'active' ORDER BY uo2.id LIMIT 1
			)
			LEFT JOIN orgs o ON o.id = uo.org_id
			ORDER BY u.id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]UserInfo, 0)
	for rows.Next() {
		var u UserInfo
		var isAdmin int
		var orgRole string
		if err := rows.Scan(&u.ID, &u.OrgID, &u.OrgName, &u.Name, &u.Mobile, &isAdmin, &orgRole, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.RoleID = roleIDFromInternal(isAdmin == 1, orgRole)
		u.Role = roleNameFromInternal(isAdmin == 1, orgRole)
		users = append(users, u)
	}
	return users, nil
}

// GetUserByID 按全局用户 id 查完整信息（取主组织，即第一个 active user_orgs），不存在返回 nil。
func (d *DB) GetUserByID(ctx context.Context, userID int64) (*UserInfo, error) {
	const q = `
		SELECT u.id, uo.org_id, o.name, u.name, COALESCE(u.mobile, ''), u.is_platform_admin, uo.role, COALESCE(u.created_at, '')
		FROM users u
		LEFT JOIN user_orgs uo ON uo.id = (
			SELECT uo2.id FROM user_orgs uo2 WHERE uo2.user_id = u.id AND uo2.status = 'active' ORDER BY uo2.id LIMIT 1
		)
		LEFT JOIN orgs o ON o.id = uo.org_id
		WHERE u.id = ?`
	var u UserInfo
	var isAdmin int
	var orgRole string
	err := d.conn.QueryRowContext(ctx, q, userID).Scan(&u.ID, &u.OrgID, &u.OrgName, &u.Name, &u.Mobile, &isAdmin, &orgRole, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.RoleID = roleIDFromInternal(isAdmin == 1, orgRole)
	u.Role = roleNameFromInternal(isAdmin == 1, orgRole)
	return &u, nil
}

// UserProfile 用户基本信息（userinfo 聚合接口用）
type UserProfile struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Mobile          string `json:"mobile"`
	IsPlatformAdmin bool   `json:"isPlatformAdmin"`
}

// GetUserProfile 按全局用户 id 查基本信息（含平台超管标志），不存在返回 nil
func (d *DB) GetUserProfile(ctx context.Context, userID int64) (*UserProfile, error) {
	const q = `SELECT id, name, COALESCE(mobile, ''), COALESCE(is_platform_admin, 0) FROM users WHERE id = ?`
	var p UserProfile
	var isAdmin int
	err := d.conn.QueryRowContext(ctx, q, userID).Scan(&p.ID, &p.Name, &p.Mobile, &isAdmin)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsPlatformAdmin = isAdmin == 1
	return &p, nil
}

// CreateUser 新建用户（admin 专用），用户名全局唯一冲突时返回唯一约束错误。
// roleID 决定 is_platform_admin 标志与组织角色。
func (d *DB) CreateUser(ctx context.Context, orgID int64, name, mobile, passwordHash string, roleID int64) (int64, error) {
	var mobileVal any
	if mobile == "" {
		mobileVal = nil
	} else {
		mobileVal = mobile
	}
	isAdmin := 0
	if roleID == RoleIDAdmin {
		isAdmin = 1
	}
	const q = `INSERT INTO users (name, mobile, password_hash, is_platform_admin) VALUES (?, ?, ?, ?)`
	if _, err := d.conn.ExecContext(ctx, q, name, mobileVal, passwordHash, isAdmin); err != nil {
		return 0, err
	}
	userID, err := d.FindUserIDByName(ctx, name)
	if err != nil {
		return 0, err
	}
	// 同步组织关系（幂等）
	if err := d.EnsureUserOrg(ctx, userID, orgID, roleToOrgRole(roleID)); err != nil {
		return 0, err
	}
	return userID, nil
}

// UpdateUserPasswordByID 重置用户密码（admin 专用，按全局 id）
func (d *DB) UpdateUserPasswordByID(ctx context.Context, userID int64, passwordHash string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?`, passwordHash, userID)
	return err
}

// UpdateUserRoleByID 修改用户角色（admin 专用）。
// orgID 指定更新哪个组织的角色；同时按 roleID 更新平台超管标志与组织角色。
func (d *DB) UpdateUserRoleByID(ctx context.Context, userID, orgID, roleID int64) error {
	isAdmin := 0
	if roleID == RoleIDAdmin {
		isAdmin = 1
	}
	if _, err := d.conn.ExecContext(ctx,
		`UPDATE users SET is_platform_admin = ?, updated_at = datetime('now') WHERE id = ?`, isAdmin, userID); err != nil {
		return err
	}
	// 更新组织角色（orgID 为 0 时用户无组织，跳过组织角色更新）
	if orgID > 0 {
		return d.UpdateUserOrgRole(ctx, userID, orgID, roleToOrgRole(roleID))
	}
	return nil
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

// DeleteUserByID 删除用户（admin 专用），tasks/user_orgs 由外键级联删除
func (d *DB) DeleteUserByID(ctx context.Context, userID int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	return err
}

// CountAdmins 统计平台超级管理员数量（用于防止删除/降级最后一个 admin）
func (d *DB) CountAdmins(ctx context.Context) (int, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_platform_admin = 1`).Scan(&count)
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
