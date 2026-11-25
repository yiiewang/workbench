# Workbench 文档中心

> 文档版本：`docs v2.1`｜ 更新日期：2026-11-24 ｜ 状态：现行
> 本文件是 `docs/` 的 **00 号入口**：统一组织全部文档，定义编号规则、目录结构、版本说明、更新记录与维护规范。

## 1. 文档编号规则

| 规则 | 说明 |
|---|---|
| 主文档 | 文件名 `NN-name.md`，`NN` 为两位序号，按**阅读顺序**排列；序号即文档清单中的编号 |
| 入口 | `README.md` 固定为 **00**（GitHub 自动展示入口，故不加数字前缀） |
| 归档 | `archive/` 下的历史文档编号 `A1`–`A3`，文件名保持不变，不参与主序列 |
| 章节 | 文内用 `## N.` / `### N.M` 编号；跨文档引用写作「`NN-name.md` §N.M」 |
| 新增文档 | 优先**追加末尾序号**（插入中间会导致全量链接改写）；新增后同步本文件 §2/§3/§4 与根 `CHANGELOG.md` |

## 2. 目录结构与导航

```
workbench/
├── README.md                       # 快速上手、配置速查、功能概览、部署安全
├── CHANGELOG.md                    # 变更记录（Keep a Changelog）
└── docs/
    ├── README.md                   # 00 文档中心（本文件）
    ├── 01-getting-started.md       # 01 快速开始
    ├── 02-functional-guide.md      # 02 功能说明
    ├── 03-design.md                # 03 软件设计
    ├── 04-build-and-release.md     # 04 构建与发布
    ├── 05-operations.md            # 05 配置与运维
    └── archive/                    # 归档
        ├── rbac-design.md           # A1 [归档] RBAC 方案设计稿
        ├── migration-strategy.md    # A2 [归档] 数据库迁移方案设计稿
        └── email-template-preview.html  # A3 邮件模板预览（开发辅助产物）
```

| 序号 | 文档 | 读者 | 内容 | 何时读 |
|:---:|---|---|---|---|
| 00 | [`README.md`](README.md) | 全体 + 维护者 | 导航、编号规则、版本、更新记录、维护规范、偏差清单 | 找文档、更新文档、发布前核对 |
| 01 | [`01-getting-started.md`](01-getting-started.md) | 使用者、部署者 | 环境、构建启动、首次超管初始化、登录、SPA 路由注册、公网部署 | 第一次部署 |
| 02 | [`02-functional-guide.md`](02-functional-guide.md) | 使用者 | 功能地图、场景/流程/输入输出/参数/限制、能力矩阵、FAQ、术语表 | 用功能、查参数、排查现象 |
| 03 | [`03-design.md`](03-design.md) | 开发者 | 架构、模块划分、关键流程、数据模型、接口/SSE、安全模型、扩展点、设计取舍 | 改代码、做方案评审 |
| 04 | [`04-build-and-release.md`](04-build-and-release.md) | 开发者、发布者 | 工具链、Makefile target、构建约束、质量门禁、xgo 打包、发布流程、构建故障 | 构建、发版、排查编译问题 |
| 05 | [`05-operations.md`](05-operations.md) | 运维、开发者 | 配置与合并语义、启用矩阵、**RAG 索引运维**、备份、迁移、日志、故障排查 | 配置、升级、索引清理、线上排查 |
| A1 | [`archive/rbac-design.md`](archive/rbac-design.md) | — | 2026-08-21 RBAC 设计稿 | 仅历史背景，**不代表现行实现** |
| A2 | [`archive/migration-strategy.md`](archive/migration-strategy.md) | — | 2026-08-21 迁移方案设计稿 | 同上 |
| A3 | [`archive/email-template-preview.html`](archive/email-template-preview.html) | 开发者 | 邮件模板预览 | 调整邮件模板时对照 |

| 状态 | 含义 | 处理 |
|---|---|---|
| 现行 | 与当前代码一致，随功能变更同步更新 | 按 §5 触发规则维护 |
| 归档 | 历史设计稿，方案已实施或被取代 | 不删除、不静默改写；文件头保留归档提示（§5.6） |
| 开发辅助 | 本地预览产物，非规范文档 | 随对应功能更新 |

