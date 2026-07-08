#!/usr/bin/env python3
"""
Preview Server - static file server with visit tracking and auth support.

Features:
- Serve static files (HTML, Markdown, JSON, etc.)
- Route mapping with clean URLs and display names
- Visit tracking with per-visitor and per-page statistics (GET /__stats__)
- Token-based authentication (HMAC-SHA256)
- Organization/user management via tasks.json
- File listing with customizable display names

Usage:
    python3 server.py [serve_directory]           # default: ./html
    PORT=8080 python3 server.py [serve_directory] # custom port
"""

import http.server
import socketserver
import json
import os
import sys
import signal
import html as html_mod
import urllib.parse
import io
import hashlib
import hmac
import base64
import time
from datetime import datetime
from collections import defaultdict

# ---- Configuration ----
PORT = int(os.environ.get("PORT", 8080))
LOG_DIR = os.path.expanduser(os.environ.get("LOG_DIR", "~/.local/state/preview-server"))
LOG_FILE = os.path.join(LOG_DIR, "preview.log")
TOKEN_EXPIRY_DAYS = 30
# ---- End Configuration ----


# ============================================================
# Route config loader
# ============================================================
def load_route_config(serve_dir: str) -> dict:
    """Load route_config from config.json in the serve directory, fallback to empty."""
    config_path = os.path.join(serve_dir, "config.json")
    if os.path.exists(config_path):
        try:
            with open(config_path, "r", encoding="utf-8") as f:
                return json.load(f).get("route_config", {})
        except Exception:
            pass
    return {}


# ============================================================
# Visit Tracker
# ============================================================
class VisitTracker:
    def __init__(self):
        self.visitors = defaultdict(lambda: {"count": 0, "pages": set(), "first_visit": None, "last_visit": None})
        self.page_views = defaultdict(int)

    def record_visit(self, visitor_id, path):
        if visitor_id not in self.visitors:
            self.visitors[visitor_id]["first_visit"] = datetime.now()
        self.visitors[visitor_id]["count"] += 1
        self.visitors[visitor_id]["pages"].add(path)
        self.visitors[visitor_id]["last_visit"] = datetime.now()
        self.page_views[path] += 1

    def get_stats(self):
        stats = {
            "total_visitors": len(self.visitors),
            "total_page_views": sum(v["count"] for v in self.visitors.values()),
            "visitors": {},
            "top_pages": dict(sorted(self.page_views.items(), key=lambda x: -x[1])[:10]),
        }
        for visitor_id, data in self.visitors.items():
            duration = (data["last_visit"] - data["first_visit"]).total_seconds()
            stats["visitors"][visitor_id] = {
                "visits": data["count"],
                "pages_visited": len(data["pages"]),
                "duration_seconds": int(duration),
                "first_visit": data["first_visit"].strftime("%H:%M:%S"),
                "last_visit": data["last_visit"].strftime("%H:%M:%S"),
            }
        return stats


# ============================================================
# Token auth
# ============================================================
def _load_token_secret(serve_dir: str) -> bytes:
    """Load or generate token signing secret, stored at .token_secret in serve_dir."""
    secret_path = os.path.join(serve_dir, ".token_secret")
    if os.path.exists(secret_path):
        with open(secret_path, "rb") as f:
            return f.read()
    secret = os.urandom(32)
    with open(secret_path, "wb") as f:
        f.write(secret)
    os.chmod(secret_path, 0o600)
    return secret


def _hash_password(password: str) -> str:
    return hashlib.sha256(password.encode("utf-8")).hexdigest()


def _generate_token(user_id: str, secret: bytes) -> str:
    expiry = int(time.time()) + TOKEN_EXPIRY_DAYS * 86400
    payload = f"{user_id}:{expiry}"
    sig = hmac.new(secret, payload.encode("utf-8"), hashlib.sha256).digest()
    raw = f"{payload}:{sig.hex()}"
    return base64.urlsafe_b64encode(raw.encode("utf-8")).decode("utf-8")


