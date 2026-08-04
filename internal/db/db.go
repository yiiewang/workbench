// Package db SQLite 数据访问层
package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// FlexString 兼容 JSON 中 string 和 number 两种类型的字符串字段
type FlexString string

// UnmarshalJSON 兼容 string 和 number，统一转为 string
func (f *FlexString) UnmarshalJSON(data []byte) error {
	// 尝试作为 string 解析
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	// 尝试作为 number 解析
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexString(n.String())
		return nil
	}
	return fmt.Errorf("FlexString: cannot unmarshal %s into string", string(data))
}

// Scan 实现 sql.Scanner 接口，支持从数据库读取
func (f *FlexString) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		*f = FlexString(v)
	case []byte:
		*f = FlexString(string(v))
	case nil:
		*f = ""
	default:
		return fmt.Errorf("FlexString: cannot scan %T into string", src)
	}
	return nil
}

// String 返回底层 string 值
func (f FlexString) String() string {
	return string(f)
}

// DB 封装 SQLite 操作
type DB struct {
	conn *sql.DB
}

// TaskItem 任务条目
type TaskItem struct {
	ID             FlexString `json:"id"`
	Title          string     `json:"title"`
	Content        string     `json:"content"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	Scheduled      string     `json:"scheduled"`
	Due            string     `json:"due"`
	Progress       int        `json:"progress"`
	Assignee       string     `json:"assignee"`
	PostponedCount int        `json:"postponedCount"`
	AutoPostponed  bool       `json:"autoPostponed"`
	SortOrder      int        `json:"sort_order,omitempty"`
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
		version_json TEXT DEFAULT '',
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

	CREATE TABLE IF NOT EXISTS app_secrets (
		key TEXT PRIMARY KEY,
		value BLOB NOT NULL,
		created_at TEXT DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS shares (
		id TEXT PRIMARY KEY,
		token TEXT UNIQUE NOT NULL,
		owner_user_id TEXT NOT NULL,
		owner_org_id TEXT NOT NULL,
		resource_path TEXT NOT NULL,
		resource_type TEXT NOT NULL DEFAULT 'file',
		max_access_count INTEGER DEFAULT 0,
		access_count INTEGER DEFAULT 0,
		password_hash TEXT DEFAULT '',
		remark TEXT DEFAULT '',
		effective_at TEXT DEFAULT '',
		expires_at TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_shares_owner ON shares(owner_user_id, owner_org_id);
	CREATE INDEX IF NOT EXISTS idx_shares_token ON shares(token);
	`
	_, err := d.conn.Exec(schema)
	if err != nil {
		return err
	}
	// 自动迁移：补齐旧表缺失的列
	if err := d.migrateTasksColumns(); err != nil {
		return err
	}
	if err := d.migrateSharesColumns(); err != nil {
		return err
	}
	return d.migrateUsersColumns()
}

// migrateUsersColumns 检测 users 表列，自动补齐缺失列（兼容旧 schema）
func (d *DB) migrateUsersColumns() error {
	rows, err := d.conn.Query(`PRAGMA table_info(users)`)
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

	if !existing["version_json"] {
		if _, err := d.conn.Exec(`ALTER TABLE users ADD COLUMN version_json TEXT DEFAULT ''`); err != nil {
			return fmt.Errorf("add column version_json: %w", err)
		}
	}

	return nil
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

// migrateSharesColumns 检测 shares 表列，自动补齐缺失列（兼容旧 schema）
func (d *DB) migrateSharesColumns() error {
	rows, err := d.conn.Query(`PRAGMA table_info(shares)`)
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
		{"remark", "TEXT DEFAULT ''"},
	}

	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		q := fmt.Sprintf("ALTER TABLE shares ADD COLUMN %s %s", col.name, col.def)
		if _, err := d.conn.Exec(q); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}

	return nil
}

// ============================================================
// 应用密钥
// ============================================================

// LoadOrCreateSecret 从数据库加载指定密钥；不存在时优先迁移旧文件，再无则生成新密钥。
// legacyFilePath 非空且 db 无记录时，尝试读取旧文件并迁移入库、删除旧文件。
func (d *DB) LoadOrCreateSecret(key, legacyFilePath string) ([]byte, error) {
	// 1. 查 db
	var val []byte
	err := d.conn.QueryRow(`SELECT value FROM app_secrets WHERE key = ?`, key).Scan(&val)
	if err == nil {
		return val, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query secret %s: %w", key, err)
	}
	// 2. db 无记录，尝试迁移旧文件
	if legacyFilePath != "" {
		if data, err := os.ReadFile(legacyFilePath); err == nil {
			if _, err := d.conn.Exec(`INSERT INTO app_secrets (key, value) VALUES (?, ?)`, key, data); err != nil {
				return nil, fmt.Errorf("migrate secret %s: %w", key, err)
			}
			os.Remove(legacyFilePath) // 迁移成功删除旧文件
			return data, nil
		}
	}
	// 3. 都没有，生成新密钥
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate secret %s: %w", key, err)
	}
	if _, err := d.conn.Exec(`INSERT INTO app_secrets (key, value) VALUES (?, ?)`, key, secret); err != nil {
		return nil, fmt.Errorf("insert secret %s: %w", key, err)
	}
	return secret, nil
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

