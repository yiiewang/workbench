package server

// AI Agent 流式转发/工具调用解析的单元测试：
// 覆盖 tool_calls 分片累积（id/name 首片携带、arguments 逐片拼接）、
// 多工具按 index 排序、data: [DONE] 抑制、普通内容增量透传。

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

// flushBuffer 实现流式输出接口，记录写入内容（模拟 iris ResponseWriter）
type flushBuffer struct{ bytes.Buffer }

func (b *flushBuffer) Flush() {}

// feedStream 把若干 SSE data 行喂给 forwardAIStream（逐行以 \n 结尾，模拟上游）
func feedStream(t *testing.T, lines []string) (aiRoundResult, string) {
	t.Helper()
	src := strings.Join(lines, "\n") + "\n"
	reader := bufio.NewReader(strings.NewReader(src))
	var out flushBuffer
	res, alive := forwardAIStream(&out, reader)
	if !alive {
		t.Fatal("forwardAIStream reported client disconnected")
	}
	return res, out.String()
}

func TestForwardAIStream_PlainContent(t *testing.T) {
	res, out := feedStream(t, []string{
		`data: {"choices":[{"delta":{"content":"你好"}}]}`,
		`data: {"choices":[{"delta":{"content":"，世界"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	})
	if res.Content != "你好，世界" {
		t.Fatalf("content = %q, want 你好，世界", res.Content)
	}
	if res.FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", res.FinishReason)
	}
	if len(res.ToolCalls) != 0 {
		t.Fatalf("unexpected tool calls: %+v", res.ToolCalls)
	}
	// 透传内容保留、[DONE] 被抑制（由调用方最终轮统一补发）
	if !strings.Contains(out, `"content":"你好"`) {
		t.Fatalf("forwarded output missing content delta: %q", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Fatalf("forwarded output must not contain [DONE]: %q", out)
	}
}

func TestForwardAIStream_ToolCallFragments(t *testing.T) {
	res, _ := feedStream(t, []string{
		// 第一个工具调用：id/name 在首片，arguments 分三片到达
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"list_dir","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"/\"}"}}]}}]}`,
		// 第二个工具调用与第一个交错到达
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/a.md\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	})

	if res.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", res.FinishReason)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2: %+v", len(res.ToolCalls), res.ToolCalls)
	}
	a, b := res.ToolCalls[0], res.ToolCalls[1]
	if a.ID != "call_a" || a.Function.Name != "list_dir" || a.Function.Arguments != `{"path":"/"}` {
		t.Fatalf("tool call[0] mismatch: %+v", a)
	}
	if a.Type != "function" {
		t.Fatalf("tool call[0].type = %q, want function", a.Type)
	}
	if b.ID != "call_b" || b.Function.Name != "read_file" || b.Function.Arguments != `{"path":"/a.md"}` {
		t.Fatalf("tool call[1] mismatch: %+v", b)
	}
}

func TestForwardAIStream_ClientDisconnect(t *testing.T) {
	// 写入立即失败 → alive=false，立即停止
	reader := bufio.NewReader(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n"))
	_, alive := forwardAIStream(errWriter{}, reader)
	if alive {
		t.Fatal("expected alive=false on write error")
	}
}

// errWriter 模拟客户端已断开：所有写入均失败
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
func (errWriter) Flush()                      {}

func TestValidateAIMessages_ToolRoles(t *testing.T) {
	cases := []struct {
		name    string
		msgs    []aiMessage
		wantErr bool
	}{
		{"tool message ok", []aiMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []aiToolCall{{ID: "c1", Type: "function", Function: aiToolCallFunction{Name: "list_dir", Arguments: "{}"}}}},
			{Role: "tool", ToolCallID: "c1", Content: `{"entries":[]}`},
		}, false},
		{"assistant tool_calls allows empty content", []aiMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []aiToolCall{{ID: "c1", Type: "function", Function: aiToolCallFunction{Name: "list_dir", Arguments: "{}"}}}},
		}, false},
		{"assistant empty content without tool_calls rejected", []aiMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "  "},
		}, true},
		{"tool message missing tool_call_id rejected", []aiMessage{
			{Role: "user", Content: "hi"},
			{Role: "tool", Content: "result"},
		}, true},
		{"tool_call missing name rejected", []aiMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []aiToolCall{{ID: "c1", Type: "function"}}},
		}, true},
	}
	for _, c := range cases {
		err := validateAIMessages(c.msgs)
		if (err != "") != c.wantErr {
			t.Errorf("%s: err = %q, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}
