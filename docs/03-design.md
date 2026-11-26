# 软件设计文档

> 面向：开发者、维护者 ｜ 代码基线：`v1.3.0-26-gc0577c4` + 工作树迁移 000021–000024
> 使用者视角见 [`02-functional-guide.md`](02-functional-guide.md)；构建与发布见 [`04-build-and-release.md`](04-build-and-release.md)；配置与运维见 [`05-operations.md`](05-operations.md)；文档维护规范与基线说明见 [`README.md`](README.md)。

## 1. 关于本文档

| 项 | 说明 |
|---|---|
| 覆盖范围 | 系统架构、模块划分、关键流程、数据模型、接口设计、依赖关系、扩展性与设计取舍 |
| 不覆盖 | 构建/发布（[`04-build-and-release.md`](04-build-and-release.md)）、运行期配置与运维（[`05-operations.md`](05-operations.md)）、功能操作与 FAQ（[`02-functional-guide.md`](02-functional-guide.md)） |
| 阅读路径 | §2 架构 → §4 启动装配 → §6 数据模型 / §7 接口 → §5 流程 → §9 扩展点 |
| 引用约定 | 「文件路径 + 函数/类型/常量名」，不写行号 |

2026-08-21 的 RBAC / 迁移方案设计稿与邮件模板预览已从 `docs/` 移除；其中的 `CGO_ENABLED=0` + modernc、`org_features`、自愈式迁移等结论早已被取代，本文件只描述现行实现，历史决策见根 `CHANGELOG.md`。

## 2. 架构总览

### 2.1 分层与交付形态

```
┌───────────────────────────────────────────────────────────────────┐
│ 浏览器  Vue3 SPA（PC DefaultLayout / 移动 /m/*）                  │
│   lib/common.ts：authHeaders / X-Org-Id / apiCall / 401 拦截      │
│   composables/useAIChat.ts：SSE / 审批 / runId 轮询恢复           │
└───────────────────────────┬───────────────────────────────────────┘
                            │ 同源 /api/*（HTTPS 由 nginx 终结）
┌───────────────────────────▼───────────────────────────────────────┐
│ 单 Go 二进制（cmd/workbench，iris v12）                           │
│  全局中间件：压缩 → 访问日志 → 安全头/CORS                        │
│  路由级：AuthMiddleware（Bearer + X-Org-Id）→ RequireUserManager │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │ internal/server  handler / 沙箱 / 分享 / AI 代理与工具 /     │ │
│  │                 后台 run / 编排执行器装配                    │ │
│  └───────┬───────────┬──────────┬──────────┬──────────┬────────┘ │
│  ┌───────▼──┐ ┌──────▼─────┐ ┌──▼───────┐ ┌──▼──────┐ ┌──▼─────┐│
│  │internal/ │ │internal/rag│ │ schedule │ │mcpclient│ │pipeline ││
│  │   db     │ │ 索引+检索  │ │cron+Bus  │ │JSON-RPC │ │声明式编排││
│  └───────┬──┘ └──────┬─────┘ └──┬───────┘ └─────────┘ └─────────┘│
│          │           │          │ notify（邮件订阅者）            │
│  ┌───────▼───────────▼──────────▼───────────────────────────────┐ │
│  │ SQLite 单文件：业务表 + ai_runs + pipeline_runs/templates │ │
│  │                + schedule_triggers + vec0 + FTS5             │ │
│  │ sqlitevec 静态扩展 / sqlite_fts5 build tag                    │ │
│  └───────────────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │ embed.go：//go:embed frontend/dist（UI 静态资源）            │ │
│  └───────────────────────────────────────────────────────────────┘ │
└───────────────────────────┬───────────────────────────────────────┘
                            │ 可选外部依赖
     OpenAI 兼容 chat 网关 · /embeddings · SMTP 587 · stateless HTTP MCP
```

**两条存储通道刻意解耦**：UI 资源来自编译期 embed 的 `frontend/dist`（运行时只读），用户文件来自 `server.static_dir` 磁盘目录（按 `orgName/userName` 分根），不共用路径空间。

### 2.2 请求生命周期

`Server.App()` 注册 3 个全局中间件，顺序固定：

| # | 中间件 | 职责 | 跳过路径 |
|---|---|---|---|
| 1 | `compressionMiddleware` | `ctx.CompressWriter(true)`，按 `Accept-Encoding` 协商 gzip/deflate/br | `/api/media`、`/api/ai/chat` |
| 2 | `loggingMiddleware` | `ctx.Record()` + 写 `preview.log` | 同上 |
| 3 | `securityHeadersMiddleware` | `nosniff` / `SAMEORIGIN` / `no-referrer`；`/api/*` 加 `Access-Control-Allow-Origin: null` | `/api/*` 的 OPTIONS 直接 204 |

压缩与 `Record` 必须同时跳过 SSE（逐行透传被压缩缓冲破坏）与媒体（Range 与压缩语义冲突）。鉴权不是全局中间件：`AuthMiddleware` 逐条挂在 `api` 分组路由上，`/api/admin` 再加 `RequireUserManager`。限流同样是 handler 内查 `rate_limits`（登录失败 5 次/分钟/IP、分享密码 5 次/5 分钟，DB 故障 fail-open）。

### 2.3 关键设计原则

- **单二进制、零外部中间件**：HTTP、调度、事件分发、向量检索、编排执行全在进程内；外部服务都是可选增强，缺失时降级关闭。
- **服务端权威**：路径、权限、版本号、索引状态、cron 合法性、编排写授权一律服务端判定。
- **身份维度贯穿**：`org_id + user_id` 是存储隔离键，也是 vec0 的 `owner` 分区键；文件根用 `orgName/userName` 拼接，构成「整数 id 存储 / 名称路径」双轨。
- **可用性优先于完整性**：RAG 检索、FTS、embedding、judge、MCP 失败均静默降级；对话主链路不被检索与通知拖垮。
- **长任务与请求解耦**：后台 run 与编排运行都把状态逐步落库，客户端断开不影响执行结果，靠轮询/查询恢复。
- **写操作显式确认**：AI 写类工具、外部 MCP 工具、编排流程走 Human-in-the-loop 审批，审批绑定发起用户且一次性。

## 3. 模块划分

### 3.1 后端 `internal/`

