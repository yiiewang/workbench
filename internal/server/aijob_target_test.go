package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
)

func TestParseAIJobTarget(t *testing.T) {
	tests := []struct {
		name    string
		job     db.AIJob
		wantTyp string
		wantErr string
	}{
		{"legacy prompt", db.AIJob{Prompt: "写日报"}, aiJobTypeAI, ""},
		{"explicit ai", db.AIJob{Target: json.RawMessage(`{"type":"ai","prompt":"总结"}`)}, aiJobTypeAI, ""},
		{"tool", db.AIJob{Target: json.RawMessage(`{"type":"tool","tool":"list_tasks"}`)}, aiJobTypeTool, ""},
		{"interface", db.AIJob{Target: json.RawMessage(`{"type":"interface","path":"tasks/open","method":"GET"}`)}, aiJobTypeInterface, ""},
		{"pipeline inline", db.AIJob{Target: json.RawMessage(`{"type":"pipeline","definition":{"steps":[{"id":"a","uses":"llm","with":{"prompt":"x"}}]}}`)}, aiJobTypePipeline, ""},
		{"ai missing prompt", db.AIJob{Target: json.RawMessage(`{"type":"ai"}`)}, "", "requires prompt"},
		{"tool missing name", db.AIJob{Target: json.RawMessage(`{"type":"tool"}`)}, "", "requires tool"},
		{"pipeline missing body", db.AIJob{Target: json.RawMessage(`{"type":"pipeline"}`)}, "", "requires definition"},
		{"unknown type", db.AIJob{Target: json.RawMessage(`{"type":"bogus"}`)}, "", "unknown target type"},
		{"invalid json", db.AIJob{Target: json.RawMessage(`{`)}, "", "invalid target json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAIJobTarget(tc.job)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tc.wantTyp {
				t.Fatalf("type = %q, want %q", got.Type, tc.wantTyp)
			}
		})
	}
}

func TestValidateAIJobDefinitionScheduledRules(t *testing.T) {
	s := &Server{app: iris.New()}
	tests := []struct {
		name string
		def  pipeline.Definition
		want string
	}{
		{
			name: "recursive pipeline tool",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "tool/run_pipeline"}}},
			want: "not allowed in scheduled jobs",
		},
		{
			name: "ai chat api",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "api/ai/chat"}}},
			want: "not allowed in scheduled jobs",
		},
		{
			name: "pipeline trigger api",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "api/pipeline/run"}}},
			want: "not allowed in scheduled jobs",
		},
		{
			name: "job config write api",
			def: pipeline.Definition{
				AllowWrite: []string{"api/ai/jobs"},
				Steps:      []pipeline.Step{{ID: "a", Uses: "api/ai/jobs", With: map[string]any{"method": "POST"}}},
			},
			want: "not allowed in scheduled jobs",
		},
		{
			name: "write tool not allowed",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "tool/reindex_files"}}},
			want: "must be listed in allowWrite",
		},
		{
			name: "write tool allowed",
			def: pipeline.Definition{
				AllowWrite: []string{"reindex_files"},
				Steps:      []pipeline.Step{{ID: "a", Uses: "tool/reindex_files"}},
			},
		},
		{
			name: "read tool",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "tool/list_tasks"}}},
		},
		{
			name: "post api not allowed",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "api/tasks", With: map[string]any{"method": "POST"}}}},
			want: "must be listed in allowWrite",
		},
		{
			name: "post api allowed",
			def: pipeline.Definition{
				AllowWrite: []string{"api/tasks"},
				Steps:      []pipeline.Step{{ID: "a", Uses: "api/tasks", With: map[string]any{"method": "POST"}}},
			},
		},
		{
			name: "get api",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "api/tasks/open"}}},
		},
		{
			name: "unknown tool",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "tool/no_such_tool"}}},
			want: "unknown tool",
		},
		{
			name: "unsupported kind",
			def:  pipeline.Definition{Steps: []pipeline.Step{{ID: "a", Uses: "shell/rm"}}},
			want: "unsupported uses kind",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.validateAIJobDefinition(tc.def)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSummarizeAIJobResults(t *testing.T) {
	body := summarizeAIJobResults([]pipeline.StepResult{
		{ID: "run", Uses: "llm", Status: "ok", Output: "日报正文"},
	}, nil)
	if body != "日报正文" {
		t.Fatalf("single llm output should be returned as-is, got %q", body)
	}

	body = summarizeAIJobResults([]pipeline.StepResult{
		{ID: "fetch", Uses: "tool/list_tasks", Status: "ok", DurationMs: 12},
		{ID: "sum", Uses: "llm", Status: "failed", Error: "upstream 500"},
	}, errors.New(`step "sum" failed`))
	for _, want := range []string{"执行失败", "## 执行步骤", "fetch", "sum", "upstream 500"} {
		if !strings.Contains(body, want) {
			t.Fatalf("summary missing %q: %s", want, body)
		}
	}
}

func TestValidateAIJobTargetPreview(t *testing.T) {
	s := &Server{app: iris.New()}
	blocked := db.AIJobWithOwner{AIJob: db.AIJob{
		Target: json.RawMessage(`{"type":"tool","tool":"run_pipeline"}`),
	}}
	if err := s.validateAIJobTarget(context.Background(), blocked); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("recursive tool should be rejected at create time, got %v", err)
	}
	ok := db.AIJobWithOwner{AIJob: db.AIJob{
		Target: json.RawMessage(`{"type":"tool","tool":"list_tasks"}`),
	}}
	if err := s.validateAIJobTarget(context.Background(), ok); err != nil {
		t.Fatalf("read tool should pass preview: %v", err)
	}
}

func TestExpandJobParams(t *testing.T) {
	def := pipeline.Definition{Steps: []pipeline.Step{{
		ID: "run", Uses: "api/tasks/open",
		With: map[string]any{
			"query": map[string]any{"date": "{{run.date}}", "kind": "{{params.kind}}"},
		},
	}}}
	got := expandJobParams(def, map[string]any{"kind": "daily", "date": "{{run.date}}"})
	q, ok := got.Steps[0].With["query"].(map[string]any)
	if !ok {
		t.Fatalf("query lost: %+v", got.Steps[0].With)
	}
	date, _ := q["date"].(string)
	if len(date) != 10 || date[4] != '-' {
		t.Fatalf("run.date not expanded: %v", q["date"])
	}
	if q["kind"] != "daily" {
		t.Fatalf("params.kind not expanded: %v", q["kind"])
	}
}
