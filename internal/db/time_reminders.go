package db

// 精确时间提醒查询（schedule 包 todo_time_reminder job 用）与 AI 定时任务 CRUD。

import (
	"context"
	"database/sql"
	"encoding/json"
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

// ScanTimeReminders 查询某日某时刻命中的精确提醒（已完成任务一律跳过）：
//   - scheduled = today 且 scheduled_time = hhmm → start
//   - due = today 且 due_time = hhmm → due
//
// 两条件同时命中只返回一行（start 优先判定）——完成状态的任务不再发提醒邮件。
func (d *DB) ScanTimeReminders(ctx context.Context, today, hhmm string) ([]TimeReminderRow, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT t.id, t.title, t.content, t.status, t.priority, t.scheduled, t.due,
		       t.scheduled_time, t.due_time, t.progress, t.assignee, t.postponed_count,
		       t.auto_postponed, t.sort_order, t.files, t.user_id, t.org_id
		FROM tasks t
		JOIN users u ON u.id = t.user_id
		WHERE t.status != 'done' AND (
		      (t.scheduled = ? AND t.scheduled_time = ?)
		   OR (t.due = ? AND t.due_time = ?)
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

// AIJob 定时任务。ai_jobs 只存执行定义；CronExpr / Enabled / LastRunAt /
// LastStatus 派生自 schedule_triggers（target_type='job'），仅用于兼容旧响应。
type AIJob struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	CronExpr string `json:"cronExpr"`
	Enabled  bool   `json:"enabled"`
	// IsGlobal 全局公共任务（user_id IS NULL，只存一条，执行时按用户展开）
	IsGlobal   bool            `json:"isGlobal,omitempty"`
	TargetType string          `json:"targetType,omitempty"`
	Target     json.RawMessage `json:"target,omitempty"`
	AllowWrite []string        `json:"allowWrite,omitempty"`
	Context    json.RawMessage `json:"context,omitempty"`
	Notify     json.RawMessage `json:"notify,omitempty"`
	LastRunAt  string          `json:"lastRunAt,omitempty"`
	LastStatus string          `json:"lastStatus,omitempty"`
	CreatedAt  string          `json:"createdAt,omitempty"`
}

// aiJobSelectCols 统一列序，与 scanAIJobRow 一一对应；调度字段用子查询取
// 该任务最近一条 job 规则。表别名固定为 j。
const aiJobSelectCols = `j.id, j.name, j.prompt,
	COALESCE(j.target_type,'ai'), COALESCE(j.target_json,''), COALESCE(j.allow_write,'[]'),
	COALESCE(j.context_json,'{}'), COALESCE(j.notify_json,'{}'), COALESCE(j.created_at,''),
	j.user_id, j.org_id,
	COALESCE((SELECT cron_expr FROM schedule_triggers WHERE target_type='job' AND target_id=j.id ORDER BY id LIMIT 1),''),
	COALESCE((SELECT enabled FROM schedule_triggers WHERE target_type='job' AND target_id=j.id ORDER BY id LIMIT 1),0),
	COALESCE((SELECT last_run_at FROM schedule_triggers WHERE target_type='job' AND target_id=j.id ORDER BY id LIMIT 1),''),
	COALESCE((SELECT last_status FROM schedule_triggers WHERE target_type='job' AND target_id=j.id ORDER BY id LIMIT 1),'')`

// scanAIJobRow 按 aiJobSelectCols 扫描；JSON 列解析失败不阻塞读取（按空处理）。
func scanAIJobRow(scan func(dest ...any) error) (AIJobWithOwner, error) {
	var j AIJob
	var enabled int
	var user sql.NullInt64
	var org int64
	var targetType, targetJSON, allowWrite, contextJSON, notifyJSON, createdAt, cronExpr string
	if err := scan(&j.ID, &j.Name, &j.Prompt,
		&targetType, &targetJSON, &allowWrite, &contextJSON, &notifyJSON, &createdAt,
		&user, &org, &cronExpr, &enabled, &j.LastRunAt, &j.LastStatus); err != nil {
		return AIJobWithOwner{}, err
	}
	j.Enabled = enabled == 1
	j.IsGlobal = !user.Valid
	j.CronExpr, j.CreatedAt = cronExpr, createdAt
	j.TargetType = targetType
	if targetJSON != "" {
		j.Target = json.RawMessage(targetJSON)
	}
	if allowWrite != "" {
		_ = json.Unmarshal([]byte(allowWrite), &j.AllowWrite)
	}
	if contextJSON != "" {
		j.Context = json.RawMessage(contextJSON)
	}
	if notifyJSON != "" {
		j.Notify = json.RawMessage(notifyJSON)
	}
	return AIJobWithOwner{AIJob: j, UserID: user.Int64, OrgID: org}, nil
}

// ListAIJobs 用户的任务列表 = 私有任务 + 全局公共任务（前端标记只读）
func (d *DB) ListAIJobs(ctx context.Context, orgID, userID int64) ([]AIJob, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT `+aiJobSelectCols+`
		 FROM ai_jobs j WHERE (j.org_id = ? AND j.user_id = ?) OR j.user_id IS NULL ORDER BY j.id`, orgID, userID)
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

// AIJobSpec 新建/更新定时任务的完整字段；target 相关字段由调用方序列化为 JSON。
type AIJobSpec struct {
	Name        string
	CronExpr    string
	Prompt      string
	Enabled     bool
	TargetType  string
	TargetJSON  string
	AllowWrite  []string
	ContextJSON string
	NotifyJSON  string
}

// legacyAITargetJSON 把历史 prompt 包装成 {"type":"ai","prompt":...}。
func legacyAITargetJSON(prompt string) string {
	b, _ := json.Marshal(map[string]string{"type": "ai", "prompt": prompt})
	return string(b)
}

// marshalAllowWrite nil 表示「创建用默认 / 更新保持现值」，空切片表示显式清空。
func marshalAllowWrite(v []string) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// CreateAIJob 兼容旧调用：cron/enabled 由装配层写入 schedule_triggers，db 层不落库。
func (d *DB) CreateAIJob(ctx context.Context, orgID, userID int64, name, cronExpr, prompt string, enabled bool) (int64, error) {
	_ = cronExpr
	_ = enabled
	return d.CreateAIJobWithSpec(ctx, orgID, userID, AIJobSpec{Name: name, Prompt: prompt})
}

// CreateAIJobWithSpec 新建执行定义（调度规则由 server 装配层单独创建）。
func (d *DB) CreateAIJobWithSpec(ctx context.Context, orgID, userID int64, spec AIJobSpec) (int64, error) {
	targetType := spec.TargetType
	if targetType == "" {
		targetType = "ai"
	}
	targetJSON := spec.TargetJSON
	if targetJSON == "" {
		targetJSON = legacyAITargetJSON(spec.Prompt)
	}
	allowWrite := marshalAllowWrite(spec.AllowWrite)
	if allowWrite == "" {
		allowWrite = "[]"
	}
	contextJSON, notifyJSON := spec.ContextJSON, spec.NotifyJSON
	if contextJSON == "" {
		contextJSON = "{}"
	}
	if notifyJSON == "" {
		notifyJSON = "{}"
	}
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO ai_jobs (user_id, org_id, name, prompt, target_type, target_json, allow_write, context_json, notify_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, orgID, spec.Name, spec.Prompt, targetType, targetJSON, allowWrite, contextJSON, notifyJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetAIJob 查单条（校验归属），不存在返回 nil
func (d *DB) GetAIJob(ctx context.Context, orgID, userID, id int64) (*AIJob, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT `+aiJobSelectCols+` FROM ai_jobs j WHERE j.org_id = ? AND j.user_id = ? AND j.id = ?`, orgID, userID, id)
	r, err := scanAIJobRow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r.AIJob, nil
}

// GetAIJobAny 按 id 查任务（不校验归属，调用方自行判断：
// 全局任务 IsGlobal=true；私有任务核对 UserID）。
func (d *DB) GetAIJobAny(ctx context.Context, id int64) (*AIJobWithOwner, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT `+aiJobSelectCols+` FROM ai_jobs j WHERE j.id = ?`, id)
	r, err := scanAIJobRow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateAIJob 兼容旧调用：只改 name/prompt；cron/enabled 走 schedule_triggers。
func (d *DB) UpdateAIJob(ctx context.Context, orgID, userID, id int64, name, cronExpr, prompt string, enabled bool) error {
	_ = cronExpr
	_ = enabled
	return d.UpdateAIJobWithSpec(ctx, orgID, userID, id, AIJobSpec{Name: name, Prompt: prompt})
}

// UpdateAIJobWithSpec 更新执行定义；Target*/Context/Notify 为空串表示保持现值，
// AllowWrite nil 表示保持、空切片表示显式清空。
func (d *DB) UpdateAIJobWithSpec(ctx context.Context, orgID, userID, id int64, spec AIJobSpec) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE ai_jobs SET name=?, prompt=?,
		 target_type = COALESCE(NULLIF(?, ''), target_type),
		 target_json = COALESCE(NULLIF(?, ''), target_json),
		 allow_write = COALESCE(NULLIF(?, ''), allow_write),
		 context_json = COALESCE(NULLIF(?, ''), context_json),
		 notify_json = COALESCE(NULLIF(?, ''), notify_json),
		 updated_at = datetime('now')
		 WHERE org_id=? AND user_id=? AND id=?`,
		spec.Name, spec.Prompt,
		spec.TargetType, spec.TargetJSON, marshalAllowWrite(spec.AllowWrite),
		spec.ContextJSON, spec.NotifyJSON,
		orgID, userID, id)
	return err
}