**事实优先级**：代码 → `02` / `03` / `04` / `05` → 本文件偏差清单 → 根 `README.md` → `archive/`。冲突时以代码为准并登记到 §6。

## 3. 版本说明

### 3.1 版本规则

- 文档版本独立编号 `docs vMAJOR.MINOR`，与代码 SemVer 解耦，但**必须记录对应代码基线**。
- `MAJOR`：文档结构重组（拆分/合并/重命名/编号变更）；`MINOR`：内容增补、事实修正、偏差清理。
- 根 `CHANGELOG.md` 的 `[Unreleased]` 是每个**代码变更**的唯一记录入口；本文件 §4 只记录**文档自身的变更**。

### 3.2 当前基线

| 项 | 值 |
|---|---|
| 文档版本 | `docs v2.1`（2026-11-24；v2.0 重建 + 孤儿索引清理同步） |
| 代码基线 | `v1.3.0-25-g517f88c`（HEAD `517f88c`，含 `000016_ai_runs` 与 1b 前端恢复） |
| 工作树增量 | **未提交的 000017 编排引擎**（`internal/pipeline`、`pipeline_runs`、`run_pipeline`、`/api/pipeline/runs`）已纳入正文；提交后需在 §4 记一笔并复核基线 |
| 撰写结束后新增 | **未提交的 000018 编排模板**（`pipeline_templates`、HTTP 入口、cron 触发）在本批收尾时出现，**尚未纳入正文**，登记为 D18 |
| 最近已发布版本 | `v1.3.0`（2026-11-10） |
| 数据库迁移基线 | `000017_pipeline_runs`（17 个版本 / 34 个脚本） |
| 内置 AI 工具 | 16 个 |
| SSE 自定义事件 | 8 类（含 `run_id`） |
| 运行时 | Go 1.25.0、`CGO_ENABLED=1`、tag `sqlite_fts5`；Vue 3 + Vite 5；SQLite WAL |

### 3.3 基线策略

重建后采用**以眼前代码为准**：已提交的 HEAD 内容全部纳入；工作树中已调研确认、且具备迁移/测试/`CHANGELOG` 条目的未提交变更也纳入正文，并在 §3.2「工作树增量」显式标注，避免文档因排除 WIP 而反复滞后。**撰写结束后新出现的变更不追写正文**，一律登记为 §6 的待同步项，下一轮按 §5.2 统一纳入——代码并行开发期间，文档基线以批次为单位锁定，避免与进行中的特性互相追赶。若某变更被回滚，按 §5.2 同步回退并更新本表。

## 4. 文档更新记录

| 版本 | 日期 | 变更 | 对应代码基线 |
|---|---|---|---|
| `docs v2.1` | 2026-11-24 | 全量重建新增孤儿索引清理（严格「文件已不存在」判定、按 owner 隔离、`pruned` 计数）；同步 `05` §5.3–§5.5/§9、`02` §8.4/§14、`03` §5.5/§10 | 同一代码基线 + 本次未提交改动 |
| `docs v2.0` | 2026-11-24 | **重建**：主文档加两位序号并重命名；新增 `01` 快速开始、`04` 构建与发布、`05` 配置与运维；`02`/`03` 全量重写并追平后台 run / 1b 恢复 / 声明式编排；根 `README.md` 的 Quick Start 与 Release Process 迁入 `01`/`04` 并改为链接；历史设计稿与模板预览归拢 `archive/`；建立编号规则；偏差清单复核 | `v1.3.0-25-g517f88c` + 工作树 000017 |
| `docs v1.0` | 2026-11-24 | 初建三套文档（功能说明 / 软件设计 / 文档中心）+ 维护规范与偏差清单 | `v1.3.0-23-g8f70235` / 迁移 000015 |
| （历史）v1 | 2026-08-21 | `rbac-design.md`、`migration-strategy.md` 设计稿 | 当时的存量库与自愈式迁移 |
| （历史）— | 未记录 | 邮件模板预览 | 随邮件模板功能引入 |

> 本表只记文档变更；功能与缺陷变更见根 `CHANGELOG.md`。

## 5. 文档维护规范

