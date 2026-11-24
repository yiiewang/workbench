package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

type mockCall struct {
	method  string
	session string
	auth    string
}

// newMockMCP 模拟无状态 MCP 服务：支持 JSON / SSE 两种响应，记录请求头与方法。
func newMockMCP(t *testing.T, sse bool) (*httptest.Server, *[]mockCall) {
	var mu sync.Mutex
	calls := &[]mockCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		mu.Lock()
		*calls = append(*calls, mockCall{
			method:  req.Method,
			session: r.Header.Get("Mcp-Session-Id"),
			auth:    r.Header.Get("Authorization"),
		})
		mu.Unlock()
		if r.Header.Get("MCP-Protocol-Version") != protocolVersion {
			t.Errorf("MCP-Protocol-Version header missing")
		}

		var resp map[string]any
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": protocolVersion}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "echo text",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
				}},
			}}
		case "tools/call":
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "hello"}},
			}}
		default:
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}}
		}
		data, _ := json.Marshal(resp)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: " + string(data) + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	return srv, calls
}

func TestListToolsAndCall(t *testing.T) {
	_ = os.Setenv("MCP_TEST_TOKEN", "secret")
	defer os.Unsetenv("MCP_TEST_TOKEN")

	for _, sse := range []bool{false, true} {
		name := "json"
		if sse {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			srv, calls := newMockMCP(t, sse)
			defer srv.Close()
			c := New("demo", srv.URL, map[string]string{"Authorization": "Bearer ${MCP_TEST_TOKEN}"})

			tools, err := c.ListTools(context.Background())
			if err != nil {
				t.Fatalf("ListTools: %v", err)
			}
			if len(tools) != 1 || tools[0].Name != "echo" || tools[0].InputSchema == nil {
				t.Fatalf("unexpected tools: %+v", tools)
			}
			text, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "hi"})
			if err != nil || text != "hello" {
				t.Fatalf("CallTool = %q, %v", text, err)
			}

			want := []string{"initialize", "notifications/initialized", "tools/list", "tools/call"}
			if len(*calls) != len(want) {
				t.Fatalf("calls = %+v", *calls)
			}
			for i, m := range want {
				if (*calls)[i].method != m {
					t.Errorf("call %d = %q, want %q", i, (*calls)[i].method, m)
				}
			}
			// session 只在握手后透传；headers 中的 ${ENV} 已展开
			for _, call := range (*calls)[2:] {
				if call.session != "sess-1" {
					t.Errorf("session not forwarded: %+v", call)
				}
			}
			if (*calls)[0].auth != "Bearer secret" {
				t.Errorf("header env not expanded: %q", (*calls)[0].auth)
			}
		})
	}
}

func TestRPCErrors(t *testing.T) {
	t.Run("http status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()
		if _, err := New("bad", srv.URL, nil).ListTools(context.Background()); err == nil {
			t.Fatal("expected HTTP error")
		}
	})
	t.Run("json-rpc error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`))
		}))
		defer srv.Close()
		_, err := New("bad", srv.URL, nil).ListTools(context.Background())
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("expected json-rpc error, got %v", err)
		}
	})
	t.Run("tool isError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"boom"}],"isError":true}}`))
		}))
		defer srv.Close()
		_, err := New("bad", srv.URL, nil).CallTool(context.Background(), "echo", nil)
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected tool error, got %v", err)
		}
	})
}
