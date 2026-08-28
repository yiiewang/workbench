package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// readMigration 读取内嵌迁移脚本内容（供测试构造 baseline 状态库）
func readMigration(name string) string {
	b, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestMigrate_FreshDB 验证全新空库：000001→000002→000003 全量迁移。
// Open 成功本身已隐含「全部建表成功」，这里只断言三个迁移脚本的关键结构特征。
func TestMigrate_FreshDB(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "fresh.db")

	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open fresh db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// 000003：users 无遗留列
	assertColumnNotExists(t, d.conn, "users", "org_id")
	assertColumnNotExists(t, d.conn, "users", "role_id")
	// 000002：阶段 A 的 is_platform_admin 列存在
	assertColumnExists(t, d.conn, "users", "is_platform_admin")
	// 000001：roles 预置 3 条
	var n int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM roles`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("roles count = %d (err=%v), want 3", n, err)
	}
}

// TestMigrate_LegacyDB 验证存量库（baseline 状态，含 org_id/role_id + 数据，无 schema_migrations）：
// detectBaseVersion 返回 1 → Force(1) → 000002 回填 user_orgs/is_platform_admin + 000003 删列。
func TestMigrate_LegacyDB(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// 用 000001 baseline 构造阶段 A 前存量库（模拟「已应用 baseline 但未纳入 golang-migrate 管理」）
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := conn.Exec(readMigration("000001_baseline.up.sql")); err != nil {
		conn.Close()
		t.Fatalf("seed baseline: %v", err)
	}
	// 插入三种 role_id 的用户数据
	if _, err := conn.Exec(`INSERT INTO orgs (name) VALUES ('cm')`); err != nil {
		conn.Close()
		t.Fatalf("seed org: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO users (org_id, name, password_hash, role_id) VALUES
		(1, 'yiiewang', 'h1', 1),
		(1, 'alice', 'h2', 2),
		(1, 'bob', 'h3', 3)`); err != nil {
		conn.Close()
		t.Fatalf("seed users: %v", err)
	}
	conn.Close()

	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open legacy db for migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// users 无遗留列
	assertColumnNotExists(t, d.conn, "users", "org_id")
	assertColumnNotExists(t, d.conn, "users", "role_id")

	// 回填正确：is_platform_admin + user_orgs.role
	assertPlatformAdmin(t, d, "yiiewang", 1)
	assertPlatformAdmin(t, d, "alice", 0)
	assertPlatformAdmin(t, d, "bob", 0)
	assertUserOrgRole(t, d, "yiiewang", RoleOwner)
	assertUserOrgRole(t, d, "alice", RoleMember)
	assertUserOrgRole(t, d, "bob", RoleAdmin)
}

// assertPlatformAdmin 断言用户 is_platform_admin 标志
func assertPlatformAdmin(t *testing.T, d *DB, name string, want int) {
	t.Helper()
	var got int
	err := d.conn.QueryRow(`SELECT is_platform_admin FROM users WHERE name = ?`, name).Scan(&got)
	if err != nil {
		t.Fatalf("query is_platform_admin for %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("%s is_platform_admin = %d, want %d", name, got, want)
	}
}

// assertUserOrgRole 断言用户在组织内的角色
func assertUserOrgRole(t *testing.T, d *DB, name, wantRole string) {
	t.Helper()
	var got string
	err := d.conn.QueryRow(`
		SELECT uo.role FROM user_orgs uo JOIN users u ON u.id = uo.user_id WHERE u.name = ?`, name).Scan(&got)
	if err != nil {
		t.Fatalf("query user_orgs role for %s: %v", name, err)
	}
	if got != wantRole {
		t.Fatalf("%s user_orgs role = %q, want %q", name, got, wantRole)
	}
}

// assertColumnExists 断言表包含指定列
func assertColumnExists(t *testing.T, conn *sql.DB, table, column string) {
	t.Helper()
	rows, err := conn.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == column {
			return
		}
	}
	t.Fatalf("table %s should contain column %s", table, column)
}

// assertColumnNotExists 断言表不含指定列
func assertColumnNotExists(t *testing.T, conn *sql.DB, table, column string) {
	t.Helper()
	rows, err := conn.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == column {
			t.Fatalf("table %s should NOT contain column %s", table, column)
		}
	}
}
