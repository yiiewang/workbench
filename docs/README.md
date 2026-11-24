# Workbench 文档中心

> 文档版本：docs v1.0 ｜ 更新日期：2026-11-24 ｜ 状态：现行
> 本文件统一组织 `docs/` 下全部文档，定义版本说明、更新记录与维护规范。

## 1. 文档定位与导航

| 文档 | 读者 | 内容 | 何时读 |
|---|---|---|---|
| [`functional-guide.md`](functional-guide.md) | 使用者、部署运维 | 功能地图、使用场景、操作流程、输入输出、参数、限制条件、FAQ | 使用功能、部署配置、排查现象 |
| [`design.md`](design.md) | 开发者、维护者 | 架构、模块划分、关键流程、数据模型、接口设计、依赖关系、扩展性与设计取舍 | 改代码、加接口、做方案评审 |
| [`README.md`](README.md)（本文件） | 全体 + 文档维护者 | 目录结构、版本说明、更新记录、维护规范、已知偏差清单 | 找文档、更新文档、发布前核对 |
| 根 [`README.md`](../README.md) | 使用者 | 快速上手、配置速查、部署与发布流程 | 首次构建与发版 |
| 根 [`CHANGELOG.md`](../CHANGELOG.md) | 全体 | 所有显著变更（Keep a Changelog） | 追溯功能/缺陷的引入版本 |
| [`rbac-design.md`](rbac-design.md) | — | **归档**：2026-08-21 RBAC 方案设计稿 | 仅作历史背景，**不代表现行实现** |
| [`migration-strategy.md`](migration-strategy.md) | — | **归档**：2026-08-21 版本化迁移方案设计稿 | 同上 |
| [`email-template-preview.html`](email-template-preview.html) | 开发者 | 邮件 HTML 模板的设计稿/预览产物 | 调整邮件模板时对照 |

**事实优先级**：代码 → `functional-guide.md` / `design.md` → 本文件的偏差清单 → 根 `README.md` → 归档设计稿。出现冲突时以代码为准，并把偏差登记到 §6。

## 2. 目录结构

```
workbench/
├── README.md                         # 快速上手、配置速查、部署与发布流程
├── CHANGELOG.md                      # 变更记录（Keep a Changelog）
└── docs/
    ├── README.md                     # 本文件：文档中心 / 版本 / 维护规范 / 偏差清单
    ├── functional-guide.md           # 功能说明文档（现行）
    ├── design.md                     # 软件设计文档（现行）
    ├── rbac-design.md                # [归档] RBAC 方案设计稿
    ├── migration-strategy.md         # [归档] 数据库迁移方案设计稿
    └── email-template-preview.html   # 邮件模板预览（开发辅助产物）
```

| 状态 | 含义 | 处理方式 |
|---|---|---|
| 现行 | 与当前代码一致，随功能变更同步更新 | 按 §5 触发规则维护 |
| 归档 | 历史设计稿，方案已被实施或被取代 | 不删除、不静默改写；文件头加归档提示（见 §5.6） |
| 开发辅助 | 供本地预览的设计产物，非规范文档 | 随对应功能一起更新 |

## 3. 版本说明

### 3.1 版本规则

- 文档版本独立编号 `docs vMAJOR.MINOR`，与代码 SemVer 解耦，但**必须记录对应的代码基线**。
- `MAJOR`：文档结构重组（拆分/合并文件、重编章节）；`MINOR`：内容增补、事实修正、偏差清理。
- 根 `CHANGELOG.md` 的 `[Unreleased]` 是每个代码变更的唯一记录入口；本文件 §4 只记录**文档自身的变更**。

### 3.2 当前基线

| 项 | 值 |
|---|---|
| 文档版本 | docs v1.0（2026-11-24） |
| **文档内容基线** | `v1.3.0-23-g8f70235`（HEAD `8f70235`，2026-11-24）；三份文档正文描述截至该提交 |
| 撰写结束时 HEAD | `v1.3.0-24-g5d28238`（2026-11-24，「对话后台运行」）；**尚未纳入正文**，见 §3.3 |
| 最近已发布版本 | `v1.3.0`（2026-11-10） |
| 数据库迁移基线（正文） | `000015_hybrid_fts`（15 个版本 / 30 个脚本）；HEAD 另含 `000016_ai_runs` |
| 内置 AI 工具 | 15 个（`internal/server/aitools.go` 注册表） |
| 运行时 | Go 1.25.0、`CGO_ENABLED=1`、build tag `sqlite_fts5`；Vue 3 + Vite 5；SQLite WAL |
| 变更记录 | 根 `CHANGELOG.md` 的 `[Unreleased]` |

