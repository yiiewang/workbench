#!/bin/bash
set -e

# Workbench launcher
# Usage: ./preview.sh [serve_directory]
#        PORT=8080 ./preview.sh [serve_directory]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HTML_DIR="${1:-$SCRIPT_DIR/html}"
BINARY="$SCRIPT_DIR/workbench"

# Build if binary doesn't exist or source changed
if [[ ! -f "$BINARY" ]] || [[ "$SCRIPT_DIR/main.go" -nt "$BINARY" ]]; then
    echo "Building workbench..."
    cd "$SCRIPT_DIR"
    go build -o "$BINARY" .
fi

exec "$BINARY" "$HTML_DIR"
