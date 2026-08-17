// Package server HTTP 请求处理、静态文件服务、目录列表、访问日志中间件
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// Server HTTP 服务
type Server struct {
	db          *db.DB
	cfg         *config.Config
	serDirAbs   string
	adminRoot   string // 管理员全局目录（serDirAbs/_admin），/index 路由用
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
	if err := os.MkdirAll(absDir, 0755); err != nil {
		return nil, fmt.Errorf("create static dir %s: %w", absDir, err)
	}
	adminRoot := filepath.Join(absDir, "_admin")
	if err := os.MkdirAll(adminRoot, 0755); err != nil {
		return nil, fmt.Errorf("create admin dir: %w", err)
	}

	// 一次性迁移：将 static_dir 顶层散落的旧文件移到 _legacy/（仅首次启动执行）
	migrateLegacyFiles(absDir)

	return &Server{
		db:          database,
		cfg:         cfg,
		serDirAbs:   absDir,
		adminRoot:   adminRoot,
		tokenSecret: tokenSecret,
		logFile:     logFile,
	}, nil
}

// migrateLegacyFiles 将 static_dir 顶层散落的旧文件（非 _admin、非 {orgId} 目录）
// 移动到 _legacy/ 目录，保留管理员可访问。已存在 _legacy/ 时不重复迁移。
// 新文件按 {orgId}/{userId}/ 结构存放，由 userRoot() 按需创建。
func migrateLegacyFiles(staticDir string) {
	legacyDir := filepath.Join(staticDir, "_legacy")
	entries, err := os.ReadDir(staticDir)
	if err != nil {
		return
	}
	// 检查是否已有用户目录（以非 _ 开头的目录），有则说明已迁移过
	hasUserDir := false
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") {
			hasUserDir = true
			break
		}
	}
	// 只在没有任何用户目录时执行迁移
	if hasUserDir {
		return
	}
	// 确保 _legacy 目录存在
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		// 跳过 _admin、_legacy 和所有以 _ 开头的目录
		if strings.HasPrefix(name, "_") {
			continue
		}
		src := filepath.Join(staticDir, name)
		dst := filepath.Join(legacyDir, name)
		// 尝试重命名，失败则忽略（可能有文件被占用）
		_ = os.Rename(src, dst)
	}
}

// App 构建 iris.Application 并注册所有路由
func (s *Server) App() *iris.Application {
	app := iris.New()
	s.app = app

	// 全局中间件：访问日志 + 安全响应头
	app.Use(s.loggingMiddleware())
	app.Use(s.securityHeadersMiddleware())

	// ============================================================
	// /api 统一分组
	// ============================================================
	api := app.Party("/api")
	auth := AuthMiddleware(s.tokenSecret, s.db.FindUserOrg)

	// 公开 API（无需鉴权）
	api.Post("/login", s.handleLogin)
	api.Post("/set-password", s.handleSetPassword)

	// 分享数据 API（无需鉴权，密码由 handler 校验）
	api.Get("/share/{token}", s.handleShareData)
	api.Post("/share/{token}", s.handleShareData)

	// 需要鉴权的 API
	api.Get("/me", auth, s.handleMe)
	api.Get("/stats", auth, s.handleStats)
	api.Get("/map", auth, s.handleMap)
	api.Get("/tree", auth, s.handleTree)
	api.Get("/org-members", auth, s.handleOrgMembers)
	api.Get("/tasks", auth, s.getTasksJSON)
	api.Put("/tasks", auth, s.putTasksJSON)
	// 增量任务接口：单条增删改，避免全量 PUT 的带宽浪费和并发风险
	api.Patch("/tasks/{id}", auth, s.patchTask)
	api.Post("/tasks/{id}", auth, s.postTask)
	api.Delete("/tasks/{id}", auth, s.deleteTask)

	api.Get("/file", auth, s.handleServeFile)

	// 分享管理 API（需鉴权）
	api.Get("/share", auth, s.handleListShares)
	api.Post("/share", auth, s.handleCreateShare)
	api.Delete("/share/{id}", auth, s.handleDeleteShare)

	// CORS 预检（sandboxed iframe null origin）
	noop := func(ctx iris.Context) { ctx.StatusCode(204) }
	api.Options("/me", noop)
	api.Options("/login", noop)
	api.Options("/set-password", noop)
	api.Options("/org-members", noop)
	api.Options("/tasks", noop)
	api.Options("/share", noop)
	api.Options("/share/{token}", noop)

	// ============================================================
	// /s 分享页面（公开 URL，不走 /api）
	// ============================================================
	app.Get("/s/{token}", s.handleShareAccess)
	app.Get("/s/{token}/{p:path}", s.handleShareAccess)

	// 静态文件 + SPA 兜底路由
	app.Get("/{path:path}", s.handleStaticWithAuth)

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
		s.listDirectory(ctx, s.adminRoot)
		return true
	}
	if target == "__spa__" {
		s.serveUIAsset(ctx, "index.html")
		return true
	}
	if strings.HasPrefix(target, "/") {
		ctx.Redirect(target, iris.StatusFound)
		return true
	}
	return false
}