def _validate_token(token: str, secret: bytes):
    """Return (valid: bool, user_id: str)."""
    try:
        raw = base64.urlsafe_b64decode(token.encode("utf-8")).decode("utf-8")
        parts = raw.split(":")
        if len(parts) != 3:
            return False, ""
        user_id, expiry_str, sig_hex = parts
        expiry = int(expiry_str)
        if time.time() > expiry:
            return False, ""
        payload = f"{user_id}:{expiry_str}"
        expected_sig = hmac.new(secret, payload.encode("utf-8"), hashlib.sha256).digest().hex()
        if not hmac.compare_digest(sig_hex, expected_sig):
            return False, ""
        return True, user_id
    except Exception:
        return False, ""


def _extract_token(handler) -> str:
    auth = handler.headers.get("Authorization", "")
    if auth.startswith("Bearer "):
        return auth[7:]
    return ""


# ============================================================
# HTTP Request Handler
# ============================================================
class PreviewHandler(http.server.SimpleHTTPRequestHandler):
    serve_directory = ""
    route_config = {}
    token_secret = b""

    def translate_path(self, path):
        translated = super().translate_path(path)
        return os.path.join(self.serve_directory, os.path.relpath(translated, start=os.getcwd()))

    def log_message(self, format, *args):
        ip = self.client_address[0] if self.client_address else "unknown"
        try:
            ua = self.headers.get("User-Agent", "unknown")
        except AttributeError:
            ua = "unknown"
        visitor_id = f"{ip}|{ua[:50]}"
        path = getattr(self, "path", "unknown")
        tracker.record_visit(visitor_id, path)

        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        status = args[1] if len(args) > 1 else "200"
        log_line = f"{timestamp} | {visitor_id} | {path} | {status}\n"
        try:
            with open(LOG_FILE, "a") as f:
                f.write(log_line)
        except Exception:
            pass
        print(log_line.strip())

    def do_GET(self):
        # Route mapping
        route = self.route_config
        if self.path in route:
            target = route[self.path]
            if target == "__listdir__":
                f = self.list_directory(self.serve_directory)
                if f:
                    try:
                        self.copyfile(f, self.wfile)
                    finally:
                        f.close()
                return
            elif isinstance(target, str) and target.startswith("/"):
                self.send_response(302)
                self.send_header("Location", target)
                self.end_headers()
                return

        # Special endpoints
        if self.path == "/__stats__":
            self.send_stats()
        elif self.path in ("/__map__", "/__map__.json"):
            display_map = {k: v for k, v in route.items() if not str(v).startswith("__")}
            self.send_response(200)
            self.send_header("Content-type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(display_map, ensure_ascii=False, indent=2).encode())
        elif self.path.startswith("/api/org-members"):
            self._send_org_members()
        elif self.path == "/api/me":
            self._send_me()
        elif self.path == "/tasks.json":
            self._send_tasks_json()
        else:
            # Check display-name access
            decoded_path = urllib.parse.unquote(self.path)
            for filepath, display_name in route.items():
                if not filepath.startswith("/") or str(display_name).startswith("__"):
                    continue
                if decoded_path.endswith("/" + display_name) or decoded_path == "/" + display_name:
                    original_path = self.path
                    self.path = filepath
                    super().do_GET()
                    self.path = original_path
                    return
            super().do_GET()

    def list_directory(self, path):
        try:
            entries = os.listdir(path)
        except OSError:
            self.send_error(404, "No permission to list directory")
            return None

        dirs = sorted([n for n in entries if os.path.isdir(os.path.join(path, n))])
        files = sorted([n for n in entries if not os.path.isdir(os.path.join(path, n))])

        r = []
        display_path = html_mod.escape(urllib.parse.unquote(self.path))
        route = self.route_config

        if display_path and display_path != "/":
            parent_path = display_path.rsplit("/", 1)[0] or "/"
            r.append(f'<li><a href="{parent_path}">../</a></li>')

        for name in dirs:
            display_name = name
            for fp, mn in route.items():
                fname = fp.lstrip("/")
                if name == fname and not str(mn).startswith("__"):
                    display_name = mn
                    break
            r.append(f'<li><a href="{html_mod.escape(name)}/">{html_mod.escape(display_name)}/</a></li>')

        for name in files:
            display_name = name
            for fp, mn in route.items():
                fname = fp.lstrip("/")
                if name == fname and not str(mn).startswith("__"):
                    display_name = mn
                    break
            base = display_path.rstrip("/")
            href = urllib.parse.quote(name)
            file_href = f"{base}/{href}" if base else f"/{href}"
            r.append(f'<li><a href="{file_href}">{html_mod.escape(display_name)}</a></li>')

        content = f"""<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Index of {display_path}</title>
</head>
<body>
<h1>Index of {display_path}</h1>
<ul>
{"".join(r)}
</ul>
</body>
</html>"""

        encoded = content.encode("utf-8", errors="surrogateescape")
        f = io.BytesIO()
        f.write(encoded)
        f.seek(0)
        self.send_response(200)
        self.send_header("Content-type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        return f

    def send_stats(self):
        stats = tracker.get_stats()
        self.send_response(200)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(stats, indent=2, ensure_ascii=False).encode())

    # ---- tasks.json helpers ----

    def _read_tasks_json(self):
        filepath = os.path.join(self.serve_directory, "tasks.json")
        if not os.path.exists(filepath):
            return None, filepath
        try:
            with open(filepath, "r", encoding="utf-8") as f:
                return json.load(f), filepath
        except Exception:
            return None, filepath

    def _write_tasks_json(self, data):
        filepath = os.path.join(self.serve_directory, "tasks.json")
        with open(filepath, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)

    def _filter_password(self, obj):
        if isinstance(obj, dict):
            return {k: self._filter_password(v) for k, v in obj.items() if k != "password"}
        elif isinstance(obj, list):
            return [self._filter_password(item) for item in obj]
        return obj

    def _send_tasks_json(self):
        filepath = os.path.join(self.serve_directory, "tasks.json")
        if not os.path.exists(filepath):
            self.send_error(404, "tasks.json not found")
            return
        try:
            with open(filepath, "r", encoding="utf-8") as f:
                data = json.load(f)
            filtered = self._filter_password(data)
            self.send_response(200)
            self.send_header("Content-type", "application/json")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            self.wfile.write(json.dumps(filtered, indent=2, ensure_ascii=False).encode())
        except Exception as e:
            self.send_error(500, f"Error reading tasks.json: {str(e)}")

    def _send_org_members(self):
        from urllib.parse import urlparse, parse_qs

        parsed = urlparse(self.path)
        params = parse_qs(parsed.query)
        org_id = params.get("orgId", ["org_default"])[0]

        filepath = os.path.join(self.serve_directory, "tasks.json")
        members = []
        if os.path.exists(filepath):
            try:
                with open(filepath, "r", encoding="utf-8") as f:
                    data = json.load(f)
                org = (data.get("orgs") or {}).get(org_id)
                if org:
                    members = [k for k, v in org.items() if isinstance(v, dict) and "tasks" in v]
            except Exception:
                members = []

        self.send_response(200)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"members": members}, ensure_ascii=False).encode())

    # ---- Auth endpoints ----

    def _send_me(self):
        token = _extract_token(self)
        if not token:
            self.send_error(401, "Unauthorized: Missing token")
            return
        valid, user_id = _validate_token(token, self.token_secret)
        if not valid:
            self.send_error(401, "Unauthorized: Invalid or expired token")
            return
        data, _ = self._read_tasks_json()
        org_id = ""
        if data:
            for oid, org in (data.get("orgs") or {}).items():
                if isinstance(org, dict) and user_id in org:
                    org_id = oid
                    break
        self.send_response(200)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({
            "userId": user_id,
            "orgId": org_id,
            "exp": int(time.time()) + TOKEN_EXPIRY_DAYS * 86400,
        }, ensure_ascii=False).encode())

    def do_POST(self):
        if self.path == "/api/login":
            self._handle_login()
        elif self.path == "/api/set-password":
            self._handle_set_password()
        else:
            self.send_error(404, "Not Found")

    def _handle_login(self):
        try:
            content_length = int(self.headers.get("Content-Length", 0))
            if content_length == 0:
                self.send_error(411, "Length Required")
                return
            body = self.rfile.read(content_length)
            payload = json.loads(body.decode("utf-8"))
            org_id = (payload.get("orgId") or "").strip()
            user_id = (payload.get("userId") or "").strip()
            password = payload.get("password") or ""
        except (json.JSONDecodeError, Exception):
            self.send_error(400, "Invalid request body")
            return

        if not org_id or not user_id or not password:
            self.send_error(400, "orgId, userId and password are required")
            return

        data, _ = self._read_tasks_json()
        if not data:
            self.send_error(500, "tasks.json not found")
            return

        org = (data.get("orgs") or {}).get(org_id)
        if not org or not isinstance(org, dict):
            self.send_error(404, f"Organization '{org_id}' not found")
            return

        user = org.get(user_id)
        if not user or not isinstance(user, dict) or "tasks" not in user:
            self.send_response(403)
            self.send_header("Content-type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "error": "password_not_set",
                "message": "Password not set. Please set password first.",
            }, ensure_ascii=False).encode())
            return

        stored_pwd = user.get("password", "")
        if not stored_pwd:
            self.send_response(403)
            self.send_header("Content-type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "error": "password_not_set",
                "message": "Password not set. Please set password first.",
            }, ensure_ascii=False).encode())
            return

        if stored_pwd != _hash_password(password):
            self.send_error(401, "Invalid password")
            return

        token = _generate_token(user_id, self.token_secret)
        self.send_response(200)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({
            "token": token,
            "user": {"userId": user_id, "orgId": org_id},
        }, ensure_ascii=False).encode())

    def _handle_set_password(self):
        try:
            content_length = int(self.headers.get("Content-Length", 0))
            if content_length == 0:
                self.send_error(411, "Length Required")
                return
            body = self.rfile.read(content_length)
            payload = json.loads(body.decode("utf-8"))
            org_id = (payload.get("orgId") or "").strip()
            user_id = (payload.get("userId") or "").strip()
            old_password = payload.get("oldPassword") or ""
            new_password = payload.get("newPassword") or ""
        except (json.JSONDecodeError, Exception):
            self.send_error(400, "Invalid request body")
            return

        if not org_id or not user_id or not new_password:
            self.send_error(400, "orgId, userId and newPassword are required")
            return

        data, _ = self._read_tasks_json()
        if not data:
            self.send_error(500, "tasks.json not found")
            return

        org = (data.get("orgs") or {}).get(org_id)
        if not org or not isinstance(org, dict):
            self.send_error(404, f"Organization '{org_id}' not found")
            return

        user = org.get(user_id)
        if not user or not isinstance(user, dict) or "tasks" not in user:
            org[user_id] = {"version": {"md5": "init"}, "tasks": []}
            user = org[user_id]

        stored_pwd = user.get("password", "")
        if stored_pwd:
            if not old_password:
                self.send_error(400, "oldPassword is required to change password")
                return
            if stored_pwd != _hash_password(old_password):
                self.send_error(401, "Invalid old password")
                return

        user["password"] = _hash_password(new_password)
        self._write_tasks_json(data)

        token = _generate_token(user_id, self.token_secret)
        self.send_response(200)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({
            "token": token,
            "user": {"userId": user_id, "orgId": org_id},
        }, ensure_ascii=False).encode())

    def do_PUT(self):
        if self.path != "/tasks.json":
            self.send_error(403, "Forbidden: Only tasks.json can be updated")
            return

        token = _extract_token(self)
        if not token:
            self.send_error(401, "Unauthorized: Missing token")
            return
        valid, user_id = _validate_token(token, self.token_secret)
        if not valid:
            self.send_error(401, "Unauthorized: Invalid or expired token")
            return

        try:
            content_length = int(self.headers.get("Content-Length", 0))
            if content_length == 0:
                self.send_error(411, "Length Required")
                return
            body = self.rfile.read(content_length)
            data = json.loads(body.decode("utf-8"))
        except json.JSONDecodeError:
            self.send_error(400, "Invalid JSON")
            return
        except Exception as e:
            self.send_error(500, f"Internal Server Error: {str(e)}")
            return

        old_data, _ = self._read_tasks_json()
        if old_data is None:
            self.send_error(500, "tasks.json not found on server")
            return

        # Safe merge: only accept data for the authenticated user
        merged = json.loads(json.dumps(old_data))
        new_orgs = data.get("orgs") or {}
        for org_id, new_org in new_orgs.items():
            if not isinstance(new_org, dict):
                continue
            if org_id not in merged["orgs"]:
                merged["orgs"][org_id] = {}
            for member_id, new_member in new_org.items():
                if not isinstance(new_member, dict):
                    continue
                if member_id == user_id:
                    old_member = (old_data.get("orgs") or {}).get(org_id, {}).get(member_id, {})
                    merged_member = dict(old_member)
                    merged_member["version"] = new_member.get("version", old_member.get("version"))
                    merged_member["tasks"] = new_member.get("tasks", old_member.get("tasks", []))
                    for k, v in new_member.items():
                        if k not in ("version", "tasks"):
                            merged_member[k] = v
                    merged["orgs"][org_id][member_id] = merged_member

        for key in ("version", "lastUpdated", "config"):
            if key in data:
                merged[key] = data[key]

        filepath = os.path.join(self.serve_directory, "tasks.json")
        with open(filepath, "w", encoding="utf-8") as f:
            json.dump(merged, f, indent=2, ensure_ascii=False)

        self.send_response(200)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"status": "ok", "file": "tasks.json"}).encode())
        print(f"Updated tasks.json for user {user_id}")

    def send_head(self):
        path = self.translate_path(self.path)

        if os.path.isdir(path):
            for index in ("index.html", "index.htm"):
                index_path = os.path.join(path, index)
                if os.path.exists(index_path):
                    path = index_path
                    break
            else:
                return self.list_directory(path)

        if not os.path.exists(path):
            self.send_error(404, "File not found")
            return None

        try:
            with open(path, "r", encoding="utf-8") as f:
                content = f.read()
            encoded = content.encode("utf-8")
            f = io.BytesIO()
            f.write(encoded)
            f.seek(0)
            ext = os.path.splitext(path)[1].lower()
            content_type = _get_content_type(ext)
            self.send_response(200)
            self.send_header("Content-type", f"{content_type}; charset=utf-8")
            self.send_header("Content-Length", str(len(encoded)))
            self.send_header("Last-Modified", self.date_time_string(os.path.getmtime(path)))
            self.end_headers()
            return f
        except UnicodeDecodeError:
            return super().send_head()


