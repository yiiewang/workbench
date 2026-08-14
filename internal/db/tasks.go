package db

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
)

// md5Hex 计算字符串的 md5 十六进制摘要（供 recomputeVersion 使用）。
func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// UpsertTasks 批量替换用户任务，同时存储客户端发来的 version JSON
func (d *DB) UpsertTasks(ctx context.Context, orgID, userID string, tasks []TaskItem, versionJSON string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE org_id = ? AND user_id = ?`, orgID, userID); err != nil {
		return err
	}

	const q = `INSERT INTO tasks (id, user_id, org_id, title, content, status, priority, scheduled, due, progress, assignee, postponed_count, auto_postponed, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range tasks {
		autoP := 0
		if t.AutoPostponed {
			autoP = 1
		}
		if _, err := stmt.ExecContext(ctx, string(t.ID), userID, orgID, t.Title, t.Content, t.Status, t.Priority, t.Scheduled, t.Due, t.Progress, t.Assignee, t.PostponedCount, autoP, t.SortOrder); err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now'), version_json = ? WHERE org_id = ? AND id = ?`, versionJSON, orgID, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateTask 增量更新单条任务（PATCH /api/tasks/{id}）。
// 更新后重新计算 version md5（服务端是唯一真相源），返回新的 versionJSON。
func (d *DB) UpdateTask(ctx context.Context, orgID, userID string, task TaskItem) (string, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	autoP := 0
	if task.AutoPostponed {
		autoP = 1
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET title=?, content=?, status=?, priority=?, scheduled=?, due=?, progress=?, assignee=?, postponed_count=?, auto_postponed=?, sort_order=? WHERE org_id=? AND user_id=? AND id=?`,
		task.Title, task.Content, task.Status, task.Priority, task.Scheduled, task.Due, task.Progress, task.Assignee, task.PostponedCount, autoP, task.SortOrder, orgID, userID, string(task.ID))
	if err != nil {
		return "", err
	}

	// 重新计算 version：读取该用户全部任务，算 md5
	versionJSON, err := d.recomputeVersion(ctx, tx, orgID, userID)
	if err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now'), version_json = ? WHERE org_id = ? AND id = ?`, versionJSON, orgID, userID); err != nil {
		return "", err
	}
	return versionJSON, tx.Commit()
}

// AddTask 新增单条任务（POST /api/tasks/{id}）。
func (d *DB) AddTask(ctx context.Context, orgID, userID string, task TaskItem) (string, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	autoP := 0
	if task.AutoPostponed {
		autoP = 1
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tasks (id, user_id, org_id, title, content, status, priority, scheduled, due, progress, assignee, postponed_count, auto_postponed, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(task.ID), userID, orgID, task.Title, task.Content, task.Status, task.Priority, task.Scheduled, task.Due, task.Progress, task.Assignee, task.PostponedCount, autoP, task.SortOrder)
	if err != nil {
		return "", err
	}

	versionJSON, err := d.recomputeVersion(ctx, tx, orgID, userID)
	if err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now'), version_json = ? WHERE org_id = ? AND id = ?`, versionJSON, orgID, userID); err != nil {
		return "", err
	}
	return versionJSON, tx.Commit()
}

// DeleteTask 删除单条任务（DELETE /api/tasks/{id}）。
func (d *DB) DeleteTask(ctx context.Context, orgID, userID, taskID string) (string, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE org_id = ? AND user_id = ? AND id = ?`, orgID, userID, taskID); err != nil {
		return "", err
	}

	versionJSON, err := d.recomputeVersion(ctx, tx, orgID, userID)
	if err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = datetime('now'), version_json = ? WHERE org_id = ? AND id = ?`, versionJSON, orgID, userID); err != nil {
		return "", err
	}
	return versionJSON, tx.Commit()
}

// GetVersionJSON 获取用户当前 version_json（供冲突检测用）。
func (d *DB) GetVersionJSON(ctx context.Context, orgID, userID string) (string, error) {
	var versionJSON string
	err := d.conn.QueryRowContext(ctx, `SELECT version_json FROM users WHERE org_id = ? AND id = ?`, orgID, userID).Scan(&versionJSON)
	return versionJSON, err
}

// recomputeVersion 重新计算用户全部任务的 version md5（服务端唯一真相源）。
// 生成格式和客户端一致：{"md5":"xxxx","timestamp":1234567890,"deviceId":"","baseMd5":"...","baseTimestamp":...}
// 保留原 baseMd5/baseTimestamp 不变，只更新 md5 和 timestamp。
func (d *DB) recomputeVersion(ctx context.Context, tx *sql.Tx, orgID, userID string) (string, error) {
	// 读取当前 version 拿到 baseMd5/baseTimestamp/deviceId
	var oldVersionJSON string
	err := tx.QueryRowContext(ctx, `SELECT version_json FROM users WHERE org_id = ? AND id = ?`, orgID, userID).Scan(&oldVersionJSON)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}

	baseMd5, baseTimestamp, deviceID := "", int64(0), ""
	if oldVersionJSON != "" && oldVersionJSON != "null" {
		var old map[string]interface{}
		if json.Unmarshal([]byte(oldVersionJSON), &old) == nil {
			if v, ok := old["baseMd5"].(string); ok {
				baseMd5 = v
			}
			if v, ok := old["baseTimestamp"].(float64); ok {
				baseTimestamp = int64(v)
			}
			if v, ok := old["deviceId"].(string); ok {
				deviceID = v
			}
		}
	}

	// 读取全部任务，算 md5
	rows, err := tx.QueryContext(ctx,
		`SELECT id, title, content, status, priority, scheduled, due, progress, assignee, postponed_count, auto_postponed, sort_order FROM tasks WHERE org_id = ? AND user_id = ? ORDER BY sort_order`,
		orgID, userID)
	if err != nil {
		return "", err
	}
	var tasks []TaskItem
	for rows.Next() {
		var t TaskItem
		var autoP int
		if err := rows.Scan(&t.ID, &t.Title, &t.Content, &t.Status, &t.Priority, &t.Scheduled, &t.Due, &t.Progress, &t.Assignee, &t.PostponedCount, &autoP, &t.SortOrder); err != nil {
			rows.Close()
			return "", err
		}
		t.AutoPostponed = autoP == 1
		tasks = append(tasks, t)
	}
	rows.Close()

	// 序列化任务列表算 md5（和客户端算法保持一致：JSON.stringify(tasks) → md5）
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		return "", err
	}
	md5sum := md5Hex(tasksJSON)
	ts := time.Now().UnixMilli()

	// 组装 version JSON
	version := map[string]interface{}{
		"md5":          md5sum,
		"timestamp":    ts,
		"deviceId":     deviceID,
		"baseMd5":      baseMd5,
		"baseTimestamp": baseTimestamp,
	}
	out, err := json.Marshal(version)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetTasks 获取用户任务
func (d *DB) GetTasks(ctx context.Context, orgID, userID string) ([]TaskItem, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, title, content, status, priority, scheduled, due, progress, assignee, postponed_count, auto_postponed, sort_order FROM tasks WHERE org_id = ? AND user_id = ? ORDER BY sort_order`,
		orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskItem
	for rows.Next() {
		var t TaskItem
		var autoP int
		if err := rows.Scan(&t.ID, &t.Title, &t.Content, &t.Status, &t.Priority, &t.Scheduled, &t.Due, &t.Progress, &t.Assignee, &t.PostponedCount, &autoP, &t.SortOrder); err != nil {
			return nil, err
		}
		t.AutoPostponed = autoP == 1
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetTasksJSON 获取全量任务 JSON（供 /api/tasks GET 使用）
func (d *DB) GetTasksJSON(ctx context.Context) (map[string]interface{}, error) {
	result := map[string]interface{}{
		"version":     "1.0",
		"lastUpdated": time.Now().Format(time.RFC3339),
	}
	orgsMap := make(map[string]map[string]interface{})

	rows, err := d.conn.QueryContext(ctx, `
		SELECT u.org_id, u.id, u.updated_at, u.version_json,
		       t.id, t.title, t.content, t.status, t.priority, t.scheduled, t.due, t.progress, t.assignee, t.postponed_count, t.auto_postponed, t.sort_order
		FROM users u
		LEFT JOIN tasks t ON t.org_id = u.org_id AND t.user_id = u.id
		ORDER BY u.org_id, u.id, t.sort_order
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type userData struct {
		tasks       []TaskItem
		versionJSON string
	}
	orgUsers := make(map[string]map[string]*userData)

	for rows.Next() {
		var orgID, userID, updatedAt, versionJSON string
		var taskID, taskTitle, taskContent, taskStatus, taskPriority, taskScheduled, taskDue, taskAssignee sql.NullString
		var taskProgress, taskPostponed, taskAutoPostponed, sortOrder sql.NullInt64
		if err := rows.Scan(&orgID, &userID, &updatedAt, &versionJSON, &taskID, &taskTitle, &taskContent, &taskStatus, &taskPriority, &taskScheduled, &taskDue, &taskProgress, &taskAssignee, &taskPostponed, &taskAutoPostponed, &sortOrder); err != nil {
			return nil, err
		}
		if orgUsers[orgID] == nil {
			orgUsers[orgID] = make(map[string]*userData)
		}
		if orgUsers[orgID][userID] == nil {
			orgUsers[orgID][userID] = &userData{versionJSON: versionJSON}
		}
		if taskID.Valid {
			orgUsers[orgID][userID].tasks = append(orgUsers[orgID][userID].tasks, TaskItem{
				ID:             FlexString(taskID.String),
				Title:          taskTitle.String,
				Content:        taskContent.String,
				Status:         taskStatus.String,
				Priority:       taskPriority.String,
				Scheduled:      taskScheduled.String,
				Due:            taskDue.String,
				Progress:       int(taskProgress.Int64),
				Assignee:       taskAssignee.String,
				PostponedCount: int(taskPostponed.Int64),
				AutoPostponed:  taskAutoPostponed.Int64 == 1,
				SortOrder:      int(sortOrder.Int64),
			})
		}
	}

	for orgID, users := range orgUsers {
		usersMap := make(map[string]interface{})
		for userID, ud := range users {
			tasks := ud.tasks
			if tasks == nil {
				tasks = []TaskItem{}
			}
			// 优先使用客户端上传时存储的 version JSON（哈希由客户端计算，确保一致）
			// 无存储 version 时回退到 {"md5": "init"}
			var version interface{}
			if ud.versionJSON != "" {
				if err := json.Unmarshal([]byte(ud.versionJSON), &version); err != nil {
					version = map[string]string{"md5": "init"}
				}
			} else {
				version = map[string]string{"md5": "init"}
			}
			usersMap[userID] = map[string]interface{}{
				"version": version,
				"tasks":   tasks,
			}
		}
		orgsMap[orgID] = usersMap
	}
	result["orgs"] = orgsMap
	return result, nil
}
