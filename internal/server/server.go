// Package server HTTP 请求处理、静态文件服务、目录列表、访问日志中间件
package server

import (
	"context"
	"crypto/subtle"
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
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/notify"
	"github.com/yiiewang/workbench/internal/schedule"
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
	// aiApprovals AI 写工具审批协调器（Agent 模式，跨请求状态：
	// SSE 对话流 create/等待，POST /api/ai/approval resolve）
	aiApprovals aiApprovalHub
	// scheduler 定时调度器（schedule.enabled 时启动，进程退出随之消亡；
	// job 均为短扫描，中断无害，无需优雅停止）
	scheduler *schedule.Scheduler
	// bus 事件总线（AI 工具 list_events/notify_user 消费；订阅者在 New 中注册）
	bus *schedule.EventBus
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

	srv := &Server{
		db:          database,
		cfg:         cfg,
		serDirAbs:   absDir,
		adminRoot:   adminRoot,
		tokenSecret: tokenSecret,
		logFile:     logFile,
	}

	// 定时调度 + 事件总线：订阅者（EmailNotifier 等）注册到总线，
	// schedule.enabled 时启动 cron 引擎（表达式配置错误 fail fast）
	bus := schedule.NewEventBus()
	srv.bus = bus
	if _, err := notify.NewEmailNotifier(cfg.Notify.Email, database, bus); err != nil {
		return nil, err
	}
	if cfg.Schedule.Enabled {
		loc, lerr := time.LoadLocation(cfg.Schedule.Timezone)
		if lerr != nil {
			slog.Warn("schedule timezone invalid, fallback to UTC", "tz", cfg.Schedule.Timezone)
			loc = time.UTC
		}
		sched := schedule.NewScheduler(bus, loc)
		job, jerr := schedule.NewTodoDueScanJob(database, loc, cfg.Schedule.TodoDueCron)
		if jerr != nil {
			return nil, fmt.Errorf("init schedule jobs: %w", jerr)
		}
		sched.AddJob(job)
		// 精确时间提醒：每分钟扫描 scheduled_time/due_time 命中当前时刻的任务
		timeJob, terr := schedule.NewTodoTimeReminderJob(database, loc)
		if terr != nil {
			return nil, fmt.Errorf("init time reminder job: %w", terr)
		}
		sched.AddJob(timeJob)
		// AI 定时任务执行（用户自建，cron 命中 → LLM 生成 → 邮件通知）
		aiJobsRunner, ajerr := schedule.NewAIJobsRunnerJob(database, srv.aiChatOnce, loc)
		if ajerr != nil {
			return nil, fmt.Errorf("init ai jobs runner: %w", ajerr)
		}
		sched.AddJob(aiJobsRunner)
		sched.Start(context.Background())
		srv.scheduler = sched
		slog.Info("scheduler started", "timezone", loc.String(), "jobs", sched.Jobs(),
			"todoDueCron", cfg.Schedule.TodoDueCron)
	}

	return srv, nil
}

