# 配置与运维

> 面向：部署运维、开发者 ｜ 代码基线：`v1.3.0-26-gc0577c4` + 工作树迁移 000021–000024
> 首次启动见 [`01-getting-started.md`](01-getting-started.md)；构建与发布见 [`04-build-and-release.md`](04-build-and-release.md)；功能参数见 [`02-functional-guide.md`](02-functional-guide.md)。

## 1. 配置加载与合并语义

顺序：**代码默认值**（`config.DefaultConfig`）→ **`config.yaml` 非零值覆盖**（`mergeConfig`）→ **环境变量覆盖**（`config.ApplyEnv`）。配置文件不存在时全部使用默认值；路径中的 `~/` 由 `ResolvePath` 展开。

| 行为 | 说明 |
|---|---|
| 非零值合并 | YAML 里的零值（`""`、`0`、`false`）**不会覆盖**非零默认值。想关闭一个默认开启的 bool 项，不能只写 `false` |
| 复合字段整体替换 | `server.hidden`、`routes`、`ai.mcp.servers` 等 slice/map 是整体替换，不是逐项追加 |
| `server.allow_symlink` | **YAML 项当前不参与 `mergeConfig`**，只能由环境变量 `ALLOW_SYMLINK=true|1` 开启；`make run` 默认带上该变量 |
| 无环境变量覆盖的段 | `schedule.*`、`notify.*`、`ai.mcp.servers`、`logging.level`、`auth.token_expiry_days` 只能改 `config.yaml` |
| 凭据 | `EMAIL_PASS`、AI keys、`ADMIN_SETUP_TOKEN`、MCP header 的 `${ENV_VAR}` 均由环境变量注入，不落仓库 |

> 仓库 `config.yaml` 是**部署示例**，不是默认值清单：端口 `8001` vs 默认 `8080`，token 有效期 `7` 天 vs 默认 `30` 天，`ai.chat.tools: true` vs 默认 `false`，`schedule.enabled: true` vs 默认 `false`。

## 2. 配置段

| 段 | 关键项 | 代码默认 | `config.yaml` 示例 |
|---|---|---|---|
| `site_url` | 邮件跳转链接的对外根地址 | `""`（不渲染链接） | `https://…` |
| `server` | `port` / `static_dir` / `allow_symlink` / `hidden` | `8080` / `./static` / `false` / `.*,*.zip,*.tar,*.gz,*.tgz,*.rar,*.7z,*.bz2,*.xz` | `8001` / `./preview` / `false` / 使用默认 |
| `database` | `path` | `./data/workbench.db` | 同 |
| `auth` | `token_expiry_days` / `admin_setup_token` | `30` / `""` | `7` / 环境变量注入 |
| `routes` | 路径 → `__spa__` / `__listdir__` / 别名 / 302 目标 | 3 个历史键 | 全部 SPA 入口（示例共 13 个 `__spa__` 键，含 `/pipelines`） |
| `logging` | `dir` / `level` | `~/.local/state/workbench` / `info` | `logs` / `info` |
| `ai.chat` | `base_url` / `api_key` / `model` / `tools` | OpenAI 默认端点 / `""` / `gpt-4o-mini` / `false` | 内部网关 / 环境变量 / `hy4` / `true` |
| `ai.embedding` | `base_url` / `api_key` / `model` / `dim` / `max_distance` / `judge_model` / `digest_model` | `""`/`""`/`""`/`1024`/`0.9`/`""`/`""` | 模型网关 / `Qwen3-Embedding-8B` / `1024` / `0.9` / `deepseek-flash` ×2 |
| `ai.mcp.servers` | `name` / `url` / `headers` | 无（关闭） | 一个 `exa-websearch` 服务 |
| `schedule` | `enabled` / `timezone` / `todo_due_cron` | `false` / `Asia/Shanghai` / `*/5 * * * *` | `true` / 同 / 同 |
| `notify.email` | `enabled` / `host` / `port` / `user` / `from` / `from_name` | `false` / `smtp.qq.com` / `587` / `""` / 回退 `user` / 运行回退 `Workbench` | `true` / 同 / `587` / 个人邮箱 / — / `Workbench Robot` |

`server.hidden` 默认隐藏点开头文件与常见压缩包；命中的文件**仍可上传**，但不在目录树显示，上传响应会单独列出 `hiddenNames`。

## 3. 环境变量

