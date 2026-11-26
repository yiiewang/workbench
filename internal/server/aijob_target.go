package server

// 定时任务执行对象（ai_jobs.target_json）的解析与校验。
//
// 四种 target 统一归一化为 pipeline.Definition，交给 runPipeline：
//   ai        → 单 llm 步（prelude 渲染 prompt + 任务数据 + RAG）
//   tool      → 单 tool/<name> 步
//   interface → 单 api/<path> 步（站内 /api/*）
//   pipeline  → 原样 Definition（内联）
//
// 校验先走编排既有 Definition.Validate，再补调度专用规则：
//   - 递归/自修改黑名单（禁止再起对话、改任务配置、触发编排）
//   - allowWrite 静态预授权预检（与执行期共用同一判定）
//   - 外部 MCP 工具 P0 不放开（NeedApproval 且不在 allowWrite 时自然失败）

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
	"github.com/yiiewang/workbench/internal/rag"
)

// aiJobTarget ai_jobs.target_json 的结构。
type aiJobTarget struct {
	Type       string               `json:"type"`
	Prompt     string               `json:"prompt,omitempty"`
	Tool       string               `json:"tool,omitempty"`
	Method     string               `json:"method,omitempty"`
	Path       string               `json:"path,omitempty"`
	Query      map[string]any       `json:"query,omitempty"`
	Body       any                  `json:"body,omitempty"`
	Definition *pipeline.Definition `json:"definition,omitempty"`
	Params     map[string]any       `json:"params,omitempty"`
}

// aiJobContext ai_jobs.context_json 的结构；缺省 = 带任务数据 + RAG top3。
type aiJobContext struct {
	Tasks   *bool `json:"tasks,omitempty"`
	RagTopK *int  `json:"ragTopK,omitempty"`
}

const (
	aiJobTypeAI        = "ai"
	aiJobTypeTool      = "tool"
	aiJobTypeInterface = "interface"
	aiJobTypePipeline  = "pipeline"
)

// scheduledJobBlacklist 定时执行禁止的 uses：再起后台对话、改任务配置、递归触发编排。
var scheduledJobBlacklist = map[string]bool{
	"tool/run_pipeline":  true,
	"tool/create_ai_job": true,
	"tool/update_ai_job": true,
	"tool/delete_ai_job": true,
	"api/ai/chat":        true,
}

// parseAIJobTarget 解析 target_json；空值按历史 prompt 兼容为 ai。
func parseAIJobTarget(job db.AIJob) (aiJobTarget, error) {
	var t aiJobTarget
	if len(job.Target) > 0 {
		if err := json.Unmarshal(job.Target, &t); err != nil {
			return t, fmt.Errorf("invalid target json: %w", err)
		}
	}
	if t.Type == "" {
		t.Type = job.TargetType
	}
	if t.Type == "" {
		t.Type = aiJobTypeAI
	}
	if t.Type == aiJobTypeAI && strings.TrimSpace(t.Prompt) == "" {
		t.Prompt = job.Prompt
	}
	return t, t.validate()
}

func (t aiJobTarget) validate() error {
	switch t.Type {
	case aiJobTypeAI:
		if strings.TrimSpace(t.Prompt) == "" {
			return fmt.Errorf("ai target requires prompt")
		}
	case aiJobTypeTool:
		if strings.TrimSpace(t.Tool) == "" {
			return fmt.Errorf("tool target requires tool")
		}
	case aiJobTypeInterface:
		if strings.TrimSpace(t.Path) == "" {
			return fmt.Errorf("interface target requires path")
		}
	case aiJobTypePipeline:
		if t.Definition == nil {
			return fmt.Errorf("pipeline target requires definition")
		}
	default:
		return fmt.Errorf("unknown target type %q", t.Type)
	}
	return nil
}

// resolveAIJobTarget 把 target 归一化为可执行的 Definition。
func (s *Server) resolveAIJobTarget(ctx context.Context, job db.AIJobWithOwner) (pipeline.Definition, error) {
	return s.buildAIJobDefinition(ctx, job, true)
}

// validateAIJobTarget 创建/更新时的预检：只归一化结构，不查任务数据与 RAG，
// 再复用调度专用校验（黑名单 + allowWrite），避免「创建时放过、执行时才失败」。
func (s *Server) validateAIJobTarget(ctx context.Context, job db.AIJobWithOwner) error {
	def, err := s.buildAIJobDefinition(ctx, job, false)
	if err != nil {
		return err
	}
	return s.validateAIJobDefinition(def)
}

