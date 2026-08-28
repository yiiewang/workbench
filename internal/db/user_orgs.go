package db

import (
	"context"
	"database/sql"
)

// 组织内角色常量（与 user_orgs.role 取值对应）
const (
	RoleOwner  = "owner"  // 组织所有者
	RoleAdmin  = "admin"  // 组织管理员
	RoleMember = "member" // 普通成员
)

// 组织关系状态常量（与 user_orgs.status 取值对应）
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// UserOrg 用户-组织关系（多对多）
type UserOrg struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	OrgID     int64  `json:"orgId"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// UserOrgContext 用户在某组织的聚合上下文（组织名 + 角色 + 状态 + 启用功能），供 /api/userinfo 聚合接口使用
type UserOrgContext struct {
	OrgID    int64    `json:"orgId"`
	OrgName  string   `json:"orgName"`
	Role     string   `json:"role"`
	Status   string   `json:"status"`
	Features []string `json:"features"`
}

// GetUserOrg 查询用户在指定组织的关系，不存在返回 nil
func (d *DB) GetUserOrg(ctx context.Context, userID, orgID int64) (*UserOrg, error) {
	const q = `SELECT id, user_id, org_id, role, status, COALESCE(created_at, '')
		FROM user_orgs WHERE user_id = ? AND org_id = ?`
	var uo UserOrg
	err := d.conn.QueryRowContext(ctx, q, userID, orgID).Scan(
		&uo.ID, &uo.UserID, &uo.OrgID, &uo.Role, &uo.Status, &uo.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &uo, nil
}

// ListUserOrgs 列出用户绑定的所有组织关系（含 disabled），按 id 排序
func (d *DB) ListUserOrgs(ctx context.Context, userID int64) ([]UserOrg, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, user_id, org_id, role, status, COALESCE(created_at, '') FROM user_orgs WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orgs := make([]UserOrg, 0)
	for rows.Next() {
		var uo UserOrg
		if err := rows.Scan(&uo.ID, &uo.UserID, &uo.OrgID, &uo.Role, &uo.Status, &uo.CreatedAt); err != nil {
			return nil, err
		}
		orgs = append(orgs, uo)
	}
	return orgs, rows.Err()
}

// GetUserOrgContexts 一次聚合用户所有 active 组织的上下文（组织名 + 角色 + 状态 + 启用功能）。
// 用于 /api/userinfo 一次请求完成所有上下文初始化。
func (d *DB) GetUserOrgContexts(ctx context.Context, userID int64) ([]UserOrgContext, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT uo.org_id, o.name, uo.role, uo.status
		FROM user_orgs uo
		JOIN orgs o ON o.id = uo.org_id
		WHERE uo.user_id = ? AND uo.status = 'active'
		ORDER BY uo.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ctxs := make([]UserOrgContext, 0)
	orgIDs := make([]int64, 0)
	for rows.Next() {
		var c UserOrgContext
		if err := rows.Scan(&c.OrgID, &c.OrgName, &c.Role, &c.Status); err != nil {
			return nil, err
		}
		c.Features = []string{}
		ctxs = append(ctxs, c)
		orgIDs = append(orgIDs, c.OrgID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ctxs) == 0 {
		return ctxs, nil
	}

	featureMap, err := d.featuresForUserOrgs(ctx, userID, orgIDs)
	if err != nil {
		return nil, err
	}
	for i := range ctxs {
		if f, ok := featureMap[ctxs[i].OrgID]; ok {
			ctxs[i].Features = f
		}
	}
	return ctxs, nil
}

// AddUserOrg 添加用户到组织（幂等：已存在则更新 role/status 为给定值）。
// 同时为用户在该组织预置默认功能（per-user-per-org，幂等不覆盖已有配置）。
func (d *DB) AddUserOrg(ctx context.Context, userID, orgID int64, role string) error {
	const q = `INSERT INTO user_orgs (user_id, org_id, role, status) VALUES (?, ?, ?, 'active')
		ON CONFLICT(user_id, org_id) DO UPDATE SET role = excluded.role, status = 'active', updated_at = datetime('now')`
	if _, err := d.conn.ExecContext(ctx, q, userID, orgID, role); err != nil {
		return err
	}
	return d.SeedUserOrgFeatures(ctx, userID, orgID)
}

// UpdateUserOrgRole 修改用户在某组织的角色
func (d *DB) UpdateUserOrgRole(ctx context.Context, userID, orgID int64, role string) error {
	const q = `UPDATE user_orgs SET role = ?, updated_at = datetime('now') WHERE user_id = ? AND org_id = ?`
	if _, err := d.conn.ExecContext(ctx, q, role, userID, orgID); err != nil {
		return err
	}
	return nil
}

// UpdateUserOrgStatus 修改用户在某组织的状态（active/disabled）
func (d *DB) UpdateUserOrgStatus(ctx context.Context, userID, orgID int64, status string) error {
	const q = `UPDATE user_orgs SET status = ?, updated_at = datetime('now') WHERE user_id = ? AND org_id = ?`
	if _, err := d.conn.ExecContext(ctx, q, status, userID, orgID); err != nil {
		return err
	}
	return nil
}

// CountOrgAdmins 统计组织内 owner+admin 数量（用于防止降级/移除最后一个管理员）
func (d *DB) CountOrgAdmins(ctx context.Context, orgID int64) (int, error) {
	var count int
	err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_orgs WHERE org_id = ? AND role IN ('owner', 'admin') AND status = 'active'`, orgID).Scan(&count)
	return count, err
}

