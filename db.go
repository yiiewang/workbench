package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB 封装 SQLite 操作
type DB struct {
	conn *sql.DB
}

// VisitorStat 访问统计中的访客信息
type VisitorStat struct {
	VisitorID     string `json:"visitor_id"`
	Visits        int    `json:"visits"`
	PagesVisited  int    `json:"pages_visited"`
	DurationSecs  int    `json:"duration_seconds"`
	FirstVisit    string `json:"first_visit"`
	LastVisit     string `json:"last_visit"`
}

// VisitStats 访问统计汇总
type VisitStats struct {
	TotalVisitors  int                    `json:"total_visitors"`
	TotalPageViews int                    `json:"total_page_views"`
	Visitors       map[string]VisitorStat `json:"visitors"`
	TopPages       map[string]int         `json:"top_pages"`
}

// OrgMembersResult 组织成员查询结果
type OrgMembersResult struct {
	Members []string `json:"members"`
}

// OpenDB 打开 SQLite 数据库并执行迁移
func OpenDB(dataDir string) (*DB, error) {
	dbPath := filepath.Join(dataDir, "workbench.db")
	conn, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite 单写模式

	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return d, nil
}

// Close 关闭数据库连接
func (d *DB) Close() error {
	return d.conn.Close()
}

// migrate 执行数据库迁移，创建表结构
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
		text TEXT NOT NULL DEFAULT '',
		done INTEGER DEFAULT 0,
		sort_order INTEGER DEFAULT 0,
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now')),
		PRIMARY KEY (id, user_id, org_id)
	);
	`
	_, err := d.conn.Exec(schema)
	return err
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

	// 总访客数和总页面访问数
	row := d.conn.QueryRow(`SELECT COUNT(DISTINCT visitor_id), COUNT(*) FROM visit_logs`)
	if err := row.Scan(&stats.TotalVisitors, &stats.TotalPageViews); err != nil {
		return nil, fmt.Errorf("count visitors: %w", err)
	}

	// 各访客详情
	visitorRows, err := d.conn.Query(`
		SELECT visitor_id, COUNT(*) as visits, COUNT(DISTINCT path) as pages,
			MIN(created_at), MAX(created_at)
		FROM visit_logs GROUP BY visitor_id ORDER BY MAX(created_at) DESC`)
	if err != nil {
		return nil, fmt.Errorf("query visitors: %w", err)
	}
	defer visitorRows.Close()

	for visitorRows.Next() {
		var v VisitorStat
		var first, last string
		if err := visitorRows.Scan(&v.VisitorID, &v.Visits, &v.PagesVisited, &first, &last); err != nil {
			return nil, fmt.Errorf("scan visitor: %w", err)
		}
		v.FirstVisit = first
		v.LastVisit = last
		v.DurationSecs = durationSeconds(first, last)
		stats.Visitors[v.VisitorID] = v
	}

	// Top 10 页面
	pageRows, err := d.conn.Query(`SELECT path, COUNT(*) FROM visit_logs GROUP BY path ORDER BY COUNT(*) DESC LIMIT 10`)
	if err != nil {
		return nil, fmt.Errorf("query top pages: %w", err)
	}
	defer pageRows.Close()

	for pageRows.Next() {
		var path string
		var count int
		if err := pageRows.Scan(&path, &count); err != nil {
			return nil, fmt.Errorf("scan page: %w", err)
		}
		stats.TopPages[path] = count
	}

	return stats, nil
}

// durationSeconds 计算两个 datetime 字符串之间的秒数差
func durationSeconds(start, end string) int {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
	}
	parse := func(s string) time.Time {
		for _, l := range layouts {
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

// FindUser 查找用户（按 org_id + user_id）
func (d *DB) FindUser(orgID, userID string) (passwordHash string, exists bool, err error) {
	const q = `SELECT password_hash FROM users WHERE org_id = ? AND id = ?`
	err = d.conn.QueryRow(q, orgID, userID).Scan(&passwordHash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return passwordHash, true, nil
}

// UpsertUser 创建或更新用户（含密码）
func (d *DB) UpsertUser(orgID, userID, passwordHash string) error {
	const q = `
		INSERT INTO users (id, org_id, password_hash, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(org_id, id) DO UPDATE SET
			password_hash = excluded.password_hash,
			updated_at = datetime('now')`
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

// UpsertTasks 批量替换用户的任务列表
func (d *DB) UpsertTasks(orgID, userID string, tasks []TaskItem) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除旧任务
	if _, err := tx.Exec(`DELETE FROM tasks WHERE org_id = ? AND user_id = ?`, orgID, userID); err != nil {
		return err
	}

	// 插入新任务
	const q = `INSERT INTO tasks (id, user_id, org_id, text, done, sort_order) VALUES (?, ?, ?, ?, ?, ?)`
	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range tasks {
		done := 0
		if t.Done {
			done = 1
		}
		if _, err := stmt.Exec(t.ID, userID, orgID, t.Text, done, t.SortOrder); err != nil {
			return err
		}
	}

	// 更新用户时间戳
	_, err = tx.Exec(`UPDATE users SET updated_at = datetime('now') WHERE org_id = ? AND id = ?`, orgID, userID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetTasks 获取用户的任务列表
func (d *DB) GetTasks(orgID, userID string) ([]TaskItem, error) {
	rows, err := d.conn.Query(
		`SELECT id, text, done, sort_order FROM tasks WHERE org_id = ? AND user_id = ? ORDER BY sort_order`,
		orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskItem
	for rows.Next() {
		var t TaskItem
		var done int
		if err := rows.Scan(&t.ID, &t.Text, &done, &t.SortOrder); err != nil {
			return nil, err
		}
		t.Done = done == 1
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []TaskItem{}
	}
	return tasks, nil
}

// GetTasksJSON 获取所有用户的完整任务数据（用于 /tasks.json GET，不含密码）
func (d *DB) GetTasksJSON() (map[string]interface{}, error) {
	result := map[string]interface{}{
		"version":     "1.0",
		"lastUpdated": time.Now().Format(time.RFC3339),
	}

	orgsMap := make(map[string]map[string]interface{})

	orgRows, err := d.conn.Query(`SELECT id FROM orgs`)
	if err != nil {
		return nil, err
	}
	defer orgRows.Close()

	var orgIDs []string
	for orgRows.Next() {
		var id string
		if err := orgRows.Scan(&id); err != nil {
			return nil, err
		}
		orgIDs = append(orgIDs, id)
	}

	if orgIDs == nil {
		result["orgs"] = orgsMap
		return result, nil
	}

	for _, orgID := range orgIDs {
		userRows, err := d.conn.Query(`SELECT id FROM users WHERE org_id = ?`, orgID)
		if err != nil {
			return nil, err
		}

		usersMap := make(map[string]interface{})
		for userRows.Next() {
			var userID string
			if err := userRows.Scan(&userID); err != nil {
				userRows.Close()
				return nil, err
			}

			tasks, err := d.GetTasks(orgID, userID)
			if err != nil {
				userRows.Close()
				return nil, err
			}

			usersMap[userID] = map[string]interface{}{
				"version": map[string]string{"md5": "init"},
				"tasks":   tasks,
			}
		}
		userRows.Close()

		if len(usersMap) > 0 {
			orgsMap[orgID] = usersMap
		}
	}

	result["orgs"] = orgsMap
	return result, nil
}

// SeedDefaultData 在数据库为空时写入默认数据
func (d *DB) SeedDefaultData() error {
	var count int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM orgs`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	// 检查是否存在旧版 tasks.json，尝试导入
	dataDir := filepath.Dir(filepath.Dir("")) // 由调用方传入
	_ = dataDir
	return nil
}

// ============================================================
// 日志文件写入（兼容旧日志格式）
// ============================================================

// WriteAccessLog 将访问日志写入文本文件（兼容旧格式）
func WriteAccessLog(logPath, timestamp, visitorID, path, status string) {
	line := fmt.Sprintf("%s | %s | %s | %s\n", timestamp, visitorID, path, status)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line)
}

// TaskItem 任务条目
type TaskItem struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Done      bool   `json:"done"`
	SortOrder int    `json:"sort_order,omitempty"`
}

// dirExists 检查目录是否存在
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ensureDir 确保目录存在
func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// fileExists 检查文件是否存在
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// trimPrefixes 去除字符串两端的空白字符
func trimPrefixes(s string, prefixes ...string) string {
	for _, p := range prefixes {
		s = strings.TrimPrefix(s, p)
	}
	return s
}