// buildAIJobDefinition 归一化四类 target；renderPrompt=false 时 ai 直接用原始 prompt。
func (s *Server) buildAIJobDefinition(ctx context.Context, job db.AIJobWithOwner, renderPrompt bool) (pipeline.Definition, error) {
	t, err := parseAIJobTarget(job.AIJob)
	if err != nil {
		return pipeline.Definition{}, err
	}
	var def pipeline.Definition
	switch t.Type {
	case aiJobTypeAI:
		prompt := t.Prompt
		if renderPrompt {
			rendered, rerr := s.renderAIJobPrompt(ctx, job, t)
			if rerr != nil {
				return pipeline.Definition{}, rerr
			}
			prompt = rendered
		}
		def = pipeline.Definition{
			Name:  job.Name,
			Steps: []pipeline.Step{{ID: "run", Uses: "llm", With: map[string]any{"prompt": prompt}}},
		}
	case aiJobTypeTool:
		def = pipeline.Definition{
			Name:  job.Name,
			Steps: []pipeline.Step{{ID: "run", Uses: "tool/" + t.Tool, With: t.Params}},
		}
	case aiJobTypeInterface:
		method := strings.ToUpper(strings.TrimSpace(t.Method))
		if method == "" {
			method = http.MethodGet
		}
		with := map[string]any{"method": method}
		if t.Query != nil {
			with["query"] = t.Query
		}
		if t.Body != nil {
			with["body"] = t.Body
		}
		def = pipeline.Definition{
			Name:  job.Name,
			Steps: []pipeline.Step{{ID: "run", Uses: "api/" + strings.TrimPrefix(t.Path, "/"), With: with}},
		}
	case aiJobTypePipeline:
		def = *t.Definition
		if def.Name == "" {
			def.Name = job.Name
		}
		if len(t.Params) > 0 {
			def = expandJobParams(def, t.Params)
		}
	}
	// 定时执行的授权以任务创建时的静态清单为准；Definition 自带的 allowWrite 不参与。
	def.AllowWrite = job.AllowWrite
	return def, nil
}

// expandJobParams 把 {{run.*}} / {{params.*}} 展开到每个步骤的 with。
// 参数值本身也可引用 {{run.date}} / {{run.datetime}}。
func expandJobParams(def pipeline.Definition, params map[string]any) pipeline.Definition {
	repl := map[string]string{
		"{{run.date}}":     time.Now().Format("2006-01-02"),
		"{{run.datetime}}": time.Now().Format("2006-01-02 15:04"),
	}
	for k, v := range params {
		val := fmt.Sprint(v)
		for ph, pv := range repl {
			val = strings.ReplaceAll(val, ph, pv)
		}
		repl["{{run."+k+"}}"] = val
		repl["{{params."+k+"}}"] = val
	}
	for i := range def.Steps {
		if def.Steps[i].With != nil {
			def.Steps[i].With = replaceJobParams(def.Steps[i].With, repl).(map[string]any)
		}
	}
	return def
}

func replaceJobParams(v any, repl map[string]string) any {
	switch x := v.(type) {
	case string:
		for ph, pv := range repl {
			x = strings.ReplaceAll(x, ph, pv)
		}
		return x
	case map[string]any:
		for k, vv := range x {
			x[k] = replaceJobParams(vv, repl)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = replaceJobParams(vv, repl)
		}
		return x
	default:
		return v
	}
}

