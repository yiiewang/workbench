package db

// schedule_triggers CRUD、目标过滤、org+user 隔离，以及 000019 回填语义。

import "testing"

func TestCronTriggerCRUD(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	// 000023 会从 seed 的全局任务回填 job 规则；本用例只验证 CRUD，先清空隔离。
	if _, err := d.conn.ExecContext(ctx, `DELETE FROM schedule_triggers`); err != nil {
		t.Fatal(err)
	}

	id, err := d.CreateCronTrigger(ctx, 1, 2, "pipeline_template", 7, "0 9 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.GetCronTrigger(ctx, 1, 2, id)
	if err != nil || !ok {
		t.Fatalf("get trigger = (%+v, %v, %v)", got, ok, err)
	}
	if got.TargetType != "pipeline_template" || got.TargetID != 7 || got.CronExpr != "0 9 * * *" || !got.Enabled {
		t.Fatalf("unexpected trigger: %+v", got)
	}
	if _, ok, err := d.GetCronTrigger(ctx, 9, 9, id); err != nil || ok {
		t.Fatalf("cross-user read = (%v, %v), want not found", ok, err)
	}

	// 可选过滤：按目标类型 / 目标 id
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "pipeline_template", 8, "0 18 * * *", false)
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "ai_job", 9, "0 8 * * *", true)
	all, err := d.ListCronTriggers(ctx, 1, 2, "", 0, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("list all = (%+v, %v)", all, err)
	}
	byTarget, err := d.ListCronTriggers(ctx, 1, 2, "pipeline_template", 7, 10)
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
		t.Fatalf("enabled scan = (%+v, %v), want only ai_job rule", enabled, err)
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
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "pipeline_template", 7, "0 9 * * *", true)
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "pipeline_template", 7, "0 18 * * *", true)
	_, _ = d.CreateCronTrigger(ctx, 1, 2, "pipeline_template", 8, "0 9 * * *", true)
	_, _ = d.CreateCronTrigger(ctx, 9, 9, "pipeline_template", 7, "0 9 * * *", true)

	if err := d.DeleteCronTriggersForTarget(ctx, 1, 2, "pipeline_template", 7); err != nil {
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

// 000019 的回填语句：只搬运已配置 cron 的模板，并带走启用/最近执行状态。
// 000020 之后模板表已无调度列，这里用 ALTER 还原「000018 时代」的列，
// 再执行与迁移中完全相同的 INSERT ... SELECT 验证列映射与过滤条件。
func TestScheduleTriggersBackfill(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	// 000023 会从 seed 的全局任务回填 job 规则；本用例只验证模板回填，先清空隔离。
	if _, err := d.conn.ExecContext(ctx, `DELETE FROM schedule_triggers`); err != nil {
		t.Fatal(err)
	}
	// 000023 会从 seed 的全局任务回填 job 规则；本用例只验证模板回填，先清空隔离。
	if _, err := d.conn.ExecContext(ctx, `DELETE FROM schedule_triggers`); err != nil {
		t.Fatal(err)
	}

	for _, ddl := range []string{
		`ALTER TABLE pipeline_templates ADD COLUMN cron_expr TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pipeline_templates ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE pipeline_templates ADD COLUMN last_run_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pipeline_templates ADD COLUMN last_status TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := d.conn.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO pipeline_templates (org_id, user_id, name, definition, cron_expr, enabled, last_run_at, last_status) VALUES
		 (1, 2, '日报', '{"steps":[]}', '0 9 * * *', 1, '2026-11-24 09:00', 'ok'),
		 (1, 2, '周报', '{"steps":[]}', '0 9 * * 1', 0, '', ''),
		 (1, 2, '手动', '{"steps":[]}', '', 1, '', '')`)
	if err != nil {
		t.Fatal(err)
	}
	var withCron, disabled int64
	if err := d.conn.QueryRow(`SELECT id FROM pipeline_templates WHERE name = '日报'`).Scan(&withCron); err != nil {
		t.Fatal(err)
	}
	if err := d.conn.QueryRow(`SELECT id FROM pipeline_templates WHERE name = '周报'`).Scan(&disabled); err != nil {
		t.Fatal(err)
	}

	_, err = d.conn.ExecContext(ctx,
		`INSERT INTO schedule_triggers (org_id, user_id, target_type, target_id, cron_expr, enabled, last_run_at, last_status)
		 SELECT org_id, user_id, 'pipeline_template', id, cron_expr, enabled, last_run_at, last_status
		 FROM pipeline_templates WHERE cron_expr <> ''`)
	if err != nil {
		t.Fatal(err)
	}

	all, err := d.ListCronTriggers(ctx, 1, 2, "", 0, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("backfill = (%+v, %v), want 2 rules (manual template excluded)", all, err)
	}
	enabled, err := d.ListEnabledCronTriggers(ctx)
	if err != nil || len(enabled) != 1 {
		t.Fatalf("enabled scan = (%+v, %v), want only the enabled rule", enabled, err)
	}
	e := enabled[0]
	if e.TargetType != "pipeline_template" || e.TargetID != withCron || e.CronExpr != "0 9 * * *" ||
		e.LastRunAt != "2026-11-24 09:00" || e.OrgID != 1 || e.UserID != 2 {
		t.Fatalf("backfilled rule mismatch: %+v", e)
	}
	// 停用的模板也应被搬运（保留 enabled=0，用户可在新 UI 里重新启用）
	var foundDisabled bool
	for _, r := range all {
		if r.TargetID == disabled {
			foundDisabled = true
			if r.Enabled {
				t.Fatal("disabled template must be backfilled as disabled")
			}
		}
	}
	if !foundDisabled {
		t.Fatal("disabled template with cron was not backfilled")
	}
}
