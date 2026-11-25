-- 000021_ai_session_messages.up.sql
-- AI 会话消息：从 ai_sessions.messages 的「整份 JSON 快照」改为一行一消息。
-- 前端按 client_msg_id 追加/UPSERT，流式增量只更新最后一条；
-- 多 tab 各写各的消息，不再互相全量覆盖，也不再有 256KB 单列上限。
CREATE TABLE ai_session_messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id    INTEGER NOT NULL,
    client_msg_id TEXT    NOT NULL,
    org_id        INTEGER NOT NULL,
    user_id       INTEGER NOT NULL,
    role          TEXT    NOT NULL,
    content       TEXT    NOT NULL DEFAULT '',
    extras        TEXT    NOT NULL DEFAULT '{}',  -- reasoning/toolCalls/ragHits/steps/usage/files 展示态字段
    created_at    TEXT DEFAULT (datetime('now')),
    updated_at    TEXT DEFAULT (datetime('now')),
    UNIQUE (session_id, client_msg_id)
);
CREATE INDEX idx_ai_session_messages_order ON ai_session_messages(session_id, id);
CREATE INDEX idx_ai_session_messages_owner ON ai_session_messages(org_id, user_id, session_id);

-- 存量会话：JSON 数组按下标展开。client_msg_id 用 idx:<下标>，与消息
-- 缺少 clientMsgId 时的兜底 id 一致，所以旧会话继续保存不会产生重复行。
-- role/content 单独成列，其余展示字段留在 extras。
INSERT INTO ai_session_messages (session_id, client_msg_id, org_id, user_id, role, content, extras, created_at, updated_at)
SELECT s.id,
       'idx:' || je.key,
       s.org_id, s.user_id,
       COALESCE(json_extract(je.value, '$.role'), ''),
       COALESCE(json_extract(je.value, '$.content'), ''),
       COALESCE(json_remove(je.value, '$.role', '$.content'), '{}'),
       s.created_at, s.updated_at
FROM ai_sessions s, json_each(s.messages) je
ORDER BY s.id, CAST(je.key AS INTEGER);

-- 去掉 ai_sessions.messages 列（SQLite 建新表 → 复制 → 删旧表 → 改名，同 000020）。
-- 子表刻意不声明外键：连接串 _foreign_keys=on，DROP TABLE 父表会级联删掉刚导入的消息；
-- 会话删除改由 db.DeleteAISession 在事务里显式清理子表。
-- 重建期间关闭外键：历史库可能有 user_id 指向已删除用户的孤儿会话，
-- 复制进带 FK 的新表会被拒（FOREIGN KEY constraint failed），留下 dirty version 21。
-- PRAGMA 必须在事务外执行，故 golang-migrate 用 NoTxWrap=true（见 migrate.go）。
PRAGMA foreign_keys=OFF;
CREATE TABLE ai_sessions_v2 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
INSERT INTO ai_sessions_v2 (id, user_id, org_id, title, created_at, updated_at)
SELECT id, user_id, org_id, title, created_at, updated_at FROM ai_sessions;
DROP TABLE ai_sessions;
ALTER TABLE ai_sessions_v2 RENAME TO ai_sessions;
CREATE INDEX idx_ai_sessions_user ON ai_sessions(org_id, user_id, updated_at);
PRAGMA foreign_keys=ON;
