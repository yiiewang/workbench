package server

// handleAIChat 端到端回归基线：SSE 单轮直答与一轮工具调用。
// 覆盖上游 payload 组装、工具轮、usage/[DONE] 收尾与后台 run 落库。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// sseLines 把多行 SSE 拼成一段响应体
func sseLines(lines ...string) string {
	return strings.Join(lines, "\n\n") + "\n\n"
}

// readTestBody 读取 httptest 抓到的原始响应体
func readTestBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// createTestOrgUser 建一个组织 + 用户，返回整数 id
func createTestOrgUser(t *testing.T, d *db.DB) (orgID, userID int64) {
	t.Helper()
	ctx := context.Background()
	hash, err := HashPassword("p")
	if err != nil {
		t.Fatal(err)
	}
	orgID, err = d.EnsureOrg(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	userID, err = d.UpsertUser(ctx, orgID, "alice", hash, db.RoleIDUser)
	if err != nil {
		t.Fatal(err)
	}
	return orgID, userID
}

func TestHandleAIChat_SingleRound(t *testing.T) {
	var payloads []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		payloads = append(payloads, p)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLines(
			`data: {"choices":[{"delta":{"content":"你好"},"finish_reason":"stop"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
			`data: [DONE]`,
		))
	}))
	defer upstream.Close()

	e, d, secret, _ := setupTestServerWith(t, func(cfg *config.Config) {
		cfg.AI.Chat.Gateways = []config.ChatGateway{{ID: "g1", BaseURL: upstream.URL, APIKey: "k", Model: "m1"}}
	})
	orgID, userID := createTestOrgUser(t, d)
	auth := authHeader(GenerateToken(userID, secret, 30))

	resp := e.POST("/api/ai/chat").WithHeader("Authorization", auth).
		WithJSON(map[string]any{
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		}).Expect().Status(http.StatusOK).Raw()
	body := readTestBody(t, resp)

	for _, want := range []string{"event: run_id", "你好", "event: usage", "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q:\n%s", want, body)
		}
	}
	if len(payloads) != 1 {
		t.Fatalf("upstream called %d times, want 1", len(payloads))
	}
	p := payloads[0]
	if p["model"] != "m1" || p["stream"] != true {
		t.Fatalf("unexpected upstream payload: %+v", p)
	}
	if _, hasTools := p["tools"]; hasTools {
		t.Fatalf("tools must not be sent when disabled: %+v", p)
	}
	msgs, _ := p["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v, want the single user message (no system prompt)", msgs)
	}

	runs, err := d.ListAIRuns(context.Background(), orgID, userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "done" || !strings.Contains(runs[0].Result, "你好") {
		t.Fatalf("run not persisted as done: %+v", runs)
	}
}

func TestHandleAIChat_ToolRound(t *testing.T) {
	var payloads []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		payloads = append(payloads, p)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(payloads) == 1 {
			io.WriteString(w, sseLines(
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"list_tasks","arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			))
			return
		}
		io.WriteString(w, sseLines(
			`data: {"choices":[{"delta":{"content":"没有任务"},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		))
	}))
	defer upstream.Close()

	e, d, secret, _ := setupTestServerWith(t, func(cfg *config.Config) {
		cfg.AI.Chat.Gateways = []config.ChatGateway{{ID: "g1", BaseURL: upstream.URL, APIKey: "k", Model: "m1"}}
		cfg.AI.Chat.Tools = true
	})
	orgID, userID := createTestOrgUser(t, d)
	auth := authHeader(GenerateToken(userID, secret, 30))

	resp := e.POST("/api/ai/chat").WithHeader("Authorization", auth).
		WithJSON(map[string]any{
			"messages": []map[string]any{{"role": "user", "content": "我有哪些任务"}},
		}).Expect().Status(http.StatusOK).Raw()
	body := readTestBody(t, resp)

	for _, want := range []string{"event: tool_call", `"name":"list_tasks"`, "event: tool_result", "没有任务", "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q:\n%s", want, body)
		}
	}
	if len(payloads) != 2 {
		t.Fatalf("upstream called %d times, want 2 (tool round + answer)", len(payloads))
	}
	first := payloads[0]
	if _, hasTools := first["tools"]; !hasTools {
		t.Fatalf("first payload must carry tools: %+v", first)
	}
	msgs, _ := first["messages"].([]any)
	if len(msgs) == 0 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("Agent mode must inject system prompt first: %+v", msgs)
	}
	second, _ := payloads[1]["messages"].([]any)
	var sawAssistantCall, sawToolResult, sawBudget bool
	for _, m := range second {
		mm := m.(map[string]any)
		if mm["role"] == "assistant" && mm["tool_calls"] != nil {
			sawAssistantCall = true
		}
		if mm["role"] == "tool" && mm["tool_call_id"] == "call_1" {
			sawToolResult = true
		}
		if content, _ := mm["content"].(string); strings.Contains(content, "已调用 1 次工具") {
			sawBudget = true
		}
	}
	if !sawAssistantCall || !sawToolResult {
		t.Fatalf("second payload must carry assistant tool_calls + tool result: %+v", second)
	}
	if !sawBudget {
		t.Fatalf("second payload must carry tool budget hint with used count: %+v", second)
	}

	runs, err := d.ListAIRuns(context.Background(), orgID, userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "done" || !strings.Contains(runs[0].Result, "没有任务") {
		t.Fatalf("run not persisted as done: %+v", runs)
	}
}
