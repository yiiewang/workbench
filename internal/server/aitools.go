package server

// AI 工具调用（Function Calling）：内置工具注册表 + 执行分发。
//
// 设计要点：
//   - 工具只在后端执行：文件路径一律经 resolveUserPathSafe 沙箱校验
//     （safeJoin + EvalSymlinks），复用现有 HTTP 接口同等的安全边界，
//     前端只做展示，永远拿不到"执行权"；
//   - 工具结果作为 tool 消息回传上游模型，多轮循环见 ai.go 的 handleAIChat；
//   - 结果统一限长（aiToolMaxResult），防单个工具结果撑爆模型上下文；
//   - execAITool 对执行器 panic 兜底，任何工具异常都以 {"error": ...}
//     形式回传模型，模型可据此向用户解释失败原因，而不是中断整次对话。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/schedule"
)

// aiToolMaxResult 工具结果序列化后的大小上限：超出则截断为错误提示，
// 防止单个工具返回（如超大目录列表）撑爆模型上下文。
const aiToolMaxResult = 32 * 1024

// aiReadFileMaxBytes read_file 工具返回内容上限（超出截断，文件本体限 4MB）
const aiReadFileMaxBytes = 64 * 1024

// aiTool 单个内置工具：JSON Schema 描述 + 执行器。
// args 为模型给出的参数（已从 arguments JSON 字符串解析为 map），
// 返回值会被序列化为 tool 消息内容回传模型。
type aiTool struct {
	Name   string
	Desc   string
	Params map[string]any
	// NeedApproval 写类工具标记：执行前需经用户确认（见 aiapproval.go），
// 审批不通过时模型收到的结果是拒绝说明，由模型向用户解释。
	NeedApproval bool
	Exec         func(s *Server, ctx iris.Context, args map[string]any) any
}

