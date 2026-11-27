# Workbench

Personal dev platform — file browser, markdown viewer, todo board, and API tools for daily development workflows. Built with Go + SQLite.

## Quick Start

> 环境要求、完整构建命令、首次管理员初始化与公网部署步骤见 [`docs/01-getting-started.md`](docs/01-getting-started.md)；Makefile target、构建约束、质量门禁与发布流程见 [`docs/04-build-and-release.md`](docs/04-build-and-release.md)；配置与运维见 [`docs/05-operations.md`](docs/05-operations.md)。

```bash
make all        # 构建前端 + 编译 Go 二进制（UI 经 //go:embed 内嵌）
make run        # 构建并启动（ALLOW_SYMLINK=true）
cd frontend && pnpm dev   # 前端热更新（Vite 4000 端口，不 embed）
```

服务端口由 `server.port` 决定：代码默认 `8080`，仓库 `config.yaml` 示例为 `8001`（`PORT` 环境变量可覆盖）。前端改动后必须重新编译二进制。

## First-time Setup (Admin Initialization)

On first launch the `users` table is empty. The **first user to set a password automatically becomes the super admin** and gains access to the User Management panel.

> **Secure the init window**: between deployment and first registration, anyone who can reach the server can claim the super admin. Set a one-time bootstrap token before first launch — the first registration must then present it:
>
> ```bash
> export ADMIN_SETUP_TOKEN=$(openssl rand -hex 16)   # remember this value
> make run
>
> # First registration (setupToken required only while no user exists; useless afterwards)
> curl -s -X POST http://localhost/api/set-password \
>   -H "Content-Type: application/json" \
>   -d '{"orgId":"cm","userId":"yiiewang","newPassword":"your-password","setupToken":"<the-value>"}'
> ```
>
> Without `ADMIN_SETUP_TOKEN`, behavior falls back to first-come-first-admin and a warning is logged at startup. The token is only checked while the user table is empty.

> `orgId` is required at registration (it records which org the user belongs to). At **login** time, `orgId` is optional — usernames are globally unique, so you can sign in with just `userId` + `password`.

Then log in as that user in the browser — the User Management icon appears in the activity bar (admin only). Use it to create more users, reset passwords, change roles, and manage organizations.

Roles: `admin` (super admin, manages all users across orgs) and `user` (regular user).

## Configuration

All settings in `config.yaml`:

```yaml
server:
  port: 80                    # HTTP listen port
  static_dir: ./preview       # Directory to serve
  allow_symlink: false        # Strict path check (symlinks outside static_dir → 404). Env: ALLOW_SYMLINK=true to enable
  hidden:                     # Hide files from directory tree listing
    - "index.html"
    - ".*"

database:
  path: ./data/workbench.db   # SQLite file path

auth:
  token_expiry_days: 7        # Token validity (keep short; leaked tokens self-expire sooner)

routes:                       # URL routing
  "/todo": "/todo.html"       # 302 redirect
  "/index": "__listdir__"     # Directory listing
  "/todo.html": "Todo Board"  # Display name

logging:
  dir: ~/.local/state/workbench  # Access log directory
  level: info                    # Operational log level: debug | info | warn | error (structured JSON to stdout)

ai:                            # AI config: chat & embedding are independent sub-sections (often different services)
  chat:                        # chat model gateways (OpenAI-compatible)
    default: openai            # default gateway id; the UI can switch per conversation
    tools: false               # enable Agent tool calling (requires function-calling capable model); env: AI_TOOLS=true
    thinking: false            # include chat_template_kwargs.thinking=true; env: AI_THINKING=true
    gateways:
      - id: openai
        name: GPT-4o mini
        base_url: https://api.openai.com/v1
        api_key: ""            # keep empty in the file — inject via AI_API_KEY env instead; never commit real keys
        model: gpt-4o-mini
  embedding:                   # vector model (RAG semantic search over your files), enabled when base_url + model are set
    base_url: ""               # e.g. http://localhost:8000/v1 (vLLM); OpenAI-compatible /embeddings
    api_key: ""                # inject via AI_EMBEDDING_API_KEY env; may stay empty for local deployments
    model: Qwen3-Embedding-8B
    dim: 1024                  # Matryoshka reduced dimension (Qwen3-Embedding native 4096); changing it invalidates all vectors
  mcp:                        # external MCP tools (stateless HTTP servers only); tools exposed as mcp_<name>_<tool>
    servers: []
    # - name: demo             # every external tool call requires user approval (same as built-in write tools)
    #   url: https://mcp.example.com/mcp
    #   headers:                # values support ${ENV_VAR} expansion, so tokens stay out of the repo
    #     Authorization: "Bearer ${MCP_DEMO_TOKEN}"

schedule:                      # cron scheduler + in-process event bus (internal/schedule)
  enabled: false               # config-only (no env override); enables all scheduler jobs
  timezone: Asia/Shanghai      # IANA timezone for cron evaluation
  todo_due_cron: "*/5 * * * *" # due-date scan frequency

notify:                        # notification channels (subscribe to the event bus)
  email:
    enabled: false             # requires schedule.enabled; recipient = task owner's email (per-user, set in UI)
    host: smtp.qq.com          # SMTP server (587 STARTTLS; port 465 often blocked by cloud providers)
    port: 587
    user: ""                   # SMTP auth username (full address for QQ mail)
    from: ""                   # sender address, defaults to user
    from_name: "Workbench"     # display name shown in the recipient's inbox
    # auth code is injected via env: EMAIL_PASS (never commit it)
```

