// Package server HTTP 鉴权、任务接口与 Server 装配
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/notify"
	"github.com/yiiewang/workbench/internal/rag"
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
	// rag 文件语义检索服务（ai.embedding 配置启用时非 nil；nil = RAG 关闭，
	// 各写钩子与对话前置检索据此静默跳过）
	rag *rag.Service
	// mcpTools 启动时从 ai.mcp.servers 拉取的外部工具表（与内置 aiTools 同构）
	mcpTools []aiTool
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
		// cron 在 New 内即 Start，定时任务可能早于 App() 装配路由触发；
		// 提前建好 app，让工具执行所需的 ContextPool 始终可用。
		app: iris.New(),
	}

	// 阶段 2：存量 ai/tool/interface 任务统一转 pipeline（幂等，失败不影响启动）
	srv.normalizeLegacyTargets(context.Background())

	// MCP 外部工具：启动时拉取各服务 tools/list（配置错误 fail fast，服务不可达跳过）
	if err := srv.initMCPTools(); err != nil {
		return nil, err
	}

	// 定时调度 + 事件总线：订阅者（EmailNotifier 等）注册到总线，
	// schedule.enabled 时启动 cron 引擎（表达式配置错误 fail fast）
	bus := schedule.NewEventBus()
	srv.bus = bus
	if err := notify.SubscribeEmailNotifier(cfg.Notify.Email, cfg.SiteURL, database, bus, srv.formatNotifyBody); err != nil {
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
		// 通用定时触发：schedule 只按 target_type 分发，装配层注册执行入口
		triggersJob, tjerr := schedule.NewTriggersJob(database, map[string]schedule.TriggerHandler{
			"job": srv.triggerJob,
		}, loc)
		if tjerr != nil {
			return nil, fmt.Errorf("init cron triggers job: %w", tjerr)
		}
		sched.AddJob(triggersJob)
		sched.Start(context.Background())
		srv.scheduler = sched
		slog.Info("scheduler started", "timezone", loc.String(), "jobs", sched.Jobs(),
			"todoDueCron", cfg.Schedule.TodoDueCron)
	}

	// RAG 语义检索：ai.embedding 配置齐全才启用。vec0 表维度取自配置，
	// 与库中已存维度不一致时 EnsureVecIndex 内部作废旧向量（触发全量重建）。
	// 建表失败不致命（RAG 降级关闭），但要有醒目日志。
	if cfg.AI.Embedding.Enabled() {
		if err := database.EnsureVecIndex(cfg.AI.Embedding.Dim); err != nil {
			slog.Error("rag: ensure vec index failed, RAG disabled", "err", err, "dim", cfg.AI.Embedding.Dim)
		} else if err := database.EnsureFTS(); err != nil {
			// FTS 失败不致命（检索降级纯向量），但要有醒目日志
			slog.Error("rag: ensure fts table failed, hybrid search degraded", "err", err)
			srv.rag = rag.NewService(database, cfg.AI.Embedding)
		} else {
			srv.rag = rag.NewService(database, cfg.AI.Embedding)
			slog.Info("rag enabled", "model", cfg.AI.Embedding.Model, "dim", cfg.AI.Embedding.Dim)
		}
	}

	return srv, nil
}

func (s *Server) expiryDays() int {
	if d := s.cfg.Auth.TokenExpiryDays; d > 0 {
		return d
	}
	return 30
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
	memberID, ok := pathID(ctx)
	if !ok {
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

	dirs, files, err := s.listTreeItems(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}

	writeOK(ctx, treeData{Path: cleanPath, Dirs: dirs, Files: files})
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
