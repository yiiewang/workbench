# Workbench

Personal dev platform — a collection of tools and services for daily development workflows. Currently includes a preview server with visit tracking and auth.

## Features

### Preview Server

A lightweight Python HTTP server for serving static files.

- **Static File Serving** — HTML, Markdown, JSON, CSS, JS, images, and more
- **Route Mapping** — Clean URLs, display name aliases, redirects, and directory listings (configured via `config.json`)
- **Visit Tracking** — Per-visitor and per-page statistics at `GET /__stats__`
- **Token Auth** — HMAC-SHA256 based authentication for write operations
- **Todo Board Backend** — User/organization task management via `tasks.json` with password-protected write API
- **Zero Dependencies** — Python 3 standard library only

## Quick Start

```bash
# Start on default port 8080, serving ./html directory
python3 server.py

# Or use the shell launcher
chmod +x preview.sh
./preview.sh

# Custom port
PORT=3000 python3 server.py

# Custom serve directory
python3 server.py /path/to/your/files
```

Then open http://localhost:8080 in your browser.

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
| `LOG_DIR` | `~/.local/state/preview-server` | Directory for access logs |

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/__stats__` | GET | Visit statistics (JSON) |
| `/__map__` | GET | Route configuration map (JSON) |
| `/api/org-members` | GET | List org members |
| `/api/login` | POST | Login, returns JWT-like token |
| `/api/set-password` | POST | Set or change password |
| `/api/me` | GET | Current user info (requires token) |
| `/tasks.json` | GET | Task data (passwords filtered) |
| `/tasks.json` | PUT | Update task data (requires token) |

### Auth Flow

1. `POST /api/set-password` — Set initial password for a user
2. `POST /api/login` — Login with `orgId`, `userId`, `password` to get a token
3. Use `Authorization: Bearer <token>` header for write operations

Tokens expire after 30 days by default (configurable via `TOKEN_EXPIRY_DAYS` in source).

## Directory Structure

```
workbench/
├── server.py          # Preview server application
├── preview.sh         # Shell launcher script
├── README.md
├── .gitignore
└── html/              # Default serve directory
    ├── config.json    # Route configuration
    ├── index.html     # Home page
    ├── todo.html      # Example todo board
    └── tasks.json     # Sample user/task data
```

## License

MIT
