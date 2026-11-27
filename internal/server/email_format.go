package server

import (
	"context"
	"fmt"
	"strings"
)

// notifyFormatPrompt 邮件正文排版提示词：只做版面整理，事实与链接由消毒层再校验。
func notifyFormatPrompt(subject, body, siteURL string) string {
	siteRule := "未配置站点地址，不要生成任何链接；原文中的 URL 保留为纯文字。"
	if site := strings.TrimRight(strings.TrimSpace(siteURL), "/"); site != "" {
		siteRule = fmt.Sprintf(
			"链接只能指向 %s 同域；把原文中已出现的站内 URL 转成 <a href>，也可生成指向 %s/jobs、%s/todo 的站内链接，但不要编造路径或参数；外链一律保留为纯文字。",
			site, site, site)
	}
	return "你是 Workbench 的邮件正文排版助手。把下面的通知正文整理成可直接嵌入邮件模板的 HTML 片段。\n\n" +
		"通知主题：" + subject + "（不要重复输出主题）\n\n" +
		"输入可能是 Markdown、纯文本、JSON、工具输出、代码块或已有的 HTML。只做版面整理：不改写、不补充、不删除事实，不推测缺失信息；原文没有的内容一律不要添加。\n\n" +
		"输出要求：\n" +
		"1. 只输出正文 HTML 片段，不要输出 <!DOCTYPE>、<html>、<head>、<body>，不要用代码块标记包裹，不要输出任何解释。\n" +
		"2. 允许的标签：<h2> <h3> <p> <div> <ul> <ol> <li> <b> <strong> <em> <code> <blockquote> <br> <hr> <a> 以及 <table> <tr> <td> <th>；不要使用其它标签。用 <h2>/<h3> 分节、<ul>/<li> 列举、<blockquote> 强调提示块、<table> 做两列对照、<code> 标注命令或字段名。\n" +
		"3. 不要写 style、class、id、on* 等任何属性；样式由邮件模板统一提供。\n" +
		"4. " + siteRule + "\n" +
		"5. 不要写任务名、问候语、落款、日期标题或「以下是…」之类的开场白，从内容直接开始。\n" +
		"6. 正文为空或只有空白时，输出 <p>（无内容）</p>。\n\n" +
		"正文：\n---\n" + body + "\n---\n"
}

// formatNotifyBody 把任意格式的 user.notify 正文交给模型排版。
// notify 包通过注入调用本方法，不反向依赖 Server 与 AI 配置。
func (s *Server) formatNotifyBody(ctx context.Context, subject, body, siteURL string) (string, error) {
	msg, _, err := s.aiChatCompletion(ctx, map[string]any{
		"messages": []aiMessage{
			{Role: "user", Content: notifyFormatPrompt(subject, body, siteURL)},
		},
	})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}