// aiTools 工具注册表：顺序稳定（tools payload 顺序会影响模型选择倾向）
var aiTools = []aiTool{
	{
		Name: "list_dir",
		Desc: "列出用户文件目录下的一层条目（名称/是否目录/大小）。path 为相对用户根目录的路径，根目录传 /",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "目录路径，如 /docs 或 /（用户根目录）",
				},
			},
			"required": []string{"path"},
		},
		Exec: execListDir,
	},
	{
		Name: "read_file",
		Desc: "读取用户文件目录下的文本文件内容（代码/文档/JSON 等，超长自动截断，二进制文件不支持）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "文件路径，如 /docs/readme.md",
				},
			},
			"required": []string{"path"},
		},
		Exec: execReadFile,
	},
	{
		Name: "list_tasks",
		Desc: "查询当前用户的 Todo 任务列表（默认只返回未完成的，含标题/状态/优先级/截止日）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"includeDone": map[string]any{
					"type":        "boolean",
					"description": "是否包含已完成任务，默认 false",
				},
			},
		},
		Exec: execListTasks,
	},
	{
		Name: "create_task",
		Desc: "为当前用户创建一条新的 Todo 任务（状态为待办）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "任务标题，必填",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "任务详细说明，可选",
				},
				"priority": map[string]any{
					"type":        "string",
					"enum":        []string{"high", "medium", "low"},
					"description": "优先级，默认 medium",
				},
				"due": map[string]any{
					"type":        "string",
					"description": "截止日期，格式 YYYY-MM-DD，可选",
				},
				"dueTime": map[string]any{
					"type":        "string",
					"description": "截止时刻 HH:mm（需同时给 due），到点自动邮件提醒，可选",
				},
				"scheduledTime": map[string]any{
					"type":        "string",
					"description": "计划开始时刻 HH:mm，到点自动邮件提醒，可选",
				},
			},
			"required": []string{"title"},
		},
		Exec: execCreateTask,
	},
	{
		Name: "write_file",
		Desc: "在用户文件目录写入或覆盖一个文本文件（执行前需要用户在界面上确认）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "目标文件路径，如 /docs/note.md（父目录必须已存在）",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "文件完整内容（文本）",
				},
			},
			"required": []string{"path", "content"},
		},
		NeedApproval: true,
		Exec:         execWriteFile,
	},
	{
		Name: "update_task",
		Desc: "更新当前用户已有的一条 Todo 任务（只更新提供的字段；执行前需要用户在界面上确认）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"taskId": map[string]any{
					"type":        "string",
					"description": "任务 ID（可用 list_tasks 查询），必填",
				},
				"title":    map[string]any{"type": "string", "description": "新标题"},
				"content":  map[string]any{"type": "string", "description": "新详细说明"},
				"status":   map[string]any{"type": "string", "enum": []string{"todo", "progress", "done"}, "description": "新状态"},
				"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}, "description": "新优先级"},
				"due":      map[string]any{"type": "string", "description": "新截止日期 YYYY-MM-DD"},
				"dueTime":  map[string]any{"type": "string", "description": "截止时刻 HH:mm，到点自动邮件提醒；传空串清除"},
				"scheduledTime": map[string]any{"type": "string", "description": "计划开始时刻 HH:mm，到点自动邮件提醒；传空串清除"},
				"progress": map[string]any{"type": "integer", "description": "进度百分比 0-100"},
			},
			"required": []string{"taskId"},
		},
		NeedApproval: true,
		Exec:         execUpdateTask,
	},
	{
		Name: "list_events",
		Desc: "查看事件总线上有哪些事件（topic）：各事件的用途说明与当前订阅者数量。据此了解可以触发什么",
		Params: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Exec: execListEvents,
	},
	{
		Name: "list_ai_jobs",
		Desc: "查看当前用户的 AI 定时任务列表（名称/cron/启用状态/最近执行，含系统公共任务）",
		Params: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Exec: execListAIJobs,
	},
	{
		Name: "create_ai_job",
		Desc: "创建一条 AI 定时任务：到点后自动执行指令（附带你的任务数据）并邮件通知你（执行前需要用户在界面上确认）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "任务名称，如：每日工作总结",
				},
				"cronExpr": map[string]any{
					"type":        "string",
					"description": "5 段 cron（分 时 日 月 周），如 \"0 18 * * *\" = 每天 18:00",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "到点后执行的指令内容，如：总结我今天完成的任务并生成日报",
				},
			},
			"required": []string{"name", "cronExpr", "prompt"},
		},
		NeedApproval: true,
		Exec:         execCreateAIJob,
	},
	{
		Name: "update_ai_job",
		Desc: "更新当前用户的 AI 定时任务（只更新提供的字段；执行前需要用户在界面上确认）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"jobId":    map[string]any{"type": "integer", "description": "任务 ID（可用 list_ai_jobs 查询），必填"},
				"name":     map[string]any{"type": "string", "description": "新名称"},
				"cronExpr": map[string]any{"type": "string", "description": "新 cron 表达式（分 时 日 月 周）"},
				"prompt":   map[string]any{"type": "string", "description": "新执行指令"},
				"enabled":  map[string]any{"type": "boolean", "description": "启用/停用"},
			},
			"required": []string{"jobId"},
		},
		NeedApproval: true,
		Exec:         execUpdateAIJob,
	},
	{
		Name: "delete_ai_job",
		Desc: "删除当前用户的 AI 定时任务（执行前需要用户在界面上确认）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"jobId": map[string]any{"type": "integer", "description": "任务 ID（可用 list_ai_jobs 查询），必填"},
			},
			"required": []string{"jobId"},
		},
		NeedApproval: true,
		Exec:         execDeleteAIJob,
	},
	{
		Name: "notify_user",
		Desc: "向当前用户发送一条自定义通知（经事件总线投递，邮件渠道需用户已配置邮箱；执行前需要用户在界面上确认）",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subject": map[string]any{
					"type":        "string",
					"description": "通知主题（邮件标题），必填",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "通知正文（纯文本，支持换行），必填",
				},
			},
			"required": []string{"subject", "body"},
		},
		NeedApproval: true,
		Exec:         execNotifyUser,
	},
}

// aiToolsPayload 组装 OpenAI /chat/completions 的 tools 参数
func aiToolsPayload() []map[string]any {
	payloads := make([]map[string]any, 0, len(aiTools))
	for _, t := range aiTools {
		payloads = append(payloads, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Desc,
				"parameters":  t.Params,
			},
		})
	}
	return payloads
}

