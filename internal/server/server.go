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
	"sync"
	"time"

	"github.com/kataras/iris/v12"
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
	app         *iris.Application
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

// App 构建 iris.Application 并注册所有路由
func (s *Server) App() *iris.Application {
	app := iris.New()
	s.app = app

	// 全局中间件：访问日志 + 安全响应头
	app.Use(s.loggingMiddleware())
	app.Use(s.securityHeadersMiddleware())

	// 公开 API
	app.Get("/__stats__", s.handleStats)
	app.Get("/__map__", s.handleMap)
	app.Get("/__map__.json", s.handleMap)
	app.Get("/__tree__", s.handleTree)
	app.Get("/api/org-members", s.handleOrgMembers)
	app.Post("/api/login", s.handleLogin)
	app.Post("/api/set-password", s.handleSetPassword)

	// 需要鉴权的 API
	app.Get("/api/me", AuthMiddleware(s.tokenSecret), s.handleMe)
	app.Get("/tasks.json", s.getTasksJSON)
	app.Put("/tasks.json", AuthMiddleware(s.tokenSecret), s.putTasksJSON)

	// CORS 预检请求（sandboxed iframe null origin）
	app.Options("/api/me", func(ctx iris.Context) { ctx.StatusCode(204) })
	app.Options("/api/login", func(ctx iris.Context) { ctx.StatusCode(204) })
	app.Options("/api/set-password", func(ctx iris.Context) { ctx.StatusCode(204) })
	app.Options("/api/org-members", func(ctx iris.Context) { ctx.StatusCode(204) })
	app.Options("/tasks.json", func(ctx iris.Context) { ctx.StatusCode(204) })

	// 静态文件兜底路由：匹配所有其他路径
	app.Get("/{path:path}", s.handleStatic)
	app.Post("/{path:path}", s.handleStatic)

	return app
}

func (s *Server) expiryDays() int {
	if d := s.cfg.Auth.TokenExpiryDays; d > 0 {
		return d
	}
	return 30
}

// ============================================================
// 路由映射（静态文件路由重定向）
// ============================================================

func (s *Server) handleRouteRedirect(ctx iris.Context) bool {
	target, exists := s.cfg.Routes[ctx.Path()]
	if !exists {
		return false
	}
	if target == "__listdir__" {
		s.listDirectory(ctx, s.serDirAbs)
		return true
	}
	if strings.HasPrefix(target, "/") {
		ctx.Redirect(target, iris.StatusFound)
		return true
	}
	return false
}

// ============================================================
// GET /__stats__
// ============================================================

