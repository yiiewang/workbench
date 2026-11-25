-- 000022_ai_jobs_target.up.sql
-- 定时任务执行对象多态化：ai_jobs 从单一 prompt 扩展为 target（ai/tool/interface/pipeline）。
-- 本期只加列并回填，cron 仍留在 ai_jobs；调度收敛见后续迁移。
ALTER TABLE ai_jobs ADD COLUMN target_type TEXT NOT NULL DEFAULT 'ai';
ALTER TABLE ai_jobs ADD COLUMN target_json TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_jobs ADD COLUMN allow_write TEXT NOT NULL DEFAULT '[]';
ALTER TABLE ai_jobs ADD COLUMN context_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE ai_jobs ADD COLUMN notify_json TEXT NOT NULL DEFAULT '{}';

-- 兼容回填：存量任务等价于 {"type":"ai","prompt":prompt}
UPDATE ai_jobs
SET target_json = json_object('type', 'ai', 'prompt', prompt)
WHERE target_json = '';
