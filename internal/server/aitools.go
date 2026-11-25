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
		Name: "search_files",
		Desc: "语义检索用户文件（RAG）：按自然语言问题返回最相关的文本片段（含文件路径与起始行号）。回答文件内容类问题时优先使用；前置检索的片段不够时，换关键词再次调用，不要猜测路径盲读文件",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "检索内容：关键词或自然语言问题描述",
				},
				"topK": map[string]any{
					"type":        "integer",
					"description": "返回片段数，1-10，默认 5",
				},
			},
			"required": []string{"query"},
		},
		Exec: execSearchFiles,
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
		Desc: "为当前用户创建一条新的 Todo 任务（状态为待办）。时间范围建议给全：scheduled（计划开始日）与 due（截止日）至少给 due",
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
				"scheduled": map[string]any{
					"type":        "string",
					"description": "计划开始日期 YYYY-MM-DD；用户未指定时默认与 due 相同",
				},
				"due": map[string]any{
					"type":        "string",
					"description": "截止日期 YYYY-MM-DD；用户未指定时默认当天",
				},
				"scheduledTime": map[string]any{
					"type":        "string",
					"description": "计划开始时刻 HH:mm，到点自动邮件提醒，可选",
				},
				"dueTime": map[string]any{
					"type":        "string",
					"description": "截止时刻 HH:mm，到点自动邮件提醒，可选",
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
				"title":         map[string]any{"type": "string", "description": "新标题"},
				"content":       map[string]any{"type": "string", "description": "新详细说明"},
				"status":        map[string]any{"type": "string", "enum": []string{"todo", "progress", "done"}, "description": "新状态"},
				"priority":      map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}, "description": "新优先级"},
				"scheduled":     map[string]any{"type": "string", "description": "新计划开始日期 YYYY-MM-DD"},
				"due":           map[string]any{"type": "string", "description": "新截止日期 YYYY-MM-DD"},
				"dueTime":       map[string]any{"type": "string", "description": "截止时刻 HH:mm，到点自动邮件提醒；传空串清除"},
				"scheduledTime": map[string]any{"type": "string", "description": "计划开始时刻 HH:mm，到点自动邮件提醒；传空串清除"},
				"progress":      map[string]any{"type": "integer", "description": "进度百分比 0-100"},
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
		Desc: "创建一条定时任务：到点后执行 AI 提示词、单个工具、站内接口或编排流程，并邮件通知你（执行前需要用户在界面上确认）",
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
					"description": "AI 提示词（target 缺省时必填；提供 target 时可省略）",
				},
				"target": map[string]any{
					"type":        "object",
					"description": "执行对象（可选，替代 prompt）：{type: ai|tool|interface|pipeline, prompt?, tool?, path?, method?, query?, body?, definition?, templateId?, params?}",
				},
				"allowWrite": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "静态预授权：写类工具名（如 notify_user）或非 GET 的 api/<path>（如 api/tasks）",
				},
				"retry": map[string]any{
					"type":        "integer",
					"description": "失败重试次数 0-3（仅无写步骤时生效）",
				},
			},
			"required": []string{"name", "cronExpr"},
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
				"prompt":   map[string]any{"type": "string", "description": "新 AI 提示词"},
				"target": map[string]any{
					"type":        "object",
					"description": "新执行对象：{type: ai|tool|interface|pipeline, ...}",
				},
				"allowWrite": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "静态预授权清单（覆盖原值）",
				},
				"retry":   map[string]any{"type": "integer", "description": "失败重试次数 0-3（仅无写步骤时生效）"},
				"enabled": map[string]any{"type": "boolean", "description": "启用/停用"},
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
	{
		Name: "reindex_files",
		Desc: "触发当前用户文件的全量向量索引重建（后台异步执行，耗时取决于文件数量，执行前需要用户在界面上确认）。适用于刚写入/上传的文件搜不到、或改过 embedding 配置后索引失效的场景；启动后用 reindex_status 查询进度",
		Params: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		NeedApproval: true,
		Exec:         execReindexFiles,
	},
	{
		Name: "reindex_status",
		Desc: "查询最近一次全量索引重建的进度（running/total/processed/indexed/skipped/failed/currentPath/startedAt/updatedAt）；running=false 表示最近一次已结束。只读，可反复调用",
		Params: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Exec: execReindexStatus,
	},
}

