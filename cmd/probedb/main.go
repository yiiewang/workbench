package main

// 临时诊断：读生产库 AI 会话（sqlite3 CLI 版本过旧读不了 WAL，用项目同款驱动读）

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	d, err := sql.Open("sqlite3", os.Args[1]+"?_journal_mode=WAL")
	if err != nil {
		panic(err)
	}
	defer d.Close()

	rows, err := d.Query(`SELECT id, user_id, length(messages), substr(title,1,30), updated_at FROM ai_sessions ORDER BY id`)
	if err != nil {
		panic(err)
	}
	for rows.Next() {
		var id, uid, l int
		var title, updated string
		_ = rows.Scan(&id, &uid, &l, &title, &updated)
		fmt.Printf("session id=%d user=%d len=%d title=%q updated=%s\n", id, uid, l, title, updated)
	}
	rows.Close()

	rows2, err := d.Query(`SELECT id, substr(messages, -3500) FROM ai_sessions ORDER BY id DESC LIMIT 1`)
	if err != nil {
		panic(err)
	}
	for rows2.Next() {
		var id int
		var tail string
		_ = rows2.Scan(&id, &tail)
		fmt.Printf("=== 会话 %d 末尾 3500 字符 ===\n%s\n", id, tail)
	}
	rows2.Close()
}
