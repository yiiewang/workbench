-- 000024_ai_job_runs_detail.down.sql
ALTER TABLE ai_job_runs DROP COLUMN finished_at;
ALTER TABLE ai_job_runs DROP COLUMN duration_ms;
ALTER TABLE ai_job_runs DROP COLUMN err_msg;
ALTER TABLE ai_job_runs DROP COLUMN steps_summary;
ALTER TABLE ai_job_runs DROP COLUMN pipeline_run_id;
ALTER TABLE ai_job_runs DROP COLUMN target_type;
