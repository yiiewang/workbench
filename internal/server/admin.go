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

// GET /api/admin/users — 列出所有用户
func (s *Server) handleAdminListUsers(ctx iris.Context) {
	users, err := s.db.ListUsers(ctx.Request().Context())
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

// POST /api/admin/users — 创建用户
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
	if req.RoleID != db.RoleIDAdmin && req.RoleID != db.RoleIDUser {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid roleId")
		return
	}

	orgID, err := s.db.EnsureOrg(rctx, req.Org)
	if err != nil {
		serverError(ctx, "ensure org failed", err, "org", req.Org)
		return
	}
	if existing, err := s.db.FindUserID(rctx, orgID, req.Name); err != nil {
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
		if *req.RoleID != db.RoleIDAdmin && *req.RoleID != db.RoleIDUser {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "invalid roleId")
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
		if err := s.db.UpdateUserRoleByID(rctx, userID, *req.RoleID); err != nil {
			serverError(ctx, "update user role failed", err, "user", userID)
			return
		}
	}

	// 改用户名：非空 + org 内查重
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "name cannot be empty")
			return
		}
		if existing, err := s.db.FindUserID(rctx, target.OrgID, name); err != nil {
			serverError(ctx, "check user exists failed", err, "org", target.OrgID, "user", name)
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
