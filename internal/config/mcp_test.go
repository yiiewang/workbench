package config

import "testing"

func TestMCPServersMerge(t *testing.T) {
	cfg := DefaultConfig()
	src := &Config{}
	src.AI.MCP.Servers = []MCPServerConfig{{
		Name:    "demo",
		URL:     "https://mcp.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${MCP_DEMO_TOKEN}"},
	}}
	mergeConfig(cfg, src)
	if len(cfg.AI.MCP.Servers) != 1 || cfg.AI.MCP.Servers[0].Name != "demo" {
		t.Fatalf("mcp servers not merged: %+v", cfg.AI.MCP.Servers)
	}
}
