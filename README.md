# Workbench

Personal dev platform — file browser, markdown viewer, todo board, and API tools for daily development workflows. Built with Go + SQLite.

## Quick Start

```bash
# 前置：Go 1.25+、Node 18+、pnpm（前端构建工具链）

# Fresh clone 或改过前端：一键构建（前端编译 + Go build，embed frontend/dist）
make all

# 只改 Go 代码：直接 build（embed 已生成的 frontend/dist）
make build

# Run (with default config.yaml)
make run

# 前端开发服务器（Vite 热更新，不 embed）
cd frontend && pnpm dev

# Custom config
make preview ARGS="--config /path/to/config.yaml"

# Environment overrides
PORT=3000 STATIC_DIR=/var/www make run
```

Then open http://localhost:80 in your browser.

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

ai:                            # AI chat (OpenAI-compatible), disabled unless api_key + base_url are set
  base_url: https://api.openai.com/v1
  api_key: ""                  # keep empty in the file — inject via AI_API_KEY env instead; never commit real keys
  model: gpt-4o-mini
  tools: false                 # enable Agent tool calling (requires function-calling capable model); env: AI_TOOLS=true

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
| `AI_BASE_URL` | `ai.base_url` | OpenAI-compatible endpoint base URL |
| `AI_API_KEY` | `ai.api_key` | AI provider API key (server-side only; prefer env over config file) |
| `AI_MODEL` | `ai.model` | Chat model name |
| `ADMIN_SETUP_TOKEN` | `auth.admin_setup_token` | One-time bootstrap token required by the first admin registration (only checked while no user exists) |

## Features

- **File Browser** — Directory tree, tabbed editor, syntax highlighting for code/markdown/JSON
- **Markdown Viewer** — Rendered markdown with Mermaid diagrams, cross-file link navigation, anchor scrolling
- **JSON Viewer** — Collapsible tree view, raw text toggle, copy raw to clipboard
- **AI Chat & Agent** — OpenAI-compatible streaming chat with 13 built-in tools (file ops / task management / event publishing / scheduled jobs); Human-in-the-loop approval for write operations; `@` file references; reasoning display; session history persisted in SQLite
- **Scheduled Tasks & Notifications** — Hand-written cron engine + in-process event bus; AI scheduled jobs (run your prompt at a cron time, results emailed); precise task-time reminders (start/due HH:mm); email delivery via SMTP 587 STARTTLS
- **Todo ↔ File Linking** — Tasks can link files (tree picker in todo editor); the file viewer shows linked tasks with jump-to-task and link-existing-task actions
- **Download & Share** — Download any file; create time-limited, access-controlled shares for files or folders
- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images with path traversal protection; gzip/br response compression
- **Route Mapping** — Clean URLs, display name aliases, redirects, directory listings
- **Token Auth** — HMAC-SHA256 authentication; all file access requires valid Bearer token (fully private mode)
- **User Management** — Admin role with a user management UI (create / edit / reset-password / delete users across orgs, per-user notification email)
- **Share Management** — Create shares with access count limits, time ranges, optional passwords; manage all shares from sidebar panel
- **Todo Board** — User/org task management with conflict detection, optimistic-lock revision sync, multi-device support
- **SQLite Storage** — All data in a single portable `workbench.db` file (WAL mode)

### AI Agent Tools (13)

When `ai.tools` is enabled, the chat agent can call these tools (write operations require in-chat approval):

| Category | Tools |
|---|---|
| Files | `list_dir` · `read_file` · `write_file` |
| Tasks | `list_tasks` · `create_task` · `update_task` |
| Scheduled jobs | `list_ai_jobs` · `create_ai_job` · `update_ai_job` · `delete_ai_job` |
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
| `/api/ai/jobs` | GET/POST | List / create AI scheduled jobs (requires token) |
| `/api/ai/jobs/{id}` | PUT/DELETE | Update / delete an AI scheduled job (requires token) |
| `/api/ai/jobs/{id}/runs` | GET | Job execution history (requires token) |
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

### Prerequisites

- Go 1.25+
- Node 18+ with pnpm
- Docker (for xgo cross-compilation)
- GitHub CLI (`gh`) with authentication

### Version Bump

1. Update version in code if needed (optional, version is auto-detected from git tag)
2. Move `[Unreleased]` entries in `CHANGELOG.md` to the new version section with date
3. Commit all changes and ensure working tree is clean

### Create Release

```bash
# 1. Create and push tag
git tag -a v1.x.x -m "Release v1.x.x - brief description"
git push origin v1.x.x

# 2. Create GitHub release (or use web UI)
gh release create v1.x.x --title "v1.x.x" --notes "Release notes..."
```

### Build & Upload Packages

```bash
# Full build: frontend + cross-platform binaries + packaging
make build-all

# This generates:
#   build/workbench-v1.x.x-linux-x86_64.tar.gz
#   build/workbench-v1.x.x-linux-arm64.tar.gz
#   build/workbench-v1.x.x-darwin-x86_64.tar.gz
#   build/workbench-v1.x.x-darwin-arm64.tar.gz
#   build/workbench-v1.x.x-win-x86_64.exe.zip

# Upload to GitHub release
gh release upload v1.x.x build/workbench-v1.x.x-* --clobber
```

### Build Targets

| Target | Description |
|--------|-------------|
| `make all` | Frontend build + Go build (for local dev) |
| `make build` | Go build only (requires frontend/dist exists) |
| `make frontend` | Vite build to frontend/dist |
| `make build-all` | Full cross-platform build + package (uses xgo) |
| `make package` | Create tar.gz/zip from existing binaries |

### Package Contents

Each release package contains:
- `workbench` (or `workbench.exe`) — compiled binary with embedded frontend
- `config.yaml` — configuration template
- `README.md` — documentation

### Quick Release Checklist

- [ ] All changes committed and pushed
- [ ] Tests pass: `make check`
- [ ] Version tag created: `git tag -a v1.x.x`
- [ ] Tag pushed: `git push origin v1.x.x`
- [ ] GitHub release created
- [ ] `make build-all` completes successfully
- [ ] All 5 platform packages uploaded to release

## License

MIT
