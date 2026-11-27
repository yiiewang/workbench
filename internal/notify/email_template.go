package notify

// HTML 邮件模板（简约版，设计稿见 docs/email-template-preview.html）。
//
// 邮件客户端兼容约束：table 布局 + 内联样式（无 flex/外部 CSS）；
// 用户内容（任务标题/备注/AI 输出）一律经 html/template 转义防注入。
// AI 输出为 markdown，经 mdToHTML 做极简转换（标题/列表/粗体/行内代码/链接/段落）。
// 全局站点根地址（顶层 site_url）用于生成正文下方的跳转按钮，并作为正文
// 链接的唯一可信域名；未配置时邮件不含任何链接，避免发出无效或伪造链接。

import (
	"html/template"
	"net/url"
	"regexp"
	"strings"
)

// emailContent 邮件正文内容模型（三种邮件共用骨架）
type emailContent struct {
	Kind        string        // 顶部灰色类型标识，如 "WORKBENCH · 任务到期提醒"
	Title       string        // 加粗大标题
	Meta        template.HTML // 标题下灰色信息行（代码构造的安全富文本，可含 b 标签）
	BodyHTML    template.HTML
	ActionURL   template.URL // 正文下方跳转按钮地址（空 = 不渲染按钮）
	ActionLabel string       // 按钮文案，如 "查看任务"
	Footer      string       // 底部小灰字（触发说明 + 时间）
}

