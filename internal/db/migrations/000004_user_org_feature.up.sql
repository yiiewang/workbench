-- 000004_user_org_feature.up.sql
-- 功能开关从「组织级（org_features）」改为「用户-组织级（user_org_feature）」：
--   每个用户在每个组织里有独立的功能开关（per-user-per-org）。
-- 数据回填：原组织的一份配置复制给该组织的每个 active 成员。

-- 1. 新建 user_org_feature 表
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
CREATE INDEX idx_user_org_feature_org ON user_org_feature(org_id, feature_code);

-- 2. 数据回填：org_features(org_id, feature_code, enabled) → 每个 active 成员各一份
INSERT INTO user_org_feature (user_id, org_id, feature_code, enabled)
SELECT uo.user_id, uo.org_id, of.feature_code, of.enabled
FROM org_features of
JOIN user_orgs uo ON uo.org_id = of.org_id AND uo.status = 'active';

-- 3. 删除旧表
DROP TABLE org_features;
