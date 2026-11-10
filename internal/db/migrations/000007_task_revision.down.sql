-- 000007_task_revision.down.sql
-- SQLite 不支持 DROP COLUMN 前的约束依赖，重建 users 去掉 revision 列。
CREATE TABLE users_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    password_hash TEXT DEFAULT '',
    version_json TEXT DEFAULT '',
    is_platform_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
INSERT INTO users_new (id, name, password_hash, version_json, is_platform_admin, created_at, updated_at)
    SELECT id, name, password_hash, version_json, is_platform_admin, created_at, updated_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