### Environment Variables

| Variable | Config Override | Description |
|---|---|---|
| `PORT` | `server.port` | HTTP listen port |
| `STATIC_DIR` | `server.static_dir` | Static file directory |
| `DB_PATH` | `database.path` | SQLite database path |
| `LOG_DIR` | `logging.dir` | Access log directory |
| `ALLOW_SYMLINK` | `server.allow_symlink` | Set `true` or `1` to allow symlink access outside static_dir |
| `AI_API_KEY` | `ai.chat.gateways[].api_key` | Default gateway key (gateway-level key wins; prefer env over config file) |
| `AI_DEFAULT_GATEWAY` | `ai.chat.default` | Default gateway id |
| `AI_EMBEDDING_BASE_URL` | `ai.embedding.base_url` | Vector model endpoint (RAG; OpenAI-compatible /embeddings) |
| `AI_EMBEDDING_API_KEY` | `ai.embedding.api_key` | Vector model API key |
| `AI_EMBEDDING_MODEL` | `ai.embedding.model` | Vector model name (e.g. Qwen3-Embedding-8B) |
| `AI_EMBEDDING_DIM` | `ai.embedding.dim` | Vector dimension (Matryoshka); changing it invalidates all vectors (rebuild via POST /api/ai/reindex) |
| `ADMIN_SETUP_TOKEN` | `auth.admin_setup_token` | One-time bootstrap token required by the first admin registration (only checked while no user exists) |

## Features

- **File Browser** — Directory tree, tabbed editor, syntax highlighting for code/markdown/JSON
- **Markdown Viewer** — Rendered markdown with Mermaid diagrams, cross-file link navigation, anchor scrolling
- **JSON Viewer** — Collapsible tree view, raw text toggle, copy raw to clipboard
- **AI Chat & Agent** — OpenAI-compatible streaming chat with 16 built-in tools (file ops / search / task management / event publishing / scheduled jobs / pipelines / indexing); Human-in-the-loop approval for write operations; `@` file references; reasoning display; session history persisted in SQLite
- **Scheduled Tasks & Notifications** — Hand-written cron engine + in-process event bus; one scanner over `schedule_triggers`; a job can run an AI prompt, a tool, an in-site API call, or a pipeline at a cron time (static `allowWrite` pre-authorization; per-run history with target type and pipeline run id). For AI-prompt jobs, `allowWrite` doubles as the tool whitelist: the model can autonomously call only the listed tools (up to 5 non-streaming agent rounds, listing = pre-approval), while an empty list keeps the original single text generation; scheduled-job results are re-formatted by AI into a letter-style email with a fixed Workbench shell and a sanitized HTML body (falls back to the card template when AI is unavailable); precise task-time reminders (start/due HH:mm); email delivery via SMTP 587 STARTTLS
- **Declarative Pipelines** — JSON `Definition` with DAG `needs`, `when`, `retry` and tool/api/llm steps, inlined in a scheduled job's `target` (AI can generate complex flows directly); run on cron or immediately; DAG view, node inspector and run overlay under `/jobs` with SSE live per-layer progress streaming
- **Todo ↔ File Linking** — Tasks can link files (tree picker in todo editor); the file viewer shows linked tasks with jump-to-task and link-existing-task actions
- **Download & Share** — Download any file; create time-limited, access-controlled shares for files or folders
- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images with path traversal protection; gzip/br response compression
- **Route Mapping** — Clean URLs, display name aliases, redirects, directory listings
- **Token Auth** — HMAC-SHA256 authentication; all file access requires valid Bearer token (fully private mode)
- **User Management** — Admin role with a user management UI (create / edit / reset-password / delete users across orgs, per-user notification email)
- **Share Management** — Create shares with access count limits, time ranges, optional passwords; manage all shares from sidebar panel
- **Todo Board** — User/org task management with conflict detection, optimistic-lock revision sync, multi-device support
- **SQLite Storage** — All data in a single portable `workbench.db` file (WAL mode)

### AI Agent Tools (16)

