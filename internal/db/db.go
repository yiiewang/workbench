// Package db SQLite 数据访问层
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB 封装 SQLite 操作
type DB struct {
	conn *sql.DB
}

// TaskItem 任务条目
type TaskItem struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Content        string `json:"content"`
	Status         string `json:"status"`
	Priority       string `json:"priority"`
	Scheduled      string `json:"scheduled"`
	Due            string `json:"due"`
	Progress       int    `json:"progress"`
	Assignee       string `json:"assignee"`
	PostponedCount int    `json:"postponedCount"`
	AutoPostponed  bool   `json:"autoPostponed"`
	SortOrder      int    `json:"sort_order,omitempty"`
	// 向后兼容旧版字段
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// VisitorStat 访问统计中的访客信息
type VisitorStat struct {
	VisitorID    string `json:"visitor_id"`
	Visits       int    `json:"visits"`
	PagesVisited int    `json:"pages_visited"`
	DurationSecs int    `json:"duration_seconds"`
	FirstVisit   string `json:"first_visit"`
	LastVisit    string `json:"last_visit"`
}

// VisitStats 访问统计汇总
type VisitStats struct {
	TotalVisitors  int                    `json:"total_visitors"`
	TotalPageViews int                    `json:"total_page_views"`
	Visitors       map[string]VisitorStat `json:"visitors"`
	TopPages       map[string]int         `json:"top_pages"`
}

// Open 打开 SQLite 数据库并执行迁移
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	os.MkdirAll(dir, 0755)

	conn, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1)

	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// Close 关闭数据库连接
func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS visit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		visitor_id TEXT NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL,
		status_code INTEGER DEFAULT 200,
		created_at TEXT DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_visit_visitor ON visit_logs(visitor_id);
	CREATE INDEX IF NOT EXISTS idx_visit_path ON visit_logs(path);

	CREATE TABLE IF NOT EXISTS orgs (
		id TEXT PRIMARY KEY,
		created_at TEXT DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS users (
		id TEXT NOT NULL,
		org_id TEXT NOT NULL,
		password_hash TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now')),
		PRIMARY KEY (org_id, id)
	);

	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		org_id TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'todo',
		priority TEXT NOT NULL DEFAULT 'medium',
		scheduled TEXT NOT NULL DEFAULT '',
		due TEXT NOT NULL DEFAULT '',
		progress INTEGER DEFAULT 0,
		assignee TEXT NOT NULL DEFAULT '',
		postponed_count INTEGER DEFAULT 0,
		auto_postponed INTEGER DEFAULT 0,
		sort_order INTEGER DEFAULT 0,
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now')),
		PRIMARY KEY (id, user_id, org_id)
	);
	`
	_, err := d.conn.Exec(schema)
	if err != nil {
		return err
	}
	// 自动迁移：补齐旧表缺失的列
	return d.migrateTasksColumns()
}

// migrateTasksColumns 检测 tasks 表列，自动补齐缺失列（兼容旧 schema）
func (d *DB) migrateTasksColumns() error {
	rows, err := d.conn.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defVal sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defVal, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	columns := []struct{ name, def string }{
		{"title", "TEXT NOT NULL DEFAULT ''"},
		{"content", "TEXT NOT NULL DEFAULT ''"},
		{"status", "TEXT NOT NULL DEFAULT 'todo'"},
		{"priority", "TEXT NOT NULL DEFAULT 'medium'"},
		{"scheduled", "TEXT NOT NULL DEFAULT ''"},
		{"due", "TEXT NOT NULL DEFAULT ''"},
		{"progress", "INTEGER DEFAULT 0"},
		{"assignee", "TEXT NOT NULL DEFAULT ''"},
		{"postponed_count", "INTEGER DEFAULT 0"},
		{"auto_postponed", "INTEGER DEFAULT 0"},
	}

	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		q := fmt.Sprintf("ALTER TABLE tasks ADD COLUMN %s %s", col.name, col.def)
		if _, err := d.conn.Exec(q); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}

	return nil
}

// ============================================================
// 访问统计
// ============================================================

// LogVisit 记录一次访问
func (d *DB) LogVisit(visitorID, ip, userAgent, path string, statusCode int) error {
	const q = `INSERT INTO visit_logs (visitor_id, ip, user_agent, path, status_code) VALUES (?, ?, ?, ?, ?)`
	_, err := d.conn.Exec(q, visitorID, ip, userAgent, path, statusCode)
	return err
}

// GetStats 查询访问统计
func (d *DB) GetStats() (*VisitStats, error) {
	stats := &VisitStats{
		Visitors: make(map[string]VisitorStat),
		TopPages: make(map[string]int),
	}

	row := d.conn.QueryRow(`SELECT COUNT(DISTINCT visitor_id), COUNT(*) FROM visit_logs`)
	if err := row.Scan(&stats.TotalVisitors, &stats.TotalPageViews); err != nil {
		return nil, fmt.Errorf("count visitors: %w", err)
	}

	vr, err := d.conn.Query(`
		SELECT visitor_id, COUNT(*), COUNT(DISTINCT path),
			MIN(created_at), MAX(created_at)
		FROM visit_logs GROUP BY visitor_id ORDER BY MAX(created_at) DESC`)
	if err != nil {
		return nil, fmt.Errorf("query visitors: %w", err)
	}
	defer vr.Close()
	for vr.Next() {
		var v VisitorStat
		var first, last string
		if err := vr.Scan(&v.VisitorID, &v.Visits, &v.PagesVisited, &first, &last); err != nil {
			return nil, fmt.Errorf("scan visitor: %w", err)
		}
		v.FirstVisit = first
		v.LastVisit = last
		v.DurationSecs = durationSeconds(first, last)
		stats.Visitors[v.VisitorID] = v
	}

	pr, err := d.conn.Query(`SELECT path, COUNT(*) FROM visit_logs GROUP BY path ORDER BY COUNT(*) DESC LIMIT 10`)
	if err != nil {
		return nil, fmt.Errorf("query top pages: %w", err)
	}
	defer pr.Close()
	for pr.Next() {
		var path string
		var count int
		if err := pr.Scan(&path, &count); err != nil {
			return nil, fmt.Errorf("scan page: %w", err)
		}
		stats.TopPages[path] = count
	}

	return stats, nil
}

func durationSeconds(start, end string) int {
	parse := func(s string) time.Time {
		for _, l := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z", "2006-01-02T15:04:05-07:00"} {
			if t, err := time.Parse(l, s); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	t1, t2 := parse(start), parse(end)
	if t1.IsZero() || t2.IsZero() {
		return 0
	}
	return int(t2.Sub(t1).Seconds())
}

// ============================================================
// 用户 / 组织 / 任务
// ============================================================

// FindUser 查找用户密码哈希
func (d *DB) FindUser(orgID, userID string) (passwordHash string, exists bool, err error) {
	const q = `SELECT password_hash FROM users WHERE org_id = ? AND id = ?`
	err = d.conn.QueryRow(q, orgID, userID).Scan(&passwordHash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return passwordHash, err == nil, err
}

// UpsertUser 创建或更新用户密码
func (d *DB) UpsertUser(orgID, userID, passwordHash string) error {
	const q = `INSERT INTO users (id, org_id, password_hash, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(org_id, id) DO UPDATE SET password_hash=excluded.password_hash, updated_at=datetime('now')`
	_, err := d.conn.Exec(q, userID, orgID, passwordHash)
	return err
}

// EnsureOrg 确保组织存在
func (d *DB) EnsureOrg(orgID string) error {
	const q = `INSERT OR IGNORE INTO orgs (id) VALUES (?)`
	_, err := d.conn.Exec(q, orgID)
	return err
}

// GetOrgMembers 获取组织成员列表
func (d *DB) GetOrgMembers(orgID string) ([]string, error) {
	rows, err := d.conn.Query(`SELECT id FROM users WHERE org_id = ?`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		members = append(members, id)
	}
	return members, nil
}

// FindUserOrg 查找用户所属组织
func (d *DB) FindUserOrg(userID string) (string, error) {
	const q = `SELECT org_id FROM users WHERE id = ? LIMIT 1`
	var orgID string
	err := d.conn.QueryRow(q, userID).Scan(&orgID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return orgID, err
}

// UpsertTasks 批量替换用户任务
func (d *DB) UpsertTasks(orgID, userID string, tasks []TaskItem) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM tasks WHERE org_id = ? AND user_id = ?`, orgID, userID); err != nil {
		return err
	}

	const q = `INSERT INTO tasks (id, user_id, org_id, title, content, status, priority, scheduled, due, progress, assignee, postponed_count, auto_postponed, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range tasks {
		autoP := 0
		if t.AutoPostponed {
			autoP = 1
		}
		if _, err := stmt.Exec(t.ID, userID, orgID, t.Title, t.Content, t.Status, t.Priority, t.Scheduled, t.Due, t.Progress, t.Assignee, t.PostponedCount, autoP, t.SortOrder); err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE users SET updated_at = datetime('now') WHERE org_id = ? AND id = ?`, orgID, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetTasks 获取用户任务
