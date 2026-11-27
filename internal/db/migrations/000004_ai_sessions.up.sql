-- 000004_ai_sessions.up.sql
-- AI 对话：会话 / 消息 / 后台 run（SSE 事件与断连恢复）。
CREATE TABLE ai_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_ai_sessions_user ON ai_sessions(org_id, user_id, updated_at);

CREATE TABLE ai_session_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL,
    client_msg_id TEXT NOT NULL,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    extras TEXT NOT NULL DEFAULT '{}',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    UNIQUE (session_id, client_msg_id)
);

CREATE INDEX idx_ai_session_messages_order ON ai_session_messages(session_id, id);
CREATE INDEX idx_ai_session_messages_owner ON ai_session_messages(org_id, user_id, session_id);

CREATE TABLE ai_runs (
    id TEXT PRIMARY KEY,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    qa_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    events TEXT NOT NULL DEFAULT '[]',
    result TEXT NOT NULL DEFAULT '',
    err_msg TEXT NOT NULL DEFAULT '',
    started_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_ai_runs_owner ON ai_runs(org_id, user_id, started_at DESC);
