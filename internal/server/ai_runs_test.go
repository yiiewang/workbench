package server

// 后台对话运行：镜像 writer 断连降级与事件快照的单测。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kataras/iris/v12"
)

func TestAIRunWriter_DisconnectKeepsLoopAlive(t *testing.T) {
	run := &aiRunState{}
	w := &aiRunWriter{w: errWriter{}, state: run, alive: true}

	first := "data: hi\n\n"
	if n, err := io.WriteString(w, first); err != nil || n != len(first) {
		t.Fatalf("first write = (%d, %v), want success so the loop continues", n, err)
	}
	if w.alive || !run.disconnected {
		t.Fatal("write failure must mark writer/run disconnected")
	}
	later := "data: later\n\n"
	if n, err := io.WriteString(w, later); err != nil || n != len(later) {
		t.Fatalf("post-disconnect write = (%d, %v), want swallowed success", n, err)
	}
	w.Flush()
}

func TestAIRunWriter_Normal(t *testing.T) {
	var buf bytes.Buffer
	run := &aiRunState{}
	w := &aiRunWriter{w: &buf, state: run, alive: true}
	if _, err := io.WriteString(w, "hello"); err != nil || buf.String() != "hello" {
		t.Fatalf("normal write failed: buf=%q err=%v", buf.String(), err)
	}
	if !w.alive || run.disconnected {
		t.Fatal("normal write must not mark disconnected")
	}
}

func TestAIRunState_Events(t *testing.T) {
	r := &aiRunState{}
	r.addEvent("tool_call", map[string]any{"name": "reindex_files"})
	r.content.WriteString("完成")
	b, err := json.Marshal(r.events)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["event"] != "tool_call" || r.content.String() != "完成" {
		t.Fatalf("unexpected snapshot: events=%s content=%q", b, r.content.String())
	}
}

// 断连后写类工具直接跳过审批，不再阻塞后台循环
func TestRunToolWithApproval_DisconnectedSkipsWriteTool(t *testing.T) {
	s := &Server{}
	app := iris.New()
	var got any
	app.Post("/t", func(ctx iris.Context) {
		run := &aiRunState{disconnected: true}
		ctx.Values().Set(aiRunStateKey, run)
		got = s.runToolWithApproval(ctx, &flushBuffer{}, aiToolCall{
			ID:       "call-1",
			Function: aiToolCallFunction{Name: "write_file", Arguments: `{"path":"/a.md","content":"x"}`},
		})
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/t", nil))
	res, ok := got.(map[string]any)
	if !ok || res["error"] != "用户已断开连接，写操作已跳过" {
		t.Fatalf("write tool should be skipped after disconnect, got %+v", got)
	}
}