| 包 | 职责 | 关键文件与符号 | 主要依赖 |
|---|---|---|---|
| `config` | YAML 结构、默认值、非零值合并、环境变量覆盖、`~/` 展开 | `config.go`：`Config`、`DefaultConfig`、`Load`、`mergeConfig`、`ApplyEnv`、`ResolvePath` | yaml.v3 |
| `db` | SQLite 连接/迁移、RBAC、任务、分享、会话、AI job、后台 run、编排运行/触发规则、RAG 表读写、密钥与限流 | `db.go`（`Open`/`TaskItem`）、`migrate.go`（`detectBaseVersion`）、`users.go`、`tasks.go`、`shares.go`、`ai_sessions.go`、`ai_runs.go`、`pipeline_runs.go`、`cron_triggers.go`、`time_reminders.go`、`rag.go`（`EnsureVecIndex`/`EnsureFTS`）、`secrets.go` | mattn/go-sqlite3、golang-migrate、sqlitevec |
| `server` | iris 路由与全部 handler、鉴权、沙箱、AI 代理/Agent/审批、后台 run、编排与通用触发的装配层 | `server.go`（`Server`/`New`/`App`/`safeJoin`）、`auth.go`、`ai.go`（`handleAIChat`/`aiRunWriter`/`handleAIRuns*`）、`aitools.go`、`aitools_mcp.go`、`aiapproval.go`、`pipeline.go`（`runPipeline`）、`pipeline_http.go`、`aijob_target.go`（`ResolveTarget`/`ValidateJobTarget`）、`aijob_exec.go`、`aijob_trigger.go`、`schedule_triggers.go`（`supportedTriggerTargets`/`validateTriggerTarget`）、`fsops.go`、`upload.go`、`media.go`、`share.go`、`admin.go`、`org.go`、`codes.go`、`response.go` | iris、db、rag、schedule、notify、mcpclient、pipeline、UIFS |
| `rag` | 文件索引与混合检索、LLM 相关性预判与摘要卡 | `service.go`（`Service`/`IndexFile`/`ReindexTree`/`Search`）、`chunk.go`、`embedding.go`、`judge.go`、`extract.go` | db、OpenAI 兼容 HTTP |
| `schedule` | 手写 cron 引擎、进程内事件总线、通用定时触发分发 | `scheduler.go`（`ParseCron`/`Scheduler`）、`bus.go`（`EventBus`）、`jobs.go`（`KnownTopics`）、`triggers.go`（`NewTriggersJob`/`TriggerHandler`/`runCronTriggers`/`ClaimCronTrigger`） | db、标准库；包内无业务对象类型 |
| `notify` | 订阅事件总线并发邮件 | `email.go`（`SubscribeEmailNotifier`）、`email_template.go`（`mdToHTML`/`safeHref`）、`email_helper.go` | net/smtp、schedule、db |
| `mcpclient` | MCP Streamable HTTP 最小客户端 | `client.go`（`initialize` → `notifications/initialized` → `tools/list` / `tools/call`） | net/http |
| `pipeline` | **声明式编排执行引擎**（DAG 分层 + 同层并行），不依赖 HTTP/iris/db | `pipeline.go`：`Definition` / `Step`（`Needs`/`When`/`Retry`）/ `StepResult` / `Layers()` / `RunContext.Interpolate` / `Runner.Run` / `Executor` | 仅标准库 |
| `sqlitevec` | 注册 sqlite-vec 静态扩展、向量序列化 | `vec.go`（`Auto`/`SerializeFloat32`）+ vendor C 源码 | cgo |

顶层还有 `embed.go`（`UIFS`）、`cmd/workbench/main.go`、`cmd/probedb`、`cmd/probeui`、`scripts/ragclean.go`（运维工具，不参与主服务启动）。

### 3.2 前端 `frontend/src/`

