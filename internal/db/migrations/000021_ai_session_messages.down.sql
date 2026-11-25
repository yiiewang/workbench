-- 000021_ai_session_messages.down.sql
-- 回滚：重建 ai_sessions 加回 messages 列，按 id 顺序把子表消息聚合回 JSON 数组。
-- 同 up：重建期间关闭外键，否则带孤儿会话的库回滚时复制会被外键拒绝。
PRAGMA foreign_keys=OFF;
CREATE TABLE ai_sessions_legacy (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    messages TEXT NOT NULL DEFAULT '[]',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
INSERT INTO ai_sessions_legacy (id, user_id, org_id, title, created_at, updated_at)
SELECT id, user_id, org_id, title, created_at, updated_at FROM ai_sessions;

-- 有序子查询 + group_concat 保序；json_patch 把 role/content 合回展示字段对象。
UPDATE ai_sessions_legacy SET messages = COALESCE((
    SELECT '[' || group_concat(msg) || ']' FROM (
        SELECT json_patch(json_object('role', m.role, 'content', m.content), m.extras) AS msg
        FROM ai_session_messages m
        WHERE m.session_id = ai_sessions_legacy.id
        ORDER BY m.id
    )
), '[]');

DROP TABLE ai_sessions;
ALTER TABLE ai_sessions_legacy RENAME TO ai_sessions;
CREATE INDEX idx_ai_sessions_user ON ai_sessions(org_id, user_id, updated_at);
DROP TABLE ai_session_messages;
PRAGMA foreign_keys=ON;
