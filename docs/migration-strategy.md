# 数据库版本化迁移方案（golang-migrate）

> 版本：v1 ｜ 日期：2026-08-21 ｜ 状态：方案评审中（未实施）

## 1. 目标与原则

用 **golang-migrate 版本化迁移** 取代当前的 **自愈式迁移（detect-and-repair）**。

原则：

1. **每个 schema 变更一个迁移脚本**，按版本号有序、一次性执行。
2. **不做自愈式迁移**——不再用 `tableHasColumn` 检测 + `ALTER TABLE ADD COLUMN` 补列这类运行期探测逻辑，平添无用代码。
3. **可回滚**（`down` 脚本）、**可追溯**（`schema_migrations` 版本表）。
4. 保持项目约束：`CGO_ENABLED=0`、单二进制 embed、SQLite（`modernc.org/sqlite`）。

## 2. 现状问题（为什么改）

当前 `internal/db/migrate.go` 的 `migrate()` 是「启动时探测 + 修复」模式，存在以下弊端：

| 问题 | 说明 |
|:---|:---|
| 无版本号 | 无法表达「第 N 版做了什么」，无法按序演进 |
| 无回滚 | 只有 forward，没有 down |
| 逻辑随历史漂移 | `migrateUserOrgsFeatures` 里的 `CASE u.role_id` 依赖 `role_id` 列，未来删列后该函数语义失效，但代码仍残留在启动路径 |
| 自愈逻辑膨胀 | `migrateLegacySchema` / `migrateFromTextPK`（表重建）/ `migrateUsersColumns` / `migrateTasksColumns` / `migrateSharesColumns` / `migrateVisitLogsColumns` 等一堆 `IF NOT EXISTS` 补列函数，全部是「为了兼容存量库」的探测逻辑 |
| 无法协作 | 团队协作时，schema 变更无法以 diff/脚本形式 review |

## 3. 技术选型

| 组件 | 选择 | 理由 |
|:---|:---|:---|
| 迁移框架 | `github.com/golang-migrate/migrate/v4` | Go 生态标准库，CLI + 库双用 |
| SQLite driver | `database/sqlite`（`github.com/golang-migrate/migrate/v4/database/sqlite`） | 基于 `modernc.org/sqlite`，**纯 Go、无需 CGO**，满足 `CGO_ENABLED=0` |
| 迁移源 | `source/iofs` + `//go:embed migrations/*.sql` | 迁移文件内嵌二进制，符合单二进制部署 |
| 版本表 | `schema_migrations`（golang-migrate 自动维护） | `(version BIGINT PRIMARY KEY, dirty BOOLEAN)` |

> ⚠️ 不要用 `database/sqlite3`（mattn/go-sqlite3），它需要 CGO，与本项目 `CGO_ENABLED=0` 冲突。

## 4. 目录结构与集成

```
internal/db/
├── migrations/                      # 迁移文件（//go:embed 内嵌）
│   ├── 000001_baseline.up.sql       # 完整现有 schema（baseline）
│   ├── 000001_baseline.down.sql     # 全量 DROP（回滚到空库）
│   └── （未来）000002_xxx.up.sql / .down.sql
├── migrate.go                       # 迁移 runner（替换现有 migrate()）
└── migrate_legacy_test.go           # 存量库基线测试
```

集成方式（库模式，非 CLI）：

```go
import (
    "embed"
    "github.com/golang-migrate/migrate/v4"
    "github.com/golang-migrate/migrate/v4/database/sqlite"
    "github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func migrate(db *sql.DB) error {
    src, err := iofs.New(migrationsFS, "migrations")
    if err != nil { return err }

    driver, err := sqlite.WithInstance(db, &sqlite.Config{})
    if err != nil { return err }

    m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
    if err != nil { return err }

    // 存量库基线检测（见 §6）
    if isLegacyDB(db) {
        if err := m.Force(1); err != nil { return err }
    }

    if err := m.Up(); err != nil && err != migrate.ErrNoChange {
        return err
    }
    return nil
}
```

> 迁移文件内嵌到二进制，无需外部 SQL 文件、无需 CLI。

## 5. 版本序列设计

### 5.1 决策：历史用「单 baseline」而非「精确版本重放」

