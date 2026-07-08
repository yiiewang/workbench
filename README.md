# Workbench

Personal dev platform — a collection of tools and services for daily development workflows. Built with Go + SQLite.

## Features

- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images, and more
- **Route Mapping** — Clean URLs, display name aliases, redirects, and directory listings (configured via `config.json`)
- **Visit Tracking** — Per-visitor and per-page statistics persisted in SQLite at `GET /__stats__`
- **Token Auth** — HMAC-SHA256 based authentication for write operations
- **Todo Board Backend** — User/organization task management with password-protected write API
- **SQLite Storage** — All data (visits, users, tasks) stored in a single portable `workbench.db` file

## Quick Start

```bash
# Build and run
go build -o workbench
./workbench

# Or use the shell launcher (auto-builds)
chmod +x preview.sh
./preview.sh

# Custom port
PORT=3000 ./workbench

# Custom serve directory
./workbench /path/to/your/files
```

Then open http://localhost:8080 in your browser.

## Dependencies

- Go 1.21+
- modernc.org/sqlite (pure Go SQLite driver, no CGo required)

```bash
go mod tidy
```

## Configuration

### Route Config (`html/config.json`)

```json
{
  "route_config": {
    "/todo": "/todo.html",           // Redirect /todo to /todo.html
    "/index": "__listdir__",         // __listdir__ shows file listing
    "/todo.html": "Todo Board"       // Display name in file listing
  }
}
```

### Environment Variables

| Variable  | Default | Description |
|-----------|---------|-------------|
| `PORT`    | `8080`  | HTTP listen port |
| `LOG_DIR` | `~/.local/state/workbench` | Directory for access logs |

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/__stats__` | GET | Visit statistics (JSON) |
| `/__map__` | GET | Route configuration map (JSON) |
| `/api/org-members` | GET | List org members |
| `/api/login` | POST | Login, returns token |
| `/api/set-password` | POST | Set or change password |
| `/api/me` | GET | Current user info (requires token) |
| `/tasks.json` | GET | Task data (passwords filtered) |
| `/tasks.json` | PUT | Update task data (requires token) |

### Auth Flow

1. `POST /api/set-password` — Set initial password for a user
2. `POST /api/login` — Login with `orgId`, `userId`, `password` to get a token
3. Use `Authorization: Bearer <token>` header for write operations

Tokens expire after 30 days by default.

## Database

Data is stored in `html/workbench.db` (SQLite, WAL mode):

- `visit_logs` — Per-request access records with visitor/IP/path/status
- `orgs` — Organizations
- `users` — Users with password hashes (SHA-256)
- `tasks` — Per-user task lists

## Directory Structure

```
workbench/
├── main.go            # Entry point
├── server.go          # HTTP handlers
├── auth.go            # Token authentication
├── db.go              # SQLite data layer
├── preview.sh         # Shell launcher (auto-build)
├── README.md
├── .gitignore
└── html/              # Default serve directory
    ├── config.json    # Route configuration
    ├── index.html     # Home page
    ├── todo.html      # Example todo board
    └── tasks.json     # (optional) legacy task data
```

## License

MIT
