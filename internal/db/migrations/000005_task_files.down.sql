-- 000005_task_files.down.sql
-- 回滚：去掉 tasks.files 列（SQLite 需重建表）

PRAGMA foreign_keys=OFF;

CREATE TABLE tasks_new (
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
    PRIMARY KEY (id, user_id, org_id),
    FOREIGN KEY (org_id, user_id) REFERENCES users(org_id, id) ON DELETE CASCADE
);
INSERT INTO tasks_new (id, user_id, org_id, title, content, status, priority, scheduled, due,
    progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at)
SELECT id, user_id, org_id, title, content, status, priority, scheduled, due,
    progress, assignee, postponed_count, auto_postponed, sort_order, created_at, updated_at
FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

PRAGMA foreign_keys=ON;
