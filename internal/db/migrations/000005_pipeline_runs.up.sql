-- 000005_pipeline_runs.up.sql
-- 编排运行：Definition 与逐步快照，HTTP 直调 / 对话工具 / 定时任务共用。
CREATE TABLE pipeline_runs (
    id TEXT PRIMARY KEY,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    definition TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    steps TEXT NOT NULL DEFAULT '[]',
    err_msg TEXT NOT NULL DEFAULT '',
    started_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_pipeline_runs_owner ON pipeline_runs(org_id, user_id, started_at DESC);
