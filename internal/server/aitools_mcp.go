package server

// MCP 外部工具桥接：启动时把各服务的 tools/list 转成与内置 aiTools 同构的
// 静态表，调用时经同一 execAITool / runToolWithApproval 链路执行。
// 不维护会话状态（每次 tools/call 独立 POST），外部工具一律需要用户审批。

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/mcpclient"
)

// mcpInitTimeout 单个 MCP 服务握手 + 拉取工具列表的超时
const mcpInitTimeout = 10 * time.Second

// mcpInvalidName 函数名合法字符之外的字符统一替换为下划线
var mcpInvalidName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// mcpToolName 外部工具命名空间：mcp_<server>_<tool>，避免与内置工具冲突
func mcpToolName(server, tool string) string {
	clean := func(s string) string { return mcpInvalidName.ReplaceAllString(s, "_") }
	return "mcp_" + clean(server) + "_" + clean(tool)
}

// allTools 内置 + 运行时合成的编排/触发工具 + MCP 工具（顺序稳定）
func (s *Server) allTools() []aiTool {
	tools := make([]aiTool, 0, len(aiTools)+2+len(s.mcpTools))
	tools = append(tools, aiTools...)
	tools = append(tools, pipelineTool(), runJobTool())
	return append(tools, s.mcpTools...)
}

// aiToolByName 按名称查找工具（内置优先，其次编排工具，最后 MCP）
func (s *Server) aiToolByName(name string) *aiTool {
	tools := s.allTools()
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// initMCPTools 启动时逐个服务握手 + tools/list，转成静态工具表。
// 配置缺 name/url 直接报错；服务不可达只记日志并跳过，不影响 AI 主流程。
func (s *Server) initMCPTools() error {
	for _, cfg := range s.cfg.AI.MCP.Servers {
		if cfg.Name == "" || cfg.URL == "" {
			return fmt.Errorf("ai.mcp.servers: name and url are required")
		}
		c := mcpclient.New(cfg.Name, cfg.URL, cfg.Headers)
		ctx, cancel := context.WithTimeout(context.Background(), mcpInitTimeout)
		tools, err := c.ListTools(ctx)
		cancel()
		if err != nil {
			slog.Warn("mcp server unavailable, skip", "name", cfg.Name, "err", err)
			continue
		}
		for _, t := range tools {
			params := t.InputSchema
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			s.mcpTools = append(s.mcpTools, aiTool{
				Name:         mcpToolName(cfg.Name, t.Name),
				Desc:         "[MCP: " + cfg.Name + "] " + t.Description,
				Params:       params,
				NeedApproval: true,
				Exec:         mcpToolExec(c, t.Name),
			})
		}
		slog.Info("mcp server tools loaded", "name", cfg.Name, "tools", len(tools))
	}
	return nil
}

// mcpToolExec 把 tools/call 包装成内置工具执行器；结果以 {"result": text}
// 回传模型，沿用现有 32KB 结果限长与审批链路。
func mcpToolExec(c *mcpclient.Client, tool string) func(*Server, iris.Context, map[string]any) any {
	return func(s *Server, ctx iris.Context, args map[string]any) any {
		text, err := c.CallTool(ctx.Request().Context(), tool, args)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return map[string]any{"result": text}
	}
}
