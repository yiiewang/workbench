#!/bin/bash
set -e

# Preview Server launcher
# Usage: ./preview.sh [html_directory]
#        PORT=8080 ./preview.sh [html_directory]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HTML_DIR="${1:-$SCRIPT_DIR/html}"

python3 "$SCRIPT_DIR/server.py" "$HTML_DIR"