def _get_content_type(ext: str) -> str:
    content_types = {
        ".txt": "text/plain",
        ".md": "text/markdown",
        ".json": "application/json",
        ".xml": "application/xml",
        ".html": "text/html",
        ".htm": "text/html",
        ".css": "text/css",
        ".js": "application/javascript",
        ".py": "text/x-python",
        ".go": "text/x-go",
    }
    return content_types.get(ext, "application/octet-stream")


# ============================================================
# Entry point
# ============================================================
tracker = VisitTracker()

if __name__ == "__main__":
    # Parse command line args
    if len(sys.argv) > 1:
        serve_dir = sys.argv[1]
    else:
        serve_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "html")

    # Ensure log directory exists
    os.makedirs(LOG_DIR, exist_ok=True)

    # Load route config
    route_config = load_route_config(serve_dir)

    # Set class-level config
    PreviewHandler.serve_directory = serve_dir
    PreviewHandler.route_config = route_config
    PreviewHandler.token_secret = _load_token_secret(serve_dir)

    def signal_handler(sig, frame):
        print("\nServer stopped")
        sys.exit(0)

    signal.signal(signal.SIGINT, signal_handler)

    class ReusableTCPServer(socketserver.TCPServer):
        allow_reuse_address = True

    with ReusableTCPServer(("0.0.0.0", PORT), PreviewHandler) as httpd:
        print(f"Preview server started: http://localhost:{PORT}")
        print(f"Serving directory: {os.path.abspath(serve_dir)}")
        print(f"Visit stats: http://localhost:{PORT}/__stats__")
        print(f"Log file: {LOG_FILE}")
        print("Press Ctrl+C to stop\n")
        httpd.serve_forever()
