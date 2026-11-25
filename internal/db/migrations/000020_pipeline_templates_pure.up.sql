-- 000020_pipeline_templates_pure.up.sql
-- 模板表回归「纯 Definition 存储」：调度配置已在 000019 迁到 schedule_triggers，
-- 这里删掉 cron_expr / enabled / last_run_at / last_status 四列。
-- SQLite 用「建新表 → 复制 → 删旧表 → 改名」重建（同 000003 的写法）。
CREATE TABLE pipeline_templates_pure (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,             -- pipeline.Definition JSON
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
INSERT INTO pipeline_templates_pure (id, org_id, user_id, name, definition, created_at, updated_at)
SELECT id, org_id, user_id, name, definition, created_at, updated_at FROM pipeline_templates;
DROP TABLE pipeline_templates;
ALTER TABLE pipeline_templates_pure RENAME TO pipeline_templates;
CREATE INDEX idx_pipeline_templates_owner ON pipeline_templates(org_id, user_id, id DESC);
