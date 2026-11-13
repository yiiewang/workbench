package rag

import (
	"strings"
	"testing"
)

func TestExtractHTMLText(t *testing.T) {
	in := `<!DOCTYPE html>
<html><head>
<title>测试报告</title>
<script>var x = "should not appear";</script>
<style>.a { color: red; }</style>
<meta name="desc" content="meta 跳过">
</head>
<body>
<h1>性能对比</h1>
<p>本报告对比 <b>V4</b> 与 <span>chainmaker2.3.9</span> 的查询性能。</p>
<div>
<ul><li>36 个查询</li><li>命中 18 次</li></ul>
</div>
<footer>© 2026</footer>
</body></html>`

	out := extractHTMLText(in)

	// 标签/脚本/样式/meta 不出现
	for _, bad := range []string{"<", "script", "should not appear", "color: red", "meta 跳过", "DOCTYPE"} {
		if strings.Contains(out, bad) {
			t.Errorf("extracted text should not contain %q:\n%s", bad, out)
		}
	}
	// 可见文本与标题保留
	for _, want := range []string{"测试报告", "性能对比", "V4", "chainmaker2.3.9", "36 个查询", "命中 18 次", "© 2026"} {
		if !strings.Contains(out, want) {
			t.Errorf("extracted text should contain %q:\n%s", want, out)
		}
	}
	// 行内拼接：加粗/ span 不拆行
	if !strings.Contains(out, "本报告对比 V4 与 chainmaker2.3.9 的查询性能。") {
		t.Errorf("inline text should join on one line:\n%s", out)
	}
	// 块级分段：段落间有空行（SplitChunks 的段落边界）
	if !strings.Contains(out, "性能对比\n\n") {
		t.Errorf("block elements should end with blank line:\n%q", out)
	}
}

func TestExtractHTMLText_Degenerate(t *testing.T) {
	// 空内容 / 纯标签：不 panic、输出为空或极简
	if out := extractHTMLText(""); strings.TrimSpace(out) != "" {
		t.Errorf("empty input should yield empty output, got %q", out)
	}
	if out := extractHTMLText("<div><span></span></div>"); strings.TrimSpace(out) != "" {
		t.Errorf("tag-only input should yield empty output, got %q", out)
	}
	// 非 HTML 纯文本：解析器宽容处理，文本原样保留（不丢内容）
	if out := extractHTMLText("plain text, no tags"); !strings.Contains(out, "plain text, no tags") {
		t.Errorf("plain text should survive extraction, got %q", out)
	}
}