// pipelineTool 运行时合成的编排工具。不能直接放进 aiTools 包级注册表：
// aiTools → execRunPipeline → execAITool → aiToolByName → aiTools
// 会构成 Go 包级变量初始化循环，因此由 allTools 在读取时合成。
func pipelineTool() aiTool {
	return aiTool{
		Name: "run_pipeline",
		Desc: "按依赖编排执行一组能力：每步可以是现有 AI 工具、站内 API 或 LLM 生成。字符串参数支持 {{ steps.<id>.output }} 引用上一步输出；steps[].needs 声明依赖（缺省=依赖前一步即顺序执行，显式 []=无依赖可并行，填 id 列表组成 DAG），主流程失败即停；steps[].when 控制条件（on_success 默认 / on_failure / always，可写失败补偿步骤），steps[].retry 指定失败重试次数（0-5）。整条流程只需一次界面确认；写类工具与非 GET 的 api 步骤必须在 allowWrite 里显式授权。执行状态写入 pipeline_runs，可用 GET /api/pipeline/runs/{id} 查每步完整输出",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "流程名称"},
				"allowWrite": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "显式授权：写类工具名（如 reindex_files），或非 GET 的 api 步骤（如 api/tasks）",
				},
				"steps": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{"type": "string", "description": "步骤唯一 id（字母/数字/_/-），供后续步骤引用"},
							"needs": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "依赖的步骤 id；不填=依赖前一步（顺序），填 []=无依赖可与同层并行，填多个 id 组成 DAG",
							},
							"when": map[string]any{
								"type":        "string",
								"enum":        []string{"on_success", "on_failure", "always"},
								"description": "执行条件：默认 on_success（无失败才跑），on_failure 用于失败补偿，always 总是执行",
							},
							"retry": map[string]any{
								"type":        "integer",
								"description": "失败重试次数 0-5，按 200ms×次数退避",
							},
							"uses": map[string]any{"type": "string", "description": "tool/<工具名> | api/<站内 api 路径，如 ai/reindex/status> | llm"},
							"with": map[string]any{"type": "object", "description": "步骤参数；llm 用 prompt，api 可用 method/body，字符串值支持 {{ steps.<id>.output }}"},
						},
						"required": []string{"id", "uses"},
					},
				},
			},
			"required": []string{"steps"},
		},
		NeedApproval: true,
		Exec:         execRunPipeline,
	}
}

// aiToolsPayload 组装 OpenAI /chat/completions 的 tools 参数
func aiToolsPayload(tools []aiTool) []map[string]any {
	payloads := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
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
	tool := s.aiToolByName(call.Function.Name)
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
			"error":     "tool result too large, please narrow the query",
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

// execSearchFiles 语义检索用户文件（RAG）：query → 向量 KNN → 片段列表
// （含路径与起始行号）。片段自带原文，不会像 read_file 全文那样撞截断；
// 命中为空提示模型换关键词重试。
func execSearchFiles(s *Server, ctx iris.Context, args map[string]any) any {
	if s.rag == nil {
		return map[string]any{"error": "RAG not enabled (ai.embedding not configured)"}
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return map[string]any{"error": "query is required"}
	}
	topK := 5
	if v, ok := args["topK"].(float64); ok && v >= 1 && v <= 10 {
		topK = int(v)
	}
	hits, err := s.rag.Search(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), query, topK)
	if err != nil {
		return map[string]any{"error": "search failed: " + err.Error()}
	}
	if len(hits) == 0 {
		return map[string]any{"ok": true, "hits": []any{}, "hint": "无命中，换关键词重试"}
	}
	// 片段截断：topK=10 全中文长块会逼近 32KB 工具结果上限（截断=整个结果变错误提示）
	const quoteMax = 500
	trimmed := make([]map[string]any, len(hits))
	for i, h := range hits {
		content := h.Content
		if len([]rune(content)) > quoteMax {
			content = string([]rune(content)[:quoteMax]) + "…"
		}
		trimmed[i] = map[string]any{
			"path": h.Path, "startLine": h.StartLine, "content": content,
		}
	}
	return map[string]any{
		"ok":   true,
		"hits": trimmed,
		"hint": "片段含文件路径与起始行号；需要更大上下文时对该文件 read_file（路径取自 hits）",
	}
}

