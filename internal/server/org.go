// Package server 组织上下文接口：userinfo 聚合 / 组织切换 / 功能列表
package server

import (
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

// ============================================================
// GET /api/userinfo — 聚合接口，一次返回用户全部上下文
// ============================================================

// handleUserInfo 返回用户基本信息、绑定组织列表（各含角色/状态/功能）、
// 当前组织 id、当前用户在当前组织启用的功能列表。
func (s *Server) handleUserInfo(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)

	profile, err := s.db.GetUserProfile(rctx, userID)
	if err != nil {
		serverError(ctx, "get user profile failed", err, "user", userID)
		return
	}
	if profile == nil {
		writeFail(ctx, iris.StatusForbidden, CodeUserNotFound)
		return
	}

	orgs, err := s.db.GetUserOrgContexts(rctx, userID)
	if err != nil {
		serverError(ctx, "get user orgs failed", err, "user", userID)
		return
	}
	if orgs == nil {
		orgs = []db.UserOrgContext{}
	}

	features, err := s.db.ListUserOrgFeatures(rctx, userID, orgID)
	if err != nil {
		serverError(ctx, "list user org features failed", err, "user", userID, "org", orgID)
		return
	}
	if features == nil {
		features = []string{}
	}

	writeOK(ctx, userinfoData{
		User: userinfoUser{
			UserID:          profile.ID,
			UserName:        profile.Name,
			Mobile:          profile.Mobile,
			IsPlatformAdmin: profile.IsPlatformAdmin,
		},
		Orgs:         orgs,
		CurrentOrgID: orgID,
		Role:         currentRole(ctx),
		Features:     features,
	})
}

// ============================================================
// POST /api/org/switch — 切换当前组织上下文
// ============================================================

// handleOrgSwitch 校验目标组织归属后返回其上下文（前端据此更新 X-Org-Id）。
func (s *Server) handleOrgSwitch(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)

	var req struct {
		OrgID int64 `json:"orgId"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if req.OrgID <= 0 {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "orgId is required")
		return
	}

	// 校验用户在该组织存在 active 关系
	uo, err := s.db.GetUserOrg(ctx, userID, req.OrgID)
	if err != nil {
		serverError(ctx, "get user org failed", err, "user", userID, "org", req.OrgID)
		return
	}
	if uo == nil || uo.Status != db.StatusActive {
		writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "User is not a member of this org")
		return
	}

	features, err := s.db.ListUserOrgFeatures(rctx, userID, req.OrgID)
	if err != nil {
		serverError(ctx, "list user org features failed", err, "user", userID, "org", req.OrgID)
		return
	}
	if features == nil {
		features = []string{}
	}

	orgName, _ := s.db.FindOrgName(rctx, req.OrgID)

	writeOK(ctx, orgSwitchData{
		OrgID:    req.OrgID,
		OrgName:  orgName,
		Role:     externalRole(uo.Role, isPlatformAdmin(ctx)),
		Features: features,
	})
}

// ============================================================
// GET /api/org/features — 当前用户在当前组织启用的功能列表
// ============================================================

func (s *Server) handleOrgFeatures(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)

	features, err := s.db.ListUserOrgFeatures(rctx, userID, orgID)
	if err != nil {
		serverError(ctx, "list user org features failed", err, "user", userID, "org", orgID)
		return
	}
	if features == nil {
		features = []string{}
	}
	writeOK(ctx, userFeaturesData{Features: features})
}

// ============================================================
// 载荷结构
// ============================================================

// userinfoUser userinfo 聚合接口的用户基本信息
type userinfoUser struct {
	UserID          int64  `json:"userId"`
	UserName        string `json:"userName"`
	Mobile          string `json:"mobile"`
	IsPlatformAdmin bool   `json:"isPlatformAdmin"`
}

// userinfoData GET /api/userinfo 载荷
type userinfoData struct {
	User         userinfoUser        `json:"user"`
	Orgs         []db.UserOrgContext `json:"orgs"`
	CurrentOrgID int64               `json:"currentOrgId"`
	Role         string              `json:"role"`
	Features     []string            `json:"features"`
}

// orgSwitchData POST /api/org/switch 载荷
type orgSwitchData struct {
	OrgID    int64    `json:"orgId"`
	OrgName  string   `json:"orgName"`
	Role     string   `json:"role"`
	Features []string `json:"features"`
}

// userFeaturesData GET /api/org/features 载荷（当前用户在当前组织启用的功能标识列表）
type userFeaturesData struct {
	Features []string `json:"features"`
}

// userFeaturesStateData GET /api/admin/users/{id}/features 载荷（全部功能 + 启用状态）
type userFeaturesStateData struct {
	Features []db.UserOrgFeature `json:"features"`
}
