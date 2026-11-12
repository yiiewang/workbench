package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"
)

// encodeTaskFiles 序列化任务关联文件列表为 JSON 数组字符串（SQLite TEXT 列）。
// 空列表存 '[]'（与列默认值一致）。
func encodeTaskFiles(files []string) string {
	if len(files) == 0 {
		return "[]"
	}
	b, err := json.Marshal(files)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// decodeTaskFiles 反序列化关联文件列表；空/非法/空数组一律返回 nil。
func decodeTaskFiles(s string) []string {
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	var files []string
	if err := json.Unmarshal([]byte(s), &files); err != nil || len(files) == 0 {
		return nil
	}
	return files
}

// bumpUserRevision 事务内递增用户任务版本号并读回新值。
// 所有任务写路径都必须经过这里，保证 revision 严格递增（乐观锁基准）。
func bumpUserRevision(ctx context.Context, tx *sql.Tx, userID int64) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET revision = revision + 1 WHERE id = ?`, userID); err != nil {
		return 0, err
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM users WHERE id = ?`, userID).Scan(&rev); err != nil {
		return 0, err
	}
	return rev, nil
}

// GetRevision 读取用户当前任务版本号（乐观锁基准；多设备轮询对账也用它）
func (d *DB) GetRevision(ctx context.Context, userID int64) (int64, error) {
	var rev int64
	err := d.conn.QueryRowContext(ctx, `SELECT revision FROM users WHERE id = ?`, userID).Scan(&rev)
	return rev, err
}

