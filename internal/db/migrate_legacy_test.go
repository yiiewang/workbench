package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrate_FreshDB 验证全新空库：000001-000006 六组 baseline 全量迁移，
// 关键结构（roles 种子、阶段 A 的 is_platform_admin、无遗留列）齐全。
func TestMigrate_FreshDB(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "fresh.db")

	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open fresh db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// users 无阶段 A 前的遗留列
	assertColumnNotExists(t, d.conn, "users", "org_id")
	assertColumnNotExists(t, d.conn, "users", "role_id")
	// 阶段 A 的 is_platform_admin 列存在
	assertColumnExists(t, d.conn, "users", "is_platform_admin")
	// 000001_core 预置 3 条角色
	var n int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM roles`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("roles count = %d (err=%v), want 3", n, err)
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
