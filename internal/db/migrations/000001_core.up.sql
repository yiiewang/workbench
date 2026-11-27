-- 000001_core.up.sql
-- 基础域：组织 / 用户 / 角色 / 成员关系 / 功能开关 / 分享 / 限流 / 应用密钥 / 通知去重。
CREATE TABLE orgs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    mobile TEXT UNIQUE,
    password_hash TEXT DEFAULT '',
    is_platform_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    revision INTEGER NOT NULL DEFAULT 1,
    email TEXT NOT NULL DEFAULT ''
);

CREATE TABLE roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE user_orgs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    role TEXT NOT NULL DEFAULT 'member',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE,
    UNIQUE (user_id, org_id)
);

CREATE TABLE user_org_feature (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    feature_code TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE,
    UNIQUE (user_id, org_id, feature_code)
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
CREATE INDEX idx_user_orgs_org ON user_orgs(org_id);
CREATE INDEX idx_user_orgs_role ON user_orgs(org_id, role);
CREATE INDEX idx_user_org_feature_org ON user_org_feature(org_id, feature_code);

CREATE TABLE rate_limits (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    expires_at INTEGER NOT NULL
);

CREATE TABLE app_secrets (
    key TEXT PRIMARY KEY,
    value BLOB NOT NULL,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE notify_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    notify_date TEXT NOT NULL,
    sent_at TEXT DEFAULT (datetime('now')),
    UNIQUE (topic, dedup_key, notify_date)
);

-- 预置基础角色（admin=1 / user=2 / org_admin=3）
INSERT INTO roles (id, name, description) VALUES
    (1, 'admin', '超级管理员，可管理所有用户与组织'),
    (2, 'user', '普通用户'),
    (3, 'org_admin', '组织管理员，仅可管理本组织用户');
