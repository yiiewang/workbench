package notify

import (
	"strings"
	"testing"
)

func TestSanitizeAIHTML(t *testing.T) {
	const site = "https://wb.example.com"

	t.Run("语义标签保留并注入统一样式", func(t *testing.T) {
		raw := `<h3>建议</h3><p>优先处理 <b>逾期项</b>，用 <code>reindex_status</code> 复核。</p><ul><li>项一</li></ul>`
		got := string(sanitizeAIHTML(raw, site))
		for _, want := range []string{"<h3", "border-left:3px solid #2563eb", "<b>逾期项</b>", "<code", "<ul", "<li"} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in %s", want, got)
			}
		}
	})

	t.Run("脚本图片表单与属性整棵丢弃", func(t *testing.T) {
		raw := `<p onmouseover="x" style="color:red">正文</p><script>alert(1)</script>` +
			`<img src="https://evil.example.com/p.png"><iframe src="https://evil.example.com"></iframe>` +
			`<form><input value="x"></form>`
		got := string(sanitizeAIHTML(raw, site))
		for _, bad := range []string{"<script", "alert", "<img", "iframe", "onmouseover", "color:red", "<form"} {
			if strings.Contains(got, bad) {
				t.Fatalf("unsafe content %q survived: %s", bad, got)
			}
		}
		if !strings.Contains(got, "正文") {
			t.Fatalf("text lost: %s", got)
		}
	})

	t.Run("外链降级为文字同域链接保留", func(t *testing.T) {
		raw := `<p><a href="https://evil.example.com/x">外链</a> ` +
			`<a href="https://wb.example.com/todo">看板</a> <a href="javascript:alert(1)">脚本</a></p>`
		got := string(sanitizeAIHTML(raw, site))
		if strings.Contains(got, "evil.example.com") || strings.Contains(got, "javascript:") {
			t.Fatalf("unsafe link survived: %s", got)
		}
		if !strings.Contains(got, `href="https://wb.example.com/todo"`) {
			t.Fatalf("same-host link lost: %s", got)
		}
		for _, want := range []string{"外链", "脚本", "看板"} {
			if !strings.Contains(got, want) {
				t.Fatalf("link text %q lost: %s", want, got)
			}
		}
	})

	t.Run("文本转义与未知标签 unwrap", func(t *testing.T) {
		got := string(sanitizeAIHTML(`<p>a &amp; b &lt;tag&gt;</p><section><span>保留</span></section>`, site))
		if strings.Contains(got, "<section") || strings.Contains(got, "<span") {
			t.Fatalf("unknown tag survived: %s", got)
		}
		if !strings.Contains(got, "保留") || !strings.Contains(got, "&lt;tag&gt;") {
			t.Fatalf("text mismatch: %s", got)
		}
	})
}

func TestHTMLToPlainText(t *testing.T) {
	got := htmlToPlainText(`<h3>建议</h3><p>优先处理 <b>逾期项</b>。</p>` +
		`<ul><li>项一</li><li>项二</li></ul><script>alert(1)</script>`)
	if strings.Contains(got, "alert") || strings.Contains(got, "<p") || strings.Contains(got, "<h3") {
		t.Fatalf("tags or script leaked: %q", got)
	}
	for _, want := range []string{"建议", "逾期项", "- 项一", "- 项二"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestRenderLetterEmailHTML(t *testing.T) {
	got := renderLetterEmailHTML(emailLetterContent{
		UserName:  "艺",
		JobName:   "每日工作总结",
		Year:      2026,
		BodyHTML:  "<p>正文</p>",
		ActionURL: "https://wb.example.com/jobs",
	})
	for _, want := range []string{"艺，你好！", "每日工作总结", "© 2026 Workbench",
		"管理定时任务", "<p>正文</p>", "Workbench 团队"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output", want)
		}
	}

	escaped := renderLetterEmailHTML(emailLetterContent{
		UserName: "<script>x</script>", JobName: "<b>日报</b>", Year: 2026, BodyHTML: "<p>正文</p>",
	})
	if strings.Contains(escaped, "<script>") || strings.Contains(escaped, "<b>日报</b>") {
		t.Fatalf("user/job name not escaped: %s", escaped)
	}

	plain := renderLetterEmailHTML(emailLetterContent{Year: 2026, BodyHTML: "<p>正文</p>"})
	if !strings.Contains(plain, ">你好！<") || strings.Contains(plain, "，你好！") {
		t.Fatalf("empty user name should fall back: %s", plain)
	}
}
