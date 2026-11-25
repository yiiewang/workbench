package schedule

// 通用触发器：Job 构造、按 target_type 分发、cron 命中、分钟键防重、
// 未知类型与非法 cron 的状态回写。schedule 包不认识任何具体业务目标。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yiiewang/workbench/internal/db"
)

func newTriggerTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "triggers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestNewTriggersJob(t *testing.T) {
	job, err := NewTriggersJob(nil, nil, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.Name != "cron_triggers_runner" || job.Spec == nil || job.Run == nil {
		t.Fatalf("unexpected job: %+v", job)
	}
	if !job.Spec.Matches(time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)) {
		t.Fatal("triggers runner must tick every minute")
	}
}

func TestRunCronTriggersDispatch(t *testing.T) {
	d := newTriggerTestDB(t)
	ctx := context.Background()
	fireAt := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	matchID, err := d.CreateCronTrigger(ctx, 1, 2, "report_job", 7, "0 9 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "report_job", 8, "0 18 * * *", true) // 时刻不命中
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "report_job", 11, "0 9 * * *", false) // 停用
	unknownID, _ := d.CreateCronTrigger(ctx, 1, 2, "mystery", 9, "0 9 * * *", true)
	badID, _ := d.CreateCronTrigger(ctx, 1, 2, "report_job", 10, "not a cron", true)

	called := make(chan db.CronTriggerWithOwner, 4)
	handlers := map[string]TriggerHandler{
		"report_job": func(_ context.Context, tr db.CronTriggerWithOwner) error {
			called <- tr
			return nil
		},
	}
	runCronTriggers(ctx, fireAt, d, handlers, time.UTC)

	select {
	case tr := <-called:
		if tr.ID != matchID || tr.TargetID != 7 || tr.OrgID != 1 || tr.UserID != 2 || tr.TargetType != "report_job" {
			t.Fatalf("handler got wrong trigger: %+v", tr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler not called for matching trigger")
	}
	select {
	case extra := <-called:
		t.Fatalf("only the matching enabled trigger should run, got extra: %+v", extra)
	case <-time.After(120 * time.Millisecond):
	}

	unknown, _, _ := d.GetCronTrigger(ctx, 1, 2, unknownID)
	if !strings.Contains(unknown.LastStatus, "unknown target type") {
		t.Fatalf("unknown target type status = %q", unknown.LastStatus)
	}
	bad, _, _ := d.GetCronTrigger(ctx, 1, 2, badID)
	if !strings.Contains(bad.LastStatus, "invalid cron") {
		t.Fatalf("invalid cron status = %q", bad.LastStatus)
	}

	// 同分钟再 tick：last_run_at 已占位，不应重复触发
	runCronTriggers(ctx, fireAt, d, handlers, time.UTC)
	select {
	case <-called:
		t.Fatal("same-minute tick should not re-run triggers")
	case <-time.After(120 * time.Millisecond):
	}
}

func TestRunCronTriggersHandlerError(t *testing.T) {
	d := newTriggerTestDB(t)
	ctx := context.Background()
	fireAt := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	id, err := d.CreateCronTrigger(ctx, 1, 2, "report_job", 7, "0 9 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	handlers := map[string]TriggerHandler{
		"report_job": func(_ context.Context, _ db.CronTriggerWithOwner) error {
			defer close(done)
			return errors.New("boom")
		},
	}
	runCronTriggers(ctx, fireAt, d, handlers, time.UTC)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not called")
	}
	// 等状态回写完成（goroutine 在 handler 返回后写）
	deadline := time.Now().Add(2 * time.Second)
	for {
		tr, _, _ := d.GetCronTrigger(ctx, 1, 2, id)
		if strings.Contains(tr.LastStatus, "error: boom") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("handler error not recorded, status = %q", tr.LastStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
