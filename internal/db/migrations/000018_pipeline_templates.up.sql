-- 000018_pipeline_templates.up.sql
-- 编排模板：把 Definition（纯数据）固化下来复用；cron_expr 非空时由
-- 每分钟扫描的 schedule Job 定时触发，执行结果落在 pipeline_runs。
CREATE TABLE pipeline_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,             -- pipeline.Definition JSON
    cron_expr TEXT NOT NULL DEFAULT '',  -- 空 = 仅手动触发
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT NOT NULL DEFAULT '',   -- YYYY-MM-DD HH:mm，分钟级防重
    last_status TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_pipeline_templates_owner ON pipeline_templates(org_id, user_id, id DESC);
CREATE INDEX idx_pipeline_templates_enabled ON pipeline_templates(enabled);
