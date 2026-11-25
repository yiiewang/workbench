-- 000017_pipeline_runs.up.sql
-- 声明式编排运行记录：一次 pipeline 执行一条，逐步落库状态/输出，
-- 供断连后查询（对话、HTTP、cron 三种入口共用）。
CREATE TABLE pipeline_runs (
    id TEXT PRIMARY KEY,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    definition TEXT NOT NULL DEFAULT '{}',  -- pipeline.Definition JSON（可复用为模板）
    status TEXT NOT NULL,                   -- running / done / failed
    steps TEXT NOT NULL DEFAULT '[]',       -- []pipeline.StepResult JSON
    err_msg TEXT NOT NULL DEFAULT '',
    started_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_pipeline_runs_owner ON pipeline_runs(org_id, user_id, started_at DESC);
