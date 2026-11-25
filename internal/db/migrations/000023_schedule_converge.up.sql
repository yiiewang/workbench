-- 000023_schedule_converge.up.sql
-- 调度收敛：cron/启停/最近执行状态统一归 schedule_triggers，ai_jobs 只留执行定义。
-- 1) 通用触发规则加租约，解决长任务跨分钟重复触发；
-- 2) 从 ai_jobs 回填已有 cron 配置（target_type='job'）；
-- 3) 重建 ai_jobs，删掉 4 个调度列。

ALTER TABLE schedule_triggers ADD COLUMN lease_until TEXT NOT NULL DEFAULT '';

-- 回填：仅已配置 cron 的任务产生规则；全局任务（user_id IS NULL）用 0/0 标记。
INSERT INTO schedule_triggers (org_id, user_id, target_type, target_id, cron_expr, enabled, last_run_at, last_status)
SELECT COALESCE(org_id, 0), COALESCE(user_id, 0), 'job', id, cron_expr, enabled,
       COALESCE(last_run_at, ''), COALESCE(last_status, '')
FROM ai_jobs
WHERE COALESCE(cron_expr, '') <> '';

CREATE TABLE ai_jobs_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,                -- NULL = 全局公共任务
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
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

INSERT INTO ai_jobs_new (id, user_id, org_id, name, prompt, target_type, target_json,
                         allow_write, context_json, notify_json, created_at, updated_at)
SELECT id, user_id, org_id, name, prompt,
       COALESCE(target_type, 'ai'), COALESCE(target_json, ''), COALESCE(allow_write, '[]'),
       COALESCE(context_json, '{}'), COALESCE(notify_json, '{}'),
       COALESCE(created_at, datetime('now')), COALESCE(updated_at, datetime('now'))
FROM ai_jobs;

DROP TABLE ai_jobs;
ALTER TABLE ai_jobs_new RENAME TO ai_jobs;
CREATE INDEX idx_ai_jobs_owner ON ai_jobs(org_id, user_id);