// UpdateTask 增量更新单条任务（PATCH /api/tasks/{id}），revision +1 并返回。
func (d *DB) UpdateTask(ctx context.Context, orgID, userID int64, task TaskItem) (int64, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	autoP := 0
	if task.AutoPostponed {
		autoP = 1
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET title=?, content=?, status=?, priority=?, scheduled=?, due=?, scheduled_time=?, due_time=?, progress=?, assignee=?, postponed_count=?, auto_postponed=?, sort_order=?, files=? WHERE org_id=? AND user_id=? AND id=?`,
		task.Title, task.Content, task.Status, task.Priority, task.Scheduled, task.Due, task.ScheduledTime, task.DueTime, task.Progress, string(task.Assignee), task.PostponedCount, autoP, task.SortOrder, encodeTaskFiles(task.Files), orgID, userID, string(task.ID))
	if err != nil {
		return 0, err
	}

	rev, err := bumpUserRevision(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now') WHERE id = ?`, userID); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

// AddTask 新增单条任务（POST /api/tasks/{id}）。
func (d *DB) AddTask(ctx context.Context, orgID, userID int64, task TaskItem) (int64, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	autoP := 0
	if task.AutoPostponed {
		autoP = 1
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tasks (id, user_id, org_id, title, content, status, priority, scheduled, due, scheduled_time, due_time, progress, assignee, postponed_count, auto_postponed, sort_order, files) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(task.ID), userID, orgID, task.Title, task.Content, task.Status, task.Priority, task.Scheduled, task.Due, task.ScheduledTime, task.DueTime, task.Progress, string(task.Assignee), task.PostponedCount, autoP, task.SortOrder, encodeTaskFiles(task.Files))
	if err != nil {
		return 0, err
	}

	rev, err := bumpUserRevision(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now') WHERE id = ?`, userID); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

// DeleteTask 删除单条任务（DELETE /api/tasks/{id}）。
func (d *DB) DeleteTask(ctx context.Context, orgID, userID int64, taskID string) (int64, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE org_id = ? AND user_id = ? AND id = ?`, orgID, userID, taskID); err != nil {
		return 0, err
	}

	rev, err := bumpUserRevision(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now') WHERE id = ?`, userID); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

// GetTasks 获取用户任务
func (d *DB) GetTasks(ctx context.Context, orgID, userID int64) ([]TaskItem, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, title, content, status, priority, scheduled, due, scheduled_time, due_time, progress, assignee, postponed_count, auto_postponed, sort_order, files FROM tasks WHERE org_id = ? AND user_id = ? ORDER BY sort_order`,
		orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskItem
	for rows.Next() {
		var t TaskItem
		var autoP int
		var filesJSON string
		if err := rows.Scan(&t.ID, &t.Title, &t.Content, &t.Status, &t.Priority, &t.Scheduled, &t.Due, &t.ScheduledTime, &t.DueTime, &t.Progress, &t.Assignee, &t.PostponedCount, &autoP, &t.SortOrder, &filesJSON); err != nil {
			return nil, err
		}
		t.AutoPostponed = autoP == 1
		t.Files = decodeTaskFiles(filesJSON)
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// ScanDueTasks 全库扫描到期任务（schedule 包 TodoDueScan job 用）：
// 所有用户 due <= today 且 status != done 的任务，JOIN users 确保用户存在。
// 提醒去重由调用方经 MarkNotified 完成（notify_log 表），此处不做过滤。
func (d *DB) ScanDueTasks(ctx context.Context, today string) ([]DueTaskRow, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT t.id, t.title, t.status, t.priority, t.scheduled, t.due, t.progress, t.user_id, t.org_id
		FROM tasks t
		JOIN users u ON u.id = t.user_id
		WHERE t.due != '' AND t.due <= ? AND t.status != 'done'
		ORDER BY t.due, t.user_id
	`, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DueTaskRow
	for rows.Next() {
		var r DueTaskRow
		if err := rows.Scan(&r.Task.ID, &r.Task.Title, &r.Task.Status, &r.Task.Priority,
			&r.Task.Scheduled, &r.Task.Due, &r.Task.Progress, &r.UserID, &r.OrgID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// DueTaskRow 到期扫描结果行（schedule 包消费）
type DueTaskRow struct {
	Task   TaskItem
	UserID int64
	OrgID  int64
}

// GetTaskByID 按 id 查单条任务（完整字段，含 content 全文）。
// org/user 归属过滤内嵌在 WHERE 中：不存在或非本人任务统一返回 exists=false。
// GET /api/tasks/{id} 详情接口用（列表响应已瘦身不含 content，编辑前拉详情）。
func (d *DB) GetTaskByID(ctx context.Context, orgID, userID int64, taskID string) (TaskItem, bool, error) {
	var t TaskItem
	var autoP int
	var filesJSON string
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, title, content, status, priority, scheduled, due, scheduled_time, due_time, progress, assignee, postponed_count, auto_postponed, sort_order, files FROM tasks WHERE org_id = ? AND user_id = ? AND id = ?`,
		orgID, userID, taskID).
		Scan(&t.ID, &t.Title, &t.Content, &t.Status, &t.Priority, &t.Scheduled, &t.Due, &t.ScheduledTime, &t.DueTime, &t.Progress, &t.Assignee, &t.PostponedCount, &autoP, &t.SortOrder, &filesJSON)
	if err == sql.ErrNoRows {
		return TaskItem{}, false, nil
	}
	if err != nil {
		return TaskItem{}, false, err
	}
	t.AutoPostponed = autoP == 1
	t.Files = decodeTaskFiles(filesJSON)
	return t, true, nil
}

// truncateRunes 按 rune 截断字符串（防止多字节中文被截出乱码），超长追加省略号
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// taskPreviewLen 列表响应中正文预览的最大 rune 数
const taskPreviewLen = 80

// GetTasksJSONByOwner 获取指定用户的任务（供 /api/tasks GET 使用）。
// 响应结构：orgs.{orgId}.{userId} = {revision, tasks}——revision 是乐观锁基准。
func (d *DB) GetTasksJSONByOwner(ctx context.Context, orgID, userID int64) (map[string]interface{}, error) {
	result := map[string]interface{}{
		"version":     "1.0",
		"lastUpdated": time.Now().Format(time.RFC3339),
	}
	orgsMap := make(map[string]map[string]interface{})

	rows, err := d.conn.QueryContext(ctx, `
		SELECT ?, u.id, u.revision,
		       t.id, t.title, t.content, t.status, t.priority, t.scheduled, t.due, t.scheduled_time, t.due_time, t.progress, t.assignee, t.postponed_count, t.auto_postponed, t.sort_order, t.files
		FROM users u
		LEFT JOIN tasks t ON t.org_id = ? AND t.user_id = u.id
		WHERE u.id = ?
		ORDER BY u.id, t.sort_order
	`, orgID, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type userData struct {
		tasks    []TaskItem
		revision int64
	}
	orgUsers := make(map[string]map[string]*userData)

	for rows.Next() {
		var oID, uID int64
		var revision int64
		var taskID, taskTitle, taskContent, taskStatus, taskPriority, taskScheduled, taskDue, taskScheduledTime, taskDueTime, taskAssignee, taskFiles sql.NullString
		var taskProgress, taskPostponed, taskAutoPostponed, sortOrder sql.NullInt64
		if err := rows.Scan(&oID, &uID, &revision, &taskID, &taskTitle, &taskContent, &taskStatus, &taskPriority, &taskScheduled, &taskDue, &taskScheduledTime, &taskDueTime, &taskProgress, &taskAssignee, &taskPostponed, &taskAutoPostponed, &sortOrder, &taskFiles); err != nil {
			return nil, err
		}
		orgKey := strconv.FormatInt(oID, 10)
		userKey := strconv.FormatInt(uID, 10)
		if orgUsers[orgKey] == nil {
			orgUsers[orgKey] = make(map[string]*userData)
		}
		if orgUsers[orgKey][userKey] == nil {
			orgUsers[orgKey][userKey] = &userData{revision: revision}
		}
		if taskID.Valid {
			// 列表瘦身：不传 content 全文（TaskItem.Content 已 omitempty，置空即省略），
			// 用截断预览替代（看板卡片/列表项展示用）；编辑/详情场景走 GET /api/tasks/{id}
			orgUsers[orgKey][userKey].tasks = append(orgUsers[orgKey][userKey].tasks, TaskItem{
				ID:             FlexString(taskID.String),
				Title:          taskTitle.String,
				Content:        "",
				ContentPreview: truncateRunes(taskContent.String, taskPreviewLen),
				Status:         taskStatus.String,
				Priority:       taskPriority.String,
				Scheduled:      taskScheduled.String,
				Due:            taskDue.String,
				ScheduledTime:  taskScheduledTime.String,
				DueTime:        taskDueTime.String,
				Progress:       int(taskProgress.Int64),
				Assignee:       FlexString(taskAssignee.String),
				PostponedCount: int(taskPostponed.Int64),
				AutoPostponed:  taskAutoPostponed.Int64 == 1,
				SortOrder:      int(sortOrder.Int64),
				Files:          decodeTaskFiles(taskFiles.String),
			})
		}
	}

	for orgKey, users := range orgUsers {
		usersMap := make(map[string]interface{})
		for userKey, ud := range users {
			tasks := ud.tasks
			if tasks == nil {
				tasks = []TaskItem{}
			}
			usersMap[userKey] = map[string]interface{}{
				"revision": ud.revision,
				"tasks":    tasks,
			}
		}
		orgsMap[orgKey] = usersMap
	}
	result["orgs"] = orgsMap
	return result, nil
}