// ============================================================
// GET /api/stats
// ============================================================

func (s *Server) handleStats(ctx iris.Context) {
	rctx := ctx.Request().Context()
	orgID := currentOrgID(ctx)
	stats, err := s.db.GetStatsByOrg(rctx, orgID)
	if err != nil {
		serverError(ctx, "get stats failed", err)
		return
	}
	writeOK(ctx, stats)
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
	writeOK(ctx, displayMap)
}

// ============================================================
// GET /api/org-members
// ============================================================

func (s *Server) handleOrgMembers(ctx iris.Context) {
	rctx := ctx.Request().Context()
	// 强制使用当前登录用户的 orgId，忽略前端传入的 orgId 参数（防止越权查询其他组织）
	orgID := currentOrgID(ctx)
	if orgID == "" {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}
	members, err := s.db.GetOrgMembers(rctx, orgID)
	if err != nil {
		serverError(ctx, "get org members failed", err, "org", orgID)
		return
	}
	if members == nil {
		members = []string{}
	}
	writeOK(ctx, membersData{Members: members})
}

// ============================================================
// GET /api/me（需要 AuthMiddleware）
// ============================================================

func (s *Server) handleMe(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	writeOK(ctx, meData{UserID: userID, OrgID: orgID, Exp: time.Now().Unix() + int64(s.expiryDays())*secondsPerDay})
}

// ============================================================
// POST /api/login
// ============================================================

