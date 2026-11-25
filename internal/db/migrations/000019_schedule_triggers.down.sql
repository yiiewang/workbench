-- 000019_schedule_triggers.down.sql
-- 回滚只删通用触发表；此时 000018 的模板表仍带 cron 列，配置不会丢。
DROP TABLE IF EXISTS schedule_triggers;
