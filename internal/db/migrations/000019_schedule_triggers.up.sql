-- 000019_schedule_triggers.up.sql
-- 通用定时触发规则：调度领域自己的持久化对象。schedule 只知道
-- 「某用户的某类目标对象、按 cron 触发」，target_type/target_id 由
-- server 装配层解释并分发，schedule 包不需要认识任何业务对象。
-- 依赖 000018：从 pipeline_templates 回填已有的 cron 配置，避免解耦时丢数据。
CREATE TABLE schedule_triggers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    target_type TEXT NOT NULL,             -- 'pipeline_template' 等，由装配层注册 handler
    target_id INTEGER NOT NULL,            -- 目标对象在各自领域内的 id
    cron_expr TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT NOT NULL DEFAULT '',  -- YYYY-MM-DD HH:mm，分钟级防重
    last_status TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_schedule_triggers_owner ON schedule_triggers(org_id, user_id, id DESC);
CREATE INDEX idx_schedule_triggers_enabled ON schedule_triggers(enabled);
CREATE INDEX idx_schedule_triggers_target ON schedule_triggers(target_type, target_id);

-- 回填：仅把「已配置 cron」的模板转成触发规则（纯手动模板不产生规则），
-- 连同启用状态与最近执行状态一起带走；000020 才会删除模板表里的旧列。
INSERT INTO schedule_triggers (org_id, user_id, target_type, target_id, cron_expr, enabled, last_run_at, last_status)
SELECT org_id, user_id, 'pipeline_template', id, cron_expr, enabled, last_run_at, last_status
FROM pipeline_templates
WHERE cron_expr <> '';
