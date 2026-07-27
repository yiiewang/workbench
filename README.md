# Workbench

Personal dev platform — file browser, markdown viewer, todo board, and API tools for daily development workflows. Built with Go + SQLite.

## Quick Start

```bash
# Build
go build -o workbench ./cmd/workbench/

# Run (with default config.yaml)
./workbench

# Or use the launcher (auto-builds on source change)
./preview.sh

# Custom config
./workbench --config /path/to/config.yaml

# Environment overrides
PORT=3000 STATIC_DIR=/var/www ./workbench
```

Then open http://localhost:80 in your browser.

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
- **Download & Share** — Download any file; share UI link (browser) or raw content link (curl)
- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images with path traversal protection
- **Route Mapping** — Clean URLs, display name aliases, redirects, directory listings
- **Visit Tracking** — Per-visitor and per-page statistics persisted in SQLite (`GET /__stats__`)
- **Token Auth** — HMAC-SHA256 authentication for write operations
- **Todo Board** — User/org task management with conflict detection, version sync, multi-device support
- **SQLite Storage** — All data in a single portable `workbench.db` file (WAL mode)
- **CORS Security** — Sandboxed iframe preview with null-origin CORS (no token leakage)

## API Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/__stats__` | GET | Visit statistics |
| `/__map__` | GET | Route configuration map |
| `/__tree__` | GET | File directory tree (JSON) |
| `/api/org-members` | GET | List org members (`?orgId=xxx`) |
| `/api/login` | POST | Login (`{orgId, userId, password}`) |
| `/api/set-password` | POST | Set/change password |
| `/api/me` | GET | Current user info (requires token) |
| `/tasks.json` | GET | Task data with version info (passwords filtered) |
| `/tasks.json` | PUT | Update tasks + version (requires `Authorization: Bearer <token>`) |
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

## Security

- **Path Traversal Protection** — `safeJoin` blocks `../` attacks (literal and URL-encoded)
- **Symlink Guard** — Default `allow_symlink: false` resolves symlinks and verifies real path stays within `static_dir`
- **CORS for Sandboxed iframe** — Preview iframe uses `sandbox` without `allow-same-origin`; API responses include `Access-Control-Allow-Origin: null` to allow script execution without token leakage
- **Token Auth** — Bearer token (HMAC-SHA256), not stored in cookies; write operations require valid token

## Directory Structure

```
workbench/
├── cmd/workbench/main.go       # Entry point
├── internal/
│   ├── config/config.go        # Config loading (YAML + env overrides)
│   ├── db/db.go                # SQLite data layer, migrations, FlexString type
│   └── server/
│       ├── server.go           # HTTP handlers, file serving, CORS, raw endpoint
│       └── auth.go             # Token authentication
├── static/                     # Source files (index.html, todo.html)
├── preview/                    # Default serve directory (symlinks to static/)
├── config.yaml                 # Configuration file
├── data/                       # Runtime data (gitignored)
├── preview.sh                  # Launcher script
├── go.mod / go.sum
├── README.md
└── .gitignore
```

## License

MIT
