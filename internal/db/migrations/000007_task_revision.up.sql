-- 000007_task_revision.up.sql
-- 任务同步机制重设计（不做旧兼容）：
--   - 新增 revision（服务端权威版本号，单调递增，每次任务写入 +1）
--   - 删除 version_json 列（md5 对账体系整体废弃）
--   - 增量接口 baseRevision 必填，不等即 409 + 服务端任务副本
--   - 全量 PUT /api/tasks 端点废弃，前端只走增量
PRAGMA foreign_keys=OFF;
CREATE TABLE users_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    mobile TEXT UNIQUE,
    password_hash TEXT DEFAULT '',
    is_platform_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    revision INTEGER NOT NULL DEFAULT 1
);
INSERT INTO users_new (id, name, mobile, password_hash, is_platform_admin, created_at, updated_at, revision)
    SELECT id, name, mobile, password_hash, is_platform_admin, created_at, updated_at, 1 FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
PRAGMA foreign_keys=ON;
