-- 000003_drop_legacy_columns.down.sql
-- 回滚：恢复 users 的 org_id / role_id 遗留列（尽力还原），并恢复 tasks 的复合外键。
-- org_id 取用户第一个 active 组织；role_id 由 is_platform_admin + 组织角色推导。

PRAGMA foreign_keys=OFF;

-- 1. 恢复 users（加回 org_id / role_id）
CREATE TABLE users_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    name TEXT NOT NULL UNIQUE,
    mobile TEXT UNIQUE,
    password_hash TEXT DEFAULT '',
    version_json TEXT DEFAULT '',
    role_id INTEGER NOT NULL DEFAULT 2,
    is_platform_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE,
    FOREIGN KEY (role_id) REFERENCES roles(id)
);
INSERT INTO users_new (id, org_id, name, mobile, password_hash, version_json, role_id, is_platform_admin, created_at, updated_at)
SELECT
    u.id,
    COALESCE((SELECT uo.org_id FROM user_orgs uo WHERE uo.user_id = u.id ORDER BY uo.id LIMIT 1), 0),
    u.name, u.mobile, u.password_hash, u.version_json,
    CASE WHEN u.is_platform_admin = 1 THEN 1
         WHEN (SELECT uo.role FROM user_orgs uo WHERE uo.user_id = u.id ORDER BY uo.id LIMIT 1) IN ('owner', 'admin') THEN 3
         ELSE 2 END,
    u.is_platform_admin, u.created_at, u.updated_at
FROM users u;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

-- 2. 恢复 tasks 的复合外键
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
    FOREIGN KEY (org_id, user_id) REFERENCES users(org_id, id) ON DELETE CASCADE
);
INSERT INTO tasks_new (id, user_id, org_id, title, content, status, priority, scheduled, due,
    progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at)
SELECT id, user_id, org_id, title, content, status, priority, scheduled, due,
    progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at
FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

PRAGMA foreign_keys=ON;
