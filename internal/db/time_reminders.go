package db

// 精确时间提醒查询（schedule 包 todo_time_reminder job 用）与 AI 定时任务 CRUD。

import (
	"context"
	"database/sql"
	"time"
)

// TimeReminderRow 精确时间提醒命中行
type TimeReminderRow struct {
	Task   TaskItem
	UserID int64
	OrgID  int64
	// Kind 命中类型：start（scheduled_time 到点）| due（due_time 到点且未完成）
	Kind string
}

// ScanTimeReminders 查询某日某时刻命中的精确提醒：
//   - scheduled = today 且 scheduled_time = hhmm → start
//   - due = today 且 due_time = hhmm 且 status != done → due
//
// 同一任务两个条件都命中时返回两行（各自独立去重投递）。
func (d *DB) ScanTimeReminders(ctx context.Context, today, hhmm string) ([]TimeReminderRow, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT t.id, t.title, t.content, t.status, t.priority, t.scheduled, t.due,
		       t.scheduled_time, t.due_time, t.progress, t.assignee, t.postponed_count,
		       t.auto_postponed, t.sort_order, t.files, t.user_id, t.org_id
		FROM tasks t
		JOIN users u ON u.id = t.user_id
		WHERE (
		      (t.scheduled = ? AND t.scheduled_time = ?)
		   OR (t.due = ? AND t.due_time = ? AND t.status != 'done')
		)
	`, today, hhmm, today, hhmm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TimeReminderRow
	for rows.Next() {
		var r TimeReminderRow
		var autoP int
		var filesJSON string
		if err := rows.Scan(&r.Task.ID, &r.Task.Title, &r.Task.Content, &r.Task.Status, &r.Task.Priority,
			&r.Task.Scheduled, &r.Task.Due, &r.Task.ScheduledTime, &r.Task.DueTime, &r.Task.Progress,
			&r.Task.Assignee, &r.Task.PostponedCount, &autoP, &r.Task.SortOrder, &filesJSON,
			&r.UserID, &r.OrgID); err != nil {
			return nil, err
		}
		r.Task.AutoPostponed = autoP == 1
		r.Task.Files = decodeTaskFiles(filesJSON)
		switch {
		case r.Task.Scheduled == today && r.Task.ScheduledTime == hhmm:
			r.Kind = "start"
		default:
			r.Kind = "due"
		}
		out = append(out, r)
	}
	return out, nil
}

// ============================================================
// AI 定时任务（ai_jobs）：用户自建"到点让 AI 执行 prompt"
// ============================================================

// AIJob AI 定时任务
type AIJob struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CronExpr  string `json:"cronExpr"`
	Prompt    string `json:"prompt"`
	Enabled   bool   `json:"enabled"`
	// IsGlobal 全局公共任务（user_id IS NULL，只存一条，执行时按用户展开）
	IsGlobal bool `json:"isGlobal,omitempty"`
	LastRunAt string `json:"lastRunAt,omitempty"`
	LastStatus string `json:"lastStatus,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// scanAIJobRow 列序：id, name, cron_expr, prompt, enabled, last_run_at, last_status, created_at, user_id(可 NULL)
func scanAIJobRow(scan func(dest ...any) error) (AIJobWithOwner, error) {
	var j AIJob
	var enabled int
	var user sql.NullInt64
	var org int64
	if err := scan(&j.ID, &j.Name, &j.CronExpr, &j.Prompt, &enabled, &j.LastRunAt, &j.LastStatus, &j.CreatedAt, &user, &org); err != nil {
		return AIJobWithOwner{}, err
	}
	j.Enabled = enabled == 1
	j.IsGlobal = !user.Valid
	return AIJobWithOwner{AIJob: j, UserID: user.Int64, OrgID: org}, nil
}

// ListAIJobs 用户的任务列表 = 私有任务 + 全局公共任务（前端标记只读）
func (d *DB) ListAIJobs(ctx context.Context, orgID, userID int64) ([]AIJob, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, name, cron_expr, prompt, enabled, COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,''), user_id, org_id
		 FROM ai_jobs WHERE (org_id = ? AND user_id = ?) OR user_id IS NULL ORDER BY id`, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]AIJob, 0)
	for rows.Next() {
		r, err := scanAIJobRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, r.AIJob)
	}
	return jobs, nil
}

// ListEnabledAIJobs 列出所有启用任务（跨用户，调度器逐分钟扫描用）。
// 全局公共任务 UserID=0，由调度器展开为每个有邮箱的用户。
func (d *DB) ListEnabledAIJobs(ctx context.Context) ([]AIJobWithOwner, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, name, cron_expr, prompt, enabled, COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,''), user_id, org_id
		 FROM ai_jobs WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIJobWithOwner
	for rows.Next() {
		r, err := scanAIJobRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// UserEmailTarget 有邮箱的用户（全局公共任务展开用）
type UserEmailTarget struct {
	UserID int64
	OrgID  int64
}

// ListUsersWithEmail 列出所有配置了邮箱的活跃用户
func (d *DB) ListUsersWithEmail(ctx context.Context) ([]UserEmailTarget, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT u.id,
		       (SELECT uo.org_id FROM user_orgs uo WHERE uo.user_id = u.id AND uo.status = 'active' ORDER BY uo.id LIMIT 1)
		FROM users u
		WHERE COALESCE(u.email, '') != ''
		  AND EXISTS (SELECT 1 FROM user_orgs uo WHERE uo.user_id = u.id AND uo.status = 'active')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserEmailTarget
	for rows.Next() {
		var t UserEmailTarget
		if err := rows.Scan(&t.UserID, &t.OrgID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// AIJobWithOwner 启用任务 + 归属（调度器执行时用）
type AIJobWithOwner struct {
	AIJob
	UserID int64
	OrgID  int64
}

// CreateAIJob 新建（cron 合法性由调用方校验）
func (d *DB) CreateAIJob(ctx context.Context, orgID, userID int64, name, cronExpr, prompt string, enabled bool) (int64, error) {
	e := 0
	if enabled {
		e = 1
	}
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO ai_jobs (user_id, org_id, name, cron_expr, prompt, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, orgID, name, cronExpr, prompt, e)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetAIJob 查单条（校验归属），不存在返回 nil
func (d *DB) GetAIJob(ctx context.Context, orgID, userID, id int64) (*AIJob, error) {
	var j AIJob
	var enabled int
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, name, cron_expr, prompt, enabled, COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,'')
		 FROM ai_jobs WHERE org_id = ? AND user_id = ? AND id = ?`, orgID, userID, id).
		Scan(&j.ID, &j.Name, &j.CronExpr, &j.Prompt, &enabled, &j.LastRunAt, &j.LastStatus, &j.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Enabled = enabled == 1
	return &j, nil
}

