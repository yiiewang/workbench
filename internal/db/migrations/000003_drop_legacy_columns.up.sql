-- 000003_drop_legacy_columns.up.sql
-- 删除 users 表的遗留列 org_id / role_id，用户-组织关系完全迁移到 user_orgs。
-- 数据回填（user_orgs / is_platform_admin）已在 000002_rbac_multi_org 完成，本迁移只做删列。
-- 因涉及表重建（改外键 + 删列），需关闭外键约束（golang-migrate 以 NoTxWrap 执行）。

PRAGMA foreign_keys=OFF;

-- 1. 重建 tasks：复合外键 (org_id, user_id) REFERENCES users(org_id, id)
--    改为单外键 (user_id) REFERENCES users(id)。
--    任务仍保留 org_id 列（任务归属组织的业务隔离字段）。
CREATE TABLE tasks_new (
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
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
INSERT INTO tasks_new (id, user_id, org_id, title, content, status, priority, scheduled, due,
    progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at)
SELECT id, user_id, org_id, title, content, status, priority, scheduled, due,
    progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at
FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

-- 2. 重建 users：删除 org_id / role_id 遗留列。
--    组织归属由 user_orgs 提供，角色由 is_platform_admin（平台超管）+ user_orgs.role（组织角色）提供。
CREATE TABLE users_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    mobile TEXT UNIQUE,
    password_hash TEXT DEFAULT '',
    version_json TEXT DEFAULT '',
    is_platform_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
INSERT INTO users_new (id, name, mobile, password_hash, version_json, is_platform_admin, created_at, updated_at)
SELECT id, name, mobile, password_hash, version_json, is_platform_admin, created_at, updated_at
FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

PRAGMA foreign_keys=ON;
