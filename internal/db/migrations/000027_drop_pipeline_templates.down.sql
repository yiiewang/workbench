-- 000027_drop_pipeline_templates.down.sql
-- 回滚时重建空表，使迁移链（000026 / 000020 的 down）仍可执行。
-- 已删除的模板数据与触发规则不可恢复。
CREATE TABLE IF NOT EXISTS pipeline_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_pipeline_templates_owner ON pipeline_templates(org_id, user_id, id DESC);

CREATE TABLE IF NOT EXISTS pipeline_template_versions (
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
CREATE INDEX IF NOT EXISTS idx_pipeline_template_versions_owner
    ON pipeline_template_versions(org_id, user_id, template_id, version DESC);
