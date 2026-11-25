-- 000023_schedule_converge.down.sql
-- 回滚：加回 ai_jobs 的 4 个调度列并从 job 规则反向回填（多规则取最近一条），
-- 再删除 job 规则与租约列。pipeline 规则不受影响。

ALTER TABLE ai_jobs ADD COLUMN cron_expr TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_jobs ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE ai_jobs ADD COLUMN last_run_at TEXT DEFAULT '';
ALTER TABLE ai_jobs ADD COLUMN last_status TEXT DEFAULT '';

UPDATE ai_jobs SET
    cron_expr = COALESCE((SELECT t.cron_expr FROM schedule_triggers t
                          WHERE t.target_type = 'job' AND t.target_id = ai_jobs.id
                          ORDER BY t.id DESC LIMIT 1), ''),
    enabled = COALESCE((SELECT t.enabled FROM schedule_triggers t
                        WHERE t.target_type = 'job' AND t.target_id = ai_jobs.id
                        ORDER BY t.id DESC LIMIT 1), 1),
    last_run_at = COALESCE((SELECT t.last_run_at FROM schedule_triggers t
                            WHERE t.target_type = 'job' AND t.target_id = ai_jobs.id
                            ORDER BY t.id DESC LIMIT 1), ''),
    last_status = COALESCE((SELECT t.last_status FROM schedule_triggers t
                            WHERE t.target_type = 'job' AND t.target_id = ai_jobs.id
                            ORDER BY t.id DESC LIMIT 1), '');

DELETE FROM schedule_triggers WHERE target_type = 'job';
ALTER TABLE schedule_triggers DROP COLUMN lease_until;
