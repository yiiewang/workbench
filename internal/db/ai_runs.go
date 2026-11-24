package db

// AI 对话后台运行 CRUD（ai_runs 表）。
// 一次 /api/ai/chat 对应一条 run：请求上下文断开后 Agent 循环继续跑完，
// 状态、结构化事件与最终回复由后端落库，按 org+user 隔离。

import (
	"context"
	"database/sql"
	"encoding/json"
)

// AIRun 后台运行状态（Events 列表接口不返回，详情接口才填充）
type AIRun struct {
	ID        string          `json:"id"`
	QAId      string          `json:"qaId"`
	SessionId string          `json:"sessionId"`
	Status    string          `json:"status"` // running | done | failed
	Events    json.RawMessage `json:"events,omitempty"`
	Result    string          `json:"result"`
	Error     string          `json:"error"`
	StartedAt string          `json:"startedAt"`
	UpdatedAt string          `json:"updatedAt"`
}

// CreateAIRun 启动一条运行记录（status=running）
func (d *DB) CreateAIRun(ctx context.Context, orgID, userID int64, id, qaID, sessionID string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO ai_runs (id, org_id, user_id, qa_id, session_id, status) VALUES (?, ?, ?, ?, ?, 'running')`,
		id, orgID, userID, qaID, sessionID)
	return err
}

// SaveAIRun 覆盖状态/事件/结果（单条 run 由单个循环串行写，无并发覆盖）
func (d *DB) SaveAIRun(ctx context.Context, orgID, userID int64, r AIRun) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE ai_runs SET status = ?, events = ?, result = ?, err_msg = ?, updated_at = datetime('now')
		 WHERE id = ? AND org_id = ? AND user_id = ?`,
		r.Status, string(r.Events), r.Result, r.Error, r.ID, orgID, userID)
	return err
}

// GetAIRun 单条运行详情（含事件）；不存在返回 ok=false
func (d *DB) GetAIRun(ctx context.Context, orgID, userID int64, id string) (AIRun, bool, error) {
	var r AIRun
	var events, startedAt, updatedAt string
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, qa_id, session_id, status, events, result, err_msg, started_at, updated_at
		 FROM ai_runs WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID).
		Scan(&r.ID, &r.QAId, &r.SessionId, &r.Status, &events, &r.Result, &r.Error, &startedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.Events = json.RawMessage(events)
	r.StartedAt = sqliteTimeToRFC3339(startedAt)
	r.UpdatedAt = sqliteTimeToRFC3339(updatedAt)
	return r, true, nil
}

// ListAIRuns 最近运行列表（不含 events，避免大载荷；详情走 GetAIRun）
func (d *DB) ListAIRuns(ctx context.Context, orgID, userID int64, limit int) ([]AIRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, qa_id, session_id, status, result, err_msg, started_at, updated_at
		 FROM ai_runs WHERE org_id = ? AND user_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`,
		orgID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AIRun
	for rows.Next() {
		var r AIRun
		var startedAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.QAId, &r.SessionId, &r.Status, &r.Result, &r.Error, &startedAt, &updatedAt); err != nil {
			return nil, err
		}
		r.StartedAt = sqliteTimeToRFC3339(startedAt)
		r.UpdatedAt = sqliteTimeToRFC3339(updatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}
