package schedule

// 内置定时任务：Todo 到期扫描。
//
// 触发：cron 表达式（默认每 5 分钟）
// 逻辑：扫所有用户 due <= 今天 且未完成的任务 → 逐条去重
// （同任务同一天只提醒一次：截止日当天 + 逾期每天各一次）→
// Publish("task.due", TaskDueEvent)。任务完成后不再命中扫描条件，
// 自然停止提醒；当天稍后完成也已收到一次（当天不会重发）。

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yiiewang/workbench/internal/db"
)

// TopicTaskDue 任务到期事件主题
const TopicTaskDue = "task.due"

// TopicUserNotify 用户自定义通知事件主题（AI notify_user 工具发布，
// EmailNotifier 等渠道订阅投递给用户本人）
const TopicUserNotify = "user.notify"

// UserNotifyEvent 用户自定义通知事件载荷
type UserNotifyEvent struct {
	UserID  int64
	OrgID   int64
	Subject string // 通知主题（邮件标题）
	Body    string // 通知正文（纯文本）
}

// TopicTaskReminder 任务精确时间提醒事件主题（每分钟扫描 scheduled_time/due_time）
const TopicTaskReminder = "task.reminder"

// TaskReminderEvent 精确时间提醒事件载荷
type TaskReminderEvent struct {
	UserID int64
	OrgID  int64
	Task   db.TaskItem
	// Kind 提醒类型：start（计划开始时刻到）| due（截止时刻到且未完成）
	Kind string
	// At 命中时刻 HH:mm
	At string
}

// TimeReminderQuerier 时间提醒 job 依赖的数据访问子集
type TimeReminderQuerier interface {
	ScanTimeReminders(ctx context.Context, today, hhmm string) ([]db.TimeReminderRow, error)
	MarkNotified(ctx context.Context, topic, dedupKey, notifyDate string) (bool, error)
}

// NewTodoTimeReminderJob 精确时间提醒任务：每分钟扫描，
// scheduled_time/due_time 命中当前时刻（且日期为今天）→ 发 task.reminder 事件。
// 去重键含时刻（taskID:kind:HH:mm）：同一分钟内重复扫描（重启/重跑）不重发。
func NewTodoTimeReminderJob(q TimeReminderQuerier, loc *time.Location) (*Job, error) {
	spec, err := ParseCron("* * * * *")
	if err != nil {
		return nil, err
	}
	return &Job{
		Name: "todo_time_reminder",
		Spec: spec,
		Run: func(ctx context.Context, fireAt time.Time, bus *EventBus) {
			runTimeReminderScan(ctx, fireAt, q, loc, bus)
		},
	}, nil
}

func runTimeReminderScan(ctx context.Context, fireAt time.Time, q TimeReminderQuerier, loc *time.Location, bus *EventBus) {
	local := fireAt.In(loc)
	today := local.Format("2006-01-02")
	hhmm := local.Format("15:04")
	rows, err := q.ScanTimeReminders(ctx, today, hhmm)
	if err != nil {
		slog.Error("time reminder scan failed", "err", err)
		return
	}
	for _, r := range rows {
		ok, err := q.MarkNotified(ctx, TopicTaskReminder,
			string(r.Task.ID)+":"+r.Kind+":"+hhmm, today)
		if err != nil {
			slog.Error("time reminder mark failed", "task", r.Task.ID, "err", err)
			continue
		}
		if !ok {
			continue
		}
		bus.Publish(ctx, Event{
			Topic: TopicTaskReminder,
			Payload: TaskReminderEvent{
				UserID: r.UserID,
				OrgID:  r.OrgID,
				Task:   r.Task,
				Kind:   r.Kind,
				At:     hhmm,
			},
		})
	}
	if len(rows) > 0 {
		slog.Info("time reminder scan published", "hits", len(rows), "at", hhmm)
	}
}

// KnownTopics 事件主题注册表：topic → 用途说明（list_events 工具展示给
// AI，让其知道总线能力）。新增 topic 时同步登记。
var KnownTopics = []struct {
	Topic string
	Desc  string
}{
	{TopicTaskDue, "任务到期提醒（TodoDueScan 定时扫描发布，含任务/截止日/逾期天数）"},
	{TopicTaskReminder, "任务精确时间提醒（每分钟扫描 scheduled_time/due_time，开始/截止时刻到点）"},
	{TopicUserNotify, "用户自定义通知（AI notify_user 工具发布，邮件投递给用户本人）"},
}

// TaskDueEvent 任务到期事件载荷：投递给订阅者（EmailNotifier 等）
type TaskDueEvent struct {
	UserID  int64
	OrgID   int64
	Task    db.TaskItem
	DueDate string // YYYY-MM-DD
	// OverdueDays 逾期天数：0 = 截止日当天，>0 = 已逾期 N 天
	OverdueDays int
}

// TaskQuerier job 依赖的数据访问子集（避免直接耦合 *db.DB，便于单测）
type TaskQuerier interface {
	// ScanDueTasks 返回所有用户 due <= today 且 status != done 的任务
	ScanDueTasks(ctx context.Context, today string) ([]db.DueTaskRow, error)
	// MarkNotified 同 (topic, taskID, date) 只标记一次；false = 今天已发过
	MarkNotified(ctx context.Context, topic, dedupKey, notifyDate string) (bool, error)
}

// NewTodoDueScanJob 构造到期扫描任务（默认 cron "*/5 * * * *"，可覆盖）
func NewTodoDueScanJob(q TaskQuerier, loc *time.Location, cronExpr string) (*Job, error) {
	if cronExpr == "" {
		cronExpr = "*/5 * * * *"
	}
	spec, err := ParseCron(cronExpr)
	if err != nil {
		return nil, fmt.Errorf("todo due scan cron: %w", err)
	}
	return &Job{
		Name: "todo_due_scan",
		Spec: spec,
		Run: func(ctx context.Context, fireAt time.Time, bus *EventBus) {
			runDueScan(ctx, fireAt, q, loc, bus)
		},
	}, nil
}

// runDueScan 单次扫描：查到期任务 → 去重 → 发事件
func runDueScan(ctx context.Context, fireAt time.Time, q TaskQuerier, loc *time.Location, bus *EventBus) {
	today := fireAt.In(loc).Format("2006-01-02")
	due, err := q.ScanDueTasks(ctx, today)
	if err != nil {
		slog.Error("todo due scan failed", "err", err)
		return
	}
	if len(due) == 0 {
		return
	}

	sent := 0
	for _, dt := range due {
		ok, err := q.MarkNotified(ctx, TopicTaskDue, string(dt.Task.ID), today)
		if err != nil {
			slog.Error("mark notified failed", "task", dt.Task.ID, "err", err)
			continue
		}
		if !ok {
			continue // 今天已提醒过
		}
		bus.Publish(ctx, Event{
			Topic: TopicTaskDue,
			Payload: TaskDueEvent{
				UserID:      dt.UserID,
				OrgID:       dt.OrgID,
				Task:        dt.Task,
				DueDate:     dt.Task.Due,
				OverdueDays: daysBetween(dt.Task.Due, today),
			},
		})
		sent++
	}
	if sent > 0 {
		slog.Info("todo due scan published", "due", len(due), "published", sent)
	}
}

// daysBetween 两个 YYYY-MM-DD 日期差（b-a），解析失败返回 0
func daysBetween(a, b string) int {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}