// App 构建 iris.Application 并注册所有路由
func (s *Server) App() *iris.Application {
	app := iris.New()
	s.app = app

	// 全局中间件：响应压缩 + 访问日志 + 安全响应头
	// （压缩在最外层：logging 的 Record 记录未压缩数据，响应给客户端的是压缩流，
	//  iris 对 Record→Compress 的包装顺序两种情况均已兼容）
	app.Use(s.compressionMiddleware())
	app.Use(s.loggingMiddleware())
	app.Use(s.securityHeadersMiddleware())

	// ============================================================
	// /api 统一分组
	// ============================================================
	api := app.Party("/api")
	auth := AuthMiddleware(s.tokenSecret, s.db.ResolveUserOrgIdentity)

	// 公开 API（无需鉴权）
	api.Post("/login", s.handleLogin)
	api.Post("/set-password", s.handleSetPassword)

	// 分享数据 API（无需鉴权，密码由 handler 校验）
	api.Get("/share/{token}", s.handleShareData)
	api.Post("/share/{token}", s.handleShareData)

	// 需要鉴权的 API
	api.Get("/me", auth, s.handleMe)
	api.Get("/userinfo", auth, s.handleUserInfo)
	api.Post("/org/switch", auth, s.handleOrgSwitch)
	// 用户自助修改通知邮箱（任务到期提醒投递目标）
	api.Post("/profile/email", auth, s.handleProfileEmail)
	api.Get("/org/features", auth, s.handleOrgFeatures)
	api.Get("/stats", auth, s.handleStats)
	api.Get("/map", auth, s.handleMap)
	api.Get("/tree", auth, s.handleTree)
	api.Get("/org-members", auth, s.handleOrgMembers)
	api.Get("/tasks", auth, s.getTasksJSON)
	// 任务详情（列表响应瘦身后按 id 拉完整任务，含 content 全文）
	api.Get("/tasks/{id}", auth, s.getTaskDetail)
	// 查看同组织成员的任务（只读，TodoView"查看成员日程"）
	api.Get("/tasks/user/{id}", auth, s.getMemberTasks)
	// 反查关联了指定文件的任务（todo ↔ file 联动，FileViewer 侧展示用）
	api.Get("/tasks/by-file", auth, s.getTasksByFile)
	// 当前用户未完成任务（todo/progress），AI 面板任务抽屉等场景用
	api.Get("/tasks/open", auth, s.getOpenTasks)
	// 增量任务接口：单条增删改，避免全量 PUT 的带宽浪费和并发风险
	api.Patch("/tasks/{id}", auth, s.patchTask)
	api.Post("/tasks/{id}", auth, s.postTask)
	api.Delete("/tasks/{id}", auth, s.deleteTask)

	api.Get("/file", auth, s.handleServeFile)
	// 媒体流式播放：ticket 需鉴权；/api/media 本身靠 ticket 鉴权（<video> 请求带不上 header）
	api.Get("/media/ticket", auth, s.handleMediaTicket)
	api.Get("/media", s.handleServeMedia)
	// 文件上传（沙箱限定在当前用户根目录内）
	api.Post("/upload", auth, s.handleUpload)
	// 文件管理（沙箱同上）：新建目录 / 删除 / 重命名 / 移动
	api.Post("/mkdir", auth, s.handleMkdir)
	api.Delete("/file", auth, s.handleDeleteFile)
	api.Post("/rename", auth, s.handleRename)
	api.Post("/move", auth, s.handleMove)

	// 分享管理 API（需鉴权）
	api.Get("/share", auth, s.handleListShares)
	api.Post("/share", auth, s.handleCreateShare)
	api.Delete("/share/{id}", auth, s.handleDeleteShare)

	// 任务版本号（轻量端点：多设备轮询对账）
	api.Get("/tasks/revision", auth, s.getTaskRevision)
	// AI 对话 API（需鉴权；未配置 ai 段时 status 返回 enabled=false、chat 返回 50301）
	api.Get("/ai/status", auth, s.handleAIStatus)
	api.Post("/ai/chat", auth, s.handleAIChat)
	// 写类工具审批决定（Agent 模式，Human-in-the-loop）
	api.Post("/ai/approval", auth, s.handleAIApproval)
	// AI 定时任务 CRUD（用户自建"到点让 AI 执行 prompt"）
	api.Get("/ai/jobs", auth, s.handleAIJobsList)
	api.Post("/ai/jobs", auth, s.handleAIJobCreate)
	api.Get("/ai/jobs/{id}/runs", auth, s.handleAIJobRuns)
	api.Put("/ai/jobs/{id}", auth, s.handleAIJobUpdate)
	api.Delete("/ai/jobs/{id}", auth, s.handleAIJobDelete)
	// AI 会话历史 CRUD（回到历史会话继续沟通）
	api.Get("/ai/sessions", auth, s.handleAISessionsList)
	api.Post("/ai/sessions", auth, s.handleAISessionCreate)
	api.Get("/ai/sessions/{id}", auth, s.handleAISessionGet)
	api.Put("/ai/sessions/{id}", auth, s.handleAISessionUpdate)
	api.Delete("/ai/sessions/{id}", auth, s.handleAISessionDelete)

	// Admin API（需鉴权 + admin 或 org_admin 角色，admin 跨 org、org_admin 仅本 org）
	admin := api.Party("/admin", auth, RequireUserManager)
	admin.Get("/users", s.handleAdminListUsers)
	admin.Post("/users", s.handleAdminCreateUser)
	admin.Patch("/users/{id}", s.handleAdminUpdateUser)
	admin.Delete("/users/{id}", s.handleAdminDeleteUser)
	admin.Get("/roles", s.handleAdminListRoles)
	admin.Get("/users/{id}/dashboard", s.handleAdminUserDashboard)
	admin.Get("/users/{id}/features", s.handleAdminListUserFeatures)
	admin.Patch("/users/{id}/features/{code}", s.handleAdminUpdateUserFeature)

	// CORS 预检（sandboxed iframe null origin）
	noop := func(ctx iris.Context) { ctx.StatusCode(204) }
	api.Options("/me", noop)
	api.Options("/login", noop)
	api.Options("/set-password", noop)
	api.Options("/org-members", noop)
	api.Options("/tasks", noop)
	api.Options("/share", noop)
	api.Options("/share/{token}", noop)
	api.Options("/admin/users", noop)
	api.Options("/admin/users/{id}", noop)
	api.Options("/admin/roles", noop)
	api.Options("/admin/users/{id}/features", noop)
	api.Options("/admin/users/{id}/features/{code}", noop)

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
	if orgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}
	members, err := s.db.GetOrgMembers(rctx, orgID)
	if err != nil {
		serverError(ctx, "get org members failed", err, "org", orgID)
		return
	}
	if members == nil {
		members = []db.Member{}
	}
	writeOK(ctx, membersData{Members: members})
}

