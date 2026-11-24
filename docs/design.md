# Workbench 软件设计文档

> 文档版本：v1.0 ｜ 面向：开发者、维护者 ｜ 事实来源：当前代码与 `internal/db/migrations/`
> 使用者视角的功能、参数与 FAQ 见 [`functional-guide.md`](functional-guide.md)；文档维护规范与已知偏差见 [`README.md`](README.md)。

## 1. 关于本文档

| 项 | 说明 |
|---|---|
| 覆盖范围 | 系统架构、模块划分、关键流程、数据模型、接口设计、依赖关系、扩展性与设计取舍 |
| 阅读路径 | 先 §2 架构 → §4 启动装配 → §6 数据模型 / §7 接口 → §5 流程 → §9 扩展点 |
| 引用约定 | 用「文件路径 + 函数/类型/常量名」定位代码，**不写行号**（行号随提交漂移） |
| 事实优先级 | 代码 > 本文件 > 根 `README.md` / `docs/` 历史设计稿 |

`docs/rbac-design.md` 与 `docs/migration-strategy.md` 是 2026-08-21 的历史设计稿，其中若干结论（如 `CGO_ENABLED=0` + modernc 驱动、`org_features` 表）已被后续实现取代，**不作为现行设计依据**。

## 2. 架构总览

### 2.1 分层与交付形态

```
┌───────────────────────────────────────────────────────────────────┐
│ 浏览器                                                            │
│   Vue3 SPA（PC DefaultLayout / 移动 /m/*）                        │
│   lib/common.ts：authHeaders / X-Org-Id / apiCall / 401 拦截      │
└───────────────────────────┬───────────────────────────────────────┘
                            │ 同源 /api/*（HTTPS 由 nginx 终结）
┌───────────────────────────▼───────────────────────────────────────┐
│ 单 Go 二进制（cmd/workbench，iris v12）                           │
│  全局中间件：压缩 → 访问日志 → 安全头/CORS                        │
│  路由级：AuthMiddleware（Bearer + X-Org-Id）→ RequireUserManager │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │ internal/server   HTTP handler / 沙箱 / 分享 / AI 代理与工具 │ │
│  └───────┬──────────────┬──────────────┬──────────────┬────────┘ │
│          │              │              │              │          │
│  ┌───────▼─────┐ ┌──────▼──────┐ ┌─────▼──────┐ ┌────▼───────┐ │
│  │ internal/db │ │ internal/rag│ │  schedule  │ │ mcpclient  │ │
│  │  SQLite/WAL │ │ 索引+检索   │ │ cron+EventBus│ │ JSON-RPC   │ │
│  └───────┬─────┘ └──────┬──────┘ └─────┬──────┘ └────────────┘ │
│          │              │              │ notify（邮件订阅者）    │
│  ┌───────▼──────────────▼──────────────▼──────────────────────┐ │
│  │ SQLite 单文件：业务表 + vec0 向量表 + FTS5 虚拟表            │ │
│  │ sqlitevec（静态扩展注册）/ sqlite_fts5 build tag             │ │
│  └──────────────────────────────────────────────────────────────┘ │
│  ┌──────────────────────────────────────────────────────────────┐ │
│  │ embed.go：//go:embed frontend/dist（UI 静态资源）             │ │
│  └──────────────────────────────────────────────────────────────┘ │
└───────────────────────────┬───────────────────────────────────────┘
                            │ 外部依赖（可选）
        OpenAI 兼容 chat 网关 · /embeddings 向量服务 · SMTP 587 · HTTP MCP
```

**两条存储通道刻意解耦**：UI 资源来自编译期 embed 的 `frontend/dist`（运行时只读），用户文件来自 `server.static_dir` 磁盘目录（按 `orgName/userName` 分根），二者不共用路径空间。业务数据全部在 SQLite 单文件内。

### 2.2 请求生命周期

`Server.App()` 注册 3 个全局中间件，顺序固定：

| # | 中间件 | 职责 | 跳过路径 |
|---|---|---|---|
| 1 | `compressionMiddleware` | `ctx.CompressWriter(true)`，按 `Accept-Encoding` 协商 gzip/deflate/br | `/api/media`、`/api/ai/chat` |
| 2 | `loggingMiddleware` | `ctx.Record()` + 写 `preview.log`（时间/IP/UA/path/status） | 同上 |
| 3 | `securityHeadersMiddleware` | `nosniff`、`SAMEORIGIN`、`no-referrer`；`/api/*` 加 `Access-Control-Allow-Origin: null` 与允许头 | 对 `/api/*` 的 OPTIONS 直接 204 |

压缩与 `Record` 必须同时跳过 SSE（逐行透传会被压缩缓冲破坏）与媒体（Range 字节区间与压缩语义冲突）。鉴权不是全局中间件：`AuthMiddleware` 逐条作为 handler 参数挂在 `api` 分组路由上，`/api/admin` 再加 `RequireUserManager`。限流同样不是中间件，而是 handler 内查 `rate_limits` 表（登录失败 5 次/分钟/IP、分享密码 5 次/5 分钟；DB 故障 fail-open）。

### 2.3 关键设计原则

- **单二进制、零外部中间件**：HTTP、调度、事件分发、向量检索全在进程内；外部服务都是可选增强，缺失时对应能力降级关闭而非启动失败。
- **服务端权威**：文件路径、权限、版本号、索引状态、cron 合法性一律服务端判定，前端只做展示与交互。
- **身份维度贯穿**：`org_id + user_id` 是存储层隔离键，也是 vec0 的 `owner` 分区键；文件根目录用 `orgName/userName` 拼接，构成「整数 id 存储 / 名称路径」双轨。
- **可用性优先于完整性**：RAG 检索、FTS 建表、embedding、judge、MCP 服务失败均静默降级；对话主链路不被检索与通知拖垮。
- **写操作需显式确认**：AI 写类工具与外部 MCP 工具走 Human-in-the-loop 审批，审批绑定发起用户且一次性。

## 3. 模块划分

### 3.1 后端 `internal/`

