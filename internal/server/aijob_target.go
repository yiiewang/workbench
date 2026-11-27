package server

// 定时任务执行对象（jobs.target_json）的解析与校验。
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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
	"github.com/yiiewang/workbench/internal/rag"
)

// aiJobTarget jobs.target_json 的结构。
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

// aiJobContext jobs.context_json 的结构；缺省 = 带任务数据 + RAG top3。
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
	"tool/run_job":       true,
	"tool/create_job": true,
	"tool/update_job": true,
	"tool/delete_job": true,
	"api/ai/chat":        true,
}

// aiJobOutputRule 定时任务 LLM 步的统一输出格式要求（markdown）：
// ai 类型的结果邮件经 AI 排版器转 HTML；pipeline 的 notify_user 走 mdToHTML 渲染，
// 两条链路都接受 markdown——要求模型输出 HTML 片段会被 mdToHTML 转义成源码。
const aiJobOutputRule = "输出格式要求（结果会作为邮件正文；模板已提供品牌标识、问候语与落款）：\n" +
	"1. 只输出 markdown 正文，不要输出 HTML 标签（如 <p>、<table>、<div>），也不要用代码块标记包裹；\n" +
	"2. 可用语法：#/##/### 分节标题、- 列表、**粗体**、`行内代码`、| 管道表格（第二行为 |---| 分隔行）、> 引用；不要使用其它格式；\n" +
	"3. 链接只允许站内 http(s) 同域地址，用 markdown 链接 [文字](url)；不要编造外链；不要使用图片、脚本、表单；\n" +
	"4. 从内容直接开始，不要重复任务名、不要写问候语、落款、日期标题或「以下是…」之类的开场白与解释；\n" +
	"5. 只写有依据的事实：数据来自任务清单、参考片段或工具返回结果；缺失的信息明确写「未取得」，不要猜测或编造数字、链接与行情；\n" +
	"6. 不要输出任何工具调用标记或调用意图（如 <tool_call>、<tool_calls>、<arg_key>、<arg_value>）；没有可用工具时直接基于已有信息作答。"

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
		with := map[string]any{"prompt": prompt}
		// AI 提示词类型的 allowWrite 即模型可用工具白名单：填写即提前审批通过，
		// 归一化后由 llm 步的 Agent 循环消费；为空时仍是单次 aiChatOnce。
		if len(job.AllowWrite) > 0 {
			with["tools"] = job.AllowWrite
		}
		// 新建/更新统一存 pipeline 时，把「任务数据 + RAG」的渲染配置固化到
		// llm 步上；执行时由 execPipelineLLM 重新渲染，避免把当天任务数据写死。
		if !renderPrompt {
			with["render"] = map[string]any{"name": job.Name, "context": string(job.Context)}
		}
		def = pipeline.Definition{
			Name:  job.Name,
			Steps: []pipeline.Step{{ID: "run", Uses: "llm", With: with}},
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
		injectOutputRule(&def)
	}
	// 定时执行的授权以任务创建时的静态清单为准；Definition 自带的 allowWrite 不参与。
	def.AllowWrite = job.AllowWrite
	return def, nil
}

