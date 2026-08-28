# 用户-组织-功能权限方案（RBAC）

> 版本：v1 ｜ 日期：2026-08-21 ｜ 状态：设计中，阶段 A（数据层）已进入实施

## 0. 现状与目标差异

| 维度 | 现状 | 目标 |
|:---|:---|:---|
| 用户↔组织 | `users.org_id` 单值外键（1 对 1） | `user_orgs` 中间表（多对多） |
| 角色 | `roles` 表 + `users.role_id`（admin/user/org_admin） | 组织内角色 `owner/admin/member` 挂到 `user_orgs`，平台级 `admin` 用 `users.is_platform_admin` 标志 |
| 组织上下文 | token payload 固化 `orgID:userID:expiry` | token 只存 `userID`，组织上下文走 `X-Org-Id` 请求头，可切换 |
| 功能开关 | 无（硬编码菜单） | `org_features` 表 + 动态下发 |
| 初始化接口 | `/api/me`（单 org、单 role） | `/api/userinfo` 聚合（一次拉全） |

**命名约定**：功能挂在**组织**上（非 user_org 关系上），统一命名 `org_features`。`org` 沿用现有 `orgs` 表，`user_org` 沿用复数风格 `user_orgs`。

## 1. 数据模型

### 1.1 关系图

```
users ──┬──< user_orgs >──┬── orgs
        │   (role,status) │
        └──  is_platform_admin (平台级超管标志)
                                    │
                              orgs ──< org_features (feature_code, enabled)
```

- **平台级**：`users.is_platform_admin` 决定是否跨组织超管（对应旧 `admin` 角色）。
- **组织级**：`user_orgs.role` 决定用户在某个组织内的角色（`owner`/`admin`/`member`）。
- **功能级**：`org_features` 决定某个组织整体启用了哪些功能模块，与用户角色叠加后决定最终可见菜单与可操作范围。

### 1.2 角色与功能枚举

```go
// 组织内角色
const (
    RoleOwner  = "owner"   // 组织所有者：可管理成员、改角色、配功能
    RoleAdmin  = "admin"   // 组织管理员：可管理成员（不能降级 owner）
    RoleMember = "member"  // 普通成员
)

// 功能标识
const (
    FeatureFile  = "file"   // 文件浏览
    FeatureShare = "share"  // 分享
    FeatureTodo  = "todo"   // 待办看板
    FeatureAdmin = "admin"  // 用户管理（组织内成员管理）
)
```

## 2. 数据库表结构

> 遵循现有约定：自增主键 `id`、SQLite 语法、`PRAGMA foreign_keys=ON`。

### 2.1 `users` — 用户表

```sql
CREATE TABLE users (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT NOT NULL UNIQUE,
    mobile             TEXT UNIQUE,
    password_hash      TEXT NOT NULL DEFAULT '',
    version_json       TEXT DEFAULT '',
    is_platform_admin  INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT DEFAULT (datetime('now')),
    updated_at         TEXT DEFAULT (datetime('now'))
);
```

变化：删除 `org_id`（移到 `user_orgs`）、删除 `role_id`（组织角色移到 `user_orgs`，平台角色用 `is_platform_admin`）。

索引：`UNIQUE(name)`（登录主索引）、`UNIQUE(mobile)`。

### 2.2 `orgs` — 组织表（沿用现有）

```sql
CREATE TABLE orgs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    created_at  TEXT DEFAULT (datetime('now'))
);
```

### 2.3 `user_orgs` — 用户-组织关系表（多对多）

```sql
CREATE TABLE user_orgs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    org_id      INTEGER NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member',
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TEXT DEFAULT (datetime('now')),
    updated_at  TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (org_id)  REFERENCES orgs(id)  ON DELETE CASCADE,
    UNIQUE (user_id, org_id)
);
```

索引：
- `UNIQUE(user_id, org_id)` — 核心唯一约束 + 查某用户的所有组织
- `INDEX idx_user_orgs_org (org_id)` — 查某组织的所有成员
- `INDEX idx_user_orgs_role (org_id, role)` — 查某组织的 owner/admin（权限判断）

### 2.4 `org_features` — 组织-功能映射表

```sql
CREATE TABLE org_features (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id        INTEGER NOT NULL,
    feature_code  TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT DEFAULT (datetime('now')),
    updated_at    TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE,
    UNIQUE (org_id, feature_code)
);
```