// ============================================================
// GET /api/me（需要 AuthMiddleware）
// ============================================================

func (s *Server) handleMe(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	writeOK(ctx, meData{
		UserID:   userID,
		OrgID:    orgID,
		UserName: currentUserName(ctx),
		OrgName:  currentOrgName(ctx),
		Role:     currentRole(ctx),
		Exp:      time.Now().Unix() + int64(s.expiryDays())*secondsPerDay,
	})
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
	if req.UserID == "" || req.Password == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "userId and password are required")
		return
	}

	// 登录限流：检查 IP 失败次数
	ip := clientIP(ctx)
	if s.loginFailureCount(rctx, ip) >= loginMaxFailures {
		writeFail(ctx, iris.StatusTooManyRequests, CodeTooManyRequests)
		return
	}

	// 用户名全局唯一：orgId 可选。填了则按 org+name 校验，留空则按 name 全局匹配。
	var orgID, userID int64
	var pwdHash string
	var exists bool
	var err error
	if req.OrgID != "" {
		orgID, userID, pwdHash, _, exists, err = s.db.FindUserByName(rctx, req.OrgID, req.UserID)
	} else {
		orgID, _, userID, pwdHash, _, exists, err = s.db.FindUserByGlobalName(rctx, req.UserID)
	}
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

	// 组织上下文：优先登录指定的 org（FindUserByName 校验过的），否则回退用户默认组织
	resolvedOrgID, resOrgName, _, orgRole, platformAdmin, err := s.db.ResolveUserOrgIdentity(rctx, userID, orgID)
	if err != nil || resolvedOrgID == 0 {
		resolvedOrgID, resOrgName, _, orgRole, platformAdmin, err = s.db.ResolveUserOrgIdentity(rctx, userID, 0)
		if err != nil || resolvedOrgID == 0 {
			serverError(ctx, "resolve user org failed", err)
			return
		}
	}

	token := GenerateToken(userID, s.tokenSecret, s.expiryDays())
	writeOK(ctx, loginData{Token: token, User: userBrief{
		UserID:   userID,
		OrgID:    resolvedOrgID,
		UserName: req.UserID,
		OrgName:  resOrgName,
		Role:     externalRole(orgRole, platformAdmin),
	}})
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
		// SetupToken 首次初始化引导码：仅当系统无任何用户时校验（见下方 !exists 分支），
		// 与环境变量/config 的 auth.admin_setup_token 匹配方可创建首个超管账号。
		SetupToken string `json:"setupToken"`
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

	orgID, err := s.db.EnsureOrg(rctx, req.OrgID)
	if err != nil {
		serverError(ctx, "ensure org failed", err, "org", req.OrgID)
		return
	}

	_, userID, pwdHash, _, exists, err := s.db.FindUserByName(rctx, req.OrgID, req.UserID)
	if err != nil {
		serverError(ctx, "find user failed", err, "org", req.OrgID, "user", req.UserID)
		return
	}

	switch {
	case !exists:
		// 不存在用户：仅当系统无任何用户时允许创建（首次初始化），首个用户自动成为 admin
		hasUsers, err := s.db.HasAnyUser(rctx)
		if err != nil {
			serverError(ctx, "check has any user failed", err)
			return
		}
		if hasUsers {
			writeFailMsg(ctx, iris.StatusForbidden, CodeUserNotFound, "User does not exist. Contact admin to create account.")
			return
		}
		// 首次初始化引导码：已配置 admin_setup_token 时，首个注册必须携带匹配值，
		// 消除"部署到注册之间"的抢占窗口。常量时间比较防时序侧信道。
		if want := s.cfg.Auth.AdminSetupToken; want != "" {
			if subtle.ConstantTimeCompare([]byte(req.SetupToken), []byte(want)) != 1 {
				slog.Warn("first init rejected: invalid setup token",
					"user", req.UserID, "org", req.OrgID, "ip", ctx.RemoteAddr())
				writeFailMsg(ctx, iris.StatusForbidden, CodeInvalidParam, "Invalid setup token")
				return
			}
			slog.Info("first init authorized via setup token", "user", req.UserID, "org", req.OrgID)
		}
	case pwdHash != "":
		// 已存在且有密码：改密需验证旧密码
		if req.OldPassword == "" {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "oldPassword is required to change password")
			return
		}
		if !VerifyPassword(pwdHash, req.OldPassword) {
			writeFail(ctx, iris.StatusUnauthorized, CodeInvalidOldPassword)
			return
		}
	default:
		// 已存在但密码为空：首次激活，直接设置（修复原 HasAnyUser 恒真导致的死代码）
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		serverError(ctx, "hash password failed", err)
		return
	}

	if !exists {
		// 首用户 → admin（UpsertUser 会设置 is_platform_admin 标志与组织关系）
		userID, err = s.db.UpsertUser(rctx, orgID, req.UserID, newHash, db.RoleIDAdmin)
	} else {
		// 改密/激活：按 org+id 更新密码，不改角色
		err = s.db.SetUserPassword(rctx, orgID, userID, newHash)
	}
	if err != nil {
		serverError(ctx, "set password failed", err, "org", req.OrgID, "user", req.UserID)
		return
	}

	// 解析组织上下文（orgID 已在上面 EnsureOrg/FindUserByName 确定）
	resolvedOrgID, resOrgName, _, orgRole, platformAdmin, err := s.db.ResolveUserOrgIdentity(rctx, userID, orgID)
	if err != nil || resolvedOrgID == 0 {
		serverError(ctx, "resolve user org failed", err)
		return
	}

	token := GenerateToken(userID, s.tokenSecret, s.expiryDays())
	writeOK(ctx, loginData{Token: token, User: userBrief{
		UserID: userID, OrgID: resolvedOrgID, UserName: req.UserID, OrgName: resOrgName, Role: externalRole(orgRole, platformAdmin)}})
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
// ============================================================
// GET /api/tasks/user/{id} — 查看同组织成员的任务（只读）
// ============================================================