// roleToOrgRole 将对外角色编码映射为组织内角色（admin→owner，org_admin→admin，user→member）
func roleToOrgRole(roleID int64) string {
	switch roleID {
	case RoleIDAdmin:
		return RoleOwner
	case RoleIDOrgAdmin:
		return RoleAdmin
	default:
		return RoleMember
	}
}

// EnsureUserOrg 确保用户-组织关系存在（幂等，已存在则不覆盖 role/status）。
// 用于用户创建时同步建立多对多关系，避免与「更新密码/改角色」语义冲突。
// 同时为用户在该组织预置默认功能（per-user-per-org，幂等不覆盖已有配置）。
func (d *DB) EnsureUserOrg(ctx context.Context, userID, orgID int64, role string) error {
	const q = `INSERT OR IGNORE INTO user_orgs (user_id, org_id, role, status) VALUES (?, ?, ?, 'active')`
	if _, err := d.conn.ExecContext(ctx, q, userID, orgID, role); err != nil {
		return err
	}
	return d.SeedUserOrgFeatures(ctx, userID, orgID)
}

// GetUserOrgIdentity 查询用户在指定组织的身份：组织名/用户名/组织角色/平台超管标志。
// 基于 user_orgs（status=active），无 active 关系时 exists=false。
func (d *DB) GetUserOrgIdentity(ctx context.Context, userID, orgID int64) (orgName, userName, orgRole string, isPlatformAdmin, exists bool, err error) {
	const q = `SELECT o.name, u.name, COALESCE(uo.role, ''), COALESCE(u.is_platform_admin, 0)
		FROM users u
		JOIN user_orgs uo ON uo.user_id = u.id AND uo.org_id = ? AND uo.status = 'active'
		JOIN orgs o ON o.id = uo.org_id
		WHERE u.id = ?`
	var isAdmin int
	err = d.conn.QueryRowContext(ctx, q, orgID, userID).Scan(&orgName, &userName, &orgRole, &isAdmin)
	if err == sql.ErrNoRows {
		return "", "", "", false, false, nil
	}
	if err != nil {
		return "", "", "", false, false, err
	}
	return orgName, userName, orgRole, isAdmin == 1, true, nil
}

// GetUserDefaultOrg 返回用户第一个 active 组织 id，无组织时返回 0。
func (d *DB) GetUserDefaultOrg(ctx context.Context, userID int64) (int64, error) {
	var orgID int64
	err := d.conn.QueryRowContext(ctx,
		`SELECT org_id FROM user_orgs WHERE user_id = ? AND status = 'active' ORDER BY id LIMIT 1`, userID).Scan(&orgID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return orgID, err
}

// ResolveUserOrgIdentity 解析用户组织身份：orgID=0 时取默认组织，否则校验指定组织。
// 返回实际使用的 orgID；无可用组织或指定组织无 active 关系时 resolvedOrgID=0。
func (d *DB) ResolveUserOrgIdentity(ctx context.Context, userID, orgID int64) (resolvedOrgID int64, orgName, userName, orgRole string, isPlatformAdmin bool, err error) {
	if orgID == 0 {
		orgID, err = d.GetUserDefaultOrg(ctx, userID)
		if err != nil {
			return 0, "", "", "", false, err
		}
		if orgID == 0 {
			return 0, "", "", "", false, nil
		}
	}
	orgName, userName, orgRole, isPlatformAdmin, exists, err := d.GetUserOrgIdentity(ctx, userID, orgID)
	if err != nil {
		return 0, "", "", "", false, err
	}
	if !exists {
		return 0, "", "", "", false, nil
	}
	return orgID, orgName, userName, orgRole, isPlatformAdmin, nil
}