| 目录 | 职责 | 代表文件 |
|---|---|---|
| `api/` | 按域封装 HTTP 与 SSE | `ai.ts`（`chatStream`/`listAIRuns`/`getAIRun(id, offset)`）、`pipelines.ts`、`scheduleTriggers.ts`、`auth.ts`、`files.ts`、`share.ts`、`tasks.ts`、`aiJobs.ts`、`admin.ts` |
| `lib/` | 请求/渲染/工具层 | `common.ts`（`authHeaders`/`apiCall`/401 拦截）、`markdownRenderer.ts`、`ragNav.ts`、`modalEsc.ts`（`escStack`/`hasOpenModal`） |
| `stores/` | 跨组件状态与身份作用域清理 | `auth.ts`、`indexStore.ts`（`resetIdentityScopedState`）、`todoStore.ts` |
| `composables/` | 组件无关业务逻辑 | `useAIChat.ts`（`ChatSession`/`aiChatKey`/`PendingRun`/SSE/审批/会话/**`pollRun`/`resumePendingRuns`/`resumeOne`**）、`todoSync.ts`、`useTodoApp.ts` + `todo/*`、`useTreeStore.ts` |
| `components/` | 展示与交互，按域分目录 | `ai/`（`AiMessage.vue`/`PipelineTriggerCard.vue`）、`pipeline/PipelineGraph.vue`、`sidebar/PipelineSidebar.vue`、`editor/`、`markdown/`、`modal/`、`tab/`、`todo/` |
| `views/` + `views/mobile/` | PC 与移动两套独立视图 | `IndexView`、`TodoView`、`PipelineView`、`AdminView`、`AiJobsView`；`MobileLayout`、`MAI`、`MTodo`、`MShareView` 等 |
| `router/` / `layouts/` / `styles/` | 路由与设备分流 / PC 外壳 / 全局样式 | `router/index.ts`、`DefaultLayout.vue`、`style.css` |

**PC 与移动端共享业务层、只拆视图层**：`useAIChat`、`FileViewer`、`ModalShell`、`api/*` 两侧复用，断连恢复逻辑也由同一 composable 承担。

### 3.3 依赖关系

```
cmd/workbench/main.go
  └─ config ─────────────┐
  └─ db ──┬─ sqlitevec   │
          └─ migrations  │
  └─ server ─┬─ db ──────┤
             ├─ config ──┘
             ├─ rag ───────── db / embedding 网关
             ├─ schedule ──── db / bus（handler 由 server 装配注入）
             ├─ notify ────── schedule.EventBus / db / SMTP
             ├─ mcpclient ─── 外部 MCP HTTP 服务
             ├─ pipeline ──── 仅标准库；Executor 由 server 注入
             └─ workbench.UIFS（embed）
```

| 类别 | 依赖 |
|---|---|
| Go 核心 | iris v12.2.11、mattn/go-sqlite3 v1.14.53、golang-migrate v4.19.0、yaml.v3、`golang.org/x/crypto`、`golang.org/x/net`、`google/uuid` |
| 前端核心 | Vue 3.5、vue-router、Element Plus、marked、DOMPurify、highlight.js、Vite 5、TypeScript |
| 外部（可选） | OpenAI 兼容 `/chat/completions`、兼容 `/embeddings`、SMTP 587 STARTTLS、stateless HTTP MCP |
| 构建期 | Go 1.25+、`CGO_ENABLED=1`、tag `sqlite_fts5`、Node 18+/pnpm、xgo+Docker（跨平台） |

## 4. 启动流程与装配

### 4.1 `cmd/workbench/main.go` → `run()`

| 步 | 动作 |
|---|---|
| 1 | 默认 JSON `slog`；解析 `-config`（默认 `config.yaml`） |
| 2 | `config.Load` → 按 `logging.level` 重建 logger → `config.ApplyEnv` |
| 3 | `ResolvePath` 展开 `staticDir`/`dbPath`/`logDir` 的 `~/`；`MkdirAll` |
| 4 | `db.Open`：注册 sqlite-vec → WAL + `busy_timeout=5000` + `foreign_keys=on` + `SetMaxOpenConns(1)` → 执行 20 个内嵌迁移 |
| 5 | `db.LoadOrCreateSecret("token", ...)` 取/生成 32 字节签名密钥 |
| 6 | 无用户且未配置 `ADMIN_SETUP_TOKEN` 时打印抢占窗口告警 |
| 7 | `server.New(...)`（见 4.2）→ `srv.App()` 注册路由与中间件 |
| 8 | `app.Run(iris.Addr(":"+port), iris.WithoutServerError(iris.ErrServerClosed))` 阻塞监听 |

`defer database.Close()` 是唯一确定的清理动作。

### 4.2 `server.New` 子系统装配

| 序 | 动作 | 失败语义 |
|---|---|---|
| 1 | 解析并创建 `static_dir` 绝对路径与 `{static_dir}/_admin` | 返回 error |
| 2 | `initMCPTools()` 拉取各 MCP 服务 `tools/list` 转静态 `aiTool` 表 | 配置缺 `name`/`url` fail fast；不可达仅告警跳过 |
| 3 | `schedule.NewEventBus()` → `notify.SubscribeEmailNotifier(...)` | `enabled=true` 且缺 `host`/`user` 返回 error |
| 4 | `schedule.enabled`：`time.LoadLocation` → `NewScheduler` → AddJob **四个** job（含 `cron_triggers_runner`）→ `Start` | 时区非法回退 UTC；cron 非法 fail fast |
| 5 | `embedding.Enabled()`：`EnsureVecIndex(dim)` → `EnsureFTS()` → `rag.NewService` | vec 建表失败 RAG 关闭；FTS 失败降级纯向量 |

`Server` 持有的跨请求状态：`aiApprovals`（审批 hub）、`scheduler`、`bus`、`rag`、`mcpTools`、`tokenSecret`。`New()` 提前创建 `iris.Application`，并把 `triggerPipelineTemplate` 以 `TriggerHandler` 注册进 `NewTriggersJob` 的 `target_type` 分发表，使 `schedule` 包只认通用规则行、不 import 任何业务对象类型。编排工具**不进 `aiTools` 静态表**：`pipelineTool()` 由 `allTools()` 在运行时合成，用于规避「`aiTools` → `execRunPipeline` → `execAITool` → `aiToolByName` → `aiTools`」的包级变量初始化环。

### 4.3 生命周期约束

- 仓库源码无 `signal.Notify` / 自定义 `Shutdown`；`Scheduler.Stop()` 存在但无调用点，job 均为短扫描。不要把「SIGTERM 优雅关闭」当作既有能力。
- `EventBus.Publish` 为每个 handler 起独立 goroutine，事件 ctx 不能绑定发布方请求生命周期：AI 定时任务与 `notify_user` 工具都刻意用 `context.Background()` 发布（`send` 自带 30s 独立超时）。
- **后台 run 的状态不跨进程重启恢复**：循环绑定当前进程，重启后 `ai_runs` 中残留的 `running` 记录不会续跑（前端靠 30 分钟过期保护与列表兜底处理）。
- 升级 `mattn/go-sqlite3` 后必须 `make sync-vec-headers` 保持 vendor 头文件与实际链接版本一致。

## 5. 关键流程

### 5.1 认证与组织上下文

```
POST /api/login {userId,password,orgId?}
  → 登录失败限流（rate_limits）→ FindUserByName / FindUserByGlobalName
  → VerifyPassword（bcrypt；失败时兼容旧 SHA-256 无盐 hex）
  → ResolveUserOrgIdentity → GenerateToken
      payload "<userID>:<expiryUnix>" + HMAC-SHA256 签名 → base64.URLEncoding
  → {token, user{...}}

每个受保护请求
  → Bearer 解码 → hmac.Equal 校验签名 → 检查 expiry
  → X-Org-Id（缺省 0）→ ResolveUserOrgIdentity 校验 status='active'
  → 写入 ctx：userID / orgID / userName / orgName / orgRole / isPlatformAdmin / role
```

`externalRole`：平台超管 → `admin`，组织 `owner`/`admin` → `org_admin`，其余 → `user`。token **不固化 orgID**（组织切换无需重新签发），代价是每请求一次身份查询。首用户初始化见 `handleSetPassword`（`AdminSetupToken` 用 `subtle.ConstantTimeCompare` 校验）。

### 5.2 文件路径沙箱与写操作

```
userRoot(ctx) = {serDirAbs}/{orgName}/{userName}/
  └─ safeJoin：Clean → Join → Rel 校验，拒绝 ".."/"../"
      └─ resolveUserPathSafe：AllowSymlink=false 时 EvalSymlinks(fsPath) 与
                              EvalSymlinks(root) 双重校验真实路径
      └─ resolveFsEntrySafe：条目本身是软链接时只操作链接，不跟随目标
```

所有文件 handler、分享（`userRootByOwner` 反查 name）、AI 工具路径复用该链路。RAG 写钩子挂在操作之后：`HandleWrite`（异步，上传/AI `write_file`）、`HandleDelete`（同步，目录按前缀删）、`HandleRename`（重命名/移动）；`mkdir` 无钩子。删除钩子清 `file_chunk_vec` / `file_chunk_fts` / `file_chunks` / `file_index_state` 四表，细节见 [`05-operations.md`](05-operations.md#52-删除文件时会清理哪些-rag-数据)。

### 5.3 AI 对话：Agent 循环与后台 run

```
POST /api/ai/chat（SSE）
  → 校验 messages（≤30）/ body（512KB）/ qaId、sessionId
  → 创建后台 run：runCtx = context.Background() + 15min 总超时，
     写入 ctx.Values()[aiRunCtxKey]；db.CreateAIRun(status=running)
  → aiRunWriter 包装 ResponseWriter（首次写失败 → disconnected，
     后续写入静默丢弃，循环继续）；header 就绪先推 run_id 事件
  → RAG 前置检索（8s 超时静默降级）→ 编号片段注入 → rag_hits
  → 循环 round = 0..5：
       上游 /chat/completions（stream=true, include_usage=true），
       upCtx 由 runCtx 派生（10min）；tools 仅在 tools=true 且未达上限轮下发
       forwardAIStream 逐行透传；tool_calls 分片按 index 累积
       无 tool_calls → 补 [DONE]；有 → tool_call/tool_result → 回传继续
       达上限轮：不下发 tools + system 提示「基于已有结果直接回答」
  → 增量落库（aiRunWriter 按 300ms maybeFlush 写 result + defer 最终态 done/failed）
  → usage 事件（多轮累加）→ [DONE]
```

工具执行统一经 `aiToolRunCtx` 取 `runCtx`，断连后 DB/RAG 操作仍可继续。写类工具走 `runToolWithApproval`：`approval_request` → 三路 `select`（用户决定 / 5 分钟超时 / 客户端断开）→ `approval_resolved`；**断连后写类工具无人确认，直接跳过并回传说明性错误**让模型收尾。审批 ID 复用上游 `tool_call.ID`，`aiApprovalHub` 校验绑定用户、一次性、作废语义，`POST /api/ai/approval` 对不存在/已处理/非本人统一 404。

### 5.4 前端断连恢复（1b）

```
chatStream 分发 run_id → useAIChat 把 PendingRun 存 sessionStorage
  ├─ 正常收到 [DONE] → onDone → 常规收尾，清 runId
  ├─ 静默断流（reader 正常结束但无 [DONE]）→ 保持 streaming → pollRun
  │    每 300ms GET /api/ai/runs/{id}?offset=<游标>，上限 15min
  │    服务端 splitRunResult 按 offset 切出 delta → 续写到对应 ChatSession
  │    的消息并推进 offset；工具/引用/usage 按 id 补齐
  └─ 用户主动停止 / 服务端业务错误（error.business）→ 原有收尾
     刷新/网络中断的 ReadableStream 原生异常判为可恢复，保留 pending 记录

刷新 / 打开面板 → maybeRestoreLastSession → resumePendingRuns()
    sessionStorage 的 PendingRun[] 按 ChatSession 归属逐个 resumeOne
    （含已完成遗留，30min 过期保护）
    本地丢失 → GET /api/ai/runs 找仍 running 的兜底
```

PC 面板与移动端共用 `useAIChat` 与 `AiMessage.vue`（经 `aiChatKey` provide/inject 各自实例），恢复逻辑只落在 composable 里；多个会话可同时生成并各自恢复。

### 5.5 RAG：索引与混合检索

```
索引（写钩子 / reindex）
  Indexable（24 类扩展名、≤2MiB、非隐藏）→ HTML 剥标签 → SHA-256 判重
  → SplitChunks（空行分段，800 字 + 100 字重叠，1-based start_line）
  → [可选 digest 摘要卡 chunk_index=-1] → Embedder（批 16、降维、L2 归一化）
  → ReplaceFileIndex 事务：删 vec/fts/chunks → 写三表 → upsert file_index_state
  → 失败记 error 状态，下次钩子/重建重试

检索（Search，8s 预算）
  query ≤2000 字 → 向量 KNN(topK*4，owner 分区，max_distance 过滤)
                 → FTS bm25(topK*4，trigram，query≥3 字)
  → RRF score = Σ 1/(60+rank) → topK（对话 5）
  → [可选 judge_model 批量预判，失败保留原结果]
  → ContextPrompt 编号 [1]..[n] 注入 + rag_hits 事件
```

`EnsureVecIndex(dim)` 双信号判定（meta 一致 **且** vec 表存在），否则 DROP 重建并作废 `file_index_state`；`EnsureFTS` 表缺失时重建回填。**`ReindexTree` 在收集完文件后先按 owner 清孤儿索引**：`db.ListIndexedPaths` 取该用户全部已索引路径 → `orphanPaths` 用 `os.Stat` 判「文件已不存在」 → `DeleteFileIndex` 事务清四表，计数写入 `ReindexStats.Pruned` / 进度 `pruned` / 完成日志。刻意**不用「本次遍历文件差集」**：`collect` 遇 `os.ReadDir` 失败只记 warn 并跳过子树，差集会误删整棵仍存在文件的索引；清理失败只记 warn 不中断重建。详见 [`05-operations.md`](05-operations.md#5-rag-索引运维)。

### 5.6 Todo：revision 乐观锁

```
GET /api/tasks → {revision, tasks}（content → 80 字 contentPreview）
写路径 PATCH/POST/DELETE /api/tasks/{id} 携带 baseRevision
  → 与 users.revision 比对：不一致 409 + 40901 + 服务端任务副本
  → 一致：单条写入 + revision +1 → 返回新 revision
前端 todoSync：30s GET /api/tasks/revision 轮询 + 回前台对账；
             409 → 差异字段对比卡（POST/DELETE 直接重拉）
```

`db.UpdateTask` 是全字段覆盖写，故 `patchTask` 探测请求体中 `task.content` 是否存在：缺失回填服务端现值，显式空串才是主动清空。

### 5.7 分享访问

`/s/{token}` 是公开路由，后端校验分享存在后返回 embed 的 `index.html`；前端切 `dataSource.mode='share'`，数据走 `GET/POST /api/share/{token}`。`handleShareData` 统一校验时间窗、次数、bcrypt 密码哈希，按实际 `os.Stat` 返回目录列表或文件元信息（含 base64 内容）。`40304`/`40305` 触发前端密码弹窗循环，密码存 `sessionStorage`。

### 5.8 定时任务与事件投递

```
Scheduler（整分钟 ticker，Vixie cron bitset 匹配）
  ├─ todo_due_scan      cron = schedule.todo_due_cron（默认 */5 * * * *）
  │    ScanDueTasks → MarkNotified(INSERT OR IGNORE) → task.due
  ├─ todo_time_reminder cron = * * * * *
  │    ScanTimeReminders → MarkNotified → task.reminder（start / due）
  └─ cron_triggers_runner cron = * * * * *
       ListEnabledCronTriggers → Matches && last_run_at != 当前分钟
       → ClaimCronTrigger(nowKey, leaseUntil) CAS 占租约
         （同分钟不重跑；超过 1 分钟的长任务租约未过期不会被重复触发）
       → handlers[target_type]（15min 独立 runCtx，状态回写用独立 ctx）
            job               → triggerJob：读 ai_jobs → ResolveTarget/Validate
                                → runPipeline → MarkAIJobRunDetail → user.notify
       → MarkCronTriggerRun(ok / "error: ...", nowKey)（同时清租约）

