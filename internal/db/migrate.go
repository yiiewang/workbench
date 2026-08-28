package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrationsFS 内嵌版本化迁移脚本（golang-migrate 格式：{序号}_{描述}.up.sql / .down.sql）
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate 使用 golang-migrate 执行版本化迁移（取代旧的自愈式迁移）。
// 每次 schema 变更加一个迁移脚本，按版本号有序、一次性执行，可回滚、可追溯。
//
// 版本链：
//
//	000001_baseline           阶段 A 前旧态（单组织 org_id/role_id）
//	000002_rbac_multi_org     阶段 A：加 user_orgs/org_features + is_platform_admin + 回填
//	000003_drop_legacy_columns 删除 org_id/role_id 遗留列
//
// 存量库基线：golang-migrate 引入前的库没有 schema_migrations 版本记录，
// 统一视为已处于 baseline（version 1），Force(1) 后仅执行后续增量脚本。
func (d *DB) migrate() error {
	// 基线检测必须先于 sqlite.WithInstance 执行：
	// WithInstance 会调用 ensureVersionTable 提前创建 schema_migrations 表，
	// 否则会误判「已被 golang-migrate 管理」。
	base, err := detectBaseVersion(d.conn)
	if err != nil {
		return err
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	// NoTxWrap=true：SQLite 的 PRAGMA foreign_keys 在事务内是 no-op，
	// 删列等表重建迁移（000003）需要在事务外执行。
	driver, err := sqlite.WithInstance(d.conn, &sqlite.Config{NoTxWrap: true})
	if err != nil {
		return fmt.Errorf("init sqlite driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}

	if base > 0 {
		// 存量库：跳到已应用的版本，只执行后续增量
		if err := m.Force(base); err != nil {
			return fmt.Errorf("force legacy baseline: %w", err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// hasTable 判断表是否存在于当前数据库
func hasTable(conn *sql.DB, name string) (bool, error) {
	var n int
	err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// detectBaseVersion 返回存量库基线版本。只认 golang-migrate 自己的版本表与空库信号，
// 不做表清单探测——那又退回自愈式迁移，需持续维护。
//
//	-1 → 已被 golang-migrate 管理（schema_migrations 存在），直接 Up
//	 0 → 空库（无 users 表），从 000001 起全量迁移
//	 1 → 存量库（有 users 表但无 schema_migrations），Force(1) 后执行增量
func detectBaseVersion(conn *sql.DB) (int, error) {
	managed, err := hasTable(conn, "schema_migrations")
	if err != nil {
		return 0, err
	}
	if managed {
		return -1, nil
	}

	hasUsers, err := hasTable(conn, "users")
	if err != nil {
		return 0, err
	}
	if hasUsers {
		return 1, nil
	}
	return 0, nil // 空库
}
