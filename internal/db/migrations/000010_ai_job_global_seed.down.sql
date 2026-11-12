-- 000010_ai_job_global_seed.down.sql
-- 回滚：删除全局公共任务（表结构差异——user_id 可空——无害，不重建）
DELETE FROM ai_jobs WHERE user_id IS NULL;
