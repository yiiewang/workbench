# Workbench

Personal dev platform — a collection of tools and services for daily development workflows. Built with Go + SQLite.

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

Then open http://localhost:8080 in your browser.

## Configuration

All settings in `config.yaml`:

```yaml
server:
  port: 8080              # HTTP listen port
  static_dir: ./static    # Directory to serve

database:
  path: ./data/workbench.db  # SQLite file path

auth:
  token_expiry_days: 30   # Token validity

routes:                   # URL routing
  "/todo": "/todo.html"   # 302 redirect
  "/index": "__listdir__"  # Directory listing
  "/todo.html": "Todo Board"  # Display name

logging:
  dir: ~/.local/state/workbench  # Access log directory
```

### Environment Variables

| Variable    | Config Override | Description |
|-------------|-----------------|-------------|
| `PORT`      | `server.port`   | HTTP listen port |
| `STATIC_DIR`| `server.static_dir` | Static file directory |
| `DB_PATH`   | `database.path` | SQLite database path |
| `LOG_DIR`   | `logging.dir`   | Access log directory |

## Features

- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images
- **Route Mapping** — Clean URLs, display name aliases, redirects, directory listings
- **Visit Tracking** — Per-visitor and per-page statistics persisted in SQLite (`GET /__stats__`)
- **Token Auth** — HMAC-SHA256 authentication for write operations
- **Todo Board Backend** — User/organization task management with password-protected API
- **SQLite Storage** — All data in a single portable `workbench.db` file (WAL mode)

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/__stats__` | GET | Visit statistics |
| `/__map__` | GET | Route configuration map |
| `/api/org-members` | GET | List org members (`?orgId=xxx`) |
| `/api/login` | POST | Login (`{orgId, userId, password}`) |
| `/api/set-password` | POST | Set/change password |
| `/api/me` | GET | Current user info (requires token) |
| `/tasks.json` | GET | Task data (passwords filtered) |
| `/tasks.json` | PUT | Update tasks (requires `Authorization: Bearer <token>`) |

## Database

SQLite database (`data/workbench.db`, WAL mode):

| Table | Description |
|-------|-------------|
| `visit_logs` | Per-request access records |
| `orgs` | Organizations |
| `users` | Users with SHA-256 password hashes |
| `tasks` | Per-user task lists |

## Directory Structure

```
workbench/
├── cmd/workbench/main.go       # Entry point
├── internal/
│   ├── config/config.go        # Config loading (YAML)
│   ├── db/db.go                # SQLite data layer
│   └── server/
│       ├── server.go           # HTTP handlers, file serving, logging
│       └── auth.go             # Token authentication
├── static/                     # Default serve directory
│   ├── index.html
│   ├── todo.html
│   └── tasks.json
├── config.yaml                 # Configuration file
├── data/                       # Runtime data (gitignored)
├── preview.sh                  # Launcher script
├── go.mod / go.sum
├── README.md
└── .gitignore
```

## License

MIT