现有 schema 已经历多次演进（TEXT 主键 → 整数主键 → 账号体系 → 分享 → 限流 → 账号隔离 → RBAC），但**存量库已经处于最终态**，且我们没有留存每次演进的历史变更脚本。因此：

- **不重放历史**——用 `000001_baseline` 一次性固化「当前完整 schema」。
- **未来增量**——从 `000002` 起，每个 schema 变更加一个版本。

这避免了「为了重放不存在的历史而硬造版本号」的无用功，同时满足「每个版本一个迁移脚本」的要求（针对未来）。

### 5.2 现有表清单（`000001_baseline` 内容）

| 表 | 说明 |
|:---|:---|
| `orgs` | 组织 |
| `roles` | 角色字典（admin/user/org_admin） |
| `users` | 用户（含 `org_id`、`role_id`、`is_platform_admin`） |
| `user_orgs` | 用户-组织多对多（RBAC） |
| `org_features` | 组织-功能映射（RBAC） |
| `tasks` | 待办任务 |
| `visit_logs` | 访问日志 |
| `app_secrets` | 应用密钥 |
| `shares` | 分享 |
| `rate_limits` | 限流持久化 |

`000001_baseline.up.sql` = 上述全部表的 `CREATE TABLE IF NOT EXISTS` + 索引（等价于现有 `newSchema`），另含 `seedRoles` 的 3 条角色 INSERT。

`000001_baseline.down.sql` = 逆序 `DROP TABLE IF EXISTS` 全部表。

> baseline 的 `up.sql` 用 `IF NOT EXISTS` 是为存量库幂等（存量库 force 后不执行它，但新库执行它需要正常建表）；**后续 `000002+` 的脚本不再用 `IF NOT EXISTS`**，必须精确表达增量变更。

### 5.3 未来增量规范（示例）

```
000002_drop_legacy_columns.up.sql    # ALTER TABLE users DROP COLUMN org_id / role_id（配合 RBAC 收尾）
000002_drop_legacy_columns.down.sql  # 重新 ADD COLUMN（含数据回填）
```

## 6. 存量库基线策略（极简，非自愈）

存量库 `data/workbench.db` 已具备完整 schema，但**从未用 golang-migrate 管理**（无 `schema_migrations` 表）。若直接 `m.Up()`，会从 version 0 开始执行 `000001_baseline`，因表已存在而报错。

**基线检测（唯一的一次性判断，仅一个查询）**：

```go
// isLegacyDB 判断是否为「已存在但未纳入 golang-migrate 管理」的存量库：
//   核心表 users 已存在  &&  schema_migrations 表不存在
func isLegacyDB(db *sql.DB) bool {
    var n int
    _ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
        WHERE type='table' AND name IN ('users', 'schema_migrations')`).Scan(&n)
    // n==1 → 只有 users 无 schema_migrations（存量库）
    // n==0 → 空库（新库，从 000001 开始）
    // n==2 → 已被 golang-migrate 管理（正常增量）
    return n == 1
}
```

- 存量库：`m.Force(1)` 将 `schema_migrations` 标记到 version 1（跳过 baseline），此后 `m.Up()` 只执行 `000002+` 的增量。
- 新库：`m.Up()` 从 `000001` 顺序执行。

**这不同于自愈式迁移**：它不做「探测列是否存在 → 补列」，只做「判断是不是存量库 → 设置一次基线」，是标准的一次性 bootstrap。

> 若未来要彻底删除 `users.org_id` / `role_id` 旧列（RBAC 收尾），该变更作为 `000002` 增量脚本，存量库与新库统一走 `000002`，无需任何 `IF EXISTS` 探测。

## 7. 移除清单（自愈逻辑）

实施时删除以下函数及其调用（全部被版本化脚本取代）：

| 函数 | 原作用 |
|:---|:---|
| `migrate()` 旧实现 | 探测 + 修复编排 |
| `migrateLegacySchema` | 检测旧 TEXT 主键 |
| `migrateFromTextPK` | 旧表重建 + 数据映射 |
| `migrateUsersColumns` | 补 `version_json`/`role_id`/`is_platform_admin` 列 |
| `migrateUsersGlobalName` | 补 `users.name` 唯一索引 |
| `migrateUserOrgsFeatures` | RBAC 数据回填 |
| `migrateTasksColumns` | 补 tasks 列 |
| `migrateSharesColumns` | 补 shares 列 |
| `migrateVisitLogsColumns` | 补 visit_logs 列 |
| `newSchema` 常量 | 迁移到 `000001_baseline.up.sql` |

`tableHasColumn` 辅助函数也一并删除（不再有探测逻辑）。

## 8. 迁移文件规范

1. **命名**：`{6位序号}_{描述}.up.sql` / `.down.sql`（golang-migrate 约定）。
2. **事务**：`database/sqlite` driver **自动把每个迁移包在隐式事务**里，迁移文件**禁止写 `BEGIN` / `COMMIT`**。
3. **外键**：`PRAGMA foreign_keys` 由项目 DB 连接控制（现有 `Open()` 已开启）；SQLite 下 `ALTER TABLE` 不能改外键，涉及外键变更需「建新表 → 迁数据 → 删旧表 → 重命名」四步。
4. **幂等**：仅 `000001_baseline` 可用 `IF NOT EXISTS`；后续增量必须精确，重复执行由 `schema_migrations` 版本表保证不会发生。
5. **down 脚本**：每个 up 配一个 down，保持可回滚。

## 9. 构建与部署影响

- 迁移文件通过 `//go:embed` 内嵌，**无新增外部文件**、无 CLI 依赖。
- 引入 `golang-migrate/migrate/v4` + `database/sqlite` + `source/iofs` 三个依赖（`go.mod` 增量）。
- `CGO_ENABLED=0` 保持不变（`database/sqlite` 是纯 Go）。
- 启动路径：`Open()` → `migrate()`（新版 runner），替换现有 `d.migrate()` 调用点。

