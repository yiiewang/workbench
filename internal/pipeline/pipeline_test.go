package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefinitionValidate(t *testing.T) {
	cases := []struct {
		name string
		def  Definition
		ok   bool
	}{
		{"empty", Definition{}, false},
		{"valid", Definition{Steps: []Step{{ID: "a", Uses: "llm"}, {ID: "b", Uses: "tool/reindex_status"}}}, true},
		{"duplicate id", Definition{Steps: []Step{{ID: "a", Uses: "llm"}, {ID: "a", Uses: "llm"}}}, false},
		{"bad id", Definition{Steps: []Step{{ID: "a b", Uses: "llm"}}}, false},
		{"bad uses", Definition{Steps: []Step{{ID: "a", Uses: "tool/"}}}, false},
		{"bad when", Definition{Steps: []Step{{ID: "a", Uses: "llm", When: "maybe"}}}, false},
		{"retry overflow", Definition{Steps: []Step{{ID: "a", Uses: "llm", Retry: 9}}}, false},
	}
	for _, c := range cases {
		if err := c.def.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestDefinitionLayersDAG(t *testing.T) {
	def := Definition{Steps: []Step{
		{ID: "a", Uses: "llm"},
		{ID: "b", Uses: "llm", Needs: []string{"a"}},
		{ID: "c", Uses: "llm", Needs: []string{"a"}},
		{ID: "d", Uses: "llm", Needs: []string{"b", "c"}},
	}}
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	layers, err := def.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 || len(layers[0]) != 1 || len(layers[1]) != 2 || len(layers[2]) != 1 {
		t.Fatalf("unexpected layers: %v", layers)
	}
	if layers[1][0] != 1 || layers[1][1] != 2 || layers[2][0] != 3 {
		t.Fatalf("layer order not stable: %v", layers)
	}
}

func TestDefinitionValidateNeeds(t *testing.T) {
	cases := []struct {
		name string
		def  Definition
		ok   bool
	}{
		{"unknown need", Definition{Steps: []Step{{ID: "a", Uses: "llm", Needs: []string{"z"}}}}, false},
		{"self need", Definition{Steps: []Step{{ID: "a", Uses: "llm", Needs: []string{"a"}}}}, false},
		{"cycle", Definition{Steps: []Step{
			{ID: "a", Uses: "llm", Needs: []string{"b"}},
			{ID: "b", Uses: "llm", Needs: []string{"a"}},
		}}, false},
		{"explicit empty needs", Definition{Steps: []Step{
			{ID: "a", Uses: "llm", Needs: []string{}},
			{ID: "b", Uses: "llm", Needs: []string{}},
		}}, true},
	}
	for _, c := range cases {
		if err := c.def.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
	layers, _ := cases[3].def.Layers()
	if len(layers) != 1 || len(layers[0]) != 2 {
		t.Fatalf("explicit empty needs should share one layer: %v", layers)
	}
}

func TestRunContextInterpolate(t *testing.T) {
	r := NewRunContext(1, 2)
	r.Outputs["a"] = "重建完成"
	r.Outputs["b"] = map[string]any{"running": false, "total": 3}
	got := r.Interpolate(map[string]any{
		"prompt": "状态：{{ steps.a.output }}，进度 {{steps.b.output}}，未知 {{ steps.z.output }}",
		"nested": []any{"{{ steps.a.output }}"},
	})
	m := got.(map[string]any)
	prompt := m["prompt"].(string)
	if !strings.Contains(prompt, "重建完成") || !strings.Contains(prompt, `"running":false`) {
		t.Fatalf("interpolation failed: %s", prompt)
	}
	if !strings.Contains(prompt, "{{ steps.z.output }}") {
		t.Fatal("unresolved reference should stay verbatim")
	}
	if m["nested"].([]any)[0] != "重建完成" {
		t.Fatal("nested list not interpolated")
	}
}

func TestRunnerSequenceAndFailure(t *testing.T) {
	def := Definition{Steps: []Step{
		{ID: "first", Uses: "llm", With: map[string]any{"prompt": "hi"}},
		{ID: "second", Uses: "llm", With: map[string]any{"prompt": "上一步：{{ steps.first.output }}"}},
		{ID: "third", Uses: "llm"},
		{ID: "fourth", Uses: "llm"},
	}}
	var seen []string
	var snapshots [][]StepResult
	runner := NewRunner(func(_ context.Context, _, _, _ string, args map[string]any, _ *RunContext) (any, error) {
		p, _ := args["prompt"].(string)
		seen = append(seen, p)
		if strings.Contains(p, "上一步") {
			return "ok", nil
		}
		if len(seen) == 3 {
			return nil, errors.New("boom")
		}
		return "第一步输出", nil
	}, func(rs []StepResult) { snapshots = append(snapshots, append([]StepResult(nil), rs...)) })

	results, err := runner.Run(context.Background(), def, NewRunContext(1, 2))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want failure, got %v", err)
	}
	if seen[1] != "上一步：第一步输出" {
		t.Fatalf("step output not passed: %q", seen[1])
	}
	if len(results) != 4 || results[0].Status != "ok" || results[1].Status != "ok" || results[2].Status != "failed" || results[3].Status != "skipped" {
		t.Fatalf("unexpected results: %+v", results)
	}
	// 线性链 4 层；失败后仍遍历剩余层做 when 判定，故每层各落一次快照
	if len(snapshots) != 4 {
		t.Fatalf("onStep should emit once per layer, got %d", len(snapshots))
	}
}

func TestRunnerParallelAndInterpolation(t *testing.T) {
	// a/b 显式 needs: [] → 同层并行；用 barrier 验证两者确实同时启动
	var arrived int32
	barrier := make(chan struct{})
	def := Definition{Steps: []Step{
		{ID: "a", Uses: "llm", Needs: []string{}},
		{ID: "b", Uses: "llm", Needs: []string{}},
		{ID: "join", Uses: "llm", Needs: []string{"a", "b"},
			With: map[string]any{"prompt": "{{ steps.a.output }} + {{ steps.b.output }}"}},
	}}
	var joinPrompt string
	runner := NewRunner(func(_ context.Context, _, _, _ string, args map[string]any, _ *RunContext) (any, error) {
		p, _ := args["prompt"].(string)
		if strings.Contains(p, "+") {
			joinPrompt = p
			return "joined", nil
		}
		if atomic.AddInt32(&arrived, 1) == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
			return "parallel", nil
		case <-time.After(2 * time.Second):
			return nil, errors.New("steps did not run in parallel")
		}
	}, nil)
	results, err := runner.Run(context.Background(), def, NewRunContext(1, 2))
	if err != nil {
		t.Fatalf("parallel run failed: %v", err)
	}
	if len(results) != 3 || results[0].Status != "ok" || results[1].Status != "ok" || results[2].Status != "ok" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if joinPrompt != "parallel + parallel" {
		t.Fatalf("join step did not see both outputs: %q", joinPrompt)
	}
}

func TestRunnerStepTimeout(t *testing.T) {
	runner := NewRunner(func(ctx context.Context, _, _, _ string, _ map[string]any, _ *RunContext) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, nil)
	runner.StepTimeout = 5 * time.Millisecond
	runner.LLMStepTimeout = 5 * time.Millisecond
	_, err := runner.Run(context.Background(), Definition{Steps: []Step{{ID: "slow", Uses: "llm"}}}, NewRunContext(1, 2))
	if err == nil {
		t.Fatal("want step timeout failure")
	}
}

// 工具参数经 map → JSON → Definition 往返：显式空数组必须保留，
// 否则会退化成 nil（=依赖前一步），并行语义丢失。
func TestNeedsEmptyArraySurvivesJSONRoundTrip(t *testing.T) {
	raw := `{"steps":[{"id":"a","uses":"llm","needs":[]},{"id":"b","uses":"llm","needs":[]}]}`
	var def Definition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var again Definition
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatal(err)
	}
	layers, err := again.Layers()
	if err != nil || len(layers) != 1 || len(layers[0]) != 2 {
		t.Fatalf("empty needs lost in round trip: %s layers=%v err=%v", b, layers, err)
	}
}