| 变量 | 覆盖项 |
|---|---|
| `PORT` / `STATIC_DIR` / `DB_PATH` / `LOG_DIR` | 端口 / 用户文件目录 / 数据库路径 / 日志目录 |
| `ALLOW_SYMLINK` | `server.allow_symlink`（`true` 或 `1`） |
| `ADMIN_SETUP_TOKEN` | `auth.admin_setup_token` |
| `AI_BASE_URL` / `AI_API_KEY` / `AI_MODEL` / `AI_TOOLS` | `ai.chat.*` |
| `AI_EMBEDDING_BASE_URL` / `AI_EMBEDDING_API_KEY` / `AI_EMBEDDING_MODEL` / `AI_EMBEDDING_DIM` / `AI_EMBEDDING_MAX_DISTANCE` / `AI_EMBEDDING_JUDGE_MODEL` / `AI_EMBEDDING_DIGEST_MODEL` | `ai.embedding.*` |
| `WORKBENCH_SITE_URL` | `site_url` |
| `EMAIL_PASS` | SMTP 授权码（不经过配置结构） |
| `EXA_API_KEY` 等 | MCP `headers` 中的 `${ENV_VAR}` 占位符 |

## 4. 功能启用矩阵

| 能力 | 生效条件 | 未满足时 |
|---|---|---|
| 基础 AI 对话 | `ai.chat.api_key` 与 `base_url` 均非空 | `GET /api/ai/status` 的 `enabled=false`，前端不显示入口 |
| Agent 工具 | `ai.chat.tools: true` / `AI_TOOLS=true`，且模型支持 function calling | 纯对话模式 |
| RAG 语义检索 | `ai.embedding.base_url` 与 `model` 非空 | `s.rag == nil`，写钩子与检索静默跳过；`reindex` 返回 `50301` |
| 相关性预判 / 摘要卡 | `judge_model` / `digest_model` 非空 | 关闭，不影响检索 |
| 外部 MCP 工具 | `ai.mcp.servers` 配置有效 | 无外部工具；配置缺 `name`/`url` 启动 fail fast，服务不可达仅告警跳过 |
| 定时调度 | `schedule.enabled: true` | 不启动 cron，任务提醒、定时任务与通用触发规则（模板 / job）都不执行 |
| 通用定时触发 | `schedule.enabled: true` + 规则 `enabled=true` + 合法 `cronExpr` + 目标（模板 / job）存在且归属当前身份 | `cron_triggers_runner` 不扫描或跳过该规则；手动触发与 `POST /api/pipeline/run` 直调不受影响 |
| 邮件通知 | `notify.email.enabled: true` **且** `schedule.enabled: true`，`host`/`user` 非空、`EMAIL_PASS` 已注入 | 事件照常发布但无订阅者；配置缺失时启动直接报错 |
| 邮件跳转按钮 | `site_url` 为合法 http(s) 地址 | 邮件不含任何链接（避免 `localhost` 死链） |

## 5. RAG 索引运维

### 5.1 索引链路与写钩子

```
可索引判定：24 类文本扩展名、≤2 MiB、非隐藏文件
  → HTML 剥标签 → SHA-256 判重（状态 ok 且 hash 未变则跳过）
  → 切块（800 字目标 + 100 字重叠，记录 1-based start_line）
  → [可选 digest_model 摘要卡] → /embeddings 批量向量化
  → ReplaceFileIndex 事务：删旧 vec/fts/chunks → 写新 → upsert file_index_state
```

| 文件操作 | 钩子 | 执行方式 |
|---|---|---|
| `POST /api/upload`、AI `write_file` | `HandleWrite` | 异步 goroutine，不阻塞请求 |
| `DELETE /api/file` | `HandleDelete` | **同步**（纯本地 SQL），目录按前缀删 |
| `POST /api/rename`、`POST /api/move` | `HandleRename` | 同步，只改 path，内容与向量不变 |
| `POST /api/mkdir` | 无 | 不产生文件内容 |

写钩子失败（`HandleDelete` / `HandleRename`）只记 `slog.Warn`，**不会回滚已完成的磁盘操作**。

### 5.2 删除文件时会清理哪些 RAG 数据

`DELETE /api/file` 在 `os.RemoveAll` 成功后调用 `HandleDelete`（`internal/server/fsops.go`）。`db.DeleteFileIndex` / `DeleteFileIndexPrefix` 在**一个事务**内删掉该路径（目录为 `path LIKE '前缀/%'`，`likeEscape` 转义 `%`/`_`）的全部四部分：

