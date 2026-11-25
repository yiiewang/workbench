-- 000025_ai_jobs_retry.up.sql
-- job 级失败重试次数（0-3）。仅对不含写步骤的执行生效（见 triggerJob）。
ALTER TABLE ai_jobs ADD COLUMN retry INTEGER NOT NULL DEFAULT 0;