// getMemberTasks 返回指定成员的任务数据（TodoView"查看成员日程"功能）。
// GET /api/tasks 只返回当前登录用户自己的任务（RBAC 收紧后），查看他人日程
// 需专用端点：先校验目标成员与当前用户同 org 且关系 active（防跨 org 拉取），
// 再复用 GetTasksJSONByOwner 按目标成员过滤，返回结构与 GET /api/tasks 一致。
func (s *Server) getMemberTasks(ctx iris.Context) {
	memberID, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || memberID <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	orgID := currentOrgID(ctx)
	if orgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}
	// 校验成员归属：目标用户必须与当前登录用户同 org 且关系 active
	_, _, _, _, exists, err := s.db.GetUserOrgIdentity(ctx.Request().Context(), memberID, orgID)
	if err != nil {
		serverError(ctx, "check member org failed", err, "member", memberID)
		return
	}
	if !exists {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}

	data, err := s.db.GetTasksJSONByOwner(ctx.Request().Context(), orgID, memberID)
	if err != nil {
		serverError(ctx, "get member tasks failed", err, "member", memberID)
		return
	}
	ctx.Header("Cache-Control", "no-cache")
	writeOK(ctx, data)
}

// ============================================================
// GET /api/tasks/by-file?path=... — 反查关联了指定文件的任务
// ============================================================

