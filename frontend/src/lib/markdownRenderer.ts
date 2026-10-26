// 全局 marked 定制（marked 是单例，配置一次即对文件预览与 todo 预览同时生效）：
//   - 代码块走 highlight.js 高亮（注册常用语言，避免引入全量语言包）
//   - 支持 ==高亮== 语法
// 原先这套配置写在 useTodoApp 的 setup 里，会隐式影响文件预览；现在收敛到统一渲染层。
import { marked } from 'marked'
import hljs from 'highlight.js/lib/core'
import 'highlight.js/styles/github.css'
import bash from 'highlight.js/lib/languages/bash'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import json from 'highlight.js/lib/languages/json'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import markdown from 'highlight.js/lib/languages/markdown'
import xml from 'highlight.js/lib/languages/xml'
import cssLang from 'highlight.js/lib/languages/css'
import yaml from 'highlight.js/lib/languages/yaml'
import sql from 'highlight.js/lib/languages/sql'
import shell from 'highlight.js/lib/languages/shell'

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('python', python)
hljs.registerLanguage('go', go)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('css', cssLang)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('shell', shell)

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