| 包 | 职责 | 关键文件与符号 | 主要依赖 |
|---|---|---|---|
| `config` | YAML 结构、默认值、非零值合并、环境变量覆盖、`~/` 展开 | `config.go`：`Config`、`DefaultConfig`、`Load`、`mergeConfig`、`ApplyEnv`、`ResolvePath` | yaml.v3 |
| `db` | SQLite 连接/迁移、用户组织 RBAC、任务、分享、会话、AI job、RAG 表读写、密钥与限流 | `db.go`（`Open`/`TaskItem`）、`migrate.go`（`detectBaseVersion`）、`users.go`、`user_orgs.go`、`tasks.go`、`shares.go`、`ai_sessions.go`、`time_reminders.go`、`rag.go`（`EnsureVecIndex`/`EnsureFTS`）、`secrets.go`、`rate_limits.go` | mattn/go-sqlite3、golang-migrate、sqlitevec |
| `server` | iris 路由与全部 handler、鉴权、沙箱、AI 代理/Agent/审批、响应封装 | `server.go`（`Server`/`New`/`App`/`safeJoin`/`resolveUserPathSafe`）、`auth.go`、`ai.go`、`aitools.go`、`aitools_mcp.go`、`aiapproval.go`、`fsops.go`、`upload.go`、`media.go`、`share.go`、`admin.go`、`org.go`、`codes.go`、`response.go`、`sortutil.go` | iris、db、rag、schedule、notify、mcpclient、workbench.UIFS |
| `rag` | 文件索引与混合检索、LLM 相关性预判与摘要卡 | `service.go`（`Service`/`IndexFile`/`ReindexTree`/`Search`/`ContextPrompt`）、`chunk.go`（`Indexable`/`SplitChunks`）、`embedding.go`（`Embedder`）、`judge.go`、`extract.go` | db、OpenAI 兼容 HTTP |
| `schedule` | 手写 cron 引擎、进程内事件总线、内置与 AI job | `scheduler.go`（`ParseCron`/`Matches`/`Scheduler`）、`bus.go`（`EventBus`/`Publish`/`Subscribe`/`Topics`）、`jobs.go`（`KnownTopics`/`NewTodoDueScanJob`/`NewTodoTimeReminderJob`）、`aijobs.go`（`NewAIJobsRunnerJob`） | db、标准库 |
| `notify` | 订阅事件总线并发邮件 | `email.go`（`SubscribeEmailNotifier`/`EmailNotifier`）、`email_template.go`（`mdToHTML`/`safeHref`）、`email_helper.go` | net/smtp、schedule、db |
| `mcpclient` | MCP Streamable HTTP 最小客户端 | `client.go`（`initialize` → `notifications/initialized` → `tools/list` / `tools/call`） | net/http |
| `sqlitevec` | 进程内注册 sqlite-vec 静态扩展、向量序列化 | `vec.go`（`Auto`/`SerializeFloat32`）+ vendor 的 `sqlite-vec.c`/`sqlite3.h` | cgo、mattn 头文件 |

顶层还有 `embed.go`（`UIFS`）、`cmd/workbench/main.go`（入口）、`cmd/probedb`、`cmd/probeui`、`scripts/ragclean.go`（运维工具，均不参与主服务启动）。

### 3.2 前端 `frontend/src/`

| 目录 | 职责 | 代表文件 |
|---|---|---|
| `api/` | 按域封装 HTTP 与 SSE；`index.ts` 为桶文件 | `auth.ts`、`files.ts`、`share.ts`、`tasks.ts`、`ai.ts`、`aiJobs.ts`、`admin.ts` |
| `lib/` | 无框架依赖的请求/渲染/工具层 | `common.ts`（`authHeaders`/`authFetch`/`apiCall`/`clearAuthState`）、`markdownRenderer.ts`、`highlighter.ts`、`ragNav.ts`、`todoUtils.ts` |
| `stores/` | 跨组件共享的响应式状态与身份作用域清理 | `auth.ts`（认证唯一真相源）、`indexStore.ts`（`resetIdentityScopedState`）、`todoStore.ts`、`adminStore.ts` |
| `composables/` | 组件无关的业务逻辑 | `useAIChat.ts`（PC/移动共用）、`todoSync.ts`（revision 乐观锁）、`useTodoApp.ts` + `todo/{board,calendar,editor}.ts`、`useTreeStore.ts`、`useIndexApp.ts` |
| `components/` | 展示与交互组件，按域分目录 | `ai/`、`editor/`、`markdown/`、`modal/`、`sidebar/`、`tab/`、`todo/`、`ui/ModalShell.vue` |
| `views/` + `views/mobile/` | PC 与移动两套独立视图 | `HomeView`、`IndexView`、`TodoView`、`AdminView`、`AiJobsView`、`LoginView`；`MobileLayout`（移动外壳）、`MHome`、`MFiles`、`MFileView`、`MAI`、`MTodo`、`MProfile`、`MShareView` |
| `router/` | `createWebHistory` 路由表、设备分流、登录/admin 守卫 | `index.ts`（`PC_TO_MOBILE` / `MOBILE_TO_PC` / `isMobileViewport`） |
| `layouts/` | PC 应用外壳与全局组件挂载 | `DefaultLayout.vue` |
| `styles/` | 全局主题、Todo、AI 消息、移动公共样式 | `style.css`（≤900px 断点）、`ai-chat.css`、`todo.css`、`mobile.css` |

**PC 与移动端共享业务层、只拆视图层**：`useAIChat`、`FileViewer`、`ModalShell`、`api/*` 两侧复用；移动端的差异集中在路由与视图。

### 3.3 依赖关系

```
cmd/workbench/main.go
  └─ config ─────────────┐
  └─ db ──┬─ sqlitevec   │
          └─ migrations  │
  └─ server ─┬─ db ──────┤
             ├─ config ──┘
             ├─ rag ───────── db / embedding 网关
             ├─ schedule ──── db / aiJobLLM(server) / bus
             ├─ notify ────── schedule.EventBus / db / SMTP
             ├─ mcpclient ─── 外部 MCP HTTP 服务
             └─ workbench.UIFS（embed，反向依赖顶层包）
```

| 类别 | 依赖 |
|---|---|
| Go 核心 | iris v12.2.11、mattn/go-sqlite3 v1.14.53、golang-migrate v4.19.0、yaml.v3、golang.org/x/crypto（bcrypt）、golang.org/x/net（HTML 正文提取）、google/uuid |
| 前端核心 | Vue 3.5、vue-router、Element Plus、marked、DOMPurify、highlight.js、Vite 5、TypeScript（`noUnusedLocals` 开启） |
| 外部服务（可选） | OpenAI 兼容 `/chat/completions`、兼容 `/embeddings`（本地 vLLM/Ollama 可无 key）、SMTP 587 STARTTLS、stateless HTTP MCP 服务 |
| 构建期 | Go 1.25+、CGO_ENABLED=1、build tag `sqlite_fts5`、Node 18+ / pnpm；跨平台用 xgo + Docker |

