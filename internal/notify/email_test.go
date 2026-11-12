package notify

// EmailNotifier 单测：邮件内容组装与 buildMail（离线，不发真实邮件）。

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/schedule"
)

func TestDueMailContent(t *testing.T) {
	ev := schedule.TaskDueEvent{
		Task:        db.TaskItem{ID: "t1", Title: "写周报", Due: "2026-11-11", Status: "progress", Priority: "high", Progress: 40},
		DueDate:     "2026-11-11",
		OverdueDays: 0,
	}
	subject, body := dueMailContent(ev)
	if !strings.Contains(subject, "写周报") || !strings.Contains(subject, "今天截止") {
		t.Fatalf("subject = %q", subject)
	}
	if !strings.Contains(body, "截止日期：2026-11-11") || !strings.Contains(body, "写周报") {
		t.Fatalf("body = %q", body)
	}

	ev.OverdueDays = 3
	subject, _ = dueMailContent(ev)
	if !strings.Contains(subject, "已逾期 3 天") {
		t.Fatalf("overdue subject = %q", subject)
	}
}

func TestBuildMail(t *testing.T) {
	msg := buildMail("from@ex.com", "Workbench Robot", "to@ex.com", "主题", "正文内容")
	// 必备头 + base64 编码的主题与正文
	for _, want := range []string{
		"From: Workbench Robot <from@ex.com>",
		"To: <to@ex.com>",
		"Subject: =?UTF-8?B?",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("mail missing %q:\n%s", want, msg)
		}
	}
	// encoded-word 必须完整闭合：Subject 行不能以未闭合的编码词结尾
	// （此前的缺陷：b64() 尾随换行混入 encoded-word，收件方无法解码原样显示）
	if !strings.Contains(msg, "?=\r\nDate:") {
		t.Errorf("subject encoded-word not closed before Date header:\n%s", msg)
	}
	// base64 行长不超过 76+2(CRLF)
	for _, line := range strings.Split(msg, "\r\n") {
		if len(line) > 78 {
			t.Errorf("base64 line too long (%d): %q", len(line), line[:40])
		}
	}

	// 中文显示名 → RFC 2047 编码
	msg = buildMail("from@ex.com", "工作台机器人", "to@ex.com", "s", "b")
	if !strings.Contains(msg, "From: =?UTF-8?B?") {
		t.Errorf("non-ASCII display name should be RFC2047 encoded:\n%s", msg)
	}
}

// 超长主题：多段 encoded-word + 空格续行折叠，每段完整闭合、每行 ≤75 字符
func TestEncodeWord_LongSubject(t *testing.T) {
	long := strings.Repeat("这是很长的邮件主题内容。", 30) // 420 字，编码必超单行上限
	enc := encodeWord(long)
	if strings.Contains(enc, "\r\n") {
		for _, line := range strings.Split(enc, "\r\n") {
			if !strings.HasPrefix(line, " ") && strings.Contains(line, "\r") {
				t.Fatalf("continuation line must start with space: %q", line[:40])
			}
			if len(line) > 76 {
				t.Errorf("folded line too long (%d)", len(line))
			}
			if !strings.Contains(line, "?=") && !strings.HasPrefix(line, " ") {
				t.Fatalf("unclosed encoded-word fragment: %q", line[:40])
			}
		}
	}
	// 每个分段都闭合
	if n := strings.Count(enc, "?="); n != strings.Count(enc, "=?UTF-8?B?") {
		t.Fatalf("encoded-word open/close mismatch: %s", enc[:80])
	}
	// 中文不被切断：解码所有段拼回应还原原文
	var decoded strings.Builder
	for _, seg := range strings.Split(enc, "\r\n ") {
		b64part := strings.TrimSuffix(strings.TrimPrefix(seg, "=?UTF-8?B?"), "?=")
		raw, err := base64.StdEncoding.DecodeString(b64part)
		if err != nil {
			t.Fatalf("segment not valid base64: %v (%q)", err, b64part[:30])
		}
		decoded.Write(raw)
	}
	if decoded.String() != long {
		t.Fatal("decoded subject does not round-trip")
	}
}

func TestB64(t *testing.T) {
	out := b64(strings.Repeat("x", 200))
	lines := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
	if len(lines) < 3 {
		t.Fatalf("long input should be folded, got %d lines", len(lines))
	}
}