// execAITool 分发执行一次工具调用：
// 解析 arguments JSON → 查注册表 → 执行（panic 兜底）→ 结果限长。
func (s *Server) execAITool(ctx iris.Context, call aiToolCall) (result any) {
	tool := aiToolByName(call.Function.Name)
	if tool == nil {
		return map[string]any{"error": "unknown tool: " + call.Function.Name}
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Error("ai tool panic", "tool", tool.Name, "panic", r)
			result = map[string]any{"error": fmt.Sprintf("tool internal error: %v", r)}
		}
	}()

	args := map[string]any{}
	if strings.TrimSpace(call.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return map[string]any{"error": "invalid arguments JSON: " + err.Error()}
		}
	}

	slog.Info("ai tool call", "user", currentUserID(ctx), "tool", tool.Name, "args", call.Function.Arguments)

	result = tool.Exec(s, ctx, args)
	if b, err := json.Marshal(result); err == nil && len(b) > aiToolMaxResult {
		return map[string]any{
			"error":    "tool result too large, please narrow the query",
			"truncated": string(b[:aiToolMaxResult]),
		}
	}
	return result
}

// ============================================================
// 工具执行器
// ============================================================

// execListDir 列目录：路径沙箱校验 → ReadDir → 过滤隐藏条目
func execListDir(s *Server, ctx iris.Context, args map[string]any) any {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	fsPath, _, ok := s.resolveUserPathSafe(ctx, path)
	if !ok {
		return map[string]any{"error": "invalid path"}
	}
	entries, err := os.ReadDir(fsPath)
	if err != nil {
		return map[string]any{"error": "read dir failed: " + err.Error()}
	}
	items := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if s.isHidden(e.Name()) {
			continue
		}
		item := map[string]any{"name": e.Name(), "isDir": e.IsDir()}
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				item["size"] = info.Size()
			}
		}
		items = append(items, item)
	}
	return map[string]any{"path": path, "entries": items}
}

// execReadFile 读文本文件：本体限 4MB，内容限 64KB（超出截断），粗判二进制
func execReadFile(s *Server, ctx iris.Context, args map[string]any) any {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return map[string]any{"error": "path is required"}
	}
	fsPath, cleanRel, ok := s.resolveUserPathSafe(ctx, path)
	if !ok {
		return map[string]any{"error": "invalid path"}
	}
	info, err := os.Stat(fsPath)
	if err != nil {
		return map[string]any{"error": "file not found"}
	}
	if info.IsDir() {
		return map[string]any{"error": "path is a directory, use list_dir instead"}
	}
	if info.Size() > 4*1024*1024 {
		return map[string]any{"error": "file too large (> 4MB)"}
	}
	data, err := os.ReadFile(fsPath)
	if err != nil {
		return map[string]any{"error": "read file failed"}
	}
	// 粗判二进制：前 512 字节含 NUL 视为二进制文件
	scan := data
	if len(scan) > 512 {
		scan = scan[:512]
	}
	for _, b := range scan {
		if b == 0 {
			return map[string]any{"error": "binary file not supported"}
		}
	}
	content := string(data)
	if len(content) > aiReadFileMaxBytes {
		content = content[:aiReadFileMaxBytes] + "\n…（内容过长，已截断）"
	}
	return map[string]any{"path": cleanRel, "size": info.Size(), "content": content}
}

// execListTasks 查任务：默认只返回未完成，可选包含已完成，上限 100 条
func execListTasks(s *Server, ctx iris.Context, args map[string]any) any {
	includeDone, _ := args["includeDone"].(bool)
	tasks, err := s.db.GetTasks(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx))
	if err != nil {
		return map[string]any{"error": "get tasks failed"}
	}
	items := make([]map[string]any, 0, 16)
	for _, t := range tasks {
		if !includeDone && t.Status == "done" {
			continue
		}
		items = append(items, map[string]any{
			"id":       string(t.ID),
			"title":    t.Title,
			"status":   t.Status,
			"priority": t.Priority,
			"due":      t.Due,
			"progress": t.Progress,
		})
		if len(items) >= 100 {
			break
		}
	}
	return map[string]any{"tasks": items, "total": len(items)}
}

