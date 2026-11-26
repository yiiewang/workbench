package server

// 编排装配单测：合成 iris.Context 的身份注入、三类执行器的授权边界、
// 站内 API 自请求、工具注册与参数校验。

import (
	"context"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/pipeline"
)

func TestWithToolContext(t *testing.T) {
	app := iris.New()
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: app}
	got := s.withToolContext(context.Background(), 7, 42, func(ictx iris.Context) any {
		return map[string]any{
			"org":  currentOrgID(ictx),
			"user": currentUserID(ictx),
			"err":  ictx.Request().Context().Err(),
		}
	})
	m, ok := got.(map[string]any)
	if !ok || m["org"] != int64(7) || m["user"] != int64(42) || m["err"] != nil {
		t.Fatalf("synthetic context mismatch: %+v", got)
	}
}

func TestRunPipelineToolRegistered(t *testing.T) {
	s := &Server{}
	tool := s.aiToolByName("run_pipeline")
	if tool == nil || !tool.NeedApproval {
		t.Fatal("run_pipeline must be registered and require one approval for the whole flow")
	}
}

func TestExecPipelineToolRequiresAllowWrite(t *testing.T) {
	s := &Server{app: iris.New()}
	_, err := s.execPipelineTool(context.Background(), "reindex_files", nil,
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err == nil || !strings.Contains(err.Error(), "allowWrite") {
		t.Fatalf("write tool should require allowWrite, got %v", err)
	}
	if _, err := s.execPipelineTool(context.Background(), "no_such_tool", nil,
		pipeline.Definition{}, pipeline.NewRunContext(1, 2)); err == nil {
		t.Fatal("unknown tool should fail")
	}
}

func TestExecPipelineAPIPathGuard(t *testing.T) {
	s := &Server{app: iris.New()}
	_, err := s.execPipelineAPI(context.Background(), "../secret", nil,
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err == nil || !strings.Contains(err.Error(), "invalid api path") {
		t.Fatalf("path escape should be rejected, got %v", err)
	}
	_, err = s.execPipelineAPI(context.Background(), "tasks", map[string]any{"method": "POST"},
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err == nil || !strings.Contains(err.Error(), "allowWrite") {
		t.Fatalf("non-GET api step should require allowWrite, got %v", err)
	}
}

func TestExecPipelineAPISignedToken(t *testing.T) {
	secret := []byte("pipeline-test-secret")
	app := iris.New()
	app.Get("/api/ping", AuthMiddleware(secret, func(_ context.Context, userID, orgID int64) (int64, string, string, string, bool, error) {
		return orgID, "org", "user", "member", false, nil
	}), func(ctx iris.Context) {
		writeOK(ctx, map[string]any{"pong": currentUserID(ctx), "org": currentOrgID(ctx)})
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: app, tokenSecret: secret}
	out, err := s.execPipelineAPI(context.Background(), "ping", nil,
		pipeline.Definition{}, pipeline.NewRunContext(3, 42))
	if err != nil {
		t.Fatalf("api step failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["pong"] != float64(42) || m["org"] != float64(3) {
		t.Fatalf("identity not propagated: %+v", out)
	}
}

func TestExecRunPipelineValidatesDefinition(t *testing.T) {
	// 校验失败发生在任何 DB/app 访问之前，可直接传空 Server 与 nil ctx
	got := execRunPipeline(&Server{}, nil, map[string]any{"name": "空流程", "steps": []any{}})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result: %+v", got)
	}
	if e, _ := m["error"].(string); !strings.Contains(e, "steps is required") {
		t.Fatalf("want validation error, got %+v", got)
	}
}

func TestExecPipelineAPIQuery(t *testing.T) {
	secret := []byte("pipeline-test-secret")
	app := iris.New()
	app.Get("/api/echo", AuthMiddleware(secret, func(_ context.Context, userID, orgID int64) (int64, string, string, string, bool, error) {
		return orgID, "org", "user", "member", false, nil
	}), func(ctx iris.Context) {
		writeOK(ctx, map[string]any{"date": ctx.URLParam("date"), "kind": ctx.URLParam("kind")})
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: app, tokenSecret: secret}
	out, err := s.execPipelineAPI(context.Background(), "echo", map[string]any{
		"method": "GET",
		"query":  map[string]any{"date": "2026-11-25", "kind": "daily"},
	}, pipeline.Definition{}, pipeline.NewRunContext(3, 42))
	if err != nil {
		t.Fatalf("api step failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["date"] != "2026-11-25" || m["kind"] != "daily" {
		t.Fatalf("query not propagated: %+v", out)
	}
}
