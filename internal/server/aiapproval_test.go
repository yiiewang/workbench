package server

// AI 写工具审批链路（Human-in-the-loop）的端到端测试：
// mock 上游模型发起 write_file 工具调用 → SSE 推 tool_call / approval_request
// → 测试侧代表用户提交审批 → approval_resolved(outcome=approved) → 工具真实
// 执行 → tool_result 携带结果 → 上游第二轮给出最终回答。
// 覆盖完整事件序列与文件落盘，验证前后端契约（前端依赖的事件顺序与字段）。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// mockAIUpstream 模拟 OpenAI 兼容 /chat/completions 流式上游：
// 第一轮返回 write_file 工具调用，之后每轮返回固定文本收尾。
func mockAIUpstream(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeSSE := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			// 第一轮：模型请求调用 write_file（参数分两片到达，模拟真实分片）
			writeSSE(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_w1", "type": "function",
					"function": map[string]any{"name": "write_file", "arguments": "{\"path\":\"/hello.txt\","},
				}}},
			}}})
			writeSSE(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index":    0,
					"function": map[string]any{"arguments": `"content":"hi"}`},
				}}},
			}}})
			writeSSE(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{}, "finish_reason": "tool_calls",
			}}})
		} else {
			// 后续轮：工具结果已回传，模型给出最终回答
			writeSSE(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"content": "已写入完成"},
			}}})
			writeSSE(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{}, "finish_reason": "stop",
			}}})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestAIWriteToolApprovalFlow(t *testing.T) {
	upstream, upstreamCalls := mockAIUpstream(t)

	// 构建指向 mock 上游的测试服务（复用 setupTestServerWith 的组装逻辑，
	// 但需要真实监听地址以支持 SSE 流式读取 + 并发审批提交）
	dbDir, err := os.MkdirTemp("", "wbdb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dbDir)
	})
	d, err := db.Open(filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	staticDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.StaticDir = staticDir
	cfg.AI.Chat = config.ChatConfig{
		Gateways: []config.ChatGateway{{
			ID: "test", BaseURL: upstream.URL + "/v1",
			APIKey: "test-key", Model: "mock-model",
		}},
		Tools: true,
	}
	secret := []byte("test-secret-key")
	srv, err := New(d, cfg, secret, filepath.Join(t.TempDir(), "access.log"))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	// iris Application 需先 Build 才能作为 http.Handler 直接服务
	app := srv.App()
	if err := app.Build(); err != nil {
		t.Fatalf("iris build failed: %v", err)
	}
	ts := httptest.NewServer(app)
	t.Cleanup(ts.Close)

	tok := createUser(t, d, secret, "org1", "alice", "pass123")

	// 发起对话（SSE 流式），读到 approval_request 立即代表用户批准
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/ai/chat",
		strings.NewReader(`{"messages":[{"role":"user","content":"请把 hi 写入 hello.txt"}],"qaId":"qa1","sessionId":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(tok))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("chat status = %d, body: %s", resp.StatusCode, body)
	}

	var events []string // 按序记录事件名（data 行归并到其 event）
	dataByEvent := map[string][]string{}
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "event: ") {
				events = append(events, strings.TrimPrefix(line, "event: "))
			} else if strings.HasPrefix(line, "data: ") {
				payload := strings.TrimPrefix(line, "data: ")
				if len(events) > 0 {
					dataByEvent[events[len(events)-1]] = append(dataByEvent[events[len(events)-1]], payload)
				}
				// 读到审批请求：立即代表用户提交批准（另一条 HTTP 连接）
				if len(events) > 0 && events[len(events)-1] == "approval_request" && strings.Contains(payload, `"call_w1"`) {
					ar, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ai/approval",
						strings.NewReader(`{"id":"call_w1","approved":true}`))
					ar.Header.Set("Content-Type", "application/json")
					ar.Header.Set("Authorization", authHeader(tok))
					aresp, aerr := http.DefaultClient.Do(ar)
					if aerr != nil {
						t.Errorf("approval post failed: %v", aerr)
					} else {
						abody, _ := io.ReadAll(io.LimitReader(aresp.Body, 512))
						aresp.Body.Close()
						if aresp.StatusCode != http.StatusOK {
							t.Errorf("approval post status = %d, body: %s", aresp.StatusCode, abody)
						}
					}
				}
			}
		}
		if err != nil {
			break // EOF：流结束
		}
	}

	// ---- 断言：事件序列 ----
	wantSeq := []string{"tool_call", "approval_request", "approval_resolved", "tool_result"}
	pos := 0
	for _, ev := range events {
		if pos < len(wantSeq) && ev == wantSeq[pos] {
			pos++
		}
	}
	if pos != len(wantSeq) {
		t.Fatalf("missing events in SSE stream, matched %d/%d; events: %v; data: %v",
			pos, len(wantSeq), events, dataByEvent)
	}

	// ---- 断言：approval_resolved 为 approved ----
	anyContains := func(list []string, sub string) bool {
		for _, s := range list {
			if strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}
	if !anyContains(dataByEvent["approval_resolved"], `"outcome":"approved"`) {
		t.Fatalf("approval_resolved not approved: %v", dataByEvent["approval_resolved"])
	}

	// ---- 断言：tool_result 携带成功结果 ----
	if !anyContains(dataByEvent["tool_result"], `"ok":true`) {
		t.Fatalf("tool_result not ok: %v", dataByEvent["tool_result"])
	}

	// ---- 断言：文件真实落盘（内容 hi）----
	var found string
	_ = filepath.Walk(staticDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "hello.txt" {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("hello.txt not written under %s", staticDir)
	}
	if b, _ := os.ReadFile(found); string(b) != "hi" {
		t.Fatalf("hello.txt content = %q, want hi", b)
	}

	// ---- 断言：上游经历两轮（工具轮 + 收尾轮）----
	if n := atomic.LoadInt32(upstreamCalls); n != 2 {
		t.Fatalf("upstream calls = %d, want 2", n)
	}
}
