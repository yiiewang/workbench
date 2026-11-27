package server

import (
	"strings"
	"testing"
)

func TestNotifyFormatPrompt(t *testing.T) {
	p := notifyFormatPrompt("[定时任务] 每日总结", "今天完成 3 项", "https://wb.example.com")
	for _, want := range []string{
		"只做版面整理", "不改写、不补充、不删除事实", "允许的标签", "<table>",
		"不要写 style", "同域", "https://wb.example.com/jobs", "外链一律保留为纯文字",
		"不要写任务名", "今天完成 3 项",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	if !strings.Contains(p, "通知主题：[定时任务] 每日总结") {
		t.Fatal("subject not passed to prompt")
	}

	emptySite := notifyFormatPrompt("提醒", "买牛奶", "")
	if !strings.Contains(emptySite, "不要生成任何链接") {
		t.Fatal("missing site should disable link generation")
	}
	if strings.Contains(emptySite, "/jobs") {
		t.Fatal("site-specific link rule leaked into empty-site prompt")
	}
}
