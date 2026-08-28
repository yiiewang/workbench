-- 000002_rbac_multi_org.up.sql
-- 阶段 A：引入多对多 RBAC。
--   1) users 增加 is_platform_admin 平台超管标志
--   2) 新增 user_orgs（用户-组织多对多）与 org_features（组织-功能映射）表
--   3) 回填：users.org_id + role_id → user_orgs / is_platform_admin
--   4) 为每个组织预置全部功能

-- 1. 平台超管标志列
ALTER TABLE users ADD COLUMN is_platform_admin INTEGER NOT NULL DEFAULT 0;

-- 2. 多对多关系表
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
CREATE INDEX idx_user_orgs_org ON user_orgs(org_id);
CREATE INDEX idx_user_orgs_role ON user_orgs(org_id, role);

-- 3. 组织功能映射表
CREATE TABLE org_features (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    feature_code TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE,
    UNIQUE (org_id, feature_code)
);
CREATE INDEX idx_org_features_enabled ON org_features(org_id, enabled);

-- 4. 回填数据：role_id=1(admin) → is_platform_admin=1
UPDATE users SET is_platform_admin = 1 WHERE role_id = 1 AND is_platform_admin = 0;

-- 5. 回填 user_orgs：users.org_id → 组织关系，角色按 role_id 映射（admin→owner, org_admin→admin, user→member）
INSERT INTO user_orgs (user_id, org_id, role, status)
SELECT u.id, u.org_id,
    CASE u.role_id WHEN 1 THEN 'owner' WHEN 3 THEN 'admin' ELSE 'member' END,
    'active'
FROM users u
WHERE u.org_id > 0;

-- 6. 预置 org_features：每个组织启用全部功能
INSERT INTO org_features (org_id, feature_code, enabled)
SELECT o.id, f.code, 1
FROM orgs o
CROSS JOIN (SELECT 'file' AS code UNION ALL SELECT 'share' UNION ALL SELECT 'todo' UNION ALL SELECT 'admin') f;
