-- 000001_baseline.up.sql
-- 阶段 A 之前的旧态：单组织结构（users.org_id + users.role_id），
-- 无 user_orgs / org_features 表，无 is_platform_admin 列。
-- 新库从 0 开始执行本 baseline，再由 000002 / 000003 演进到最终态。

CREATE TABLE visit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    visitor_id TEXT NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    org_id INTEGER NOT NULL DEFAULT 0,
    user_agent TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    status_code INTEGER DEFAULT 200,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_visit_visitor ON visit_logs(visitor_id);
CREATE INDEX idx_visit_path ON visit_logs(path);
CREATE INDEX idx_visit_org ON visit_logs(org_id);

CREATE TABLE orgs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now'))
);

-- 阶段 A 前的 users：含 org_id / role_id，无 is_platform_admin。
-- UNIQUE(org_id, id) 供 tasks 表的复合外键 (org_id, user_id) REFERENCES users(org_id, id) 引用。
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    name TEXT NOT NULL UNIQUE,
    mobile TEXT UNIQUE,
    password_hash TEXT DEFAULT '',
    version_json TEXT DEFAULT '',
    role_id INTEGER NOT NULL DEFAULT 2,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE
);

CREATE TABLE tasks (
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

CREATE TABLE app_secrets (
    key TEXT PRIMARY KEY,
    value BLOB NOT NULL,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE shares (
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
CREATE INDEX idx_shares_owner ON shares(owner_user_id, owner_org_id);
CREATE INDEX idx_shares_token ON shares(token);

CREATE TABLE rate_limits (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    expires_at INTEGER NOT NULL
);

-- 预置基础角色（admin=1 / user=2 / org_admin=3）
INSERT INTO roles (id, name, description) VALUES
    (1, 'admin', '超级管理员，可管理所有用户与组织'),
    (2, 'user', '普通用户'),
    (3, 'org_admin', '组织管理员，仅可管理本组织用户');
