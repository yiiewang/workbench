package schedule

// AI 定时任务执行引擎：用户自建"到点让 AI 执行 prompt"的任务（ai_jobs 表）。
//
// 每分钟 tick 读取启用的 ai_jobs → cron 表达式匹配当前分钟 →
// 以任务归属用户身份执行 prompt（附带其任务数据）→ 结果经 user.notify
// 事件邮件投递 → 记录 last_run_at/last_status。
//
// 动态性：逐分钟读 DB——用户新建/修改/启停任务下一分钟即生效，无需重载调度器。
// 幂等：last_run_at 精确到分钟（YYYY-MM-DD HH:mm），同分钟内重复 tick 跳过；
// 进程重启后同一分钟内不会重跑。
// 隔离：每个命中任务独立 goroutine 执行，单任务慢（LLM 调用）不阻塞其他任务与 tick。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/db"
)

// LLMCaller 非流式 LLM 调用（由 server 装配注入，复用上游 AI 配置；
// schedule 包不依赖 server，保持单向依赖）
type LLMCaller func(ctx context.Context, prompt string) (string, error)

// aiJobTimeout 单个 AI 任务执行超时（LLM 生成 + 数据组装）
const aiJobTimeout = 2 * time.Minute

// NewAIJobsRunnerJob AI 定时任务调度入口（每分钟 tick）
func NewAIJobsRunnerJob(database *db.DB, llm LLMCaller, loc *time.Location) (*Job, error) {
	spec, err := ParseCron("* * * * *")
	if err != nil {
		return nil, err
	}
	return &Job{
		Name: "ai_jobs_runner",
		Spec: spec,
		Run: func(ctx context.Context, fireAt time.Time, bus *EventBus) {
			runAIJobs(ctx, fireAt, database, llm, loc, bus)
		},
	}, nil
}

// runAIJobs 单次扫描：cron 命中且本分钟未跑过的任务逐个派发。
// 全局公共任务（UserID=0）展开为每个有邮箱的用户逐个执行（各自任务数据/投递）。
func runAIJobs(ctx context.Context, fireAt time.Time, database *db.DB, llm LLMCaller, loc *time.Location, bus *EventBus) {
	jobs, err := database.ListEnabledAIJobs(ctx)
	if err != nil {
		slog.Error("ai jobs scan failed", "err", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	local := fireAt.In(loc)
	nowKey := local.Format("2006-01-02 15:04")
	for _, j := range jobs {
		job := j
		spec, err := ParseCron(job.CronExpr)
		if err != nil {
			// 创建时已校验，此处容错：禁用并记录
			_ = database.MarkAIJobRun(ctx, job.ID, "error: invalid cron "+job.CronExpr, "")
			continue
		}
		if !spec.Matches(local) || job.LastRunAt == nowKey {
			continue
		}
		if job.UserID == 0 {
			// 全局公共任务：展开为每个有邮箱的用户，last_run 记录一次
			targets, err := database.ListUsersWithEmail(ctx)
			if err != nil {
				slog.Error("ai job expand users failed", "job", job.Name, "err", err)
				continue
			}
			for _, t := range targets {
				perUser := job
				perUser.UserID = t.UserID
				perUser.OrgID = t.OrgID
				go func(j db.AIJobWithOwner) {
					runCtx, cancel := context.WithTimeout(context.Background(), aiJobTimeout)
					defer cancel()
					runAIJob(runCtx, j, database, llm, bus)
				}(perUser)
			}
			slog.Info("ai global job dispatched", "job", job.Name, "users", len(targets))
			continue
		}
		go func() {
			runCtx, cancel := context.WithTimeout(context.Background(), aiJobTimeout)
			defer cancel()
			runAIJob(runCtx, job, database, llm, bus)
		}()
	}
}

// runAIJob 执行单个 AI 定时任务：组 prompt（用户任务数据 + 指令）→ LLM → 通知
func runAIJob(ctx context.Context, job db.AIJobWithOwner, database *db.DB, llm LLMCaller, bus *EventBus) {
	start := time.Now()
	tasks, err := database.GetTasks(ctx, job.OrgID, job.UserID)
	if err != nil {
		_ = database.MarkAIJobRun(ctx, job.ID, "error: get tasks failed", "")
		slog.Error("ai job get tasks failed", "job", job.Name, "err", err)
		return
	}

	output, err := llm(ctx, buildAIJobPrompt(job, tasks))
	if err != nil {
		_ = database.MarkAIJobRun(ctx, job.ID, "error: "+err.Error(), "")
		slog.Error("ai job llm failed", "job", job.Name, "err", err)
		// 失败也通知用户（可感知任务没跑成）。Publish 用独立长生命周期 ctx：
		// runCtx 在本函数 return 后即被 cancel，异步 handler 若沿用会
		// context canceled（曾导致任务成功但邮件查询失败）。
		bus.Publish(context.Background(), Event{
			Topic: TopicUserNotify,
			Payload: UserNotifyEvent{
				UserID:  job.UserID,
				OrgID:   job.OrgID,
				Subject: "[定时任务失败] " + job.Name,
				Body:    "任务「" + job.Name + "」执行失败：" + err.Error() + "\n\n请检查 AI 配置（ai.base_url / AI_API_KEY）后重试。",
			},
		})
		return
	}

	_ = database.MarkAIJobRun(ctx, job.ID, "ok", output)
	// 同上：发布事件的 ctx 不能绑定 runCtx（handler 异步执行时会被取消）
	bus.Publish(context.Background(), Event{
		Topic: TopicUserNotify,
		Payload: UserNotifyEvent{
			UserID:  job.UserID,
			OrgID:   job.OrgID,
			Subject: "[定时任务] " + job.Name,
			Body:    output,
		},
	})
	slog.Info("ai job done", "job", job.Name, "user", job.UserID, "elapsed", time.Since(start).Round(time.Millisecond).String())
}

// buildAIJobPrompt 组装执行提示：平台角色 + 用户任务数据（精简）+ 用户指令
func buildAIJobPrompt(job db.AIJobWithOwner, tasks []db.TaskItem) string {
	var b strings.Builder
	b.WriteString("你是 Workbench 个人开发平台的 AI 助手，正在执行用户安排的定时任务。\n\n")
	b.WriteString(fmt.Sprintf("## 定时任务：%s\n\n## 我的当前任务（%d 条）\n", job.Name, len(tasks)))
	for i, t := range tasks {
		if i >= 50 {
			b.WriteString(fmt.Sprintf("…（其余 %d 条省略）\n", len(tasks)-50))
			break
		}
		due := t.Due
		if t.DueTime != "" {
			due += " " + t.DueTime
		}
		b.WriteString(fmt.Sprintf("- [%s] %s（优先级 %s，截止 %s，进度 %d%%）\n", t.Status, t.Title, t.Priority, due, t.Progress))
	}
	b.WriteString("\n## 任务指令\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n\n请直接输出结果内容（将作为邮件正文发送给用户），不要输出多余的解释或寒暄。")
	return b.String()
}
