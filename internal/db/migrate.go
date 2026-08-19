package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"
)

// newSchema 全新安装的建表语句（含索引），同时被旧库迁移复用。
const newSchema = `
CREATE TABLE IF NOT EXISTS visit_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	visitor_id TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	org_id INTEGER NOT NULL DEFAULT 0,
	user_agent TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL,
	status_code INTEGER DEFAULT 200,
	created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_visit_visitor ON visit_logs(visitor_id);
CREATE INDEX IF NOT EXISTS idx_visit_path ON visit_logs(path);

CREATE TABLE IF NOT EXISTS orgs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	org_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	mobile TEXT UNIQUE,
	password_hash TEXT DEFAULT '',
	version_json TEXT DEFAULT '',
	created_at TEXT DEFAULT (datetime('now')),
	updated_at TEXT DEFAULT (datetime('now')),
	UNIQUE (org_id, name),
	UNIQUE (org_id, id),
	FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tasks (
	id TEXT NOT NULL,
	user_id INTEGER NOT NULL,
	org_id INTEGER NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	content TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'todo',
	priority TEXT NOT NULL DEFAULT 'medium',
	scheduled TEXT NOT NULL DEFAULT '',
	due TEXT NOT NULL DEFAULT '',
	progress INTEGER DEFAULT 0,
	assignee TEXT NOT NULL DEFAULT '',
	postponed_count INTEGER DEFAULT 0,
	auto_postponed INTEGER DEFAULT 0,
	sort_order INTEGER DEFAULT 0,
	created_at TEXT DEFAULT (datetime('now')),
	updated_at TEXT DEFAULT (datetime('now')),
	PRIMARY KEY (id, user_id, org_id),
	FOREIGN KEY (org_id, user_id) REFERENCES users(org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS app_secrets (
	key TEXT PRIMARY KEY,
	value BLOB NOT NULL,
	created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS shares (
	id TEXT PRIMARY KEY,
	token TEXT UNIQUE NOT NULL,
	owner_user_id INTEGER NOT NULL,
	owner_org_id INTEGER NOT NULL,
	resource_path TEXT NOT NULL,
	resource_type TEXT NOT NULL DEFAULT 'file',
	max_access_count INTEGER DEFAULT 0,
	access_count INTEGER DEFAULT 0,
	password_hash TEXT DEFAULT '',
	remark TEXT DEFAULT '',
	effective_at TEXT DEFAULT '',
	expires_at TEXT DEFAULT '',
	created_at TEXT DEFAULT (datetime('now')),
	updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_shares_owner ON shares(owner_user_id, owner_org_id);
CREATE INDEX IF NOT EXISTS idx_shares_token ON shares(token);

CREATE TABLE IF NOT EXISTS rate_limits (
	key TEXT PRIMARY KEY,
	count INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL
);
`

// migrate 执行 schema 迁移，使用 background context（启动阶段）
func (d *DB) migrate() error {
	ctx := context.Background()

	// 旧版 TEXT 业务名主键 schema 检测与重建（必须先于建表）
	migrated, err := d.migrateLegacySchema(ctx)
	if err != nil {
		return err
	}
	if !migrated {
		if _, err := d.conn.ExecContext(ctx, newSchema); err != nil {
			return err
		}
	}

	if err := d.migrateUsersColumns(ctx); err != nil {
		return err
	}
	if err := d.migrateTasksColumns(ctx); err != nil {
		return err
	}
	if err := d.migrateSharesColumns(ctx); err != nil {
		return err
	}
	return d.migrateVisitLogsColumns(ctx)
}

