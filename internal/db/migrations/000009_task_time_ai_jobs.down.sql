-- 000009_task_time_ai_jobs.down.sql
DROP TABLE IF EXISTS ai_jobs;
ALTER TABLE tasks DROP COLUMN due_time;
ALTER TABLE tasks DROP COLUMN scheduled_time;