// execListTasks 查任务：默认只返回未完成，可选包含已完成，上限 100 条
func execListTasks(s *Server, ctx iris.Context, args map[string]any) any {
	includeDone, _ := args["includeDone"].(bool)
	tasks, err := s.db.GetTasks(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx))
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
	scheduled, _ := args["scheduled"].(string)
	today := time.Now().Format("2006-01-02")
	// 默认值：未给 due → 当天；未给 scheduled → 与 due 相同（保证任务有时间范围）
	if due == "" {
		due = today
	}
	if scheduled == "" {
		scheduled = due
	}
	if _, err := time.Parse("2006-01-02", due); err != nil {
		return map[string]any{"error": "due must be YYYY-MM-DD"}
	}
	if _, err := time.Parse("2006-01-02", scheduled); err != nil {
		return map[string]any{"error": "scheduled must be YYYY-MM-DD"}
	}
	if scheduled > due {
		return map[string]any{"error": "scheduled must not be later than due"}
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
		Scheduled:     scheduled,
		Due:           due,
		DueTime:       dueTime,
		ScheduledTime: scheduledTime,
		Progress:      0,
		Assignee:      db.FlexString(strconv.FormatInt(currentUserID(ctx), 10)),
	}
	rev, err := s.db.AddTask(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), task)
	if err != nil {
		return map[string]any{"error": "create task failed"}
	}
	return map[string]any{"ok": true, "taskId": string(task.ID), "title": title, "priority": priority,
		"scheduled": scheduled, "due": due, "revision": rev}
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

// ============================================================
// AI 定时任务工具执行器
// ============================================================

// execListAIJobs 列出当前用户的定时任务（含全局公共任务，标注只读）
func execListAIJobs(s *Server, ctx iris.Context, args map[string]any) any {
	jobs, err := s.db.ListAIJobs(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx))
	if err != nil {
		return map[string]any{"error": "list ai jobs failed"}
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, map[string]any{
			"id": j.ID, "name": j.Name, "cron": j.CronExpr,
			"enabled": j.Enabled, "isGlobal": j.IsGlobal,
			"targetType": j.TargetType, "target": j.Target, "allowWrite": j.AllowWrite,
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
	if v, ok := args["retry"].(float64); ok {
		b.Retry = int(v)
	}
	if v, ok := args["target"].(map[string]any); ok {
		if raw, err := json.Marshal(v); err == nil {
			b.Target = raw
		}
	}
	if v, ok := args["allowWrite"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				b.AllowWrite = append(b.AllowWrite, s)
			}
		}
	}
	if v, ok := args["context"].(map[string]any); ok {
		if raw, err := json.Marshal(v); err == nil {
			b.Context = raw
		}
	}
	if v, ok := args["notify"].(map[string]any); ok {
		if raw, err := json.Marshal(v); err == nil {
			b.Notify = raw
		}
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
	id, err := s.db.CreateAIJobWithSpec(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), aiJobSpecFromBody(b))
	if err != nil {
		return map[string]any{"error": "create ai job failed"}
	}
	if err := s.upsertAIJobTrigger(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), id, b.CronExpr, *b.Enabled); err != nil {
		_ = s.db.DeleteAIJob(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), id)
		return map[string]any{"error": "create ai job trigger failed"}
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
	cur, err := s.db.GetAIJob(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), int64(jobID))
	if err != nil {
		return map[string]any{"error": "get ai job failed"}
	}
	if cur == nil {
		return map[string]any{"error": "job not found (系统公共任务不可修改)"}
	}
	// 合并：未提供的字段沿用现值
	merged := &aiJobBody{
		Name: cur.Name, CronExpr: cur.CronExpr, Prompt: cur.Prompt,
		Target: cur.Target, AllowWrite: cur.AllowWrite, Context: cur.Context, Notify: cur.Notify,
	}
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
	if v, ok := args["target"].(map[string]any); ok {
		if raw, err := json.Marshal(v); err == nil {
			merged.Target = raw
		}
	}
	if v, ok := args["allowWrite"].([]any); ok {
		list := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				list = append(list, s)
			}
		}
		merged.AllowWrite = list
	}
	if v, ok := args["context"].(map[string]any); ok {
		if raw, err := json.Marshal(v); err == nil {
			merged.Context = raw
		}
	}
	if v, ok := args["notify"].(map[string]any); ok {
		if raw, err := json.Marshal(v); err == nil {
			merged.Notify = raw
		}
	}
	if msg := validateAIJobBody(merged); msg != "" {
		return map[string]any{"error": msg}
	}
	if err := s.db.UpdateAIJobWithSpec(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), int64(jobID), aiJobSpecFromBody(merged)); err != nil {
		return map[string]any{"error": "update ai job failed"}
	}
	if err := s.upsertAIJobTrigger(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), int64(jobID), merged.CronExpr, *merged.Enabled); err != nil {
		return map[string]any{"error": "update ai job trigger failed"}
	}
	return map[string]any{"ok": true, "jobId": int64(jobID), "name": merged.Name, "cron": merged.CronExpr}
}

