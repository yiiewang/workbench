#!/bin/bash
set -e

# Workbench launcher
# Usage: ./preview.sh [--config config.yaml]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="$SCRIPT_DIR/workbench"

# Build if binary outdated
if [[ ! -f "$BINARY" ]] || [[ \
    "$SCRIPT_DIR/cmd/workbench/main.go" -nt "$BINARY" || \
    "$SCRIPT_DIR/internal/server/server.go" -nt "$BINARY" || \
    "$SCRIPT_DIR/internal/db/db.go" -nt "$BINARY" || \
    "$SCRIPT_DIR/internal/config/config.go" -nt "$BINARY" \
   ]]; then
    echo "Building workbench..."
    cd "$SCRIPT_DIR"
    go build -o "$BINARY" ./cmd/workbench/
fi

exec "$BINARY" "$@"
