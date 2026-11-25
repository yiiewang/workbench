package server

// 定时任务执行装配：target 归一化 → 调度校验 → runPipeline → 结果摘要。
// schedule 包只负责扫描/判重/超时/通知，具体执行在这里完成。

import (
	"context"
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
}

// execAIJob 单次定时任务的执行入口。
func (s *Server) execAIJob(ctx context.Context, job db.AIJobWithOwner) (aiJobRunResult, error) {
	start := time.Now()
	def, err := s.resolveAIJobTarget(ctx, job)
	if err != nil {
		return aiJobRunResult{DurationMs: time.Since(start).Milliseconds()}, err
	}
	if err := s.validateAIJobDefinition(def); err != nil {
		return aiJobRunResult{DurationMs: time.Since(start).Milliseconds()}, err
	}
	// 审计：定时执行无人审批，静态授权与最终步骤在此留痕。
	slog.Info("scheduled job authorized",
		"job", job.ID, "name", job.Name, "user", job.UserID, "target", job.TargetType,
		"steps", stepUses(def.Steps), "allowWrite", def.AllowWrite)
	runID := uuid.NewString()
	results, runErr := s.runPipeline(ctx, job.OrgID, job.UserID, runID, def)
	return aiJobRunResult{
		Output:        summarizeAIJobResults(results, runErr),
		PipelineRunID: runID,
		Steps:         results,
		DurationMs:    time.Since(start).Milliseconds(),
	}, runErr
}

// stepUses 审计日志里的步骤摘要。
func stepUses(steps []pipeline.Step) []string {
	out := make([]string, 0, len(steps))
	for _, st := range steps {
		out = append(out, st.Uses)
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
