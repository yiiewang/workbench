package db

import "testing"

// TestGetUserOrgContexts_Aggregate 验证 userinfo 聚合：一次查全用户所有组织的
// 组织名 + 角色 + 状态 + 启用功能
func TestGetUserOrgContexts_Aggregate(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	org1, _ := d.EnsureOrg(ctx(), "org1")
	org2, _ := d.EnsureOrg(ctx(), "org2")
	alice, _ := d.UpsertUser(ctx(), org1, "alice", "h1", RoleIDUser)

	// 手动建立多组织关系
	if err := d.AddUserOrg(ctx(), alice, org1, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := d.AddUserOrg(ctx(), alice, org2, RoleMember); err != nil {
		t.Fatal(err)
	}
	// 定制功能（per-user-per-org）：alice 在 org1 关 admin，在 org2 关 todo
	if err := d.UpsertUserOrgFeature(ctx(), alice, org1, FeatureAdmin, false); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertUserOrgFeature(ctx(), alice, org2, FeatureTodo, false); err != nil {
		t.Fatal(err)
	}

	ctxs, err := d.GetUserOrgContexts(ctx(), alice)
	if err != nil {
		t.Fatalf("get user org contexts: %v", err)
	}
	if len(ctxs) != 2 {
		t.Fatalf("contexts = %d, want 2", len(ctxs))
	}

	byOrg := map[int64]UserOrgContext{}
	for _, c := range ctxs {
		byOrg[c.OrgID] = c
	}

	c1 := byOrg[org1]
	if c1.OrgName != "org1" || c1.Role != RoleOwner || c1.Status != StatusActive {
		t.Fatalf("org1 context = %+v", c1)
	}
	if contains(c1.Features, FeatureAdmin) {
		t.Fatalf("org1 features should not contain admin, got %v", c1.Features)
	}

	c2 := byOrg[org2]
	if c2.OrgName != "org2" || c2.Role != RoleMember {
		t.Fatalf("org2 context = %+v", c2)
	}
	if contains(c2.Features, FeatureTodo) {
		t.Fatalf("org2 features should not contain todo, got %v", c2.Features)
	}
}

// TestUserOrg_CRUD 验证 AddUserOrg / UpdateUserOrgRole / UpdateUserOrgStatus / CountOrgAdmins
func TestUserOrg_CRUD(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	org1, _ := d.EnsureOrg(ctx(), "org1")
	alice, _ := d.UpsertUser(ctx(), org1, "alice", "h1", RoleIDUser)
	bob, _ := d.UpsertUser(ctx(), org1, "bob", "h2", RoleIDUser)

	if err := d.AddUserOrg(ctx(), alice, org1, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := d.AddUserOrg(ctx(), bob, org1, RoleMember); err != nil {
		t.Fatal(err)
	}

	// CountOrgAdmins：1 个 owner
	if n, _ := d.CountOrgAdmins(ctx(), org1); n != 1 {
		t.Fatalf("org admins = %d, want 1", n)
	}

	// 提升 bob 为 admin
	if err := d.UpdateUserOrgRole(ctx(), bob, org1, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.CountOrgAdmins(ctx(), org1); n != 2 {
		t.Fatalf("org admins after promote = %d, want 2", n)
	}

	// 禁用 bob 后不计入管理员
	if err := d.UpdateUserOrgStatus(ctx(), bob, org1, StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.CountOrgAdmins(ctx(), org1); n != 1 {
		t.Fatalf("org admins after disable = %d, want 1", n)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
