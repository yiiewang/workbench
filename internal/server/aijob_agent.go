package server

// 定时任务的非流式 Agent 循环：模型只能看到 job 白名单里的工具，
// 白名单同时是该步的静态预授权（定时执行无界面审批）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/yiiewang/workbench/internal/pipeline"
)

// aiChatCompletion 非流式调用上游 /chat/completions，返回回复消息与 finish_reason。
func (s *Server) aiChatCompletion(ctx context.Context, payload map[string]any) (aiMessage, string, error) {
	if !s.cfg.AI.Enabled() {
		return aiMessage{}, "", fmt.Errorf("AI not configured (ai.api_key / AI_BASE_URL)")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return aiMessage{}, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeAIEndpoint(s.cfg.AI.Chat.BaseURL), bytes.NewReader(body))
	if err != nil {
		return aiMessage{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.AI.Chat.APIKey)
	req.Header.Set("X-Model-Request-Id", uuid.NewString())
	resp, err := aiClient.Do(req)
	if err != nil {
		return aiMessage{}, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return aiMessage{}, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return aiMessage{}, "", fmt.Errorf("upstream %s: %s", resp.Status, truncateStr(string(raw), 200))
	}
	var out struct {
		Choices []struct {
			Message      aiMessage `json:"message"`
			FinishReason string    `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return aiMessage{}, "", fmt.Errorf("unexpected upstream response")
	}
	return out.Choices[0].Message, out.Choices[0].FinishReason, nil
}

// 上游网关偶发把工具调用以 <tool_calls:xxxx>…</tool_calls:xxxx> 的文本形式
// 回传给模型，模型会把它当答复正文，最终原样进入邮件。
var (
	toolMarkerBlockRE = regexp.MustCompile(`(?s)<tool_calls?(:[0-9a-zA-Z]+)?>.*?</tool_calls?(:[0-9a-zA-Z]+)?>`)
	toolMarkerTagRE   = regexp.MustCompile(`</?tool_calls?(:[0-9a-zA-Z]+)?>`)
	toolMarkerTailRE  = regexp.MustCompile(`(?s)<tool_calls?(:[0-9a-zA-Z]+)?>.*$`)
)

// stripToolMarkers 清理误当正文输出的工具调用标记：
// 先删完整块，再删未闭合的残留尾巴，最后清掉嵌套块留下的孤立标签。
func stripToolMarkers(text string) string {
	if !strings.Contains(text, "<tool_call") {
		return text
	}
	text = toolMarkerBlockRE.ReplaceAllString(text, "")
	text = toolMarkerTailRE.ReplaceAllString(text, "")
	text = toolMarkerTagRE.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// aiChatAgent 按白名单驱动非流式工具循环，最多 aiMaxToolRounds 轮；
// 最后一轮不带 tools，强制模型用已有结果给出文本回答。
// 返回值带完整 reasoning：定时任务没有流式通道，思考过程只能随响应一次落库。
// 空正文视为失败——否则 runPipeline 记 ok，邮件只剩步骤列表（实测 job 9）。
func (s *Server) aiChatAgent(ctx context.Context, prompt string, names []string, def pipeline.Definition, rctx *pipeline.RunContext) (string, string, error) {
	tools := s.scheduledAgentTools(names)
	if len(tools) == 0 {
		return "", "", fmt.Errorf("no usable tools in whitelist")
	}
	stepDef := def
	stepDef.AllowWrite = mergeAllowWrite(def.AllowWrite, names)
	msgs := []aiMessage{
		{Role: "system", Content: "你是 Workbench 的定时任务助手。可以调用已授权的工具完成任务，工具结果会回传给你。\n" +
			"思考与执行协议：\n" +
			"1. 每轮只推进一个步骤；已确认的事实与已得到的结果不要重复复述，已排除的方案不再重提。\n" +
			"2. 信息足够时直接给出最终回答；不要为确认细节反复检索同一问题，检索无命中即停止并说明未取得。\n" +
			"3. 只写有依据的事实，不要编造未取得的信息。\n" +
			"4. 正文即交付物：不要输出工具调用标记、检索过程或「我将要…」之类的说明。"},
		{Role: "user", Content: prompt},
	}
	toolCallsUsed := 0
	var reasoning strings.Builder
	for round := 0; ; round++ {
		forceAnswer := round >= aiMaxToolRounds
		// 工具轮之间显式告知剩余轮次：该路径没有对话侧的 UI 提示，
		// 模型容易在 5 轮里反复换 query 拼数据。
		payloadMsgs := msgs
		if !forceAnswer && round > 0 {
			payloadMsgs = append(append([]aiMessage{}, msgs...), aiMessage{Role: "system", Content: fmt.Sprintf(
				"工具轮次预算：剩余 %d 轮（已调用 %d 次工具）。信息足够时直接给出最终回答。",
				aiMaxToolRounds-round, toolCallsUsed)})
		}
		payload := map[string]any{
			"model":    s.cfg.AI.Chat.Model,
			"messages": payloadMsgs,
		}
		if !forceAnswer {
			payload["tools"] = aiToolsPayload(tools)
			payload["tool_choice"] = "auto"
		}
		msg, finish, err := s.aiChatCompletion(ctx, payload)
		if err != nil {
			return "", reasoning.String(), err
		}
		reasoning.WriteString(msg.ReasoningContent)
		if forceAnswer || finish != "tool_calls" || len(msg.ToolCalls) == 0 {
			text := stripToolMarkers(msg.Content)
			if strings.TrimSpace(text) == "" {
				if r := strings.TrimSpace(reasoning.String()); r != "" {
					return "", reasoning.String(), fmt.Errorf("empty model response (reasoning %d chars)", len([]rune(r)))
				}
				return "", reasoning.String(), fmt.Errorf("empty model response")
			}
			return text, reasoning.String(), nil
		}
		msgs = append(msgs, aiMessage{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls})
		for _, call := range msg.ToolCalls {
			toolCallsUsed++
			result := s.execAgentTool(ctx, call, names, stepDef, rctx)
			resultJSON, _ := json.Marshal(result)
			msgs = append(msgs, aiMessage{Role: "tool", ToolCallID: call.ID,
				Content: summarizeToolResult(call.Function.Name, resultJSON) + string(resultJSON)})
		}
	}
}

// scheduledAgentTools 按白名单顺序取工具定义；模型看不到清单以外的能力。
func (s *Server) scheduledAgentTools(names []string) []aiTool {
	all := s.allTools()
	out := make([]aiTool, 0, len(names))
	for _, name := range names {
		for i := range all {
			if all[i].Name == name {
				out = append(out, all[i])
				break
			}
		}
	}
	return out
}

// execAgentTool 执行模型发起的一次工具调用；失败以 {"error":...} 回灌模型，
// 不中断整次任务，与对话侧工具异常的处理方式一致。
func (s *Server) execAgentTool(ctx context.Context, call aiToolCall, names []string, def pipeline.Definition, rctx *pipeline.RunContext) any {
	name := call.Function.Name
	if !containsString(names, name) {
		return map[string]any{"error": "tool not allowed: " + name}
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return map[string]any{"error": "invalid tool arguments: " + err.Error()}
	}
	res, err := s.execPipelineTool(ctx, name, args, def, rctx)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return res
}
