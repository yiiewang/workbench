package db

import (
	"context"
	"database/sql"
	"fmt"
)

// migrate 执行 schema 迁移，使用 background context（启动阶段）
func (d *DB) migrate() error {
	ctx := context.Background()
	schema := `
	CREATE TABLE IF NOT EXISTS visit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		visitor_id TEXT NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		org_id TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL,
		status_code INTEGER DEFAULT 200,
		created_at TEXT DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_visit_visitor ON visit_logs(visitor_id);
	CREATE INDEX IF NOT EXISTS idx_visit_path ON visit_logs(path);
	CREATE INDEX IF NOT EXISTS idx_visit_org ON visit_logs(org_id);

	CREATE TABLE IF NOT EXISTS orgs (
		id TEXT PRIMARY KEY,
		created_at TEXT DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS users (
		id TEXT NOT NULL,
		org_id TEXT NOT NULL,
		password_hash TEXT DEFAULT '',
		version_json TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now')),
		PRIMARY KEY (org_id, id),
		FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		org_id TEXT NOT NULL,
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
		owner_user_id TEXT NOT NULL,
		owner_org_id TEXT NOT NULL,
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
	if _, err := d.conn.ExecContext(ctx, schema); err != nil {
		return err
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
		if _, err := d.conn.ExecContext(ctx, `ALTER TABLE visit_logs ADD COLUMN org_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add column org_id to visit_logs: %w", err)
		}
		if _, err := d.conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_visit_org ON visit_logs(org_id)`); err != nil {
			return fmt.Errorf("create index idx_visit_org: %w", err)
		}
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