索引：
- `UNIQUE(org_id, feature_code)` — 核心唯一约束 + 查某组织启用的功能列表
- `INDEX idx_org_features_enabled (org_id, enabled)` — 只查启用项

## 3. 组织切换

### 3.1 会话与上下文

| 项 | 设计 |
|:---|:---|
| Token | payload 改为 `userID:expiry`（去掉 orgID） |
| 当前组织上下文 | 请求头 `X-Org-Id` 传递（无状态） |
| 鉴权中间件 | `AuthMiddleware` 校验 token 得 userID；再读 `X-Org-Id`，校验归属（查 `user_orgs` 且 `status='active'`），写 ctx 的 `orgID/orgName/role` |

组织切换本质是前端本地状态切换（`currentOrgId`），`/api/userinfo` 已一次性返回所有组织及角色/功能；后端每次请求仍校验 `X-Org-Id` 归属防越权。

### 3.2 接口

```
POST /api/org/switch
Body:   { "orgId": 2 }
```

后端校验 `user_orgs` 存在 `(user_id, org_id=2, status=active)`，返回该组织上下文。

### 3.3 前端

- `auth.ts` 新增 `currentOrgId` ref（持久化 localStorage）
- `apiCall` 统一注入 `X-Org-Id`
- `DefaultLayout` 活动栏顶部「组织切换器」下拉

## 4. 动态功能加载

### 4.1 接口

```
GET /api/org/features
Header: X-Org-Id: 2
```

### 4.2 前端

- `auth.ts` 新增 `features` ref
- 菜单/侧栏用 `v-if="hasFeature('share')"` 动态渲染
- 组织切换后 `loadFeatures()` 刷新

## 5. userinfo 聚合接口

```
GET /api/userinfo
Header: Authorization: Bearer <token>
（可选）Header: X-Org-Id: 2
```

```json
{
  "code": 0,
  "data": {
    "user": { "userId": 1, "userName": "yiiewang", "mobile": "", "isPlatformAdmin": false },
    "currentOrgId": 2,
    "orgs": [
      { "orgId": 1, "orgName": "org-a", "role": "member", "status": "active",
        "features": ["file", "share", "todo"] },
      { "orgId": 2, "orgName": "cm", "role": "owner", "status": "active",
        "features": ["file", "share", "todo", "admin"] }
    ],
    "features": ["file", "share", "todo", "admin"]
  }
}
```

后端实现：`GetUserOrgs(userID)` 一次 JOIN `user_orgs` + `orgs` + `org_features` 查出全部，按 org 分组聚合 features。

## 6. 与现有代码的迁移映射

| 旧 | 新 | 说明 |
|:---|:---|:---|
| `users.org_id` | `user_orgs(org_id)` | 存量每个 user 生成一条 `user_orgs` 记录 |
| `users.role_id` / `roles` 表 | `user_orgs.role` + `users.is_platform_admin` | `admin`→`is_platform_admin=1`；`org_admin`→`user_orgs.role='admin'`；`user`→`member` |
| `roles` 表 | 代码常量枚举 | 角色固定三种，用常量即可 |
| token `orgID:userID:expiry` | `userID:expiry` | `ValidateToken` 返回 `(valid, userID)` |
| `AuthMiddleware` 写 `orgID/role` | 从 `X-Org-Id` + `user_orgs` 反查 | `currentOrgID(ctx)` 改读 `X-Org-Id` 校验后的值 |
| `/api/me` | `/api/userinfo` | 前端 `checkAuth` 改调聚合接口 |
| `handleLogin` 填 `orgId` | 登录不填 org，从 `userinfo.orgs` 选默认 | `LoginView` 移除 Org ID 输入框 |
| `RequireUserManager` / `isSuperAdmin` | `isPlatformAdmin` + `user_orgs.role` | 平台超管跨 org；组织 owner/admin 仅本 org |

## 7. 落地分阶段

1. **阶段 A（数据层）**：新增 `user_orgs`、`org_features` 表 + 迁移存量数据
2. **阶段 B（鉴权）**：token 去 orgID、`AuthMiddleware` 支持 `X-Org-Id`、`RequireUserManager` 改造
3. **阶段 C（接口）**：`/api/userinfo`、`/api/org/switch`、`/api/org/features`
4. **阶段 D（前端）**：`auth.ts` 组织上下文/功能集合、组织切换器、动态菜单
5. **阶段 E（功能配置 UI）**：组织 owner/admin 配置 `org_features` 开关

