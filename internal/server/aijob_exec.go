package server

// 定时任务执行装配：target 归一化 → 调度校验 → runPipeline → 结果摘要。
// schedule 包只负责扫描/判重/超时/通知，具体执行在这里完成。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
)

// aiJobRunResult 单次执行的结构化结果，供历史落库与邮件渲染。
type aiJobRunResult struct {
	Output        string
	PipelineRunID string
	Steps         []pipeline.StepResult
	DurationMs    int64
	// Retryable 无写步骤才允许 job 级重试（重跑写操作可能重复副作用）。
	Retryable bool
}

// execAIJob 单次定时任务的执行入口。
// 执行前先落一条 running 的 job_runs（带 pipeline_run_id），结束更新为终态：
// 任务详情页在运行中就能看到这条记录并拉取 pipeline_runs 的逐步快照。
func (s *Server) execAIJob(ctx context.Context, job db.AIJobWithOwner) (res aiJobRunResult, err error) {
	start := time.Now()
	runID := uuid.NewString()
	var runRowID int64
	if s.db != nil {
		runRowID, _ = s.db.InsertAIJobRun(ctx, job.ID, db.AIJobRun{
			Status: "running", TargetType: job.TargetType, PipelineRunID: runID,
		})
	}
	defer func() {
		res.PipelineRunID = runID
		if runRowID == 0 {
			return
		}
		status, errMsg := "ok", ""
		if err != nil {
			status, errMsg = "error: "+err.Error(), err.Error()
		}
		stepsJSON, _ := json.Marshal(res.Steps)
		_ = s.db.UpdateAIJobRun(context.Background(), runRowID, db.AIJobRun{
			Status: status, Output: res.Output, TargetType: job.TargetType,
			PipelineRunID: runID, StepsSummary: string(stepsJSON), ErrMsg: errMsg,
			DurationMs: res.DurationMs, FinishedAt: time.Now().Format("2006-01-02 15:04:05"),
		})
	}()

	def, err := s.resolveAIJobTarget(ctx, job)
	if err != nil {
		res.DurationMs = time.Since(start).Milliseconds()
		return res, err
	}
	if err := s.validateAIJobDefinition(def); err != nil {
		res.DurationMs = time.Since(start).Milliseconds()
		return res, err
	}
	// 审计：定时执行无人审批，静态授权与最终步骤在此留痕。
	slog.Info("scheduled job authorized",
		"job", job.ID, "name", job.Name, "user", job.UserID, "target", job.TargetType,
		"steps", stepUses(def.Steps), "allowWrite", def.AllowWrite, "mcpSteps", mcpStepUses(def.Steps))
	results, runErr := s.runPipeline(ctx, job.OrgID, job.UserID, runID, def)
	res = aiJobRunResult{
		Output:        summarizeAIJobResults(results, runErr),
		PipelineRunID: runID,
		Steps:         results,
		DurationMs:    time.Since(start).Milliseconds(),
		Retryable:     !s.defHasWrite(def),
	}
	return res, runErr
}

// defHasWrite 判断本次执行是否可能带写副作用：job 级预授权里的写类工具 /
// 非 GET 接口，以及 llm 步白名单里的写类工具。仅无写操作时允许 job 级重试。
func (s *Server) defHasWrite(def pipeline.Definition) bool {
	names := append([]string(nil), def.AllowWrite...)
	for _, step := range def.Steps {
		kind, _, ok := pipeline.ParseUses(step.Uses)
		if !ok || kind != "llm" {
			continue
		}
		tools, err := stepToolNames(step.With)
		if err == nil {
			names = append(names, tools...)
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "api/") {
			return true
		}
		if t := s.aiToolByName(name); t != nil && t.NeedApproval {
			return true
		}
	}
	return false
}

// stepUses 审计日志里的步骤摘要。
func stepUses(steps []pipeline.Step) []string {
	out := make([]string, 0, len(steps))
	for _, st := range steps {
		out = append(out, st.Uses)
	}
	return out
}

// mcpStepUses 审计日志里的外部 MCP 能力：tool/mcp_* 步骤 + llm 步 with.tools
// 白名单（MCP 通常挂在 llm 步的工具白名单里，只看 uses 会漏报）。
func mcpStepUses(steps []pipeline.Step) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(name string) {
		if strings.HasPrefix(name, "mcp_") && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, st := range steps {
		if kind, name, ok := pipeline.ParseUses(st.Uses); ok && kind == "tool" {
			add(name)
		}
		if st.Uses != "llm" {
			continue
		}
		switch tools := st.With["tools"].(type) {
		case []string:
			for _, t := range tools {
				add(t)
			}
		case []any:
			for _, t := range tools {
				if name, ok := t.(string); ok {
					add(name)
				}
			}
		}
	}
	return out
}

// summarizeAIJobResults 结果摘要：纯 ai 单步直接返回正文（保持邮件现状），
// 其余类型输出步骤状态列表；失败时附首个错误，供失败邮件使用。
func summarizeAIJobResults(results []pipeline.StepResult, runErr error) string {
	if len(results) == 1 && results[0].Status == "ok" && results[0].Uses == "llm" {
		if out, ok := results[0].Output.(string); ok && strings.TrimSpace(out) != "" {
			return out
		}
		return "模型未返回正文（可能只返回了思考内容）"
	}
	var b strings.Builder
	if runErr != nil {
		b.WriteString("执行失败：" + runErr.Error() + "\n\n")
	}
	if len(results) > 0 {
		b.WriteString("## 执行步骤\n")
		for _, r := range results {
			line := fmt.Sprintf("- %s（%s）：%s", r.ID, r.Uses, r.Status)
			if r.DurationMs > 0 {
				line += fmt.Sprintf("，耗时 %dms", r.DurationMs)
			}
			if r.Error != "" {
				line += "；" + r.Error
			}
			b.WriteString(line + "\n")
		}
	}
	if b.Len() == 0 {
		b.WriteString("任务已执行，无输出。")
	}
	return b.String()
}
