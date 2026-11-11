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
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
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
	Exec   func(s *Server, ctx iris.Context, args map[string]any) any
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
			},
			"required": []string{"title"},
		},
		Exec: execCreateTask,
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
	var tool *aiTool
	for i := range aiTools {
		if aiTools[i].Name == call.Function.Name {
			tool = &aiTools[i]
			break
		}
	}
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
	content, _ := args["content"].(string)

	task := db.TaskItem{
		ID:       db.FlexString("ai-" + newAITaskID()),
		Title:    title,
		Content:  content,
		Status:   "todo",
		Priority: priority,
		Due:      due,
		Progress: 0,
		Assignee: db.FlexString(strconv.FormatInt(currentUserID(ctx), 10)),
	}
	rev, err := s.db.AddTask(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), task)
	if err != nil {
		return map[string]any{"error": "create task failed"}
	}
	return map[string]any{"ok": true, "taskId": string(task.ID), "title": title, "priority": priority, "due": due, "revision": rev}
}

// newAITaskID 生成 AI 建任务用的 ID：毫秒时间戳 + 4 字节随机数
func newAITaskID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}
