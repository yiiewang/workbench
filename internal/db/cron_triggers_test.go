package db

// schedule_triggers CRUD、目标过滤、org+user 隔离。

import "testing"

func TestCronTriggerCRUD(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	// 000023 会从 seed 的全局任务回填 job 规则；本用例只验证 CRUD，先清空隔离。
	if _, err := d.conn.ExecContext(ctx, `DELETE FROM schedule_triggers`); err != nil {
		t.Fatal(err)
	}

	id, err := d.CreateCronTrigger(ctx, 1, 2, "job", 7, "0 9 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.GetCronTrigger(ctx, 1, 2, id)
	if err != nil || !ok {
		t.Fatalf("get trigger = (%+v, %v, %v)", got, ok, err)
	}
	if got.TargetType != "job" || got.TargetID != 7 || got.CronExpr != "0 9 * * *" || !got.Enabled {
		t.Fatalf("unexpected trigger: %+v", got)
	}
	if _, ok, err := d.GetCronTrigger(ctx, 9, 9, id); err != nil || ok {
		t.Fatalf("cross-user read = (%v, %v), want not found", ok, err)
	}

	// 可选过滤：按目标类型 / 目标 id
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "job", 8, "0 18 * * *", false)
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "reminder", 9, "0 8 * * *", true)
	all, err := d.ListCronTriggers(ctx, 1, 2, "", 0, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("list all = (%+v, %v)", all, err)
	}
	byTarget, err := d.ListCronTriggers(ctx, 1, 2, "job", 7, 10)
	if err != nil || len(byTarget) != 1 || byTarget[0].ID != id {
		t.Fatalf("filter by target = (%+v, %v)", byTarget, err)
	}

	if err := d.UpdateCronTrigger(ctx, 1, 2, id, "0 10 * * *", false); err != nil {
		t.Fatal(err)
	}
	if err := d.MarkCronTriggerRun(ctx, id, "ok", "2026-11-24 10:00"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = d.GetCronTrigger(ctx, 1, 2, id)
	if got.CronExpr != "0 10 * * *" || got.Enabled || got.LastRunAt != "2026-11-24 10:00" || got.LastStatus != "ok" {
		t.Fatalf("unexpected updated trigger: %+v", got)
	}

	enabled, err := d.ListEnabledCronTriggers(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].TargetID != 9 || enabled[0].UserID != 2 {
		t.Fatalf("enabled scan = (%+v, %v), want only the enabled reminder rule", enabled, err)
	}

	if err := d.DeleteCronTrigger(ctx, 1, 2, id); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.GetCronTrigger(ctx, 1, 2, id); err != nil || ok {
		t.Fatalf("deleted trigger still visible: (%v, %v)", ok, err)
	}
}

// 目标对象删除时按 (target_type, target_id) 批量清理，且不影响其他目标/其他用户
func TestDeleteCronTriggersForTarget(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "job", 7, "0 9 * * *", true)
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "job", 7, "0 18 * * *", true)
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "job", 8, "0 9 * * *", true)
	_, _ = d.CreateCronTrigger(ctx, 9, 9, "job", 7, "0 9 * * *", true)

	if err := d.DeleteCronTriggersForTarget(ctx, 1, 2, "job", 7); err != nil {
		t.Fatal(err)
	}
	remaining, err := d.ListCronTriggers(ctx, 1, 2, "", 0, 10)
	if err != nil || len(remaining) != 1 || remaining[0].TargetID != 8 {
		t.Fatalf("remaining = (%+v, %v), want only target 8", remaining, err)
	}
	otherUser, err := d.ListCronTriggers(ctx, 9, 9, "", 0, 10)
	if err != nil || len(otherUser) != 1 {
		t.Fatalf("other user's rules must be untouched: (%+v, %v)", otherUser, err)
	}
}