// execCreateTask 建任务：字段取值与前端 Todo 应用保持一致
// （status: todo/progress/done；priority: high/medium/low；due: YYYY-MM-DD）
func execCreateTask(s *Server, ctx iris.Context, args map[string]any) any {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return map[string]any{"error": "title is required"}
	}
	priority, _ := args["priority"].(string)
	switch priority {
	case "high", "medium", "low":
	default:
		priority = "medium"
	}
	due, _ := args["due"].(string)
	if due != "" {
		if _, err := time.Parse("2006-01-02", due); err != nil {
			return map[string]any{"error": "due must be YYYY-MM-DD"}
		}
	}
	dueTime, _ := args["dueTime"].(string)
	scheduledTime, _ := args["scheduledTime"].(string)
	if !validHHMM(dueTime) || !validHHMM(scheduledTime) {
		return map[string]any{"error": "dueTime/scheduledTime must be HH:mm"}
	}
	content, _ := args["content"].(string)

	task := db.TaskItem{
		ID:            db.FlexString("ai-" + newAITaskID()),
		Title:         title,
		Content:       content,
		Status:        "todo",
		Priority:      priority,
		Due:           due,
		DueTime:       dueTime,
		ScheduledTime: scheduledTime,
		Progress:      0,
		Assignee:      db.FlexString(strconv.FormatInt(currentUserID(ctx), 10)),
	}
	rev, err := s.db.AddTask(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), task)
	if err != nil {
		return map[string]any{"error": "create task failed"}
	}
	return map[string]any{"ok": true, "taskId": string(task.ID), "title": title, "priority": priority, "due": due, "revision": rev}
}

// validHHMM 校验 HH:mm 时刻（空串合法 = 无精确时间）
func validHHMM(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}

// newAITaskID 生成 AI 建任务用的 ID：毫秒时间戳 + 4 字节随机数
func newAITaskID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}

// aiToolByName 按名称查找工具（Agent 循环判断是否需要审批时用）
func aiToolByName(name string) *aiTool {
	for i := range aiTools {
		if aiTools[i].Name == name {
			return &aiTools[i]
		}
	}
	return nil
}

// ============================================================
// AI 定时任务工具执行器
// ============================================================

// execListAIJobs 列出当前用户的定时任务（含全局公共任务，标注只读）
func execListAIJobs(s *Server, ctx iris.Context, args map[string]any) any {
	jobs, err := s.db.ListAIJobs(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx))
	if err != nil {
		return map[string]any{"error": "list ai jobs failed"}
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, map[string]any{
			"id": j.ID, "name": j.Name, "cron": j.CronExpr,
			"enabled": j.Enabled, "isGlobal": j.IsGlobal,
			"lastRunAt": j.LastRunAt, "lastStatus": j.LastStatus,
		})
	}
	return map[string]any{"jobs": items}
}

// ============================================================
// 事件总线工具执行器
// ============================================================

// aiJobArgsToBody 模型参数 → aiJobBody（复用 REST 校验），ok=false 时返回错误
func aiJobArgsToBody(args map[string]any) (*aiJobBody, string) {
	b := &aiJobBody{}
	b.Name, _ = args["name"].(string)
	b.CronExpr, _ = args["cronExpr"].(string)
	b.Prompt, _ = args["prompt"].(string)
	if v, ok := args["enabled"].(bool); ok {
		b.Enabled = &v
	}
	if msg := validateAIJobBody(b); msg != "" {
		return nil, msg
	}
	return b, ""
}

// execCreateAIJob 创建定时任务（写类，审批后执行）
func execCreateAIJob(s *Server, ctx iris.Context, args map[string]any) any {
	b, msg := aiJobArgsToBody(args)
	if b == nil {
		return map[string]any{"error": msg}
	}
	id, err := s.db.CreateAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), b.Name, b.CronExpr, b.Prompt, *b.Enabled)
	if err != nil {
		return map[string]any{"error": "create ai job failed"}
	}
	slog.Info("ai tool create ai job", "user", currentUserID(ctx), "job", b.Name, "cron", b.CronExpr)
	return map[string]any{"ok": true, "jobId": id, "name": b.Name, "cron": b.CronExpr, "note": "下一分钟生效"}
}

