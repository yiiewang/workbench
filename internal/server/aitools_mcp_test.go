package server

import "testing"

func TestMCPToolName(t *testing.T) {
	if got := mcpToolName("my.server", "read file"); got != "mcp_my_server_read_file" {
		t.Fatalf("name = %q", got)
	}
}

func TestMCPToolsMerge(t *testing.T) {
	s := &Server{mcpTools: []aiTool{{
		Name:         "mcp_demo_echo",
		Desc:         "[MCP: demo] echo",
		Params:       map[string]any{"type": "object"},
		NeedApproval: true,
	}}}
	if tool := s.aiToolByName("mcp_demo_echo"); tool == nil || !tool.NeedApproval {
		t.Fatal("mcp tool missing or approval not required")
	}
	if s.aiToolByName("list_dir") == nil {
		t.Fatal("builtin tool missing")
	}
	if s.aiToolByName("missing") != nil {
		t.Fatal("unknown tool should be nil")
	}
	if got := len(s.allTools()); got != len(aiTools)+1 {
		t.Fatalf("allTools len = %d, want %d", got, len(aiTools)+1)
	}

	found := false
	for _, p := range aiToolsPayload(s.allTools()) {
		if fn, ok := p["function"].(map[string]any); ok && fn["name"] == "mcp_demo_echo" {
			found = true
		}
	}
	if !found {
		t.Fatal("mcp tool not in tools payload")
	}
}
