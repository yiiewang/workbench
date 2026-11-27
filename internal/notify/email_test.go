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
	subject, text, html := dueMailContent(ev, "")
	if !strings.Contains(subject, "写周报") || !strings.Contains(subject, "今天截止") {
		t.Fatalf("subject = %q", subject)
	}
	if !strings.Contains(text, "截止日期：2026-11-11") || !strings.Contains(text, "写周报") {
		t.Fatalf("text = %q", text)
	}
	if !strings.Contains(html, "写周报") || !strings.Contains(html, "今天截止") {
		t.Fatalf("html = %q", html)
	}

	ev.OverdueDays = 3
	subject, _, html = dueMailContent(ev, "")
	if !strings.Contains(subject, "已逾期 3 天") {
		t.Fatalf("overdue subject = %q", subject)
	}
	if !strings.Contains(html, "已逾期 <b>3 天</b>") {
		t.Fatalf("overdue html = %q", html)
	}
}

func TestBuildMail(t *testing.T) {
	msg := buildMail("from@ex.com", "Workbench Robot", "to@ex.com", "主题", "正文内容", "<p>正文</p>")
	// 必备头 + multipart 结构
	for _, want := range []string{
		"From: Workbench Robot <from@ex.com>",
		"To: <to@ex.com>",
		"Subject: =?UTF-8?B?",
		"Content-Type: multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("mail missing %q:\n%s", want, msg)
		}
	}
	// multipart 边界闭合
	if !strings.Contains(msg, "\r\n--") {
		t.Error("multipart boundary lines missing")
	}
	// encoded-word 必须完整闭合：Subject 行不能以未闭合的编码词结尾
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
	msg = buildMail("from@ex.com", "工作台机器人", "to@ex.com", "s", "b", "<p>b</p>")
	if !strings.Contains(msg, "From: =?UTF-8?B?") {
		t.Errorf("non-ASCII display name should be RFC2047 encoded:\n%s", msg)
	}
}

// mdToHTML：标题/列表/粗体/转义
func TestMdToHTML(t *testing.T) {
	md := "# 日报\n\n今天完成 **3 项** 任务。\n\n- 修复审批\n- 发布版本\n\n<script>alert(1)</script>"
	html := string(mdToHTML(md, ""))
	// 标题
	if !strings.Contains(html, "font-weight:600") || !strings.Contains(html, "日报") {
		t.Errorf("heading missing: %s", html)
	}
	// 粗体
	if !strings.Contains(html, "<b>3 项</b>") {
		t.Errorf("bold missing: %s", html)
	}
	// 列表
	if !strings.Contains(html, "· 修复审批") {
		t.Errorf("list missing: %s", html)
	}
	// XSS 转义
	if strings.Contains(html, "<script>") {
		t.Errorf("script tag not escaped: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("escaped script missing: %s", html)
	}
}

// mdToHTML：管道表格 + 引用块渲染，分隔行不外泄，单元格内容仍转义
func TestMdToHTML_TableAndQuote(t *testing.T) {
	md := "| 指数 | 涨跌幅 |\n|---|---|\n| 上证指数 | +0.01% |\n| <script>alert(1)</script> | -0.02% |\n\n> 说明：今日为正常交易日。\n> 数据均为收盘后发布。"
	html := string(mdToHTML(md, ""))
	if !strings.Contains(html, "<table") || !strings.Contains(html, "<th") || !strings.Contains(html, "<td") {
		t.Fatalf("table not rendered: %s", html)
	}
	if strings.Contains(html, "|---|") {
		t.Fatalf("separator row leaked: %s", html)
	}
	if !strings.Contains(html, "上证指数") || !strings.Contains(html, "+0.01%") {
		t.Fatalf("table cells missing: %s", html)
	}
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("table cell not escaped: %s", html)
	}
	if !strings.Contains(html, "border-left:3px solid #9aa1ab") || !strings.Contains(html, "说明：今日为正常交易日") {
		t.Fatalf("quote not rendered: %s", html)
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

// 站点地址配置后才渲染跳转按钮与任务深链；未配置/非法地址不产出链接
func TestEmailActionLinks(t *testing.T) {
	ev := schedule.TaskDueEvent{
		Task:    db.TaskItem{ID: "t1", Title: "写周报", Due: "2026-11-11", Status: "progress", Priority: "high"},
		DueDate: "2026-11-11",
	}
	_, text, html := dueMailContent(ev, "https://wb.example.com")
	if !strings.Contains(html, `href="https://wb.example.com/todo?task=t1"`) || !strings.Contains(html, "查看任务") {
		t.Fatalf("action button missing: %s", html)
	}
	if !strings.Contains(text, "查看任务：https://wb.example.com/todo?task=t1") {
		t.Fatalf("plain text link missing: %s", text)
	}
	for _, site := range []string{"", "javascript:alert(1)", "wb.example.com"} {
		_, _, html = dueMailContent(ev, site)
		if strings.Contains(html, "href=") {
			t.Errorf("invalid site_url %q should not render a link: %s", site, html)
		}
	}
}

// 正文 markdown 链接只允许站内 http(s)：外域/javascript 降级为纯文字
func TestMdToHTML_Links(t *testing.T) {
	md := "详见 [任务看板](https://wb.example.com/todo) 与 `done` 状态。\n\n- [危险](javascript:alert)\n- [外链](https://evil.com/x)\n- **重点**"
	html := string(mdToHTML(md, "https://wb.example.com"))
	if !strings.Contains(html, `<a href="https://wb.example.com/todo"`) {
		t.Errorf("same-site link missing: %s", html)
	}
	if !strings.Contains(html, "<code") || !strings.Contains(html, "<b>重点</b>") {
		t.Errorf("inline code/bold missing: %s", html)
	}
	if strings.Contains(html, "javascript:") || strings.Contains(html, "evil.com") {
		t.Errorf("unsafe link rendered: %s", html)
	}
	if !strings.Contains(html, "text-indent:-14px") {
		t.Errorf("list hanging indent missing: %s", html)
	}
	// 站点未配置：链接只显示为文字
	html = string(mdToHTML("[任务看板](https://wb.example.com/todo)", ""))
	if strings.Contains(html, "<a ") {
		t.Errorf("link should degrade to text without site_url: %s", html)
	}
}
