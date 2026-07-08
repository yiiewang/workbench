// Package server HTTP 请求处理、静态文件服务、目录列表、访问日志中间件
package server

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// Server HTTP 服务
type Server struct {
	db          *db.DB
	cfg         *config.Config
	serDirAbs   string
	tokenSecret []byte
	logFile     string
}

// New 创建 Server 实例
func New(database *db.DB, cfg *config.Config, tokenSecret []byte, logFile string) (*Server, error) {
	absDir, err := filepath.Abs(config.ResolvePath(cfg.Server.StaticDir))
	if err != nil {
		return nil, fmt.Errorf("resolve static dir: %w", err)
	}
	os.MkdirAll(absDir, 0755)

	return &Server{
		db:          database,
		cfg:         cfg,
		serDirAbs:   absDir,
		tokenSecret: tokenSecret,
		logFile:     logFile,
	}, nil
}

// Handler 返回 http.Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/__stats__", s.handleStats)
	mux.HandleFunc("/__map__", s.handleMap)
	mux.HandleFunc("/__map__.json", s.handleMap)
	mux.HandleFunc("/tasks.json", s.handleTasksJSON)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/set-password", s.handleSetPassword)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/org-members", s.handleOrgMembers)
	mux.HandleFunc("/", s.handleStatic)
	return withLogging(mux, s)
}

func (s *Server) expiryDays() int {
	if d := s.cfg.Auth.TokenExpiryDays; d > 0 {
		return d
	}
	return 30
}

// ============================================================
// 路由映射
// ============================================================

func (s *Server) handleRouteRedirect(w http.ResponseWriter, r *http.Request) bool {
	target, exists := s.cfg.Routes[r.URL.Path]
	if !exists {
		return false
	}
	if target == "__listdir__" {
		s.listDirectory(w, r, s.serDirAbs)
		return true
	}
	if strings.HasPrefix(target, "/") {
		http.Redirect(w, r, target, http.StatusFound)
		return true
	}
	return false
}

// ============================================================
// GET /__stats__
// ============================================================

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	stats, err := s.db.GetStats()
	if err != nil {
		log.Printf("get stats failed, err=%v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ============================================================
// GET /__map__
// ============================================================

func (s *Server) handleMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	displayMap := make(map[string]string)
	for k, v := range s.cfg.Routes {
		if strings.HasPrefix(k, "/") && !strings.HasPrefix(v, "__") {
			displayMap[k] = v
		}
	}
	writeJSON(w, http.StatusOK, displayMap)
}

// ============================================================
// /api/org-members
// ============================================================

func (s *Server) handleOrgMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	orgID := r.URL.Query().Get("orgId")
	if orgID == "" {
		orgID = "org_default"
	}
	members, err := s.db.GetOrgMembers(orgID)
	if err != nil {
		log.Printf("get org members failed, org=%s, err=%v", orgID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if members == nil {
		members = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"members": members})
}

// ============================================================
// /api/me
// ============================================================

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	userID, ok := RequireAuth(w, r, s.tokenSecret)
	if !ok {
		return
	}
	orgID, err := s.db.FindUserOrg(userID)
	if err != nil {
		log.Printf("find user org failed, user=%s, err=%v", userID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"userId": userID,
		"orgId":  orgID,
		"exp":    time.Now().Unix() + int64(s.expiryDays())*86400,
	})
}

