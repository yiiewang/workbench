package db

import (
	"context"
	"testing"
)

// target 列读写：显式 target 往返、旧 Update 不清空 target、显式空 allowWrite 可清空。
func TestAIJobTargetCRUD(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	// ai_jobs.user_id 有外键到 users；本测试只验证列读写，不铺用户数据。
	d.conn.SetMaxOpenConns(1)
	if _, err := d.conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}

	id, err := d.CreateAIJobWithSpec(ctx, 1, 2, AIJobSpec{
		Name:        "盘中检查",
		CronExpr:    "*/30 9-15 * * 1-5",
		Prompt:      "",
		Enabled:     true,
		TargetType:  "interface",
		TargetJSON:  `{"type":"interface","method":"GET","path":"tasks/open"}`,
		AllowWrite:  []string{"notify_user"},
		ContextJSON: `{"tasks":false,"ragTopK":0}`,
		NotifyJSON:  `{"on":["success","failure"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	job, err := d.GetAIJob(ctx, 1, 2, id)
	if err != nil || job == nil {
		t.Fatalf("get: %v, %v", job, err)
	}
	if job.TargetType != "interface" || string(job.Target) != `{"type":"interface","method":"GET","path":"tasks/open"}` {
		t.Fatalf("target mismatch: type=%q json=%s", job.TargetType, job.Target)
	}
	if len(job.AllowWrite) != 1 || job.AllowWrite[0] != "notify_user" {
		t.Fatalf("allowWrite mismatch: %v", job.AllowWrite)
	}
	if string(job.Context) != `{"tasks":false,"ragTopK":0}` {
		t.Fatalf("context mismatch: %s", job.Context)
	}

	// 旧签名只改基础字段，target 保持现值
	if err := d.UpdateAIJob(ctx, 1, 2, id, "盘中检查（改）", "*/15 9-15 * * 1-5", "", true); err != nil {
		t.Fatal(err)
	}
	job, err = d.GetAIJob(ctx, 1, 2, id)
	if err != nil || job == nil {
		t.Fatalf("get after update: %v, %v", job, err)
	}
	if job.Name != "盘中检查（改）" || job.TargetType != "interface" || string(job.Target) == "" {
		t.Fatalf("legacy update should keep target: name=%q type=%q target=%s", job.Name, job.TargetType, job.Target)
	}

	// 显式空 allowWrite 表示清空；target 可整体替换
	if err := d.UpdateAIJobWithSpec(ctx, 1, 2, id, AIJobSpec{
		Name:       job.Name,
		CronExpr:   job.CronExpr,
		Enabled:    true,
		TargetType: "tool",
		TargetJSON: `{"type":"tool","tool":"list_tasks"}`,
		AllowWrite: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	job, err = d.GetAIJob(ctx, 1, 2, id)
	if err != nil || job == nil {
		t.Fatalf("get after spec update: %v, %v", job, err)
	}
	if job.TargetType != "tool" || len(job.AllowWrite) != 0 {
		t.Fatalf("spec update mismatch: type=%q allowWrite=%v", job.TargetType, job.AllowWrite)
	}
}

// 调度字段派生自 schedule_triggers：建规则后 Get/List 能读到 cron/enabled。
func TestAIJobTriggerDerivedFields(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	d.conn.SetMaxOpenConns(1)
	if _, err := d.conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.conn.Exec(`DELETE FROM schedule_triggers`); err != nil {
		t.Fatal(err)
	}

	id, err := d.CreateAIJobWithSpec(ctx, 1, 2, AIJobSpec{Name: "日报", Prompt: "写日报"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := d.GetAIJob(ctx, 1, 2, id)
	if err != nil || job == nil {
		t.Fatalf("get: %v, %v", job, err)
	}
	if job.CronExpr != "" || job.Enabled {
		t.Fatalf("job without trigger should have empty schedule: %+v", job)
	}

	if _, err := d.CreateCronTrigger(ctx, 1, 2, "job", id, "0 9 * * *", true); err != nil {
		t.Fatal(err)
	}
	job, err = d.GetAIJob(ctx, 1, 2, id)
	if err != nil || job == nil {
		t.Fatalf("get after trigger: %v, %v", job, err)
	}
	if job.CronExpr != "0 9 * * *" || !job.Enabled {
		t.Fatalf("derived schedule mismatch: cron=%q enabled=%v", job.CronExpr, job.Enabled)
	}

	jobs, err := d.ListAIJobs(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	var found *AIJob
	for i := range jobs {
		if jobs[i].ID == id {
			found = &jobs[i]
			break
		}
	}
	if found == nil || found.CronExpr != "0 9 * * *" || !found.Enabled {
		t.Fatalf("list derived schedule mismatch: %+v", found)
	}
}
