-- 000011_ai_job_runs.up.sql
-- AI 定时任务执行历史：每次触发留痕（前端任务详情页可查看）。
-- 注意：部署此 migration 前若手动执行过"全局任务替换"SQL 且 ai_jobs 里
-- 还残留按用户复制的旧默认任务（name='每日工作总结' 且 user_id 非空），
-- 请先清理：DELETE FROM ai_jobs WHERE user_id IS NOT NULL AND name='每日工作总结';
CREATE TABLE ai_job_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id INTEGER NOT NULL,
    ran_at TEXT DEFAULT (datetime('now', 'localtime')),
    status TEXT NOT NULL,           -- ok / error: ...
    output TEXT DEFAULT '',         -- 执行输出（截断存储，前 4KB）
    FOREIGN KEY (job_id) REFERENCES ai_jobs(id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_job_runs_job ON ai_job_runs(job_id, id DESC);