// execUpdateAIJob 更新定时任务（写类，审批后执行）：先读现有合并，全局任务不可改
func execUpdateAIJob(s *Server, ctx iris.Context, args map[string]any) any {
	jobID, _ := args["jobId"].(float64)
	if jobID <= 0 {
		return map[string]any{"error": "jobId is required"}
	}
	cur, err := s.db.GetAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), int64(jobID))
	if err != nil {
		return map[string]any{"error": "get ai job failed"}
	}
	if cur == nil {
		return map[string]any{"error": "job not found (系统公共任务不可修改)"}
	}
	// 合并：未提供的字段沿用现值
	merged := &aiJobBody{Name: cur.Name, CronExpr: cur.CronExpr, Prompt: cur.Prompt}
	enabled := cur.Enabled
	merged.Enabled = &enabled
	if v, ok := args["name"].(string); ok && strings.TrimSpace(v) != "" {
		merged.Name = v
	}
	if v, ok := args["cronExpr"].(string); ok && strings.TrimSpace(v) != "" {
		merged.CronExpr = v
	}
	if v, ok := args["prompt"].(string); ok && strings.TrimSpace(v) != "" {
		merged.Prompt = v
	}
	if v, ok := args["enabled"].(bool); ok {
		merged.Enabled = &v
	}
	if msg := validateAIJobBody(merged); msg != "" {
		return map[string]any{"error": msg}
	}
	if err := s.db.UpdateAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), int64(jobID), merged.Name, merged.CronExpr, merged.Prompt, *merged.Enabled); err != nil {
		return map[string]any{"error": "update ai job failed"}
	}
	return map[string]any{"ok": true, "jobId": int64(jobID), "name": merged.Name, "cron": merged.CronExpr}
}

// execDeleteAIJob 删除定时任务（写类，审批后执行）
func execDeleteAIJob(s *Server, ctx iris.Context, args map[string]any) any {
	jobID, _ := args["jobId"].(float64)
	if jobID <= 0 {
		return map[string]any{"error": "jobId is required"}
	}
	cur, err := s.db.GetAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), int64(jobID))
	if err != nil {
		return map[string]any{"error": "get ai job failed"}
	}
	if cur == nil {
		return map[string]any{"error": "job not found"}
	}
	if err := s.db.DeleteAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), int64(jobID)); err != nil {
		return map[string]any{"error": "delete ai job failed"}
	}
	return map[string]any{"ok": true, "deleted": cur.Name}
}



// execListEvents 总线内省（只读）：KnownTopics 注册表 + 实时订阅者数。
// 让 AI 知道总线上"能触发什么、谁在听"。
func execListEvents(s *Server, ctx iris.Context, args map[string]any) any {
	if s.bus == nil {
		return map[string]any{"error": "event bus not available"}
	}
	subs := s.bus.Topics()
	events := make([]map[string]any, 0, len(schedule.KnownTopics))
	for _, kt := range schedule.KnownTopics {
		events = append(events, map[string]any{
			"topic":        kt.Topic,
			"description":  kt.Desc,
			"subscribers":  subs[kt.Topic],
			"publishable":  kt.Topic == schedule.TopicUserNotify, // AI 当前仅可发布用户通知
		})
	}
	return map[string]any{"events": events}
}

// execNotifyUser 发布用户自定义通知事件（写类，经审批后执行）：
// 发布是异步的（订阅者各自 goroutine 处理），工具返回即"已受理"，
// 实际投递结果由 EmailNotifier 记日志。收件人 = 当前用户邮箱。
func execNotifyUser(s *Server, ctx iris.Context, args map[string]any) any {
	if s.bus == nil {
		return map[string]any{"error": "event bus not available"}
	}
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return map[string]any{"error": "subject is required"}
	}
	if len(body) > 32*1024 {
		return map[string]any{"error": "body too large (max 32KB)"}
	}
	s.bus.Publish(ctx.Request().Context(), schedule.Event{
		Topic: schedule.TopicUserNotify,
		Payload: schedule.UserNotifyEvent{
			UserID:  currentUserID(ctx),
			OrgID:   currentOrgID(ctx),
			Subject: subject,
			Body:    body,
		},
	})
	slog.Info("ai tool publish user.notify", "user", currentUserID(ctx), "subject", subject)
	return map[string]any{
		"ok":     true,
		"topic":  schedule.TopicUserNotify,
		"note":   "事件已发布，投递异步进行（用户未配置邮箱时跳过）",
	}
}

// ============================================================
// 写类工具执行器（均需用户审批后才到达这里）
// ============================================================

// aiWriteFileMaxBytes write_file 单次写入内容上限
const aiWriteFileMaxBytes = 256 * 1024

