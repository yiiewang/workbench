package server

// 临时诊断：把真实网关的抓包（/tmp/hy4_full.txt）喂给 forwardAIStream，
// 验证解析层是否能还原工具调用。

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestReplayRealGatewayStream(t *testing.T) {
	raw, err := os.ReadFile("/tmp/hy4_full.txt")
	if err != nil {
		t.Skip("no capture file")
	}
	reader := bufio.NewReader(strings.NewReader(string(raw)))
	var out flushBuffer
	res, alive := forwardAIStream(&out, reader, nil)
	if !alive {
		t.Fatal("reported disconnected")
	}
	t.Logf("Content=%q", res.Content)
	t.Logf("FinishReason=%q", res.FinishReason)
	t.Logf("ToolCalls=%+v", res.ToolCalls)
	t.Logf("Usage=%+v", res.Usage)
	if res.FinishReason != "tool_calls" || len(res.ToolCalls) != 1 {
		t.Fatalf("tool call lost: finish=%q calls=%d", res.FinishReason, len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Function.Name != "write_file" || tc.ID == "" {
		t.Fatalf("bad tool call: %+v", tc)
	}
}
