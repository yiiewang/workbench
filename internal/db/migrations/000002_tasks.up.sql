-- 000002_tasks.up.sql
-- Todo 任务：按 (user_id, org_id) 隔离，主键为 (id, user_id, org_id)。
CREATE TABLE tasks (
    id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'todo',
    priority TEXT NOT NULL DEFAULT 'medium',
    scheduled TEXT NOT NULL DEFAULT '',
    due TEXT NOT NULL DEFAULT '',
    progress INTEGER DEFAULT 0,
    assignee TEXT NOT NULL DEFAULT '',
    postponed_count INTEGER DEFAULT 0,
    auto_postponed INTEGER DEFAULT 0,
    sort_order INTEGER DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    files TEXT NOT NULL DEFAULT '[]',
    scheduled_time TEXT NOT NULL DEFAULT '',
    due_time TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, user_id, org_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
