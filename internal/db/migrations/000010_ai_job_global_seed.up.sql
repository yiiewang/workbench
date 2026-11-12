-- 000010_ai_job_global_seed.up.sql
-- 1. ai_jobs 表结构对齐：user_id 改为可空（NULL = 全局公共任务）。
--    SQLite 不能 ALTER 列约束，走重建表（数据原样搬回，幂等：
--    对已是目标结构的库重建后数据不变）。
--    背景：早期 000009 部署的 user_id 为 NOT NULL，无法插入全局行。
-- 2. 预置全局公共任务：每日工作总结（一条记录，执行时对所有
--    有邮箱的用户逐个以本人身份展开——各自任务数据、各自投递，
--    避免按用户复制记录导致数据量膨胀）。幂等：已存在全局任务则跳过。

CREATE TABLE ai_jobs_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,                -- NULL = 全局公共任务
    org_id INTEGER NOT NULL DEFAULT 0,
    name TEXT NOT NULL,
    cron_expr TEXT NOT NULL,
    prompt TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT DEFAULT '',
    last_status TEXT DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
INSERT INTO ai_jobs_new (id, user_id, org_id, name, cron_expr, prompt, enabled, last_run_at, last_status, created_at, updated_at)
SELECT id, user_id, org_id, name, cron_expr, prompt, enabled,
       COALESCE(last_run_at, ''), COALESCE(last_status, ''), created_at, updated_at
FROM ai_jobs;
DROP TABLE ai_jobs;
ALTER TABLE ai_jobs_new RENAME TO ai_jobs;
CREATE INDEX idx_ai_jobs_enabled ON ai_jobs(enabled);

INSERT INTO ai_jobs (user_id, org_id, name, cron_expr, prompt, enabled)
SELECT NULL, 0, '每日工作总结', '0 18 * * *',
       '总结我今天完成的任务和进展，生成一份简短的工作日报，指出明天应优先处理的事项。',
       1
WHERE NOT EXISTS (SELECT 1 FROM ai_jobs WHERE user_id IS NULL);
