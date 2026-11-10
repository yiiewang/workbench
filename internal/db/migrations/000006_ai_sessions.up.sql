-- 000006_ai_sessions.up.sql
-- AI 对话会话历史（session 持久化：用户可回到历史会话继续沟通）
-- messages 为 JSON 数组：[{role, content, files?, reasoning?}]（前端 UI 消息全量快照）
-- 外键与 tasks 现状一致：单外键 user_id（000003 后 users 无 (org_id,id) 复合唯一）
CREATE TABLE ai_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    messages TEXT NOT NULL DEFAULT '[]',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_ai_sessions_user ON ai_sessions(org_id, user_id, updated_at);