### 5.1 单一事实源

| 信息类别 | 唯一权威文档 | 代码事实来源 |
|---|---|---|
| 环境、首次启动、初始化、部署 | `01-getting-started.md` | `Makefile` / `config.yaml` / `handleSetPassword` |
| 功能、操作流程、参数、限制、FAQ | `02-functional-guide.md` | handler / 前端组件 |
| 架构、流程时序、表结构、接口、取舍 | `03-design.md` | `internal/*`、`frontend/src/*` |
| 构建、质量门禁、打包、发布 | `04-build-and-release.md` | `Makefile` / `vite.config.ts` / `scripts/package.sh` |
| 运行期配置、索引运维、备份、故障排查 | `05-operations.md` | `config.go` / `rag` / `scripts/ragclean.go` |
| 文档导航、编号、版本、更新记录、规范、偏差 | `docs/README.md` | `git describe` / `CHANGELOG.md` |
| 每次代码变更的记录 | 根 `CHANGELOG.md` `[Unreleased]` | 提交内容 |

同一事实只在一处展开，其他文档用相对链接引用，避免副本各自漂移。

### 5.2 变更触发规则

| 代码变更 | 必须同步 |
|---|---|
| 新增/修改 REST 接口、SSE 事件或请求响应字段 | `03` §7 → `02` 对应模块 → 根 `CHANGELOG.md` |
| 新增配置项 / 环境变量 | `config.yaml` → `05` §2/§3 → `03` §3/§9 → 根 `CHANGELOG.md` |
| 新增数据库迁移 | `03` §6 → 本文件 §3.2 迁移基线 → 根 `CHANGELOG.md` |
| 新增/调整内置工具或审批策略 | `02` §8.3 → `03` §5.3/§9/§10 → 本文件 §3.2 工具数 → 根 `CHANGELOG.md` |
| 新增前端页面或路由 | `router/index.ts` + **`config.yaml` 的 `routes`** → `02` §12 → `03` §9 → 根 `CHANGELOG.md` |
| 新增事件 topic / 通知渠道 / 定时 job | `03` §5.8 → `02` §10 → 根 `CHANGELOG.md` |
| 新增编排能力类别 | `internal/pipeline` + `server/pipeline.go` → `02` §9 → `03` §5.9/§9 → 根 `CHANGELOG.md` |
| 构建链、Makefile target、打包或发布流程变化 | `04` → `01` §3 → 根 `CHANGELOG.md` |
| 限制条件、默认值、行为边界变化 | `02` §13/§14 → `05` → `03` §10 → 根 `CHANGELOG.md` |
| 架构决策变化 | `03` §2/§10；必要时 `docs` 版本 MINOR+1 |
| 文档编号/结构/基线变化 | 本文件 §1/§2/§3/§4，并在根 `CHANGELOG.md` 记一条 |
| 发现既有文档与代码不符 | 本文件 §6 登记；修正后更新该行状态 |

**每次功能变更都要更新根 `CHANGELOG.md` 的 `[Unreleased]` 段**（Keep a Changelog、中文、条目带技术细节），文档同步与代码变更放在同一次交付中。

### 5.3 写作约定

- 中文撰写；表格优先，避免长段叙述；章节编号稳定，跨文档引用用「文件名 + 章节号 + 相对链接」。
- 引用代码使用「文件路径 + 函数/类型/常量名」，**不写行号**（行号随提交漂移）。
- 配置说明必须区分**代码默认值**、**`config.yaml` 示例值**与**运行回退值**。
- 不把未证实的意图写成事实；无法确认的行为标注「不确定」或列入 §6。
- 描述实现现状保持中性（记录事实，不在文档中作缺陷判断）。
- 主文档用 `NN-name.md` 命名；新增时优先追加末尾序号，避免全量改链接。
- 相对链接必须在提交前验证可达（§5.4）。

### 5.4 提交前检查清单

