-- 000004_user_org_feature.down.sql
-- 回滚：user_org_feature → org_features（组织级，成员共享）。
-- 组织级配置取「该组织第一个成员的功能配置」作为组织配置。

-- 1. 新建 org_features
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

-- 2. 数据回填：每个组织取「该组织任一成员」的功能配置（成员间功能配置视为一致）
INSERT INTO org_features (org_id, feature_code, enabled)
SELECT uf.org_id, uf.feature_code, uf.enabled
FROM user_org_feature uf
WHERE uf.user_id = (
    SELECT MIN(uf2.user_id) FROM user_org_feature uf2 WHERE uf2.org_id = uf.org_id
);

-- 3. 删除新表
DROP TABLE user_org_feature;