## 4. 启动流程与装配

### 4.1 `cmd/workbench/main.go` → `run()`

| 步 | 动作 |
|---|---|
| 1 | 默认 JSON `slog`（stdout）；解析 `-config`（默认 `config.yaml`） |
| 2 | `config.Load`（文件不存在用默认值）→ 按 `logging.level` 重建 logger → `config.ApplyEnv` 环境变量覆盖 |
| 3 | `config.ResolvePath` 展开 `staticDir` / `dbPath` / `logDir` 的 `~/`；`MkdirAll` 建目录 |
| 4 | `db.Open`：注册 sqlite-vec 扩展 → WAL + `busy_timeout=5000` + `foreign_keys=on` + `SetMaxOpenConns(1)` → 执行 15 个内嵌迁移 |
| 5 | `db.LoadOrCreateSecret(ctx, "token", ...)`：取/生成 32 字节 token 密钥，兼容迁移旧 `.token_secret` 文件 |
| 6 | 无用户且未配置 `ADMIN_SETUP_TOKEN` 时打印「首个注册者将成为超管」告警 |
| 7 | `server.New(...)`（见 4.2）→ `srv.App()` 注册路由与中间件 |
| 8 | `app.Run(iris.Addr(":"+port), iris.WithoutServerError(iris.ErrServerClosed))` 阻塞监听 |

`defer database.Close()` 是唯一确定的清理动作。

### 4.2 `server.New` 子系统装配顺序

| 序 | 动作 | 失败语义 |
|---|---|---|
| 1 | 解析并创建 `static_dir` 绝对路径与 `{static_dir}/_admin` | 返回 error |
| 2 | `initMCPTools()`：拉取各 MCP 服务 `tools/list`，转成静态 `aiTool` 表 | 配置缺 `name`/`url` **fail fast**；服务不可达仅告警跳过 |
| 3 | `schedule.NewEventBus()` → `notify.SubscribeEmailNotifier(...)` | `enabled=true` 且缺 `host`/`user` 返回 error |
| 4 | `schedule.enabled` 时：`time.LoadLocation(timezone)` → `NewScheduler` → 依次 AddJob 三个 job → `Start` | 时区非法回退 UTC（告警）；cron 表达式非法 fail fast |
| 5 | `embedding.Enabled()` 时：`EnsureVecIndex(dim)` → `EnsureFTS()` → `rag.NewService` | vec 建表失败 → RAG 降级关闭；FTS 失败 → 降级纯向量 |

`Server` 结构体持有的跨请求状态：`aiApprovals`（审批 hub）、`scheduler`、`bus`、`rag`、`mcpTools`、`tokenSecret`。

### 4.3 生命周期约束

- 仓库源码中**没有 `signal.Notify` / 自定义 `Shutdown` 调用**；`Scheduler.Stop()` 存在但无调用点，job 均为短扫描，随进程终止（注释明确「无需优雅停止」）。不要把「SIGTERM 优雅关闭」当作既有能力。
- `EventBus.Publish` 为每个 handler 起独立 goroutine，因此**事件 ctx 不能绑定发布方请求生命周期**：AI 定时任务与失败通知刻意使用 `context.Background()` 发布，否则 LLM 完成后 `defer cancel()` 会打断异步邮件 handler 的查库。
- sqlite-vec 的 `sqlite3.h` vendor 自 mattn 的 `sqlite3-binding.h`，升级 `mattn/go-sqlite3` 后必须执行 `make sync-vec-headers` 保持版本一致。

## 5. 关键流程

### 5.1 认证与组织上下文

```
POST /api/login {userId,password,orgId?}
  → 登录失败限流检查（rate_limits）
  → FindUserByName / FindUserByGlobalName（用户名全局唯一）
  → VerifyPassword（bcrypt；失败时兼容旧 SHA-256 无盐 hex，改密后升级）
  → ResolveUserOrgIdentity 解析组织上下文
  → GenerateToken：payload "<userID>:<expiryUnix>"，HMAC-SHA256 签名
      raw = payload + ":" + sigHex → base64.URLEncoding
  → {token, user{userId,orgId,userName,orgName,role}}

每个受保护请求
  → Authorization: Bearer <token> → URL base64 解码 → hmac.Equal 校验签名 → 检查 expiry
  → X-Org-Id（缺省 0）→ db.ResolveUserOrgIdentity 校验归属（status='active'）
  → 写入 ctx：userID / orgID / userName / orgName / orgRole / isPlatformAdmin / role
  → handler
```

角色映射 `externalRole`：平台超管 → `admin`，组织 `owner`/`admin` → `org_admin`，其余 → `user`；`RequireUserManager` 要求前两者。token **不固化 orgID**（组织切换无需重新签发），代价是每请求一次身份查询。首次初始化见 `handleSetPassword`：`!exists && !hasUsers` 时创建超管，`AdminSetupToken` 用 `subtle.ConstantTimeCompare` 校验。

### 5.2 文件路径沙箱与写操作

```
userRoot(ctx) = {serDirAbs}/{orgName}/{userName}/        （自动 MkdirAll）
  └─ safeJoin(baseDir, relPath)：Clean → Join → Rel 校验，拒绝 ".."/"../" 前缀
      └─ resolveUserPathSafe：AllowSymlink=true 直接返回；
         false 时 EvalSymlinks(fsPath) 与 EvalSymlinks(root) 双重校验真实路径
      └─ resolveFsEntrySafe（写操作）：条目本身是符号链接时只操作链接，不跟随目标
```

所有文件 handler、分享（按 owner 反查 name 的 `userRootByOwner`）、AI 工具路径都复用该链路。上传另有 `sanitizeUploadName`；文件树展示用 `isHidden` 过滤。RAG 写钩子挂在这些操作之后：`HandleWrite`（异步，上传与 AI `write_file`）、`HandleDelete`（同步，目录按前缀删）、`HandleRename`（重命名/移动）；`mkdir` 无钩子。

### 5.3 AI 对话：Agent 循环与审批

