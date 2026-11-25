-- 000020_pipeline_templates_pure.down.sql
-- 回滚：把 4 个调度列加回，并从 schedule_triggers 反向回填。
-- 迁移按版本倒序回滚，此时 000019 的 schedule_triggers 仍然存在。
-- 一个模板可能有多条触发规则，取 id 最大的一条（最近创建）。
CREATE TABLE pipeline_templates_legacy (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,
    cron_expr TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT NOT NULL DEFAULT '',
    last_status TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
INSERT INTO pipeline_templates_legacy (id, org_id, user_id, name, definition, created_at, updated_at)
SELECT id, org_id, user_id, name, definition, created_at, updated_at FROM pipeline_templates;
UPDATE pipeline_templates_legacy SET
    cron_expr = COALESCE((
        SELECT st.cron_expr FROM schedule_triggers st
        WHERE st.target_type = 'pipeline_template' AND st.target_id = pipeline_templates_legacy.id
        ORDER BY st.id DESC LIMIT 1), ''),
    enabled = COALESCE((
        SELECT st.enabled FROM schedule_triggers st
        WHERE st.target_type = 'pipeline_template' AND st.target_id = pipeline_templates_legacy.id
        ORDER BY st.id DESC LIMIT 1), 1),
    last_run_at = COALESCE((
        SELECT st.last_run_at FROM schedule_triggers st
        WHERE st.target_type = 'pipeline_template' AND st.target_id = pipeline_templates_legacy.id
        ORDER BY st.id DESC LIMIT 1), ''),
    last_status = COALESCE((
        SELECT st.last_status FROM schedule_triggers st
        WHERE st.target_type = 'pipeline_template' AND st.target_id = pipeline_templates_legacy.id
        ORDER BY st.id DESC LIMIT 1), '');
DROP TABLE pipeline_templates;
ALTER TABLE pipeline_templates_legacy RENAME TO pipeline_templates;
CREATE INDEX idx_pipeline_templates_owner ON pipeline_templates(org_id, user_id, id DESC);
CREATE INDEX idx_pipeline_templates_enabled ON pipeline_templates(enabled);
