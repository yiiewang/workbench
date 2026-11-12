-- 000008_user_email_notify.up.sql
-- 1. users 加 email 列（通知投递目标，个人资料/用户管理可编辑，空 = 不通知）
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';

-- 2. 通知去重表：同 topic + dedup_key 当天（notify_date）只投递一次
--    （Todo 到期提醒：dedup_key = 任务 ID，截止日当天与逾期每天各提醒一次）
CREATE TABLE notify_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    notify_date TEXT NOT NULL,
    sent_at TEXT DEFAULT (datetime('now')),
    UNIQUE (topic, dedup_key, notify_date)
);