```
POST /api/ai/chat（SSE）
  → 校验 messages（≤30）/ body（512KB）/ qaId、sessionId（validTraceID）
  → RAG 前置检索（rag.Search，8s 超时静默降级）→ 编号片段注入 system → rag_hits 事件
  → 循环 round = 0..5：
       组装上游 /chat/completions（stream=true, include_usage=true）
       tools 仅在 ai.tools=true 且未达上限轮时下发
       → forwardAIStream 逐行透传；tool_calls 分片按 index 累积
       → 无 tool_calls：补发 [DONE]，结束
       → 有 tool_calls：tool_call / tool_result 事件，结果以 tool 消息回传继续
       达上限轮：不再下发 tools + system 提示「基于已有结果直接回答」
  → usage 事件（多轮累加）→ [DONE]
```

写类工具走 `runToolWithApproval`：`approval_request` → 阻塞等待三路 `select`（用户决定 / 5 分钟超时 / 客户端断开）→ `approval_resolved`。跨请求协调由 `aiApprovalHub` 完成：审批 ID 复用上游 `tool_call.ID`，`create(id, userID)` 注册缓冲 channel，`resolve` 校验绑定用户并删除条目实现一次性，`remove` 作废；`POST /api/ai/approval` 对不存在/已处理/非本人统一返回 404 防探测。所有外部 MCP 工具 `NeedApproval=true`，复用同一链路。

### 5.4 RAG：索引与混合检索

```
索引（写钩子 / reindex）
  Indexable（24 类扩展名、≤2MiB、非隐藏）→ HTML 剥标签 → SHA-256 判重
  → SplitChunks（空行分段，800 字目标 + 100 字重叠，记录 1-based start_line）
  → [可选 digest_model 生成 chunk_index=-1 摘要卡]
  → Embedder（批 16、dimensions 降维、L2 归一化）
  → ReplaceFileIndex 事务：删 vec/fts/chunks → 写 file_chunks + file_chunk_fts + file_chunk_vec
  → upsert file_index_state(ok)；失败记 error 状态，下次重试

检索（Search，8s 预算）
  query 截断 2000 字 → 向量路 KNN(topK*4, owner 分区, max_distance 过滤)
                     → FTS 路 bm25(topK*4, trigram, query≥3 字)
  → RRF 融合 score = Σ 1/(60+rank)，取 topK（对话为 5）
  → [可选 judge_model 批量相关性预判，失败保留原结果]
  → ContextPrompt 以 [1]..[n] 编号注入；rag_hits 事件同源推送
```

`EnsureVecIndex(dim)` 用**双信号**判定（meta 记录一致 **且** vec 表实际存在），否则 DROP 重建并作废 `file_index_state`；`EnsureFTS` 在 FTS 表缺失时重建并回填。`file_chunk_vec` 的 DELETE 是惰性删除，释放空间需 `make ragclean ARGS=--reset`（整表 DROP + VACUUM），启动时自动重建空表。

### 5.5 Todo：revision 乐观锁

```
GET /api/tasks → {revision, tasks}（列表瘦身，content → 80 字 contentPreview）
写路径 PATCH/POST/DELETE /api/tasks/{id} 携带 baseRevision
  → 与 users.revision 比对：不一致 409 + code 40901 + 服务端任务副本
  → 一致：单条写入 + revision +1，返回新 revision
前端 todoSync：成功更新 lastRevision；409 → 差异字段对比卡（POST/DELETE 直接重拉）
            30s GET /api/tasks/revision 轮询 + visibilitychange 回前台对账
```

`db.UpdateTask` 是**全字段覆盖写**，因此 `patchTask` 会探测请求体中 `task.content` 字段是否存在：缺失时回填服务端现值，显式传空串才是主动清空。`sortutil.go` 提供章节号感知的自然排序。

### 5.6 分享访问

`/s/{token}` 是公开路由，后端校验分享存在后直接返回 embed 的 `index.html`；前端 `useIndexApp` 切到 `dataSource.mode='share'`，数据走 `GET/POST /api/share/{token}`。服务端 `handleShareData` 统一校验 `effectiveAt` / `expiresAt` / `access_count` / 密码 bcrypt 哈希，并按实际 `os.Stat` 返回目录列表或文件元信息（含 base64 内容）。密码错误码 `40304`/`40305` 触发前端密码弹窗循环，密码存 `sessionStorage`。

### 5.7 定时任务与事件投递

```
Scheduler（对齐整分钟的 1min ticker，Vixie cron bitset 匹配）
  ├─ todo_due_scan      cron = schedule.todo_due_cron（默认 */5 * * * *）
  │    ScanDueTasks → MarkNotified(INSERT OR IGNORE) → task.due
  ├─ todo_time_reminder cron = * * * * *
  │    ScanTimeReminders → MarkNotified → task.reminder（start / due）
  └─ ai_jobs_runner     cron = * * * * *
       每分钟重读 ai_jobs → Matches && last_run_at != 当前分钟 → goroutine
       → buildAIJobPrompt（角色 + ≤50 条任务 + 用户 prompt + Markdown 格式要求）
       → aiJobLLM（RAG top-3 前置 + 非流式 aiChatOnce，5min 超时）
       → MarkAIJobRun（ai_job_runs，output 前 4KB）
       → bus.Publish(context.Background(), user.notify)

EventBus.Publish → 每个 handler 独立 goroutine（panic recover）
  → EmailNotifier.onTaskDue / onTaskReminder / onUserNotify
  → GetUserEmail（空跳过）→ SMTP STARTTLS → multipart/alternative 邮件
```

`KnownTopics` 注册表（供 `list_events` 工具内省）：`task.due`、`task.reminder`、`user.notify`；只有 `user.notify` 标记为可由 AI 发布。全局公共任务（`ai_jobs.user_id IS NULL`）在执行时按 `ListUsersWithEmail` 展开为每个用户独立执行与投递，`last_run_at` 只记一次。

### 5.8 SPA 路由与深链兜底

前端用 `createWebHistory()`，所有深链必须由后端兜底：`handleStaticWithAuth` → `handleRouteRedirect`，`routes` 配置为 `__spa__` 时返回 embed 的 `index.html`，`__listdir__` 时列目录，其他字符串作 302 或显示别名。`isPublicEntry()` 放行 `/`、UI 资源与 `routes` 中的键；其余静态路径要求 Bearer + 组织解析。**新增前端路由必须同步在 `config.yaml` 的 `routes` 注册**，否则刷新该 URL 会得到 401（历史 bug 的根因）。

