package notify

// 信件式邮件模板（AI 提示词定时任务结果专用）与模型输出 HTML 的消毒。
// 模型输出不可信：只放行语义标签白名单，样式由模板统一注入，链接仅允许
// 站点同域，脚本/图片/表单整棵丢弃；纯文本兜底从同一份 HTML 提取。

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// emailLetterContent 信件式模板的内容模型。
type emailLetterContent struct {
	UserName  string
	JobName   string
	Year      int
	BodyHTML  template.HTML
	ActionURL template.URL
}

// letterEmailTmpl 信件式骨架：品牌标识 → 问候 → AI 正文片段 → 落款 → 页脚。
// 与卡片式模板同样使用 table 布局 + 内联样式，不依赖外部 CSS。
var letterEmailTmpl = template.Must(template.New("letter").Parse(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body style="margin:0;padding:28px 12px 56px;background:#ffffff;font-family:-apple-system,'PingFang SC','Microsoft YaHei','Helvetica Neue',Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#ffffff;">
<tr><td align="center" style="padding:0 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;margin:0 auto;background:#ffffff;padding:44px 48px 36px;">
<tr><td align="center" style="padding-bottom:34px;">
<div style="font-size:15px;font-weight:600;color:#1a1f27;letter-spacing:1px;"><span style="display:inline-block;width:16px;height:16px;margin-right:8px;vertical-align:-2px;background:#2563eb;border-radius:4px;"></span>Workbench</div>
</td></tr>
<tr><td style="font-size:15px;color:#374151;line-height:1.9;padding-bottom:22px;">{{if .UserName}}{{.UserName}}，你好！{{else}}你好！{{end}}</td></tr>
<tr><td style="font-size:14px;color:#374151;line-height:1.95;">{{.BodyHTML}}</td></tr>
<tr><td style="font-size:14px;color:#374151;line-height:1.9;padding-top:30px;">谢谢！<br>Workbench 团队</td></tr>
<tr><td align="center" style="padding-top:48px;">
<div style="height:1px;background:#f0f1f3;margin:0 0 22px;"></div>
<div style="font-size:20px;font-weight:600;color:#9aa1ab;letter-spacing:1px;padding-bottom:10px;">Workbench</div>
<div style="font-size:11px;color:#b0b6bf;line-height:1.9;">© {{.Year}} Workbench{{if .ActionURL}}<br><a href="{{.ActionURL}}" style="color:#9aa1ab;text-decoration:underline;">管理定时任务</a>{{end}}</div>
<div style="font-size:11px;color:#c3c8d0;line-height:1.9;padding-top:18px;">本邮件由定时任务「{{.JobName}}」自动生成，内容由 AI 整理，请自行核对关键信息。</div>
</td></tr>
</table></td></tr></table></body></html>`))

func renderLetterEmailHTML(c emailLetterContent) string {
	if c.Year == 0 {
		c.Year = time.Now().Year()
	}
	var b strings.Builder
	if err := letterEmailTmpl.Execute(&b, c); err != nil {
		return "" // 渲染失败退回纯文本（发送层有 text 兜底）
	}
	return b.String()
}

// letterDropTags 整棵丢弃的标签（含其文本），防脚本、外链图片、表单与追踪内容。
var letterDropTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true,
	"img": true, "svg": true, "form": true, "input": true, "button": true,
	"link": true, "meta": true, "base": true, "video": true, "audio": true,
	"source": true, "frame": true, "frameset": true, "noscript": true,
	"template": true, "head": true, "title": true,
}

// letterAllowedTags 放行的语义标签；未列出的标签只保留其子内容（unwrap）。
var letterAllowedTags = map[string]bool{
	"p": true, "div": true, "h2": true, "h3": true, "h4": true,
	"ul": true, "ol": true, "li": true, "b": true, "strong": true,
	"em": true, "i": true, "a": true, "code": true, "br": true, "hr": true,
	"blockquote": true, "table": true, "thead": true, "tbody": true,
	"tr": true, "td": true, "th": true,
}

// letterTagStyle 白名单标签的统一内联样式；模型自带的 style/class 全部丢弃。
var letterTagStyle = map[string]string{
	"h2":         "font-weight:600;font-size:15px;color:#1a1f27;margin:20px 0 8px;padding-left:10px;border-left:3px solid #2563eb;",
	"h3":         "font-weight:600;font-size:15px;color:#1a1f27;margin:20px 0 8px;padding-left:10px;border-left:3px solid #2563eb;",
	"h4":         "font-weight:600;font-size:14px;color:#1a1f27;margin:16px 0 8px;",
	"p":          "padding:2px 0 10px;",
	"div":        "padding:2px 0 10px;",
	"ul":         "margin:8px 0;padding-left:20px;",
	"ol":         "margin:8px 0;padding-left:20px;",
	"li":         "padding:4px 0;",
	"code":       "padding:1px 4px;background:#f3f4f6;border-radius:3px;font-size:12px;color:#b91c1c;",
	"blockquote": "margin:8px 0;padding-left:12px;border-left:3px solid #e5e7eb;color:#6b7280;",
	"a":          "color:#2563eb;text-decoration:underline;",
	"hr":         "border:0;border-top:1px solid #f0f1f3;margin:16px 0;",
	"table":      "border-collapse:collapse;margin:10px 0;",
	"td":         "padding:6px 10px;border:1px solid #e5e7eb;",
	"th":         "padding:6px 10px;border:1px solid #e5e7eb;font-weight:600;",
}

// sanitizeAIHTML 把模型输出的 HTML 片段消毒为可安全嵌入邮件模板的富文本。
func sanitizeAIHTML(raw, siteURL string) template.HTML {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return template.HTML(escHTML(raw))
	}
	body := letterHTMLBody(doc)
	if body == nil {
		return ""
	}
	allowedHost := siteHost(siteURL)
	var b strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		writeLetterNode(&b, c, allowedHost)
	}
	return template.HTML(b.String())
}

func letterHTMLBody(doc *html.Node) *html.Node {
	var body *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	return body
}

func writeLetterNode(b *strings.Builder, n *html.Node, allowedHost string) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(escHTML(n.Data))
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		if letterDropTags[tag] {
			return
		}
		if !letterAllowedTags[tag] {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				writeLetterNode(b, c, allowedHost)
			}
			return
		}
		if tag == "br" {
			b.WriteString("<br>")
			return
		}
		if tag == "hr" {
			fmt.Fprintf(b, `<hr style="%s">`, letterTagStyle["hr"])
			return
		}
		attr := ""
		if tag == "a" {
			href, ok := letterHref(n, allowedHost)
			if !ok {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					writeLetterNode(b, c, allowedHost)
				}
				return
			}
			attr = fmt.Sprintf(` href="%s"`, href)
		}
		if style := letterTagStyle[tag]; style != "" {
			fmt.Fprintf(b, `<%s%s style="%s">`, tag, attr, style)
		} else {
			fmt.Fprintf(b, `<%s%s>`, tag, attr)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeLetterNode(b, c, allowedHost)
		}
		fmt.Fprintf(b, `</%s>`, tag)
	}
}

func letterHref(n *html.Node, allowedHost string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "href") {
			return safeHref(a.Val, allowedHost)
		}
	}
	return "", false
}

// htmlToPlainText 从模型输出的 HTML 提取纯文本，供 multipart 邮件的 text 部分使用。
func htmlToPlainText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	body := letterHTMLBody(doc)
	if body == nil {
		return ""
	}
	var b strings.Builder
	writeLetterText(&b, body)
	var lines []string
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" && (len(lines) == 0 || lines[len(lines)-1] == "") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func writeLetterText(b *strings.Builder, n *html.Node) {
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		if letterDropTags[tag] {
			return
		}
		if tag == "li" {
			b.WriteString("\n- ")
		}
		if tag == "br" {
			b.WriteString("\n")
		}
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeLetterText(b, c)
	}
	if n.Type == html.ElementNode {
		switch strings.ToLower(n.Data) {
		case "p", "div", "h2", "h3", "h4", "ul", "ol", "li", "tr", "hr", "blockquote", "table":
			b.WriteString("\n")
		}
	}
}
