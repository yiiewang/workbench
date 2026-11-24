# Workbench 功能说明文档

> 文档版本：v1.0 ｜ 面向：使用者、部署运维 ｜ 事实来源：当前代码与 `config.yaml`
> 维护规范、版本对应关系与已知文档偏差见 [`README.md`](README.md)；面向开发者的架构与接口设计见 [`design.md`](design.md)。

## 1. 关于本文档

| 项 | 说明 |
|---|---|
| 覆盖范围 | 系统核心功能、使用场景、操作流程、输入输出、参数、限制条件、常见问题 |
| 不覆盖 | 内部实现、模块划分、表结构、扩展点（见 [`design.md`](design.md)）；构建发布流程（见根 `README.md` 的 Release Process） |
| 事实优先级 | **代码 > 本文件 > 根 `README.md`**。根 `README.md` 存在若干过时描述，已登记在 [`README.md`](README.md#6-已知文档偏差清单) 的偏差清单中 |
| 引用约定 | 引用代码位置统一用「文件路径 + 函数/常量名」，不写行号（行号随提交漂移） |

## 2. 系统概览

Workbench 是个人开发工作台：文件浏览与预览、Todo 看板、AI 对话与语义检索、定时提醒、文件分享、用户与组织管理。交付形态为**一个 Go 二进制**：Vue3 前端产物经 `//go:embed` 内嵌，业务数据全部落在单个 SQLite 文件（`data/workbench.db`，WAL 模式），用户文件放在 `server.static_dir` 目录下。除 AI 网关、向量模型、SMTP、外部 MCP 服务外，无其他外部依赖。

### 2.1 功能地图

| 模块 | 入口（PC / 移动端） | 说明 |
|---|---|---|
| 文件浏览与预览 | `/file` / `/m/files`、`/m/file` | 目录树、多标签、Markdown/代码/JSON/XML/HTML/图片/音视频预览 |
| 文件管理 | 侧栏工具栏、右键菜单 / 不提供 | 上传、新建目录、重命名、移动、删除；软链接识别 |
| 分享 | `/shares`、Tab 栏 Share / 仅访问 | 带密码、有效期、访问次数上限的公开链接 `/s/{token}` |
| Todo 看板 | `/todo` / `/m/todo` | 四象限/看板/日历甘特、任务↔文件关联、多设备同步 |
| AI 对话 | 右下浮动面板 / `/m/ai` | 流式对话、推理展示、`@` 引用文件、Agent 工具、审批、RAG 引用、会话历史 |
| AI 定时任务与通知 | `/jobs` / 通过 `/jobs` 访问 | cron 定时执行 prompt、执行历史、邮件日报与任务提醒 |
| 用户与组织管理 | `/admin`（仅管理员） / 不提供 | 用户增删改、重置密码、角色、按用户×组织的功能开关 |
| 账号 | Account 弹窗 / `/m/profile` | 通知邮箱、组织切换、登出 |

### 2.2 角色与数据隔离

| 概念 | 取值 | 影响 |
|---|---|---|
| 平台超管 | `admin` | 跨组织管理全部用户，可授予 `admin` 角色 |
| 组织内角色 | `owner` / `admin` / `member` | 前两者对外显示为 `org_admin`，可管理本组织成员 |
| 功能开关 | `file` / `share` / `todo` / `admin` | 按「用户 × 组织」维度单独配置，控制菜单入口显隐 |
| 用户文件根 | `{static_dir}/{orgName}/{userName}/` | 每个账号只能访问自己的目录；路径越界与软链接逃逸被服务端拒绝 |
| 数据库分区 | `org_id + user_id` | 任务、AI 会话、RAG 向量按归属隔离（向量表用 `owner` 分区键） |

> 用户管理入口由角色控制，不叠加 `admin` 功能开关——否则关闭该开关后无法再开启。

## 3. 快速开始

### 3.1 前置条件

- Go 1.25+、Node 18+、pnpm；构建需 `CGO_ENABLED=1`（SQLite 驱动 mattn/go-sqlite3 + sqlite-vec）。
- `Makefile` 已统一携带构建标签 `sqlite_fts5`（FTS5 混合检索必需）。**直接 `go run` 不带该标签会报 `no such module: fts5`**，清理 RAG 索引请用 `make ragclean`。

### 3.2 构建与启动

| 命令 | 行为 |
|---|---|
| `make all` / `make build` | 构建前端（`pnpm build` → `frontend/dist`）后编译 Go 二进制；`build` 依赖 `frontend`，**每次都会重建前端** |
| `make run` | 构建并以 `ALLOW_SYMLINK=true` 启动 |
| `make preview ARGS="--config /path/to/config.yaml"` | 构建并以指定配置启动 |
| `cd frontend && pnpm dev` | Vite 开发服务器（4000 端口），`/api` 代理到 `VITE_API_TARGET`（默认 `http://localhost:80`） |
| `make check` | `vet` + `lint` + `test` |

服务监听端口由 `server.port` 决定，**代码默认值 8080**，仓库 `config.yaml` 示例为 `8001`；环境变量 `PORT` 可覆盖。启动后访问 `http://<host>:<port>/`。

### 3.3 首次初始化（创建第一个超管）

`users` 表为空时，**第一个成功调用 `/api/set-password` 的人自动成为平台超管**。部署后应先设置一次性引导码，否则任何能访问服务的人都可抢占超管：

```bash
export ADMIN_SETUP_TOKEN=$(openssl rand -hex 16)   # 记住该值
make run

curl -s -X POST http://localhost:8001/api/set-password \
  -H "Content-Type: application/json" \
  -d '{"orgId":"cm","userId":"yiiewang","newPassword":"your-password","setupToken":"<上一步的值>"}'
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `orgId` | 是 | 组织标识，不存在时自动创建（`EnsureOrg`） |
| `userId` | 是 | 用户名，全局唯一 |
| `newPassword` | 是 | 密码，服务端用 bcrypt 存储 |
| `setupToken` | 仅首次 | 仅当系统无任何用户时校验，与 `ADMIN_SETUP_TOKEN` 常量时间比较；有用户后该字段无效 |
| `oldPassword` | 改密时 | 已设置密码的账号改密必须提供 |

**限制**：当前前端**没有「设置密码」页面**（登录页注明不支持自助注册）。首用户创建必须走上述 HTTP 调用；后续账号由管理员在 `/admin` 创建，重置密码也在该页面完成。已有账号但密码为空时，可直接用 `newPassword` 激活，无需 `oldPassword`。

### 3.4 登录与组织切换

浏览器打开 `/login`，输入 User ID + Password 即可（用户名全局唯一，`orgId` 可不填）。成功后后端返回 Bearer token 与用户摘要，前端调用 `/api/userinfo` 拉取全部组织、角色与功能开关，后续每个请求自动注入 `Authorization: Bearer <token>` 与 `X-Org-Id: <id>`。Account 弹窗（移动端「我的」页）可切换组织、修改通知邮箱、登出。

### 3.5 公网部署要点

- 后端只绑定回环地址，由 nginx 终结 HTTPS 后反向代理；不要直接暴露明文后端。
- `client_max_body_size 512m`（与上传上限一致）、`proxy_buffering off`（SSE 流式对话必需）、`proxy_read_timeout 3600s`。
- **所有 SPA 路径必须在 `config.yaml` 的 `routes` 中注册为 `__spa__`**（`/login`、`/file`、`/todo`、`/jobs`、`/shares`、`/admin`、`/m*` 等）。未注册时浏览器刷新该路径会被静态兜底当作资源请求而返回 401。
- 密钥一律用环境变量注入，不写进 `config.yaml`：`AI_API_KEY`、`AI_EMBEDDING_API_KEY`、`EMAIL_PASS`、`ADMIN_SETUP_TOKEN`、`EXA_API_KEY`（MCP header 用 `${ENV_VAR}` 展开）。误入 git 历史的密钥应视为已泄漏，先到提供方吊销。
- 配置 `site_url`（或 `WORKBENCH_SITE_URL`）为对外 https 地址，邮件才会带可点击的跳转按钮；未配置或非法时不渲染任何链接。

## 4. 通用约定

### 4.1 请求与响应

| 项 | 约定 |
|---|---|
| 认证 | `Authorization: Bearer <token>`；组织上下文 `X-Org-Id: <正整数>`（缺失时后端按默认组织解析） |
| 响应信封 | `{ "code": 0, "msg": "ok", "data": ... }`；`code != 0` 视为失败 |
| 编码规则 | `HTTP 状态码 × 100 + 序号`，如 `40901` = 任务乐观锁冲突 |
| 路径参数 | 以 `/` 开头，相对**当前用户根目录**；`..`、URL 编码穿越均被拒绝 |
| 日期时间 | 任务 `YYYY-MM-DD`；精确时刻 `HH:mm`（`00:00` 视为「未指定」）；分享时间 RFC3339 |
| CORS | `/api/*` 返回 `Access-Control-Allow-Origin: null`，配合无 `allow-same-origin` 的沙箱 iframe；不使用 Cookie |

### 4.2 常见业务码

| 码 | 含义 | 典型处理 |
|---|---|---|
| `40102` / `40103` | 缺少 / 无效或过期 token | 重新登录 |
| `40301` | 用户尚未设置密码 | 走 3.3 初始化或联系管理员 |
| `40303` / `40307` | 无权操作 / 需要管理员权限 | 检查角色与 `X-Org-Id` |
| `40304` / `40305` | 分享需要密码 / 密码错误 | 弹出密码框（最多重试 5 次） |
| `40306` / `41001` / `41002` | 分享未生效 / 已过期 / 次数用尽 | 联系分享者 |
| `40901` | `baseRevision` 落后，返回服务端任务副本 | Todo 冲突对比卡或自动重拉 |
| `40902` | 全量 RAG 重建已在进行中 | 查看 `/api/ai/reindex/status` |
| `42901` | 请求过于频繁（登录/分享密码限流） | 稍后重试 |
| `50301` | AI 服务未配置 | 检查 `ai.chat.api_key` / `ai.embedding` |

## 5. 文件浏览与预览

**场景**：查看自己的笔记、代码、配置、报告、音视频；Markdown 内链与标题导航；从 AI 引用跳转回原文行。

**流程**：打开 `/file` → 侧栏目录树懒加载展开 → 点击文件开新 tab → 按扩展名自动选择预览组件 → 可下载、分享、AI 摘要（仅 Markdown）、查看/新建「关联任务」。

| 输入 | 输出 |
|---|---|
| `GET /api/tree?path=<目录>` | 目录树数据（受 `server.hidden` 规则过滤，软链接带 `isLink` 标识） |
| `GET /api/file?path=<文件>` | 原始字节流（需 Bearer） |
| `GET /api/media/ticket?path=<媒体>` | `{url, size}`；`url` 内含 2 小时有效的 HMAC ticket |
| `GET /api/media?...&ticket=...` | `http.ServeContent` Range 流式播放（无需 Bearer，ticket 自带凭证） |
| `GET /api/tasks/by-file?path=...` | 关联了该文件的任务列表 |

**预览能力**：Markdown（DOMPurify 净化、Mermaid、表格滚动、标题折叠、悬浮目录、`==高亮==`）、代码（highlight.js、行号、`#region` 折叠、超大文件降级纯文本）、JSON/XML 树形查看、HTML（`srcdoc` 沙箱 iframe + 锚点拦截）、图片、音视频、二进制卡片（下载）。

**限制**：

- 文件根为 `{static_dir}/{orgName}/{userName}/`，`allow_symlink` 默认关闭，指向根外的软链接一律 404；开启后仍需通过真实路径校验。
- `server.hidden` 默认隐藏点开头文件与常见压缩包（`.*`、`*.zip`、`*.tar`、`*.gz` 等）；命中的文件仍可上传，但**不在目录树中显示**，上传响应会单独列出 `hiddenNames`。
- HTML 预览运行在不含 `allow-same-origin` 的沙箱 iframe 中，页面脚本无法访问父页面 DOM 与 localStorage 中的 token。
- `/api/ai/chat` 与 `/api/media` 显式跳过响应压缩（前者保证 SSE 逐行透传，后者保证 Range 语义）。

## 6. 文件管理

**场景**：上传资料、整理目录、重命名/移动/删除；AI「把回复保存为文件」也走同一上传接口。

| 操作 | 入口 | API | 关键参数 |
|---|---|---|---|
| 上传 | 侧栏 Upload / 文件选择器 | `POST /api/upload`（multipart） | `path`=目标目录（默认 `/`）、`files`=多文件 |
| 新建目录 | 右键 → New folder | `POST /api/mkdir` | `{path}`=完整目标路径（单层） |
| 重命名 | 右键 → Rename | `POST /api/rename` | `{path, newName}`（同目录改名） |
| 移动 | 右键 Move → 目标目录右键 Move here | `POST /api/move` | `{path, destDir}`（保持原名） |
| 删除 | 右键 → Delete | `DELETE /api/file?path=...` | 目录递归删除，不可恢复 |

上传响应：`{uploaded:[{name,size}], skipped:[{name,reason}], hiddenNames:[...]}`。**同名文件跳过不覆盖**（`reason: already exists`），文件名经清理，非法名与写入失败也进 `skipped`。

**限制**：单请求上限 512 MB / 100 个文件；目标必须为用户根内已存在的目录；禁止删除用户根目录；移动带环检测（不能把目录移进自身）；对软链接条目只操作链接本身，不触及真实目标；所有写操作均记录审计日志（user/org/path）。

## 7. 分享

**场景**：把文件或整个目录以临时链接发给他人（对方无需账号），可加密码、访问次数与生效/过期时间。

**创建流程**：在文件 tab 点 Share（或目录右键 Share）→ 填写表单 → 创建后自动把「链接 + 密码 + 备注」复制到剪贴板 → 在 `/shares` 查看、复制、撤销。

| 参数 | 说明 |
|---|---|
| `resourcePath` | 用户根内路径，服务端强制以 `/` 开头并拒绝 `..` |
| `resourceType` | 表单值 `file`/`dir`；**服务端按实际 `os.Stat` 结果覆盖**，不受传入值影响 |
| `maxAccessCount` | `0` = 无限 |
| `password` | 留空 = 无密码；服务端存 bcrypt 哈希，明文只缓存在创建者浏览器便于复制链接 |
| `remark` | 备注，随链接文本一起复制 |
| `effectiveAt` / `expiresAt` | RFC3339 字符串，留空 = 不限 |

**访问流程**：打开 `/s/{token}`（移动端自动走 `/m/s/{token}`）→ 后端返回 SPA 入口 → 前端以分享模式装载目录列表或文件预览 → 需要密码时弹窗重试（最多 5 次）→ 文件/目录分享共用同一套 `GET|POST /api/share/{token}` 数据接口（带密码时用 POST）。

**限制**：分享密码错误每 5 分钟最多 5 次，超限返回 `42901`；超出次数/时间窗分别返回 `41001`/`41002`/`40306`；分享模式下上传、右键菜单、AI 面板、Share/AI 摘要按钮均隐藏；移动端只提供访问，不提供创建与管理。

## 8. Todo 任务

**场景**：日常任务管理（四象限、看板拖拽、日历与甘特）、按日期与精确时刻提醒、任务与文件双向关联、多设备同步。

### 8.1 任务字段

| 字段 | 类型 / 取值 | 说明 |
|---|---|---|
| `id` | string (UUID) | 客户端生成 |
| `title` / `content` | string | 正文为 Markdown；列表响应不含 `content`，只有 80 字 `contentPreview` |
| `status` | `todo` / `progress` / `done` | 看板分组依据（待办/进行中/已完成）；进度 100 与 `done` 联动 |
| `priority` | `high` / `medium` / `low` | 默认 `medium` |
| `scheduled` / `due` | `YYYY-MM-DD` | 计划起止日期 |
| `scheduledTime` / `dueTime` | `HH:mm` | 精确提醒时刻；`00:00` 归一为空（避免凌晨提醒风暴） |
| `progress` / `assignee` / `postponedCount` / `autoPostponed` / `sortOrder` | — | 进度、负责人、延期次数等 |
| `files` | string[] | 关联的用户根内路径，上限 20 条 |

### 8.2 操作流程与接口

| 动作 | API | 输入 | 输出 |
|---|---|---|---|
| 拉全量 | `GET /api/tasks` | — | `{revision, tasks}`（仅本人任务） |
| 拉详情 | `GET /api/tasks/{id}` | — | `{task（含 content 全文）, revision}` |
| 轻量版本 | `GET /api/tasks/revision` | — | `{revision}`，30s 轮询对账用 |
| 新增 / 修改 / 删除 | `POST` / `PATCH` / `DELETE /api/tasks/{id}` | `{task, baseRevision}`（DELETE 走 query） | 新 `{revision}`；落后返回 `40901` + 服务端任务副本 |
| 成员日程 | `GET /api/tasks/user/{id}` | — | 同组织 active 成员的任务（只读） |
| 文件反查 | `GET /api/tasks/by-file?path=` | — | 关联该文件的任务 |
| 未完成任务 | `GET /api/tasks/open` | — | `status != done`，按截止日升序 |

**多设备同步**：所有写入携带 `baseRevision`；前端每 30 秒及页面切回前台时比对 `/api/tasks/revision`，不一致自动重拉（编辑弹窗打开、冲突待裁决、离线、查看他人时跳过）。冲突时若差异字段与服务端一致则静默采用服务端，否则弹出**单任务字段级对比卡**逐项选「我的/服务器」；新增/删除冲突直接重拉。编辑弹窗先拉详情成功才打开，避免列表瘦身后保存清空正文。支持 JSON 导入（同 id 跳过）与导出、`Ctrl/Cmd+S` 保存、`/todo?task=<id>` 直接定位。

**提醒**：`schedule.enabled=true` 时，到期扫描（默认每 5 分钟）对 `due <= 今天且未完成` 发 `task.due`；精确时刻扫描（每分钟）对 `scheduledTime`/`dueTime` 发 `task.reminder`（`due` 提醒过滤已完成任务）。两类均按「任务 + 日期/时刻」去重，收件人为任务归属用户的通知邮箱。

**限制**：`PATCH` 请求体中**未出现的字段保留服务端现值**（快捷勾选/拖拽不会清空正文），显式传空串才是主动清空；任务 ID 由客户端生成，旧版 `PUT /api/tasks` 全量同步已废弃；移动端仅提供分组列表、状态轻编辑与详情，不提供三视图、冲突卡、轮询与导入导出。

## 9. AI 对话

**场景**：基于 OpenAI 兼容网关的流式问答；推理过程展示；引用个人文件提问；让 Agent 读写文件、管理任务与定时任务；基于 RAG 自动检索自己的文件并给出可跳转的引用。

### 9.1 启用条件

| 能力 | 开关 | 生效条件 |
|---|---|---|
| 基础对话 | `ai.chat` | `api_key` 与 `base_url` 均非空（推荐 `AI_API_KEY` 环境变量注入） |
| Agent 工具 | `ai.chat.tools: true` / `AI_TOOLS=true` | 上游模型需支持 function calling；默认关闭（纯对话） |
| RAG 语义检索 | `ai.embedding` | `base_url` 与 `model` 非空；本地 vLLM/Ollama 可留空 `api_key` |
| 相关性预判 / 摘要卡 | `ai.embedding.judge_model` / `digest_model` | 可选，复用 embedding 网关的 `/chat/completions` |
| 外部 MCP 工具 | `ai.mcp.servers` | 仅 HTTP 型无状态服务；headers 值支持 `${ENV_VAR}` |

`GET /api/ai/status` 返回 `{enabled, model, tools}`，前端据此决定是否显示入口。

### 9.2 输入输出

| 项 | 说明 |
|---|---|
| 请求 | `POST /api/ai/chat`，`{messages, qaId?, sessionId?}`；`messages` 为 OpenAI chat 格式，最多 30 条，请求体 ≤512 KB；`qaId`/`sessionId` 为 ≤64 的可打印标识，透传为上游 `X-QA-Id`/`X-Session-Id` 便于链路排障 |
| 响应 | `text/event-stream`；默认事件为 OpenAI 兼容增量（`delta.content` / `delta.reasoning_content`），结束 `data: [DONE]` |
| 自定义事件 | `rag_hits`（检索命中）、`usage`（多轮累计 token）、`tool_call` / `tool_result`、`approval_request` / `approval_resolved`、`error` |

### 9.3 内置工具（13 个，`ai.tools=true` 时）

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

写类工具执行前，SSE 流发出 `approval_request` 并暂停，用户在工具卡片点「批准/拒绝」，决定经 `POST /api/ai/approval {id, approved}` 提交。**5 分钟未确认按拒绝处理**，关闭面板或停止生成会作废未决审批；拒绝/超时以说明性结果回传模型，由模型解释原因后继续对话。所有外部 MCP 工具（`mcp_<server>_<tool>`）一律需要审批。工具结果统一截断到 32 KB，Agent 最多 5 轮工具调用，达到上限时强制模型用已有结果作答。

### 9.4 RAG 文件检索

- **索引**：上传、AI `write_file`、删除/重命名/移动均自动维护索引；文本类扩展名白名单（md/txt/json/yaml/go/py/html… 共 24 类），≤2 MiB，隐藏文件、隐藏目录与 `node_modules`/`__pycache__`/`vendor`/`target` 整棵跳过。整文件 SHA-256 判重，内容未变零成本跳过。
- **切块**：按空行分段，目标 800 字 + 相邻块 100 字重叠，记录 1-based 起始行；可选生成文件级摘要卡（`chunk_index=-1`）与原文块一起参与检索。
- **检索**：向量 KNN + FTS5 trigram 关键词双路召回，经 RRF（`score = Σ 1/(60+rank)`）融合取 top-5；向量路受 `max_distance`（默认 0.9）过滤，关键词路不受；可选 `judge_model` 批量预判相关性。短问题（<30 字）会拼上一条 user 消息补语境。
- **展示**：命中片段经 `rag_hits` 推送，注入 prompt 时编号 `[1]…[n]`；回答底部「引用来源」只列出正文实际引用到的编号，点击可跳转到文件并定位行（PC hash `#L12`，移动 `?line=12`）。
- **运维**：`POST /api/ai/reindex` 全量重建（同用户并发触发返回 `40902`）；`GET /api/ai/reindex/status` 查进度；`GET /api/ai/index-status` 查每文件索引状态与错误；索引清理用 `make ragclean [--reset]`。

### 9.5 会话与消息操作

会话历史存于 SQLite `ai_sessions`，前端流式期间 3 秒节流保存快照（含工具调用、引用、token 用量），刷新后自动接上最近会话（同一 tab 内主动「新对话」且尚未提问时除外）。列表最多 50 条、只取摘要不取正文；长会话按「轮」截断（展开后总条数 ≤28），默认只渲染最近 5 条消息。消息支持复制、重新生成、保存为文件（写入 `/ai/YYYY-MM-DD-主题.md`）。PC 面板另有宽度拖拽、剧场模式、消息目录刻度、索引状态抽屉。

**限制**：`@` 引用仅接受文本类文件，单文件截断 20k 字；`read_file` 本体 ≤4 MB、内容 ≤64 KB，前 512 字节含 NUL 视为二进制；上游单次流式超时 10 分钟；检索超时 8 秒并静默降级（对话可用性优先）；**修改 `ai.embedding.dim` 会作废全部向量**，必须执行 `POST /api/ai/reindex` 重建；RAG 未启用时 `search_files` 返回明确错误；上游不支持 `stream_options.include_usage` 时不显示 token 消耗。

## 10. AI 定时任务与通知

**场景**：每天 18:00 让 AI 汇总当天任务并发日报邮件；每周一 09:00 生成周报；任务到期/计划开始时刻收到提醒邮件。

| 参数 | 说明 |
|---|---|
| `name` / `prompt` | 任务名与要执行的指令；结果按 Markdown 输出，作为 HTML 邮件正文 |
| `cronExpr` | 标准 5 段 cron（分 时 日 月 周），支持 `*`、`*/n`、`a-b`、逗号列表、英文别名（如 `wed`/`nov`）；创建时校验，非法即失败 |
| `enabled` | 启用开关；增删改下一分钟生效，无需重启 |

前端提供 5 个预设：`0 18 * * *`、`0 9 * * *`、`0 9 * * 1`、`0 18 * * 5`、`0 * * * *`。接口：`GET|POST /api/ai/jobs`、`PUT|DELETE /api/ai/jobs/{id}`、`GET /api/ai/jobs/{id}/runs?limit=`。

**全局公共任务**：`ai_jobs.user_id IS NULL` 的记录（预置「每日工作总结」，`0 18 * * *`）所有用户可见但只读，执行时对所有**已配置邮箱且组织关系 active** 的用户逐个以本人身份展开（各自任务数据、各自投递），避免按用户复制记录。执行历史对全局任务所有用户可查，私有任务仅归属人可查；`ai_job_runs.output` 只保留输出前 4 KB。

**通知链路**：`schedule.enabled` 开启后，进程内 cron 引擎每分钟扫描；命中任务经事件总线 `user.notify`（成功/失败）或 `task.due` / `task.reminder` 分发，邮件订阅者用 SMTP 587 STARTTLS 投递给任务归属用户的邮箱。收件邮箱在 `/admin` 创建用户时填写，或移动端「我的」页自助修改（空串 = 不再接收）。`EMAIL_PASS` 环境变量提供 SMTP 授权码。

**限制**：`notify.email.enabled` 必须 `schedule.enabled=true`，且 `host`/`user` 必填（缺失启动即失败）；未配置 `site_url` 时邮件不含跳转按钮；去重发生在发布事件之前，`task.due`/`task.reminder` 当天只投一次，**SMTP 失败不会重投**；`user.notify` 不去重；LLM 调用超时 5 分钟，prompt 最多附带 50 条任务；执行记录只存输出前 4 KB。

## 11. 用户与组织管理（`/admin`）

**场景**：管理员创建账号、重置密码、调整角色、按成员开关功能模块、查看任务完成率与分享数。

| 操作 | API | 参数 |
|---|---|---|
| 用户列表 / 角色列表 | `GET /api/admin/users[?orgId=]` / `GET /api/admin/roles` | 超管可跨组织 |
| 创建 | `POST /api/admin/users` | `{org, name, password, roleId, mobile?, email?}` |
| 编辑 / 重置密码 | `PATCH /api/admin/users/{id}` | `{name?, mobile?, email?, password?, roleId?}`（只传要改的字段） |
| 删除 | `DELETE /api/admin/users/{id}` | 不能删自己或最后一个 admin |
| 看板统计 | `GET /api/admin/users/{id}/dashboard` | `{totalTasks, doneTasks, shareCount}` |
| 功能开关 | `GET /api/admin/users/{id}/features`、`PATCH .../{code}` | `{enabled}`；`code` ∈ `file/share/todo/admin` |

**限制**：仅 PC 提供；入口对 `admin` 与 `org_admin` 可见（`requiresAdmin` 守卫），组织管理员不能授予 `admin` 角色、不能跨组织操作，新建用户的组织锁定为自身组织；给自己改功能开关会即时刷新菜单；用户名全局唯一，登录时 `orgId` 可省略。

## 12. 移动端

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
| AI 面板剧场模式 / 刻度 / 索引抽屉 / 宽度拖拽 | ✅ | — |
| AI 定时任务 | ✅ `/jobs` | ✅ 首页入口指向 `/jobs`（该路径窄视口不重定向） |
| 通知邮箱编辑 | — | ✅「我的」页 |
| 用户管理 | ✅ | — |

> 复杂管理流程刻意留在 PC。`/jobs` 不在 PC→移动映射表中，因此在窄视口下仍以响应式 PC 布局呈现。

## 13. 配置参考（使用者视角）

配置加载顺序：代码默认值 → `config.yaml`「非零值」覆盖 → 环境变量覆盖。仓库 `config.yaml` 是**部署示例**，与代码默认值不同。

| 配置段 | 关键项 | 代码默认 | 示例值 |
|---|---|---|---|
| `site_url` | 邮件跳转链接的对外根地址 | `""`（不渲染链接） | `https://…` |
| `server` | `port` / `static_dir` / `allow_symlink` / `hidden` | `8080` / `./static` / `false` / `.*,*.zip,*.tar,*.gz,*.tgz,*.rar,*.7z,*.bz2,*.xz` | `8001` / `./preview` / `false` / 使用默认 |
| `database.path` | SQLite 文件 | `./data/workbench.db` | 同 |
| `auth` | `token_expiry_days` / `admin_setup_token` | `30` / `""` | `7` / 环境变量注入 |
| `routes` | 路径 → `__spa__` / `__listdir__` / 别名 / 302 目标 | 仅 3 个历史键 | 全部 SPA 入口 |
| `logging` | `dir` / `level` | `~/.local/state/workbench` / `info` | `logs` / `info` |
| `ai.chat` | `base_url` / `api_key` / `model` / `tools` | OpenAI 默认端点 / `""` / `gpt-4o-mini` / `false` | 内部网关 / 环境变量 / `hy4` / `true` |
| `ai.embedding` | `base_url` / `api_key` / `model` / `dim` / `max_distance` / `judge_model` / `digest_model` | `""`/`""`/`""`/`1024`/`0.9`/`""`/`""` | 模型网关 / `Qwen3-Embedding-8B` / `1024` / `0.9` / `deepseek-flash` ×2 |
| `ai.mcp.servers` | `name` / `url` / `headers` | 无（功能关闭） | 一个 `exa-websearch` 服务 |
| `schedule` | `enabled` / `timezone` / `todo_due_cron` | `false` / `Asia/Shanghai` / `*/5 * * * *` | `true` / `Asia/Shanghai` / 同 |
| `notify.email` | `enabled` / `host` / `port` / `user` / `from` / `from_name` | `false` / `smtp.qq.com` / `587` / `""` / 回退 `user` / 运行回退 `Workbench` | `true` / 同 / `587` / 个人邮箱 / — / `Workbench Robot` |

| 环境变量 | 覆盖项 |
|---|---|
| `PORT` / `STATIC_DIR` / `DB_PATH` / `LOG_DIR` | 端口 / 用户文件目录 / 数据库路径 / 日志目录 |
| `ALLOW_SYMLINK` | `server.allow_symlink`（`true` 或 `1`） |
| `ADMIN_SETUP_TOKEN` | `auth.admin_setup_token` |
| `AI_BASE_URL` / `AI_API_KEY` / `AI_MODEL` / `AI_TOOLS` | `ai.chat.*` |
| `AI_EMBEDDING_BASE_URL` / `AI_EMBEDDING_API_KEY` / `AI_EMBEDDING_MODEL` / `AI_EMBEDDING_DIM` / `AI_EMBEDDING_MAX_DISTANCE` / `AI_EMBEDDING_JUDGE_MODEL` / `AI_EMBEDDING_DIGEST_MODEL` | `ai.embedding.*` |
| `WORKBENCH_SITE_URL` | `site_url` |
| `EMAIL_PASS` | SMTP 授权码（不经过 config 结构） |
| `EXA_API_KEY` 等 | MCP `headers` 中的 `${ENV_VAR}` 占位符 |

**已知行为（配置时需注意）**：

- YAML 合并是「非零值覆盖」：**`false` 无法覆盖默认 `true`**；`hidden`、`routes`、`ai.mcp.servers` 等复合字段为整体替换。
- `server.allow_symlink` 的 YAML 项当前**不参与合并**，只能通过环境变量 `ALLOW_SYMLINK` 开启。
- `schedule.*`、`notify.*`、`ai.mcp.servers`、`logging.level`、`auth.token_expiry_days` **没有环境变量覆盖**，只能改 `config.yaml`。
- `ai.chat.api_key`、`ai.embedding.api_key` 等凭据请勿写入配置文件，用环境变量注入；本地向量服务 `api_key` 可留空。
- 修改 `ai.embedding.dim` 后必须全量 `reindex`；`ragclean --reset` 用于释放 vec0/FTS5 惰性删除残留的空间。

## 14. 限制条件汇总

| 类别 | 限制 |
|---|---|
| 上传 | 512 MB / 100 文件每请求；同名跳过；目标目录须已存在 |
| 认证 | token 默认 30 天（示例配置 7 天）；**不能单独吊销**，轮换 `app_secrets` 的 token 密钥会使全部 token 失效；登录失败 5 次/分钟/IP |
| 分享 | 密码错误 5 次/5 分钟；密码/次数/时间窗均在服务端校验 |
| AI 对话 | 请求体 512 KB、消息 ≤30 条、上游超时 10 分钟、Agent ≤5 轮工具、工具结果 32 KB、`write_file` 256 KB、`read_file` 4 MB（内容 64 KB）、审批 5 分钟 |
| AI 会话 | 快照 JSON ≤256 KB，列表最多 50 条，回传历史按轮预算展开 ≤28 条 |
| RAG | 单文件 ≤2 MiB + 24 类白名单、query ≤2000 字、检索 8 秒超时、对话 top-5、索引 5 分钟超时；短问题自动补语境 |
| 定时任务 | 每分钟扫描、LLM 超时 5 分钟、prompt 最多 50 条任务、执行记录输出前 4 KB |
| 通知 | 587 STARTTLS 强制；`EMAIL_PASS` 必填；收件人为任务归属用户邮箱；去重在投递前，失败不重投 |
| 媒体 | ticket 有效期 2 小时，绑定用户与路径；分享模式降级为整文件 blob |
| MCP | 仅 stateless HTTP 服务（JSON-RPC 2.0），不支持 stdio 子进程、有状态会话、服务端推送与重连；服务不可达时启动仅告警并跳过 |
| 存储 | 单 SQLite 文件、WAL、单连接 `SetMaxOpenConns(1)`；未实现进程级 SIGTERM 优雅关闭（仅有 `defer db.Close()`） |
| 移动端 | ≤900px 断点；复杂管理、文件写操作、分享管理、用户管理不提供 |

## 15. 常见问题

| 现象 | 原因与处理 |
|---|---|
| 登录报 `Password not set` | 账号尚未激活。首用户按 §3.3 调 `/api/set-password`；其余账号联系管理员在 `/admin` 创建/重置 |
| 刷新 `/file`、`/admin`、`/jobs` 返回 401 | `config.yaml` 的 `routes` 未把该路径注册为 `__spa__`，补上并重启 |
| AI 入口不显示 | `GET /api/ai/status` 的 `enabled=false`；检查 `ai.chat.api_key`（`AI_API_KEY`）与 `base_url` 是否都非空 |
| 上传成功但文件树看不到 | 命中 `server.hidden` 规则（点开头文件、压缩包）；查看上传响应的 `hiddenNames`，或调整 `hidden` |
| 改了 `ai.embedding.dim` 后检索无结果 | 维度变更会 DROP 向量表并作废索引状态；执行 `POST /api/ai/reindex`，用 `/api/ai/reindex/status` 看进度 |
| AI 回答引用了不相关文件 | 调小 `ai.embedding.max_distance`；配置 `judge_model` 做相关性预判；短问题可用更完整的表述 |
| 收不到提醒邮件 | 依次检查：`schedule.enabled`、`notify.email.enabled` + `host`/`user`、`EMAIL_PASS`、用户邮箱是否填写、任务状态是否 `done`、当天是否已去重、`site_url` 是否合法 |
| 视频无法拖动进度条 | `/api/media` 已显式跳过压缩并支持 Range；若走反代，确认 `proxy_buffering off` 且未对响应做二次压缩 |
| 多设备编辑任务提示冲突 | `baseRevision` 落后（40901）。在字段级对比卡选择保留本地或服务端值；无差异会自动静默合并 |
| token 疑似泄漏 | token 无法单独吊销：轮换 `app_secrets` 中 `token` 密钥（或删除后由 `LoadOrCreateSecret` 重新生成）并重启，所有用户需重新登录 |
| 任务到期邮件里的链接点不开 | `site_url` 未配置或不是 http(s) 地址时邮件不渲染链接；配置后重启 |
| MCP 工具列表为空 | 只支持 HTTP 型无状态 MCP；检查 `name`/`url`（缺失启动即失败）、`${ENV_VAR}` 是否已导出、服务是否可达 |
| 移动端找不到上传/分享管理/用户管理 | 设计如此，复杂操作请在 PC 端完成 |
| 公网访问 API 很慢 | 已启用 gzip/deflate/br 协商压缩；确认反代未关闭压缩、客户端发送了 `Accept-Encoding` |
| `go run scripts/ragclean.go` 报 `no such module: fts5` | 必须经 `make ragclean` 入口（统一携带 `sqlite_fts5` build tag） |
| 数据库在哪、如何备份 | `database.path`（默认 `./data/workbench.db`）；WAL 模式下备份需同时处理 `-wal`/`-shm` 或使用 SQLite 备份 API |

## 16. 术语表

| 术语 | 含义 |
|---|---|
| 用户根 | `{static_dir}/{orgName}/{userName}/`，每个账号的私有文件目录，所有路径参数相对它 |
| revision | 服务端权威任务版本号；所有任务写入 +1，客户端用 `baseRevision` 做乐观锁 |
| 分享 token | `/s/{token}` 中的随机串，是公开访问的唯一凭证（配合密码/时间/次数约束） |
| media ticket | `<video>/<audio>` 无法带 Bearer 头时换取的 2 小时 HMAC 短时效 URL |
| RAG / 混合检索 | 向量 KNN（语义）+ FTS5 trigram（关键词）双路召回，经 RRF 融合 |
| Agent | 服务端循环调用工具并把结果回传模型的多轮对话模式（`ai.chat.tools`） |
| HITL 审批 | 写类工具执行前需用户在独立 HTTP 请求中确认（Human-in-the-loop） |
| 全局公共任务 | `ai_jobs.user_id IS NULL` 的只读任务，执行时对所有有邮箱的用户逐个展开 |
| `__spa__` | `routes` 配置值，表示把该路径直接返回内嵌的 SPA 入口 `index.html` |