// getTasksByFile 返回当前用户任务中 files 字段包含指定路径的任务。
// todo ↔ file 联动：任务可挂文件（TaskItem.Files），文件浏览侧据此展示
// "哪些任务引用了当前文件"。路径比较前统一为 / 开头的规范化形式。
func (s *Server) getTasksByFile(ctx iris.Context) {
	relPath := strings.TrimSpace(ctx.URLParam("path"))
	if relPath == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	relPath = "/" + strings.TrimPrefix(filepath.Clean(relPath), "/")

	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	tasks, err := s.db.GetTasks(ctx.Request().Context(), orgID, userID)
	if err != nil {
		serverError(ctx, "get tasks failed", err)
		return
	}

	matched := make([]db.TaskItem, 0, 4)
	for _, t := range tasks {
		for _, f := range t.Files {
			norm := "/" + strings.TrimPrefix(filepath.Clean(f), "/")
			if norm == relPath {
				matched = append(matched, t)
				break
			}
		}
	}
	writeOK(ctx, map[string]any{"tasks": matched})
}

// getOpenTasks 返回当前用户未完成任务（status != done），按截止日升序。
// AI 面板任务抽屉等场景用：任务持久化在 DB，面板刷新后重新拉取即可，不丢。
func (s *Server) getOpenTasks(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	tasks, err := s.db.GetTasks(ctx.Request().Context(), orgID, userID)
	if err != nil {
		serverError(ctx, "get tasks failed", err)
		return
	}
	open := make([]db.TaskItem, 0, 8)
	for _, t := range tasks {
		if t.Status != "done" {
			open = append(open, t)
		}
	}
	// 截止日升序，空日期排最后
	sort.SliceStable(open, func(i, j int) bool {
		di, dj := open[i].Due, open[j].Due
		if di == "" {
			return false
		}
		if dj == "" {
			return true
		}
		return di < dj
	})
	writeOK(ctx, map[string]any{"tasks": open})
}

// ============================================================
// 增量任务接口：PATCH/POST/DELETE /api/tasks/{id}
// 乐观锁：请求携带 baseRevision（客户端上次同步到的服务端版本号），
// 与服务端当前 revision 不一致 → 409 + 服务端任务副本（前端做字段级合并）。
// baseRevision 缺省 = 跳过校验（旧客户端兼容写，last-writer-wins）。
// ============================================================

// writeTaskConflict 写入 409 冲突响应：携带服务端当前任务副本与 revision
func (s *Server) writeTaskConflict(ctx iris.Context, orgID, userID int64, taskID string, revision int64) {
	data := map[string]any{"revision": revision}
	if tasks, err := s.db.GetTasks(ctx.Request().Context(), orgID, userID); err == nil {
		for _, t := range tasks {
			if string(t.ID) == taskID {
				data["task"] = t
				break
			}
		}
	}
	writeFailData(ctx, iris.StatusConflict, CodeTaskConflict, data)
}
// patchTask 更新单条任务（PATCH /api/tasks/{id}）
// 请求体：{ "task": {...}, "baseRevision": 12 }
//
// content 字段缺省语义（列表瘦身配套）：列表响应不再携带 content 全文，
// 看板拖拽/勾选完成/移动端快捷操作等路径的 task 对象没有 content 字段
// （序列化时省略）。UpdateTask 是全字段覆盖写——请求体 task 中 content
// 字段缺失时必须回填服务端现值，否则这些快捷修改会把正文清空。
// 显式传 content（含空串 = 用户主动清空正文）按传入值更新。
func (s *Server) patchTask(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	taskID := ctx.Params().Get("id")
	if taskID == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	// 探测 task.content 字段是否存在（缺失 = 保留服务端现值）
	var probe struct {
		Task map[string]json.RawMessage `json:"task"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	var req struct {
		Task         db.TaskItem `json:"task"`
		BaseRevision *int64      `json:"baseRevision"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.Task.ID = db.FlexString(taskID)

	if req.BaseRevision == nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "baseRevision is required")
		return
	}
	if cur, err := s.db.GetRevision(rctx, userID); err == nil && *req.BaseRevision != cur {
		s.writeTaskConflict(ctx, orgID, userID, taskID, cur)
		return
	}

	// 缺失字段回填服务端现值（任务不存在时保持空值，UpdateTask 自然失败）：
	//   - content：列表瘦身（不传全文）后快捷修改路径无此字段
	//   - scheduledTime/dueTime：omitempty 序列化，空值省略——快捷修改同样会缺失
	// 显式传值（含空串 = 用户主动清空）按传入值更新。
	hasContent := probe.Task["content"] != nil
	hasSTime := probe.Task["scheduledTime"] != nil
	hasDTime := probe.Task["dueTime"] != nil
	if !hasContent || !hasSTime || !hasDTime {
		if cur, exists, err := s.db.GetTaskByID(rctx, orgID, userID, taskID); err == nil && exists {
			if !hasContent {
				req.Task.Content = cur.Content
			}
			if !hasSTime {
				req.Task.ScheduledTime = cur.ScheduledTime
			}
			if !hasDTime {
				req.Task.DueTime = cur.DueTime
			}
		}
	}

	rev, err := s.db.UpdateTask(rctx, orgID, userID, req.Task)
	if err != nil {
		serverError(ctx, "update task failed", err, "task", taskID)
		return
	}
	slog.Info("task patched", "user", userID, "task", taskID, "status", req.Task.Status)
	writeOK(ctx, map[string]any{"status": "ok", "revision": rev})
}