func (s *Server) handleStats(ctx iris.Context) {
	stats, err := s.db.GetStats()
	if err != nil {
		log.Printf("get stats failed, err=%v", err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	writeJSON(ctx, iris.StatusOK, stats)
}

// ============================================================
// GET /__map__
// ============================================================

func (s *Server) handleMap(ctx iris.Context) {
	displayMap := make(map[string]string)
	for k, v := range s.cfg.Routes {
		if strings.HasPrefix(k, "/") && !strings.HasPrefix(v, "__") {
			displayMap[k] = v
		}
	}
	writeJSON(ctx, iris.StatusOK, displayMap)
}

// ============================================================
// GET /api/org-members
// ============================================================

func (s *Server) handleOrgMembers(ctx iris.Context) {
	orgID := ctx.URLParam("orgId")
	if orgID == "" {
		orgID = "org_default"
	}
	members, err := s.db.GetOrgMembers(orgID)
	if err != nil {
		log.Printf("get org members failed, org=%s, err=%v", orgID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if members == nil {
		members = []string{}
	}
	writeJSON(ctx, iris.StatusOK, map[string]any{"members": members})
}

// ============================================================
// GET /api/me（需要 AuthMiddleware）
// ============================================================

func (s *Server) handleMe(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID, err := s.db.FindUserOrg(userID)
	if err != nil {
		log.Printf("find user org failed, user=%s, err=%v", userID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	writeJSON(ctx, iris.StatusOK, map[string]any{
		"userId": userID,
		"orgId":  orgID,
		"exp":    time.Now().Unix() + int64(s.expiryDays())*86400,
	})
}

// ============================================================
// POST /api/login
// ============================================================

func (s *Server) handleLogin(ctx iris.Context) {
	var req struct {
		OrgID    string `json:"orgId"`
		UserID   string `json:"userId"`
		Password string `json:"password"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.OrgID == "" || req.UserID == "" || req.Password == "" {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "orgId, userId and password are required"})
		return
	}

	// 登录限流：检查 IP 失败次数
	ip := clientIP(ctx)
	if loginFailureCount(ip) >= loginMaxFailures {
		writeJSON(ctx, iris.StatusTooManyRequests, map[string]string{"error": "Too many failed attempts, try later"})
		return
	}

	pwdHash, exists, err := s.db.FindUser(req.OrgID, req.UserID)
	if err != nil {
		log.Printf("find user failed, org=%s, user=%s, err=%v", req.OrgID, req.UserID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if !exists || pwdHash == "" {
		recordLoginFailure(ip)
		writeJSON(ctx, iris.StatusForbidden, map[string]string{
			"error":   "password_not_set",
			"message": "Password not set. Please set password first.",
		})
		return
	}
	if !VerifyPassword(pwdHash, req.Password) {
		recordLoginFailure(ip)
		writeJSON(ctx, iris.StatusUnauthorized, map[string]string{"error": "Invalid password"})
		return
	}

	clearLoginFailures(ip)
	token := GenerateToken(req.UserID, s.tokenSecret, s.expiryDays())
	writeJSON(ctx, iris.StatusOK, map[string]any{
		"token": token,
		"user":  map[string]string{"userId": req.UserID, "orgId": req.OrgID},
	})
}

// ============================================================
// POST /api/set-password
// ============================================================

func (s *Server) handleSetPassword(ctx iris.Context) {
	var req struct {
		OrgID       string `json:"orgId"`
		UserID      string `json:"userId"`
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.OrgID == "" || req.UserID == "" || req.NewPassword == "" {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "orgId, userId and newPassword are required"})
		return
	}

	if err := s.db.EnsureOrg(req.OrgID); err != nil {
		log.Printf("ensure org failed, org=%s, err=%v", req.OrgID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	pwdHash, exists, err := s.db.FindUser(req.OrgID, req.UserID)
	if err != nil {
		log.Printf("find user failed, org=%s, user=%s, err=%v", req.OrgID, req.UserID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	// 不存在用户：仅当系统无任何用户时允许创建（首次初始化），之后禁止开放注册
	if !exists {
		hasUsers, err := s.db.HasAnyUser()
		if err != nil {
			log.Printf("check has any user failed, err=%v", err)
			writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
			return
		}
		if hasUsers {
			writeJSON(ctx, iris.StatusForbidden, map[string]string{
				"error":   "user_not_found",
				"message": "User does not exist. Contact admin to create account.",
			})
			return
		}
	} else if pwdHash != "" {
		if req.OldPassword == "" {
			writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "oldPassword is required to change password"})
			return
		}
		if !VerifyPassword(pwdHash, req.OldPassword) {
			writeJSON(ctx, iris.StatusUnauthorized, map[string]string{"error": "Invalid old password"})
			return
		}
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		log.Printf("hash password failed, err=%v", err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if err := s.db.UpsertUser(req.OrgID, req.UserID, newHash); err != nil {
		log.Printf("upsert user failed, org=%s, user=%s, err=%v", req.OrgID, req.UserID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	token := GenerateToken(req.UserID, s.tokenSecret, s.expiryDays())
	writeJSON(ctx, iris.StatusOK, map[string]any{
		"token": token,
		"user":  map[string]string{"userId": req.UserID, "orgId": req.OrgID},
	})
}

// ============================================================
// /tasks.json
// ============================================================

func (s *Server) getTasksJSON(ctx iris.Context) {
	data, err := s.db.GetTasksJSON()
	if err != nil {
		log.Printf("get tasks json failed, err=%v", err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	ctx.Header("Cache-Control", "no-cache")
	writeJSON(ctx, iris.StatusOK, data)
}

func (s *Server) putTasksJSON(ctx iris.Context) {
	userID := currentUserID(ctx)

	var req struct {
		Orgs map[string]json.RawMessage `json:"orgs"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	for orgID, rawOrg := range req.Orgs {
		var org map[string]struct {
			Tasks   []db.TaskItem  `json:"tasks"`
			Version json.RawMessage `json:"version"`
		}
		if err := json.Unmarshal(rawOrg, &org); err != nil {
			log.Printf("unmarshal org tasks failed, org=%s, err=%v", orgID, err)
			writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid tasks JSON"})
			return
		}
		for memberID, member := range org {
			if memberID != userID {
				continue
			}
			if err := s.db.EnsureOrg(orgID); err != nil {
				log.Printf("ensure org failed, org=%s, err=%v", orgID, err)
				writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
				return
			}
			// 存储客户端发来的 version JSON，GET 时原样回传，确保哈希一致
			versionJSON := ""
			if len(member.Version) > 0 && string(member.Version) != "null" {
				versionJSON = string(member.Version)
			}
			if err := s.db.UpsertTasks(orgID, memberID, member.Tasks, versionJSON); err != nil {
				log.Printf("upsert tasks failed, org=%s, user=%s, err=%v", orgID, memberID, err)
				writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
				return
			}
		}
	}
	log.Printf("tasks.json updated by user=%s", userID)
	writeJSON(ctx, iris.StatusOK, map[string]string{"status": "ok", "file": "tasks.json"})
}

// ============================================================
// GET /__tree__
// ============================================================

type treeItem struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size,omitempty"`
}

func (s *Server) handleTree(ctx iris.Context) {
	relPath := ctx.URLParam("path")
	if relPath == "" {
		relPath = "/"
	}

	// 安全检查：防止目录穿越，校验最终路径必须落在 serDirAbs 内
	fsPath, cleanPath, ok := safeJoin(s.serDirAbs, relPath)
	if !ok {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid path"})
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil || !info.IsDir() {
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Not found or not a directory"})
		return
	}

	entries, err := os.ReadDir(fsPath)
	if err != nil {
		writeJSON(ctx, iris.StatusForbidden, map[string]string{"error": "Cannot read directory"})
		return
	}

	var dirs, files = make([]treeItem, 0), make([]treeItem, 0)
	for _, e := range entries {
		if s.isHidden(e.Name()) {
			continue
		}
		isDir := e.IsDir()
		// 软链接目录：e.IsDir() 返回 false，需要 stat 跟随链接后判断
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(fsPath, e.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, treeItem{Name: e.Name(), IsDir: true})
		} else {
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			files = append(files, treeItem{Name: e.Name(), IsDir: false, Size: size})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return naturalLess(dirs[i].Name, dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return naturalLess(files[i].Name, files[j].Name) })

	writeJSON(ctx, iris.StatusOK, map[string]any{
		"path":  cleanPath,
		"dirs":  dirs,
		"files": files,
	})
}

// isHidden 根据配置中的 hidden 规则判断文件名是否应隐藏
func (s *Server) isHidden(name string) bool {
	for _, pattern := range s.cfg.Server.Hidden {
		if pattern == ".*" && strings.HasPrefix(name, ".") {
			return true
		}
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// ============================================================
// 静态文件 + 目录列表
// ============================================================

func (s *Server) handleStatic(ctx iris.Context) {
	if s.handleRouteRedirect(ctx) {
		return
	}

	reqPath := ctx.Path()

	// 检查通过中文名访问
	decodedPath, _ := url.QueryUnescape(reqPath)
	for filePath, displayName := range s.cfg.Routes {
		if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(displayName, "__") {
			continue
		}
		if decodedPath == "/"+displayName || strings.HasSuffix(decodedPath, "/"+displayName) {
			reqPath = filePath
			break
		}
	}

	fsPath, _, ok := safeJoin(s.serDirAbs, reqPath)
	if !ok {
		ctx.NotFound()
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil {
		ctx.NotFound()
		return
	}

	if info.IsDir() {
		indexPath := filepath.Join(fsPath, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			ctx.ServeFile(indexPath)
			return
		}
		s.listDirectory(ctx, fsPath)
		return
	}

	content, err := os.ReadFile(fsPath)
	if err != nil {
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal Server Error"})
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	// 二进制扩展名走 ServeFile 触发下载
	if isBinaryExt(ext) {
		ctx.ServeFile(fsPath)
		return
	}
	ctx.ContentType(contentType(ext) + "; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Content-Length", fmt.Sprintf("%d", len(content)))
	ctx.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	ctx.StatusCode(iris.StatusOK)
	ctx.Write(content)
}

// isBinaryExt 判断是否应触发下载而非内联展示
func isBinaryExt(ext string) bool {
	switch ext {
	case ".zip", ".tar", ".gz", ".tgz", ".rar", ".7z", ".bz2", ".xz",
		".exe", ".dll", ".so", ".dylib", ".bin",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".mp4", ".mp3", ".wav", ".flv", ".avi", ".mov", ".mkv",
		".ttf", ".otf", ".woff", ".woff2":
		return true
	}
	return false
}

func (s *Server) listDirectory(ctx iris.Context, dirPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		ctx.StatusCode(iris.StatusForbidden)
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

	displayPath := html.EscapeString(ctx.Path())

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

	ctx.ContentType("text/html; charset=utf-8")
	ctx.WriteString(body)
}

// ============================================================
// 访问日志中间件
// ============================================================

func (s *Server) loggingMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		ctx.Record()
		ctx.Next()

		statusCode := ctx.GetStatusCode()

		ip, _, _ := net.SplitHostPort(ctx.RemoteAddr())
		if ip == "" {
			ip = ctx.RemoteAddr()
		}
		ua := ctx.GetHeader("User-Agent")
		if len(ua) > 50 {
			ua = ua[:50]
		}
		visitorID := fmt.Sprintf("%s|%s", ip, ua)

		go func() {
			if err := s.db.LogVisit(visitorID, ip, ua, ctx.Path(), statusCode); err != nil {
				log.Printf("log visit failed, err=%v", err)
			}
		}()

		timestamp := time.Now().Format("2006-01-02 15:04:05")
		writeAccessLog(s.logFile, timestamp, visitorID, ctx.Path(), fmt.Sprintf("%d", statusCode))
	}
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
// 安全响应头中间件
// ============================================================

func (s *Server) securityHeadersMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "SAMEORIGIN")
		ctx.Header("Referrer-Policy", "no-referrer")

		// CORS for sandboxed iframe（null origin）— 仅 API 路径
		// 预览窗口 iframe 使用 sandbox（无 allow-same-origin），origin 为 null
		// 不设置 Allow-Credentials，因为使用 Bearer token 而非 cookie
		path := ctx.Path()
		if strings.HasPrefix(path, "/api/") || path == "/tasks.json" {
			ctx.Header("Access-Control-Allow-Origin", "null")
			ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			ctx.Header("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			if ctx.Method() == "OPTIONS" {
				ctx.StatusCode(204)
				return
			}
		}

		ctx.Next()
	}
}

// ============================================================
// 登录限流：每 IP 每分钟最多 loginMaxFailures 次失败
// ============================================================

const (
	loginMaxFailures = 5
	loginWindow      = time.Minute
)

var loginLimiter = struct {
	sync.Mutex
	fails map[string][]time.Time
}{fails: make(map[string][]time.Time)}

// loginFailureCount 返回 IP 在窗口内的失败次数
func loginFailureCount(ip string) int {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	cutoff := time.Now().Add(-loginWindow)
	count := 0
	for _, t := range loginLimiter.fails[ip] {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// recordLoginFailure 记录一次登录失败并清理过期记录
func recordLoginFailure(ip string) {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	now := time.Now()
	cutoff := now.Add(-loginWindow)
	old := loginLimiter.fails[ip]
	valid := old[:0]
	for _, t := range old {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	loginLimiter.fails[ip] = append(valid, now)
}

// clearLoginFailures 登录成功后清除 IP 的失败记录
func clearLoginFailures(ip string) {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	delete(loginLimiter.fails, ip)
}

// clientIP 从 ctx.RemoteAddr 提取客户端 IP
func clientIP(ctx iris.Context) string {
	ip, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		return ctx.RemoteAddr()
	}
	return ip
}

// ============================================================
// 工具函数
// ============================================================

// safeJoin 将 baseDir 与 relPath 安全拼接，确保结果落在 baseDir 目录内，防止路径遍历。
// 返回拼接后的绝对路径、相对于 baseDir 的规范化路径（以 / 开头，根目录为 /）。
func safeJoin(baseDir, relPath string) (fsPath, cleanRel string, ok bool) {
	cleaned := filepath.Clean(relPath)
	fsPath = filepath.Join(baseDir, cleaned)
	rel, err := filepath.Rel(baseDir, fsPath)
	if err != nil {
		return "", "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	if rel == "." {
		cleanRel = "/"
	} else {
		cleanRel = "/" + filepath.ToSlash(rel)
	}
	return fsPath, cleanRel, true
}

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

func writeJSON(ctx iris.Context, status int, data any) {
	ctx.StatusCode(status)
	ctx.JSON(data)
}

func readJSON(ctx iris.Context, v any) error {
	data, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// naturalLess 按章节号路径排序：1.1.2 < 1.2 < 2 < 10 < 10.1；非章节号文件名回退到自然排序。
func naturalLess(a, b string) bool {
	pa, pOK := leadingChapterPath(a)
	pb, pOKb := leadingChapterPath(b)
	if pOK && pOKb {
		if c := chapterPathCmp(pa, pb); c != 0 {
			return c < 0
		}
	}
	return naturalLessImpl(a, b)
}

// leadingChapterPath 抽取开头的章节号路径，如 "1.2.3 xxx.md" → [1,2,3]。
// 章节号格式：连续的数字+分隔符（.或-或_或空），数字后面必须跟分隔符或字符串结束。
// 数字段后紧跟 alnum 字母（如 "10xxx"）视为非法章节号。
func leadingChapterPath(s string) ([]int64, bool) {
	var path []int64
	i := 0
	for {
		// 跳过分隔符（空格、点、横线、下划线等非 alnum）
		saveI := i
		for i < len(s) && !isDigit(s[i]) {
			if isAlnum(s[i]) {
				// 字母开头（如 "abc.md"）→ 不是章节号
				return nil, false
			}
			i++
		}
		if i >= len(s) {
			if len(path) == 0 {
				return nil, false
			}
			return path, true
		}
		if i == saveI {
			// 没有跳过分隔符就遇到数字：必须之前已读章节号
			if len(path) == 0 {
				// 字符串以数字开头但没分段（单段），仍算章节号
			}
		}
		// 读数字
		n := int64(0)
		for i < len(s) && isDigit(s[i]) {
			n = n*10 + int64(s[i]-'0')
			i++
		}
		path = append(path, n)
		if i >= len(s) {
			break
		}
		if isAlnum(s[i]) {
			// 数字后接字母（如 "10xxx"）→ 不是章节号
			return nil, false
		}
		// 是分隔符，继续
		if len(path) == 0 {
			return nil, false
		}
		// 跳到下一个分隔符后看是否是数字
		j := i
		for j < len(s) && !isAlnum(s[j]) {
			j++
		}
		if j >= len(s) || !isDigit(s[j]) {
			break
		}
		i = j
	}
	if len(path) == 0 {
		return nil, false
	}
	return path, true
}

// chapterPathCmp 比较章节号路径：[1,2] vs [1,2,1] → 短前缀更小 → 返回 -1
func chapterPathCmp(a, b []int64) int {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			if a[k] < b[k] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func naturalLessImpl(a, b string) bool {
	for i, j := 0, 0; ; {
		for i < len(a) && !isAlnum(a[i]) {
			i++
		}
		for j < len(b) && !isAlnum(b[j]) {
			j++
		}
		if i == len(a) && j == len(b) {
			return len(a) < len(b)
		}
		if i == len(a) {
			return true
		}
		if j == len(b) {
			return false
		}
		ni, nj := i, j
		for ni < len(a) && isAlnum(a[ni]) {
			ni++
		}
		for nj < len(b) && isAlnum(b[nj]) {
			nj++
		}
		// 整段是否为纯数字
		aDigit := true
		for k := i; k < ni; k++ {
			if a[k] < '0' || a[k] > '9' {
				aDigit = false
				break
			}
		}
		bDigit := true
		for k := j; k < nj; k++ {
			if b[k] < '0' || b[k] > '9' {
				bDigit = false
				break
			}
		}
		if aDigit && bDigit {
			ai, aj := i, j
			for ai < ni-1 && a[ai] == '0' {
				ai++
			}
			for aj < nj-1 && b[aj] == '0' {
				aj++
			}
			if ni-ai != nj-aj {
				return ni-ai < nj-aj
			}
			for k := 0; k < ni-ai; k++ {
				if a[ai+k] != b[aj+k] {
					return a[ai+k] < b[aj+k]
				}
			}
			if ni-i != nj-j {
				return ni-i < nj-j
			}
		} else {
			// 至少一段含字母：按字符逐个比较，遇数字按数字段比
			if c := mixedLess(a, i, ni, b, j, nj); c != 0 {
				return c < 0
			}
		}
		i, j = ni, nj
	}
}

// mixedLess 比较 a[i1:i2] 与 b[j1:j2]：逐段比较，段内是连续同类型字符（数字或非数字）
// 数字段按整数比，非数字段按小写字典比。
func mixedLess(a string, i1, i2 int, b string, j1, j2 int) int {
	for i1 < i2 && j1 < j2 {
		// 跳过分隔符
		for i1 < i2 && !isAlnum(a[i1]) {
			i1++
		}
		for j1 < j2 && !isAlnum(b[j1]) {
			j1++
		}
		if i1 == i2 || j1 == j2 {
			break
		}
		// 判断 a 段类型
		ai := i1
		aDigit := a[i1] >= '0' && a[i1] <= '9'
		if aDigit {
			for ai < i2 && a[ai] >= '0' && a[ai] <= '9' {
				ai++
			}
		} else {
			for ai < i2 && isAlnum(a[ai]) && !(a[ai] >= '0' && a[ai] <= '9') {
				ai++
			}
		}
		bj := j1
		bDigit := b[j1] >= '0' && b[j1] <= '9'
		if bDigit {
			for bj < j2 && b[bj] >= '0' && b[bj] <= '9' {
				bj++
			}
		} else {
			for bj < j2 && isAlnum(b[bj]) && !(b[bj] >= '0' && b[bj] <= '9') {
				bj++
			}
		}
		// 比较 a[i1:ai] 和 b[j1:bj]
		if aDigit && bDigit {
			na, nb := int64(0), int64(0)
			for k := i1; k < ai; k++ {
				na = na*10 + int64(a[k]-'0')
			}
			for k := j1; k < bj; k++ {
				nb = nb*10 + int64(b[k]-'0')
			}
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			if ai-i1 != bj-j1 {
				if ai-i1 < bj-j1 {
					return -1
				}
				return 1
			}
		} else if !aDigit && !bDigit {
			// 两段都是字母段：字符逐个小写比
			k := 0
			for ; i1+k < ai && j1+k < bj; k++ {
				ca, cb := a[i1+k], b[j1+k]
				if ca >= 'A' && ca <= 'Z' {
					ca += 32
				}
				if cb >= 'A' && cb <= 'Z' {
					cb += 32
				}
				if ca != cb {
					if ca < cb {
						return -1
					}
					return 1
				}
			}
			// 一方多出的字符决定胜负：数字字符 < 字母字符？这里都已是字母段
			if ai-i1 != bj-j1 {
				if ai-i1 < bj-j1 {
					return -1
				}
				return 1
			}
		} else {
			// 一方数字一方字母：按 ASCII 比较首字符等价
			// 约定：数字段 < 字母段
			if aDigit {
				return -1
			}
			return 1
		}
		i1, j1 = ai, bj
	}
	if i1 == i2 && j1 == j2 {
		return 0
	}
	if i1 == i2 {
		return -1
	}
	return 1
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
