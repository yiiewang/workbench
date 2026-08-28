package server

import (
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

// ============================================================
// Admin API（均需 auth + RequireAdmin，跨 org 管理所有用户）
// ============================================================

// GET /api/admin/users — 列出用户。admin 跨 org（可带 ?orgId=xxx 过滤），org_admin 仅看自己 org
func (s *Server) handleAdminListUsers(ctx iris.Context) {
	rctx := ctx.Request().Context()
	var orgFilter int64
	if !isSuperAdmin(ctx) {
		// org_admin 只能看自己 org，忽略请求参数
		orgFilter = currentOrgID(ctx)
	} else {
		// 超级 admin 可选 ?orgId 过滤
		if s := ctx.URLParam("orgId"); s != "" {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				orgFilter = v
			}
		}
	}
	users, err := s.db.ListUsers(rctx, orgFilter)
	if err != nil {
		serverError(ctx, "list users failed", err)
		return
	}
	writeOK(ctx, usersData{Users: users})
}

// GET /api/admin/roles — 列出所有角色
func (s *Server) handleAdminListRoles(ctx iris.Context) {
	roles, err := s.db.ListRoles(ctx.Request().Context())
	if err != nil {
		serverError(ctx, "list roles failed", err)
		return
	}
	writeOK(ctx, rolesData{Roles: roles})
}

// GET /api/admin/users/{id}/dashboard — 用户看板统计（任务数/完成数/分享数）
func (s *Server) handleAdminUserDashboard(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid user id")
		return
	}

	target, err := s.db.GetUserByID(rctx, userID)
	if err != nil {
		serverError(ctx, "get user failed", err, "user", userID)
		return
	}
	if target == nil {
		writeFail(ctx, iris.StatusForbidden, CodeUserNotFound)
		return
	}
	// org_admin 只能看自己 org 用户的看板
	if !s.requireSameOrg(ctx, userID) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "org_admin can only view users in own org")
		return
	}

	dash, err := s.db.GetUserDashboard(rctx, target.OrgID, userID)
	if err != nil {
		serverError(ctx, "get user dashboard failed", err, "user", userID)
		return
	}
	writeOK(ctx, dash)
}