### 3.3 基线差距与待同步内容

正式文档只描述**已提交且稳定的实现**。撰写本套文档期间，HEAD 由 `8f70235` 前进到 `5d28238`（`feat(ai): 对话后台运行——断连后 Agent 循环继续跑完`），该提交的内容**未写入正文**，在此登记为待同步项：

| 待同步内容 | 影响章节 |
|---|---|
| 迁移 `000016_ai_runs`（`ai_runs` 表：runId、org/user、status、events、result、err_msg） | `design.md` §6.1/§6.2、`functional-guide.md` §9.5 |
| `GET /api/ai/runs`、`GET /api/ai/runs/{id}` | `design.md` §7.2、`functional-guide.md` §9 |
| SSE 新增 `run_id` 事件 | `design.md` §7.3、`functional-guide.md` §9.2 |
| AI 对话与请求连接解耦（后台 run、15 分钟总超时、`aiRunWriter`、断连后跳过写类工具审批） | `design.md` §5.3/§4.3/§10、`functional-guide.md` §9.3/§14 |

该提交已落地，按 §5.2 触发规则一次性更新上述章节后清空本表并把 `docs` 版本号 +1。根 `CHANGELOG.md` 已记录「AI 对话与请求连接解耦」；同批的「AI 可触发索引重建并查询进度」（`reindex_files` / `reindex_status`）在 `8f70235` 中，本套文档已按 15 个工具纳入。撰写结束时 `frontend/src/api/ai.ts` 的前端恢复 UI 仍未提交，同样不写入正文。**正文锁定在 `8f70235` 是有意的基线选择**，避免文档与进行中的特性互相追赶。

## 4. 文档更新记录

| 版本 | 日期 | 变更 | 对应代码基线 |
|---|---|---|---|
| docs v1.0 | 2026-11-24 | 新增三套文档：`functional-guide.md`（16 章，使用者视角）、`design.md`（12 章，开发者视角）、本文件（文档中心）；为两份 2026-08-21 设计稿加归档状态提示；建立 §5 维护规范与 §6 已知文档偏差清单 | `v1.3.0-23-g8f70235` / 迁移 000015 |
| （历史）v1 | 2026-08-21 | `rbac-design.md`（RBAC 方案）、`migration-strategy.md`（golang-migrate 方案） | 当时的存量库与自愈式迁移 |
| （历史）— | 未记录 | `email-template-preview.html` 邮件模板预览 | 随邮件模板功能引入 |

> 本表只记文档变更；功能与缺陷变更见根 `CHANGELOG.md`。

## 5. 文档维护规范

### 5.1 单一事实源

| 信息类别 | 唯一权威文档 | 代码事实来源 |
|---|---|---|
| 功能、操作流程、参数、限制、FAQ | `functional-guide.md` | handler / 前端组件 |
| 架构、流程时序、表结构、接口、取舍 | `design.md` | `internal/*`、`frontend/src/*` |
| 文档导航、版本、更新记录、规范、偏差 | `docs/README.md` | `git describe` / `CHANGELOG.md` |
| 每次代码变更的记录 | 根 `CHANGELOG.md` `[Unreleased]` | 提交内容 |
| 快速上手与发版流程 | 根 `README.md` | `Makefile` / `scripts/package.sh` |

同一事实只在一处展开，其他文档用相对链接引用，避免多处副本各自漂移。

### 5.2 变更触发规则

| 代码变更 | 必须同步 |
|---|---|
| 新增/修改 REST 接口或请求响应字段 | `design.md` §7.2 → `functional-guide.md` 对应模块 → 根 `CHANGELOG.md` |
| 新增配置项 / 环境变量 | `config.yaml` → `functional-guide.md` §13 → `design.md` §3/§9 → 根 `CHANGELOG.md` |
| 新增数据库迁移 | `design.md` §6.1/§6.2 → §3.2 迁移基线 → 根 `CHANGELOG.md` |
| 新增/调整内置 AI 工具或审批策略 | `functional-guide.md` §9.3 → `design.md` §5.3/§9/§10 → 根 `CHANGELOG.md` |
| 新增 SSE 事件 | `design.md` §7.3 → `functional-guide.md` §9.2 → 根 `CHANGELOG.md` |
| 新增事件总线 topic / 通知渠道 / 定时 job | `design.md` §5.7 → `functional-guide.md` §10 → 根 `CHANGELOG.md` |
| 新增前端页面或路由 | `router/index.ts` + **`config.yaml` 的 `routes`** → `functional-guide.md` §12 → `design.md` §9 → 根 `CHANGELOG.md` |
| 限制条件、默认值、行为边界变化 | `functional-guide.md` §13/§14/§15 → `design.md` §10 → 根 `CHANGELOG.md` |
| 架构决策变化 | `design.md` §2/§10；必要时 `docs` 版本 MINOR+1 |
| 文档自身结构或基线变化 | 本文件 §2/§3/§4，并在根 `CHANGELOG.md` 记一条 |
| 发现既有文档与代码不符 | 本文件 §6 登记；修正后更新该行状态 |