| 表 | 删除方式 |
|---|---|
| `file_chunk_vec` | 先查 chunk id，再按 id 点删（vec0 不支持子查询删除） |
| `file_chunk_fts` | 按 chunk id 删（必须早于 `file_chunks` 删除） |
| `file_chunks` | 按 path / 前缀删 |
| `file_index_state` | 按 path / 前缀删 |

所以**正常删除文件，索引在删除时就已清掉，不依赖后续重建**。此外：文件被替换为非文本或超 2 MiB 时 `IndexFile` 会主动 `DeleteFileIndex`；同名文件重新创建走 `ReplaceFileIndex`（先删旧再写新），不会残留旧内容。

### 5.3 全量重建会清理孤儿索引

`POST /api/ai/reindex`（`rag.ReindexTree`）在收集完待索引文件后、逐文件索引前，先执行一次孤儿清理：

1. `db.ListIndexedPaths` 取出**当前 `org:user`** 在 `file_index_state` 中的全部路径（无 limit、按 owner 隔离）；
2. `orphanPaths` 把每个路径拼回磁盘路径并 `os.Stat`，**仅当 `os.IsNotExist`** 才判为孤儿；
3. 复用 `DeleteFileIndex` 在事务内清 `file_chunk_vec` / `file_chunk_fts` / `file_chunks` / `file_index_state` 四表；清理数写入 `ReindexStats.Pruned`、进度快照 `pruned` 与 `rag: reindex done` 日志。

孤儿索引成因：绕过接口手工 `rm`、服务停机期间删除、`HandleDelete` 失败（只记 warn）、外部工具整目录搬走、索引写入与文件删除的竞态。**执行一次 reindex 即可清掉，清理失败只记 warn 不中断重建**。

**为什么用「文件是否存在」而不是「本次遍历到的文件差集」**：`collect` 遇到 `os.ReadDir` 失败只记 warn 并跳过该子树，差集会把「文件仍在但本次没扫到」的整棵子树索引误删；存在性检查天然不受遍历完整性影响。

**边界：reindex 不会清掉的无效索引**

| 类型 | 原因 |
|---|---|
| 文件仍在但变成二进制 / 超 2 MiB / 空文件 | 文件存在；`collect` 的 `Indexable` 预筛使 `IndexFile` 的清理分支不被调用（只有写钩子 `HandleWrite` 路径会清） |
| 隐藏文件 / `.venv` 等隐藏目录、`node_modules` 等依赖目录里的旧索引 | 文件存在，`collect` 整棵跳过 |
| `allow_symlink=false` 时曾通过软链接索引过的路径 | 链接条目仍存在 |
| `status='error'` 且文件仍存在 | 不是孤儿，重建会按现有逻辑重试覆盖 |
| vec0 / FTS5 惰性删除的物理残留空间 | 删除是标记式，需整表 DROP + VACUUM |

### 5.4 排查与清理

| 手段 | 作用 |
|---|---|
| `GET /api/ai/index-status` | 汇总 `files/chunks/errors` + 明细（最多 500 条），可看到路径、块数、错误状态 |
| AI 面板左栏「索引状态」tab | 同上，绿点 ok / 红点失败，可刷新；原独立抽屉已并入左栏三 tab |
| `POST /api/ai/reindex` | **按 owner 清孤儿索引** + 重建现存文件；进度 `pruned` 显示清理数量 |
| `make ragclean` | 默认按 `junkPath`（`/.`、`node_modules`、`__pycache__`、`vendor`、`target`）清隐藏/依赖目录索引；不处理孤儿（已由 reindex 负责），也不删 FTS 行 |
| `make ragclean ARGS=--reset` | **全表（所有用户）重置**：`DROP file_chunk_vec`、`DROP file_chunk_fts`、`DELETE file_chunks`、`DELETE file_index_state`、清 `rag_meta(vec_dim)`、`VACUUM` |

现在**清孤儿不需要停服或 reset**，直接触发重建即可：

```bash
curl -X POST http://localhost:8001/api/ai/reindex -H "Authorization: Bearer <token>"
# 进度：GET /api/ai/reindex/status（pruned 字段）；结果：GET /api/ai/index-status
```

只有要回收 vec0/FTS5 惰性删除的空间、或清 §5.3 边界表里的非孤儿残留时，才需要全量重置：

```bash
# 1. 停服（避免运行中的写钩子继续写入）
make ragclean ARGS=--reset
# 2. 启动服务：EnsureVecIndex / EnsureFTS 自动重建空表
make run
# 3. 全量重建当前磁盘上真实存在的文件（每个用户各自触发）
curl -X POST http://localhost:8001/api/ai/reindex -H "Authorization: Bearer <token>"
```

