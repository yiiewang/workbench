# 功能说明文档

> 面向：使用者 ｜ 代码基线：`v1.3.0-25-g517f88c` + 工作树中的编排引擎（迁移 000017）
> 环境、构建与首次启动见 [`01-getting-started.md`](01-getting-started.md)；配置与运维见 [`05-operations.md`](05-operations.md)；构建发布见 [`04-build-and-release.md`](04-build-and-release.md)；架构与接口见 [`03-design.md`](03-design.md)；文档维护规范见 [`README.md`](README.md)。

## 1. 关于本文档

| 项 | 说明 |
|---|---|
| 覆盖范围 | 核心功能、使用场景、操作流程、输入输出、参数、限制条件、常见问题 |
| 不覆盖 | 快速开始/部署步骤、配置默认值与环境变量、构建发布、内部实现与表结构 |
| 事实优先级 | **代码 > 本文档 > 根 `README.md`**；已知偏差登记在 [`README.md`](README.md#6-已知文档偏差清单) |
| 引用约定 | 用「文件路径 + 函数/类型/常量名」定位代码，不写行号 |

## 2. 系统概览

Workbench 是个人开发工作台，交付形态为**一个 Go 二进制**：Vue3 前端产物经 `//go:embed` 内嵌，业务数据全部落在单个 SQLite 文件，用户文件放 `server.static_dir` 磁盘目录。除 AI 网关、向量模型、SMTP、外部 MCP 服务外无外部依赖。

### 2.1 功能地图

| 模块 | 入口（PC / 移动端） | 说明 |
|---|---|---|
| 文件浏览与预览 | `/file` / `/m/files`、`/m/file` | 目录树、多标签、Markdown/代码/JSON/XML/HTML/图片/音视频预览 |
| 文件管理 | 侧栏工具栏、右键菜单 / 不提供 | 上传、新建目录、重命名、移动、删除；软链接识别 |
| 分享 | `/shares`、Tab 栏 Share / 仅访问 | 密码、有效期、访问次数上限的公开链接 `/s/{token}` |
| Todo 看板 | `/todo` / `/m/todo` | 四象限/看板/日历甘特、任务↔文件关联、多设备同步 |
| AI 对话 | 右下浮动面板 / `/m/ai` | 流式对话、推理展示、`@` 引用、Agent 工具与审批、RAG 引用、会话历史、**断连后后台跑完并恢复** |
| 声明式编排 | AI 对话内 `run_pipeline` | 模型自主把多个工具/站内 API/LLM 调用编成步骤序列一次执行，逐步落库可查询 |
| AI 定时任务与通知 | `/jobs` / 通过 `/jobs` 访问 | cron 定时执行 prompt、执行历史、邮件日报与任务提醒 |
| 用户与组织管理 | `/admin`（仅管理员） / 不提供 | 用户增删改、重置密码、角色、按用户×组织的功能开关 |
| 账号 | Account 弹窗 / `/m/profile` | 通知邮箱、组织切换、登出 |

### 2.2 角色与数据隔离

| 概念 | 取值 | 影响 |
|---|---|---|
| 平台超管 | `admin` | 跨组织管理全部用户，可授予 `admin` 角色 |
| 组织内角色 | `owner` / `admin` / `member` | 前两者对外显示 `org_admin`，可管理本组织成员 |
| 功能开关 | `file` / `share` / `todo` / `admin` | 按「用户 × 组织」配置，控制菜单入口显隐 |
| 用户文件根 | `{static_dir}/{orgName}/{userName}/` | 每账号只能访问自己目录，越界与软链接逃逸被拒 |
| 数据库分区 | `org_id + user_id` | 任务、会话、后台运行、编排运行、RAG 向量按归属隔离 |

> 用户管理入口由角色控制，不叠加 `admin` 功能开关，否则关闭后无法重新开启。

## 3. 通用约定

| 项 | 约定 |
|---|---|
| 认证 | `Authorization: Bearer <token>` + `X-Org-Id: <正整数>`；用户名全局唯一，登录时 `orgId` 可省略 |
| 响应信封 | `{ "code": 0, "msg": "ok", "data": ... }`，`code != 0` 即失败 |
| 错误码 | `HTTP 状态码 × 100 + 序号`，如 `40901` 任务冲突、`40902` 重建进行中 |
| 路径参数 | 以 `/` 开头，相对**当前用户根目录**；`..` 与 URL 编码穿越均被拒绝 |
| 日期时间 | 任务 `YYYY-MM-DD`；精确时刻 `HH:mm`（`00:00` = 未指定）；分享时间 RFC3339 |
| CORS | `/api/*` 返回 `Access-Control-Allow-Origin: null`，配合沙箱 iframe；不使用 Cookie |

常用业务码：`40102`/`40103` 缺 token / 无效或过期、`40301` 未设置密码、`40304`/`40305` 分享需密码 / 密码错、`40306`/`41001`/`41002` 分享未生效 / 过期 / 次数用尽、`40901` 任务乐观锁冲突、`40902` 全量重建进行中、`42901` 限流、`50301` AI 未配置。完整清单见 [`03-design.md`](03-design.md#71-统一约定)。

## 4. 文件浏览与预览

**场景**：查看笔记、代码、配置、报告、音视频；Markdown 内链与标题导航；从 AI 引用跳回原文行。

**流程**：打开 `/file` → 侧栏目录树懒加载展开 → 点击文件开新 tab → 按扩展名自动选择预览组件 → 可下载、分享、AI 摘要（仅 Markdown）、查看/新建「关联任务」。

| 输入 | 输出 |
|---|---|
| `GET /api/tree?path=<目录>` | 目录树（受 `server.hidden` 过滤，软链接带 `isLink` 标识） |
| `GET /api/file?path=<文件>` | 原始字节流（需 Bearer） |
| `GET /api/media/ticket?path=<媒体>` | `{url, size}`，`url` 内含 2 小时 HMAC ticket |
| `GET /api/media?...&ticket=...` | Range 流式播放（无需 Bearer，ticket 自带凭证） |
| `GET /api/tasks/by-file?path=...` | 关联了该文件的任务 |

**预览能力**：Markdown（DOMPurify 净化、Mermaid、表格滚动、标题折叠、悬浮目录、`==高亮==`）、代码（highlight.js、行号、`#region` 折叠、超大文件降级）、JSON/XML 树、HTML（沙箱 `srcdoc` + 锚点拦截）、图片、音视频、二进制卡片。

**限制**：`allow_symlink` 默认关闭，指向根外的软链接 404；`server.hidden` 默认隐藏点开头文件与压缩包，命中文件仍可上传但不显示在树中（上传响应另列 `hiddenNames`）；HTML 预览运行在无 `allow-same-origin` 的沙箱 iframe；`/api/ai/chat` 与 `/api/media` 跳过响应压缩（保证 SSE 与 Range 语义）。

## 5. 文件管理

**场景**：上传资料、整理目录、重命名/移动/删除；AI「把回复保存为文件」也走上传接口。

| 操作 | 入口 | API | 参数 |
|---|---|---|---|
| 上传 | 侧栏 Upload / 文件选择器 | `POST /api/upload`（multipart） | `path`=目标目录（默认 `/`）、`files`=多文件 |
| 新建目录 | 右键 → New folder | `POST /api/mkdir` | `{path}`=完整目标路径（单层） |
| 重命名 | 右键 → Rename | `POST /api/rename` | `{path, newName}`（同目录） |
| 移动 | 右键 Move → 目标目录 Move here | `POST /api/move` | `{path, destDir}`（保持原名） |
| 删除 | 右键 → Delete | `DELETE /api/file?path=...` | 目录递归删除，不可恢复 |

上传响应 `{uploaded:[{name,size}], skipped:[{name,reason}], hiddenNames:[...]}`；**同名跳过不覆盖**（`already exists`），非法名与写失败也进 `skipped`。

**限制**：单请求 512 MB / 100 文件；目标须是用户根内已存在的目录；禁止删除用户根；移动带环检测；对软链接条目只操作链接本身；所有写操作记录审计日志。删除会同步清理对应 RAG 索引（详见 [`05-operations.md`](05-operations.md#52-删除文件时会清理哪些-rag-数据)）。

## 6. 分享

**场景**：把文件或整个目录以临时链接发给他人（对方无需账号），可加密码、访问次数与生效/过期时间。

**创建**：文件 tab 点 Share（或目录右键 Share）→ 填表单 → 创建后自动复制「链接 + 密码 + 备注」→ `/shares` 查看、复制、撤销。

| 参数 | 说明 |
|---|---|
| `resourcePath` | 用户根内路径，服务端强制 `/` 开头并拒绝 `..` |
| `resourceType` | 表单值 `file`/`dir`；**服务端按实际 `os.Stat` 覆盖** |
| `maxAccessCount` | `0` = 无限 |
| `password` | 留空 = 无密码；服务端存 bcrypt 哈希，明文只缓存在创建者浏览器便于复制 |
| `remark` | 备注，随链接文本一起复制 |
| `effectiveAt` / `expiresAt` | RFC3339，留空 = 不限 |

**访问**：`/s/{token}`（移动端自动 `/m/s/{token}`）→ 前端切到分享模式 → 需要密码时弹窗重试（最多 5 次）→ 文件/目录共用 `GET|POST /api/share/{token}`（带密码用 POST）。

**限制**：密码错误 5 次/5 分钟（`42901`）；超限/过期/未生效分别 `41002`/`41001`/`40306`；分享模式隐藏上传、右键菜单、AI 面板、Share/AI 摘要；移动端只提供访问，不提供创建与管理。

## 7. Todo 任务

**场景**：日常任务管理（四象限、看板拖拽、日历与甘特）、按日期与精确时刻提醒、任务↔文件双向关联、多设备同步。

### 7.1 任务字段

| 字段 | 取值 / 说明 |
|---|---|
| `id` | UUID，客户端生成 |
| `title` / `content` | 正文为 Markdown；列表响应不含 `content`，只有 80 字 `contentPreview` |
| `status` | `todo` / `progress` / `done`（待办/进行中/已完成）；进度 100 与 `done` 联动 |
| `priority` | `high` / `medium` / `low`，默认 `medium` |
| `scheduled` / `due` | `YYYY-MM-DD` |
| `scheduledTime` / `dueTime` | `HH:mm`；`00:00` 归一为空（避免凌晨提醒风暴） |
| `progress` / `assignee` / `postponedCount` / `autoPostponed` / `sortOrder` | 进度、负责人、延期次数等 |
| `files` | 关联的用户根内路径，上限 20 条 |

### 7.2 流程与接口

| 动作 | API | 输入 | 输出 |
|---|---|---|---|
| 拉全量 | `GET /api/tasks` | — | `{revision, tasks}`（仅本人任务） |
| 拉详情 | `GET /api/tasks/{id}` | — | `{task（含 content 全文）, revision}` |
| 轻量版本 | `GET /api/tasks/revision` | — | `{revision}`，30s 轮询对账 |
| 增 / 改 / 删 | `POST` / `PATCH` / `DELETE /api/tasks/{id}` | `{task, baseRevision}`（DELETE 走 query） | 新 `{revision}`；落后返回 `40901` + 服务端副本 |
| 成员日程 | `GET /api/tasks/user/{id}` | — | 同组织 active 成员任务（只读） |
| 文件反查 / 未完成任务 | `GET /api/tasks/by-file?path=`、`GET /api/tasks/open` | — | 关联任务 / `status != done` |

**多设备同步**：所有写携带 `baseRevision`；每 30 秒及页面回前台比对 `/api/tasks/revision`，不一致自动重拉（弹窗打开/冲突待裁决/离线/查看他人时跳过）。冲突时差异字段与服务端一致则静默采用服务端，否则弹**单任务字段级对比卡**逐项选「我的/服务器」；新增/删除冲突直接重拉。编辑弹窗先拉详情成功才打开，避免列表瘦身后保存清空正文。支持 JSON 导入导出、`Ctrl/Cmd+S`、`/todo?task=<id>` 定位。

**提醒**：`schedule.enabled=true` 时，到期扫描（默认每 5 分钟）对未完成且 `due <= 今天` 发 `task.due`；精确时刻扫描（每分钟）对 `scheduledTime`/`dueTime` 发 `task.reminder`，`due` 提醒过滤已完成任务。两类按「任务 + 日期/时刻」去重，收件人为任务归属用户的通知邮箱。

**限制**：`PATCH` 请求体中未出现的字段保留服务端现值（快捷勾选/拖拽不清空正文），显式空串才是主动清空；旧版 `PUT /api/tasks` 全量同步已废弃；移动端仅分组列表、状态轻编辑与详情，无三视图/冲突卡/轮询/导入导出。

## 8. AI 对话

**场景**：基于 OpenAI 兼容网关的流式问答；推理过程展示；引用个人文件提问；让 Agent 读写文件、管理任务与定时任务；RAG 自动检索并给出可跳转引用；**关掉面板或断网后，回答仍会在后台跑完并可恢复**。

### 8.1 启用条件

| 能力 | 开关 | 生效条件 |
|---|---|---|
| 基础对话 | `ai.chat` | `api_key` 与 `base_url` 均非空 |
| Agent 工具 | `ai.chat.tools: true` / `AI_TOOLS=true` | 上游模型支持 function calling；默认关闭（纯对话） |
| RAG 语义检索 | `ai.embedding` | `base_url` 与 `model` 非空；本地服务可留空 `api_key` |
| 相关性预判 / 摘要卡 | `judge_model` / `digest_model` | 可选，复用 embedding 网关的 `/chat/completions` |
| 外部 MCP 工具 | `ai.mcp.servers` | 仅 HTTP 型无状态服务；headers 支持 `${ENV_VAR}` |

`GET /api/ai/status` 返回 `{enabled, model, tools}`，前端据此决定是否显示入口。完整启用矩阵见 [`05-operations.md`](05-operations.md#4-功能启用矩阵)。

### 8.2 输入输出

| 项 | 说明 |
|---|---|
| 请求 | `POST /api/ai/chat`，`{messages, qaId?, sessionId?}`；`messages` 为 OpenAI chat 格式，≤30 条，请求体 ≤512 KB；`qaId`/`sessionId` 为 ≤64 可打印标识，透传为上游 `X-QA-Id`/`X-Session-Id` |
| 响应 | `text/event-stream`；默认事件为 OpenAI 兼容增量（`delta.content` / `delta.reasoning_content`），结束 `data: [DONE]` |
| 自定义事件 | `run_id`、`rag_hits`、`usage`、`tool_call` / `tool_result`、`approval_request` / `approval_resolved`、`error` |

### 8.3 内置工具（16 个，`ai.tools=true` 时）

| 类别 | 工具 | 关键参数 | 需审批 |
|---|---|---|---|
| 文件 | `list_dir` / `read_file` | `path` | 否 |
| 文件 | `write_file` | `path`, `content`（≤256 KB，父目录须已存在） | **是** |
| 检索 | `search_files` | `query`, `topK`（1–10，默认 5） | 否 |
| 任务 | `list_tasks` | `includeDone` | 否 |
| 任务 | `create_task` | `title`（必填）、`content`、`priority`、`scheduled`/`due`、`scheduledTime`/`dueTime` | 否 |
| 任务 | `update_task` | `taskId` + 待改字段（先读后合并） | **是** |
| 定时任务 | `list_ai_jobs` | 无 | 否 |
| 定时任务 | `create_ai_job` / `update_ai_job` / `delete_ai_job` | `name`/`cronExpr`/`prompt` / `jobId` | **是** |
| 事件 | `list_events` | 无（内省事件总线 topic 与订阅者数） | 否 |
| 通知 | `notify_user` | `subject`, `body`（≤32 KB） | **是** |
| 索引 | `reindex_files` | 无（触发本人文件全量重建，后台异步） | **是** |
| 索引 | `reindex_status` | 无（返回进度快照副本） | 否 |
| 编排 | `run_pipeline` | `name`, `allowWrite`, `steps`（见 §9） | **是** |

写类工具执行前，SSE 流发 `approval_request` 并暂停；用户在工具卡片点「批准/拒绝」，决定经 `POST /api/ai/approval {id, approved}` 提交。**5 分钟未确认按拒绝处理**，关闭面板或停止生成会作废未决审批；拒绝/超时以说明性结果回传模型继续对话。所有外部 MCP 工具（`mcp_<server>_<tool>`）一律需审批。工具结果截断到 32 KB，Agent 最多 5 轮工具调用，达到上限时强制用已有结果作答。

> `create_task` 是写操作但注册表未设 `NeedApproval`，与「写操作先请求确认」的系统提示表述不完全一致；其余写类工具均需审批。

### 8.4 RAG 文件检索

- **索引**：上传、AI `write_file`、删除/重命名/移动自动维护索引；24 类文本扩展名、≤2 MiB；隐藏文件/隐藏目录与 `node_modules`/`__pycache__`/`vendor`/`target` 整棵跳过；整文件 SHA-256 判重，内容未变零成本跳过。
- **切块**：空行分段，800 字目标 + 100 字重叠，记录 1-based 起始行；可选文件级摘要卡（`chunk_index=-1`）。
- **检索**：向量 KNN + FTS5 trigram 关键词双路召回，RRF（`score = Σ 1/(60+rank)`）融合取 top-5；向量路受 `max_distance`（默认 0.9）过滤，关键词路不受；可选 `judge_model` 批量预判；短问题（<30 字）拼上一条 user 消息补语境。
- **展示**：命中经 `rag_hits` 推送，注入 prompt 时编号 `[1]…[n]`；回答底部「引用来源」只列正文实际引用到的编号，点击跳转文件并定位行（PC `#L12`，移动 `?line=12`）。
- **运维**：`POST /api/ai/reindex`（会按 owner 清孤儿索引，进度含 `pruned`）、`GET /api/ai/reindex/status`、`GET /api/ai/index-status`、`make ragclean`；**删除文件与全量重建对索引的处理**详见 [`05-operations.md`](05-operations.md#5-rag-索引运维)。

### 8.5 后台运行与断连恢复

一次 `/api/ai/chat` 被抽象成一个后台 run，**关闭面板、断网、刷新页面不会掐断 Agent 循环**：

| 阶段 | 行为 |
|---|---|
| 开流 | 后端创建 `ai_runs` 记录（`running`），SSE header 就绪后先推 `run_id` 事件，前端把 runId 存 `sessionStorage` |
| 后台 | 上游请求与全部工具执行绑定独立 `runCtx`（总超时 15 分钟），不再依赖 HTTP 请求 ctx；`aiRunWriter` 在首次写失败后标记断连并静默吞掉后续写入，循环照常跑完 |
| 落库 | 每个工具轮结束落一次快照，退出时由 defer 写最终态（`done`/`failed`）；`events` 保存 `run_id`/`rag_hits`/`tool_call`/`tool_result`/`usage`，`result` 累积各轮文本 |
| 审批 | 断连后写类工具无人确认，直接跳过并回传「用户已断开连接，写操作已跳过」让模型基于已有结果收尾；审批等待中断开同样作废 |
| 恢复 | 静默断流时前端保持 `streaming` 并启动 2 秒轮询 `GET /api/ai/runs/{id}`（上限 15 分钟），不再补「（已中断）」占位；`applyRunSnapshot` 用后端 `result` 覆盖半截正文并按 id 补齐工具调用、引用与 token 用量 |
| 刷新 | `maybeRestoreLastSession` 末尾触发 `resumePendingRun`：优先本地 runId（含已完成的断流遗留，30 分钟过期保护），本地丢失时用 `GET /api/ai/runs` 兜底找仍 `running` 的；PC 与移动端共用同一逻辑 |

查询接口：`GET /api/ai/runs?limit=`（最近 20 条，不含 events）、`GET /api/ai/runs/{id}`（含事件，按 org+user 隔离）。`stopStreaming` 会停止轮询但**服务端 run 仍会跑完**，结果仍可查。

**限制**：运行状态不跨进程重启恢复（循环绑定当前进程）；后台总超时 15 分钟，单次上游流式 10 分钟；用户主动停止与业务错误仍走原有收尾，不进入轮询恢复。

### 8.6 会话与消息操作

会话历史存 `ai_sessions`，流式期间 3 秒节流保存快照（含工具调用、引用、token 用量），刷新后自动接上最近会话（同 tab 内主动「新对话」且尚未提问时除外）。列表最多 50 条、只取摘要；长会话按「轮」截断（展开后 ≤28 条），默认只渲染最近 5 条。消息支持复制、重新生成、保存为文件（`/ai/YYYY-MM-DD-主题.md`）。PC 面板另有宽度拖拽、剧场模式、消息目录刻度、索引状态抽屉。

**其他限制**：`@` 引用仅文本类文件，单文件截断 20k 字；`read_file` 本体 ≤4 MB、内容 ≤64 KB，前 512 字节含 NUL 视为二进制；检索 8 秒超时静默降级；修改 `ai.embedding.dim` 会作废全部向量，必须全量 reindex；上游不支持 `include_usage` 时不显示 token 消耗。

## 9. 声明式编排（`run_pipeline`）

**场景**：让 AI 自主规划多步任务——例如「读取这份周报 → 让 LLM 提炼要点 → 创建一条任务 → 发通知」，一次确认后按序执行，每步结果可查。无需改造 Agent 循环：模型自己构造步骤数组并调用 `run_pipeline`。

### 9.1 输入输出

| 项 | 说明 |
|---|---|
| 输入 | `run_pipeline` 工具参数 `name`（流程名）、`allowWrite`（显式授权的写操作列表）、`steps`（步骤数组） |
| 步骤 | `{id, uses, with}`；`id` 为 1–64 位字母/数字/`_`/`-`，全局唯一；`uses` ∈ `tool/<工具名>`、`api/<站内 /api/* 路径>`、`llm` |
| 插值 | `with` 中的字符串支持 `{{ steps.<id>.output }}` 引用前序步骤输出（字符串直嵌，其余 JSON 序列化；未产出的引用原样保留） |
| 输出 | 工具只回传步骤摘要；完整每步输出走 `GET /api/pipeline/runs/{id}` |
| 状态查询 | `GET /api/pipeline/runs?limit=`、`GET /api/pipeline/runs/{id}`，按 org+user 隔离 |

示例（概念）：

```json
{
  "name": "周报提炼并建任务",
  "allowWrite": ["create_task"],
  "steps": [
    { "id": "read", "uses": "tool/read_file", "with": { "path": "/notes/weekly.md" } },
    { "id": "summary", "uses": "llm", "with": { "prompt": "提炼要点：{{ steps.read.output }}" } },
    { "id": "task", "uses": "tool/create_task", "with": { "title": "{{ steps.summary.output }}" } }
  ]
}
```

### 9.2 操作流程

1. 模型在对话中给出 Definition → 触发一次**整条流程的界面确认**（`run_pipeline` 是写类工具）。
2. 用户在工具卡片批准后，Runner 顺序执行：每步独立 5 分钟超时；失败即停，剩余步骤标记 `skipped`；每步产出经 `onStep` 落库到 `pipeline_runs`。
3. 三类能力由服务端注入：`tool/<name>` 复用现有 AI 工具；`api/<path>` 仅限站内 `/api/*`（自签短期 token + `X-Org-Id`，经内部 `ServeHTTP` 调用）；`llm` 走非流式 `aiChatOnce`。
4. 结果与状态随时可经查询接口获取，断连后仍会落库。

### 9.3 限制条件

- **写操作必须预授权**：写类工具与非 GET 的 `api` 步骤必须在 `allowWrite` 显式列出，否则该步失败；这是「一次确认覆盖整条流程」的安全前提。
- 最多 50 步，仅**顺序执行**；第一版无模板存库、无 HTTP 直调入口、无 cron 触发、无条件分支与重试；Definition 目前只来自工具参数（JSON）。
- `api/<path>` 只能调站内 `/api/*`，不能访问外部地址；工具执行仍走各自沙箱与结果限长。
- 每步输出会写入 `pipeline_runs.steps`，大结果会增大数据库。

## 10. AI 定时任务与通知

**场景**：每天 18:00 让 AI 汇总当天任务并发日报邮件；每周一 09:00 生成周报；任务到期/计划开始时刻收到提醒。

| 参数 | 说明 |
|---|---|
| `name` / `prompt` | 任务名与指令；结果按 Markdown 输出，作为 HTML 邮件正文 |
| `cronExpr` | 标准 5 段 cron，支持 `*`、`*/n`、`a-b`、逗号列表、英文别名（`wed`/`nov`）；创建时校验，非法即失败 |
| `enabled` | 增删改下一分钟生效，无需重启 |

前端预设 `0 18 * * *`、`0 9 * * *`、`0 9 * * 1`、`0 18 * * 5`、`0 * * * *`。接口：`GET|POST /api/ai/jobs`、`PUT|DELETE /api/ai/jobs/{id}`、`GET /api/ai/jobs/{id}/runs?limit=`。

**全局公共任务**：`ai_jobs.user_id IS NULL`（预置「每日工作总结」`0 18 * * *`）所有用户可见但只读，执行时对所有**已配置邮箱且关系 active** 的用户逐个以本人身份展开（各自任务数据、各自投递）；执行历史对全局任务所有用户可查，私有任务仅归属人可查；`ai_job_runs.output` 只留输出前 4 KB。

**通知链路**：`schedule.enabled` 开启后 cron 每分钟扫描；命中任务经事件总线 `user.notify`（成功/失败）或 `task.due` / `task.reminder` 分发，邮件订阅者用 SMTP 587 STARTTLS 投递给任务归属用户邮箱。收件邮箱在 `/admin` 填写，或移动端「我的」页自助修改（空串 = 不再接收）；`EMAIL_PASS` 提供授权码。

**限制**：`notify.email.enabled` 必须配合 `schedule.enabled`，且 `host`/`user` 必填（缺失启动即失败）；未配置 `site_url` 时邮件不含跳转按钮；去重发生在发布事件之前，`task.due`/`task.reminder` 当天只投一次且 **SMTP 失败不重投**；`user.notify` 不去重；LLM 超时 5 分钟，prompt 最多附带 50 条任务。

## 11. 用户与组织管理（`/admin`）

| 操作 | API | 参数 |
|---|---|---|
| 用户 / 角色列表 | `GET /api/admin/users[?orgId=]`、`GET /api/admin/roles` | 超管可跨组织 |
| 创建 | `POST /api/admin/users` | `{org, name, password, roleId, mobile?, email?}` |
| 编辑 / 重置密码 | `PATCH /api/admin/users/{id}` | `{name?, mobile?, email?, password?, roleId?}`（只传要改的） |
| 删除 | `DELETE /api/admin/users/{id}` | 不能删自己或最后一个 admin |
| 看板统计 | `GET /api/admin/users/{id}/dashboard` | `{totalTasks, doneTasks, shareCount}` |
| 功能开关 | `GET /api/admin/users/{id}/features`、`PATCH .../{code}` | `{enabled}`；`code` ∈ `file/share/todo/admin` |

**限制**：仅 PC 提供；入口对 `admin` 与 `org_admin` 可见，组织管理员不能授予 `admin` 角色、不能跨组织操作，新建用户组织锁定为自身组织；给自己改开关即时刷新菜单；用户名全局唯一。前端**没有「设置密码」页面**，首用户初始化见 [`01-getting-started.md`](01-getting-started.md#4-首次初始化创建第一个超管)。

## 12. 移动端与能力矩阵

视口 ≤900px 时路由守卫把多数 PC 路径重定向到 `/m/*`（登录页不参与分流），桌面打开 `/m` 回 PC；分享链接自动改写到 `/m/s/{token}`。移动端是**独立的浏览优先视图集**，与 PC 共用 api / stores / lib / 预览组件与 AI 对话核心 `useAIChat`。

| 能力 | PC | 移动端 |
|---|:---:|:---:|
| 文件浏览与全类型预览 | ✅ 树 + 多 tab | ✅ 单层目录下钻，复用预览组件 |
| 上传 / 新建 / 重命名 / 移动 / 删除 / 多 tab | ✅ | — |
| 分享创建与管理 | ✅ | — |
| 分享链接访问（含密码重试） | ✅ | ✅ 专属视图 |
| Todo 三视图 / 拖拽 / 导入导出 / 冲突卡 / 轮询 | ✅ | — |
| Todo 查看、状态切换、详情 | ✅ | ✅ 分组列表 + 轻编辑 |
| AI 对话（流式、`@`、审批、历史、RAG 引用、消息操作） | ✅ 浮动面板 | ✅ 全屏页，能力一致 |
| 后台运行断连恢复（`run_id` 轮询） | ✅ | ✅ 共用 `useAIChat` |
| 声明式编排（`run_pipeline` 审批与卡片） | ✅ | ✅ 同一工具链路 |
| AI 面板剧场模式 / 刻度 / 索引抽屉 / 宽度拖拽 | ✅ | — |
| AI 定时任务 | ✅ `/jobs` | ✅ 首页入口指向 `/jobs` |
| 通知邮箱编辑 | — | ✅「我的」页 |
| 用户管理 | ✅ | — |

> 复杂管理流程刻意留在 PC。`/jobs` 不在 PC→移动映射表中，窄视口下仍以响应式 PC 布局呈现。

## 13. 限制条件汇总

| 类别 | 限制 |
|---|---|
| 上传 | 512 MB / 100 文件每请求；同名跳过；目标目录须已存在 |
| 认证 | token 默认 30 天（示例配置 7 天）；不能单独吊销，轮换密钥使全部失效；登录失败 5 次/分钟/IP |
| 分享 | 密码错误 5 次/5 分钟；次数/时间窗服务端校验 |
| AI 对话 | 请求体 512 KB、消息 ≤30 条、单次上游 10 分钟、**后台 run 总超时 15 分钟**、Agent ≤5 轮工具、工具结果 32 KB、`write_file` 256 KB、`read_file` 4 MB（内容 64 KB）、审批 5 分钟 |
| 断连恢复 | 2 秒轮询 `/api/ai/runs/{id}`，上限 15 分钟；本地 runId 30 分钟过期保护；运行状态不跨进程重启 |
| 会话 | 快照 JSON ≤256 KB，列表最多 50 条，回传历史展开 ≤28 条 |
| 编排 | ≤50 步、每步 5 分钟、仅顺序执行；写操作须 `allowWrite` 预授权；无模板/HTTP 直调/cron/条件/重试 |
| RAG | 单文件 ≤2 MiB + 24 类白名单、query ≤2000 字、检索 8 秒超时、对话 top-5、索引 5 分钟超时 |
| 定时任务 | 每分钟扫描、LLM 5 分钟、prompt ≤50 条任务、执行记录输出前 4 KB |
| 通知 | 587 STARTTLS 强制；`EMAIL_PASS` 必填；投递前去重，SMTP 失败不重投 |
| 媒体 | ticket 2 小时，绑定用户与路径；分享模式降级整文件 blob |
| MCP | 仅 stateless HTTP（JSON-RPC 2.0），不支持 stdio / 有状态会话 / 重连 |
| 存储 | 单 SQLite 文件、WAL、单连接；无进程级 SIGTERM 优雅关闭 |

## 14. 常见问题

| 现象 | 处理 |
|---|---|
| 登录报 `Password not set` | 首用户按 [`01-getting-started.md`](01-getting-started.md#4-首次初始化创建第一个超管) 调 `/api/set-password`；其余账号联系管理员 |
| 刷新 `/file`、`/admin`、`/jobs` 返回 401 | `config.yaml` 的 `routes` 未注册为 `__spa__` |
| AI 入口不显示 | `GET /api/ai/status` 的 `enabled=false`；检查 `AI_API_KEY` 与 `base_url` |
| 关闭面板/断网后回答丢了 | 服务端后台 run 仍会跑完；刷新后会自动按 runId 恢复，也可查 `GET /api/ai/runs` |
| 编排里写步骤失败 | 该工具名或非 GET 的 api 路径未写进 `allowWrite`；补上后重新执行 |
| 编排结果太长看不到完整输出 | 工具只回步骤摘要，用 `GET /api/pipeline/runs/{id}` 查每步完整输出 |
| 删掉的文件仍被 AI 检索/引用 | 多为绕过接口删除留下的孤儿索引；执行一次 `POST /api/ai/reindex` 即会按 owner 清掉，详见 [`05-operations.md`](05-operations.md#53-全量重建会清理孤儿索引) |
| 上传成功但文件树看不到 | 命中 `server.hidden`，查看上传响应 `hiddenNames` |
| 多设备编辑任务提示冲突 | `baseRevision` 落后（40901），在字段级对比卡选择保留本地或服务端值 |
| 收不到提醒邮件 | 见 [`05-operations.md`](05-operations.md#9-常见运维问题) 的排查顺序 |
| token 疑似泄漏 | 轮换 `app_secrets` 的 token 密钥并重启，全部用户重新登录 |
| 移动端找不到上传/分享管理/用户管理 | 设计如此，复杂操作在 PC 端完成 |
| 构建报 `no such module: fts5` | 见 [`04-build-and-release.md`](04-build-and-release.md#9-常见构建故障) |

## 15. 术语表

| 术语 | 含义 |
|---|---|
| 用户根 | `{static_dir}/{orgName}/{userName}/`，每个账号的私有文件目录 |
| revision | 服务端权威任务版本号，写操作 +1，客户端用 `baseRevision` 做乐观锁 |
| 分享 token | `/s/{token}` 中的随机串，公开访问凭证（配合密码/时间/次数） |
| media ticket | 音视频无法带 Bearer 时换取的 2 小时 HMAC URL |
| RAG / 混合检索 | 向量 KNN + FTS5 trigram 双路召回，RRF 融合 |
| Agent | 服务端循环调用工具并把结果回传模型的多轮对话模式 |
| HITL 审批 | 写类工具执行前需用户在独立请求中确认 |
| 后台 run / runId | 一次 `/api/ai/chat` 的后台执行实体与标识；断连后按此轮询恢复 |
| Definition / Step | 编排的纯数据定义（`name`/`allowWrite`/`steps`）与单个步骤（`id`/`uses`/`with`） |
| `allowWrite` | 编排中对写类工具与非 GET api 步骤的显式预授权清单 |
| 全局公共任务 | `ai_jobs.user_id IS NULL` 的只读任务，执行时对所有有邮箱用户展开 |
| `__spa__` | `routes` 配置值，表示该路径返回内嵌 SPA 入口 `index.html` |
