package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestDB 创建一个临时 SQLite 数据库用于测试，测试结束自动关闭。
func newTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func ctx() context.Context { return context.Background() }

// ============================================================
// Open / 迁移
// ============================================================

func TestOpen_CreatesTables(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	tables := []string{"orgs", "users", "tasks", "shares", "app_secrets", "visit_logs"}
	for _, tbl := range tables {
		var name string
		err := d.conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Fatalf("table %s not created: %v", tbl, err)
		}
	}
}

// ============================================================
// 应用密钥
// ============================================================

func TestLoadOrCreateSecret_GenerateAndLoad(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	secret, err := d.LoadOrCreateSecret(ctx(), "token", "")
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	if len(secret) != 32 {
		t.Fatalf("secret length = %d, want 32", len(secret))
	}

	again, err := d.LoadOrCreateSecret(ctx(), "token", "")
	if err != nil {
		t.Fatalf("load secret: %v", err)
	}
	if string(again) != string(secret) {
		t.Fatal("reloaded secret differs from generated")
	}
}

func TestLoadOrCreateSecret_MigrateLegacyFile(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	legacy := filepath.Join(t.TempDir(), ".token_secret")
	want := []byte("legacy-secret-bytes")
	if err := os.WriteFile(legacy, want, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := d.LoadOrCreateSecret(ctx(), "token", legacy)
	if err != nil {
		t.Fatalf("migrate secret: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("migrated secret = %q, want %q", got, want)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy file should be removed after migration")
	}
	again, _ := d.LoadOrCreateSecret(ctx(), "token", "")
	if string(again) != string(want) {
		t.Fatal("db should persist migrated secret")
	}
}

// ============================================================
// 用户 / 组织
// ============================================================

func TestUsers_CRUD(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	if has, err := d.HasAnyUser(ctx()); err != nil || has {
		t.Fatalf("HasAnyUser initial = %v, err=%v, want false", has, err)
	}

	hash, exists, err := d.FindUser(ctx(), "org1", "alice")
	if err != nil || exists || hash != "" {
		t.Fatalf("FindUser missing = (%q,%v,%v), want empty/false/nil", hash, exists, err)
	}

	if err := d.EnsureOrg(ctx(), "org1"); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureOrg(ctx(), "org1"); err != nil {
		t.Fatalf("EnsureOrg idempotent: %v", err)
	}

	if err := d.UpsertUser(ctx(), "org1", "alice", "hash-v1"); err != nil {
		t.Fatal(err)
	}
	hash, exists, err = d.FindUser(ctx(), "org1", "alice")
	if err != nil || !exists || hash != "hash-v1" {
		t.Fatalf("FindUser after create = (%q,%v,%v), want hash-v1/true", hash, exists, err)
	}

	if err := d.UpsertUser(ctx(), "org1", "alice", "hash-v2"); err != nil {
		t.Fatal(err)
	}
	hash, _, _ = d.FindUser(ctx(), "org1", "alice")
	if hash != "hash-v2" {
		t.Fatalf("password not updated, got %q", hash)
	}

	if has, _ := d.HasAnyUser(ctx()); !has {
		t.Fatal("HasAnyUser should be true after creating user")
	}

	members, err := d.GetOrgMembers(ctx(), "org1")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0] != "alice" {
		t.Fatalf("members = %v, want [alice]", members)
	}

	org, err := d.FindUserOrg(ctx(), "alice")
	if err != nil || org != "org1" {
		t.Fatalf("FindUserOrg = %q, err=%v, want org1", org, err)
	}
	org, _ = d.FindUserOrg(ctx(), "nobody")
	if org != "" {
		t.Fatalf("FindUserOrg unknown = %q, want empty", org)
	}
}

// ============================================================
// 任务
// ============================================================