// getTaskDetail 任务详情（GET /api/tasks/{id}）：
// 列表响应瘦身后 content 不随列表下发，编辑/查看详情场景按 id 拉取完整任务。
func (s *Server) getTaskDetail(ctx iris.Context) {
	taskID := strings.TrimSpace(ctx.Params().Get("id"))
	if taskID == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	task, exists, err := s.db.GetTaskByID(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), taskID)
	if err != nil {
		serverError(ctx, "get task failed", err, "task", taskID)
		return
	}
	if !exists {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	rev, err := s.db.GetRevision(ctx.Request().Context(), currentUserID(ctx))
	if err != nil {
		serverError(ctx, "get revision failed", err)
		return
	}
	writeOK(ctx, map[string]any{"task": task, "revision": rev})
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
		Task         db.TaskItem `json:"task"`
		BaseRevision *int64      `json:"baseRevision"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	req.Task.ID = db.FlexString(taskID)

	if req.BaseRevision == nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "baseRevision is required")
		return
	}
	if cur, err := s.db.GetRevision(rctx, userID); err == nil && *req.BaseRevision != cur {
		s.writeTaskConflict(ctx, orgID, userID, taskID, cur)
		return
	}

	rev, err := s.db.AddTask(rctx, orgID, userID, req.Task)
	if err != nil {
		serverError(ctx, "add task failed", err, "task", taskID)
		return
	}
	slog.Info("task added", "user", userID, "task", taskID)
	writeOK(ctx, map[string]any{"status": "ok", "revision": rev})
}

// deleteTask 删除单条任务（DELETE /api/tasks/{id}?baseRevision=12）
func (s *Server) deleteTask(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	taskID := ctx.Params().Get("id")
	if taskID == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	// DELETE 无请求体，baseRevision 走 query（必填）
	base, baseErr := ctx.URLParamInt64("baseRevision")
	if baseErr != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "baseRevision is required")
		return
	}
	if cur, err := s.db.GetRevision(rctx, userID); err == nil && base != cur {
		s.writeTaskConflict(ctx, orgID, userID, taskID, cur)
		return
	}

	rev, err := s.db.DeleteTask(rctx, orgID, userID, taskID)
	if err != nil {
		serverError(ctx, "delete task failed", err, "task", taskID)
		return
	}
	slog.Info("task deleted", "user", userID, "task", taskID)
	writeOK(ctx, map[string]any{"status": "ok", "revision": rev})
}

// getTaskRevision 轻量版本号端点（GET /api/tasks/revision）：
// 多设备轮询对账用，只返回当前 revision，不拉任务数据。
func (s *Server) getTaskRevision(ctx iris.Context) {
	rev, err := s.db.GetRevision(ctx.Request().Context(), currentUserID(ctx))
	if err != nil {
		serverError(ctx, "get revision failed", err)
		return
	}
	writeOK(ctx, map[string]any{"revision": rev})
}

// ============================================================
// GET /api/tree
// ============================================================

type treeItem struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size,omitempty"`
	// IsDir 之外的链接标记：条目为符号链接时为 true（前端据此加角标；
	// 浏览/预览仍跟随链接展示真实内容，fsops 的移动/重命名/删除只作用于链接本身）
	IsLink bool `json:"isLink,omitempty"`
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
		isLink := e.Type()&os.ModeSymlink != 0
		// 软链接目录：e.IsDir() 返回 false，需要 stat 跟随链接后判断
		if isLink && !isDir {
			if info, err := os.Stat(filepath.Join(fsPath, e.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, treeItem{Name: e.Name(), IsDir: true, IsLink: isLink})
		} else {
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			files = append(files, treeItem{Name: e.Name(), IsDir: false, Size: size, IsLink: isLink})
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

// userRoot 返回当前登录用户的私有文件根目录: serDirAbs/{orgName}/{userName}/
// 目录层使用 name 拼路径（双轨架构：存储层用整数 id，目录层用 name）。
// 若目录不存在则自动创建。orgName 或 userName 为空时返回空串表示无效。
func (s *Server) userRoot(ctx iris.Context) string {
	orgName := currentOrgName(ctx)
	userName := currentUserName(ctx)
	if orgName == "" || userName == "" {
		return ""
	}
	root := filepath.Join(s.serDirAbs, orgName, userName)
	_ = os.MkdirAll(root, 0755)
	return root
}

// userRootByOwner 通过 share 的 owner 整数 id 反查 name 定位用户根目录（分享访问场景）
func (s *Server) userRootByOwner(ctx context.Context, orgID, userID int64) string {
	if orgID == 0 || userID == 0 {
		return ""
	}
	orgName, userName, err := s.db.GetUserNames(ctx, orgID, userID)
	if err != nil {
		return ""
	}
	return filepath.Join(s.serDirAbs, orgName, userName)
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
func (s *Server) resolveUserPathByOwner(ctx context.Context, orgID, userID int64, relPath string) (string, bool) {
	root := s.userRootByOwner(ctx, orgID, userID)
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
	valid, userID := ValidateToken(token, s.tokenSecret)
	if !valid {
		writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
		return
	}
	// 组织上下文：X-Org-Id 头（可选），缺省默认组织
	orgID := int64(0)
	if h := ctx.GetHeader("X-Org-Id"); h != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(h), 10, 64); err == nil {
			orgID = v
		}
	}
	resolvedOrgID, orgName, userName, orgRole, isPlatformAdmin, err := s.db.ResolveUserOrgIdentity(context.Background(), userID, orgID)
	if err != nil || resolvedOrgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}
	ctx.Values().Set("userID", userID)
	ctx.Values().Set("orgID", resolvedOrgID)
	ctx.Values().Set("userName", userName)
	ctx.Values().Set("orgName", orgName)
	ctx.Values().Set("orgRole", orgRole)
	ctx.Values().Set("isPlatformAdmin", isPlatformAdmin)
	ctx.Values().Set("role", externalRole(orgRole, isPlatformAdmin))
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
// 响应压缩中间件（gzip/deflate/br，按客户端 Accept-Encoding 协商）
// ============================================================

// compressionSkipPrefixes 排除压缩的流式接口：
//   - /api/media：http.ServeContent 的 Range 语义与压缩不兼容
//     （客户端按未压缩字节区间请求，压缩后区间错位，视频拖动直接失效）
//   - /api/ai/chat：SSE 逐行透传（每行 flush 的小增量在压缩层下无收益，
//     徒增大量小 gzip 块与解压开销）
func compressionSkipPrefixes() []string {
	return []string{"/api/media", "/api/ai/chat"}
}

func (s *Server) compressionMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		p := ctx.Path()
		for _, prefix := range compressionSkipPrefixes() {
			if strings.HasPrefix(p, prefix) {
				ctx.Next()
				return
			}
		}
		// 客户端未声明 Accept-Encoding 时 iris 内部自动回退原 writer（静默失败）
		_ = ctx.CompressWriter(true)
		ctx.Next()
	}
}

// ============================================================
// 访问日志中间件
// ============================================================

func (s *Server) loggingMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		// /api/media、/api/ai/chat 必须跳过 Record：ResponseRecorder 会把整个响应体攒在
		// 内存里、请求结束才 flush，那样就不是流式了（大视频/SSE 增量直接失效）。
		if ctx.Path() != "/api/media" && ctx.Path() != "/api/ai/chat" {
			ctx.Record()
		}
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