// DeleteAIJob 删除
func (d *DB) DeleteAIJob(ctx context.Context, orgID, userID, id int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM ai_jobs WHERE org_id = ? AND user_id = ? AND id = ?`, orgID, userID, id)
	return err
}

// MarkAIJobRun 追加一次基础执行历史（兼容旧调用）。
func (d *DB) MarkAIJobRun(ctx context.Context, id int64, status, output string) error {
	return d.MarkAIJobRunDetail(ctx, id, AIJobRun{Status: status, Output: output})
}

// MarkAIJobRunDetail 追加一次执行历史（output 截断 4KB）。
// 最近执行状态由 schedule_triggers.last_run_at/last_status 记录。
func (d *DB) MarkAIJobRunDetail(ctx context.Context, id int64, run AIJobRun) error {
	if len(run.Output) > 4096 {
		run.Output = run.Output[:4096] + "\n…（截断）"
	}
	if run.RanAt == "" {
		run.RanAt = time.Now().Format("2006-01-02 15:04")
	}
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO ai_job_runs (job_id, ran_at, status, output, target_type, pipeline_run_id, steps_summary, err_msg, duration_ms, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, run.RanAt, run.Status, run.Output, run.TargetType, run.PipelineRunID,
		run.StepsSummary, run.ErrMsg, run.DurationMs, run.FinishedAt)
	return err
}

// AIJobRun 单次执行记录
type AIJobRun struct {
	ID            int64  `json:"id"`
	JobID         int64  `json:"jobId"`
	RanAt         string `json:"ranAt"`
	Status        string `json:"status"`
	Output        string `json:"output"`
	TargetType    string `json:"targetType,omitempty"`
	PipelineRunID string `json:"pipelineRunId,omitempty"`
	StepsSummary  string `json:"stepsSummary,omitempty"`
	ErrMsg        string `json:"errMsg,omitempty"`
	DurationMs    int64  `json:"durationMs,omitempty"`
	FinishedAt    string `json:"finishedAt,omitempty"`
}

// ListAIJobRuns 任务的执行历史（最近 limit 条，倒序）
func (d *DB) ListAIJobRuns(ctx context.Context, jobID int64, limit int) ([]AIJobRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, job_id, ran_at, status, COALESCE(output,''),
		        COALESCE(target_type,''), COALESCE(pipeline_run_id,''), COALESCE(steps_summary,''),
		        COALESCE(err_msg,''), COALESCE(duration_ms,0), COALESCE(finished_at,'')
		 FROM ai_job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?`,
		jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]AIJobRun, 0)
	for rows.Next() {
		var r AIJobRun
		if err := rows.Scan(&r.ID, &r.JobID, &r.RanAt, &r.Status, &r.Output,
			&r.TargetType, &r.PipelineRunID, &r.StepsSummary,
			&r.ErrMsg, &r.DurationMs, &r.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}