// migrateLegacySchema 检测旧版 TEXT 业务名主键 schema，命中则执行重建。
// 返回 true 表示已完成旧库迁移（调用方跳过建表）。
func (d *DB) migrateLegacySchema(ctx context.Context) (bool, error) {
	var name string
	err := d.conn.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='orgs'`).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil // 空库，后续建新表
	}
	if err != nil {
		return false, err
	}

	// 新 schema 的 orgs 含 name 列；旧 schema 的 id 即业务名、无 name 列
	hasName, err := d.tableHasColumn(ctx, "orgs", "name")
	if err != nil {
		return false, err
	}
	if hasName {
		return false, nil // 已是新 schema
	}

	if err := d.migrateFromTextPK(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// migrateFromTextPK 将旧版 TEXT 业务名主键 schema 完全重建为 INTEGER 自增主键 schema。
// 数据映射：orgs.id(业务名) → name 列，users.id(业务名) → name 列，由 AUTOINCREMENT 分配整数 id；
// tasks.user_id/org_id、shares.owner_*、visit_logs.org_id 通过 JOIN orgs.name/users.name 映射为新整数 id；
// visit_logs 空 org_id 统一归入首个 org（id=1）。
func (d *DB) migrateFromTextPK(ctx context.Context) error {
	// 1. WAL checkpoint 落盘，保证主 db 文件包含最新数据
	if _, err := d.conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}

	// 2. 备份数据库文件
	if err := d.backupDB(); err != nil {
		return err
	}

	// 3. 关闭外键约束（重建表期间）
	if _, err := d.conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		_, _ = d.conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
	}()

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 4. 旧表重命名（被引用表放在引用表之后重命名，避免外键引用更新歧义）
	renames := []string{
		"ALTER TABLE tasks RENAME TO tasks_old",
		"ALTER TABLE shares RENAME TO shares_old",
		"ALTER TABLE users RENAME TO users_old",
		"ALTER TABLE orgs RENAME TO orgs_old",
		"ALTER TABLE visit_logs RENAME TO visit_logs_old",
	}
	for _, q := range renames {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("rename old table: %w", err)
		}
	}

	// 5. 建新表（复用 newSchema）
	if _, err := tx.ExecContext(ctx, newSchema); err != nil {
		return fmt.Errorf("create new schema: %w", err)
	}

	// 6. 迁移数据（先父表后子表，AUTOINCREMENT 按 rowid 顺序分配 id，保证 cm→1、yiiewang→1）
	migrations := []string{
		`INSERT INTO orgs (name, created_at) SELECT id, created_at FROM orgs_old ORDER BY rowid`,
		`INSERT INTO users (org_id, name, password_hash, version_json, created_at, updated_at)
		 SELECT o.id, u.id, u.password_hash, u.version_json, u.created_at, u.updated_at
		 FROM users_old u JOIN orgs o ON o.name = u.org_id ORDER BY u.rowid`,
		`INSERT INTO tasks (
			id, user_id, org_id, title, content, status, priority, scheduled, due,
			progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at
		 )
		 SELECT
			t.id, usr.id, o.id, t.title, t.content, t.status, t.priority, t.scheduled, t.due,
			t.progress,
			CASE WHEN t.assignee = '' THEN '' ELSE CAST(a.id AS TEXT) END,
			t.postponed_count, t.auto_postponed, t.sort_order, t.created_at, t.updated_at
		 FROM tasks_old t
		 JOIN orgs o ON o.name = t.org_id
		 JOIN users usr ON usr.org_id = o.id AND usr.name = t.user_id
		 LEFT JOIN users a ON a.org_id = o.id AND a.name = t.assignee
		 ORDER BY t.rowid`,
		`INSERT INTO shares (
			id, token, owner_user_id, owner_org_id, resource_path, resource_type,
			max_access_count, access_count, password_hash, remark, effective_at, expires_at,
			created_at, updated_at
		 )
		 SELECT
			s.id, s.token, usr.id, o.id, s.resource_path, s.resource_type,
			s.max_access_count, s.access_count, s.password_hash, s.remark, s.effective_at, s.expires_at,
			s.created_at, s.updated_at
		 FROM shares_old s
		 JOIN orgs o ON o.name = s.owner_org_id
		 JOIN users usr ON usr.org_id = o.id AND usr.name = s.owner_user_id
		 ORDER BY s.rowid`,
		`INSERT INTO visit_logs (visitor_id, ip, org_id, user_agent, path, status_code, created_at)
		 SELECT v.visitor_id, v.ip, COALESCE(o.id, 1), v.user_agent, v.path, v.status_code, v.created_at
		 FROM visit_logs_old v
		 LEFT JOIN orgs o ON o.name = v.org_id`,
	}
	for _, q := range migrations {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migrate data: %w", err)
		}
	}

	// 7. 删除旧表
	drops := []string{
		"DROP TABLE tasks_old",
		"DROP TABLE shares_old",
		"DROP TABLE users_old",
		"DROP TABLE orgs_old",
		"DROP TABLE visit_logs_old",
	}
	for _, q := range drops {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("drop old table: %w", err)
		}
	}

	return tx.Commit()
}

// backupDB 复制数据库文件到带时间戳的备份文件，供旧库迁移前兜底
func (d *DB) backupDB() error {
	if d.path == "" {
		return nil
	}
	src, err := os.Open(d.path)
	if err != nil {
		return fmt.Errorf("backup: open db file: %w", err)
	}
	defer src.Close()

	backupPath := d.path + ".bak-" + time.Now().Format("20060102-150405")
	dst, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("backup: create backup file: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("backup: copy db file: %w", err)
	}
	return dst.Close()
}

// tableHasColumn 检查表是否包含指定列
func (d *DB) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := d.conn.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var colName, typ string
		var notNull, pk int
		var defVal sql.NullString
		if err := rows.Scan(&cid, &colName, &typ, &notNull, &defVal, &pk); err != nil {
			return false, err
		}
		if colName == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// migrateVisitLogsColumns 检测 visit_logs 表列，自动补齐 org_id 列（兼容旧 schema）
func (d *DB) migrateVisitLogsColumns(ctx context.Context) error {
	rows, err := d.conn.QueryContext(ctx, `PRAGMA table_info(visit_logs)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defVal sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defVal, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	if !existing["org_id"] {
		if _, err := d.conn.ExecContext(ctx, `ALTER TABLE visit_logs ADD COLUMN org_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add column org_id to visit_logs: %w", err)
		}
	}

	// 无论列是新补的还是建表时就存在，都确保索引存在（fresh install 与旧库升级均覆盖）
	if _, err := d.conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_visit_org ON visit_logs(org_id)`); err != nil {
		return fmt.Errorf("create index idx_visit_org: %w", err)
	}

	return nil
}

// migrateUsersColumns 检测 users 表列，自动补齐缺失列（兼容旧 schema）
func (d *DB) migrateUsersColumns(ctx context.Context) error {
	rows, err := d.conn.QueryContext(ctx, `PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defVal sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defVal, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	if !existing["version_json"] {
		if _, err := d.conn.ExecContext(ctx, `ALTER TABLE users ADD COLUMN version_json TEXT DEFAULT ''`); err != nil {
			return fmt.Errorf("add column version_json: %w", err)
		}
	}

	return nil
}

// migrateTasksColumns 检测 tasks 表列，自动补齐缺失列（兼容旧 schema）
func (d *DB) migrateTasksColumns(ctx context.Context) error {
	rows, err := d.conn.QueryContext(ctx, `PRAGMA table_info(tasks)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defVal sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defVal, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	columns := []struct{ name, def string }{
		{"title", "TEXT NOT NULL DEFAULT ''"},
		{"content", "TEXT NOT NULL DEFAULT ''"},
		{"status", "TEXT NOT NULL DEFAULT 'todo'"},
		{"priority", "TEXT NOT NULL DEFAULT 'medium'"},
		{"scheduled", "TEXT NOT NULL DEFAULT ''"},
		{"due", "TEXT NOT NULL DEFAULT ''"},
		{"progress", "INTEGER DEFAULT 0"},
		{"assignee", "TEXT NOT NULL DEFAULT ''"},
		{"postponed_count", "INTEGER DEFAULT 0"},
		{"auto_postponed", "INTEGER DEFAULT 0"},
	}

	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		q := fmt.Sprintf("ALTER TABLE tasks ADD COLUMN %s %s", col.name, col.def)
		if _, err := d.conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}

	return nil
}

// migrateSharesColumns 检测 shares 表列，自动补齐缺失列（兼容旧 schema）
func (d *DB) migrateSharesColumns(ctx context.Context) error {
	rows, err := d.conn.QueryContext(ctx, `PRAGMA table_info(shares)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defVal sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defVal, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	columns := []struct{ name, def string }{
		{"remark", "TEXT DEFAULT ''"},
	}

	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		q := fmt.Sprintf("ALTER TABLE shares ADD COLUMN %s %s", col.name, col.def)
		if _, err := d.conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}

	return nil
}