EventBus.Publish → 每 handler 独立 goroutine（panic recover）
  → EmailNotifier.onTaskDue / onTaskReminder / onUserNotify
  → GetUserEmail（空跳过）→ SMTP STARTTLS → multipart/alternative
```

`KnownTopics`：`task.due`、`task.reminder`、`user.notify`（仅 `user.notify` 允许 AI 发布）。全局公共任务（`user_id IS NULL`）的 job 规则用 `org_id=0 / user_id=0` 标记，`triggerJob` 内按 `ListUsersWithEmail` 展开为每用户独立执行与投递；规则行的 `last_run_at` 记录本次触发。`cron_triggers_runner` 不走事件总线：只按 `target_type` 调装配层 `TriggerHandler`，未注册类型或非法 cron 记 `error: ...` 状态并跳过，不影响其他规则。

### 5.9 声明式编排与定时触发

三条执行入口共用 `runPipeline(ctx, org, user, runID, def)`（`server/pipeline.go`）：

```
run_pipeline 工具（一次界面确认） / POST /api/pipeline/run（直调 Definition，
                                    供 /jobs 任务详情页「立即执行」使用）
triggerJob（定时任务：读 ai_jobs.target_json 里的内联 Definition，
           ResolveTarget 归一化后进入同一核心）
  → db.CreatePipelineRun(status=running)
  → Definition.Validate：步骤非空、≤50、id 唯一合法、uses/when/retry 合法、
    needs 引用存在且无环
  → Definition.Layers() 拓扑分层，同层并行（maxParallelSteps=4）
       withToolContext 从 app.ContextPool 合成 iris.Context 预置 userID/orgID
       （并经 db.GetUserNames 反查补齐 userName/orgName——真实请求由 auth
       中间件写入这两个 name，userRoot 拼用户目录依赖它们），
       Request().Context() 绑后台 run ctx（工具层零改动）
       RunContext.Interpolate 在启动 goroutine 前替换 {{ steps.<id>.output }}
       when 判定：缺省 on_success；on_failure / always 作补偿步骤
       Executor 按 uses 分派：
         tool/<name> → execAITool（复用内置/MCP 工具）
         api/<path>  → 站内 /api/*，自签短期 token + X-Org-Id，
                        经 app.ServeHTTP 内部调用
         llm         → aiChatOnce 非流式
       execWithRetry：retry ≤5、固定退避 200ms×次数、每步 5min 超时
  → 每层结束 onStep 落一次 pipeline_runs 全量快照；主流程失败即停
  → 工具只回步骤摘要；直调返回 runId；完整输出 GET /api/pipeline/runs/{id}
  → GET /api/pipeline/runs/{id}/stream（SSE）：1s 轮询 pipeline_runs 比对
    status/steps/err_msg 变化推 progress（整份快照），终态推 end 后关闭；
    15s 心跳注释行防代理掐断，连接寿命上限 = 执行总超时 + 1min
```

**领域解耦**：编排 Definition 内联在 `ai_jobs.target_json` 里（`000027` 删除独立模板表与版本表）；`ai_jobs` 只存执行定义（`target_type` / `target_json` / `allow_write` / context / notify，000023 删掉 4 个调度列），cron / 启停 / 最近状态由 `target_type='job'` 的 `schedule_triggers` 规则提供。`db` 层不解析 Definition。`schedule.NewTriggersJob` 只按 `target_type` 分发，`supportedTriggerTargets` 白名单只登记 `job`，目标是否存在与归谁所有由 `server/schedule_triggers.go` 的 `validateTriggerTarget` 在装配层校验（创建后目标绑定不可变）；删除任务经 `DeleteCronTriggersForTarget` 清理孤儿规则。`ai_jobs` / `pipeline_runs` / `schedule_triggers` 都按 `org_id + user_id` 隔离，跨身份读任务返回 `40401`。

安全模型：`allowWrite` 必须显式列出写类工具与非 GET 的 api 步骤，否则该步失败；`api/<path>` 限定站内 `/api/*`，不能外访。定时执行无人审批，创建 / 更新时即做 Definition 级预检，并拒绝 `tool/run_pipeline`、任务自修改工具、`api/ai/chat`、非 GET `api/ai/jobs`、`api/pipeline/*`。`internal/pipeline` 不依赖 HTTP/iris/db，`Executor` 由 server 注入，可独立单测。

### 5.10 SPA 路由与深链兜底

前端用 `createWebHistory()`，深链由后端兜底：`handleStaticWithAuth` → `handleRouteRedirect`，`routes` 为 `__spa__` 时返回 embed 的 `index.html`，`__listdir__` 时列目录。**新增前端路由必须同步在 `config.yaml` 的 `routes` 注册**，否则刷新该 URL 得到 401。定时任务页 `/jobs` 不进 `PC_TO_MOBILE` 映射，窄视口走响应式 PC 布局；编排流程已并入该页，无独立 `/pipelines` 路由。

## 6. 数据模型

### 6.1 迁移体系

`internal/db/migrations/` 用 golang-migrate，`//go:embed migrations/*.sql` 内嵌，每版本一对 `.up.sql` / `.down.sql`。`db.migrate()` 用 `sqlite3.Config{NoTxWrap:true}`（PRAGMA 在事务内无效）；存量库无 `schema_migrations` 但有 `users` 时 `Force(1)` 跳过 baseline。禁止手写 `BEGIN`/`COMMIT`；仅 baseline 允许 `IF NOT EXISTS`。

| 版本 | 主题 | 版本 | 主题 |
|---|---|---|---|
| 000001 | baseline | 000010 | 全局公共 AI 任务 |
| 000002 | RBAC 多组织 | 000011 | `ai_job_runs` |
| 000003 | 删遗留列 | 000012 | 删 `visit_logs` |
| 000004 | `user_org_feature` | 000013 | RAG 索引三表 + `rag_meta` |
| 000005 | `tasks.files` | 000014 | `file_chunks.start_line` |
| 000006 | `ai_sessions` | 000015 | FTS5 混合检索 |
| 000007 | `users.revision` | 000016 | `ai_runs`（后台对话运行） |
| 000008 | 用户邮箱 + `notify_log` | 000017 | `pipeline_runs`（编排运行） |
| 000009 | 任务精确时刻 + `ai_jobs` | | |
| 000018 | `pipeline_templates`（初版含 `cron_expr` / `enabled` / `last_run_at` / `last_status`） | | |
| 000019 | `schedule_triggers` 通用规则表，并从模板 `cron_expr <> ''` 回填已有配置 | | |
| 000020 | 模板表纯化：建新表 → 复制 → 删旧表 → 改名，删掉 4 个调度列 | | |
| 000021 | AI 会话消息表（`ai_session_messages`，行级消息） | 000022 | `ai_jobs` 执行对象：target 五列 + `prompt` 回填 |
| 000023 | 调度收敛：`ai_jobs` 去 4 个调度列 + `schedule_triggers.lease_until` + job 规则回填 | 000024 | `ai_job_runs` 详情列（run id / steps / 耗时） |
| 000025 | `ai_jobs.retry`（job 级失败重试，0-3，仅无写步骤生效） | | |
| 000026 | `pipeline_template_versions`（模板版本历史，历史迁移保留） | 000027 | 删除模板两张表 + 清理 `pipeline_template` 孤儿触发规则；编排 Definition 改为内联在 `ai_jobs.target_json` |

000020 是 SQLite 无 `DROP COLUMN` 时的标准重建写法；其 `.down.sql` 建回 `pipeline_templates_legacy`，按 `target_type='pipeline_template'` 从 `schedule_triggers` 取最近一条反向回填调度列。`000018`–`000026` 的历史脚本保留不动以维持新建库迁移链，`000027` 在运行时删表；其 `.down.sql` 只重建空表，模板数据不可恢复。

### 6.2 现行表清单（22 项，含虚拟表与框架表）

| 组 | 表 | 关键列 / 约束 |
|---|---|---|
| 组织权限 | `orgs`、`roles`、`users`、`user_orgs`、`user_org_feature`、`app_secrets`、`rate_limits` | `users` 含 `is_platform_admin`/`revision`/`email`；`user_org_feature` 唯一 `(user_id,org_id,feature_code)` |
| 业务 | `tasks`、`shares`、`notify_log` | `tasks.PK(id,user_id,org_id)` + `files`/`scheduled_time`/`due_time`；`notify_log` 唯一 `(topic,dedup_key,notify_date)` |
| AI 会话调度 | `ai_sessions`、`ai_jobs`、`ai_job_runs` | `ai_sessions.messages` 为前端快照 JSON；`ai_jobs` 只存执行定义（`target_type`/`target_json`/`allow_write`/context/notify/`retry`，无调度列）；`ai_job_runs` 含 `target_type`/`pipeline_run_id`/`steps_summary`/`err_msg`/`duration_ms`/`finished_at` |
| **后台运行** | `ai_runs` | `id TEXT PK`、org/user、`qa_id`/`session_id`、`status running/done/failed`、`events JSON`、`result`、`err_msg`、时间戳；索引 `(org_id,user_id,started_at DESC)` |
| **编排运行** | `pipeline_runs` | `id TEXT PK`、org/user、`name`、`definition JSON`、`status`、`steps JSON`、`err_msg`、时间戳；同款 owner 索引；Definition 由任务 `target_json` 内联提供 |
| **通用触发规则** | `schedule_triggers` | `id`、org/user、`target_type`（当前仅 `job`）、`target_id`、`cron_expr`、`enabled`、`last_run_at`/`last_status`、`lease_until`、时间戳；索引含 owner、enabled 与 `(target_type,target_id)` |
| RAG | `file_index_state`、`file_chunks`、`rag_meta`、`file_chunk_fts`、`file_chunk_vec` | `file_chunks.chunk_index=-1` 为摘要卡；FTS5 `tokenize=trigram`，`rowid = file_chunks.id`；vec0 的 `owner = "orgID:userID"` 为分区键，**运行时由 `EnsureVecIndex` 创建，不在迁移脚本中** |
| 框架 | `schema_migrations` | golang-migrate 维护 |

已删除的历史表：`visit_logs`（000012）、`org_features`（000004 被 `user_org_feature` 取代）、`pipeline_templates` 与 `pipeline_template_versions`（000027，编排模板概念移除）。

### 6.3 关系与约束要点

```
orgs ─< user_orgs >─ users ─< tasks / ai_sessions / ai_jobs / shares
                        └─< ai_runs / pipeline_runs
                             / schedule_triggers（org_id+user_id 隔离）
user_orgs ─< user_org_feature
ai_jobs ─< ai_job_runs
ai_jobs ─< schedule_triggers（target_type='job' + target_id；逻辑关联，无外键）
RAG 五表 ← 按 (org_id,user_id) 分区，vec0 用 owner PARTITION KEY
```

- 外键 `PRAGMA foreign_keys=on`，WAL + `busy_timeout=5000` + `SetMaxOpenConns(1)` 串行写。
- 存储层整数 id / 磁盘路径 `orgName/userName` 双轨，改名需同步迁移目录。
- `users.revision` 是所有任务写入的单调版本号；`tasks.files` 为 JSON 数组，空关联归一 `nil` + `omitempty`。
- `notify_log` 去重在 **Publish 之前**用 `INSERT OR IGNORE` + `RowsAffected` 原子判定，SMTP 失败不重投；`user.notify` 不去重。
- `ai_runs` / `pipeline_runs` / `ai_jobs` / `schedule_triggers` 均按 org+user 隔离；列表不含大字段（events / steps / definition 由详情接口返回）；任务详情跨身份访问返回 `40401`。

## 7. 接口设计

### 7.1 统一约定

| 项 | 约定 |
|---|---|
| 响应 | `Response{code,msg,data}`；`writeOK`/`writeFail`/`writeFailMsg`/`writeFailData`/`serverError` 收敛样板 |
| 错误码 | `HTTP 状态码 × 100 + 序号`，`codes.go` + `errMsgMap`；`40901` 冲突、`40902` 重建中、`50301` AI 未配置 |
| 认证头 | Bearer + `X-Org-Id`；`/api/admin/*` 额外 `RequireUserManager` |
| CORS | `/api/*` 固定 `Access-Control-Allow-Origin: null`，不设 `Allow-Credentials` |
| 流式 | `/api/ai/chat`（SSE）、`/api/media`（Range）跳过压缩与 `ctx.Record()` |

### 7.2 路由全量清单

`api := app.Party("/api")`，下表省略前缀。公开路由无 Bearer；其余需有效 token + 组织上下文。

**公开**：`POST /api/login`、`POST /api/set-password`、`GET|POST /api/share/{token}`、`GET /api/media`（2h HMAC ticket）、`GET /s/{token}[/{p:path}]`、`GET /{path:path}`（静态兜底，`isPublicEntry` 放行 `/`、UI 资源与 `routes` 键）、`/api/*` 的 OPTIONS（全局中间件直接 204）。

**认证与组织**：`/api/me`、`/api/userinfo`、`/api/org/switch`(POST)、`/api/org/features`、`/api/profile/email`(POST)、`/api/map`、`/api/tree`、`/api/org-members`。

**任务**：`GET /api/tasks`、`GET /api/tasks/revision`、`GET /api/tasks/{id}`、`GET /api/tasks/user/{id}`、`GET /api/tasks/by-file`、`GET /api/tasks/open`、`PATCH|POST|DELETE /api/tasks/{id}`。

**文件与分享**：`GET /api/file`、`GET /api/media/ticket`、`POST /api/upload`、`POST /api/mkdir`、`DELETE /api/file`、`POST /api/rename`、`POST /api/move`、`GET|POST /api/share`、`DELETE /api/share/{id}`。

**AI**：

| Method | 路径 | 说明 |
|---|---|---|
| GET | `/api/ai/status` | `{enabled, model, tools}` |
| POST | `/api/ai/chat` | SSE 对话（后台 run + Agent 循环） |
| POST | `/api/ai/approval` | 写工具审批 `{id, approved}` |
| POST | `/api/ai/reindex` | 全量重建（并发 `40902`）；与 `reindex_*` 工具共用 `startReindex` |
| GET | `/api/ai/reindex/status` · `/api/ai/index-status` | 进度 / 文件索引状态 |
| GET | `/api/ai/runs` · `/api/ai/runs/{id}?offset=` | 后台运行列表（不含 events）/ 详情（含 events，按 `offset` 切出 `delta` 与新游标） |
| GET · POST | `/api/ai/jobs` | 定时任务列表 / 创建（`target` 四类执行对象；老 `prompt` 兼容） |
| PUT · DELETE | `/api/ai/jobs/{id}` | 更新 / 删除（同步 `/api/schedule/triggers` 的 job 规则） |
| GET | `/api/ai/jobs/{id}/runs` | 执行历史（含 targetType / pipelineRunId / stepsSummary / durationMs） |
| GET · POST | `/api/ai/sessions` | 会话列表 / 创建 |
| GET · PUT · DELETE | `/api/ai/sessions/{id}` | 会话详情 / 更新 / 删除 |

**编排与通用触发**（8 条，全部带 `auth` 且按 org+user 隔离）：

| Method | 路径 | 说明 |
|---|---|---|
| GET | `/api/pipeline/runs` · `/api/pipeline/runs/{id}` | 运行列表（不含 steps）/ 详情（含逐步结果） |
| GET | `/api/pipeline/runs/{id}/stream` | SSE 实时进度：`progress`（整份运行快照，按 DAG 层推进）→ 终态 `end` 关闭；推送源为 pipeline_runs 表 1s 轮询，非进程内 pub/sub |
| POST | `/api/pipeline/run` | 直调 Definition，后台异步执行，返回 `runId`；任务的编排定义走 `ai_jobs.target_json`，无模板 CRUD 接口 |
| GET · POST | `/api/schedule/triggers` | 规则列表（可按 `targetType`/`targetId` 过滤）/ 创建；`targetType` 仅 `job` |
| GET · PUT · DELETE | `/api/schedule/triggers/{id}` | 规则详情 / 更新 / 删除 |

**管理**（`auth` + `RequireUserManager`）：`GET|POST /api/admin/users`、`PATCH|DELETE /api/admin/users/{id}`、`GET /api/admin/roles`、`GET /api/admin/users/{id}/dashboard`、`GET /api/admin/users/{id}/features`、`PATCH /api/admin/users/{id}/features/{code}`。handler 内另有细粒度约束（组织管理员仅本组织、不能授予 `admin`、禁止删自己或最后一个 admin）。

> 根 `README.md` 仍提到的 `/__map__`、`/__tree__`、`/tasks.json` 未在代码注册，实际为 `/api/map`、`/api/tree`、`/api/tasks*`。

### 7.3 SSE 协议

服务端 `sseWriteEvent` 发出 **8 类**自定义事件，另有 OpenAI 兼容无名增量与最终 `data: [DONE]`：

| event | 载荷 | 时机 |
|---|---|---|
| `run_id` | `{id}` | 流 header 就绪后一次性，供断连恢复 |
| `rag_hits` | `{hits:[{path,content,startLine,distance}]}` | RAG 命中后一次性 |
| `tool_call` / `tool_result` | `aiToolCall{...}` / `{id,name,result}` | 每轮工具调用与结果 |
| `approval_request` / `approval_resolved` | `{id,name,arguments}` / `{id,outcome}` | 写工具等待 / 定格 |
| `usage` | `{prompt_tokens,completion_tokens,total_tokens}` | 最终轮（Agent 多轮累加） |
| `error` | `{msg}` | 流已开启后上游失败；前端重抛时打 `business=true`，与刷新/网络造成的客户端流中断区分 |

前端 `api/ai.ts` 逐行解析 `event:` + `data:`，`chatStream` 把 `run_id` / `onDone` 交给 `useAIChat`；断连后经 `getAIRun(id, offset)` 按 300ms 取 `delta/offset` 增量续写；历史按「轮」预算（展开后 ≤28 条）裁剪以适配后端 30 条上限。

### 7.4 前端请求封装

无 axios / baseURL，端点写同源相对路径；`lib/common.ts` 的 `authHeaders()` 注入 Bearer 与 `X-Org-Id`，`apiCall` 解包信封并对 401 全局跳转（排除 `/api/login`、`/api/me`、`/api/share`——分享密码错误也返回 401）。任务写路径用 `parseMutationResponse` 把 `40901` 增强为带 `serverTask`/`serverRevision` 的 Error。

## 8. 安全模型

| 面 | 机制 |
|---|---|
| 认证 | HMAC-SHA256 Bearer（无状态、不存 Cookie）；bcrypt 密码，兼容旧 SHA-256 仅供存量升级 |
| 授权 | 每请求校验 `X-Org-Id` 归属；`RequireUserManager` + handler 内细粒度约束；功能开关控制入口 |
| 首用户 | `ADMIN_SETUP_TOKEN` 常量时间比较，仅用户表为空时校验 |
| 路径 | `safeJoin` + `resolveUserPathSafe`（`EvalSymlinks` 双校验）+ `sanitizeUploadName`；软链接条目只操作链接 |
| 后台 run / 编排数据 | `ai_runs` / `pipeline_runs` / `ai_jobs` / `schedule_triggers` 按 org+user 隔离；断连后写工具跳过；审批绑定用户且一次性 |
| 编排 | `run_pipeline` 一次确认覆盖整条流程；`allowWrite` 显式预授权；`api/<path>` 限站内 `/api/*`，自签短期 token 带当前身份 |
| 定时任务 | 创建 / 更新时按 Definition 预检 `allowWrite`；执行不进审批分支；拒绝 `tool/run_pipeline`、任务自修改工具、`api/ai/chat`、非 GET `api/ai/jobs`、`api/pipeline/*`；外部 MCP 仅白名单 |
| 通用触发 | 创建规则时校验 `target_type` 白名单（当前仅 `job`）与目标归属，创建后目标绑定不可变；任务 / 规则详情跨身份返回 `40401`，不泄露存在性 |
| 凭据 | AI keys / SMTP 授权码 / MCP header token 走环境变量（`${ENV_VAR}` 展开），不落仓库 |
| 渲染 | Markdown 经 DOMPurify；HTML 预览用无 `allow-same-origin` 沙箱 iframe + null-origin CORS |
| 邮件 | 用户内容 HTML 转义；`safeHref` 只允许 http(s) 且与 `site_url` 同 host |
| 链路头 | `qaId`/`sessionId` 用 `validTraceID` 限长 64 可见 ASCII 防 header 注入 |
| 限流 | 登录失败 5 次/分钟/IP、分享密码 5 次/5 分钟，`rate_limits` 持久化 |
| 已知边界 | token 无法单独吊销（只能轮换密钥）；Bearer 而非 Cookie，无 CSRF 面 |

## 9. 扩展性

| 要新增 | 落点 | 注意 |
|---|---|---|
| REST 接口 | `server/server.go` 的 `App()` + 单独 handler；载荷放 `response.go`；错误码放 `codes.go` | 需 auth 就挂 `AuthMiddleware`；SSE/Range 接口要加入压缩与日志跳过前缀 |
| 数据表 | `internal/db/migrations/NNNNNN_name.up.sql` + `.down.sql` | 每版本一对；禁止 `BEGIN/COMMIT`；不用 `IF NOT EXISTS` 掩盖增量 |
| 内置 AI 工具 | `aitools.go` 的 `aiTools` 表 + `execAITool`；写类设 `NeedApproval` | 路径走 `resolveUserPathSafe`；结果 32KB；当前 16 个工具，更新后同步 `02-functional-guide.md` §8.3 |
| 外部工具 | `ai.mcp.servers` 配置（无需改代码） | stateless HTTP；`mcp_<server>_<tool>` 命名空间；一律审批 |
| 编排能力类别 | `internal/pipeline` 的 `parseUses` + `server/pipeline.go` 的 `Executor` 分派 | 本包保持零外部依赖；新类别需补 `allowWrite` 判定 |
| 通用触发目标类型 | `server/schedule_triggers.go` 的 `supportedTriggerTargets` + `Server.New` 注册 `TriggerHandler` + `validateTriggerTarget` | `schedule` 包只认 `target_type/target_id`；目标归属在装配层校验 |
| 事件 topic | `schedule/jobs.go` 的 `KnownTopics` + 载荷 + 发布方 | 订阅者在 `Server.New` 注册；异步 handler 不依赖发布方 ctx |
| 通知渠道 | 实现 `schedule.Handler` 并在 `Server.New` 订阅 | `Publish` 不等待、panic 隔离；渠道自管去重与失败 |
| 定时 job | `schedule/` 新增 `New*Job` 并在 `Server.New` 中 `AddJob` | cron 非法 fail fast；DB 扫描要短；通用触发复用 `NewTriggersJob`，无需新增 job |
| 前端页面 | `router/index.ts` + `views/` + `api/` + `components/` | **同步 `config.yaml` 的 `routes`**；移动版需加 `/m/*` 与分流映射；`/jobs` 未进映射，走响应式 PC 布局 |
| 预览类型 | `FileViewer.vue` 分派 + `mimeTypeByExt` | 二进制走下载/blob；媒体需 ticket + Range |
| 配置项 | `config.go` 结构体 + `DefaultConfig` + `mergeConfig` + 可选 `ApplyEnv` | 注意非零值合并语义；同步 `config.yaml`、`05-operations.md` 与两份文档 |

## 10. 设计取舍与已知约束

| 决策 | 选择 | 理由 | 代价 / 约束 |
|---|---|---|---|
| 交付形态 | 单二进制 + embed | 部署只需一个文件 | 前端改动必须重编；`make build` 每次重建前端 |
| 数据库 | SQLite 单文件、WAL、单连接 | 零运维、便携 | 写入串行、吞吐有限、不支持多实例共享 |
| SQLite 驱动 | mattn + cgo + sqlite-vec 静态扩展 | 原生 vec0 KNN，向量存库内 | `CGO_ENABLED=1`、xgo 交叉编译、升级需 `sync-vec-headers` |
| 检索 | 向量 KNN + FTS5 trigram + RRF | 专有名词不被距离阈值误杀 | `sqlite_fts5` tag 全局统一；惰性删除需 DROP 重建 |
| 孤儿索引清理 | 按 owner 逐个 `os.Stat` 判「文件已不存在」，而非与本次遍历结果做差集 | `collect` 遇目录读取失败会跳过子树，差集会误删整棵仍存在文件的索引 | 只清真正孤儿；不可索引文件 / 排除目录 / 未跟随 symlink 的旧索引仍需 `ragclean` |
| 调度 | 手写 cron + 进程内 EventBus | 零依赖、纯函数易测、三层解耦 | 单实例、无持久化队列/重试、重启丢事件 |
| 事件分发 | 每 handler 独立 goroutine + panic recover | 发布方不被拖住、故障隔离 | ctx 不能绑定发布方生命周期 |
| 会话 | 无状态 token，`X-Org-Id` 传组织 | 无会话表、支持切换组织 | 不能单独吊销；每请求身份解析 |
| 路径标识 | 整数 id 存储 / 名称路径 | 目录可读、账号隔离 | 改名需同步迁移磁盘 |
| 任务同步 | 服务端 `revision` + 乐观锁 + 30s 轮询 | 增量写、多设备对账 | 1.3.0 起废弃 `PUT /api/tasks`，前后端须同版本 |
| 列表瘦身 | `content` → `contentPreview` + 详情接口 | 省带宽 | `PATCH` 必须实现「缺省保留」语义 |
| **对话与请求解耦** | 后台 run（`runCtx` 15min + `aiRunWriter` 镜像输出） | 断网/关面板不掐断 Agent 循环，结果可查 | 状态不跨进程重启；断连后写类工具跳过；15min 总超时 |
| **断连恢复** | `run_id` + 300ms 游标轮询 `/api/ai/runs/{id}?offset=` | 无需 WebSocket/推送，改动集中在一个 composable，多个会话可并行恢复 | 轮询上限 15min、本地 runId 30min 过期保护；静默断流需与 `[DONE]` 区分；刷新中断判为可恢复，SSE `error` 带 `business` 才是业务错误 |
| **编排 vs 改造 Agent** | 声明式纯数据 `Definition` + 独立 Runner | 模型自主规划多步，Agent 循环零改动；借鉴 GitLab CI 逐步状态、GitHub Actions `uses`/`with`/插值 | Definition 由模型生成需校验；写步骤须 `allowWrite` 预授权 |
| **DAG 分层并行** | `needs` 缺省依赖前一步（默认顺序），显式 `needs` 拓扑分层 + 同层并发 4 | 保留旧定义的顺序语义，同时支持并行；`when`/`retry` 补条件与补偿 | 插值在启动 goroutine 前完成，层内不共享写；每层落一次全量快照 |
| **编排内联在任务里** | Definition 存 `ai_jobs.target_json`（000027 删独立模板表） | 编排只是定时任务的一种可执行对象，AI 直接生成复杂流程，无需模板库与版本历史；调度不绑定业务对象，直调 / 定时共用同一 `runPipeline` | Definition 由模型生成需校验；任务删除需级联清规则 |
| **通用定时触发** | `cron_triggers_runner` + `TriggerHandler` 按 `target_type` 分发 | `schedule` 包零业务依赖，新增领域只需装配层注册 | `ClaimCronTrigger` CAS 租约（同分钟 + 长任务跨分钟防重）、15min 独立超时、无持久化队列 |
| **定时任务执行对象** | `ai_jobs.target` 四类对象（ai / tool / interface / pipeline）归一化为 `Definition`，复用 `runPipeline`；cron 与启停归 `schedule_triggers` | 不为 schedule 写四套执行器，AI 工具 / 站内接口能力直接复用 | 写操作必须静态 `allowWrite`；外部 MCP 仅白名单；`api/*` 限站内；`pipeline` 单次总超时 15min |
| **消息渲染共用** | PC/移动共用 `AiMessage.vue` + `aiChatKey` provide/inject | 消除两套消息模板分叉，审批/重新生成作用于正确会话 | 子组件依赖注入，不能独立调用 `useAIChat` |
| 编排执行器注入 | `internal/pipeline` 不依赖 HTTP/iris/db，`Executor` 由 server 注入 | 可独立单测、职责清晰 | `withToolContext` 需从 `ContextPool` 合成 iris.Context 预置身份 |
| 编排写授权 | 一次审批覆盖整条流程 + `allowWrite` 显式列出 | 不让模型在长流程里绕过确认 | 写步骤未预授权即失败；Definition 由模型生成需校验 |
| 工具表合成 | `pipelineTool()` 由 `allTools()` 运行时合成 | 规避包级变量初始化环（`aiTools → execRunPipeline → aiToolByName → aiTools`） | 工具表分两段，查询统一走 `aiToolByName` 合并表 |
| API 步骤 | 自签短期 token + 内部 `ServeHTTP` | 复用现有 handler 与鉴权语义 | 只能调站内 `/api/*`，不能外访 |
| AI 会话持久化 | 前端 UI 快照 JSON 存 `ai_sessions.messages` | 刷新/断网保住半截回复 | 服务端不理解消息语义；按「轮」预算裁剪 ≤28 条；快照 ≤256KB；后台 run 正文另按 300ms 增量落 `ai_runs.result` 供游标恢复 |
| MCP 接入 | 仅 stateless HTTP | 不引入会话生命周期与权限模型 | 不支持 stdio / 有状态 SSE / 重连 |
| 可用性 | RAG / FTS / embedding / judge / MCP 失败静默降级 | 对话主链路优先 | 检索不完整时用户无感，需查 `/api/ai/index-status` |
| 前端形态 | PC 与移动两套视图，共用业务层与 `AiMessage.vue` | 浏览优先、PC 零改动 | 两套视图需同步能力；`PC_TO_MOBILE` 未含 `/jobs` |
| 优雅关闭 | 无自定义信号处理，仅 `defer db.Close()` | job 为短扫描，中断无害 | 长请求/SSE 可能被截断；后台 run 重启不恢复 |

**其他已核实的实现现状（中性记录，非缺陷判断）**：

- `create_task` 是写操作但未设 `NeedApproval`，与系统提示「写操作先请求确认」表述不完全一致。
- AI job 幂等用 `last_run_at` 字符串比对（job 侧按 `schedule.timezone`、写回侧按进程本地时区），时区不一致时同分钟存在理论重跑窗口。
- `config.mergeConfig` 未合并 `Server.AllowSymlink`，该 YAML 项不生效，只能由 `ALLOW_SYMLINK` 开启。
- `DefaultConfig` 的默认 `routes` 仍是 3 个历史键，SPA 深链依赖 `config.yaml` 覆盖。
- 前端无「设置密码」UI；`api/auth.ts` 的 `setPassword` 无调用点，也未提供 `setupToken`/`oldPassword`。
- `vite.config.ts` 是**单入口**；`todo.html` 在 `embed.go` 注释、`isUIAsset` 白名单与 `config.yaml` 中属历史遗留。
- 根 `README.md` 的构建/发布章节已迁入 `04-build-and-release.md`；其余偏差见 [`README.md`](README.md#6-已知文档偏差清单)。

## 11. 代码索引

| 主题 | 入口 |
|---|---|
| 配置 / 启动 | `internal/config/config.go`、`cmd/workbench/main.go`（`run`）、`internal/server/server.go`（`New`/`App`） |
| 认证与沙箱 | `internal/server/auth.go`、`server.go`（`safeJoin`/`resolveUserPathSafe`）、`fsops.go` |
| 响应与错误码 | `internal/server/response.go`、`codes.go` |
| 数据层 | `internal/db/*.go`、`internal/db/migrations/*.sql` |
| AI / 后台 run | `internal/server/ai.go`（`handleAIChat`/`aiRunWriter`/`handleAIRuns*`）、`aitools.go`、`aiapproval.go`、`aitools_mcp.go`、`internal/db/ai_runs.go`（`splitRunResult`） |
| 编排 | `internal/pipeline/pipeline.go`、`internal/server/pipeline.go`（`runPipeline`）、`pipeline_http.go`、`schedule_triggers.go`、`aijob_target.go`、`internal/db/pipeline_runs.go`、`cron_triggers.go` |
| RAG | `internal/rag/*.go`、`internal/db/rag.go`、`internal/sqlitevec/vec.go` |
| 调度与通知 | `internal/schedule/*.go`（含 `triggers.go` 的 `NewTriggersJob`/`runCronTriggers`）、`internal/notify/*.go` |
| 前端 | `frontend/src/router/index.ts`、`lib/common.ts`、`api/ai.ts`、`api/pipelines.ts`、`api/scheduleTriggers.ts`、`composables/useAIChat.ts`、`components/ai/AiMessage.vue`、`components/pipeline/PipelineGraph.vue`、`views/AiJobsView.vue`、`composables/todoSync.ts` |
| 构建 | `Makefile`、`frontend/vite.config.ts`、`embed.go`（详见 [`04-build-and-release.md`](04-build-and-release.md)） |
