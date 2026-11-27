package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yiiewang/workbench/internal/schedule"
)

func testFormatNotifier(format BodyFormatter) *EmailNotifier {
	return &EmailNotifier{siteURL: "https://wb.example.com", format: format}
}

func jobNotifyEvent(body string) schedule.UserNotifyEvent {
	return schedule.UserNotifyEvent{
		UserID:   1,
		Subject:  "[定时任务] 每日工作总结",
		Body:     body,
		UserName: "艺",
		JobName:  "每日工作总结",
	}
}

func TestNotifyUserBodyAIFormat(t *testing.T) {
	n := testFormatNotifier(func(_ context.Context, subject, body, siteURL string) (string, error) {
		if !strings.Contains(body, "原始正文") {
			t.Errorf("formatter got wrong body: %q", body)
		}
		if siteURL != "https://wb.example.com" || !strings.Contains(subject, "每日工作总结") {
			t.Errorf("formatter got wrong context: %q / %q", subject, siteURL)
		}
		return `<h3>建议</h3><p>优先处理 <b>逾期项</b>，` +
			`<a href="https://wb.example.com/todo">看板</a>，` +
			`<a href="https://evil.example.com">外链</a></p>`, nil
	})
	text, html := n.notifyUserBody(context.Background(), jobNotifyEvent("原始正文 Markdown"),
		"WORKBENCH · AI 定时任务", "每日工作总结", "", "管理定时任务")

	for _, want := range []string{"建议", "逾期项", "看板"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q: %q", want, text)
		}
	}
	for _, want := range []string{"艺，你好！", "每日工作总结", "Workbench 团队", "<h3", "<b>逾期项</b>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q", want)
		}
	}
	if strings.Contains(html, "evil.example.com") {
		t.Fatal("external link survived sanitizer")
	}
	if !strings.Contains(html, `href="https://wb.example.com/todo"`) {
		t.Fatal("same-host link lost")
	}
}

func TestNotifyUserBodyFallback(t *testing.T) {
	n := testFormatNotifier(func(_ context.Context, _, _, _ string) (string, error) {
		return "", errors.New("upstream down")
	})
	text, html := n.notifyUserBody(context.Background(), jobNotifyEvent("原始正文"),
		"WORKBENCH · AI 定时任务", "每日工作总结", "", "管理定时任务")
	if !strings.Contains(html, "WORKBENCH · AI 定时任务") || strings.Contains(html, "艺，你好！") {
		t.Fatalf("should fall back to card template: %s", html)
	}
	if !strings.Contains(text, "原始正文") {
		t.Fatalf("fallback text lost: %q", text)
	}
}

func TestNotifyUserBodyShortNoticeSkipsAI(t *testing.T) {
	called := false
	n := testFormatNotifier(func(_ context.Context, _, _, _ string) (string, error) {
		called = true
		return "<p>x</p>", nil
	})
	ev := schedule.UserNotifyEvent{UserID: 1, Subject: "提醒", Body: "买牛奶"}
	text, html := n.notifyUserBody(context.Background(), ev,
		"WORKBENCH · 用户通知", "提醒", "", "打开 Workbench")
	if called {
		t.Fatal("short notice should not invoke AI formatter")
	}
	if !strings.Contains(html, "WORKBENCH · 用户通知") || !strings.Contains(text, "买牛奶") {
		t.Fatalf("short notice body mismatch: %q / %s", text, html)
	}
}

func TestNotifyUserBodyScriptSanitized(t *testing.T) {
	n := testFormatNotifier(func(_ context.Context, _, _, _ string) (string, error) {
		return `<p>正文</p><script>alert(1)</script><img src="https://evil.example.com/p.png">`, nil
	})
	_, html := n.notifyUserBody(context.Background(), jobNotifyEvent("原始正文"),
		"WORKBENCH · AI 定时任务", "每日工作总结", "", "管理定时任务")
	for _, bad := range []string{"<script", "alert", "<img", "evil.example.com"} {
		if strings.Contains(html, bad) {
			t.Fatalf("unsafe %q survived: %s", bad, html)
		}
	}
	if !strings.Contains(html, "正文") {
		t.Fatalf("text lost: %s", html)
	}
}
