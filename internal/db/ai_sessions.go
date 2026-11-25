package db

// AI 对话会话历史（ai_sessions + ai_session_messages）。
// 消息改为一行一条，前端按 clientMsgId 追加/UPSERT，不再整份快照覆盖；
// 按 org+user 隔离；列表查询不取消息体，避免大载荷。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AISession AI 会话（Messages 仅详情接口填充，列表为空）
type AISession struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	Messages  json.RawMessage `json:"messages"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

// AISessionMessage 一条会话消息。ClientMsgID 由前端生成，缺失时回退为
// 数组下标 idx:<n>（兼容旧快照与 000021 导入的存量数据）。Extras 保存
// role/content 之外的展示态字段（reasoning/toolCalls/steps/usage/ragHits/files）。
type AISessionMessage struct {
	ClientMsgID string          `json:"clientMsgId"`
	Role        string          `json:"role"`
	Content     string          `json:"content"`
	Extras      json.RawMessage `json:"extras"`
}

const upsertSessionMessageSQL = `INSERT INTO ai_session_messages
    (session_id, client_msg_id, org_id, user_id, role, content, extras, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
    ON CONFLICT(session_id, client_msg_id) DO UPDATE SET
        role = excluded.role, content = excluded.content,
        extras = excluded.extras, updated_at = datetime('now')`

// sqlQuerier 让 *sql.DB 与 *sql.Tx 共用同一组读写函数
type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sqliteTimeToRFC3339 把 SQLite datetime('now') 的 "2006-01-02 15:04:05"（UTC）
// 转为 RFC3339，前端 new Date() 跨浏览器可靠解析。
func sqliteTimeToRFC3339(s string) string {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

// parseSessionMessages 把整份消息数组拆成行级消息；缺少 clientMsgId 时用下标兜底。
func parseSessionMessages(raw string) ([]AISessionMessage, error) {
	if raw == "" {
		raw = "[]"
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	out := make([]AISessionMessage, 0, len(items))
	for i, item := range items {
		var m AISessionMessage
		if v, ok := item["role"]; ok {
			_ = json.Unmarshal(v, &m.Role)
		}
		if v, ok := item["content"]; ok {
			_ = json.Unmarshal(v, &m.Content)
		}
		if v, ok := item["clientMsgId"]; ok {
			_ = json.Unmarshal(v, &m.ClientMsgID)
		}
		if m.ClientMsgID == "" {
			m.ClientMsgID = fmt.Sprintf("idx:%d", i)
		}
		delete(item, "role")
		delete(item, "content")
		delete(item, "clientMsgId")
		extras, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		m.Extras = extras
		out = append(out, m)
	}
	return out, nil
}

// readSessionMessages 按写入顺序把行级消息拼回前端展示态数组，顺带带回 clientMsgId。
func readSessionMessages(ctx context.Context, q sqlQuerier, sessionID, orgID, userID int64) (json.RawMessage, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT client_msg_id, role, content, extras FROM ai_session_messages
		 WHERE session_id = ? AND org_id = ? AND user_id = ? ORDER BY id`,
		sessionID, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs := make([]json.RawMessage, 0)
	for rows.Next() {
		var clientID, role, content, extras string
		if err := rows.Scan(&clientID, &role, &content, &extras); err != nil {
			return nil, err
		}
		obj := map[string]any{}
		if extras != "" {
			_ = json.Unmarshal([]byte(extras), &obj)
		}
		obj["role"] = role
		obj["content"] = content
		obj["clientMsgId"] = clientID
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(msgs)
}

// ListAISessions 用户最近会话列表（不含消息体），按 updated_at 倒序
func (d *DB) ListAISessions(ctx context.Context, orgID, userID int64, limit int) ([]AISession, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, title, created_at, updated_at FROM ai_sessions
		 WHERE org_id = ? AND user_id = ? ORDER BY updated_at DESC LIMIT ?`,
		orgID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AISession
	for rows.Next() {
		var s AISession
		if err := rows.Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.CreatedAt = sqliteTimeToRFC3339(s.CreatedAt)
		s.UpdatedAt = sqliteTimeToRFC3339(s.UpdatedAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

// CreateAISession 新建会话并写入首批消息，返回 id
func (d *DB) CreateAISession(ctx context.Context, orgID, userID int64, title, messagesJSON string) (int64, error) {
	msgs, err := parseSessionMessages(messagesJSON)
	if err != nil {
		return 0, err
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO ai_sessions (user_id, org_id, title) VALUES (?, ?, ?)`,
		userID, orgID, title)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, m := range msgs {
		if _, err := tx.ExecContext(ctx, upsertSessionMessageSQL,
			id, m.ClientMsgID, orgID, userID, m.Role, m.Content, string(m.Extras)); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// GetAISession 会话详情（含消息）；不存在返回 ok=false
func (d *DB) GetAISession(ctx context.Context, orgID, userID, id int64) (AISession, bool, error) {
	var s AISession
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, title, created_at, updated_at FROM ai_sessions
		 WHERE org_id = ? AND user_id = ? AND id = ?`,
		orgID, userID, id).
		Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	msgs, err := readSessionMessages(ctx, d.conn, s.ID, orgID, userID)
	if err != nil {
		return s, false, err
	}
	s.Messages = msgs
	s.CreatedAt = sqliteTimeToRFC3339(s.CreatedAt)
	s.UpdatedAt = sqliteTimeToRFC3339(s.UpdatedAt)
	return s, true, nil
}

// UpsertAISessionMessages 追加/更新一批消息（消息级写入接口）。
// truncateAfter 非空时先删除该 clientMsgId 之后的所有消息，用于 regenerate
// 丢弃旧回复后重新生成；此时 messages 只应包含截断位置之后的新消息。
func (d *DB) UpsertAISessionMessages(ctx context.Context, orgID, userID, sessionID int64, messagesJSON, truncateAfter string) error {
	msgs, err := parseSessionMessages(messagesJSON)
	if err != nil {
		return err
	}
	return d.upsertSessionMessages(ctx, orgID, userID, sessionID, msgs, truncateAfter)
}

// upsertSessionMessages 在一个事务里截断、刷新会话并逐条 UPSERT 消息。
// 新增行与更新行共用同一条语句，重试天然幂等；不同 tab 各写各的消息互不覆盖。
func (d *DB) upsertSessionMessages(ctx context.Context, orgID, userID, sessionID int64, msgs []AISessionMessage, truncateAfter string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var owned int
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM ai_sessions WHERE id = ? AND org_id = ? AND user_id = ?`,
		sessionID, orgID, userID).Scan(&owned); err != nil {
		return err
	}
	if truncateAfter != "" {
		var afterID int64
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM ai_session_messages WHERE session_id = ? AND client_msg_id = ?`,
			sessionID, truncateAfter).Scan(&afterID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM ai_session_messages WHERE session_id = ? AND id > ?`,
			sessionID, afterID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE ai_sessions SET updated_at = datetime('now')
		 WHERE id = ? AND org_id = ? AND user_id = ?`,
		sessionID, orgID, userID); err != nil {
		return err
	}
	for _, m := range msgs {
		if _, err := tx.ExecContext(ctx, upsertSessionMessageSQL,
			sessionID, m.ClientMsgID, orgID, userID, m.Role, m.Content, string(m.Extras)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteAISession 删除会话：子表无外键，需在同一事务里显式清理消息行
func (d *DB) DeleteAISession(ctx context.Context, orgID, userID, id int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ai_session_messages WHERE session_id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ai_sessions WHERE org_id = ? AND user_id = ? AND id = ?`,
		orgID, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}
