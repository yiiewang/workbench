// Package db SQLite 数据访问层
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	path string
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
	Assignee       FlexString `json:"assignee"`
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

// sqliteBusyTimeoutMs SQLite busy 超时（毫秒）
const sqliteBusyTimeoutMs = 5000

// Open 打开 SQLite 数据库并执行迁移
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory %s: %w", dir, err)
	}

	conn, err := sql.Open("sqlite", fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on", dbPath, sqliteBusyTimeoutMs))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1)

	d := &DB{conn: conn, path: dbPath}
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
