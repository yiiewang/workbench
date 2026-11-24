package db

// ai_runs CRUD：状态流转、事件持久化与 org+user 隔离。

import (
	"encoding/json"
	"testing"
)

func TestAIRunCRUD(t *testing.T) {
	d := newTestDB(t)
	ctx := ctx()

	if err := d.CreateAIRun(ctx, 1, 2, "run-1", "qa-1", "sess-1"); err != nil {
		t.Fatal(err)
	}
	r, ok, err := d.GetAIRun(ctx, 1, 2, "run-1")
	if err != nil || !ok {
		t.Fatalf("get run = (%+v, %v, %v)", r, ok, err)
	}
	if r.Status != "running" || r.QAId != "qa-1" || r.SessionId != "sess-1" || string(r.Events) != "[]" {
		t.Fatalf("unexpected initial run: %+v", r)
	}

	events := json.RawMessage(`[{"event":"tool_call","data":{"name":"reindex_files"}}]`)
	if err := d.SaveAIRun(ctx, 1, 2, AIRun{
		ID: "run-1", Status: "done", Events: events, Result: "已完成",
	}); err != nil {
		t.Fatal(err)
	}
	r, ok, err = d.GetAIRun(ctx, 1, 2, "run-1")
	if err != nil || !ok {
		t.Fatalf("get updated run = (%+v, %v, %v)", r, ok, err)
	}
	if r.Status != "done" || r.Result != "已完成" || string(r.Events) != string(events) {
		t.Fatalf("unexpected updated run: %+v", r)
	}

	if _, ok, err := d.GetAIRun(ctx, 9, 9, "run-1"); err != nil || ok {
		t.Fatalf("cross-user read = (%v, %v), want not found", ok, err)
	}
	list, err := d.ListAIRuns(ctx, 1, 2, 10)
	if err != nil || len(list) != 1 || list[0].ID != "run-1" || list[0].Events != nil {
		t.Fatalf("list = (%+v, %v), want single run without events", list, err)
	}
}