## 10. 回滚策略

- 生产环境**不自动 down**，仅在需要时通过 CLI 或显式代码调用 `m.Steps(-1)` / `m.Down()`。
- 每个版本的 down 脚本保证「回滚到上一版本的 schema 状态」。

## 11. 分步实施计划

| 步骤 | 内容 | 产出 |
|:---|:---|:---|
| 1 | 从现有 `newSchema` + `seedRoles` 生成 `000001_baseline.up.sql` / `.down.sql` | 迁移文件 |
| 2 | 引入 golang-migrate 依赖，实现 `migrate()` runner + `isLegacyDB` 基线检测 | `migrate.go` |
| 3 | `Open()` 改调新 runner | `db.go` |
| 4 | 删除全部自愈迁移函数 + `newSchema` 常量 + `tableHasColumn` | 清理 |
| 5 | 补测试：新库从 0 迁移 / 存量库 force 基线 / 增量执行 | `migrate_*_test.go` |
| 6 | `go build` / `go vet` / `go test` 全绿 | 验证 |

## 12. 风险与注意事项

1. **存量库 force 前必须确认 schema 完整**——`isLegacyDB` 判定「users 存在」即视为存量库，若某存量库 users 存在但缺其他表（历史半成品），force 到 1 会跳过 baseline 导致缺表。缓解：判定条件可升级为「校验全部核心表存在」（`IN ('orgs','users','tasks',...)` 计数），而非仅 users。
2. **`schema_migrations.dirty` 标志**——若某次迁移中途失败，dirty=1 会阻塞后续迁移，需手动处理（golang-migrate 提供 `m.Force` 恢复）。
3. **外键 per-connection**——`modernc.org/sqlite` 的 `foreign_keys` 是 per-connection 状态，golang-migrate 若从连接池取新连接，需确认 `foreign_keys=ON` 在迁移连接上生效（`sqlite.Config` 可设连接初始化）。
4. **事务语义变化**——现有自愈逻辑里 `migrateFromTextPK` 依赖「关闭外键 + 重建表」，改为版本化后，这类复杂重建需设计成单个迁移脚本内的原子序列（或拆成多个版本）。

## 13. 待确认

- [ ] baseline 策略：单 `000001_baseline`（推荐）还是拆成多个历史版本？
- [ ] 存量库判定：仅查 `users`，还是校验全部核心表？
- [ ] 是否在本轮一并做 `000002_drop_legacy_columns`（删除 `users.org_id`/`role_id` 旧列）？