> vec0 v0.1.6 与 FTS5 的 `DELETE` 都是**惰性/段式删除**：逻辑上查不到，但 blob / 影子表占用不回收，`VACUUM` 也不释放。只有整表 `DROP` 重建才能真正缩小数据库，这也是 `--reset` 存在的理由。注意 `--reset` 的 SQL 没有 org/user 过滤，多用户部署下所有人索引都会作废。

### 5.5 重建与状态查询

| 接口 / 工具 | 说明 |
|---|---|
| `POST /api/ai/reindex` | 后台异步全量重建；同一 `org:user` 并发触发返回 `40902`；立即返回 `{"started":true}` |
| `GET /api/ai/reindex/status` | 进度快照 `running/total/processed/indexed/skipped/failed/pruned/currentPath/startedAt/updatedAt`；`pruned` 为本次清理的孤儿索引数，`running=false` 时是最近一次已完成记录 |
| `GET /api/ai/index-status` | 文件级索引状态与错误明细 |
| AI 工具 `reindex_files` / `reindex_status` | 与 HTTP 接口共用 `startReindex`；前者为写类需审批，后者只读 |

HTTP 与 Agent 共用同一套「RAG 可用性检查 → org:user 防重 → 后台 `ReindexTree`」逻辑，避免两套异步/防重语义漂移。

### 5.6 维度变更

修改 `ai.embedding.dim` 后，`EnsureVecIndex` 用**双信号**判定（`rag_meta.vec_dim` 一致 **且** `file_chunk_vec` 表实际存在）：不一致就 DROP 向量表并 `DELETE FROM file_index_state`，使全部向量作废。`file_chunks` / FTS 文本保留但向量暂缺，必须执行 `POST /api/ai/reindex` 重新灌入。

## 6. 数据与备份

- 业务数据全部在 `database.path` 的单个 SQLite 文件，WAL 模式、`busy_timeout=5000`、`foreign_keys=on`、`SetMaxOpenConns(1)`。
- **备份必须包含 `-wal` / `-shm`**，或使用 SQLite 在线备份 API / `VACUUM INTO`；只复制主库文件可能丢失未 checkpoint 的写入。
- `app_secrets` 存 token 签名密钥（`LoadOrCreateSecret`，兼容迁移旧 `.token_secret` 文件）。**token 无法单独吊销**：轮换该密钥或删库重建会使全部 token 失效，所有用户需重新登录。
- 用户文件在 `static_dir/{orgName}/{userName}/`，需与数据库一起备份；两者是「整数 id 存储 / 名称路径」双轨，改名需同步迁移磁盘目录。
- 分享记录、AI 会话、后台运行记录、编排模板 / 通用触发规则 / 编排运行记录、任务、索引状态均在库内，删库即全丢。

## 7. 升级与迁移

`internal/db/migrations/` 用 golang-migrate 管理，`//go:embed` 内嵌；当前基线 **000026_pipeline_template_versions**（26 个版本 / 52 个脚本，每个版本一对 `.up.sql` / `.down.sql`）。

- 000017 新建 `pipeline_runs`；000018 新建 `pipeline_templates`（初版含 cron / 启停 / 状态列）；000019 新建 `schedule_triggers` 并从模板 `cron_expr <> ''` 回填已有规则；000020 重建模板表删掉 4 个调度列；000021 新建 `ai_session_messages`；000022 给 `ai_jobs` 加 target 五列并回填 `prompt`；000023 调度收敛（`ai_jobs` 去 4 个调度列、`schedule_triggers` 加 `lease_until`、回填 `target_type='job'`）；000024 给 `ai_job_runs` 加详情列；000025 给 `ai_jobs` 加 `retry`（job 级失败重试，0-3，仅无写步骤生效）；000026 新建 `pipeline_template_versions`（模板版本历史，删除模板级联清理）。000020 / 000023 的 `.down.sql` 都按规则反向回填调度列，回滚不丢配置。
- 启动时自动 `m.Up()`；`sqlite3.Config{NoTxWrap:true}`（`PRAGMA foreign_keys` 在事务内无效）。
- 存量库无 `schema_migrations` 但有 `users` 时 `Force(1)` 跳过 baseline。
- **生产不自动执行 down**；需要回滚时用 CLI 或显式调用 `m.Steps(-1)` / `m.Down()`。
- 新增迁移规范：禁止手写 `BEGIN`/`COMMIT`；仅 `000001_baseline` 允许 `IF NOT EXISTS`，后续脚本必须精确表达增量。
- 升级 `mattn/go-sqlite3` 后执行 `make sync-vec-headers`，再完整 `make check`。

