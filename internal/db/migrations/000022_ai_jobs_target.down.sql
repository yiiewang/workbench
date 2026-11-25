-- 000022_ai_jobs_target.down.sql
-- 回滚只删新增列；prompt/cron 等原列未动，存量行为可完整恢复。
ALTER TABLE ai_jobs DROP COLUMN notify_json;
ALTER TABLE ai_jobs DROP COLUMN context_json;
ALTER TABLE ai_jobs DROP COLUMN allow_write;
ALTER TABLE ai_jobs DROP COLUMN target_json;
ALTER TABLE ai_jobs DROP COLUMN target_type;
