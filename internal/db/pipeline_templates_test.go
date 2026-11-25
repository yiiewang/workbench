package db

// pipeline_templates CRUD：模板只存纯 Definition，调度列已由 000020 删除。

import "testing"

func TestPipelineTemplateCRUD(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	def := `{"name":"索引巡检","steps":[{"id":"a","uses":"tool/reindex_status"}]}`

	// 000020 之后模板表不再有调度列
	for _, col := range []string{"cron_expr", "enabled", "last_run_at", "last_status"} {
		assertColumnNotExists(t, d.conn, "pipeline_templates", col)
	}

	id, err := d.CreatePipelineTemplate(ctx, 1, 2, "索引巡检", def)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.GetPipelineTemplate(ctx, 1, 2, id)
	if err != nil || !ok {
		t.Fatalf("get template = (%+v, %v, %v)", got, ok, err)
	}
	if got.Definition != def || got.Name != "索引巡检" {
		t.Fatalf("unexpected template: %+v", got)
	}
	if _, ok, err := d.GetPipelineTemplate(ctx, 9, 9, id); err != nil || ok {
		t.Fatalf("cross-user read = (%v, %v), want not found", ok, err)
	}

	list, err := d.ListPipelineTemplates(ctx, 1, 2, 10)
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Definition != "" {
		t.Fatalf("list = (%+v, %v), want single template without definition", list, err)
	}

	updated := `{"name":"改名","steps":[{"id":"a","uses":"llm"}]}`
	if err := d.UpdatePipelineTemplate(ctx, 1, 2, id, "改名", updated); err != nil {
		t.Fatal(err)
	}
	got, _, _ = d.GetPipelineTemplate(ctx, 1, 2, id)
	if got.Name != "改名" || got.Definition != updated {
		t.Fatalf("unexpected updated template: %+v", got)
	}

	if err := d.DeletePipelineTemplate(ctx, 1, 2, id); err != nil {
		t.Fatal(err)
	}
	list, _ = d.ListPipelineTemplates(ctx, 1, 2, 10)
	if len(list) != 0 {
		t.Fatal("template not deleted")
	}
}

// 000020 的 down：加回 4 个调度列并从 schedule_triggers 反向回填，回滚不丢配置。
func TestMigration020DownRestoresCronColumns(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()

	id, err := d.CreatePipelineTemplate(ctx, 1, 2, "日报", `{"steps":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	trigID, err := d.CreateCronTrigger(ctx, 1, 2, "pipeline_template", id, "0 9 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.MarkCronTriggerRun(ctx, trigID, "ok", "2026-11-24 09:00"); err != nil {
		t.Fatal(err)
	}

	if _, err := d.conn.ExecContext(ctx, readMigration("000020_pipeline_templates_pure.down.sql")); err != nil {
		t.Fatal(err)
	}
	assertColumnExists(t, d.conn, "pipeline_templates", "cron_expr")
	var cron, lastAt, lastStatus string
	var enabled int
	if err := d.conn.QueryRow(`SELECT cron_expr, enabled, last_run_at, last_status FROM pipeline_templates WHERE id = ?`, id).
		Scan(&cron, &enabled, &lastAt, &lastStatus); err != nil {
		t.Fatal(err)
	}
	if cron != "0 9 * * *" || enabled != 1 || lastAt != "2026-11-24 09:00" || lastStatus != "ok" {
		t.Fatalf("down backfill mismatch: cron=%q enabled=%d last=%q status=%q", cron, enabled, lastAt, lastStatus)
	}
}
