package db

// pipeline_templates CRUD：模板只存纯 Definition，调度列已由 000020 删除。

import (
	"database/sql"
	"testing"
)

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

// 模板版本历史：每次保存/回滚写新版本，删除模板一并清理。
func TestPipelineTemplateVersions(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	v1 := `{"name":"v1","steps":[{"id":"a","uses":"llm"}]}`
	v2 := `{"name":"v2","steps":[{"id":"a","uses":"tool/list_tasks"}]}`

	id, err := d.CreatePipelineTemplate(ctx, 1, 2, "tpl", v1)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := d.ListPipelineTemplateVersions(ctx, 1, 2, id)
	if err != nil || len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("initial versions = (%+v, %v)", versions, err)
	}

	if err := d.UpdatePipelineTemplate(ctx, 1, 2, id, "tpl2", v2); err != nil {
		t.Fatal(err)
	}
	versions, err = d.ListPipelineTemplateVersions(ctx, 1, 2, id)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions after update = (%+v, %v)", versions, err)
	}
	if list, _ := d.ListPipelineTemplateVersions(ctx, 9, 9, id); len(list) != 0 {
		t.Fatal("cross-user versions must be invisible")
	}

	got, ok, err := d.GetPipelineTemplateVersion(ctx, 1, 2, id, 1)
	if err != nil || !ok || got.Definition != v1 {
		t.Fatalf("version 1 detail = (%+v, %v, %v)", got, ok, err)
	}

	newVersion, err := d.RestorePipelineTemplateVersion(ctx, 1, 2, id, 1)
	if err != nil || newVersion != 3 {
		t.Fatalf("restore = (%d, %v), want version 3", newVersion, err)
	}
	tpl, _, _ := d.GetPipelineTemplate(ctx, 1, 2, id)
	if tpl.Definition != v1 || tpl.Name != "tpl" {
		t.Fatalf("restored template = %+v", tpl)
	}
	if _, err := d.RestorePipelineTemplateVersion(ctx, 1, 2, id, 99); err != sql.ErrNoRows {
		t.Fatalf("missing version should return ErrNoRows, got %v", err)
	}

	if err := d.DeletePipelineTemplate(ctx, 1, 2, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM pipeline_template_versions WHERE template_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("versions should be removed with template, got %d", n)
	}
}