// execDeleteAIJob 删除定时任务（写类，审批后执行）
func execDeleteAIJob(s *Server, ctx iris.Context, args map[string]any) any {
	jobID, _ := args["jobId"].(float64)
	if jobID <= 0 {
		return map[string]any{"error": "jobId is required"}
	}
	cur, err := s.db.GetAIJob(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), int64(jobID))
	if err != nil {
		return map[string]any{"error": "get ai job failed"}
	}
	if cur == nil {
		return map[string]any{"error": "job not found"}
	}
	if err := s.deleteAIJobTriggers(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), int64(jobID)); err != nil {
		return map[string]any{"error": "delete ai job triggers failed"}
	}
	if err := s.db.DeleteAIJob(aiToolRunCtx(ctx), currentOrgID(ctx), currentUserID(ctx), int64(jobID)); err != nil {
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
			"topic":       kt.Topic,
			"description": kt.Desc,
			"subscribers": subs[kt.Topic],
			"publishable": kt.Topic == schedule.TopicUserNotify, // AI 当前仅可发布用户通知
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
	s.bus.Publish(aiToolRunCtx(ctx), schedule.Event{
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
		"ok":    true,
		"topic": schedule.TopicUserNotify,
		"note":  "事件已发布，投递异步进行（用户未配置邮箱时跳过）",
	}
}

// 索引重建工具：reindex_status 只读；reindex_files 为写类，经审批后才到达这里。
// 两者共用 startReindex / ProgressOf，与 POST /api/ai/reindex 语义一致。
func execReindexStatus(s *Server, ctx iris.Context, args map[string]any) any {
	if s.rag == nil {
		return map[string]any{"error": "RAG（ai.embedding）未配置，没有重建进度"}
	}
	p := s.rag.ProgressOf(currentOrgID(ctx), currentUserID(ctx))
	if p == nil {
		return map[string]any{"running": false, "note": "尚未触发过全量重建（reindex_files）"}
	}
	return p
}

func execReindexFiles(s *Server, ctx iris.Context, args map[string]any) any {
	switch s.startReindex(ctx) {
	case reindexUnavailable:
		return map[string]any{"error": "RAG（ai.embedding）未配置，无法重建索引"}
	case reindexAlreadyRunning:
		return map[string]any{"error": "全量重建已在进行中，请用 reindex_status 查询进度"}
	default:
		return map[string]any{
			"started": true,
			"note":    "重建在后台异步执行，请用 reindex_status 查询进度",
		}
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
	// RAG 索引钩子：AI 写入的文件同样进语义索引
	if s.rag != nil {
		s.rag.HandleWrite(currentOrgID(ctx), currentUserID(ctx), dst, parentRel+"/"+name)
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
	rctx := aiToolRunCtx(ctx)
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
	if v, ok := args["scheduled"].(string); ok {
		if v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return map[string]any{"error": "scheduled must be YYYY-MM-DD"}
			}
		}
		cur.Scheduled = v
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
