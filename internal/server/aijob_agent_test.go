package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
)

// agentTestServer 用 httptest 顶替上游，并注入一个纯函数的只读工具，
// 让 Agent 循环不依赖数据库与文件系统。
func agentTestServer(t *testing.T, reply func(round int, payload map[string]any) map[string]any) (*Server, func()) {
	t.Helper()
	round := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream payload: %v", err)
		}
		out := reply(round, payload)
		round++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	cfg := &config.Config{}
	cfg.AI.Chat.Gateways = []config.ChatGateway{{
		ID: "test", BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "test-model",
	}}
	s := &Server{
		cfg: cfg,
		app: iris.New(),
		mcpTools: []aiTool{{
			Name:   "agent_echo",
			Desc:   "回显参数",
			Params: map[string]any{"type": "object", "properties": map[string]any{}},
			Exec: func(_ *Server, _ iris.Context, args map[string]any) any {
				return map[string]any{"echo": args}
			},
		}},
	}
	return s, srv.Close
}

func chatReply(content string, calls ...aiToolCall) map[string]any {
	msg := map[string]any{"role": "assistant", "content": content}
	finish := "stop"
	if len(calls) > 0 {
		msg["tool_calls"] = calls
		finish = "tool_calls"
	}
	return map[string]any{"choices": []any{map[string]any{"message": msg, "finish_reason": finish}}}
}

func agentCall(id, name, args string) aiToolCall {
	return aiToolCall{ID: id, Type: "function", Function: aiToolCallFunction{Name: name, Arguments: args}}
}