// execWriteFile 写文件：目标路径拆父目录 + 文件名（目标可不存在，
// 不能直接 resolveUserPathSafe——EvalSymlinks 要求路径存在），
// 父目录必须已存在且在沙箱内，语义与 handleMkdir 一致。
func execWriteFile(s *Server, ctx iris.Context, args map[string]any) any {
	path, _ := args["path"].(string)
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimSuffix(path, "/")
	content, _ := args["content"].(string)
	if strings.TrimSpace(path) == "" || path == "/" {
		return map[string]any{"error": "path is required"}
	}
	if len(content) > aiWriteFileMaxBytes {
		return map[string]any{"error": "content too large (max 256KB)"}
	}
	idx := strings.LastIndex(path, "/")
	parent, name := path[:idx], path[idx+1:]
	if parent == "" {
		parent = "/"
	}
	if !validEntryName(name) {
		return map[string]any{"error": "invalid file name"}
	}
	parentFs, parentRel, ok := s.resolveUserPathSafe(ctx, parent)
	if !ok {
		return map[string]any{"error": "invalid path"}
	}
	if info, err := os.Stat(parentFs); err != nil || !info.IsDir() {
		return map[string]any{"error": "parent directory not found"}
	}
	overwritten := false
	dst := filepath.Join(parentFs, name)
	if _, err := os.Lstat(dst); err == nil {
		overwritten = true
	}
	if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
		return map[string]any{"error": "write file failed: " + err.Error()}
	}
	return map[string]any{
		"ok":          true,
		"path":        parentRel + "/" + name,
		"overwritten": overwritten,
		"bytes":       len(content),
	}
}

// execUpdateTask 改任务：先读现有任务（校验归属 + 保留未提供字段——
// db.UpdateTask 是全字段覆盖写，不能只带改动字段直接写），
// 再合并模型提供的字段后写回。
func execUpdateTask(s *Server, ctx iris.Context, args map[string]any) any {
	taskID, _ := args["taskId"].(string)
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return map[string]any{"error": "taskId is required"}
	}
	rctx := ctx.Request().Context()
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	tasks, err := s.db.GetTasks(rctx, orgID, userID)
	if err != nil {
		return map[string]any{"error": "get tasks failed"}
	}
	var cur *db.TaskItem
	for i := range tasks {
		if string(tasks[i].ID) == taskID {
			cur = &tasks[i]
			break
		}
	}
	if cur == nil {
		return map[string]any{"error": "task not found: " + taskID}
	}

	// 逐字段合并（仅采用合法取值，非法值返回错误让模型纠正）
	if v, ok := args["title"].(string); ok && strings.TrimSpace(v) != "" {
		cur.Title = strings.TrimSpace(v)
	}
	if v, ok := args["content"].(string); ok {
		cur.Content = v
	}
	if v, ok := args["status"].(string); ok {
		switch v {
		case "todo", "progress", "done":
			cur.Status = v
			if v == "done" {
				cur.Progress = 100
			}
		default:
			return map[string]any{"error": "status must be todo|progress|done"}
		}
	}
	if v, ok := args["priority"].(string); ok {
		switch v {
		case "high", "medium", "low":
			cur.Priority = v
		default:
			return map[string]any{"error": "priority must be high|medium|low"}
		}
	}
	if v, ok := args["due"].(string); ok {
		if v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return map[string]any{"error": "due must be YYYY-MM-DD"}
			}
		}
		cur.Due = v
	}
	if v, ok := args["dueTime"].(string); ok {
		if !validHHMM(v) {
			return map[string]any{"error": "dueTime must be HH:mm"}
		}
		cur.DueTime = v
	}
	if v, ok := args["scheduledTime"].(string); ok {
		if !validHHMM(v) {
			return map[string]any{"error": "scheduledTime must be HH:mm"}
		}
		cur.ScheduledTime = v
	}
	if v, ok := args["progress"].(float64); ok { // JSON number 解析为 float64
		p := int(v)
		if p < 0 || p > 100 {
			return map[string]any{"error": "progress must be 0-100"}
		}
		cur.Progress = p
	}

	rev, err := s.db.UpdateTask(rctx, orgID, userID, *cur)
	if err != nil {
		return map[string]any{"error": "update task failed"}
	}
	return map[string]any{
		"ok": true, "taskId": taskID, "title": cur.Title,
		"status": cur.Status, "priority": cur.Priority,
		"due": cur.Due, "progress": cur.Progress, "revision": rev,
	}
}
