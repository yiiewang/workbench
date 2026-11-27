package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

func TestRenderAIJobNotify(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	job := db.AIJobWithOwner{AIJob: db.AIJob{ID: 9, Name: "每日工作总结", TargetType: aiJobTypeAI}}

	subject, body := s.renderAIJobNotify(job, aiJobRunResult{Output: "<p>正文</p>"}, nil)
	if subject != "[定时任务] 每日工作总结" || body != "<p>正文</p>" {
		t.Fatalf("ok: subject=%q body=%q", subject, body)
	}

	subject, body = s.renderAIJobNotify(job, aiJobRunResult{}, errors.New("boom"))
	if !strings.HasPrefix(subject, "[定时任务失败]") || !strings.Contains(body, "boom") {
		t.Fatalf("failed: subject=%q body=%q", subject, body)
	}

	job.TargetType = aiJobTypeTool
	_, body = s.renderAIJobNotify(job, aiJobRunResult{Output: "工具结果"}, nil)
	if !strings.Contains(body, "```") {
		t.Fatalf("tool result should be wrapped as code block: %q", body)
	}
}

func TestRenderAIJobPromptHTMLContract(t *testing.T) {
	s := &Server{}
	job := db.AIJobWithOwner{AIJob: db.AIJob{
		Name:    "每日工作总结",
		Context: json.RawMessage(`{"tasks":false,"ragTopK":0}`),
	}}
	prompt, err := s.renderAIJobPrompt(context.Background(), job, aiJobTarget{Prompt: "写日报"})
	if err != nil {
		t.Fatalf("render prompt: %v", err)
	}
	for _, want := range []string{"markdown 正文", "不要输出 HTML 标签", "管道表格", "同域", "未取得", "不要输出任何工具调用标记"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing constraint %q in prompt: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "HTML 片段") {
		t.Fatal("old HTML contract survived")
	}
}
