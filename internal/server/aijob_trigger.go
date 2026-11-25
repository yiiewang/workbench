package server

// 定时任务触发入口：注册为 schedule.TriggerHandler（target_type="job"）。
// 规则只提供 target_id；任务定义读取、全局展开、执行、留痕与通知都在这里。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/schedule"
)

// triggerJob 通用调度器回调：读任务 → 执行 → 写历史 → 发通知。
func (s *Server) triggerJob(ctx context.Context, t db.CronTriggerWithOwner) error {
	job, err := s.db.GetAIJobAny(ctx, t.TargetID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("job %d not found", t.TargetID)
	}
	if !job.IsGlobal {
		return s.runAIJobOnce(ctx, *job)
	}
	// 全局公共任务：按有邮箱的用户逐个以本人身份执行，各自留痕与通知。
	targets, err := s.db.ListUsersWithEmail(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, u := range targets {
		perUser := *job
		perUser.UserID = u.UserID
		perUser.OrgID = u.OrgID
		if err := s.runAIJobOnce(ctx, perUser); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	slog.Info("global job dispatched", "job", job.ID, "users", len(targets))
	return firstErr
}

// runAIJobOnce 执行一次任务并投递结果邮件；返回执行错误供规则状态回写。
func (s *Server) runAIJobOnce(ctx context.Context, job db.AIJobWithOwner) error {
	res, err := s.execAIJob(ctx, job)
	status := "ok"
	errMsg := ""
	if err != nil {
		status = "error: " + err.Error()
		errMsg = err.Error()
	}
	stepsJSON, _ := json.Marshal(res.Steps)
	if merr := s.db.MarkAIJobRunDetail(ctx, job.ID, db.AIJobRun{
		Status:        status,
		Output:        res.Output,
		TargetType:    job.TargetType,
		PipelineRunID: res.PipelineRunID,
		StepsSummary:  string(stepsJSON),
		ErrMsg:        errMsg,
		DurationMs:    res.DurationMs,
		FinishedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}); merr != nil {
		slog.Warn("mark ai job run failed", "job", job.ID, "err", merr)
	}

	subject, body := s.renderAIJobNotify(job, res, err)
	if s.bus != nil {
		// 发布事件用独立 ctx：runCtx 结束后异步订阅者仍要执行。
		s.bus.Publish(context.Background(), schedule.Event{
			Topic: schedule.TopicUserNotify,
			Payload: schedule.UserNotifyEvent{
				UserID: job.UserID, OrgID: job.OrgID, Subject: subject, Body: body,
			},
		})
	}
	return err
}

// renderAIJobNotify 按 target 类型组织邮件主题与正文。
func (s *Server) renderAIJobNotify(job db.AIJobWithOwner, res aiJobRunResult, runErr error) (string, string) {
	subject, body := "[定时任务] "+job.Name, res.Output
	if runErr != nil {
		subject = "[定时任务失败] " + job.Name
		if strings.TrimSpace(body) == "" {
			body = "任务「" + job.Name + "」执行失败：" + runErr.Error() +
				"\n\n请检查任务配置或 AI 配置（ai.base_url / AI_API_KEY）后重试。"
		}
		return subject, body
	}
	switch job.TargetType {
	case aiJobTypeTool, aiJobTypeInterface:
		if strings.TrimSpace(body) != "" {
			body = "```\n" + body + "\n```"
		}
	case aiJobTypePipeline:
		if res.PipelineRunID != "" && strings.TrimSpace(s.cfg.SiteURL) != "" {
			body += "\n\n详细结果：" + strings.TrimRight(s.cfg.SiteURL, "/") + "/pipelines?run=" + res.PipelineRunID
		}
	}
	return subject, body
}

// upsertAIJobTrigger 写入/更新任务的调度规则（target_type="job"）。
func (s *Server) upsertAIJobTrigger(ctx context.Context, orgID, userID, jobID int64, cronExpr string, enabled bool) error {
	triggers, err := s.db.ListCronTriggers(ctx, orgID, userID, "job", jobID, 1)
	if err != nil {
		return err
	}
	if len(triggers) > 0 {
		return s.db.UpdateCronTrigger(ctx, orgID, userID, triggers[0].ID, cronExpr, enabled)
	}
	_, err = s.db.CreateCronTrigger(ctx, orgID, userID, "job", jobID, cronExpr, enabled)
	return err
}

// deleteAIJobTriggers 删除任务的调度规则，避免留下孤儿规则。
func (s *Server) deleteAIJobTriggers(ctx context.Context, orgID, userID, jobID int64) error {
	return s.db.DeleteCronTriggersForTarget(ctx, orgID, userID, "job", jobID)
}