- [ ] 代码变更已在根 `CHANGELOG.md` 的 `[Unreleased]` 留条目
- [ ] §5.2 触发规则中命中的文档章节均已更新
- [ ] `03` §7.2 路由清单与 `Server.App()` 一致
- [ ] `05` §2/§3 配置与环境变量和 `DefaultConfig`、`ApplyEnv`、`config.yaml` 一致
- [ ] §3.2 的迁移基线、内置工具数、SSE 事件数与代码一致
- [ ] 新增前端路由已在 `config.yaml` 的 `routes` 注册 `__spa__`
- [ ] §4 更新记录、§3.2 代码基线已刷新（如涉及）
- [ ] §6 偏差清单已复核，新增偏差已登记或已消除
- [ ] **全部文档相对链接可达**（重命名/移动文件后尤其要检查）
- [ ] 文档编号连续，本文件 §2 表格与实际文件名一致

### 5.5 一致性核对方法

| 核对项 | 代码位置 | 文档位置 |
|---|---|---|
| 路由与中间件 | `internal/server/server.go` 的 `App()` 及三个全局中间件 | `03` §2.2 / §7.2 |
| 配置与环境变量 | `internal/config/config.go` 的 `DefaultConfig` / `mergeConfig` / `ApplyEnv`、`config.yaml` | `05` §1–§4 |
| 表与迁移 | `internal/db/migrations/*.sql`、`EnsureVecIndex` / `EnsureFTS` | `03` §6、`05` §7 |
| 内置工具 | `internal/server/aitools.go` 的 `aiTools`（`Name`/`NeedApproval`）与 `pipelineTool()` | `02` §8.3、`03` §9 |
| SSE 事件 | `internal/server/ai.go` 的 `sseWriteEvent`、`frontend/src/api/ai.ts` | `03` §7.3、`02` §8.2 |
| 编排定义 | `internal/pipeline/pipeline.go` 的 `Definition`/`Runner` | `02` §9、`03` §5.9 |
| 事件 topic | `internal/schedule/jobs.go` 的 `KnownTopics`、`notify/email.go` | `03` §5.8 |
| 业务码 | `internal/server/codes.go` | `02` §3 |
| 前端路由与分流 | `frontend/src/router/index.ts`、`config.yaml` 的 `routes` | `02` §12、`03` §5.10 |
| 构建与发布 | `Makefile`、`frontend/package.json`、`vite.config.ts`、`scripts/package.sh` | `04` |
| 版本基线 | `git describe --tags --always --dirty`、迁移目录最大编号 | 本文件 §3.2 |

建议节奏：**每次功能变更随提交核对**；**发版前**全量复核 §6；**每季度**复核归档文档状态。

### 5.6 归档规则

设计稿一旦实施完成或被后续方案取代，**不删除、不静默改写为现行描述**：

1. 文件头加归档提示（模板见下），保留原有日期与状态行；
2. 移入 `archive/`，修正相对链接为 `../NN-name.md` / `../README.md`；
3. 本文件 §2 标为「归档」、§4 保留历史行、§6 登记主要漂移点；
4. 现行事实统一由 `02` / `03` / `04` / `05` 承载。

```markdown
> ⚠️ **归档文档（历史设计稿）**：本文写于 <日期>，记录当时的方案设计与评审状态，**不代表当前实现**。
> 现行架构、数据模型与接口请以 `../03-design.md` 与代码为准；偏差清单见 `../README.md`。
```

## 6. 已知文档偏差清单

> 约定：根 `README.md` 中与构建/发布相关的章节已迁入 `01`/`04`；其余过时描述**仅登记、暂不修改**。