func TestRunnerWhenAndRetry(t *testing.T) {
	var flakyCalls atomic.Int32
	mk := func(tag string) map[string]any { return map[string]any{"tag": tag} }
	def := Definition{Steps: []Step{
		{ID: "main", Uses: "llm", With: mk("main")},
		{ID: "compensate", Uses: "llm", Needs: []string{"main"}, When: "on_failure", With: mk("compensate")},
		{ID: "normal", Uses: "llm", Needs: []string{"main"}, With: mk("normal")},
		{ID: "cleanup", Uses: "llm", Needs: []string{"main"}, When: "always", With: mk("cleanup")},
		{ID: "flaky", Uses: "llm", Needs: []string{"main"}, When: "always", Retry: 2, With: mk("flaky")},
	}}
	exec := func(_ context.Context, _, _, _ string, args map[string]any, _ *RunContext) (any, error) {
		tag, _ := args["tag"].(string)
		if tag == "main" {
			return nil, errors.New("boom")
		}
		if tag == "flaky" && flakyCalls.Add(1) < 3 {
			return nil, errors.New("retry me")
		}
		return tag, nil
	}
	results, err := NewRunner(exec, nil).Run(context.Background(), def, NewRunContext(1, 2))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want main failure, got %v", err)
	}
	byID := map[string]StepResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	if byID["main"].Status != "failed" || byID["compensate"].Status != "ok" ||
		byID["normal"].Status != "skipped" || byID["cleanup"].Status != "ok" || byID["flaky"].Status != "ok" {
		t.Fatalf("unexpected statuses: %+v", byID)
	}
	if got := flakyCalls.Load(); got != 3 {
		t.Fatalf("retry attempts = %d, want 3", got)
	}
}