// HasAnyUser 判断系统是否已存在任意用户（用于 set-password 首次初始化判断）
func (d *DB) HasAnyUser() (bool, error) {
	var count int
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count > 0, err
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

// UpsertTasks 批量替换用户任务，同时存储客户端发来的 version JSON
func (d *DB) UpsertTasks(orgID, userID string, tasks []TaskItem, versionJSON string) error {
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
		if _, err := stmt.Exec(string(t.ID), userID, orgID, t.Title, t.Content, t.Status, t.Priority, t.Scheduled, t.Due, t.Progress, t.Assignee, t.PostponedCount, autoP, t.SortOrder); err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE users SET updated_at = datetime('now'), version_json = ? WHERE org_id = ? AND id = ?`, versionJSON, orgID, userID)
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

// ============================================================
// 分享管理
// ============================================================

// Share 分享记录
type Share struct {
	ID             string `json:"id"`
	Token          string `json:"token"`
	OwnerUserID    string `json:"ownerUserId"`
	OwnerOrgID     string `json:"ownerOrgId"`
	ResourcePath   string `json:"resourcePath"`
	ResourceType   string `json:"resourceType"` // "file" | "dir"
	MaxAccessCount int    `json:"maxAccessCount"`
	AccessCount    int    `json:"accessCount"`
	PasswordHash   string `json:"-"` // 不序列化
	HasPassword    bool   `json:"hasPassword"`
	Remark         string `json:"remark"` // 分享备注（复制链接时附带）
	EffectiveAt    string `json:"effectiveAt"`
	ExpiresAt      string `json:"expiresAt"`
	CreatedAt      string `json:"createdAt"`
}

// CreateShare 创建分享记录
func (d *DB) CreateShare(s *Share) error {
	const q = `INSERT INTO shares (id, token, owner_user_id, owner_org_id, resource_path, resource_type, max_access_count, access_count, password_hash, remark, effective_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`
	_, err := d.conn.Exec(q, s.ID, s.Token, s.OwnerUserID, s.OwnerOrgID, s.ResourcePath, s.ResourceType,
		s.MaxAccessCount, s.PasswordHash, s.Remark, s.EffectiveAt, s.ExpiresAt)
	return err
}

// ListSharesByOwner 查询用户的所有分享
func (d *DB) ListSharesByOwner(orgID, userID string) ([]Share, error) {
	rows, err := d.conn.Query(`SELECT id, token, owner_user_id, owner_org_id, resource_path, resource_type,
		max_access_count, access_count, password_hash, remark, effective_at, expires_at, created_at
		FROM shares WHERE owner_org_id = ? AND owner_user_id = ? ORDER BY created_at DESC`, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []Share
	for rows.Next() {
		var s Share
		if err := rows.Scan(&s.ID, &s.Token, &s.OwnerUserID, &s.OwnerOrgID, &s.ResourcePath, &s.ResourceType,
			&s.MaxAccessCount, &s.AccessCount, &s.PasswordHash, &s.Remark, &s.EffectiveAt, &s.ExpiresAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.HasPassword = s.PasswordHash != ""
		shares = append(shares, s)
	}
	return shares, nil
}

// GetShareByToken 通过 token 查询分享（用于公开访问）
func (d *DB) GetShareByToken(token string) (*Share, error) {
	var s Share
	err := d.conn.QueryRow(`SELECT id, token, owner_user_id, owner_org_id, resource_path, resource_type,
		max_access_count, access_count, password_hash, remark, effective_at, expires_at, created_at
		FROM shares WHERE token = ?`, token).Scan(
		&s.ID, &s.Token, &s.OwnerUserID, &s.OwnerOrgID, &s.ResourcePath, &s.ResourceType,
		&s.MaxAccessCount, &s.AccessCount, &s.PasswordHash, &s.Remark, &s.EffectiveAt, &s.ExpiresAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.HasPassword = s.PasswordHash != ""
	return &s, nil
}

// DeleteShare 删除分享（仅 owner 可删）
func (d *DB) DeleteShare(orgID, userID, shareID string) error {
	const q = `DELETE FROM shares WHERE id = ? AND owner_org_id = ? AND owner_user_id = ?`
	res, err := d.conn.Exec(q, shareID, orgID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("share not found or no permission")
	}
	return nil
}

// IncrementShareAccessCount 增加访问计数，返回更新后的值；达到上限返回 false
func (d *DB) IncrementShareAccessCount(token string) (newCount int, limitReached bool, err error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	var accessCount, maxCount int
	err = tx.QueryRow(`SELECT access_count, max_access_count FROM shares WHERE token = ?`, token).Scan(&accessCount, &maxCount)
	if err == sql.ErrNoRows {
		return 0, false, fmt.Errorf("share not found")
	}
	if err != nil {
		return 0, false, err
	}

	accessCount++
	if maxCount > 0 && accessCount > maxCount {
		return accessCount, true, nil
	}

	_, err = tx.Exec(`UPDATE shares SET access_count = ?, updated_at = datetime('now') WHERE token = ?`, accessCount, token)
	if err != nil {
		return 0, false, err
	}
	return accessCount, false, tx.Commit()
}
func (d *DB) GetTasksJSON() (map[string]interface{}, error) {
	result := map[string]interface{}{
		"version":     "1.0",
		"lastUpdated": time.Now().Format(time.RFC3339),
	}
	orgsMap := make(map[string]map[string]interface{})

	rows, err := d.conn.Query(`
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