## 6. 数据模型

### 6.1 迁移体系

`internal/db/migrations/` 使用 golang-migrate，`//go:embed migrations/*.sql` 内嵌，每个版本含 `.up.sql` / `.down.sql`。`db.migrate()` 用 `sqlite3.Config{NoTxWrap:true}`（`PRAGMA foreign_keys` 在事务内无效）；存量库无 `schema_migrations` 但有 `users` 时 `Force(1)` 跳过 baseline。迁移文件禁止手写 `BEGIN`/`COMMIT`；仅 `000001_baseline` 可用 `IF NOT EXISTS`，后续脚本必须精确表达增量。

| 版本 | 主题 | 版本 | 主题 |
|---|---|---|---|
| 000001 | baseline（组织/用户/任务/分享/密钥/限流） | 000009 | 任务精确时刻 + `ai_jobs` |
| 000002 | RBAC 多组织（`user_orgs`） | 000010 | 全局公共 AI 任务（重建 `user_id` 可空 + 种子） |
| 000003 | 删除遗留列、统一外键 | 000011 | `ai_job_runs` |
| 000004 | `user_org_feature`（删旧 `org_features`） | 000012 | 删除 `visit_logs`（改文本访问日志） |
| 000005 | `tasks.files`（todo↔file） | 000013 | RAG 向量索引三表 + `rag_meta` |
| 000006 | `ai_sessions` | 000014 | `file_chunks.start_line` |
| 000007 | `users.revision`（删 `version_json`） | 000015 | 混合检索 FTS5 虚拟表 |
| 000008 | 用户邮箱 + `notify_log` | | |

### 6.2 现行表清单（19 项，含虚拟表与框架表）

| 组 | 表 | 关键列 / 约束 | 用途 |
|---|---|---|---|
| 组织与权限 | `orgs` | `id`, `name UNIQUE` | 组织 |
| | `roles` | `id`, `name`；预置 1 admin / 2 user / 3 org_admin | 对外角色字典 |
| | `users` | `name UNIQUE`, `mobile UNIQUE`, `password_hash`, `is_platform_admin`, `revision`, `email` | 用户、平台超管标志、任务版本号、通知邮箱 |
| | `user_orgs` | `UNIQUE(user_id, org_id)`, `role(member/owner/admin)`, `status` | 用户-组织多对多 |
| | `user_org_feature` | `UNIQUE(user_id, org_id, feature_code)` | 按用户×组织的功能开关 |
| | `app_secrets` | `key PK`, `value BLOB` | token 签名密钥（旧 `.token_secret` 迁入） |
| | `rate_limits` | `key PK`, `count`, `expires_at` | 登录/分享密码失败窗口 |
| 业务 | `tasks` | `PK(id,user_id,org_id)`, `files TEXT '[]'`, `scheduled_time`/`due_time` | 任务、关联文件、精确提醒 |
| | `shares` | `token UNIQUE`, `owner_*`, `resource_path/type`, `max_access_count/access_count`, `password_hash`, `effective_at/expires_at` | 分享 |
| | `notify_log` | `UNIQUE(topic, dedup_key, notify_date)` | 通知按天去重 |
| AI 会话与调度 | `ai_sessions` | `messages TEXT '[]'`（前端 UI 快照 JSON），索引 `(org_id,user_id,updated_at)` | 会话历史 |
| | `ai_jobs` | `user_id NULL`（全局公共任务）, `cron_expr`, `prompt`, `enabled`, `last_run_at`, `last_status` | 定时任务 |
| | `ai_job_runs` | `job_id FK`, `status`, `output`（前 4KB） | 执行历史 |
| RAG | `file_index_state` | `PK(org_id,user_id,path)`, `content_hash`, `status ok/error` | 索引幂等状态 |
| | `file_chunks` | `UNIQUE(org,user,path,chunk_index)`, `start_line`（1-based），`chunk_index=-1` 为摘要卡 | 文本块 |
| | `rag_meta` | `key PK`；当前唯一键 `vec_dim` | 向量维度记录 |
| | `file_chunk_fts` | FTS5 虚拟表，`tokenize=trigram`，`rowid = file_chunks.id` | 关键词召回 |
| | `file_chunk_vec` | vec0 虚拟表，`owner PARTITION KEY`，`embedding float[dim]` | 向量 KNN；**运行时由 `EnsureVecIndex` 创建，不在迁移脚本中** |
| 框架 | `schema_migrations` | golang-migrate 自动维护 | 迁移版本与 dirty 标志 |

已删除的历史表：`visit_logs`（000012 DROP）、`org_features`（000004 被 `user_org_feature` 取代）。

### 6.3 关系与约束要点

```
orgs ─< user_orgs >─ users ─< tasks
                        ├─< ai_sessions
                        ├─< ai_jobs（user_id 可空 = 全局）
                        └─< shares（owner_user_id）
user_orgs ─< user_org_feature
ai_jobs ─< ai_job_runs
file_index_state / file_chunks / file_chunk_fts / file_chunk_vec  ← 按 (org_id,user_id) 分区
```

- 外键 `PRAGMA foreign_keys=on`，连接串带 `_journal_mode=WAL` 与 `_busy_timeout=5000`，`SetMaxOpenConns(1)` 串行化写入。
- 存储层用整数 `org_id/user_id`，文件路径用 `orgName/userName` 拼接（双轨）；改名需同步迁移磁盘目录。
- `users.revision` 是所有任务写入的单调版本号；`tasks.files` 为 JSON 数组，空关联归一为 `nil` + `omitempty` 以保持旧数据兼容。
- vec0 的 `owner = fmt.Sprintf("%d:%d", orgID, userID)` 是分区键，KNN 只扫本人分区，无跨用户泄漏。
- `notify_log` 去重在 **Publish 之前**用 `INSERT OR IGNORE` + `RowsAffected` 原子判定，无竞态窗口，但 SMTP 失败不会重投；`user.notify` 不去重。

## 7. 接口设计

### 7.1 统一约定

