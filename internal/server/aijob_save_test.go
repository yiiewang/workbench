package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// newAIJobSaveTestServer 构建只用于 saveAIJob 单测的 Server（临时 DB + 临时静态目录）。
// DB 目录手动创建，避免异步访问日志 goroutine 占用 WAL 导致 t.TempDir 清理失败。
func newAIJobSaveTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	dbDir, err := os.MkdirTemp("", "wbsave")
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		time.Sleep(10 * time.Millisecond)
		_ = os.RemoveAll(dbDir)
	})
	cfg := config.DefaultConfig()
	cfg.Server.StaticDir = t.TempDir()
	srv, err := New(d, cfg, []byte("test-secret-key"), filepath.Join(t.TempDir(), "access.log"))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, d
}

func TestSaveAIJobCreateThenUpdate(t *testing.T) {
	srv, d := newAIJobSaveTestServer(t)
	ctx := context.Background()

	orgID, err := d.EnsureOrg(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := d.UpsertUser(ctx, orgID, "alice", "hash", db.RoleIDUser)
	if err != nil {
		t.Fatal(err)
	}

	body := &aiJobBody{
		Name:     "日报",
		CronExpr: "0 9 * * *",
		Target:   json.RawMessage(`{"type":"pipeline","definition":{"steps":[{"id":"s1","uses":"llm","with":{"prompt":"写日报"}}]}}`),
	}
	id, err := srv.saveAIJob(ctx, orgID, userID, body, 0, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("create returned id %d", id)
	}

	job, err := d.GetAIJob(ctx, orgID, userID, id)
	if err != nil || job == nil {
		t.Fatalf("get created job: %v, nil=%v", err, job == nil)
	}
	if job.Name != "日报" {
		t.Fatalf("job name = %q", job.Name)
	}
	triggers, err := d.ListCronTriggers(ctx, orgID, userID, "job", id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || triggers[0].CronExpr != "0 9 * * *" {
		t.Fatalf("triggers after create = %+v", triggers)
	}

	// 更新：同一任务不应新增第二条 trigger
	updatedID, err := srv.saveAIJob(ctx, orgID, userID, &aiJobBody{
		Name:     "周报",
		CronExpr: "0 10 * * 1",
		Target:   json.RawMessage(`{"type":"pipeline","definition":{"steps":[{"id":"s1","uses":"llm","with":{"prompt":"写周报"}}]}}`),
	}, id, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updatedID != id {
		t.Fatalf("update returned id %d, want %d", updatedID, id)
	}
	job, err = d.GetAIJob(ctx, orgID, userID, id)
	if err != nil || job == nil {
		t.Fatalf("get updated job: %v", err)
	}
	if job.Name != "周报" {
		t.Fatalf("job name after update = %q", job.Name)
	}
	triggers, err = d.ListCronTriggers(ctx, orgID, userID, "job", id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || triggers[0].CronExpr != "0 10 * * 1" {
		t.Fatalf("triggers after update = %+v", triggers)
	}
}

func TestSaveAIJobErrors(t *testing.T) {
	srv, d := newAIJobSaveTestServer(t)
	ctx := context.Background()

	orgID, err := d.EnsureOrg(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := d.UpsertUser(ctx, orgID, "alice", "hash", db.RoleIDUser)
	if err != nil {
		t.Fatal(err)
	}
	validTarget := json.RawMessage(`{"type":"pipeline","definition":{"steps":[{"id":"s1","uses":"llm","with":{"prompt":"x"}}]}}`)

	tests := []struct {
		name  string
		body  *aiJobBody
		jobID int64
		want  int // 期望的 HTTP 状态语义
	}{
		{"missing name", &aiJobBody{CronExpr: "0 9 * * *"}, 0, 400},
		{"invalid cron", &aiJobBody{Name: "x", CronExpr: "bad"}, 0, 400},
		{"missing target and prompt", &aiJobBody{Name: "x", CronExpr: "0 9 * * *"}, 0, 400},
		{"update unknown job", &aiJobBody{Name: "x", CronExpr: "0 9 * * *", Target: validTarget}, 9999, 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.saveAIJob(ctx, orgID, userID, tc.body, tc.jobID, nil)
			var se *aiJobSaveError
			if !errors.As(err, &se) {
				t.Fatalf("want *aiJobSaveError, got %v", err)
			}
			if se.status != tc.want {
				t.Fatalf("status = %d, want %d (msg=%q)", se.status, tc.want, se.msg)
			}
		})
	}
}

func TestSaveAIJobPreflightRejects(t *testing.T) {
	srv, d := newAIJobSaveTestServer(t)
	ctx := context.Background()

	orgID, err := d.EnsureOrg(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := d.UpsertUser(ctx, orgID, "alice", "hash", db.RoleIDUser)
	if err != nil {
		t.Fatal(err)
	}

	body := &aiJobBody{
		Name:     "日报",
		CronExpr: "0 9 * * *",
		Target:   json.RawMessage(`{"type":"pipeline","definition":{"steps":[{"id":"s1","uses":"llm","with":{"prompt":"x"}}]}}`),
	}
	_, err = srv.saveAIJob(ctx, orgID, userID, body, 0, func(db.AIJobWithOwner) error {
		return errors.New("target not runnable")
	})
	var se *aiJobSaveError
	if !errors.As(err, &se) || se.status != 400 {
		t.Fatalf("want 400 aiJobSaveError, got %v", err)
	}
	// 预检失败不应落库
	jobs, err := d.ListAIJobs(ctx, orgID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("preflight failure should not persist job, got %d", len(jobs))
	}
}
