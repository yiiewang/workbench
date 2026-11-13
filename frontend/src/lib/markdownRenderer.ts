// 全局 marked 定制（marked 是单例，配置一次即对文件预览与 todo 预览同时生效）：
//   - 代码块走 highlight.js 高亮（注册常用语言，避免引入全量语言包）
//   - 支持 ==高亮== 语法
// 原先这套配置写在 useTodoApp 的 setup 里，会隐式影响文件预览；现在收敛到统一渲染层。
import { marked } from 'marked'
// hljs 实例与语言注册统一在 highlighter.ts（与 CodePreview 文件预览共用一份）
import { hljs } from './highlighter'

let configured = false

// 只转义 & < >：mermaid.run() 读的是节点 innerHTML，随后会做 entityDecode 还原成原字符，
// 所以按实体输出即可保证拿到的是原文（不能多做转义，否则会变成 %xx 之类的字面量）
function escapeHtml(src: string): string {
  return src.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

export function configureMarked(): void {
  if (configured) return
  configured = true

  marked.use({
    extensions: [
      {
        name: 'mark',
        level: 'inline',
        start(src: string) {
          return src.indexOf('==')
        },
        tokenizer(src: string) {
          const match = /^==(.+?)==/.exec(src)
          if (!match) return undefined
          return { type: 'mark', raw: match[0], text: match[1] }
        },
        // 自定义 token 不在 marked 内置 Tokens 联合里，这里按自定义结构取值
        renderer(token: any) {
          return '<mark>' + token.text + '</mark>'
        },
      } as never,
    ],
    renderer: {
      code(code: string, language?: string) {
        // mermaid 必须原样输出：mermaid.run() 用节点 innerHTML 作为源码，
        // 一旦被 hljs（highlightAuto）插进 <span>，这些标签就会变成源码文本 → "Syntax error in text"
        if (language === 'mermaid') {
          return `<pre class="mermaid-source"><code class="language-mermaid">${escapeHtml(code)}</code></pre>`
        }
        const highlighted = language && hljs.getLanguage(language)
          ? hljs.highlight(code, { language }).value
          : hljs.highlightAuto(code).value
        const langClass = language ? `language-${language}` : ''
        return `<pre><code class="hljs ${langClass}">${highlighted}</code></pre>`
      },
    },
  })
}

/**
 * markdown → 安全 HTML：AI 对话消息、移动端任务详情等 v-html 场景共用。
 * 剔除脚本类标签与内联事件兜底（marked 默认不转义原始 HTML）。
 */
export function renderMarkdown(text: string): string {
  if (!text) return ''
  configureMarked()
  try {
    return (marked.parse(text, { async: false }) as string)
      .replace(/<script[\s\S]*?<\/script>/gi, '')
      .replace(/<iframe[\s\S]*?<\/iframe>/gi, '')
      .replace(/\son\w+="[^"]*"/gi, '')
  } catch {
    return ''
  }
}

/**
 * 把正文中的 RAG 引用编号 [n] 替换为可悬浮预览的 .rag-ref 元素（data 属性
 * 携带路径/行号/原文片段，tooltip 组件按 dataset 渲染）。
 * DOM 后处理而非字符串正则：跳过 CODE/PRE（代码里的 a[1] 不是引用），
 * 行内位置要求编号前一个字符非字母/数字/下划线（进一步排除数组下标误伤）。
 * 超出命中数的编号（模型幻觉的 [9]）不处理，保持原样。
 */
export function linkRagRefs(
  html: string,
  hits: { path: string; content: string; startLine: number }[],
): string {
  if (!html || !hits?.length) return html
  const doc = new DOMParser().parseFromString(html, 'text/html')
  const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT)
  const textNodes: Text[] = []
  while (walker.nextNode()) {
    const node = walker.currentNode as Text
    const tag = node.parentElement?.tagName
    if (tag === 'CODE' || tag === 'PRE') continue
    if (!/\[\d{1,2}\]/.test(node.data)) continue
    textNodes.push(node)
  }
  for (const node of textNodes) {
    const parts = node.data.split(/(\[\d{1,2}\])/)
    const frag = doc.createDocumentFragment()
    let lastChar = ''
    for (const part of parts) {
      const m = /^\[(\d{1,2})\]$/.exec(part)
      const hit = m ? hits[Number(m[1]) - 1] : undefined
      if (m && hit && !/[A-Za-z0-9_]/.test(lastChar)) {
        const span = doc.createElement('span')
        span.className = 'rag-ref'
        span.dataset.idx = m[1]
        span.dataset.path = hit.path
        span.dataset.line = String(hit.startLine)
        // 原文截断进 data 属性（tooltip 展示用；过长会使 DOM/序列化膨胀）
        span.dataset.quote = hit.content.length > 600 ? hit.content.slice(0, 600) + '…' : hit.content
        span.textContent = part
        frag.appendChild(span)
      } else {
        frag.appendChild(doc.createTextNode(part))
      }
      lastChar = part.slice(-1)
    }
    node.parentElement?.replaceChild(frag, node)
  }
  return doc.body.innerHTML
}
