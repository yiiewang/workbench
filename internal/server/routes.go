package server

import (
	"github.com/kataras/iris/v12"
)

// App 构建 iris.Application 并注册所有路由。
// 中间件顺序：压缩（最外层，logging 记录未压缩字节）→ 访问日志 → 安全响应头。
func (s *Server) App() *iris.Application {
	app := s.app // New 中已创建，cron 触发时不依赖 App() 的调用时机

	app.Use(s.compressionMiddleware())
	app.Use(s.loggingMiddleware())
	app.Use(s.securityHeadersMiddleware())

	s.registerAPIRoutes(app)
	s.registerPageRoutes(app)
	return app
}

// registerAPIRoutes 注册 /api 下的公开、鉴权与后台管理路由。
func (s *Server) registerAPIRoutes(app *iris.Application) {
	api := app.Party("/api")
	auth := AuthMiddleware(s.tokenSecret, s.db.ResolveUserOrgIdentity)

	// 公开 API（无需鉴权）
	api.Post("/login", s.handleLogin)
	api.Post("/set-password", s.handleSetPassword)

	// 分享数据 API（无需鉴权，密码由 handler 校验）
	api.Get("/share/{token}", s.handleShareData)
	api.Post("/share/{token}", s.handleShareData)

	s.registerUserAPIRoutes(api, auth)
	s.registerAIAPIRoutes(api, auth)
	s.registerAdminRoutes(api, auth)
	s.registerCORSPreflight(api)
}

// registerUserAPIRoutes 注册用户信息、文件、任务与分享管理路由（均需鉴权）。
func (s *Server) registerUserAPIRoutes(api iris.Party, auth iris.Handler) {
	api.Get("/me", auth, s.handleMe)
	api.Get("/userinfo", auth, s.handleUserInfo)
	api.Post("/org/switch", auth, s.handleOrgSwitch)
	// 用户自助修改通知邮箱（任务到期提醒投递目标）
	api.Post("/profile/email", auth, s.handleProfileEmail)
	api.Get("/org/features", auth, s.handleOrgFeatures)
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
	// 任务版本号（轻量端点：多设备轮询对账）
	api.Get("/tasks/revision", auth, s.getTaskRevision)

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
}

// registerAIAPIRoutes 注册 AI 对话、编排、定时触发与 AI 任务路由。
func (s *Server) registerAIAPIRoutes(api iris.Party, auth iris.Handler) {
	// AI 对话 API（需鉴权；未配置 ai 段时 status 返回 enabled=false、chat 返回 50301）
	api.Get("/ai/status", auth, s.handleAIStatus)
	api.Post("/ai/chat", auth, s.handleAIChat)
	// 写类工具审批决定（Agent 模式，Human-in-the-loop）
	api.Post("/ai/approval", auth, s.handleAIApproval)
	// 手动触发当前用户文件全量重建向量索引（RAG 未启用时 50301）
	api.Post("/ai/reindex", auth, s.handleAIReindex)
	// 全量重建进度查询（running/total/processed/failed/currentPath）
	api.Get("/ai/reindex/status", auth, s.handleAIReindexStatus)
	// 文件索引状态（汇总 files/chunks/errors + 明细列表）
	api.Get("/ai/index-status", auth, s.handleAIIndexStatus)
	// 后台对话运行状态（客户端断开后仍可查询跑完的结果）
	api.Get("/ai/runs", auth, s.handleAIRunsList)
	api.Get("/ai/runs/{id}", auth, s.handleAIRunGet)
	// 编排运行状态（每步结果，断连后仍可查询）
	api.Get("/pipeline/runs", auth, s.handlePipelineRunsList)
	api.Get("/pipeline/runs/{id}", auth, s.handlePipelineRunGet)
	api.Get("/pipeline/runs/{id}/stream", auth, s.handlePipelineRunStream)
	// 编排执行（Definition 直调；定时任务的编排由 job 触发器驱动）
	api.Post("/pipeline/run", auth, s.handlePipelineRun)
	// 通用定时触发规则（schedule 只按 target_type 分发，目标归属在装配层校验）
	api.Get("/schedule/triggers", auth, s.handleScheduleTriggersList)
	api.Get("/schedule/triggers/{id}", auth, s.handleScheduleTriggerGet)
	api.Post("/schedule/triggers", auth, s.handleScheduleTriggerCreate)
	api.Put("/schedule/triggers/{id}", auth, s.handleScheduleTriggerUpdate)
	api.Delete("/schedule/triggers/{id}", auth, s.handleScheduleTriggerDelete)
	// AI 定时任务 CRUD（用户自建"到点让 AI 执行 prompt"）
	api.Get("/jobs", auth, s.handleAIJobsList)
	api.Post("/jobs", auth, s.handleAIJobCreate)
	api.Post("/jobs/{id}/run", auth, s.handleAIJobRun)
	api.Get("/jobs/{id}/runs", auth, s.handleAIJobRuns)
	api.Put("/jobs/{id}", auth, s.handleAIJobUpdate)
	api.Delete("/jobs/{id}", auth, s.handleAIJobDelete)
	// AI 会话历史 CRUD（回到历史会话继续沟通）
	api.Get("/ai/sessions", auth, s.handleAISessionsList)
	api.Post("/ai/sessions", auth, s.handleAISessionCreate)
	api.Get("/ai/sessions/{id}", auth, s.handleAISessionGet)
	api.Delete("/ai/sessions/{id}", auth, s.handleAISessionDelete)
	api.Put("/ai/sessions/{id}/messages", auth, s.handleAISessionMessagesUpsert)
}

// registerAdminRoutes 注册 Admin API（需 admin 或 org_admin 角色，
// admin 跨 org、org_admin 仅本 org）。
func (s *Server) registerAdminRoutes(api iris.Party, auth iris.Handler) {
	admin := api.Party("/admin", auth, RequireUserManager)
	admin.Get("/users", s.handleAdminListUsers)
	admin.Post("/users", s.handleAdminCreateUser)
	admin.Patch("/users/{id}", s.handleAdminUpdateUser)
	admin.Delete("/users/{id}", s.handleAdminDeleteUser)
	admin.Get("/roles", s.handleAdminListRoles)
	admin.Get("/users/{id}/dashboard", s.handleAdminUserDashboard)
	admin.Get("/users/{id}/features", s.handleAdminListUserFeatures)
	admin.Patch("/users/{id}/features/{code}", s.handleAdminUpdateUserFeature)
}

// registerCORSPreflight 注册 CORS 预检（sandboxed iframe null origin）
func (s *Server) registerCORSPreflight(api iris.Party) {
	noop := func(ctx iris.Context) { ctx.StatusCode(204) }
	for _, path := range []string{
		"/me", "/login", "/set-password", "/org-members", "/tasks", "/share", "/share/{token}",
		"/admin/users", "/admin/users/{id}", "/admin/roles",
		"/admin/users/{id}/features", "/admin/users/{id}/features/{code}",
	} {
		api.Options(path, noop)
	}
}

// registerPageRoutes 注册 /s 分享页面（公开 URL）与静态文件 + SPA 兜底路由。
func (s *Server) registerPageRoutes(app *iris.Application) {
	app.Get("/s/{token}", s.handleShareAccess)
	app.Get("/s/{token}/{p:path}", s.handleShareAccess)

	// 静态文件 + SPA 兜底路由（必须最后注册）
	app.Get("/{path:path}", s.handleStaticWithAuth)
}
