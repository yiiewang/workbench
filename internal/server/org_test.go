package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/yiiewang/workbench/internal/db"
)

// TestHandler_UserInfo 验证聚合接口一次返回用户全部上下文：
// 基本信息（含平台超管标志）、组织列表（各含角色/状态/功能）、当前组织 id、对外角色、功能列表。
func TestHandler_UserInfo(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	// alice 在 org1 关闭 admin 功能（per-user-per-org）
	org1, _ := d.FindOrgID(context.Background(), "org1")
	aliceID, _ := d.FindUserIDByName(context.Background(), "alice")
	if err := d.UpsertUserOrgFeature(context.Background(), aliceID, org1, db.FeatureAdmin, false); err != nil {
		t.Fatal(err)
	}

	obj := e.GET("/api/userinfo").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object()
	data := obj.Value("data").Object()

	data.Value("user").Object().Value("userName").Equal("alice")
	data.Value("user").Object().Value("isPlatformAdmin").Equal(false)
	data.Value("currentOrgId").Number().Equal(float64(org1))
	data.Value("role").Equal("user")
	data.Value("features").Array().NotContains(db.FeatureAdmin)

	orgs := data.Value("orgs").Array()
	orgs.Length().Equal(1)
	orgs.First().Object().Value("orgName").Equal("org1")
	orgs.First().Object().Value("role").Equal(db.RoleMember)
	orgs.First().Object().Value("status").Equal(db.StatusActive)
}

// TestHandler_UserInfo_PlatformAdmin 平台超管 userinfo 返回 isPlatformAdmin=true、role=admin
func TestHandler_UserInfo_PlatformAdmin(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createAdminUser(t, d, secret, "org1", "root", "p")

	data := e.GET("/api/userinfo").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object()
	data.Value("user").Object().Value("isPlatformAdmin").Equal(true)
	data.Value("role").Equal("admin")
}

// TestHandler_OrgSwitch 验证组织切换接口：合法切换返回目标组织上下文，越权 403，缺参 400。
func TestHandler_OrgSwitch(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	org2, _ := d.EnsureOrg(context.Background(), "org2")
	aliceID, _ := d.FindUserIDByName(context.Background(), "alice")
	if err := d.AddUserOrg(context.Background(), aliceID, org2, db.RoleMember); err != nil {
		t.Fatal(err)
	}

	// 切换到 org2 → 成功
	obj := e.POST("/api/org/switch").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]any{"orgId": org2}).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object()
	obj.Value("orgId").Number().Equal(float64(org2))
	obj.Value("orgName").Equal("org2")
	obj.Value("role").Equal("user")

	// 切换到未绑定的组织 → 403
	e.POST("/api/org/switch").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]any{"orgId": 9999}).
		Expect().Status(http.StatusForbidden)

	// 缺 orgId → 400
	e.POST("/api/org/switch").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]any{}).
		Expect().Status(http.StatusBadRequest)
}

// TestHandler_OrgFeatures 验证功能列表接口返回当前用户在当前组织启用的功能。
func TestHandler_OrgFeatures(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	org1, _ := d.FindOrgID(context.Background(), "org1")
	aliceID, _ := d.FindUserIDByName(context.Background(), "alice")
	if err := d.UpsertUserOrgFeature(context.Background(), aliceID, org1, db.FeatureShare, false); err != nil {
		t.Fatal(err)
	}

	arr := e.GET("/api/org/features").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("features").Array()
	arr.NotContains(db.FeatureShare)
	arr.Contains(db.FeatureFile)
}

// TestHandler_AdminUserFeatures 验证成员功能配置接口：列出全部功能状态 + 更新开关 + 越权拦截。
func TestHandler_AdminUserFeatures(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	adminTok := createAdminUser(t, d, secret, "org1", "root", "p")
	aliceTok := createUser(t, d, secret, "org1", "alice", "p")
	aliceID, _ := d.FindUserIDByName(context.Background(), "alice")

	// 普通用户（非管理员）访问 → 403
	e.GET("/api/admin/users/"+strconv.FormatInt(aliceID, 10)+"/features").
		WithHeader("Authorization", authHeader(aliceTok)).
		Expect().Status(http.StatusForbidden)

	// 列出 alice 全部功能（含未启用的），返回 AllFeatures 数量
	arr := e.GET("/api/admin/users/"+strconv.FormatInt(aliceID, 10)+"/features").
		WithHeader("Authorization", authHeader(adminTok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("features").Array()
	arr.Length().Equal(len(db.AllFeatures))
	arr.First().Object().Value("featureCode").Equal(db.FeatureFile)
	arr.First().Object().Value("enabled").Equal(true)

	// 关闭 alice 的 share 功能
	e.PATCH("/api/admin/users/"+strconv.FormatInt(aliceID, 10)+"/features/share").
		WithHeader("Authorization", authHeader(adminTok)).
		WithJSON(map[string]any{"enabled": false}).
		Expect().Status(http.StatusOK)

	// 再次列出，share 已关闭
	arr2 := e.GET("/api/admin/users/"+strconv.FormatInt(aliceID, 10)+"/features").
		WithHeader("Authorization", authHeader(adminTok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("features").Array()
	shareState := false
	for _, item := range arr2.Iter() {
		if item.Object().Value("featureCode").Raw() == db.FeatureShare {
			shareState = item.Object().Value("enabled").Raw() == true
		}
	}
	if shareState {
		t.Fatal("share should be disabled after update")
	}

	// 非法功能标识 → 400
	e.PATCH("/api/admin/users/"+strconv.FormatInt(aliceID, 10)+"/features/bogus").
		WithHeader("Authorization", authHeader(adminTok)).
		WithJSON(map[string]any{"enabled": true}).
		Expect().Status(http.StatusBadRequest)
}
