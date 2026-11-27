# Workbench 文档中心

> 文档版本：`docs v2.7`｜ 更新日期：2026-11-26 ｜ 状态：现行
> 本文件是 `docs/` 的 **00 号入口**：统一组织全部文档，定义编号规则、目录结构、版本说明、更新记录与维护规范。

## 1. 文档编号规则

| 规则 | 说明 |
|---|---|
| 主文档 | 文件名 `NN-name.md`，`NN` 为两位序号，按**阅读顺序**排列；序号即文档清单中的编号 |
| 入口 | `README.md` 固定为 **00**（GitHub 自动展示入口，故不加数字前缀） |
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
    └── 06-schedule-executable-design.md  # 06 定时任务可执行对象设计
```

| 序号 | 文档 | 读者 | 内容 | 何时读 |
|:---:|---|---|---|---|
| 00 | [`README.md`](README.md) | 全体 + 维护者 | 导航、编号规则、版本、更新记录、维护规范、偏差清单 | 找文档、更新文档、发布前核对 |
| 01 | [`01-getting-started.md`](01-getting-started.md) | 使用者、部署者 | 环境、构建启动、首次超管初始化、登录、SPA 路由注册、公网部署 | 第一次部署 |
| 02 | [`02-functional-guide.md`](02-functional-guide.md) | 使用者 | 功能地图、场景/流程/输入输出/参数/限制、能力矩阵、FAQ、术语表 | 用功能、查参数、排查现象 |
| 03 | [`03-design.md`](03-design.md) | 开发者 | 架构、模块划分、关键流程、数据模型、接口/SSE、安全模型、扩展点、设计取舍 | 改代码、做方案评审 |
| 04 | [`04-build-and-release.md`](04-build-and-release.md) | 开发者、发布者 | 工具链、Makefile target、构建约束、质量门禁、xgo 打包、发布流程、构建故障 | 构建、发版、排查编译问题 |
| 05 | [`05-operations.md`](05-operations.md) | 运维、开发者 | 配置与合并语义、启用矩阵、**RAG 索引运维**、备份、迁移、日志、故障排查 | 配置、升级、索引清理、线上排查 |
| 06 | [`06-schedule-executable-design.md`](06-schedule-executable-design.md) | 开发者 | 设计决策记录：定时任务四种执行对象、target 归一化、调度收敛、静态预授权、迁移 000022–000027；顶部标注现行状态 | 评审调度方案、追溯 P0/P1/P2 决策 |

| 状态 | 含义 | 处理 |
|---|---|---|
| 现行 | 与当前代码一致，随功能变更同步更新 | 按 §5 触发规则维护 |

**事实优先级**：代码 → `02` / `03` / `04` / `05` → 本文件偏差清单 → 根 `README.md`。冲突时以代码为准并登记到 §6。

## 3. 版本说明

### 3.1 版本规则

- 文档版本独立编号 `docs vMAJOR.MINOR`，与代码 SemVer 解耦，但**必须记录对应代码基线**。
- `MAJOR`：文档结构重组（拆分/合并/重命名/编号变更）；`MINOR`：内容增补、事实修正、偏差清理。
- 根 `CHANGELOG.md` 的 `[Unreleased]` 是每个**代码变更**的唯一记录入口；本文件 §4 只记录**文档自身的变更**。

### 3.2 当前基线

| 项 | 值 |
|---|---|
| 文档版本 | `docs v2.7`（2026-11-26；信件式结果邮件与 AI 排版） |
| 代码基线 | `v1.3.0-26-gc0577c4` + 迁移 000021–000027（AI 会话消息、target 五列、调度收敛、执行历史详情、job 重试、模板表删除）；本批功能变更不含新迁移 |
| 工作树增量 | AI 提示词任务的 `allowWrite` 复用为工具白名单 + 非流式 Agent 循环（`internal/server/aijob_agent.go`）、`retry` 判定按写类工具、`AiJobsView` 表单文案按类型切换；定时任务结果邮件改信件式模板 + AI 排版（`notify/email_letter.go`、`server/email_format.go`、`docs/email-template-preview.html` 原型稿），`UserNotifyEvent` 改带 `JobName`；`GET /api/tasks` 列表响应瘦身（预览 80 → 24 rune、短正文不下发、删 `TaskItem.text`/`done` 与外层 `version`/`lastUpdated`）；`go test -tags sqlite_fts5 ./...`、`vue-tsc --noEmit` 通过 |
| 撰写结束后新增 | 000021 已纳入 v2.3；000022–000026 随定时任务改造纳入；000027 随 v2.4 模板移除纳入；SSE 进度流与两处修复随 v2.5 纳入；AI 提示词工具白名单与 Agent 循环随 v2.6 纳入；信件式结果邮件与 AI 排版随 v2.7 纳入 |
| 最近已发布版本 | `v1.3.0`（2026-11-10） |
| 数据库迁移基线 | `000027_drop_pipeline_templates`（27 个版本 / 54 个脚本） |
| 内置 AI 工具 | 16 个（含运行时合成的 `run_pipeline`；8 个需审批） |
| SSE 自定义事件 | AI 对话流 8 类（含 `run_id`）；另编排进度流 `progress` / `end` / `error` |
| 运行时 | Go 1.25.0、`CGO_ENABLED=1`、tag `sqlite_fts5`；Vue 3 + Vite 5；SQLite WAL |

### 3.3 基线策略

重建后采用**以眼前代码为准**：已提交的 HEAD 内容全部纳入；工作树中已调研确认、且具备迁移/测试/`CHANGELOG` 条目的未提交变更也纳入正文，并在 §3.2「工作树增量」显式标注，避免文档因排除 WIP 而反复滞后。**撰写结束后新出现的变更不追写正文**，一律登记为 §6 的待同步项，下一轮按 §5.2 统一纳入——代码并行开发期间，文档基线以批次为单位锁定，避免与进行中的特性互相追赶。若某变更被回滚，按 §5.2 同步回退并更新本表。`docs v2.2` 已按此规则把上一批登记的待同步项统一纳入正文，当前基线对应一次干净的工作树。

## 4. 文档更新记录

| 版本 | 日期 | 变更 | 对应代码基线 |
|---|---|---|---|
| `docs v2.7` | 2026-11-26 | **信件式结果邮件与 AI 排版**：定时任务结果邮件改为固定信件外框（品牌 / 用户名问候 / AI 正文 / 落款 / 页脚）+ 模型整理正文版式；新增 `notify/email_letter.go`（`renderLetterEmailHTML` / `sanitizeAIHTML` / `htmlToPlainText`）与 `server/email_format.go`（`notifyFormatPrompt` / `formatNotifyBody`），`EmailNotifier` 注入 `BodyFormatter`（60s 超时，失败降级卡片式模板）；`UserNotifyEvent` 改带 `JobName` / `UserName`；原型稿 `docs/email-template-preview.html`；同步 `02` §10/§14、`03` §1/§2/§5.8/§9、`04` 单测清单、`05` §3/§9/§10、根 `README.md` Features 与本文件 §3.2/§4 | 同一代码基线 + 工作树（未提交） |
| `docs v2.6` | 2026-11-26 | **AI 提示词定时任务按白名单自主调工具**：`ai` target 的 `allowWrite` 既是模型唯一可见工具白名单、也是该步静态预授权，归一化写进 `llm` 步 `with.tools`；新增非流式 Agent 循环（`aijob_agent.go`，最多 5 轮、轮满强制出文本、越权工具以 error 回灌）、创建时白名单校验、`Retryable` 按写类工具判定；同步 `02` §9.3/§10、`03` §5.8/§6/§9/§10、`04` 单测清单、`05` §7/§9、`06` 顶部现行状态、根 `README.md` Features 与本文件 §3.2/§4；同批纳入 `GET /api/tasks` 列表摘要化（`contentPreview` 80 → 24 rune、短正文不下发、删 `TaskItem.text`/`done` 与响应外层 `version`/`lastUpdated`），同步 `02` §7.1/§7.2、`03` §5.6/§10、根 `CHANGELOG.md` | 同一代码基线 + 工作树（未提交） |
| `docs v2.5` | 2026-11-25 | **编排运行实时进度链路**：新增 SSE 端点 `GET /api/pipeline/runs/{id}/stream`（推送源为 pipeline_runs 表 1s 轮询，`progress` 推整份快照、终态 `end` 关闭、15s 心跳防代理掐断、连接寿命上限 = 执行总超时 + 1min），前端 `streamPipelineRun` + `AiJobsView`「立即执行」后实时叠加流程图（「运行中…」指示、断流降级拉取最终结果、终态刷新执行记录）；两处缺陷修复同步成文：`withToolContext` 经 `db.GetUserNames` 反查补齐 `userName`/`orgName`（修定时任务里 `reindex_files` 拿空用户根目录、扫 0 文件"成功"）、`notify_user` 发布事件改用 `context.Background()`（修异步订阅者 `context canceled`）；同步 `02` §9.1/§9.2、`03` §5.8/§10、`05` §9、根 `README.md` | `v1.3.0-26-gc0577c4` + 工作树（未提交） |
| `docs v2.4` | 2026-11-25 | **编排模板概念整体移除**（迁移 000027 删两张模板表与模板 API）：编排 Definition 内联进 `jobs.target_json`、流程图 / 节点 Inspector / 运行叠加 / 立即执行并入 `/jobs`、`supportedTriggerTargets` 只留 `job`；同步 `01`/`02`/`03`/`04`/`05`/`06`/根 `README.md`；`06` 转为设计决策记录并标注现行状态；迁移基线 26/52 → 27/54、表 24 → 22 项 | `v1.3.0-26-gc0577c4` + 工作树 000027 |
| `docs v2.3` | 2026-11-25 | 定时任务执行对象多态化与调度收敛：`jobs` target 五列（000022）、`jobs` 去调度列 + `schedule_triggers.lease_until` + job 规则回填（000023）、`job_runs` 详情列（000024）、`jobs.retry` 重试（000025）、模板版本历史（000026）；`02` §2.1/§8.3/§10/§13/§14、`03` §2.1/§5.8/§5.9/§6/§7.2/§8/§10、本文件 §2/§3.2/§6 同步；新增 `06` 设计文档；迁移基线 20/40 → 26/52，关闭 D20 | `v1.3.0-26-gc0577c4` + 工作树 000021–000024 |
| `docs v2.2` | 2026-11-25 | 追平 `c0577c4`：编排模板 CRUD/手动触发、通用定时触发规则（000018–000020）、DAG `needs`/`when`/`retry` 与同层并行、`/pipelines` 流程图与模板页、AI 对话 300ms 游标恢复/多会话/`AiMessage`/PC 左栏三 tab；同步 `01`/`02`/`03`/`04`/`05` 的文件头与正文，迁移基线 17/34 → 20/40、表 21 → 23 项，关闭 D16/D18；**移除 `archive/` 归档目录与 A1–A3 编号、删除 §5.6 归档规则**，关闭 D14、D19 收敛为代码注释旧路径 | `v1.3.0-26-gc0577c4` |
| `docs v2.1` | 2026-11-24 | 全量重建新增孤儿索引清理（严格「文件已不存在」判定、按 owner 隔离、`pruned` 计数）；同步 `05` §5.3–§5.5/§9、`02` §8.4/§14、`03` §5.5/§10 | 同一代码基线 + 本次未提交改动 |
| `docs v2.0` | 2026-11-24 | **重建**：主文档加两位序号并重命名；新增 `01` 快速开始、`04` 构建与发布、`05` 配置与运维；`02`/`03` 全量重写并追平后台 run / 1b 恢复 / 声明式编排；根 `README.md` 的 Quick Start 与 Release Process 迁入 `01`/`04` 并改为链接；历史设计稿与模板预览归拢 `archive/`；建立编号规则；偏差清单复核 | `v1.3.0-25-g517f88c` + 工作树 000017 |
| `docs v1.0` | 2026-11-24 | 初建三套文档（功能说明 / 软件设计 / 文档中心）+ 维护规范与偏差清单 | `v1.3.0-23-g8f70235` / 迁移 000015 |
| （历史）v1 | 2026-08-21 | `rbac-design.md`、`migration-strategy.md` 设计稿 | 当时的存量库与自愈式迁移 |
| （历史）— | 未记录 | 邮件模板预览 | 随邮件模板功能引入 |

> 本表只记文档变更；功能与缺陷变更见根 `CHANGELOG.md`。历史行中的 `archive/` 与旧设计稿 / 模板预览文件名仅为追溯记录，当前 `docs/` 已不含这些文件。

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
| SSE 事件 | `internal/server/ai.go` 的 `sseWriteEvent`、`frontend/src/api/ai.ts`（含 `getAIRun(offset)` 的 `delta`） | `03` §7.3、`02` §8.2 |
| 编排定义 | `internal/pipeline/pipeline.go` 的 `Definition`/`Step.Needs`/`When`/`Retry`/`Layers()`/`Runner`、`server/pipeline.go` 的 `runPipeline` | `02` §9、`03` §5.9 |
| 定时任务与通用触发 | `server/aijob_target.go` / `aijob_exec.go` / `aijob_trigger.go`、`internal/db/cron_triggers.go`、`internal/schedule/triggers.go` 的 `NewTriggersJob`/`TriggerHandler`、`server/pipeline_http.go`、`server/schedule_triggers.go` | `02` §9/§10、`03` §5.8/§5.9/§6/§7.2 |
| 事件 topic | `internal/schedule/jobs.go` 的 `KnownTopics`、`notify/email.go` | `03` §5.8 |
| 业务码 | `internal/server/codes.go` | `02` §3 |
| 前端路由与分流 | `frontend/src/router/index.ts`（定时任务与编排共用 `/jobs`）、`config.yaml` 的 `routes` | `02` §12、`03` §5.10 |
| 构建与发布 | `Makefile`、`frontend/package.json`、`vite.config.ts`、`scripts/package.sh` | `04` |
| 版本基线 | `git describe --tags --always --dirty`、迁移目录最大编号 | 本文件 §3.2 |

建议节奏：**每次功能变更随提交核对**；**发版前**全量复核 §6。

## 6. 已知文档偏差清单

> 约定：根 `README.md` 中与构建/发布相关的章节已迁入 `01`/`04`；其余过时描述**仅登记、暂不修改**。

| # | 位置 | 过时/不准确描述 | 当前事实 | 状态 |
|---|---|---|---|---|
| D01 | 根 `README.md` API Endpoints | `/__map__`、`/__tree__`、`/tasks.json` GET/PUT | 实际 `/api/map`、`/api/tree`、`/api/tasks*`；`PUT /api/tasks` 自 1.3.0 废弃，改为带 `baseRevision` 的 `PATCH/POST/DELETE /api/tasks/{id}` | 待修正 |
| D02 | 根 `README.md` Database | 表清单含 `visit_logs`、`version_json`，密码写 SHA-256 | 两表/列已删；密码 bcrypt（仅兼容旧 SHA-256 存量）；现行 **22 项表**见 `03-design.md` §6.2 | 待修正 |
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
| D14 | 原 `archive/rbac-design.md`、`archive/migration-strategy.md` | 状态「设计中 / 未实施」，含 `CGO_ENABLED=0` + modernc、`org_features`、自愈式迁移 | 结论早已被取代：mattn + cgo + sqlite-vec、`user_org_feature`、golang-migrate 版本化迁移（基线 000020）；`docs v2.2` 已移除归档目录与这两份设计稿 | **已移除（docs v2.2）** |
| D15 | 提交 `5d28238`（000016 `ai_runs` 等） | 初版文档未包含 | v2.0 已纳入 `02` §8.5、`03` §5.3/§5.4/§6 | **已同步** |
| D16 | 提交 `c0577c4`（000017 编排引擎） | v2.0 撰写时属未提交的工作树增量 | `02` §9、`03` §3/§5.9/§6/§10 已纳入；000017 已随 `c0577c4` 提交 | **已提交并复核（docs v2.2）** |
| D17 | 根 `README.md` Features / API Endpoints | 未覆盖后台运行、编排模板/定时触发、断点恢复等新能力 | `docs v2.3` 已补 Features（编排、定时任务四类执行对象）、AI 工具 16 个、编排 / 模板版本 / 通用触发 API；断点恢复见 `02` §8.5 | **已补充（docs v2.3）** |
| D18 | 提交 `c0577c4`（000018–000020 编排模板与通用定时触发） | v2.0 正文未包含（`pipeline_templates` 表、HTTP 直调入口、cron 触发模板、共享 `runPipeline` 核心） | 模板只存 Definition，cron/启停/状态已迁至 `schedule_triggers`（000019/000020）；`docs v2.2` 已写入 `02` §9、`03` §5.9/§6/§7.2、`05` §4/§7 | **已同步（docs v2.2）** |
| D19 | `internal/notify/email_template.go` 注释、根 `CHANGELOG.md` 历史条目 | 仍写 `docs/email-template-preview.html` 及旧 `design.md` / 迁移基线 000015 路径 | 邮件模板预览与两份历史设计稿已删除；`CHANGELOG.md` 历史条目按「不删历史行」保留 | 代码注释待修正 |
| D20 | 本文件 §3.2 / `03` §6.1 迁移基线 | `docs v2.2` 写 `000020_pipeline_templates_pure`（20 个版本 / 40 个脚本） | 000021–000026 补齐后为 26 个版本 / 52 个脚本（v2.3 修正）；000027 后为 **27 个版本 / 54 个脚本**，§3.2 与 `03` §6.1、`05` §7 已同步 | **已修正（docs v2.4）** |

| D21 | `02`/`03`/`05`/`06`/根 `README.md` 的编排模板描述 | `docs v2.2`/`v2.3` 把编排模板写成独立功能（`/pipelines` 页、模板表/版本表、模板 CRUD API、`pipeline_template` 触发目标） | 000027 已整体移除模板概念：编排 Definition 内联进 `jobs.target_json`，流程图能力并入 `/jobs`；`06` 转为设计决策记录并加顶部现行状态说明，`02`/`03`/`05`/根 `README.md` 同批同步；结果为邮件跳转死链 `/pipelines?run=` 改 `/jobs?id=` | **已同步（docs v2.4）** |

新增偏差时追加一行并写清「位置 / 描述 / 当前事实 / 状态」；修正后把状态改为「已修正（版本）」，不删除历史行，便于追溯。