**每次功能变更都要更新根 `CHANGELOG.md` 的 `[Unreleased]` 段**（Keep a Changelog、中文、条目带技术细节），文档同步与代码变更放在同一次交付中。

### 5.3 写作约定

- 中文撰写；表格优先，避免长段叙述；章节编号稳定，跨文档引用用章节号 + 相对链接。
- 引用代码使用「文件路径 + 函数/类型/常量名」，**不写行号**（行号随提交漂移）。
- 配置说明必须区分**代码默认值**、**`config.yaml` 示例值**与**运行回退值**。
- 不把未证实的意图写成事实；无法确认的行为标注「不确定」或列入 §6。
- 描述实现现状时保持中性（记录事实，不在文档中作缺陷判断）。
- 新增文件用英文短横线命名（`xxx-yyy.md`），与现有风格一致。

### 5.4 提交前检查清单

- [ ] 代码变更已在根 `CHANGELOG.md` 的 `[Unreleased]` 留条目
- [ ] §5.2 触发规则中命中的文档章节均已更新
- [ ] `design.md` §7.2 路由清单与 `Server.App()` 一致
- [ ] `functional-guide.md` §13 配置/环境变量与 `DefaultConfig`、`ApplyEnv`、`config.yaml` 一致
- [ ] §3.2 的迁移基线、内置工具数量与代码一致
- [ ] 新增前端路由已在 `config.yaml` 的 `routes` 注册 `__spa__`
- [ ] §4 更新记录、§3.2 代码基线已刷新（如涉及）
- [ ] §6 偏差清单已复核，新增偏差已登记或已消除
- [ ] 文档间相对链接可跳转，无悬空引用

### 5.5 一致性核对方法

| 核对项 | 代码位置 | 文档位置 |
|---|---|---|
| 路由与中间件 | `internal/server/server.go` 的 `App()`、`compressionMiddleware` / `loggingMiddleware` / `securityHeadersMiddleware` | `design.md` §2.2 / §7.2 |
| 配置与环境变量 | `internal/config/config.go` 的 `DefaultConfig` / `mergeConfig` / `ApplyEnv`、`config.yaml` | `functional-guide.md` §13 |
| 表与迁移 | `internal/db/migrations/*.sql`、`db.EnsureVecIndex` / `EnsureFTS` | `design.md` §6 |
| 内置工具 | `internal/server/aitools.go` 的 `aiTools` 表（`Name` / `NeedApproval`） | `functional-guide.md` §9.3、`design.md` §9 |
| SSE 事件 | `internal/server/ai.go` 的 `sseWriteEvent`、`frontend/src/api/ai.ts` 解析分支 | `design.md` §7.3 |
| 事件 topic | `internal/schedule/jobs.go` 的 `KnownTopics`、`notify/email.go` 的订阅 | `design.md` §5.7 |
| 业务码 | `internal/server/codes.go` | `functional-guide.md` §4.2 |
| 前端路由与分流 | `frontend/src/router/index.ts`、`config.yaml` 的 `routes` | `functional-guide.md` §12、`design.md` §5.8 |
| 版本基线 | `git describe --tags --always --dirty`、迁移目录最大编号 | 本文件 §3.2 |

建议节奏：**每次功能变更随提交核对**；**发版前**把 §6 偏差清单全量复核一遍；**每季度**复核一次归档文档状态。

### 5.6 归档规则

设计稿一旦实施完成或被后续方案取代，**不删除、不静默改写为现行描述**，执行：

1. 文件头加归档提示（模板见下），保留原有日期与状态行；
2. 本文件 §2 目录结构标为「归档」、§4 更新记录保留历史行、§6 登记主要漂移点；
3. 现行事实统一由 `design.md` / `functional-guide.md` 承载。

```markdown
> ⚠️ **归档文档（历史设计稿）**：本文写于 <日期>，记录当时的方案设计与评审状态，**不代表当前实现**。
> 现行架构、数据模型与接口请以 [`design.md`](design.md) 与代码为准；偏差清单见 [`README.md`](README.md)。
```

