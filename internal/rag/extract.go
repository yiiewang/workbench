// Package rag 内 HTML 正文提取：索引 .html 文件时剥离标签/脚本/样式，
// 只留可见文本——源码直接入库会让 <div>/<style> 等高频标签污染 embedding
// 向量（检索质量下降）并浪费注入 token（模型读到一堆语法）。
//
// 输出约定：块级元素（p/div/h1-h6/li/tr…）之间以空行分段（SplitChunks 的
// 段落边界天然成立）；行内元素文本以空格拼接；标题保留为独立段（语义锚点）；
// <head> 只取 <title>（页面主题信息）。解析失败退回原文（降级保可用）。
package rag

import (
	"strings"

	"golang.org/x/net/html"
)

// htmlSkipTags 整个子树跳过的元素（不可见/无语义）
var htmlSkipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "iframe": true, "canvas": true, "object": true,
}

// htmlBlockTags 块级元素：结束后空行分段
var htmlBlockTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"p": true, "div": true, "section": true, "article": true, "header": true,
	"footer": true, "nav": true, "aside": true, "main": true, "li": true,
	"tr": true, "table": true, "pre": true, "blockquote": true, "ul": true, "ol": true,
	"dl": true, "dt": true, "dd": true, "figure": true, "figcaption": true,
	"title": true,
}

// extractHTMLText 提取 HTML 可见正文为分段纯文本
func extractHTMLText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	var b strings.Builder
	endsWithNL := true // 追加状态：块间空行去重
	writeText := func(t string) {
		if t == "" {
			return
		}
		if !endsWithNL {
			b.WriteByte(' ')
		}
		b.WriteString(t)
		endsWithNL = false
	}
	endBlock := func() {
		if !endsWithNL {
			b.WriteString("\n\n")
			endsWithNL = true
		}
	}

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			switch {
			case htmlSkipTags[n.Data]:
				return
			case n.Data == "head":
				// 只取 <title>（页面主题），其余 head 内容（meta/link/script）跳过
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "title" {
						walk(c)
					}
				}
				return
			case n.Data == "br":
				endBlock()
				return
			}
		case html.TextNode:
			writeText(strings.TrimSpace(n.Data))
			return
		case html.CommentNode:
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && htmlBlockTags[n.Data] {
			endBlock()
		}
	}
	walk(doc)
	return strings.TrimSpace(b.String())
}
