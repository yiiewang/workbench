-- 000006_jobs.up.sql
-- 定时任务与调度：jobs（执行定义）/ job_runs（执行历史）/ schedule_triggers（cron 规则）。
-- jobs.user_id 为 NULL 表示全局公共任务。
CREATE TABLE jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    org_id INTEGER NOT NULL DEFAULT 0,
    name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT 'ai',
    target_json TEXT NOT NULL DEFAULT '',
    allow_write TEXT NOT NULL DEFAULT '[]',
    context_json TEXT NOT NULL DEFAULT '{}',
    notify_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    retry INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_jobs_owner ON jobs(org_id, user_id);

CREATE TABLE job_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id INTEGER NOT NULL,
    ran_at TEXT DEFAULT (datetime('now', 'localtime')),
    status TEXT NOT NULL,
    output TEXT DEFAULT '',
    target_type TEXT NOT NULL DEFAULT '',
    pipeline_run_id TEXT NOT NULL DEFAULT '',
    steps_summary TEXT NOT NULL DEFAULT '',
    err_msg TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    finished_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE INDEX idx_job_runs_job ON job_runs(job_id, id DESC);

CREATE TABLE schedule_triggers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    target_type TEXT NOT NULL,
    target_id INTEGER NOT NULL,
    cron_expr TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT NOT NULL DEFAULT '',
    last_status TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    lease_until TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_schedule_triggers_owner ON schedule_triggers(org_id, user_id, id DESC);
CREATE INDEX idx_schedule_triggers_enabled ON schedule_triggers(enabled);
CREATE INDEX idx_schedule_triggers_target ON schedule_triggers(target_type, target_id);
