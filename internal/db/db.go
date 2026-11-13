// Package db SQLite 数据访问层
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"github.com/yiiewang/workbench/internal/sqlitevec"
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
	ID        FlexString `json:"id"`
	Title     string     `json:"title"`
	Content   string     `json:"content,omitempty"`
	Status    string     `json:"status"`
	Priority  string     `json:"priority"`
	Scheduled string     `json:"scheduled"`
	Due       string     `json:"due"`
	// ScheduledTime/DueTime 精确时刻（HH:mm，空 = 无精确时间，仅按日期粒度提醒）：
	// scheduled_time 到点发"开始提醒"，due_time 到点且未完成发"到期提醒"
	ScheduledTime  string     `json:"scheduledTime,omitempty"`
	DueTime        string     `json:"dueTime,omitempty"`
	Progress       int        `json:"progress"`
	Assignee       FlexString `json:"assignee"`
	PostponedCount int        `json:"postponedCount"`
	AutoPostponed  bool       `json:"autoPostponed"`
	SortOrder      int        `json:"sort_order,omitempty"`
	// ContentPreview 列表响应中的正文预览（截断）：
	// 列表/详情分离——列表不传 content 全文（响应瘦身），编辑/详情场景
	// 经 GET /api/tasks/{id} 拉取完整任务。详情与编辑保存场景不填此字段。
	ContentPreview string `json:"contentPreview,omitempty"`
	// Files 关联的文件路径（相对用户根目录），todo ↔ file 联动用。
	// omitempty + 读出归一为 nil：保证无关联任务的 md5 与旧版一致（recomputeVersion 兼容）
	Files []string `json:"files,omitempty"`
	// 向后兼容旧版字段
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// sqliteBusyTimeoutMs SQLite busy 超时（毫秒）
const sqliteBusyTimeoutMs = 5000

// Open 打开 SQLite 数据库并执行迁移
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory %s: %w", dir, err)
	}

	// 进程级注册 sqlite-vec 扩展（vec0 虚拟表）：此后所有新连接自动加载。
	// cgo 驱动（mattn/go-sqlite3）的代价是交叉编译需 xgo，换来向量 KNN 原生能力。
	sqlitevec.Auto()

	conn, err := sql.Open("sqlite3", fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on", dbPath, sqliteBusyTimeoutMs))
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