// renderAIJobPrompt 渲染 ai target 的 prompt：任务数据（≤50 条）+ 指令 + 输出格式 + RAG。
func (s *Server) renderAIJobPrompt(ctx context.Context, job db.AIJobWithOwner, t aiJobTarget) (string, error) {
	var c aiJobContext
	if len(job.Context) > 0 {
		_ = json.Unmarshal(job.Context, &c)
	}
	includeTasks := c.Tasks == nil || *c.Tasks
	ragTopK := 3
	if c.RagTopK != nil {
		ragTopK = *c.RagTopK
	}

	var b strings.Builder
	if includeTasks {
		tasks, err := s.db.GetTasks(ctx, job.OrgID, job.UserID)
		if err != nil {
			return "", fmt.Errorf("get tasks failed: %w", err)
		}
		b.WriteString("你是 Workbench 个人开发平台的 AI 助手，正在执行用户安排的定时任务。\n\n")
		b.WriteString(fmt.Sprintf("## 定时任务：%s\n\n## 我的当前任务（%d 条）\n", job.Name, len(tasks)))
		for i, task := range tasks {
			if i >= 50 {
				b.WriteString(fmt.Sprintf("…（其余 %d 条省略）\n", len(tasks)-50))
				break
			}
			due := task.Due
			if task.DueTime != "" {
				due += " " + task.DueTime
			}
			b.WriteString(fmt.Sprintf("- [%s] %s（优先级 %s，截止 %s，进度 %d%%）\n",
				task.Status, task.Title, task.Priority, due, task.Progress))
		}
		b.WriteString("\n## 任务指令\n")
	}
	b.WriteString(t.Prompt)
	b.WriteString("\n\n输出格式要求（结果将作为 HTML 邮件正文发送）：\n" +
		"1. 使用 Markdown：用「## 小节标题」分节，用「- 」列表列举条目，用「**粗体**」强调关键信息；\n" +
		"2. 不要使用中文序号（一、二、）或「·」作列表符号；\n" +
		"3. 直接输出结果内容，不要输出多余的解释或寒暄。")

	prompt := b.String()
	if ragTopK > 0 && s.rag != nil {
		if hits, err := s.rag.Search(ctx, job.OrgID, job.UserID, t.Prompt, ragTopK); err == nil && len(hits) > 0 {
			prompt = rag.ReferencePrompt(hits) + "\n\n" + prompt
		}
	}
	return prompt, nil
}

// validateAIJobDefinition 调度专用校验：结构校验 + 黑名单 + allowWrite 预检。
func (s *Server) validateAIJobDefinition(def pipeline.Definition) error {
	if err := def.Validate(); err != nil {
		return err
	}
	for _, step := range def.Steps {
		kind, name, ok := pipeline.ParseUses(step.Uses)
		if !ok {
			return fmt.Errorf("invalid uses %q", step.Uses)
		}
		uses := kind
		if name != "" {
			uses = kind + "/" + name
		}
		if scheduledJobBlacklist[uses] {
			return fmt.Errorf("%s is not allowed in scheduled jobs", uses)
		}
		switch kind {
		case "tool":
			if err := s.checkToolAllowWrite(name, def); err != nil {
				return err
			}
		case "api":
			if strings.HasPrefix(name, "pipeline/") {
				return fmt.Errorf("api/%s is not allowed in scheduled jobs", name)
			}
			method := http.MethodGet
			if m, ok := step.With["method"].(string); ok && strings.TrimSpace(m) != "" {
				method = strings.ToUpper(strings.TrimSpace(m))
			}
			if strings.HasPrefix(name, "ai/jobs") && method != http.MethodGet {
				return fmt.Errorf("api/%s with %s is not allowed in scheduled jobs", name, method)
			}
			if _, _, err := resolveAPIRequest(name, step.With, def); err != nil {
				return err
			}
		case "llm":
		default:
			return fmt.Errorf("unsupported uses kind %q in scheduled jobs", kind)
		}
	}
	return nil
}

// checkToolAllowWrite 写类工具必须在 allowWrite 中；创建时与执行期共用。
func (s *Server) checkToolAllowWrite(name string, def pipeline.Definition) error {
	tool := s.aiToolByName(name)
	if tool == nil {
		return fmt.Errorf("unknown tool %q", name)
	}
	if tool.NeedApproval && !containsString(def.AllowWrite, name) {
		return fmt.Errorf("write tool %q must be listed in allowWrite", name)
	}
	return nil
}

// resolveAPIRequest 校验站内路径并解析 method；非 GET 必须在 allowWrite 中。
// 返回的 path 带 /api/ 前缀，name 保持 uses 里的相对路径（不含 /api/）。
func resolveAPIRequest(name string, args map[string]any, def pipeline.Definition) (path, method string, err error) {
	path = "/api/" + strings.TrimPrefix(name, "/")
	if strings.Contains(path, "..") || strings.Contains(path, "://") || strings.ContainsAny(path, " \t\r\n") {
		return "", "", fmt.Errorf("invalid api path %q", path)
	}
	method = http.MethodGet
	if m, ok := args["method"].(string); ok && strings.TrimSpace(m) != "" {
		method = strings.ToUpper(strings.TrimSpace(m))
	}
	if method != http.MethodGet && !containsString(def.AllowWrite, "api/"+name) {
		return "", "", fmt.Errorf("api step %q with %s must be listed in allowWrite", path, method)
	}
	return path, method, nil
}