func (s *Server) handleLogin(ctx iris.Context) {
	rctx := ctx.Request().Context()
	var req struct {
		OrgID    string `json:"orgId"`
		UserID   string `json:"userId"`
		Password string `json:"password"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.OrgID == "" || req.UserID == "" || req.Password == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "orgId, userId and password are required")
		return
	}

	// 登录限流：检查 IP 失败次数
	ip := clientIP(ctx)
	if s.loginFailureCount(rctx, ip) >= loginMaxFailures {
		writeFail(ctx, iris.StatusTooManyRequests, CodeTooManyRequests)
		return
	}

	pwdHash, exists, err := s.db.FindUser(rctx, req.OrgID, req.UserID)
	if err != nil {
		serverError(ctx, "find user failed", err, "org", req.OrgID, "user", req.UserID)
		return
	}
	if !exists || pwdHash == "" {
		s.recordLoginFailure(rctx, ip)
		writeFailMsg(ctx, iris.StatusForbidden, CodePasswordNotSet, "Password not set. Please set password first.")
		return
	}
	if !VerifyPassword(pwdHash, req.Password) {
		s.recordLoginFailure(rctx, ip)
		writeFail(ctx, iris.StatusUnauthorized, CodeInvalidPassword)
		return
	}

	s.clearLoginFailures(rctx, ip)
	token := GenerateToken(req.OrgID, req.UserID, s.tokenSecret, s.expiryDays())
	writeOK(ctx, loginData{Token: token, User: userBrief{UserID: req.UserID, OrgID: req.OrgID}})
}

// ============================================================
// POST /api/set-password
// ============================================================

func (s *Server) handleSetPassword(ctx iris.Context) {
	rctx := ctx.Request().Context()
	var req struct {
		OrgID       string `json:"orgId"`
		UserID      string `json:"userId"`
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.OrgID == "" || req.UserID == "" || req.NewPassword == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "orgId, userId and newPassword are required")
		return
	}

	if err := s.db.EnsureOrg(rctx, req.OrgID); err != nil {
		serverError(ctx, "ensure org failed", err, "org", req.OrgID)
		return
	}

	pwdHash, exists, err := s.db.FindUser(rctx, req.OrgID, req.UserID)
	if err != nil {
		serverError(ctx, "find user failed", err, "org", req.OrgID, "user", req.UserID)
		return
	}
	// 不存在用户：仅当系统无任何用户时允许创建（首次初始化），之后禁止开放注册
	switch {
	case !exists:
		hasUsers, err := s.db.HasAnyUser(rctx)
		if err != nil {
			serverError(ctx, "check has any user failed", err)
			return
		}
		if hasUsers {
			writeFailMsg(ctx, iris.StatusForbidden, CodeUserNotFound, "User does not exist. Contact admin to create account.")
			return
		}
	case pwdHash != "":
		// 已存在用户且有密码：必须验证旧密码
		if req.OldPassword == "" {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "oldPassword is required to change password")
			return
		}
		if !VerifyPassword(pwdHash, req.OldPassword) {
			writeFail(ctx, iris.StatusUnauthorized, CodeInvalidOldPassword)
			return
		}
	default:
		// 已存在用户但密码为空：仅首次初始化时允许设置，否则需要管理员重置
		hasUsers, err := s.db.HasAnyUser(rctx)
		if err != nil {
			serverError(ctx, "check has any user failed", err)
			return
		}
		if hasUsers {
			writeFailMsg(ctx, iris.StatusForbidden, CodePasswordNotSet, "Password not set. Contact admin to reset.")
			return
		}
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		serverError(ctx, "hash password failed", err)
		return
	}
	if err := s.db.UpsertUser(rctx, req.OrgID, req.UserID, newHash); err != nil {
		serverError(ctx, "upsert user failed", err, "org", req.OrgID, "user", req.UserID)
		return
	}

	token := GenerateToken(req.OrgID, req.UserID, s.tokenSecret, s.expiryDays())
	writeOK(ctx, loginData{Token: token, User: userBrief{UserID: req.UserID, OrgID: req.OrgID}})
}

// ============================================================
// /api/tasks
// ============================================================

func (s *Server) getTasksJSON(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	data, err := s.db.GetTasksJSONByOwner(ctx.Request().Context(), orgID, userID)
	if err != nil {
		serverError(ctx, "get tasks json failed", err)
		return
	}
	ctx.Header("Cache-Control", "no-cache")
	writeOK(ctx, data)
}

func (s *Server) putTasksJSON(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	currentOrg := currentOrgID(ctx)

	var req struct {
		Orgs map[string]json.RawMessage `json:"orgs"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	for orgID, rawOrg := range req.Orgs {
		// 只允许写入当前登录用户所属的 org
		if orgID != currentOrg {
			writeFailMsg(ctx, iris.StatusForbidden, CodeForbidden, "Cannot write to other org's tasks")
			return
		}
		var org map[string]struct {
			Tasks   []db.TaskItem   `json:"tasks"`
			Version json.RawMessage `json:"version"`
		}
		if err := json.Unmarshal(rawOrg, &org); err != nil {
			slog.Error("unmarshal org tasks failed", "org", orgID, "err", err)
			writeFail(ctx, iris.StatusBadRequest, CodeInvalidTasksJSON)
			return
		}
		for memberID, member := range org {
			if memberID != userID {
				continue
			}
			if err := s.db.EnsureOrg(rctx, orgID); err != nil {
				serverError(ctx, "ensure org failed", err, "org", orgID)
				return
			}
			// 存储客户端发来的 version JSON，GET 时原样回传，确保哈希一致
			versionJSON := ""
			if len(member.Version) > 0 && string(member.Version) != "null" {
				versionJSON = string(member.Version)
			}
			if err := s.db.UpsertTasks(rctx, orgID, memberID, member.Tasks, versionJSON); err != nil {
				serverError(ctx, "upsert tasks failed", err, "org", orgID, "user", memberID)
				return
			}
		}
	}
	slog.Info("tasks.json updated", "user", userID)
	writeOK(ctx, tasksOKData{Status: "ok", File: "tasks.json"})
}

// ============================================================
// 增量任务接口：PATCH/POST/DELETE /api/tasks/{id}
// 避免全量 PUT 的带宽浪费（改一条任务不发全部 23 条）
// ============================================================

// patchTask 更新单条任务（PATCH /api/tasks/{id}）
// 请求体：{ "task": { ...单条任务 } }
// 响应：{ "code": 0, "data": { "status": "ok", "version": {...新version} } }
func (s *Server) patchTask(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	taskID := ctx.Params().Get("id")
	if taskID == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	var req struct {
		Task db.TaskItem `json:"task"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.Task.ID = db.FlexString(taskID)

	if err := s.db.EnsureOrg(rctx, orgID); err != nil {
		serverError(ctx, "ensure org failed", err, "org", orgID)
		return
	}

	versionJSON, err := s.db.UpdateTask(rctx, orgID, userID, req.Task)
	if err != nil {
		serverError(ctx, "update task failed", err, "task", taskID)
		return
	}

	var version interface{}
	if versionJSON != "" {
		_ = json.Unmarshal([]byte(versionJSON), &version)
	}
	slog.Info("task patched", "user", userID, "task", taskID, "status", req.Task.Status)
	writeOK(ctx, map[string]interface{}{"status": "ok", "version": version})
}

// postTask 新增单条任务（POST /api/tasks/{id}）
func (s *Server) postTask(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	taskID := ctx.Params().Get("id")
	if taskID == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	var req struct {
		Task db.TaskItem `json:"task"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.Task.ID = db.FlexString(taskID)

	if err := s.db.EnsureOrg(rctx, orgID); err != nil {
		serverError(ctx, "ensure org failed", err, "org", orgID)
		return
	}

	versionJSON, err := s.db.AddTask(rctx, orgID, userID, req.Task)
	if err != nil {
		serverError(ctx, "add task failed", err, "task", taskID)
		return
	}

	var version interface{}
	if versionJSON != "" {
		_ = json.Unmarshal([]byte(versionJSON), &version)
	}
	slog.Info("task added", "user", userID, "task", taskID)
	writeOK(ctx, map[string]interface{}{"status": "ok", "version": version})
}

// deleteTask 删除单条任务（DELETE /api/tasks/{id}）
func (s *Server) deleteTask(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	taskID := ctx.Params().Get("id")
	if taskID == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	if err := s.db.EnsureOrg(rctx, orgID); err != nil {
		serverError(ctx, "ensure org failed", err, "org", orgID)
		return
	}

	versionJSON, err := s.db.DeleteTask(rctx, orgID, userID, taskID)
	if err != nil {
		serverError(ctx, "delete task failed", err, "task", taskID)
		return
	}

	var version interface{}
	if versionJSON != "" {
		_ = json.Unmarshal([]byte(versionJSON), &version)
	}
	slog.Info("task deleted", "user", userID, "task", taskID)
	writeOK(ctx, map[string]interface{}{"status": "ok", "version": version})
}

// ============================================================
// GET /api/tree
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

	// 安全检查：路径解析限定在当前用户根目录内
	fsPath, cleanPath, ok := s.resolveUserPathSafe(ctx, relPath)
	if !ok {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil || !info.IsDir() {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	entries, err := os.ReadDir(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
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

	writeOK(ctx, treeData{Path: cleanPath, Dirs: dirs, Files: files})
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

// userRoot 返回当前登录用户的私有文件根目录: serDirAbs/{orgId}/{userId}/
// 若目录不存在则自动创建。orgID 或 userID 为空时返回空串表示无效。
func (s *Server) userRoot(ctx iris.Context) string {
	orgID := currentOrgID(ctx)
	userID := currentUserID(ctx)
	if orgID == "" || userID == "" {
		return ""
	}
	root := filepath.Join(s.serDirAbs, orgID, userID)
	_ = os.MkdirAll(root, 0755)
	return root
}

// userRootByOwner 通过 share 的 owner 信息定位用户根目录（分享访问场景）
func (s *Server) userRootByOwner(orgID, userID string) string {
	if orgID == "" || userID == "" {
		return ""
	}
	return filepath.Join(s.serDirAbs, orgID, userID)
}

// resolveUserPath 以当前用户根为基准解析相对路径，防止跨用户/跨组织目录穿越
// 返回解析后的绝对路径和是否成功
func (s *Server) resolveUserPath(ctx iris.Context, relPath string) (string, bool) {
	root := s.userRoot(ctx)
	if root == "" {
		return "", false
	}
	fsPath, _, ok := safeJoin(root, relPath)
	if !ok {
		return "", false
	}
	if s.cfg.Server.AllowSymlink {
		return fsPath, true
	}
	realPath, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", false
	}
	realBase, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if realPath != realBase && !strings.HasPrefix(realPath, realBase+string(filepath.Separator)) {
		return "", false
	}
	return realPath, true
}

// resolveUserPathByOwner 以指定 owner 的用户根为基准解析路径（分享访问场景）
func (s *Server) resolveUserPathByOwner(orgID, userID, relPath string) (string, bool) {
	root := s.userRootByOwner(orgID, userID)
	if root == "" {
		return "", false
	}
	fsPath, _, ok := safeJoin(root, relPath)
	if !ok {
		return "", false
	}
	if s.cfg.Server.AllowSymlink {
		return fsPath, true
	}
	realPath, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", false
	}
	realBase, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if realPath != realBase && !strings.HasPrefix(realPath, realBase+string(filepath.Separator)) {
		return "", false
	}
	return realPath, true
}

// resolveUserPathSafe 以当前用户根为基准解析路径，返回 (fsPath, cleanRel, ok)
// cleanRel 是相对于用户根的规范化路径（以 / 开头），供前端展示用
func (s *Server) resolveUserPathSafe(ctx iris.Context, relPath string) (string, string, bool) {
	root := s.userRoot(ctx)
	if root == "" {
		return "", "", false
	}
	fsPath, cleanRel, ok := safeJoin(root, relPath)
	if !ok {
		return "", "", false
	}
	if s.cfg.Server.AllowSymlink {
		return fsPath, cleanRel, true
	}
	realPath, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", "", false
	}
	realBase, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	if realPath != realBase && !strings.HasPrefix(realPath, realBase+string(filepath.Separator)) {
		return "", "", false
	}
	return realPath, cleanRel, true
}

// serveUIAsset 从 embed FS 服务内置 UI 资源（index.html/todo.html/css/js）
// 命中返回 true（已写入响应），未命中返回 false 由调用方走磁盘
func (s *Server) serveUIAsset(ctx iris.Context, reqPath string) bool {
	rel := strings.TrimPrefix(reqPath, "/")
	if rel == "" {
		rel = "index.html"
	}
	if !isUIAsset(rel) {
		return false
	}
	data, err := workbench.UIFS.ReadFile("frontend/dist/" + rel)
	if err != nil {
		return false
	}
	ctx.ContentType(mimeTypeByExt(rel))
	// HTML 入口（index.html/todo.html）必须每次重新校验，避免浏览器缓存旧版
	// 导致引用的 /assets/* hash 失效（典型的 stale cache → MIME 404 雪崩）
	// assets/* 由 Vite 哈希命名，内容不可变，可长期缓存
	if rel == "index.html" || rel == "todo.html" {
		ctx.Header("Cache-Control", "no-cache")
	} else {
		ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	_, _ = ctx.Write(data)
	return true
}

// isUIAsset 判断是否为内置 UI 资源（Vite 构建产物，embed 打包，不可变）
func isUIAsset(rel string) bool {
	switch rel {
	case "index.html", "todo.html":
		return true
	}
	return strings.HasPrefix(rel, "assets/")
}

// mimeTypeByExt 按扩展名返回 Content-Type
func mimeTypeByExt(path string) string {
	switch filepath.Ext(path) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2; charset=utf-8"
	case ".woff":
		return "font/woff; charset=utf-8"
	case ".ttf":
		return "font/ttf; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// isPublicEntry 判断路径是否为公开页面入口（无需鉴权即可访问）
func (s *Server) isPublicEntry(path string) bool {
	// 根路径
	if path == "/" || path == "" {
		return true
	}
	cleaned := strings.TrimPrefix(path, "/")
	// UI 资源（embed 内置：index.html/todo.html/assets/*）一律公开，无需鉴权
	if isUIAsset(cleaned) {
		return true
	}
	// 路由映射中配置的公开路径（如 /todo 重定向到 /todo.html）
	if _, exists := s.cfg.Routes[path]; exists {
		return true
	}
	return false
}

// handleStaticWithAuth 静态文件访问：页面入口公开，其余需 Bearer token
func (s *Server) handleStaticWithAuth(ctx iris.Context) {
	reqPath := ctx.Path()

	// 公开页面入口：放行
	if s.isPublicEntry(reqPath) {
		s.handleStatic(ctx)
		return
	}

	// 其余文件：需要鉴权
	token := extractTokenFromContext(ctx)
	if token == "" {
		writeFail(ctx, iris.StatusUnauthorized, CodeUnauthorized)
		return
	}
	valid, orgID, userID := ValidateToken(token, s.tokenSecret)
	if !valid {
		writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
		return
	}
	// 旧格式 token 兼容：orgID 为空时查 DB 补全
	if orgID == "" {
		if oid, err := s.db.FindUserOrg(context.Background(), userID); err == nil && oid != "" {
			orgID = oid
		}
	}
	ctx.Values().Set("userID", userID)
	ctx.Values().Set("orgID", orgID)
	s.handleStatic(ctx)
}

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

	// UI 资源走 embed（内置模板，不可变），未命中再走磁盘用户根
	if s.serveUIAsset(ctx, reqPath) {
		return
	}

	fsPath, ok := s.resolveUserPath(ctx, reqPath)
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
			_ = ctx.ServeFile(indexPath)
			return
		}
		s.listDirectory(ctx, fsPath)
		return
	}

	content, err := os.ReadFile(fsPath)
	if err != nil {
		serverError(ctx, "read static file failed", err)
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	// 二进制扩展名走 ServeFile 触发下载
	if isBinaryExt(ext) {
		_ = ctx.ServeFile(fsPath)
		return
	}
	ctx.ContentType(contentType(ext) + "; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Content-Length", fmt.Sprintf("%d", len(content)))
	ctx.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	ctx.StatusCode(iris.StatusOK)
	_, _ = ctx.Write(content)
}

// ============================================================
// GET /api/file?path=... — 静态文件访问（统一 /api 前缀）
// ============================================================

func (s *Server) handleServeFile(ctx iris.Context) {
	relPath := ctx.URLParam("path")
	if relPath == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}

	if !strings.HasPrefix(relPath, "/") {
		relPath = "/" + relPath
	}
	fsPath, ok := s.resolveUserPath(ctx, relPath)
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	if info.IsDir() {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	if isBinaryExt(ext) {
		_ = ctx.ServeFile(fsPath)
		return
	}

	content, err := os.ReadFile(fsPath)
	if err != nil {
		serverError(ctx, "read file failed", err)
		return
	}
	ctx.ContentType(contentType(ext) + "; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Content-Length", fmt.Sprintf("%d", len(content)))
	ctx.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	ctx.StatusCode(iris.StatusOK)
	_, _ = ctx.Write(content)
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
		var fileHref string
		switch {
		case !strings.HasSuffix(displayPath, "/"):
			fileHref = displayPath + "/" + href
		case displayPath != "/":
			fileHref = displayPath + href
		default:
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
	_, _ = ctx.WriteString(body)
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
		path := ctx.Path()
		orgID := currentOrgID(ctx)
		go func() {
			if err := s.db.LogVisit(context.Background(), visitorID, ip, orgID, ua, path, statusCode); err != nil {
				slog.Error("log visit failed", "err", err)
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
	_, _ = f.WriteString(line)
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
		if strings.HasPrefix(path, "/api/") {
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
// 登录限流：每 IP 每分钟最多 loginMaxFailures 次失败（DB 持久化，跨重启/多实例生效）
// ============================================================

const (
	loginMaxFailures = 5
	loginWindow      = time.Minute
)

// loginFailureCount 返回 IP 在窗口内的失败次数；DB 查询失败时 fail-open 返回 0。
func (s *Server) loginFailureCount(ctx context.Context, ip string) int {
	count, err := s.db.RateLimitCheck(ctx, "login:"+ip, loginWindow)
	if err != nil {
		slog.Error("rate limit check failed, fail-open", "key", "login:"+ip, "err", err)
		return 0
	}
	return count
}

// recordLoginFailure 记录一次登录失败。
func (s *Server) recordLoginFailure(ctx context.Context, ip string) {
	if _, err := s.db.RateLimitRecord(ctx, "login:"+ip, loginWindow); err != nil {
		slog.Error("rate limit record failed", "key", "login:"+ip, "err", err)
	}
}

// clearLoginFailures 登录成功后清除 IP 的失败记录。
func (s *Server) clearLoginFailures(ctx context.Context, ip string) {
	if err := s.db.RateLimitClear(ctx, "login:"+ip); err != nil {
		slog.Error("rate limit clear failed", "key", "login:"+ip, "err", err)
	}
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
	_ = ctx.JSON(data)
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
		// 没有跳过分隔符就遇到数字（字符串以数字开头或单段），仍算章节号，直接进入读数字。
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
		switch {
		case aDigit && bDigit:
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
		case !aDigit && !bDigit:
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
		default:
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