func TestRunnerOnFailureSkippedWhenSuccess(t *testing.T) {
	def := Definition{Steps: []Step{
		{ID: "a", Uses: "llm"},
		{ID: "notify", Uses: "llm", Needs: []string{"a"}, When: "on_failure"},
		{ID: "always", Uses: "llm", Needs: []string{"a"}, When: "always"},
	}}
	results, err := NewRunner(func(_ context.Context, _, _, _ string, _ map[string]any, _ *RunContext) (any, error) {
		return "ok", nil
	}, nil).Run(context.Background(), def, NewRunContext(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "ok" || results[1].Status != "skipped" || results[2].Status != "ok" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestRunnerOnAttemptPerRetry(t *testing.T) {
	var attempts []string
	runner := NewRunner(func(context.Context, string, string, string, map[string]any, *RunContext) (any, error) {
		return nil, errors.New("boom")
	}, nil)
	runner.OnAttempt = func(sr StepResult) { attempts = append(attempts, sr.Error) }
	_, err := runner.Run(context.Background(),
		Definition{Steps: []Step{{ID: "s1", Uses: "llm", Retry: 2}}}, NewRunContext(1, 2))
	if err == nil {
		t.Fatal("want error")
	}
	if len(attempts) != 3 || attempts[0] != "attempt 1/3" || attempts[2] != "attempt 3/3" {
		t.Fatalf("attempts = %v", attempts)
	}
}

func TestRunnerLLMStepTimeout(t *testing.T) {
	var llmDeadline, toolDeadline time.Duration
	runner := NewRunner(func(ctx context.Context, _, kind, _ string, _ map[string]any, _ *RunContext) (any, error) {
		if d, ok := ctx.Deadline(); ok {
			if kind == "llm" {
				llmDeadline = time.Until(d)
			} else {
				toolDeadline = time.Until(d)
			}
		}
		return "ok", nil
	}, nil)
	runner.StepTimeout = 100 * time.Millisecond
	runner.LLMStepTimeout = time.Second
	_, err := runner.Run(context.Background(), Definition{Steps: []Step{
		{ID: "a", Uses: "llm"},
		{ID: "b", Uses: "tool/x", Needs: []string{"a"}},
	}}, NewRunContext(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if llmDeadline < 500*time.Millisecond {
		t.Fatalf("llm deadline = %v, want ~1s", llmDeadline)
	}
	if toolDeadline >= 500*time.Millisecond {
		t.Fatalf("tool deadline = %v, want ~100ms", toolDeadline)
	}
}
