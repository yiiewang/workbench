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

On first launch the `users` table is empty. The **first user to set a password automatically becomes the super admin** and gains access to the User Management panel:

```bash
curl -s -X POST http://localhost/api/set-password \
  -H "Content-Type: application/json" \
  -d '{"orgId":"cm","userId":"yiiewang","newPassword":"your-password"}'
```

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
  token_expiry_days: 30       # Token validity

routes:                       # URL routing
  "/todo": "/todo.html"       # 302 redirect
  "/index": "__listdir__"     # Directory listing
  "/todo.html": "Todo Board"  # Display name

logging:
  dir: ~/.local/state/workbench  # Access log directory
  level: info                    # Operational log level: debug | info | warn | error (structured JSON to stdout)
```

### Environment Variables

| Variable | Config Override | Description |
|---|---|---|
| `PORT` | `server.port` | HTTP listen port |
| `STATIC_DIR` | `server.static_dir` | Static file directory |
| `DB_PATH` | `database.path` | SQLite database path |
| `LOG_DIR` | `logging.dir` | Access log directory |
| `ALLOW_SYMLINK` | `server.allow_symlink` | Set `true` or `1` to allow symlink access outside static_dir |

## Features

- **File Browser** — Directory tree, tabbed editor, syntax highlighting for code/markdown/JSON
- **Markdown Viewer** — Rendered markdown with Mermaid diagrams, cross-file link navigation, anchor scrolling
- **JSON Viewer** — Collapsible tree view, raw text toggle, copy raw to clipboard
- **Download & Share** — Download any file; create time-limited, access-controlled shares for files or folders
- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images with path traversal protection
- **Route Mapping** — Clean URLs, display name aliases, redirects, directory listings
- **Visit Tracking** — Per-visitor and per-page statistics persisted in SQLite (`GET /__stats__`)
- **Token Auth** — HMAC-SHA256 authentication; all file access requires valid Bearer token (fully private mode)
- **User Management** — Admin role with a user management UI (create / edit / reset-password / delete users across orgs)
- **Share Management** — Create shares with access count limits, time ranges, optional passwords; manage all shares from sidebar panel
- **Todo Board** — User/org task management with conflict detection, version sync, multi-device support
- **SQLite Storage** — All data in a single portable `workbench.db` file (WAL mode)
- **CORS Security** — Sandboxed iframe preview with null-origin CORS (no token leakage)

## API Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/__stats__` | GET | Visit statistics (requires token) |
| `/__map__` | GET | Route configuration map (requires token) |
| `/__tree__` | GET | File directory tree (JSON, requires token) |
| `/api/org-members` | GET | List org members (`?orgId=xxx`, requires token) |
| `/api/login` | POST | Login (`{orgId, userId, password}`) |
| `/api/set-password` | POST | Set/change password |
| `/api/me` | GET | Current user info (requires token) |
| `/tasks.json` | GET | Task data with version info (requires token) |
| `/tasks.json` | PUT | Update tasks + version (requires `Authorization: Bearer <token>`) |
| `/api/share` | GET | List my shares (requires token) |
| `/api/share` | POST | Create share `{resourcePath, resourceType, maxAccessCount, password, effectiveAt, expiresAt}` (requires token) |
| `/api/share/{id}` | DELETE | Revoke share (requires token) |
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

## Security

- **Path Traversal Protection** — `safeJoin` blocks `../` attacks (literal and URL-encoded)
- **Symlink Guard** — Default `allow_symlink: false` resolves symlinks and verifies real path stays within `static_dir`
- **CORS for Sandboxed iframe** — Preview iframe uses `sandbox` without `allow-same-origin`; API responses include `Access-Control-Allow-Origin: null` to allow script execution without token leakage
- **Token Auth** — Bearer token (HMAC-SHA256), not stored in cookies; write operations require valid token

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

## License

MIT