## 6. 已知文档偏差清单

> 用户确认：本轮**仅登记、不修改**根 `README.md` 与代码注释。下表是待后续按最小 diff 清理的清单。

| # | 位置 | 过时/不准确描述 | 当前事实 | 状态 |
|---|---|---|---|---|
| D01 | 根 `README.md` API Endpoints | `/__map__`、`/__tree__`、`/tasks.json` GET / PUT | 实际为 `/api/map`、`/api/tree`、`/api/tasks*`；`PUT /api/tasks` 自 1.3.0 起废弃，改为带 `baseRevision` 的 `PATCH/POST/DELETE /api/tasks/{id}` | 待修正 |
| D02 | 根 `README.md` Database | 表清单含 `visit_logs`、`version_json`，密码写 SHA-256 | `visit_logs` 于 000012 删除、`version_json` 于 000007 删除；密码用 bcrypt（仅兼容旧 SHA-256 存量）；现行 19 项表见 `design.md` §6.2 | 待修正 |
| D03 | 根 `README.md` Directory Structure | `internal/` 只列 config/db/server；`frontend/index.html / todo.html # Vite 多入口`；`scripts/` 只列 package.sh | `internal/` 有 8 个包；Vite 当前为**单入口** `index.html`；`scripts/` 另有 `ragclean.go` | 待修正 |
| D04 | 根 `README.md` AI Agent Tools | 标题写「13 个」，表格只列 12 个；未区分审批 | HEAD 注册表为 **15 个**（缺 `search_files`、`reindex_files`、`reindex_status`）；7 个需审批，`create_task` 不需要 | 待修正 |
| D05 | 根 `README.md` Quick Start | "Then open http://localhost:80" | 代码默认端口 `8080`，仓库 `config.yaml` 示例为 `8001` | 待修正 |
| D06 | 根 `README.md` Quick Start | `make build` 注释称「只改 Go 代码直接 build（embed 已生成的 frontend/dist）」 | `Makefile` 中 `build: frontend`，**每次都会重建前端** | 待修正 |
| D07 | 根 `README.md` Configuration / Environment Variables | `hidden` 默认值只列 `index.html` / `.*`；`ai` 段缺 `site_url` / `max_distance` / `judge_model` / `digest_model`；环境变量表缺 5 项 | 见 `functional-guide.md` §13 的完整默认值与环境变量清单 | 待修正 |
| D08 | 根 `README.md` Features 末尾 | `- **CORS Security**` 孤立 bullet 落在功能矩阵表格之后，排版错乱 | 应归入 Security 章节 | 待修正 |
| D09 | `frontend/src/api/ai.ts` 注释 | 审批「60s 未确认」 | 后端 `aiApprovalTimeout = 5 * time.Minute` | 待修正 |
| D10 | `internal/config/config.go`、`config.yaml` 注释 | 「模型可调 … 4 个内置工具」 | 15 个 | 待修正 |
| D11 | `embed.go` 注释、`server.isUIAsset` 白名单、`config.yaml` 的 `/todo.html` 别名 | 存在 `todo.html` 多入口 | `frontend/dist` 无该文件，属历史遗留 | 待修正 |
| D12 | `internal/notify/email.go` 头注释 | 提到 `EMAIL_USER` 环境变量 | 实际只有 `EMAIL_PASS`；`user` 来自 `config.yaml` | 待修正 |
| D13 | `internal/server/upload.go` 注释 | 「编辑器保存也走本接口」 | 前端仅侧栏上传、文件选择器上传、AI「回复保存为文件」调用 `uploadFiles` | 待修正 |
| D14 | `docs/rbac-design.md`、`docs/migration-strategy.md` | 状态为「设计中 / 方案评审中（未实施）」，含 `CGO_ENABLED=0` + modernc、`org_features`、自愈式迁移等结论 | 已实施/被取代：mattn + cgo + sqlite-vec、`user_org_feature`、golang-migrate 版本化迁移（基线 000015） | **已处理**：文件头加归档提示，本表登记 |
| D15 | 提交 `5d28238`（000016 `ai_runs` 等） | 本套文档正文未包含（内容基线锁定 `8f70235`） | 见 §3.3「基线差距与待同步内容」，按 §5.2 同步 | 待同步 |

新增偏差时追加一行并写清「位置 / 描述 / 当前事实 / 状态」；修正后把状态改为「已修正（日期）」，不删除历史行，便于追溯。
