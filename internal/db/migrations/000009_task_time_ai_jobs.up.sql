-- 000009_task_time_ai_jobs.up.sql
-- 1. tasks 加精确时间列（HH:mm，空 = 无精确时间，仅按日期粒度提醒）：
--    scheduled_time = 任务开始时刻（到点发"开始提醒"）
--    due_time       = 截止时刻（到点且未完成发"到期提醒"）
ALTER TABLE tasks ADD COLUMN scheduled_time TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN due_time TEXT NOT NULL DEFAULT '';

-- 2. AI 定时任务表：cron 触发 → 执行 prompt → 结果邮件通知。
--    两类任务共用一表：
--      user_id 非空 = 私有任务（归属用户，/jobs 页面自建自管）
--      user_id IS NULL = 全局公共任务（只存一条；执行时对所有
--                        有邮箱的用户逐个以本人身份展开——各自任务数据、
--                        各自投递，避免按用户复制记录导致数据量膨胀）
CREATE TABLE ai_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,                -- NULL = 全局公共任务
    org_id INTEGER NOT NULL DEFAULT 0,
    name TEXT NOT NULL,             -- 任务名（通知邮件主题前缀）
    cron_expr TEXT NOT NULL,        -- 5 段 cron（分 时 日 月 周）
    prompt TEXT NOT NULL,           -- 到点执行的内容指令
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT DEFAULT '',    -- 最近一次触发时刻（本地时间 YYYY-MM-DD HH:mm）
    last_status TEXT DEFAULT '',    -- ok / error: ...
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_jobs_enabled ON ai_jobs(enabled);