## 8. 日志与可观测

| 通道 | 内容 |
|---|---|
| stdout `slog` JSON | 结构化运行日志，级别由 `logging.level` 控制（`debug`/`info`/`warn`/`error`） |
| `logging.dir/preview.log` | 每请求访问日志（时间/IP/UA/path/status）；SSE 与媒体路径跳过 `ctx.Record()` 以保持流式 |
| `rag: reindex progress` | 全量重建每 20 个文件输出一次进度 |
| 审计日志 | 文件写操作记录 user/org/path |
| 告警示例 | 「首个注册者将成为超管」、MCP 服务不可达、cron/时区非法、RAG 建表失败降级、索引钩子失败、`cron trigger done/failed`（未知 `target_type`、非法 cron 与目标不存在会写 `error: ...` 状态） |

## 9. 常见运维问题

| 现象 | 排查与处理 |
|---|---|
| **删掉的文件仍被 AI 检索/引用** | 多为绕过接口删除或 `HandleDelete` 失败留下的孤儿索引；执行 `POST /api/ai/reindex` 即会按 owner 清掉，用 `/api/ai/reindex/status` 的 `pruned` 与 `/api/ai/index-status` 核对；若属 §5.3 边界表里的非孤儿残留，才用 `make ragclean ARGS=--reset` |
| `no such module: fts5` | 直接 `go run` 缺 build tag；一律走 `make` 入口 |
| 改了 `dim` 后检索无结果 | 向量已作废，`POST /api/ai/reindex` 重建，用 `/api/ai/reindex/status` 看进度 |
| 索引重建一直 `running` | 查 `currentPath` 与日志 `rag: reindex progress`；单文件超时 5 分钟，embedding 失败记 `error` 状态行，下次钩子或重建重试 |
| 数据库很大但文件不多 | vec0/FTS5 惰性删除残留；`make ragclean ARGS=--reset` + 重启 + reindex |
| 上传成功但目录树看不到 | 命中 `server.hidden`；查看上传响应 `hiddenNames` 或调整 `hidden` |
| 刷新 `/file`、`/jobs`、`/pipelines` 等返回 401 | `config.yaml` 的 `routes` 未把该路径注册为 `__spa__` |
| 模板的定时触发没执行 | 依次查 `schedule.enabled` → `cronExpr` 合法性 → 规则 `enabled` → `lastRunAt` / `lastStatus`（未知 `target_type` / 目标不存在会写 `error: ...`）→ 目标模板归属与是否被删 → 15 分钟单次超时 → 分钟键防重 → 删模板时的级联清理；规则增删改下一分钟生效 |
| 收不到提醒邮件 | 依次查 `schedule.enabled`、`notify.email.enabled` + `host`/`user`、`EMAIL_PASS`、用户邮箱、任务是否 `done`、当天是否已去重（去重在发布前，SMTP 失败不重投） |
| 邮件里没有跳转按钮 | `site_url` 未配置或不是 http(s) |
| AI 入口不显示 | `GET /api/ai/status` 的 `enabled`；检查 `AI_API_KEY` 与 `base_url` |
| 公网访问 API 很慢 | 确认响应压缩生效、客户端带 `Accept-Encoding`、反代 `proxy_buffering off` |
| 疑似 token 泄漏 | 轮换 `app_secrets` 的 token 密钥并重启，全部用户重新登录 |
| 移动端缺上传/分享管理/用户管理 | 设计如此，复杂操作在 PC 端完成 |

## 10. 安全运维

- 后端只绑定回环，由 nginx 终结 TLS；`client_max_body_size 512m`、`proxy_buffering off`、`proxy_read_timeout 3600s`。
- 密钥全部环境变量注入；误入 git 历史视为泄漏，先吊销再重写历史。
- `ADMIN_SETUP_TOKEN` 只在用户表为空时校验，部署到首次注册之间应始终配置。
- token 默认 30 天（示例配置 7 天），无状态、不可单独吊销；登录失败 5 次/分钟/IP、分享密码 5 次/5 分钟由 `rate_limits` 持久化。
- HTML 预览运行在无 `allow-same-origin` 的沙箱 iframe；`/api/*` 固定 `Access-Control-Allow-Origin: null`。
- 邮件正文经 HTML 转义，`safeHref` 只允许 http(s) 且与 `site_url` 同 host。