When `ai.chat.tools` is enabled, the chat agent can call these tools (write operations require in-chat approval):

| Category | Tools |
|---|---|
| Files | `list_dir` · `read_file` · `write_file` |
| Search | `search_files` |
| Tasks | `list_tasks` · `create_task` · `update_task` |
| Scheduled jobs | `list_jobs` · `create_job` · `update_job` · `delete_job` |
| Pipelines | `run_pipeline` |
| Indexing | `reindex_files` · `reindex_status` |
| Events & notifications | `list_events` · `notify_user` |

### Feature Matrix (Desktop vs Mobile)

The mobile UI (`/m/*`, auto-served on viewports ≤900px) is a separate, browse-first view set that shares all business logic with the desktop UI. Complex management flows stay desktop-only by design.

| Capability | Desktop | Mobile |
|---|:---:|:---:|
| File browsing (tree / directory drill-down) | ✅ full sidebar tree + tabs | ✅ single-level drill-down list |
| File preview (markdown / code / image / audio / video) | ✅ | ✅ same viewer components |
| File upload | ✅ drag & drop / context menu | ✅ (from file picker) |
| File management (create / rename / move / delete) | ✅ | — |
| Multi-tab editor | ✅ | — |
| Markdown TOC (heading navigation) | ✅ | — |
| AI chat (streaming, `@` file references, reasoning, history) | ✅ floating panel | ✅ full-screen page |
| AI message actions (copy / regenerate / save as file) | ✅ | ✅ |
| AI document summary (one-click) | ✅ tab-bar button | — |
| Todo: view tasks | ✅ quadrant / board / calendar | ✅ status-grouped list |
| Todo: edit tasks | ✅ full editor | ✅ status toggle / check |
| Todo: create / delete tasks, quadrant & calendar views | ✅ | — |
| Todo ↔ file linking | ✅ picker + reverse-link | ✅ view links, open files |
| Share creation & management | ✅ | — |
| Share consumption (opening a shared link) | ✅ | ✅ dedicated mobile view |
| User management (admin) | ✅ | — |
| Organization switching | ✅ | ✅ profile page |
| Login / logout | ✅ | ✅ |
- **CORS Security** — Sandboxed iframe preview with null-origin CORS (no token leakage)

## API Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/__map__` | GET | Route configuration map (requires token) |
| `/__tree__` | GET | File directory tree (JSON, requires token) |
| `/api/org-members` | GET | List org members (`?orgId=xxx`, requires token) |
| `/api/login` | POST | Login (`{userId, password, orgId?}` — orgId optional, username globally unique) |
| `/api/set-password` | POST | Set/change password |
| `/api/me` | GET | Current user info (requires token) |
| `/tasks.json` | GET | Task data with version info (requires token) |
| `/tasks.json` | PUT | Update tasks + version (requires `Authorization: Bearer <token>`) |
| `/api/share` | GET | List my shares (requires token) |
| `/api/share` | POST | Create share `{resourcePath, resourceType, maxAccessCount, password, effectiveAt, expiresAt}` (requires token) |
| `/api/share/{id}` | DELETE | Revoke share (requires token) |
| `/api/profile/email` | POST | Update own notification email `{email}` (requires token) |
| `/api/tasks/{id}` | GET | Task detail (full fields incl. content; requires token) |
| `/api/ai/approval` | POST | Approve/decline an AI write-tool call `{id, approved}` (requires token) |
| `/api/jobs` | GET/POST | List / create scheduled jobs (`target`: ai / tool / interface / pipeline; requires token) |
| `/api/jobs/{id}` | PUT/DELETE | Update / delete a scheduled job (requires token) |
| `/api/jobs/{id}/runs` | GET | Execution history (target type, pipeline run id, steps summary; requires token) |
| `/api/pipeline/run` | POST | Run a pipeline Definition asynchronously (used by "Run now" in `/jobs`; requires token) |
| `/api/pipeline/runs` · `/api/pipeline/runs/{id}` | GET | List / detail of pipeline runs incl. per-step results (requires token) |
| `/api/pipeline/runs/{id}/stream` | GET | SSE live progress of a pipeline run: `progress` events with full run snapshot per DAG layer, `end` on terminal state (requires token) |
| `/api/schedule/triggers` | GET/POST | List / create cron trigger rules (requires token) |
| `/api/schedule/triggers/{id}` | GET/PUT/DELETE | Trigger detail / update / delete (requires token) |
| `/api/admin/users` | GET | List all users across orgs (admin only) |
| `/api/admin/users` | POST | Create user `{org, name, password, roleId, mobile}` (admin only) |
| `/api/admin/users/{id}` | PATCH | Update user `{name?, mobile?, password?, roleId?}` (admin only) |
| `/api/admin/users/{id}` | DELETE | Delete user (admin only, cannot delete self or last admin) |
| `/api/admin/roles` | GET | List roles (admin only) |
| `/s/{token}` | GET | Access shared resource (public, subject to share permissions) |
| `/api/*`, `/tasks.json` | OPTIONS | CORS preflight (returns 204) |

