-- 000012_drop_visit_logs.down.sql
-- 恢复 visit_logs 表结构（与 000001 baseline 一致；历史数据不可恢复）
CREATE TABLE IF NOT EXISTS visit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    visitor_id TEXT NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    org_id INTEGER NOT NULL DEFAULT 0,
    user_agent TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    status_code INTEGER DEFAULT 200,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_visit_visitor ON visit_logs(visitor_id);
CREATE INDEX IF NOT EXISTS idx_visit_path ON visit_logs(path);
CREATE INDEX IF NOT EXISTS idx_visit_org ON visit_logs(org_id);