// POST /api/admin/users — 创建用户。admin 跨 org，org_admin 仅可创建自己 org 用户
func (s *Server) handleAdminCreateUser(ctx iris.Context) {
	rctx := ctx.Request().Context()
	var req struct {
		Org      string `json:"org"`
		Name     string `json:"name"`
		Password string `json:"password"`
		RoleID   int64  `json:"roleId"`
		Mobile   string `json:"mobile"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.Org = strings.TrimSpace(req.Org)
	req.Name = strings.TrimSpace(req.Name)
	req.Mobile = strings.TrimSpace(req.Mobile)
	if req.Org == "" || req.Name == "" || req.Password == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "org, name and password are required")
		return
	}
	// 校验 roleId 在白名单内：user/org_admin 任何人都能创建；admin 仅超级 admin 可创建
	if req.RoleID != db.RoleIDUser && req.RoleID != db.RoleIDOrgAdmin && req.RoleID != db.RoleIDAdmin {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid roleId")
		return
	}
	if req.RoleID == db.RoleIDAdmin && !isSuperAdmin(ctx) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeAdminRequired, "Only super admin can create admin")
		return
	}

	orgID, err := s.db.EnsureOrg(rctx, req.Org)
	if err != nil {
		serverError(ctx, "ensure org failed", err, "org", req.Org)
		return
	}
	// org_admin 只能往自己 org 创建
	if !isSuperAdmin(ctx) && orgID != currentOrgID(ctx) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "org_admin can only create users in own org")
		return
	}
	// 用户名全局唯一：跨 org 查重
	if existing, err := s.db.FindUserIDByName(rctx, req.Name); err != nil {
		serverError(ctx, "check user exists failed", err, "org", req.Org, "user", req.Name)
		return
	} else if existing != 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeUserNameExists)
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		serverError(ctx, "hash password failed", err)
		return
	}
	userID, err := s.db.CreateUser(rctx, orgID, req.Name, req.Mobile, hash, req.RoleID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeFail(ctx, iris.StatusBadRequest, CodeUserNameExists)
			return
		}
		serverError(ctx, "create user failed", err, "org", req.Org, "user", req.Name)
		return
	}

	user, err := s.db.GetUserByID(rctx, userID)
	if err != nil {
		serverError(ctx, "get created user failed", err)
		return
	}
	writeOK(ctx, user)
}

// PATCH /api/admin/users/{id} — 更新用户（role/name/mobile/password 可选字段，传哪个改哪个）
func (s *Server) handleAdminUpdateUser(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid user id")
		return
	}

	target, err := s.db.GetUserByID(rctx, userID)
	if err != nil {
		serverError(ctx, "get user failed", err, "user", userID)
		return
	}
	if target == nil {
		writeFail(ctx, iris.StatusForbidden, CodeUserNotFound)
		return
	}
	// org_admin 只能操作自己 org 的用户
	if !s.requireSameOrg(ctx, userID) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "org_admin can only manage users in own org")
		return
	}

	var req struct {
		Name     *string `json:"name"`
		Mobile   *string `json:"mobile"`
		Password *string `json:"password"`
		RoleID   *int64  `json:"roleId"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	// 改角色：降级 admin 需保护（不能降级自己、不能降级最后一个 admin）
	if req.RoleID != nil {
		if *req.RoleID != db.RoleIDAdmin && *req.RoleID != db.RoleIDUser && *req.RoleID != db.RoleIDOrgAdmin {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid roleId")
			return
		}
		// 仅超级 admin 可把角色设为 admin
		if *req.RoleID == db.RoleIDAdmin && !isSuperAdmin(ctx) {
			writeFailMsg(ctx, iris.StatusForbidden, CodeAdminRequired, "Only super admin can grant admin role")
			return
		}
		if target.RoleID == db.RoleIDAdmin && *req.RoleID != db.RoleIDAdmin {
			if target.ID == currentUserID(ctx) {
				writeFail(ctx, iris.StatusForbidden, CodeCannotDeleteSelf)
				return
			}
			admins, err := s.db.CountAdmins(rctx)
			if err != nil {
				serverError(ctx, "count admins failed", err)
				return
			}
			if admins <= 1 {
				writeFail(ctx, iris.StatusForbidden, CodeLastAdmin)
				return
			}
		}
		// 角色是组织级（user_orgs.role）：super admin 改主组织，org_admin 改当前组织
		targetOrgID := target.OrgID
		if !isSuperAdmin(ctx) {
			targetOrgID = currentOrgID(ctx)
		}
		if err := s.db.UpdateUserRoleByID(rctx, userID, targetOrgID, *req.RoleID); err != nil {
			serverError(ctx, "update user role failed", err, "user", userID)
			return
		}
	}

	// 改用户名：非空 + 全局查重（用户名全局唯一）
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "name cannot be empty")
			return
		}
		if existing, err := s.db.FindUserIDByName(rctx, name); err != nil {
			serverError(ctx, "check user exists failed", err, "user", name)
			return
		} else if existing != 0 && existing != userID {
			writeFail(ctx, iris.StatusBadRequest, CodeUserNameExists)
			return
		}
		if err := s.db.UpdateUserNameByID(rctx, userID, name); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				writeFail(ctx, iris.StatusBadRequest, CodeUserNameExists)
				return
			}
			serverError(ctx, "update user name failed", err, "user", userID)
			return
		}
	}

	// 改手机号：空串清空
	if req.Mobile != nil {
		mobile := strings.TrimSpace(*req.Mobile)
		if err := s.db.UpdateUserMobileByID(rctx, userID, mobile); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "mobile already used")
				return
			}
			serverError(ctx, "update user mobile failed", err, "user", userID)
			return
		}
	}

	// 重置密码
	if req.Password != nil {
		if *req.Password == "" {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "password cannot be empty")
			return
		}
		hash, err := HashPassword(*req.Password)
		if err != nil {
			serverError(ctx, "hash password failed", err)
			return
		}
		if err := s.db.UpdateUserPasswordByID(rctx, userID, hash); err != nil {
			serverError(ctx, "update user password failed", err, "user", userID)
			return
		}
	}

	updated, err := s.db.GetUserByID(rctx, userID)
	if err != nil {
		serverError(ctx, "get updated user failed", err, "user", userID)
		return
	}
	writeOK(ctx, updated)
}