> 阶段 A 采用「兼容共存」策略：保留 `users.org_id` / `users.role_id` 旧列（现有 server 层继续使用），新增 `is_platform_admin` 列与两张新表，并通过迁移函数同步数据，避免一次性破坏现有功能。后续阶段 B/C 逐步切换后，再移除旧列。

## 8. 存量数据库迁移方案

存量库（`data/workbench.db`）在阶段 A 前只有 `users.org_id` + `users.role_id` 单组织关系，缺少 `user_orgs` / `org_features` 表与 `is_platform_admin` 列。迁移在 **`Open()` 启动时自动执行**（`internal/db/migrate.go` 的 `migrate()`），无需手动干预，幂等可重复运行。

### 8.1 迁移步骤

| 步骤 | 函数 | 行为 |
|:---|:---|:---|
| 1 | `newSchema` | `CREATE TABLE IF NOT EXISTS user_orgs` / `org_features`（存量库缺失则创建；users 表已存在不重建） |
| 2 | `migrateUsersColumns` | 检测 `users` 缺 `is_platform_admin` 列 → `ALTER TABLE users ADD COLUMN is_platform_admin INTEGER NOT NULL DEFAULT 0` |
| 3 | `migrateUserOrgsFeatures` | 三步同步（见下） |

### 8.2 migrateUserOrgsFeatures 数据同步

```sql
-- ① 平台超管标志：旧 admin(role_id=1) → is_platform_admin=1
UPDATE users SET is_platform_admin = 1 WHERE role_id = 1 AND is_platform_admin = 0;

-- ② 多对多关系：users.org_id → user_orgs，组织角色按旧 role_id 映射
INSERT INTO user_orgs (user_id, org_id, role, status)
SELECT u.id, u.org_id,
  CASE u.role_id WHEN 1 THEN 'owner' WHEN 3 THEN 'admin' ELSE 'member' END,
  'active'
FROM users u
WHERE u.org_id > 0 AND NOT EXISTS (SELECT 1 FROM user_orgs uo WHERE uo.user_id = u.id AND uo.org_id = u.org_id);

-- ③ 组织功能预置：每个 org 启用全部 4 个功能
INSERT OR IGNORE INTO org_features (org_id, feature_code, enabled)
SELECT o.id, f.code, 1 FROM orgs o
CROSS JOIN (SELECT 'file' AS code UNION ALL SELECT 'share' UNION ALL SELECT 'todo' UNION ALL SELECT 'admin') f;
```

### 8.3 角色映射

| 旧 `role_id` | 旧语义 | 新 `is_platform_admin` | 新 `user_orgs.role` |
|:---|:---|:---|:---|
| 1 | admin | `1` | `owner` |
| 3 | org_admin | `0` | `admin` |
| 2 | user | `0` | `member` |

### 8.4 幂等性

- `user_orgs` 用 `NOT EXISTS` 防重复插入
- `org_features` 用 `INSERT OR IGNORE` 防重复
- `is_platform_admin` 补列用 `ALTER TABLE ... DEFAULT 0`，`UPDATE ... AND is_platform_admin = 0` 防重复置位

### 8.5 验证

端到端迁移测试 `TestMigrate_LegacyUserOrgsFeatures`（`internal/db/migrate_legacy_test.go`）：手工预建存量库（`orgs`/`roles`/`users` 旧 schema + 三种 role_id 数据）→ `Open()` 触发迁移 → 断言 `is_platform_admin` / `user_orgs.role` / `org_features` 全部正确生成。

> 注意：存量库的 `users` 表需含 `UNIQUE(org_id, id)` 约束，以满足 `tasks` 表 `FOREIGN KEY (org_id, user_id) REFERENCES users(org_id, id)` 的复合外键要求（`newSchema` 的 users 定义一致）。

## 9. 前端功能入口门控约定

- 业务功能（`file` / `share` / `todo`）：活动栏图标用 `v-if="hasFeature(code)"` 动态渲染，受组织功能开关控制。
- 用户管理（`admin`）：活动栏图标用 `v-if="canManageUsers"`（角色权限）门控，**不叠加 `hasFeature('admin')`**。原因有二：① 避免「关闭 admin 功能 → 入口消失 → 无法重新开启」的死锁；② 避免 `features` 未加载（刷新首帧）时误隐藏入口。
- `features` 状态持久化到 `localStorage`（`workbench-current-features`），刷新不丢，避免业务功能入口闪烁。