// emailTmpl 骨架模板（简约版：类型行/标题/meta/正文/分隔线/底部）
var emailTmpl = template.Must(template.New("email").Parse(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body style="margin:0;padding:48px 16px;background:#f4f5f7;font-family:-apple-system,'PingFang SC','Microsoft YaHei','Helvetica Neue',Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:880px;margin:0 auto;background:#ffffff;border-radius:10px;padding:32px 36px;">
<tr><td style="font-size:11px;color:#9aa1ab;letter-spacing:.8px;padding-bottom:20px;">{{.Kind}}</td></tr>
<tr><td style="font-size:18px;font-weight:600;color:#1a1f27;line-height:1.5;padding-bottom:8px;">{{.Title}}</td></tr>
{{if .Meta}}<tr><td style="font-size:13px;color:#6b7280;line-height:1.7;padding-bottom:20px;">{{.Meta}}</td></tr>{{end}}
<tr><td style="font-size:14px;color:#374151;line-height:1.8;padding-bottom:20px;">{{.BodyHTML}}</td></tr>
{{if .ActionURL}}<tr><td style="padding-bottom:24px;"><table role="presentation" cellpadding="0" cellspacing="0"><tr><td bgcolor="#2563eb" style="border-radius:6px;"><a href="{{.ActionURL}}" style="display:inline-block;padding:10px 18px;color:#ffffff;font-size:13px;font-weight:600;text-decoration:none;">{{.ActionLabel}} →</a></td></tr></table></td></tr>{{end}}
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

// inlineRe 行内标记：[文字](url) / **粗体** / `行内代码`
var inlineRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)|\*\*(.+?)\*\*|` + "`([^`]+)`")

// escHTMLAttr HTML 属性值转义（在 escHTML 基础上补引号）
func escHTMLAttr(s string) string {
	return strings.NewReplacer(`"`, "&#34;", "'", "&#39;").Replace(escHTML(s))
}

// siteActionURL 拼接站内跳转链接；站点地址缺失或非 http(s) 时返回空（不渲染按钮）。
func siteActionURL(siteURL, path string) string {
	if siteHost(siteURL) == "" {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(siteURL), "/") + path
}

// siteHost 站点可信域名（供正文链接校验）；非法配置返回空。
func siteHost(siteURL string) string {
	u, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Host
}

// safeHref 校验正文 markdown 链接：只允许 http(s) 且与站点同域，
// 防止 AI 编造外链或 javascript: 注入；不合法时调用方降级为纯文字。
func safeHref(raw, allowedHost string) (string, bool) {
	if allowedHost == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, allowedHost) {
		return "", false
	}
	return escHTMLAttr(u.String()), true
}

// mdToHTML 极简 markdown → HTML 转换（AI 定时任务输出用）：
//
//	#/##/### 与「一、」→ 分节标题；- / · / 1. → 列表项；
//	|...| 管道表格（第二行为 |---| 分隔行）→ table；> 引用 → 灰底引文块；
//	**text** 粗体、`code` 行内代码、[文字](url) 站内链接；空行分段。
//
// 每项独立成块 + 悬挂缩进：长句折行与首行文字对齐、条目间留白，
// 避免多个长 bullet 挤成一片文字墙。siteURL 未配置时链接只显示为文字。
// 不求完备（AI 输出邮件场景够用），未识别格式按段落原样（转义后）输出。
func mdToHTML(md, siteURL string) template.HTML {
	allowedHost := siteHost(siteURL)
	inline := func(s string) string {
		var out strings.Builder
		last := 0
		for _, m := range inlineRe.FindAllStringSubmatchIndex(s, -1) {
			out.WriteString(escHTML(s[last:m[0]]))
			switch {
			case m[2] >= 0: // [文字](url)
				text, raw := s[m[2]:m[3]], s[m[4]:m[5]]
				if href, ok := safeHref(raw, allowedHost); ok {
					out.WriteString(`<a href="` + href + `" style="color:#2563eb;text-decoration:underline;">` + escHTML(text) + "</a>")
				} else {
					out.WriteString(escHTML(text))
				}
			case m[6] >= 0: // **粗体**
				out.WriteString("<b>" + escHTML(s[m[6]:m[7]]) + "</b>")
			case m[8] >= 0: // `行内代码`
				out.WriteString(`<code style="padding:1px 4px;background:#f3f4f6;border-radius:3px;font-size:12px;color:#b91c1c;">` + escHTML(s[m[8]:m[9]]) + "</code>")
			}
			last = m[1]
		}
		out.WriteString(escHTML(s[last:]))
		return out.String()
	}

	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	// 中文序号小标题（AI 输出常用"一、二、…"分节）
	cnHeading := func(t string) bool {
		for _, p := range []string{"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"} {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}
	// 管道表格：表头行 + |---| 分隔行 + 数据行；分隔行只用于识别，不输出
	isTableSep := func(s string) bool {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "|") || !strings.HasSuffix(s, "|") {
			return false
		}
		inner := strings.Trim(s[1:len(s)-1], " ")
		if inner == "" || !strings.Contains(inner, "-") {
			return false
		}
		for _, r := range inner {
			if r != '-' && r != ':' && r != '|' && r != ' ' {
				return false
			}
		}
		return true
	}
	isTableRow := func(s string) bool {
		s = strings.TrimSpace(s)
		return strings.HasPrefix(s, "|") && strings.HasSuffix(s, "|") && len(s) > 1
	}
	splitCells := func(s string) []string {
		s = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(s), "|"), "|")
		parts := strings.Split(s, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case isTableRow(t) && i+1 < len(lines) && isTableSep(lines[i+1]):
			rows := []string{t, strings.TrimSpace(lines[i+1])}
			i += 2
			for i < len(lines) && isTableRow(lines[i]) {
				rows = append(rows, strings.TrimSpace(lines[i]))
				i++
			}
			i--
			b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" style="border-collapse:collapse;width:100%;margin:10px 0;font-size:13px;">`)
			b.WriteString("<thead><tr>")
			for _, c := range splitCells(rows[0]) {
				b.WriteString(`<th style="border:1px solid #e5e7eb;background:#f8f9fa;padding:6px 10px;text-align:left;font-weight:600;color:#1a1f27;">` + inline(c) + "</th>")
			}
			b.WriteString("</tr></thead><tbody>")
			for _, row := range rows[2:] {
				b.WriteString("<tr>")
				for _, c := range splitCells(row) {
					b.WriteString(`<td style="border:1px solid #e5e7eb;padding:6px 10px;color:#374151;">` + inline(c) + "</td>")
				}
				b.WriteString("</tr>")
			}
			b.WriteString("</tbody></table>")
		case strings.HasPrefix(t, "> ") || t == ">":
			var quote []string
			for i < len(lines) {
				q := strings.TrimSpace(lines[i])
				if strings.HasPrefix(q, "> ") {
					quote = append(quote, strings.TrimPrefix(q, "> "))
				} else if q == ">" {
					quote = append(quote, "")
				} else {
					break
				}
				i++
			}
			i--
			b.WriteString(`<div style="margin:12px 0;padding:8px 12px;background:#f8f9fa;border-left:3px solid #9aa1ab;color:#6b7280;font-size:13px;line-height:1.7;">` + inline(strings.Join(quote, " ")) + "</div>")
		case strings.HasPrefix(t, "### ") || strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "# "):
			title := strings.TrimSpace(strings.TrimLeft(t, "# "))
			b.WriteString(`<div style="font-weight:600;font-size:15px;color:#1a1f27;margin:20px 0 8px;padding-left:10px;border-left:3px solid #2563eb;">` + inline(title) + "</div>")
		case cnHeading(t):
			b.WriteString(`<div style="font-weight:600;font-size:15px;color:#1a1f27;margin:20px 0 8px;padding-left:10px;border-left:3px solid #2563eb;">` + inline(t) + "</div>")
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "· ") || strings.HasPrefix(t, "• "):
			// 列表（含 AI 常用的中点/圆点符号）；只剥离列表符号本身，保留内容里的 **粗体**
			item := t
			for _, p := range []string{"- ", "* ", "· ", "• "} {
				if strings.HasPrefix(t, p) {
					item = strings.TrimPrefix(t, p)
					break
				}
			}
			b.WriteString(`<div style="padding:5px 0 5px 14px;text-indent:-14px;">· ` + inline(strings.TrimSpace(item)) + "</div>")
		case len(t) > 3 && (t[0] >= '0' && t[0] <= '9') && (strings.HasPrefix(t, "1. ") || strings.HasPrefix(t, "2. ") || strings.HasPrefix(t, "3. ") ||
			strings.HasPrefix(t, "4. ") || strings.HasPrefix(t, "5. ") || strings.HasPrefix(t, "6. ") ||
			strings.HasPrefix(t, "7. ") || strings.HasPrefix(t, "8. ") || strings.HasPrefix(t, "9. ")):
			b.WriteString(`<div style="padding:5px 0 5px 14px;text-indent:-14px;">` + inline(t) + "</div>")
		default:
			b.WriteString(`<div style="padding:2px 0 8px;">` + inline(t) + "</div>")
		}
	}
	return template.HTML(b.String())
}
