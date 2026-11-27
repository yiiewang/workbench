package db

import (
	"database/sql"
	"testing"
)

// squash 后的 baseline 只验证最终 schema：空库建库后关键表/列齐全。
// 历史专项迁移测试（000021/000023/000024/000025/000026）随 squash 移除。
func TestMigrationsBaselineSchema(t *testing.T) {
	d := newTestDB(t) // Open 内已执行 migrate

	tables := []string{
		"orgs", "users", "roles", "user_orgs", "user_org_feature", "shares",
		"rate_limits", "app_secrets", "notify_log", "tasks",
		"file_index_state", "file_chunks", "rag_meta",
		"ai_sessions", "ai_session_messages", "ai_runs",
		"pipeline_runs", "jobs", "job_runs", "schedule_triggers",
	}
	for _, name := range tables {
		ok, err := hasTable(d.conn, name)
		if err != nil || !ok {
			t.Fatalf("table %s missing: ok=%v err=%v", name, ok, err)
		}
	}

	// 关键列：squash 时容易漏掉后期增量列
	cols := []struct{ table, col string }{
		{"users", "email"},
		{"tasks", "files"}, {"tasks", "scheduled_time"}, {"tasks", "due_time"},
		{"file_chunks", "start_line"},
		{"schedule_triggers", "lease_until"}, {"schedule_triggers", "finished_at"},
		{"jobs", "retry"}, {"job_runs", "finished_at"}, {"job_runs", "pipeline_run_id"},
	}
	for _, tc := range cols {
		ok, err := hasColumn(d.conn, tc.table, tc.col)
		if err != nil || !ok {
			t.Fatalf("column %s.%s missing: ok=%v err=%v", tc.table, tc.col, ok, err)
		}
	}
}

func hasColumn(conn *sql.DB, table, col string) (bool, error) {
	rows, err := conn.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}
