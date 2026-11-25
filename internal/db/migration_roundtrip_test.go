package db

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// 000021 回滚：down 把行级消息聚合回 ai_sessions.messages，再 up 能重新展开导入，
// 往返不丢消息、顺序与展示字段保持一致。
//
// 测试连接刻意关闭 foreign_keys：造数据不必先铺 users/org 记录；
// 迁移脚本本身不依赖外键，ai_session_messages 也刻意没有外键（见 up 脚本注释）。
// 单连接是必要的：000003/000007 的 up 脚本结尾有 PRAGMA foreign_keys=ON，
// Up() 之后要显式关掉，且后续查询必须复用同一连接才能保持该 PRAGMA。
func TestMigration000021RoundTrip(t *testing.T) {
	conn, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "migrate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite3.WithInstance(conn, &sqlite3.Config{NoTxWrap: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("up: %v", err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(`INSERT INTO ai_sessions (id, user_id, org_id, title) VALUES (1, 1, 1, 't')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO ai_session_messages
		(session_id, client_msg_id, org_id, user_id, role, content, extras) VALUES
		(1, 'm1', 1, 1, 'user', '问题', '{"reasoning":"想了一下"}'),
		(1, 'm2', 1, 1, 'assistant', '回答', '{"usage":{"totalTokens":10}}')`); err != nil {
		t.Fatal(err)
	}

	// 按版本回滚到 000020：同时 down 掉后续迁移（如 000022），
	// 避免后续新增迁移时本测试依赖「最新版本恰好是 000021」。
	if err := m.Migrate(20); err != nil {
		t.Fatalf("down to 20: %v", err)
	}

	var messages string
	if err := conn.QueryRow(`SELECT messages FROM ai_sessions WHERE id = 1`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(messages), &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 2 || arr[0]["role"] != "user" || arr[0]["content"] != "问题" || arr[0]["reasoning"] != "想了一下" {
		t.Fatalf("down messages mismatch: %s", messages)
	}
	if arr[1]["role"] != "assistant" || arr[1]["content"] != "回答" {
		t.Fatalf("down message order/extras mismatch: %s", messages)
	}
	var tables int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ai_session_messages'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("ai_session_messages should be dropped after down")
	}

	// 再 up：旧列重新按下标展开导入，数据不丢，messages 列再次移除
	if err := m.Steps(1); err != nil {
		t.Fatalf("re-up 000021: %v", err)
	}
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ai_session_messages WHERE session_id = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("re-up messages count: %d", count)
	}
	var firstID, lastID, content string
	if err := conn.QueryRow(`SELECT client_msg_id, content FROM ai_session_messages WHERE session_id = 1 ORDER BY id LIMIT 1`).Scan(&firstID, &content); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT client_msg_id FROM ai_session_messages WHERE session_id = 1 ORDER BY id DESC LIMIT 1`).Scan(&lastID); err != nil {
		t.Fatal(err)
	}
	if firstID != "idx:0" || content != "问题" || lastID != "idx:1" {
		t.Fatalf("re-up ids/order mismatch: %s %s %s", firstID, content, lastID)
	}
	var cols int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ai_sessions') WHERE name='messages'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != 0 {
		t.Fatal("messages column should be dropped after re-up")
	}
}

// 生产库回归：用户被删除后遗留的孤儿会话（user_id 不在 users 表）不能阻断 000021，
// 否则表重建时 INSERT ... SELECT 被外键拒绝，会留下 dirty version 21。
func TestMigration000021WithOrphanSession(t *testing.T) {
	conn, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "orphan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite3.WithInstance(conn, &sqlite3.Config{NoTxWrap: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(20); err != nil {
		t.Fatalf("migrate to 20: %v", err)
	}

	// 000007 的 up 结尾会打开 FK；造孤儿数据前临时关闭，造完恢复到生产状态
	if _, err := conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO ai_sessions (id, user_id, org_id, title, messages)
		VALUES (1, 999, 1, 'orphan', '[{"role":"user","content":"hi"}]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}

	// 只升到 000021：本用例验证孤儿会话不阻断该迁移，后续版本升级由其他用例覆盖。
	if err := m.Migrate(21); err != nil {
		t.Fatalf("up 000021 with orphan session: %v", err)
	}

	var sessions, messages, cols int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ai_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ai_session_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ai_sessions') WHERE name='messages'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || messages != 1 || cols != 0 {
		t.Fatalf("migration result: sessions=%d messages=%d messagesColumn=%d", sessions, messages, cols)
	}
	var content string
	if err := conn.QueryRow(`SELECT content FROM ai_session_messages WHERE session_id = 1`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "hi" {
		t.Fatalf("orphan session messages not imported: %q", content)
	}
	var version, dirty int
	if err := conn.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != 21 || dirty != 0 {
		t.Fatalf("schema_migrations: version=%d dirty=%d", version, dirty)
	}
}