// GetAIJobAny 按 id 查任务（不校验归属，调用方自行判断：
// 全局任务 IsGlobal=true；私有任务核对 UserID）。
func (d *DB) GetAIJobAny(ctx context.Context, id int64) (*AIJobWithOwner, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT id, name, cron_expr, prompt, enabled, COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,''), user_id, org_id FROM ai_jobs WHERE id = ?`, id)
	r, err := scanAIJobRow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateAIJob 更新（全字段覆盖；仅更新提供的字段由调用方合并）
func (d *DB) UpdateAIJob(ctx context.Context, orgID, userID, id int64, name, cronExpr, prompt string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := d.conn.ExecContext(ctx,
		`UPDATE ai_jobs SET name=?, cron_expr=?, prompt=?, enabled=?, updated_at=datetime('now') WHERE org_id=? AND user_id=? AND id=?`,
		name, cronExpr, prompt, e, orgID, userID, id)
	return err
}

// DeleteAIJob 删除
func (d *DB) DeleteAIJob(ctx context.Context, orgID, userID, id int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM ai_jobs WHERE org_id = ? AND user_id = ? AND id = ?`, orgID, userID, id)
	return err
}

// MarkAIJobRun 记录执行结果：ai_jobs.last_* 更新为最近一次 + ai_job_runs 追加历史
// （output 截断 4KB 存储，完整输出已邮件投递）
func (d *DB) MarkAIJobRun(ctx context.Context, id int64, status, output string) error {
	if len(output) > 4096 {
		output = output[:4096] + "\n…（截断）"
	}
	now := time.Now().Format("2006-01-02 15:04")
	if _, err := d.conn.ExecContext(ctx,
		`UPDATE ai_jobs SET last_run_at = ?, last_status = ?, updated_at = datetime('now') WHERE id = ?`,
		now, status, id); err != nil {
		return err
	}
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO ai_job_runs (job_id, ran_at, status, output) VALUES (?, ?, ?, ?)`,
		id, now, status, output)
	return err
}

// AIJobRun 单次执行记录
type AIJobRun struct {
	ID     int64  `json:"id"`
	JobID  int64  `json:"jobId"`
	RanAt  string `json:"ranAt"`
	Status string `json:"status"`
	Output string `json:"output"`
}

// ListAIJobRuns 任务的执行历史（最近 limit 条，倒序）
func (d *DB) ListAIJobRuns(ctx context.Context, jobID int64, limit int) ([]AIJobRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, job_id, ran_at, status, COALESCE(output,'') FROM ai_job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?`,
		jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]AIJobRun, 0)
	for rows.Next() {
		var r AIJobRun
		if err := rows.Scan(&r.ID, &r.JobID, &r.RanAt, &r.Status, &r.Output); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}