### Raw File Access

```bash
# Get raw file via curl (direct file path, no #)
curl http://host/goModule/v2.3.9.json

# Browser opens file viewer UI (with #)
http://host/#/goModule/v2.3.9.json
```

## Database

SQLite database (`data/workbench.db`, WAL mode):

| Table | Description |
|---|---|
| `visit_logs` | Per-request access records |
| `orgs` | Organizations |
| `users` | Users with SHA-256 password hashes, `version_json` for sync conflict detection |
| `tasks` | Per-user task lists |
| `shares` | Resource shares with access count, time range, password protection |
| `app_secrets` | Application secrets (token signing key, etc.) |

## Deployment (Public Server)

When exposing Workbench beyond localhost, put a reverse proxy in front and never expose the backend directly:

```
client ──HTTPS──> nginx (443, TLS) ──HTTP──> workbench (127.0.0.1:8001)
```

- **Bind the backend to loopback only** — the only public ports should be 80/443 on the proxy. Otherwise the backend serves plaintext and bypasses TLS entirely.
- **TLS**: on a server without a domain (bare IP), Let's Encrypt issues IP-address certificates via the `shortlived` profile (~6-day validity, auto-renewed):
  ```bash
  acme.sh --issue -d <server-ip> --webroot <acme-webroot> --cert-profile shortlived
  acme.sh --install-cert -d <server-ip> \
    --key-file <ssl-dir>/ip.key --fullchain-file <ssl-dir>/ip.crt \
    --reloadcmd "nginx -s reload"
  ```
  Mainland-China note: an unregistered (未备案) server typically gets TCP-through-TLS-reset on port 443 — verify your provider allows it, and make sure the security group actually opens 443.
- **Proxy settings that matter**:
  ```nginx
  client_max_body_size 512m;      # match the workbench upload limit (512MB)
  proxy_buffering off;            # required for SSE streaming (/api/ai/chat)
  proxy_read_timeout 3600s;
  ```
- **Never commit real secrets** (AI keys, bootstrap tokens) to the repository — inject them through environment variables. If a key ever lands in git history, treat it as leaked: revoke it at the provider, then rewrite history.

## Security

- **Path Traversal Protection** — `safeJoin` blocks `../` attacks (literal and URL-encoded)
- **Symlink Guard** — Default `allow_symlink: false` resolves symlinks and verifies real path stays within `static_dir`
- **CORS for Sandboxed iframe** — Preview iframe uses `sandbox` without `allow-same-origin`; API responses include `Access-Control-Allow-Origin: null` to allow script execution without token leakage
- **Token Auth** — Bearer token (HMAC-SHA256), not stored in cookies; write operations require valid token
- **Short Token Lifetime** — `token_expiry_days: 7` by default; leaked tokens self-expire quickly and cannot be revoked individually (re-issue requires rotating the signing secret)
- **Admin Init Guard** — First registration requires a one-time `ADMIN_SETUP_TOKEN` when configured (constant-time compare), closing the deployment-to-registration race window

## Directory Structure

```
workbench/
├── cmd/workbench/main.go       # Entry point
├── embed.go                    # //go:embed frontend/dist — 内置 UI 资源
├── internal/
│   ├── config/                 # Config loading (YAML + env overrides, logging level)
│   ├── db/                     # SQLite layer: db/migrate/secrets/visits/users/tasks/shares/rate_limits
│   └── server/                 # HTTP handlers, auth, share, codes/response helpers, handler_test
├── frontend/                   # Vite + Vue3 + TypeScript 工程
│   ├── src/                    # 源码: TodoApp.vue / TaskItem.vue / MarkdownEditor.vue / todo-app.ts / common.ts
│   ├── dist/                   # 构建产物 (gitignored, make frontend 生成, //go:embed 嵌入)
│   ├── index.html / todo.html  # Vite 多入口
│   └── vite.config.ts          # manualChunks 拆 vendor + highlight.js 按需
├── preview/                    # Default serve directory (用户文件)
├── scripts/package.sh          # Cross-platform packaging (used by `make package`)
├── config.yaml                 # Configuration file
├── data/                       # Runtime data (gitignored)
├── Makefile                    # build / frontend / all / check
├── go.mod / go.sum
├── README.md
└── .gitignore
```

## Release Process

发布流程（版本 bump、tag、GitHub Release、跨平台打包与上传、发布检查清单）与全部构建说明统一维护在 [`docs/04-build-and-release.md`](docs/04-build-and-release.md#8-发布流程)。

## License

MIT