| 项 | 约定 |
|---|---|
| 响应 | `Response{code, msg, data}`；`writeOK` / `writeFail` / `writeFailMsg` / `writeFailData` / `serverError` 收敛 handler 样板 |
| 错误码 | `HTTP 状态码 × 100 + 序号`，定义在 `codes.go` + `errMsgMap`；如 `40901` 任务冲突、`40902` reindex 运行中、`50301` AI 未配置 |
| 认证头 | `Authorization: Bearer <token>` + `X-Org-Id`；`/api/admin/*` 额外 `RequireUserManager` |
| CORS | `/api/*` 固定 `Access-Control-Allow-Origin: null`（沙箱 iframe 的 null origin），不设 `Allow-Credentials` |
| 流式 | `/api/ai/chat`（SSE）、`/api/media`（Range）跳过压缩与 `ctx.Record()` |
| 载荷结构 | 集中在 `response.go`（`loginData`、`treeData`、`shareCreateData`、`shareAccessData` 等） |

### 7.2 路由全量清单

`Server.App()` 中 `api := app.Party("/api")`，下表省略公共前缀。公开路由无 Bearer；其余均需有效 token + 组织上下文解析成功。

**公开**

| Method | 路径 | 说明 |
|---|---|---|
| POST | `/api/login` | 登录（内部登录失败限流） |
| POST | `/api/set-password` | 首用户初始化 / 改密 / 空密码激活 |
| GET · POST | `/api/share/{token}` | 分享数据（密码/时间/次数在 handler 内校验） |
| GET | `/api/media` | Range 流式播放，靠 2h HMAC ticket 鉴权 |
| GET | `/s/{token}[/{p:path}]` | 分享页面入口（返回 embed 的 `index.html`） |
| GET | `/{path:path}` | 静态兜底；`isPublicEntry` 放行 `/`、UI 资源、`routes` 键，其余要求 Bearer |
| OPTIONS | `/api/*` | 全局安全头中间件直接 204 |

**认证与组织**

`/api/me`、`/api/userinfo`、`/api/org/switch`(POST)、`/api/org/features`、`/api/profile/email`(POST)、`/api/map`、`/api/tree`、`/api/org-members`。

**任务**

| Method | 路径 | 说明 |
|---|---|---|
| GET | `/api/tasks` | `{revision, tasks}`（列表瘦身） |
| GET | `/api/tasks/revision` | 轻量版本号 |
| GET | `/api/tasks/{id}` | 详情（含 `content`） |
| GET | `/api/tasks/user/{id}` | 同组织成员任务（校验 active） |
| GET | `/api/tasks/by-file` · `/api/tasks/open` | 文件反查 / 未完成任务 |
| PATCH · POST · DELETE | `/api/tasks/{id}` | 增量写，携带 `baseRevision` |

**文件与分享**

`GET /api/file`、`GET /api/media/ticket`、`POST /api/upload`、`POST /api/mkdir`、`DELETE /api/file`、`POST /api/rename`、`POST /api/move`；`GET|POST /api/share`（列表/创建）、`DELETE /api/share/{id}`（撤销）。

**AI**

| Method | 路径 | 说明 |
|---|---|---|
| GET | `/api/ai/status` | `{enabled, model, tools}` |
| POST | `/api/ai/chat` | SSE 对话（Agent 循环） |
| POST | `/api/ai/approval` | 写工具审批 `{id, approved}` |
| POST | `/api/ai/reindex` | 全量重建（并发 40902）；与 `reindex_files` / `reindex_status` 工具共用 `startReindex` |
| GET | `/api/ai/reindex/status` · `/api/ai/index-status` | 进度 / 文件索引状态 |
| GET · POST | `/api/ai/jobs` | 定时任务列表 / 创建 |
| PUT · DELETE | `/api/ai/jobs/{id}` | 更新 / 删除 |
| GET | `/api/ai/jobs/{id}/runs` | 执行历史 |
| GET · POST | `/api/ai/sessions` | 会话列表 / 创建 |
| GET · PUT · DELETE | `/api/ai/sessions/{id}` | 会话详情 / 更新 / 删除 |

**管理（`/api/admin`，`auth` + `RequireUserManager`）**

`GET|POST /api/admin/users`、`PATCH|DELETE /api/admin/users/{id}`、`GET /api/admin/roles`、`GET /api/admin/users/{id}/dashboard`、`GET /api/admin/users/{id}/features`、`PATCH /api/admin/users/{id}/features/{code}`。handler 内另有细粒度约束：组织管理员仅本组织、不能授予 `admin`、禁止删自己或最后一个 admin。

> 根 `README.md` 提到的 `/__map__`、`/__tree__`、`/tasks.json` 未在代码注册，实际为 `/api/map`、`/api/tree` 与 `/api/tasks*`。

### 7.3 SSE 协议

服务端 `sseWriteEvent` 发出 7 种自定义事件，另有 OpenAI 兼容无名增量与最终 `data: [DONE]`：

| event | 载荷 | 时机 |
|---|---|---|
| `rag_hits` | `{hits:[{path,content,startLine,distance}]}` | 流 header 就绪后一次性 |
| `tool_call` / `tool_result` | `aiToolCall{id,type,function}` / `{id,name,result}` | 每轮工具调用与结果 |
| `approval_request` / `approval_resolved` | `{id,name,arguments}` / `{id,outcome}` | 写工具等待 / 定格 |
| `usage` | `{prompt_tokens,completion_tokens,total_tokens}` | 最终轮（Agent 多轮累加） |
| `error` | `{msg}` | 流已开启后上游失败（HTTP 状态不可再改） |

前端 `api/ai.ts` 逐行解析 `event:` + `data:`，跨 chunk 半截 JSON 忽略；`useAIChat.ts` 按 `steps` 时序渲染「思考 → 工具 → 回答」，并按「轮」预算（展开后 ≤28 条）裁剪历史以适配后端 30 条上限。

### 7.4 前端请求封装

无 axios、无 baseURL，端点写同源相对路径；`lib/common.ts` 的 `authHeaders()` 注入 Bearer 与 `X-Org-Id`，`authFetch` 合并 headers，`apiCall` 解包 `{code,msg,data}` 并对 401 做全局跳转（排除 `/api/login`、`/api/me`、`/api/share`，因分享密码错误也返回 401）。开发期 Vite 只代理 `/api`，`/s/` 故意不代理。任务写路径有特化解析 `parseMutationResponse`，把 `40901` 增强为带 `serverTask`/`serverRevision` 的 Error。

## 8. 安全模型

