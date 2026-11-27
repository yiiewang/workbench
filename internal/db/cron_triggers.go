package db

// 通用定时触发规则 CRUD（schedule_triggers 表）。
// 这张表属于调度领域：记录「某用户的某类目标对象按 cron 触发」，
// target_type / target_id 只是不透明标识，db 层不引用任何业务对象类型。
// 具体目标是否存在、归谁所有，由 server 装配层在写入前校验。

import (
	"context"
	"database/sql"
)

// CronTrigger 一条触发规则（列表与详情同形）
type CronTrigger struct {
	ID         int64  `json:"id"`
	TargetType string `json:"targetType"`
	TargetID   int64  `json:"targetId"`
	CronExpr   string `json:"cronExpr"`
	Enabled    bool   `json:"enabled"`
	LastRunAt  string `json:"lastRunAt"`
	LastStatus string `json:"lastStatus"`
	FinishedAt string `json:"finishedAt"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// CronTriggerWithOwner 带归属（cron 每分钟扫描用，避免回表）
type CronTriggerWithOwner struct {
	CronTrigger
	OrgID  int64
	UserID int64
}

func scanCronTrigger(s interface{ Scan(...any) error }) (CronTrigger, error) {
	var t CronTrigger
	var enabled int
	err := s.Scan(&t.ID, &t.TargetType, &t.TargetID, &t.CronExpr, &enabled,
		&t.LastRunAt, &t.LastStatus, &t.FinishedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	t.Enabled = enabled != 0
	t.CreatedAt = sqliteTimeToRFC3339(t.CreatedAt)
	t.UpdatedAt = sqliteTimeToRFC3339(t.UpdatedAt)
	return t, nil
}

// ListCronTriggers 用户的触发规则；targetType / targetID 为可选过滤（空串 / 0 表示不过滤）
func (d *DB) ListCronTriggers(ctx context.Context, orgID, userID int64, targetType string, targetID int64, limit int) ([]CronTrigger, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := `SELECT id, target_type, target_id, cron_expr, enabled, last_run_at, last_status, finished_at, created_at, updated_at
	      FROM schedule_triggers WHERE org_id = ? AND user_id = ?`
	args := []any{orgID, userID}
	if targetType != "" {
		q += ` AND target_type = ?`
		args = append(args, targetType)
	}
	if targetID > 0 {
		q += ` AND target_id = ?`
		args = append(args, targetID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CronTrigger
	for rows.Next() {
		t, err := scanCronTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListEnabledCronTriggers 全库启用规则（cron 每分钟扫描，含归属信息）
func (d *DB) ListEnabledCronTriggers(ctx context.Context) ([]CronTriggerWithOwner, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, org_id, user_id, target_type, target_id, cron_expr, last_run_at
		 FROM schedule_triggers WHERE enabled = 1 ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CronTriggerWithOwner
	for rows.Next() {
		var t CronTriggerWithOwner
		if err := rows.Scan(&t.ID, &t.OrgID, &t.UserID, &t.TargetType, &t.TargetID, &t.CronExpr, &t.LastRunAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetCronTrigger 单条详情；不存在返回 ok=false
func (d *DB) GetCronTrigger(ctx context.Context, orgID, userID, id int64) (CronTrigger, bool, error) {
	t, err := scanCronTrigger(d.conn.QueryRowContext(ctx,
		`SELECT id, target_type, target_id, cron_expr, enabled, last_run_at, last_status, finished_at, created_at, updated_at
		 FROM schedule_triggers WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID))
	if err == sql.ErrNoRows {
		return t, false, nil
	}
	return t, err == nil, err
}

// CreateCronTrigger 新建规则，返回 id
func (d *DB) CreateCronTrigger(ctx context.Context, orgID, userID int64, targetType string, targetID int64, cronExpr string, enabled bool) (int64, error) {
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO schedule_triggers (org_id, user_id, target_type, target_id, cron_expr, enabled)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		orgID, userID, targetType, targetID, cronExpr, boolToInt(enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCronTrigger 只改 cron 与启用状态；目标绑定（target_type/target_id）创建后不可变
func (d *DB) UpdateCronTrigger(ctx context.Context, orgID, userID, id int64, cronExpr string, enabled bool) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE schedule_triggers SET cron_expr = ?, enabled = ?, updated_at = datetime('now')
		 WHERE id = ? AND org_id = ? AND user_id = ?`,
		cronExpr, boolToInt(enabled), id, orgID, userID)
	return err
}

// DeleteCronTrigger 删除规则
func (d *DB) DeleteCronTrigger(ctx context.Context, orgID, userID, id int64) error {
	_, err := d.conn.ExecContext(ctx,
		`DELETE FROM schedule_triggers WHERE id = ? AND org_id = ? AND user_id = ?`,
		id, orgID, userID)
	return err
}

// DeleteCronTriggersForTarget 删除指向某个目标对象的全部规则。
// 目标对象被删除时由装配层调用，避免留下每分钟报「目标不存在」的孤儿规则。
func (d *DB) DeleteCronTriggersForTarget(ctx context.Context, orgID, userID int64, targetType string, targetID int64) error {
	_, err := d.conn.ExecContext(ctx,
		`DELETE FROM schedule_triggers WHERE org_id = ? AND user_id = ? AND target_type = ? AND target_id = ?`,
		orgID, userID, targetType, targetID)
	return err
}

// ClaimCronTrigger CAS 占用一次触发：同分钟不重跑，且租约未过期时不重复触发。
// leaseUntil 为本次执行允许占用的截止时刻（YYYY-MM-DD HH:mm）。
// recovered 表示本次是接管一个已过期的 running 租约（上一次执行未按时回收），
// 调用方据此记日志，避免"僵尸执行 + 新执行"并发却无人知晓。
func (d *DB) ClaimCronTrigger(ctx context.Context, id int64, nowKey, leaseUntil string) (claimed, recovered bool, err error) {
	// 先取旧状态：查询失败不阻断 claim（宁可重复执行，也不要卡死调度）
	var prevStatus, prevLease string
	_ = d.conn.QueryRowContext(ctx,
		`SELECT last_status, lease_until FROM schedule_triggers WHERE id = ?`, id).
		Scan(&prevStatus, &prevLease)
	res, err := d.conn.ExecContext(ctx,
		`UPDATE schedule_triggers
		 SET last_run_at = ?, lease_until = ?, last_status = 'running'
		 WHERE id = ? AND last_run_at <> ? AND (lease_until = '' OR lease_until < ?)`,
		nowKey, leaseUntil, id, nowKey, nowKey)
	if err != nil {
		return false, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, false, err
	}
	return true, prevStatus == "running" && prevLease != "" && prevLease < nowKey, nil
}

// MarkCronTriggerRun 记录最近一次触发结果并释放租约。
// at 保持触发分钟（同分钟防重依赖它）；finished_at 单独记录真正完成时刻。
func (d *DB) MarkCronTriggerRun(ctx context.Context, id int64, status, at string) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE schedule_triggers
		 SET last_run_at = ?, last_status = ?, lease_until = '',
		     finished_at = datetime('now'), updated_at = datetime('now')
		 WHERE id = ?`,
		at, status, id)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
