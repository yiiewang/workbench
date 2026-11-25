package schedule

// 通用定时触发：schedule 只认「target_type + target_id + cron」，
// 具体目标由 server 装配层注册 handler 后执行；本包不 import 任何业务对象类型。
// 与 AIJobsRunnerJob 一样复用 Job/Scheduler 的每分钟扫描内核。

import (
	"context"
	"log/slog"
	"time"

	"github.com/yiiewang/workbench/internal/db"
)

// TriggerHandler 执行一次触发。t 为通用规则行（含归属与目标标识），
// handler 由 server 装配层实现，负责解释 target_id 属于哪个领域对象。
type TriggerHandler func(ctx context.Context, t db.CronTriggerWithOwner) error

// cronTriggerTimeout 单次触发执行超时（多步 LLM 可能较久）
const cronTriggerTimeout = 15 * time.Minute

// NewTriggersJob 每分钟扫描 schedule_triggers 的通用调度入口。
// handlers 按 target_type 分发；未注册的类型只记错误状态，不影响其他规则。
func NewTriggersJob(database *db.DB, handlers map[string]TriggerHandler, loc *time.Location) (*Job, error) {
	spec, err := ParseCron("* * * * *")
	if err != nil {
		return nil, err
	}
	return &Job{
		Name: "cron_triggers_runner",
		Spec: spec,
		Run: func(ctx context.Context, fireAt time.Time, bus *EventBus) {
			runCronTriggers(ctx, fireAt, database, handlers, loc)
		},
	}, nil
}

func runCronTriggers(ctx context.Context, fireAt time.Time, database *db.DB, handlers map[string]TriggerHandler, loc *time.Location) {
	triggers, err := database.ListEnabledCronTriggers(ctx)
	if err != nil {
		slog.Error("cron triggers scan failed", "err", err)
		return
	}
	if len(triggers) == 0 {
		return
	}
	local := fireAt.In(loc)
	nowKey := local.Format("2006-01-02 15:04")
	for _, t := range triggers {
		spec, err := ParseCron(t.CronExpr)
		if err != nil {
			_ = database.MarkCronTriggerRun(ctx, t.ID, "error: invalid cron "+t.CronExpr, nowKey)
			continue
		}
		if !spec.Matches(local) || t.LastRunAt == nowKey {
			continue
		}
		handler := handlers[t.TargetType]
		if handler == nil {
			_ = database.MarkCronTriggerRun(ctx, t.ID, "error: unknown target type "+t.TargetType, nowKey)
			continue
		}
		// CAS 占用：同分钟重复 tick 不重跑；跨分钟的长任务由租约拦住
		leaseUntil := local.Add(cronTriggerTimeout).Format("2006-01-02 15:04")
		claimed, err := database.ClaimCronTrigger(ctx, t.ID, nowKey, leaseUntil)
		if err != nil {
			slog.Error("cron trigger claim failed", "trigger", t.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		item, fn := t, handler
		go func() {
			runCtx, cancel := context.WithTimeout(context.Background(), cronTriggerTimeout)
			defer cancel()
			status := "ok"
			if err := fn(runCtx, item); err != nil {
				status = "error: " + err.Error()
				slog.Error("cron trigger failed", "trigger", item.ID, "target", item.TargetType, "err", err)
			}
			// runCtx 在本 goroutine 结束后即 cancel，状态回写用独立 ctx
			_ = database.MarkCronTriggerRun(context.Background(), item.ID, status, nowKey)
			slog.Info("cron trigger done", "trigger", item.ID, "status", status)
		}()
	}
}
