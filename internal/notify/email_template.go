package notify

// HTML 邮件模板（简约版，设计稿见 docs/email-template-preview.html）。
//
// 邮件客户端兼容约束：table 布局 + 内联样式（无 flex/外部 CSS）；
// 用户内容（任务标题/备注/AI 输出）一律经 html/template 转义防注入。
// AI 输出为 markdown，经 mdToHTML 做极简转换（标题/列表/粗体/段落）。

import (
	"html/template"
	"strings"
)

// emailContent 邮件正文内容模型（三种邮件共用骨架）
type emailContent struct {
	Kind     string        // 顶部灰色类型标识，如 "WORKBENCH · 任务到期提醒"
	Title    string        // 加粗大标题
	Meta     template.HTML // 标题下灰色信息行（代码构造的安全富文本，可含 b 标签）
	BodyHTML template.HTML
	Footer   string // 底部小灰字（触发说明 + 时间）
}

// emailTmpl 骨架模板（简约版：类型行/标题/meta/正文/分隔线/底部）
var emailTmpl = template.Must(template.New("email").Parse(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body style="margin:0;padding:48px 16px;background:#f4f5f7;font-family:-apple-system,'PingFang SC','Microsoft YaHei','Helvetica Neue',Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:10px;padding:32px 36px;">
<tr><td style="font-size:11px;color:#9aa1ab;letter-spacing:.8px;padding-bottom:20px;">{{.Kind}}</td></tr>
<tr><td style="font-size:18px;font-weight:600;color:#1a1f27;line-height:1.5;padding-bottom:8px;">{{.Title}}</td></tr>
{{if .Meta}}<tr><td style="font-size:13px;color:#6b7280;line-height:1.7;padding-bottom:20px;">{{.Meta}}</td></tr>{{end}}
<tr><td style="font-size:14px;color:#374151;line-height:1.9;padding-bottom:24px;">{{.BodyHTML}}</td></tr>
<tr><td><div style="height:1px;background:#f0f1f3;margin:0 0 14px;"></div>
<div style="font-size:11px;color:#b0b6bf;line-height:1.7;">{{.Footer}}</div></td></tr>
</table></body></html>`))

// renderEmailHTML 渲染邮件 HTML
func renderEmailHTML(c emailContent) string {
	var b strings.Builder
	if err := emailTmpl.Execute(&b, c); err != nil {
		return "" // 渲染失败退回纯文本（发送层有 text 兜底）
	}
	return b.String()
}

// mdToHTML 极简 markdown → HTML 转换（AI 定时任务输出用）：
//   #/##/### 标题 → 加粗段；- / 1. 列表行；**text** 粗体；空行分段。
// 不求完备（AI 输出邮件场景够用），未识别格式按段落原样（转义后）输出。
func mdToHTML(md string) template.HTML {
	esc := func(s string) string {
		s = strings.ReplaceAll(s, "&", "&amp;")
		s = strings.ReplaceAll(s, "<", "&lt;")
		s = strings.ReplaceAll(s, ">", "&gt;")
		return s
	}
	bold := func(s string) string {
		var out strings.Builder
		rest := s
		for {
			i := strings.Index(rest, "**")
			if i < 0 {
				out.WriteString(esc(rest))
				return out.String()
			}
			out.WriteString(esc(rest[:i]))
			rest = rest[i+2:]
			j := strings.Index(rest, "**")
			if j < 0 {
				out.WriteString(esc(rest))
				return out.String()
			}
			out.WriteString("<b>" + esc(rest[:j]) + "</b>")
			rest = rest[j+2:]
		}
	}

	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	inList := false
	closeList := func() {
		if inList {
			b.WriteString("</div>")
			inList = false
		}
	}
	// 中文序号小标题（AI 输出常用"一、二、…"分节）
	cnHeading := func(t string) bool {
		for _, p := range []string{"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"} {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			closeList()
		case strings.HasPrefix(t, "### ") || strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "# "):
			closeList()
			title := strings.TrimSpace(strings.TrimLeft(t, "# "))
			b.WriteString(`<div style="font-weight:600;color:#1a1f27;margin:14px 0 6px;">` + bold(title) + "</div>")
		case cnHeading(t):
			// 中文序号分节（"一、今日完成与进展"）→ 加粗小标题
			closeList()
			b.WriteString(`<div style="font-weight:600;color:#1a1f27;margin:14px 0 6px;">` + bold(t) + "</div>")
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "· ") || strings.HasPrefix(t, "• "):
			// 列表（含 AI 常用的中点/圆点符号）
			if !inList {
				b.WriteString(`<div style="padding:2px 0;">`)
				inList = true
			}
			b.WriteString("· " + bold(strings.TrimSpace(strings.TrimLeft(t, "-*·• "))) + "<br>")
		case len(t) > 3 && (t[0] >= '0' && t[0] <= '9') && (strings.HasPrefix(t, "1. ") || strings.HasPrefix(t, "2. ") || strings.HasPrefix(t, "3. ") ||
			strings.HasPrefix(t, "4. ") || strings.HasPrefix(t, "5. ") || strings.HasPrefix(t, "6. ") ||
			strings.HasPrefix(t, "7. ") || strings.HasPrefix(t, "8. ") || strings.HasPrefix(t, "9. ")):
			if !inList {
				b.WriteString(`<div style="padding:2px 0;">`)
				inList = true
			}
			b.WriteString(bold(t) + "<br>")
		default:
			closeList()
			b.WriteString(bold(t) + "<br>")
		}
	}
	closeList()
	return template.HTML(b.String())
}