// DELETE /api/admin/users/{id} — 删除用户（禁止删除自己、禁止删除最后一个 admin）
func (s *Server) handleAdminDeleteUser(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid user id")
		return
	}

	target, err := s.db.GetUserByID(rctx, userID)
	if err != nil {
		serverError(ctx, "get user failed", err, "user", userID)
		return
	}
	if target == nil {
		writeFail(ctx, iris.StatusForbidden, CodeUserNotFound)
		return
	}
	// org_admin 只能删自己 org 的用户
	if !s.requireSameOrg(ctx, userID) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "org_admin can only delete users in own org")
		return
	}

	if target.ID == currentUserID(ctx) {
		writeFail(ctx, iris.StatusForbidden, CodeCannotDeleteSelf)
		return
	}
	if target.RoleID == db.RoleIDAdmin {
		admins, err := s.db.CountAdmins(rctx)
		if err != nil {
			serverError(ctx, "count admins failed", err)
			return
		}
		if admins <= 1 {
			writeFail(ctx, iris.StatusForbidden, CodeLastAdmin)
			return
		}
	}

	if err := s.db.DeleteUserByID(rctx, userID); err != nil {
		serverError(ctx, "delete user failed", err, "user", userID)
		return
	}
	writeOK(ctx, map[string]any{"deleted": userID})
}

// requireSameOrg 校验目标用户是否属于当前会话组织（org_admin 越权防护）。
// 平台超级管理员（isSuperAdmin）跨 org，直接放行。
func (s *Server) requireSameOrg(ctx iris.Context, targetUserID int64) bool {
	if isSuperAdmin(ctx) {
		return true
	}
	uo, err := s.db.GetUserOrg(ctx.Request().Context(), targetUserID, currentOrgID(ctx))
	if err != nil || uo == nil || uo.Status != db.StatusActive {
		return false
	}
	return true
}

// ============================================================
// 成员功能配置（user_org_feature，per-user-per-org）—— 组织 owner/admin 与平台 admin 均可用
// 操作对象为「某成员在当前会话组织」（currentOrgID，即 X-Org-Id 头）的功能开关
// ============================================================

// GET /api/admin/users/{id}/features — 列出某成员在当前组织的全部功能及启用状态
func (s *Server) handleAdminListUserFeatures(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid user id")
		return
	}
	orgID := currentOrgID(ctx)

	// org_admin 只能配置自己 org 成员
	if !s.requireSameOrg(ctx, userID) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "org_admin can only manage users in own org")
		return
	}

	features, err := s.db.ListUserOrgFeaturesWithState(rctx, userID, orgID)
	if err != nil {
		serverError(ctx, "list user org features failed", err, "user", userID, "org", orgID)
		return
	}
	writeOK(ctx, userFeaturesStateData{Features: features})
}

// PATCH /api/admin/users/{id}/features/{code} — 更新某成员在当前组织的某功能启用状态
func (s *Server) handleAdminUpdateUserFeature(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid user id")
		return
	}
	orgID := currentOrgID(ctx)

	// org_admin 只能配置自己 org 成员
	if !s.requireSameOrg(ctx, userID) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "org_admin can only manage users in own org")
		return
	}

	code := strings.TrimSpace(ctx.Params().Get("code"))
	if !isValidFeature(code) {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid feature code")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	if err := s.db.UpsertUserOrgFeature(rctx, userID, orgID, code, req.Enabled); err != nil {
		serverError(ctx, "update user org feature failed", err, "user", userID, "org", orgID, "feature", code)
		return
	}
	writeOK(ctx, map[string]any{"featureCode": code, "enabled": req.Enabled})
}

// isValidFeature 校验功能标识是否在白名单内
func isValidFeature(code string) bool {
	for _, f := range db.AllFeatures {
		if f == code {
			return true
		}
	}
	return false
}
