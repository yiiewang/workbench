-- 000024_ai_job_runs_detail.up.sql
-- 执行历史联动编排：记录 target 类型、pipeline run id、步骤摘要与耗时。
ALTER TABLE ai_job_runs ADD COLUMN target_type TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_job_runs ADD COLUMN pipeline_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_job_runs ADD COLUMN steps_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_job_runs ADD COLUMN err_msg TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_job_runs ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_job_runs ADD COLUMN finished_at TEXT NOT NULL DEFAULT '';
