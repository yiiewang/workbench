-- 000026_pipeline_template_versions.up.sql
-- 编排模板版本历史：每次保存/回滚写入一个新版本，便于审计与回退。
CREATE TABLE pipeline_template_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    template_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    version INTEGER NOT NULL,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now')),
    UNIQUE (template_id, version)
);
CREATE INDEX idx_pipeline_template_versions_owner
    ON pipeline_template_versions(org_id, user_id, template_id, version DESC);
