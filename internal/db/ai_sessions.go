package db

// AI 对话会话历史 CRUD（ai_sessions 表）。
// messages 为前端 UI 消息全量快照 JSON（含 reasoning/files 展示字段），
// 按 org+user 隔离；列表查询不取 messages 列，避免大载荷。

import (
	"context"
	"database/sql"
	"encoding/json"
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

// sqliteTimeToRFC3339 把 SQLite datetime('now') 的 "2006-01-02 15:04:05"（UTC）
// 转为 RFC3339，前端 new Date() 跨浏览器可靠解析。
func sqliteTimeToRFC3339(s string) string {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

// ListAISessions 用户最近会话列表（不含 messages），按 updated_at 倒序
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
	return out, nil
}

// CreateAISession 新建会话，返回 id
func (d *DB) CreateAISession(ctx context.Context, orgID, userID int64, title, messagesJSON string) (int64, error) {
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO ai_sessions (user_id, org_id, title, messages) VALUES (?, ?, ?, ?)`,
		userID, orgID, title, messagesJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetAISession 会话详情（含 messages）；不存在返回 ok=false
func (d *DB) GetAISession(ctx context.Context, orgID, userID, id int64) (AISession, bool, error) {
	var s AISession
	// messages 列 TEXT：sqlite 驱动返回 string，json.RawMessage 不能直接 Scan，先收 string
	var messages string
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, title, messages, created_at, updated_at FROM ai_sessions
		 WHERE org_id = ? AND user_id = ? AND id = ?`,
		orgID, userID, id).
		Scan(&s.ID, &s.Title, &messages, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	s.Messages = json.RawMessage(messages)
	s.CreatedAt = sqliteTimeToRFC3339(s.CreatedAt)
	s.UpdatedAt = sqliteTimeToRFC3339(s.UpdatedAt)
	return s, true, nil
}

// UpdateAISession 更新会话消息快照，顺带刷新 updated_at。
// title 传空串时保留原标题（COALESCE + NULLIF），调用方无需先查旧值。
func (d *DB) UpdateAISession(ctx context.Context, orgID, userID, id int64, title, messagesJSON string) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE ai_sessions SET title = COALESCE(NULLIF(?, ''), title), messages = ?, updated_at = datetime('now')
		 WHERE org_id = ? AND user_id = ? AND id = ?`,
		title, messagesJSON, orgID, userID, id)
	return err
}

// DeleteAISession 删除会话
func (d *DB) DeleteAISession(ctx context.Context, orgID, userID, id int64) error {
	_, err := d.conn.ExecContext(ctx,
		`DELETE FROM ai_sessions WHERE org_id = ? AND user_id = ? AND id = ?`,
		orgID, userID, id)
	return err
}
