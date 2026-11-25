package db

// pipeline_runs CRUD：状态流转、步骤结果持久化与 org+user 隔离。

import (
	"encoding/json"
	"testing"
)

func TestPipelineRunCRUD(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()
	def := `{"name":"索引巡检","steps":[{"id":"a","uses":"tool/reindex_status"}]}`

	if err := d.CreatePipelineRun(ctx, 1, 2, "pipe-1", "索引巡检", def); err != nil {
		t.Fatal(err)
	}
	r, ok, err := d.GetPipelineRun(ctx, 1, 2, "pipe-1")
	if err != nil || !ok {
		t.Fatalf("get run = (%+v, %v, %v)", r, ok, err)
	}
	if r.Status != "running" || string(r.Definition) != def || string(r.Steps) != "[]" {
		t.Fatalf("unexpected initial run: %+v", r)
	}

	steps := json.RawMessage(`[{"id":"a","uses":"tool/reindex_status","status":"ok","output":{"running":false}}]`)
	if err := d.SavePipelineRun(ctx, 1, 2, PipelineRun{
		ID: "pipe-1", Status: "done", Steps: steps,
	}); err != nil {
		t.Fatal(err)
	}
	r, ok, err = d.GetPipelineRun(ctx, 1, 2, "pipe-1")
	if err != nil || !ok {
		t.Fatalf("get updated run = (%+v, %v, %v)", r, ok, err)
	}
	if r.Status != "done" || string(r.Steps) != string(steps) {
		t.Fatalf("unexpected updated run: %+v", r)
	}

	if _, ok, err := d.GetPipelineRun(ctx, 9, 9, "pipe-1"); err != nil || ok {
		t.Fatalf("cross-user read = (%v, %v), want not found", ok, err)
	}
	list, err := d.ListPipelineRuns(ctx, 1, 2, 10)
	if err != nil || len(list) != 1 || list[0].ID != "pipe-1" || list[0].Steps != nil {
		t.Fatalf("list = (%+v, %v), want single run without steps", list, err)
	}
}