func TestTasks_UpsertReplaceAndVersion(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	_ = d.EnsureOrg(ctx(), "org1")
	_ = d.UpsertUser(ctx(), "org1", "alice", "h")

	tasks := []TaskItem{
		{ID: "t1", Title: "first", Status: "todo", SortOrder: 1},
		{ID: "t2", Title: "second", Status: "done", SortOrder: 2},
	}
	if err := d.UpsertTasks(ctx(), "org1", "alice", tasks, `{"md5":"abc"}`); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetTasks(ctx(), "org1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("GetTasks len = %d, want 2", len(got))
	}
	if got[0].Title != "first" || got[1].Title != "second" {
		t.Fatalf("tasks order = %v,%v", got[0].Title, got[1].Title)
	}

	if err := d.UpsertTasks(ctx(), "org1", "alice", []TaskItem{{ID: "t3", Title: "only", SortOrder: 1}}, `{"md5":"def"}`); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetTasks(ctx(), "org1", "alice")
	if len(got) != 1 || got[0].ID != "t3" {
		t.Fatalf("after replace got = %v, want [t3]", got)
	}

	data, err := d.GetTasksJSONByOwner(ctx(), "org1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	orgs, ok := data["orgs"].(map[string]map[string]interface{})
	if !ok {
		t.Fatalf("orgs missing in tasks json: %T", data["orgs"])
	}
	org1, ok := orgs["org1"]
	if !ok {
		t.Fatal("org1 missing")
	}
	user := org1["alice"].(map[string]interface{})
	ver := user["version"].(map[string]interface{})
	if ver["md5"] != "def" {
		t.Fatalf("version = %v, want def", ver["md5"])
	}
	if ts, ok := user["tasks"].([]TaskItem); !ok || len(ts) != 1 {
		t.Fatalf("tasks in json = %v", user["tasks"])
	}
}

func TestTasksJSON_EmptyVersionFallback(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	_ = d.EnsureOrg(ctx(), "org1")
	_ = d.UpsertUser(ctx(), "org1", "bob", "h")
	_ = d.UpsertTasks(ctx(), "org1", "bob", nil, "")

	data, _ := d.GetTasksJSONByOwner(ctx(), "org1", "bob")
	user := data["orgs"].(map[string]map[string]interface{})["org1"]["bob"].(map[string]interface{})
	ver := user["version"].(map[string]string)
	if ver["md5"] != "init" {
		t.Fatalf("empty version should fall back to init, got %v", ver)
	}
}

// ============================================================
// 分享
// ============================================================

func TestShares_CRUD(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	s := &Share{
		ID:             "sid-1",
		Token:          "tok-1",
		OwnerUserID:    "alice",
		OwnerOrgID:     "org1",
		ResourcePath:   "/docs",
		ResourceType:   "dir",
		MaxAccessCount: 3,
		PasswordHash:   "hashed",
		Remark:         "note",
	}
	if err := d.CreateShare(ctx(), s); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetShareByToken(ctx(), "tok-1")
	if err != nil || got == nil {
		t.Fatalf("GetShareByToken = %v, err=%v", got, err)
	}
	if got.ID != "sid-1" || got.ResourcePath != "/docs" {
		t.Fatalf("share = %+v", got)
	}
	if !got.HasPassword {
		t.Fatal("HasPassword should be true when PasswordHash set")
	}

	missing, err := d.GetShareByToken(ctx(), "nope")
	if err != nil || missing != nil {
		t.Fatalf("missing share = %v, err=%v", missing, err)
	}

	s2 := &Share{ID: "sid-2", Token: "tok-2", OwnerUserID: "alice", OwnerOrgID: "org1", ResourcePath: "/file.md"}
	_ = d.CreateShare(ctx(), s2)
	list, err := d.ListSharesByOwner(ctx(), "org1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}

	if err := d.DeleteShare(ctx(), "org1", "alice", "sid-1"); err != nil {
		t.Fatalf("delete own share: %v", err)
	}
	if err := d.DeleteShare(ctx(), "org1", "alice", "sid-1"); err == nil {
		t.Fatal("delete missing share should error")
	}
	if err := d.DeleteShare(ctx(), "org1", "eve", "sid-2"); err == nil {
		t.Fatal("delete by non-owner should error")
	}
}

func TestIncrementShareAccessCount_Limit(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	_ = d.CreateShare(ctx(), &Share{
		ID: "sid", Token: "tok", OwnerUserID: "alice", OwnerOrgID: "org1",
		ResourcePath: "/f", MaxAccessCount: 2,
	})

	for i := 1; i <= 2; i++ {
		count, reached, err := d.IncrementShareAccessCount(ctx(), "tok")
		if err != nil || reached {
			t.Fatalf("access %d: count=%d reached=%v err=%v", i, count, reached, err)
		}
		if count != i {
			t.Fatalf("access %d count = %d, want %d", i, count, i)
		}
	}
	count, reached, err := d.IncrementShareAccessCount(ctx(), "tok")
	if err != nil || !reached {
		t.Fatalf("limit access: count=%d reached=%v err=%v", count, reached, err)
	}

	_, _, err = d.IncrementShareAccessCount(ctx(), "missing")
	if err == nil {
		t.Fatal("increment missing share should error")
	}
}

func TestIncrementShareAccessCount_Unlimited(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	_ = d.CreateShare(ctx(), &Share{ID: "sid", Token: "tok", OwnerUserID: "a", OwnerOrgID: "o", ResourcePath: "/f", MaxAccessCount: 0})
	for i := 1; i <= 5; i++ {
		count, reached, err := d.IncrementShareAccessCount(ctx(), "tok")
		if err != nil || reached {
			t.Fatalf("unlimited access %d: count=%d reached=%v", i, count, reached)
		}
	}
}

// ============================================================
// 访问统计
// ============================================================

func TestStats_LogAndQuery(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)

	stats, err := d.GetStatsByOrg(ctx(), "org1")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalVisitors != 0 || stats.TotalPageViews != 0 {
		t.Fatalf("empty stats = %+v", stats)
	}

	_ = d.LogVisit(ctx(), "v1", "1.1.1.1", "org1", "ua", "/a", 200)
	_ = d.LogVisit(ctx(), "v1", "1.1.1.1", "org1", "ua", "/b", 200)
	_ = d.LogVisit(ctx(), "v2", "2.2.2.2", "org1", "ua", "/a", 200)

	stats, _ = d.GetStatsByOrg(ctx(), "org1")
	if stats.TotalVisitors != 2 {
		t.Fatalf("visitors = %d, want 2", stats.TotalVisitors)
	}
	if stats.TotalPageViews != 3 {
		t.Fatalf("page views = %d, want 3", stats.TotalPageViews)
	}
	v1 := stats.Visitors["v1"]
	if v1.Visits != 2 || v1.PagesVisited != 2 {
		t.Fatalf("v1 = %+v", v1)
	}
	if stats.TopPages["/a"] != 2 {
		t.Fatalf("top page /a = %d, want 2", stats.TopPages["/a"])
	}
}

// ============================================================
// 限流（DB 持久化）
// ============================================================

func TestRateLimit_WindowAndClear(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	const window = 5 * time.Second

	// 无记录 → 0
	if c, _ := d.RateLimitCheck(ctx(), "k", window); c != 0 {
		t.Fatalf("initial count = %d, want 0", c)
	}

	// 记录 3 次失败
	for i := 1; i <= 3; i++ {
		c, err := d.RateLimitRecord(ctx(), "k", window)
		if err != nil || c != i {
			t.Fatalf("record %d: count=%d err=%v, want %d", i, c, err, i)
		}
	}
	if c, _ := d.RateLimitCheck(ctx(), "k", window); c != 3 {
		t.Fatalf("check = %d, want 3", c)
	}

	// clear 后归 0
	if err := d.RateLimitClear(ctx(), "k"); err != nil {
		t.Fatal(err)
	}
	if c, _ := d.RateLimitCheck(ctx(), "k", window); c != 0 {
		t.Fatalf("after clear = %d, want 0", c)
	}
}

func TestRateLimit_WindowExpiry(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	const window = 50 * time.Millisecond

	c, _ := d.RateLimitRecord(ctx(), "k", window)
	if c != 1 {
		t.Fatalf("first record = %d, want 1", c)
	}
	// 等待窗口过期
	time.Sleep(70 * time.Millisecond)
	// 过期后 check 返回 0
	if c, _ := d.RateLimitCheck(ctx(), "k", window); c != 0 {
		t.Fatalf("after expiry check = %d, want 0", c)
	}
	// 再次 record 应重置为 1（非累加）
	c, _ = d.RateLimitRecord(ctx(), "k", window)
	if c != 1 {
		t.Fatalf("after expiry record = %d, want 1", c)
	}
}

// ============================================================
// FlexString
// ============================================================

func TestFlexString_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`"hello"`, "hello"},
		{`123`, "123"},
		{`3.14`, "3.14"},
	}
	for _, c := range cases {
		var f FlexString
		if err := f.UnmarshalJSON([]byte(c.input)); err != nil {
			t.Fatalf("unmarshal %s: %v", c.input, err)
		}
		if f.String() != c.want {
			t.Fatalf("unmarshal %s = %q, want %q", c.input, f, c.want)
		}
	}

	var f FlexString
	if err := f.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("unmarshal object should error")
	}
}