// 000023 调度收敛：job 规则回填、ai_jobs 去掉调度列，回滚能恢复。
func TestMigration000023ScheduleConverge(t *testing.T) {
	conn, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "converge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite3.WithInstance(conn, &sqlite3.Config{NoTxWrap: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(22); err != nil {
		t.Fatalf("migrate to 22: %v", err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	// 清掉 seed 的全局任务，避免 000023 回填出额外规则。
	if _, err := conn.Exec(`DELETE FROM ai_jobs WHERE user_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO ai_jobs (id, user_id, org_id, name, cron_expr, prompt, enabled, target_type, target_json) VALUES
		(10, 1, 1, '日报', '0 9 * * *', '写日报', 1, 'ai', '{"type":"ai","prompt":"写日报"}'),
		(11, 1, 1, '手动', '', 'x', 1, 'ai', '{"type":"ai","prompt":"x"}')`); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(23); err != nil {
		t.Fatalf("migrate to 23: %v", err)
	}

	var rules int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schedule_triggers WHERE target_type='job'`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != 1 {
		t.Fatalf("job rules = %d, want 1 (manual job excluded)", rules)
	}
	var targetID int64
	var cron string
	if err := conn.QueryRow(`SELECT target_id, cron_expr FROM schedule_triggers WHERE target_type='job'`).Scan(&targetID, &cron); err != nil {
		t.Fatal(err)
	}
	if targetID != 10 || cron != "0 9 * * *" {
		t.Fatalf("backfilled rule mismatch: id=%d cron=%q", targetID, cron)
	}
	var scheduleCols int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ai_jobs') WHERE name IN ('cron_expr','enabled','last_run_at','last_status')`).Scan(&scheduleCols); err != nil {
		t.Fatal(err)
	}
	if scheduleCols != 0 {
		t.Fatalf("ai_jobs still has %d schedule columns", scheduleCols)
	}
	var targetJSON string
	if err := conn.QueryRow(`SELECT target_json FROM ai_jobs WHERE id = 10`).Scan(&targetJSON); err != nil {
		t.Fatal(err)
	}
	if targetJSON != `{"type":"ai","prompt":"写日报"}` {
		t.Fatalf("target_json lost: %s", targetJSON)
	}

	if err := m.Migrate(22); err != nil {
		t.Fatalf("down to 22: %v", err)
	}
	var enabled int
	if err := conn.QueryRow(`SELECT cron_expr, enabled FROM ai_jobs WHERE id = 10`).Scan(&cron, &enabled); err != nil {
		t.Fatal(err)
	}
	if cron != "0 9 * * *" || enabled != 1 {
		t.Fatalf("rollback did not restore schedule columns: cron=%q enabled=%d", cron, enabled)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schedule_triggers WHERE target_type='job'`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != 0 {
		t.Fatalf("job rules should be removed on rollback, got %d", rules)
	}
}

// 000024 执行历史详情列：up 加列、down 删除，存量行默认空值。
func TestMigration000024RunDetail(t *testing.T) {
	conn, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "rundetail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite3.WithInstance(conn, &sqlite3.Config{NoTxWrap: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(23); err != nil {
		t.Fatalf("migrate to 23: %v", err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO ai_job_runs (job_id, ran_at, status, output) VALUES (1, '2026-11-25 09:00', 'ok', 'x')`); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(24); err != nil {
		t.Fatalf("migrate to 24: %v", err)
	}
	var targetType, pipelineRunID string
	var durationMs int64
	if err := conn.QueryRow(`SELECT target_type, pipeline_run_id, duration_ms FROM ai_job_runs WHERE job_id = 1`).
		Scan(&targetType, &pipelineRunID, &durationMs); err != nil {
		t.Fatal(err)
	}
	if targetType != "" || pipelineRunID != "" || durationMs != 0 {
		t.Fatalf("existing run should have empty detail: %q %q %d", targetType, pipelineRunID, durationMs)
	}

	if err := m.Migrate(23); err != nil {
		t.Fatalf("down to 23: %v", err)
	}
	var cols int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ai_job_runs') WHERE name IN
		('target_type','pipeline_run_id','steps_summary','err_msg','duration_ms','finished_at')`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != 0 {
		t.Fatalf("detail columns should be dropped, got %d", cols)
	}
}