func payloadMessages(payload map[string]any) []map[string]any {
	raw, _ := payload["messages"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func payloadToolNames(payload map[string]any) []string {
	raw, _ := payload["tools"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := m["function"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := fn["name"].(string); ok {
			out = append(out, name)
		}
	}
	return out
}

func llmStep(tools any) pipeline.Step {
	with := map[string]any{"prompt": "写日报"}
	if tools != nil {
		with["tools"] = tools
	}
	return pipeline.Step{ID: "run", Uses: "llm", With: with}
}

func TestStepToolNames(t *testing.T) {
	tests := []struct {
		name    string
		tools   any
		want    []string
		wantErr bool
	}{
		{"absent", nil, nil, false},
		{"normalized", []string{"list_tasks", " notify_user ", "list_tasks"}, []string{"list_tasks", "notify_user"}, false},
		{"json array", []any{"list_tasks", "notify_user"}, []string{"list_tasks", "notify_user"}, false},
		{"non string item", []any{"list_tasks", 42}, nil, true},
		{"not an array", "notify_user", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stepToolNames(map[string]any{"tools": tc.tools})
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildAIJobDefinitionAITools(t *testing.T) {
	s := &Server{app: iris.New()}
	withAllow := db.AIJobWithOwner{AIJob: db.AIJob{
		Name: "日报", TargetType: aiJobTypeAI, Prompt: "总结", AllowWrite: []string{"list_tasks", "notify_user"},
	}}
	def, err := s.legacyTargetToDefinition(context.Background(), withAllow)
	if err != nil {
		t.Fatalf("legacy ai with allowWrite: %v", err)
	}
	names, err := stepToolNames(def.Steps[0].With)
	if err != nil || strings.Join(names, ",") != "list_tasks,notify_user" {
		t.Fatalf("ai target tools = %v, err = %v", names, err)
	}

	plain := db.AIJobWithOwner{AIJob: db.AIJob{Name: "日报", TargetType: aiJobTypeAI, Prompt: "总结"}}
	def, err = s.legacyTargetToDefinition(context.Background(), plain)
	if err != nil {
		t.Fatalf("legacy ai without allowWrite: %v", err)
	}
	if _, ok := def.Steps[0].With["tools"]; ok {
		t.Fatal("empty allowWrite should not add tools to llm step")
	}
}

func TestValidateAIJobDefinitionLLMTools(t *testing.T) {
	s := &Server{app: iris.New()}
	tests := []struct {
		name    string
		tools   any
		wantErr string
	}{
		{"read and write tools", []string{"list_tasks", "notify_user"}, ""},
		{"no tools", nil, ""},
		{"unknown tool", []string{"no_such_tool"}, "unknown tool"},
		{"blacklisted tool", []string{"run_pipeline"}, "not allowed"},
		{"api entry", []string{"api/tasks"}, "not a tool name"},
		{"invalid shape", "notify_user", "array of tool names"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def := pipeline.Definition{Steps: []pipeline.Step{llmStep(tc.tools)}}
			err := s.validateAIJobDefinition(def)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestAIJobAgentLoop(t *testing.T) {
	var firstTools []string
	var toolMessages []map[string]any
	s, closeSrv := agentTestServer(t, func(round int, payload map[string]any) map[string]any {
		if round == 0 {
			firstTools = payloadToolNames(payload)
			return chatReply("", agentCall("c1", "agent_echo", `{"q":"hi"}`))
		}
		for _, m := range payloadMessages(payload) {
			if m["role"] == "tool" {
				toolMessages = append(toolMessages, m)
			}
		}
		return chatReply("汇总完成")
	})
	defer closeSrv()

	out, _, err := s.aiChatAgent(context.Background(), "请汇总", []string{"agent_echo"},
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err != nil {
		t.Fatalf("agent loop: %v", err)
	}
	if out != "汇总完成" {
		t.Fatalf("output = %q", out)
	}
	if len(firstTools) != 1 || firstTools[0] != "agent_echo" {
		t.Fatalf("upstream tools = %v, want only agent_echo", firstTools)
	}
	if len(toolMessages) != 1 {
		t.Fatalf("tool messages = %d, want 1", len(toolMessages))
	}
	content, _ := toolMessages[0]["content"].(string)
	if !strings.Contains(content, `"echo"`) || !strings.Contains(content, `"hi"`) {
		t.Fatalf("tool result not fed back: %s", content)
	}
}

func TestAIJobAgentRoundLimit(t *testing.T) {
	calls := 0
	lastHasTools := true
	s, closeSrv := agentTestServer(t, func(round int, payload map[string]any) map[string]any {
		calls++
		_, lastHasTools = payload["tools"]
		if round >= aiMaxToolRounds {
			return chatReply("轮次用尽")
		}
		return chatReply("", agentCall("c"+strconv.Itoa(round), "agent_echo", `{}`))
	})
	defer closeSrv()

	out, _, err := s.aiChatAgent(context.Background(), "请汇总", []string{"agent_echo"},
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err != nil {
		t.Fatalf("agent loop: %v", err)
	}
	if out != "轮次用尽" || calls != aiMaxToolRounds+1 || lastHasTools {
		t.Fatalf("out=%q calls=%d lastHasTools=%v", out, calls, lastHasTools)
	}
}

func TestAIJobAgentRejectsToolOutsideWhitelist(t *testing.T) {
	s, closeSrv := agentTestServer(t, func(round int, payload map[string]any) map[string]any {
		if round == 0 {
			return chatReply("", agentCall("c1", "notify_user", `{"subject":"x","body":"y"}`))
		}
		var toolContent string
		for _, m := range payloadMessages(payload) {
			if m["role"] == "tool" {
				toolContent, _ = m["content"].(string)
			}
		}
		if !strings.Contains(toolContent, "tool not allowed: notify_user") {
			t.Errorf("unexpected tool result: %s", toolContent)
		}
		return chatReply("已说明")
	})
	defer closeSrv()

	out, _, err := s.aiChatAgent(context.Background(), "请汇总", []string{"agent_echo"},
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err != nil || out != "已说明" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestExecPipelineLLMWithoutTools(t *testing.T) {
	calls := 0
	hasTools := true
	s, closeSrv := agentTestServer(t, func(_ int, payload map[string]any) map[string]any {
		calls++
		_, hasTools = payload["tools"]
		return chatReply("普通回答")
	})
	defer closeSrv()

	out, err := s.execPipelineLLM("run", context.Background(), map[string]any{"prompt": "写日报"},
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err != nil {
		t.Fatalf("llm step: %v", err)
	}
	if out != "普通回答" || calls != 1 || hasTools {
		t.Fatalf("out=%v calls=%d hasTools=%v", out, calls, hasTools)
	}
}

func TestDefHasWrite(t *testing.T) {
	s := &Server{app: iris.New()}
	tests := []struct {
		name string
		def  pipeline.Definition
		want bool
	}{
		{"empty", pipeline.Definition{}, false},
		{"read only whitelist", pipeline.Definition{
			AllowWrite: []string{"list_tasks"}, Steps: []pipeline.Step{llmStep([]string{"list_tasks"})},
		}, false},
		{"write whitelist", pipeline.Definition{
			AllowWrite: []string{"notify_user"}, Steps: []pipeline.Step{llmStep([]string{"notify_user"})},
		}, true},
		{"inline whitelist only", pipeline.Definition{
			Steps: []pipeline.Step{llmStep([]string{"notify_user"})},
		}, true},
		{"non GET api", pipeline.Definition{AllowWrite: []string{"api/tasks"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.defHasWrite(tc.def); got != tc.want {
				t.Fatalf("defHasWrite = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStripToolMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"正常正文", "正常正文"},
		{"前半<tool_calls:abc><tool_call:abc>search<arg_key:abc>q</arg_key:abc></tool_call:abc></tool_calls:abc>", "前半"},
		{"正文<tool_calls:abc><tool_call:abc>search", "正文"},
		{"<tool_call>search</tool_call>正文", "正文"},
	}
	for _, c := range cases {
		if got := stripToolMarkers(c.in); got != c.want {
			t.Fatalf("stripToolMarkers(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAIThinkingPayload(t *testing.T) {
	var got map[string]any
	s, closeSrv := agentTestServer(t, func(_ int, payload map[string]any) map[string]any {
		got = payload
		return chatReply("ok")
	})
	defer closeSrv()
	s.cfg.AI.Chat.Thinking = true
	if _, _, err := s.aiChatCompletion(context.Background(), map[string]any{
		"model": "x", "messages": []any{},
	}); err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if _, ok := got["chat_template_kwargs"]; !ok {
		t.Fatal("thinking param missing from upstream payload")
	}
}

func TestAIJobAgentEmptyResponse(t *testing.T) {
	s, closeSrv := agentTestServer(t, func(_ int, _ map[string]any) map[string]any {
		return chatReply("")
	})
	defer closeSrv()

	_, _, err := s.aiChatAgent(context.Background(), "请汇总", []string{"agent_echo"},
		pipeline.Definition{}, pipeline.NewRunContext(1, 2))
	if err == nil || !strings.Contains(err.Error(), "empty model response") {
		t.Fatalf("err = %v, want empty model response", err)
	}
}