| 面 | 机制 |
|---|---|
| 认证 | HMAC-SHA256 Bearer token（无状态、不存 Cookie）；bcrypt 密码哈希，兼容旧 SHA-256 仅供存量升级 |
| 授权 | `AuthMiddleware` 每请求校验 `X-Org-Id` 归属；`RequireUserManager` + handler 内细粒度约束；功能开关控制入口 |
| 首次初始化 | `ADMIN_SETUP_TOKEN` 常量时间比较，仅用户表为空时校验，关闭部署到注册的抢占窗口 |
| 路径 | `safeJoin` + `resolveUserPathSafe`（`EvalSymlinks` 双重校验）+ `sanitizeUploadName`；写操作对软链接只操作链接条目 |
| 凭据 | AI keys / SMTP 授权码 / MCP header token 走环境变量（headers 支持 `${ENV_VAR}` 展开），不落仓库 |
| 前端渲染 | Markdown 经 DOMPurify 净化；HTML 预览用无 `allow-same-origin` 的沙箱 iframe + null-origin CORS |
| 邮件正文 | 用户内容 `html/template` 转义；`safeHref` 只允许 http(s) 且与 `site_url` 同 host，防 `javascript:` 与 AI 编造链接 |
| AI 工具 | 工具在后端执行、路径复用沙箱、结果 32KB 截断、panic 兜底；写类与外部工具一律审批，审批绑定用户且一次性 |
| 链路头 | `qaId`/`sessionId` 用 `validTraceID` 限长 64 可见 ASCII，防 header 注入 |
| 限流 | 登录失败 5 次/分钟/IP、分享密码 5 次/5 分钟，`rate_limits` 持久化、跨重启生效 |
| 已知边界 | token 无法单独吊销（只能轮换密钥）；token 默认 30 天（示例配置 7 天）；无 CSRF 问题（Bearer 而非 Cookie） |

## 9. 扩展性

| 要新增 | 落点 | 注意 |
|---|---|---|
| REST 接口 | `server/server.go` 的 `App()` 注册 + 单独 handler 文件；载荷结构放 `response.go`；错误码放 `codes.go` | 需 auth 就挂 `AuthMiddleware`；SSE/Range 类接口要加入压缩与日志的跳过前缀 |
| 数据表 | `internal/db/migrations/NNNNNN_name.up.sql` + `.down.sql` | 每个版本一对文件；禁止 `BEGIN/COMMIT`；不要用 `IF NOT EXISTS` 掩盖增量 |
| 内置 AI 工具 | `server/aitools.go` 的 `aiTools` 注册表 + `execAITool` 分发；写类设 `NeedApproval` | 路径必须走 `resolveUserPathSafe`；结果限长 32KB；当前 15 个工具，更新后同步功能文档 §9.3 的清单 |
| 外部工具 | `ai.mcp.servers` 配置（无需改代码） | 仅 stateless HTTP；`mcp_<server>_<tool>` 命名空间；一律需审批 |
| 事件 topic | `schedule/jobs.go` 的 `KnownTopics` + 载荷结构 + 发布方 | 订阅者在 `Server.New` 注册；异步 handler 不能依赖发布方 ctx |
| 通知渠道 | 新增订阅者（实现 `schedule.Handler`）并在 `Server.New` 订阅现有 topic | `Publish` 不等待、panic 隔离；渠道自身负责去重与失败处理 |
| 定时 job | `schedule/` 新增 `New*Job` 并在 `Server.New` 中 `AddJob` | cron 非法应 fail fast；job 内 DB 扫描要短，避免阻塞 tick |
| 前端页面 | `router/index.ts` 路由 + `views/` 视图 + `api/` 封装 | **同步在 `config.yaml` 的 `routes` 注册 `__spa__`**；移动版需加入 `PC_TO_MOBILE`/`MOBILE_TO_PC` 映射与 `/m/*` 路由 |
| 预览类型 | `components/editor/FileViewer.vue` 分派 + `server.mimeTypeByExt` | 二进制类需走下载/blob；媒体需 ticket + Range |
| 配置项 | `config/config.go` 结构体 + `DefaultConfig` + `mergeConfig` + 可选 `ApplyEnv` | 注意「非零值合并」语义：bool `false` 无法覆盖默认 `true`；同步 `config.yaml` 与两份文档 |
| 前端依赖 | `frontend/package.json` + `vite.config.ts` 的 `manualChunks` | `pnpm build` 会先跑 `vue-tsc --noEmit`；`noUnusedLocals` 开启 |

## 10. 设计取舍与已知约束