| # | 位置 | 过时/不准确描述 | 当前事实 | 状态 |
|---|---|---|---|---|
| D01 | 根 `README.md` API Endpoints | `/__map__`、`/__tree__`、`/tasks.json` GET/PUT | 实际 `/api/map`、`/api/tree`、`/api/tasks*`；`PUT /api/tasks` 自 1.3.0 废弃，改为带 `baseRevision` 的 `PATCH/POST/DELETE /api/tasks/{id}` | 待修正 |
| D02 | 根 `README.md` Database | 表清单含 `visit_logs`、`version_json`，密码写 SHA-256 | 两表/列已删；密码 bcrypt（仅兼容旧 SHA-256 存量）；现行 **21 项表**见 `03-design.md` §6.2 | 待修正 |
| D03 | 根 `README.md` Directory Structure | `internal/` 只列 3 个包；Vite 多入口 `todo.html`；`scripts/` 只列 `package.sh` | 9 个包（含 `pipeline`/`rag`/`schedule`/`notify`/`mcpclient`/`sqlitevec`）；Vite 单入口；`scripts/` 另有 `ragclean.go` | 待修正 |
| D04 | 根 `README.md` AI Agent Tools | 标题写「13 个」，表格列 12 个 | 实际 **16 个**（缺 `search_files`、`reindex_files`、`reindex_status`、`run_pipeline`）；8 个需审批，`create_task` 不需要 | 待修正 |
| D05 | 根 `README.md` Quick Start | "open http://localhost:80" | 代码默认 `8080`，`config.yaml` 示例 `8001` | **已修正**（v2.0 迁入 `01`） |
| D06 | 根 `README.md` Quick Start / Build Targets | `make build` 称「只改 Go 代码直接 build」 | `build: frontend`，每次重建前端；已写入 `04-build-and-release.md` §3/§4 | **已修正**（v2.0） |
| D07 | 根 `README.md` Configuration / Environment Variables | `hidden` 默认值不全；`ai` 段缺 `site_url`/`max_distance`/`judge_model`/`digest_model`；环境变量表缺 7 项 | 见 `05-operations.md` §2/§3 | 待修正 |
| D08 | 根 `README.md` Features 末尾 | `- **CORS Security**` 孤立 bullet 落在功能矩阵表之后 | 应归入 Security 章节 | 待修正 |
| D09 | `frontend/src/api/ai.ts` 注释 | 审批「60s 未确认」 | 后端 `aiApprovalTimeout = 5 * time.Minute` | 待修正 |
| D10 | `internal/config/config.go`、`config.yaml` 注释 | 「4 个内置工具」 | 16 个 | 待修正 |
| D11 | `embed.go` 注释、`isUIAsset` 白名单、`config.yaml` 的 `/todo.html` 别名 | 存在 `todo.html` 多入口 | `frontend/dist` 无该文件，属历史遗留 | 待修正 |
| D12 | `internal/notify/email.go` 头注释 | 提到 `EMAIL_USER` 环境变量 | 实际只有 `EMAIL_PASS`；`user` 来自 `config.yaml` | 待修正 |
| D13 | `internal/server/upload.go` 注释 | 「编辑器保存也走本接口」 | 前端仅侧栏上传、文件选择器上传、AI「回复保存为文件」调用 `uploadFiles` | 待修正 |
| D14 | `archive/rbac-design.md`、`archive/migration-strategy.md` | 状态「设计中 / 未实施」，含 `CGO_ENABLED=0` + modernc、`org_features`、自愈式迁移 | 已实施/被取代：mattn + cgo + sqlite-vec、`user_org_feature`、golang-migrate 版本化迁移（基线 000017） | **已处理**：归档提示 + 移入 `archive/` |
| D15 | 提交 `5d28238`（000016 `ai_runs` 等） | 初版文档未包含 | v2.0 已纳入 `02` §8.5、`03` §5.3/§5.4/§6 | **已同步** |
| D16 | 工作树未提交的 000017 编排引擎 | 无历史文档 | v2.0 已纳入 `02` §9、`03` §3/§5.9/§6/§10，并在 §3.2 标注为工作树增量 | **已纳入，待提交后复核** |
| D17 | 根 `README.md` Features / API Endpoints | 未覆盖后台运行、编排、断点恢复等新能力 | 见 `02-functional-guide.md` §8.5/§9 与 `03-design.md` §7.2 | 待补充 |
| D18 | 工作树未提交的 000018 编排模板 | v2.0 正文未包含（`pipeline_templates` 表、HTTP 直调入口、cron 触发模板、共享 `runPipeline` 核心） | 根 `CHANGELOG.md` 已记录「编排能力第 2 步（后端）」；按 §3.3 批次基线策略留待下一轮同步 `02` §9、`03` §5.9/§6/§7.2、`05` §7 | 待同步 |

新增偏差时追加一行并写清「位置 / 描述 / 当前事实 / 状态」；修正后把状态改为「已修正（版本）」，不删除历史行，便于追溯。