// injectOutputRule 给 pipeline 的 llm 步补统一输出格式要求：
// 只处理已有 prompt 的步骤，空 prompt 留给 Validate 报错。
func injectOutputRule(def *pipeline.Definition) {
	for i := range def.Steps {
		if def.Steps[i].Uses != "llm" {
			continue
		}
		prompt, _ := def.Steps[i].With["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			continue
		}
		def.Steps[i].With["prompt"] = strings.TrimSpace(prompt) + "\n\n" + aiJobOutputRule
	}
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

// renderTaskContext 拼装定时任务的执行 prompt：任务数据 + 用户 prompt + 可选输出规则 + RAG。
// withOutputRule=false 用于 pipeline 内的 llm 步：输出规则已由 injectOutputRule 注入，避免重复。
func (s *Server) renderTaskContext(ctx context.Context, orgID, userID int64, name string, contextJSON json.RawMessage, prompt string, withOutputRule bool) (string, error) {
	var c aiJobContext
	if len(contextJSON) > 0 {
		_ = json.Unmarshal(contextJSON, &c)
	}
	includeTasks := c.Tasks == nil || *c.Tasks
	ragTopK := 3
	if c.RagTopK != nil {
		ragTopK = *c.RagTopK
	}

	var b strings.Builder
	if includeTasks {
		tasks, err := s.db.GetTasks(ctx, orgID, userID)
		if err != nil {
			return "", fmt.Errorf("get tasks failed: %w", err)
		}
		b.WriteString("你是 Workbench 个人开发平台的 AI 助手，正在执行用户安排的定时任务。\n\n")
		b.WriteString(fmt.Sprintf("## 定时任务：%s\n\n## 我的当前任务（%d 条）\n", name, len(tasks)))
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
	b.WriteString(prompt)
	if withOutputRule {
		b.WriteString("\n\n")
		b.WriteString(aiJobOutputRule)
	}

	out := b.String()
	if ragTopK > 0 && s.rag != nil {
		if hits, err := s.rag.Search(ctx, orgID, userID, prompt, ragTopK); err == nil && len(hits) > 0 {
			out = rag.ReferencePrompt(hits) + "\n\n" + out
		}
	}
	return out, nil
}

// renderAIJobPrompt 旧 ai target 的执行入口：兼容存量记录。
func (s *Server) renderAIJobPrompt(ctx context.Context, job db.AIJobWithOwner, t aiJobTarget) (string, error) {
	return s.renderTaskContext(ctx, job.OrgID, job.UserID, job.Name, job.Context, t.Prompt, true)
}

// normalizeTargetToPipeline 新建/更新时把非 pipeline target 归一化为内联 Definition：
// 读取路径仍兼容旧类型，存量任务不需要迁移。
func (s *Server) normalizeTargetToPipeline(ctx context.Context, job *db.AIJobWithOwner) error {
	if job.TargetType == aiJobTypePipeline {
		return nil
	}
	def, err := s.buildAIJobDefinition(ctx, *job, false)
	if err != nil {
		return err
	}
	def.AllowWrite = job.AllowWrite
	raw, err := json.Marshal(aiJobTarget{Type: aiJobTypePipeline, Definition: &def})
	if err != nil {
		return err
	}
	job.TargetType = aiJobTypePipeline
	job.Target = raw
	return nil
}

// normalizeLegacyTargets 启动时把存量 ai/tool/interface 任务统一转成 pipeline：
// 幂等，失败只记日志；旧类型读取分支保留作回退。
func (s *Server) normalizeLegacyTargets(ctx context.Context) {
	jobs, err := s.db.ListAllAIJobs(ctx)
	if err != nil {
		slog.Warn("normalize legacy target: list jobs failed", "err", err)
		return
	}
	for i := range jobs {
		if jobs[i].TargetType == aiJobTypePipeline {
			continue
		}
		job := jobs[i]
		if err := s.normalizeTargetToPipeline(ctx, &job); err != nil {
			slog.Warn("normalize legacy target failed", "job", job.ID, "err", err)
			continue
		}
		if err := s.db.UpdateAIJobTarget(ctx, job.ID, job.TargetType, string(job.Target)); err != nil {
			slog.Warn("normalize legacy target update failed", "job", job.ID, "err", err)
			continue
		}
		slog.Info("legacy target normalized to pipeline", "job", job.ID)
	}
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
			if strings.HasPrefix(name, "jobs") && method != http.MethodGet {
				return fmt.Errorf("api/%s with %s is not allowed in scheduled jobs", name, method)
			}
			if _, _, err := resolveAPIRequest(name, step.With, def); err != nil {
				return err
			}
		case "llm":
			names, err := stepToolNames(step.With)
			if err != nil {
				return err
			}
			stepDef := def
			stepDef.AllowWrite = mergeAllowWrite(def.AllowWrite, names)
			for _, name := range names {
				if strings.HasPrefix(name, "api/") {
					return fmt.Errorf("api entry %q is not a tool name for llm steps", name)
				}
				if scheduledJobBlacklist["tool/"+name] {
					return fmt.Errorf("tool/%s is not allowed in scheduled jobs", name)
				}
				if err := s.checkToolAllowWrite(name, stepDef); err != nil {
					return err
				}
			}
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

// stepToolNames 解析 llm 步 with.tools：归一化写入的是 []string，
// 前端内联 Definition 经 JSON 解码后是 []any，两种都接受。
func stepToolNames(with map[string]any) ([]string, error) {
	raw, ok := with["tools"]
	if !ok || raw == nil {
		return nil, nil
	}
	var names []string
	switch v := raw.(type) {
	case []string:
		names = append(names, v...)
	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("llm step tools must be tool names")
			}
			names = append(names, name)
		}
	default:
		return nil, fmt.Errorf("llm step tools must be an array of tool names")
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !containsString(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// mergeAllowWrite 把 llm 步白名单并入该步作用域的静态预授权：
// 清单里的写类工具视为已提前审批，不必再在 allowWrite 里重复填一遍。
func mergeAllowWrite(base, names []string) []string {
	out := append([]string(nil), base...)
	for _, name := range names {
		if !containsString(out, name) {
			out = append(out, name)
		}
	}
	return out
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