| 决策 | 选择 | 理由 | 代价 / 约束 |
|---|---|---|---|
| 交付形态 | 单二进制 + `//go:embed frontend/dist` | 部署只需一个文件 + config + db | 前端改动必须重新编译二进制；`dist` 需在构建期存在（Makefile `build: frontend` 每次重建前端） |
| 数据库 | SQLite 单文件、WAL、`SetMaxOpenConns(1)` | 零运维、便携、备份简单 | 写入串行、吞吐有限；不支持多实例共享库 |
| SQLite 驱动 | 从 modernc（纯 Go）换 mattn + cgo + sqlite-vec 静态扩展 | 原生 vec0 KNN，向量存库内，无需独立向量库 | `CGO_ENABLED=1`、交叉编译依赖 xgo；放弃原 `CGO_ENABLED=0` 目标；升级 mattn 需 `make sync-vec-headers` |
| 检索 | 向量 KNN + FTS5 trigram + RRF 融合 | 专有名词/错误码的精确匹配不被向量距离阈值误杀 | 需统一 `sqlite_fts5` build tag（所有构建入口在 Makefile 收敛）；trigram 存储与维护成本；DELETE 惰性删除需 DROP 重建 |
| 调度 | 手写 5 段 cron（bitset）+ 进程内 EventBus | 零依赖、匹配器是纯函数便于单测、触发/分发/投递三层解耦 | 单实例、无持久化队列与重试、进程重启丢事件；时区依赖 `schedule.timezone` |
| 事件分发 | `Publish` 每 handler 独立 goroutine + panic recover | 发布方不被慢订阅者拖住、单订阅者故障隔离 | ctx 不能绑定发布方生命周期；AI job 必须用 `context.Background()` 发布 |
| 会话 | 无状态 HMAC token，组织上下文走 `X-Org-Id` | 无会话表、支持前端切换组织 | 不能单独吊销；每请求一次身份解析查询 |
| 路径标识 | 存储层整数 id，磁盘路径 `orgName/userName` | 目录可读、按账号自然隔离 | 双轨；改名需同步迁移磁盘目录 |
| 任务同步 | 服务端权威 `revision` + 单任务乐观锁 + 30s 轮询 | 增量写、多设备自动对账、冲突只弹字段级对比卡 | 1.3.0 起废弃 `PUT /api/tasks`，前后端必须同版本部署 |
| 列表瘦身 | `content` → 80 字 `contentPreview`，详情单独接口 | 省公网带宽 | `PATCH` 必须实现「缺省保留」语义，否则快捷操作会清空正文 |
| AI 会话持久化 | 前端 UI 消息快照 JSON 存 `ai_sessions.messages` | 刷新/断网保住半截回复，服务端无需理解展示态 | 服务端无法解析消息语义；历史需按「轮」预算裁剪（≤28 条）；快照上限 256KB |
| MCP 接入 | 仅 stateless HTTP 服务 | 不引入会话生命周期与权限模型 | 不支持 stdio 子进程、有状态 SSE、会话恢复与重连；启动失败仅告警跳过 |
| 安全与体验 | 写工具审批 + 5 分钟超时，绑定用户且一次性 | Human-in-the-loop，防模型越权写 | 对话流会阻塞等待；客户端断开自动作废 |
| 可用性 | RAG / FTS / embedding / judge / MCP 失败全面静默降级 | 对话主链路可用性优先于检索完整性 | 检索不完整时用户无感，只能查 `/api/ai/index-status` |
| 响应压缩 | 全局协商压缩，显式排除 SSE 与 Range | JSON 与静态资源公网传输缩减 70–85% | 新增流式接口必须记得加入跳过前缀 |
| 前端形态 | PC 与移动两套独立视图，共用业务层 | 浏览优先、PC 零改动 | 两套视图需同步能力；`PC_TO_MOBILE` 未含 `/jobs`，窄视口下该路径仍用响应式 PC 布局 |
| 优雅关闭 | 无自定义信号处理，仅 `defer db.Close()` | job 均为短扫描，中断无害 | 长请求与进行中的 SSE 可能被直接截断 |
| 通知去重 | 发布前 `INSERT OR IGNORE` 原子判定 | 无竞态窗口、跨重启生效 | SMTP 失败不重投；`user.notify` 不去重 |

**其他已核实的实现现状（非缺陷判断）**：

- `create_task` 是写操作但注册表未设 `NeedApproval`，与「写操作先请求确认」的系统提示表述不完全一致；`write_file`、`update_task`、四个 AI job 工具、`notify_user` 均需审批。
- AI job 幂等用 `last_run_at` 字符串比对（job 侧按 `schedule.timezone` 格式化，写回侧用进程本地时区）；若两者不一致，同分钟重复 tick 理论上存在重跑窗口。
- `config.mergeConfig` 未合并 `Server.AllowSymlink`，该 YAML 项当前不生效，只能由 `ALLOW_SYMLINK` 环境变量开启。
- `config.DefaultConfig` 的默认 `routes` 仍是 3 个历史键，实际 SPA 深链依赖 `config.yaml` 覆盖。
- 前端没有「设置密码」UI；`api/auth.ts` 的 `setPassword` 无调用点，也未提供 `setupToken`/`oldPassword` 字段。首用户初始化需直接调 HTTP 接口。
- `frontend/vite.config.ts` 当前是**单入口**（只有 `index.html`），`embed.go` 注释、`server.isUIAsset` 白名单与 `config.yaml` 中的 `todo.html` 属历史遗留。
- 根 `README.md` 的数据库表清单、端点别名与 Vite 多入口描述已过时；偏差清单见 [`README.md`](README.md#6-已知文档偏差清单)。

## 11. 构建、测试与质量门禁

| 入口 | 命令 | 说明 |
|---|---|---|
| 本地构建 | `make all` / `make build` | 先 `pnpm build`（含 `vue-tsc --noEmit`）再 `go build -tags sqlite_fts5` |
| 前端 | `make frontend` / `cd frontend && pnpm dev` | 产物 `frontend/dist`，被 `//go:embed` 打包 |
| 检查 | `make check` | `go vet` + `golangci-lint` + `go test`，全部携带 `sqlite_fts5` |
| 跨平台 | `make build-all` / `make package` | xgo + Docker 出 5 个平台包 |
| 运维 | `make ragclean [ARGS=--reset]` | 清理/全量重置 RAG 索引（必须经此入口，独立 `go run` 缺 build tag） |
| 头文件 | `make sync-vec-headers` | 升级 mattn/go-sqlite3 后同步 vendored `sqlite3.h` |

单测覆盖：cron 解析/匹配、事件总线分发与 panic 隔离、到期扫描去重、邮件内容与报文组装、`safeJoin` 沙箱、审批协调器、会话持久化、vec0 KNN 冒烟、存量库基线迁移、压缩行为、RAG 切块与 HTML 提取等。

## 12. 代码索引

| 主题 | 入口 |
|---|---|
| 配置 | `internal/config/config.go` |
| 启动 | `cmd/workbench/main.go`（`run`）、`internal/server/server.go`（`New`/`App`） |
| 路由与中间件 | `internal/server/server.go`（`App`/`compressionMiddleware`/`loggingMiddleware`/`securityHeadersMiddleware`） |
| 认证与沙箱 | `internal/server/auth.go`、`server.go`（`safeJoin`/`resolveUserPathSafe`）、`fsops.go` |
| 响应与错误码 | `internal/server/response.go`、`codes.go` |
| 数据层 | `internal/db/*.go`、`internal/db/migrations/*.sql` |
| AI | `internal/server/ai.go`、`aitools.go`、`aiapproval.go`、`aitools_mcp.go` |
| RAG | `internal/rag/*.go`、`internal/db/rag.go`、`internal/sqlitevec/vec.go` |
| 调度与通知 | `internal/schedule/*.go`、`internal/notify/*.go` |
| 前端 | `frontend/src/router/index.ts`、`lib/common.ts`、`composables/useAIChat.ts`、`composables/todoSync.ts` |
| 构建 | `Makefile`、`frontend/vite.config.ts`、`frontend/package.json`、`embed.go` |