// ============================================================
// /api/login
// ============================================================

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var req struct {
		OrgID    string `json:"orgId"`
		UserID   string `json:"userId"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.OrgID == "" || req.UserID == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "orgId, userId and password are required"})
		return
	}

	pwdHash, exists, err := s.db.FindUser(req.OrgID, req.UserID)
	if err != nil {
		log.Printf("find user failed, org=%s, user=%s, err=%v", req.OrgID, req.UserID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if !exists || pwdHash == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":   "password_not_set",
			"message": "Password not set. Please set password first.",
		})
		return
	}
	if pwdHash != HashPassword(req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid password"})
		return
	}

	token := GenerateToken(req.UserID, s.tokenSecret, s.expiryDays())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  map[string]string{"userId": req.UserID, "orgId": req.OrgID},
	})
}

// ============================================================
// /api/set-password
// ============================================================

func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var req struct {
		OrgID       string `json:"orgId"`
		UserID      string `json:"userId"`
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.OrgID == "" || req.UserID == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "orgId, userId and newPassword are required"})
		return
	}

	if err := s.db.EnsureOrg(req.OrgID); err != nil {
		log.Printf("ensure org failed, org=%s, err=%v", req.OrgID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	pwdHash, exists, err := s.db.FindUser(req.OrgID, req.UserID)
	if err != nil {
		log.Printf("find user failed, org=%s, user=%s, err=%v", req.OrgID, req.UserID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if exists && pwdHash != "" {
		if req.OldPassword == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "oldPassword is required to change password"})
			return
		}
		if pwdHash != HashPassword(req.OldPassword) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid old password"})
			return
		}
	}

	newHash := HashPassword(req.NewPassword)
	if err := s.db.UpsertUser(req.OrgID, req.UserID, newHash); err != nil {
		log.Printf("upsert user failed, org=%s, user=%s, err=%v", req.OrgID, req.UserID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	token := GenerateToken(req.UserID, s.tokenSecret, s.expiryDays())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  map[string]string{"userId": req.UserID, "orgId": req.OrgID},
	})
}

// ============================================================
// /tasks.json
// ============================================================

func (s *Server) handleTasksJSON(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getTasksJSON(w, r)
	case http.MethodPut:
		s.putTasksJSON(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

func (s *Server) getTasksJSON(w http.ResponseWriter, r *http.Request) {
	data, err := s.db.GetTasksJSON()
	if err != nil {
		log.Printf("get tasks json failed, err=%v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) putTasksJSON(w http.ResponseWriter, r *http.Request) {
	userID, ok := RequireAuth(w, r, s.tokenSecret)
	if !ok {
		return
	}

	var req struct {
		Orgs map[string]json.RawMessage `json:"orgs"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	for orgID, rawOrg := range req.Orgs {
		var org map[string]struct {
			Tasks []db.TaskItem `json:"tasks"`
		}
		if err := json.Unmarshal(rawOrg, &org); err != nil {
			continue
		}
		for memberID, member := range org {
			if memberID != userID {
				continue
			}
			if err := s.db.EnsureOrg(orgID); err != nil {
				log.Printf("ensure org failed, org=%s, err=%v", orgID, err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
				return
			}
			if err := s.db.UpsertTasks(orgID, memberID, member.Tasks); err != nil {
				log.Printf("upsert tasks failed, org=%s, user=%s, err=%v", orgID, memberID, err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
				return
			}
		}
	}
	log.Printf("tasks.json updated by user=%s", userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "file": "tasks.json"})
}

// ============================================================
// 静态文件 + 目录列表
// ============================================================

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.handleRouteRedirect(w, r) {
		return
	}

	// 检查通过中文名访问
	decodedPath, _ := url.QueryUnescape(r.URL.Path)
	for filePath, displayName := range s.cfg.Routes {
		if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(displayName, "__") {
			continue
		}
		if decodedPath == "/"+displayName || strings.HasSuffix(decodedPath, "/"+displayName) {
			r.URL.Path = filePath
			break
		}
	}

	fsPath := filepath.Join(s.serDirAbs, filepath.Clean(r.URL.Path))
	info, err := os.Stat(fsPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if info.IsDir() {
		indexPath := filepath.Join(fsPath, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
		s.listDirectory(w, r, fsPath)
		return
	}

	content, err := os.ReadFile(fsPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	w.Header().Set("Content-Type", contentType(ext)+"; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

func (s *Server) listDirectory(w http.ResponseWriter, r *http.Request, dirPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var dirs, files []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	displayPath := html.EscapeString(r.URL.Path)

	var items []string
	if displayPath != "/" {
		parent := filepath.Dir(strings.TrimRight(displayPath, "/"))
		if parent == "." {
			parent = "/"
		}
		items = append(items, fmt.Sprintf(`<li><a href="%s">../</a></li>`, parent))
	}

	displayNames := make(map[string]string)
	for fp, mn := range s.cfg.Routes {
		fname := strings.TrimPrefix(fp, "/")
		if strings.HasPrefix(mn, "__") {
			continue
		}
		displayNames[fname] = mn
	}

	for _, d := range dirs {
		name := d.Name()
		dn := name
		if mapped, ok := displayNames[name]; ok {
			dn = mapped
		}
		items = append(items, fmt.Sprintf(`<li><a href="%s/">%s/</a></li>`,
			html.EscapeString(name), html.EscapeString(dn)))
	}

	for _, f := range files {
		name := f.Name()
		dn := name
		if mapped, ok := displayNames[name]; ok {
			dn = mapped
		}
		href := url.PathEscape(name)
		fileHref := href
		if !strings.HasSuffix(displayPath, "/") {
			fileHref = displayPath + "/" + href
		} else if displayPath != "/" {
			fileHref = displayPath + href
		} else {
			fileHref = "/" + href
		}
		items = append(items, fmt.Sprintf(`<li><a href="%s">%s</a></li>`,
			html.EscapeString(fileHref), html.EscapeString(dn)))
	}

	body := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Index of %s</title>
</head>
<body>
<h1>Index of %s</h1>
<ul>
%s
</ul>
</body>
</html>`, displayPath, displayPath, strings.Join(items, ""))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(body))
}

// ============================================================
// 日志中间件
// ============================================================

func withLogging(next http.Handler, s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)

		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}
		ua := r.Header.Get("User-Agent")
		if len(ua) > 50 {
			ua = ua[:50]
		}
		visitorID := fmt.Sprintf("%s|%s", ip, ua)

		go func() {
			if err := s.db.LogVisit(visitorID, ip, ua, r.URL.Path, lrw.statusCode); err != nil {
				log.Printf("log visit failed, err=%v", err)
			}
		}()

		timestamp := time.Now().Format("2006-01-02 15:04:05")
		writeAccessLog(s.logFile, timestamp, visitorID, r.URL.Path, fmt.Sprintf("%d", lrw.statusCode))
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func writeAccessLog(logPath, timestamp, visitorID, path, status string) {
	line := fmt.Sprintf("%s | %s | %s | %s\n", timestamp, visitorID, path, status)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line)
}

// ============================================================
// 工具函数
// ============================================================

func contentType(ext string) string {
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".xml":
		return "application/xml"
	case ".py":
		return "text/x-python"
	case ".go":
		return "text/x-go"
	default:
		return "application/octet-stream"
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