func (d *DB) GetTasks(orgID, userID string) ([]TaskItem, error) {
	rows, err := d.conn.Query(
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

// GetTasksJSON 获取完整任务数据（不含密码），一次性 JOIN 查询避免嵌套查询死锁
func (d *DB) GetTasksJSON() (map[string]interface{}, error) {
	result := map[string]interface{}{
		"version":     "1.0",
		"lastUpdated": time.Now().Format(time.RFC3339),
	}
	orgsMap := make(map[string]map[string]interface{})

	rows, err := d.conn.Query(`
		SELECT u.org_id, u.id, t.id, t.title, t.content, t.status, t.priority, t.scheduled, t.due, t.progress, t.assignee, t.postponed_count, t.auto_postponed, t.sort_order
		FROM users u
		LEFT JOIN tasks t ON t.org_id = u.org_id AND t.user_id = u.id
		ORDER BY u.org_id, u.id, t.sort_order
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type userData struct {
		tasks []TaskItem
	}
	orgUsers := make(map[string]map[string]*userData)

	for rows.Next() {
		var orgID, userID string
		var taskID, taskTitle, taskContent, taskStatus, taskPriority, taskScheduled, taskDue, taskAssignee sql.NullString
		var taskProgress, taskPostponed, taskAutoPostponed, sortOrder sql.NullInt64
		if err := rows.Scan(&orgID, &userID, &taskID, &taskTitle, &taskContent, &taskStatus, &taskPriority, &taskScheduled, &taskDue, &taskProgress, &taskAssignee, &taskPostponed, &taskAutoPostponed, &sortOrder); err != nil {
			return nil, err
		}
		if orgUsers[orgID] == nil {
			orgUsers[orgID] = make(map[string]*userData)
		}
		if orgUsers[orgID][userID] == nil {
			orgUsers[orgID][userID] = &userData{}
		}
		if taskID.Valid {
			orgUsers[orgID][userID].tasks = append(orgUsers[orgID][userID].tasks, TaskItem{
				ID:             taskID.String,
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
			usersMap[userID] = map[string]interface{}{
				"version": map[string]string{"md5": "init"},
				"tasks":   tasks,
			}
		}
		orgsMap[orgID] = usersMap
	}
	result["orgs"] = orgsMap
	return result, nil
}
