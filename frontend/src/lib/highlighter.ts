// ============================================================
// 代码高亮统一入口：highlight.js 实例 + 语言注册 + 按行切分 token
//   - markdown 代码块（markdownRenderer）与文件预览（CodePreview）共用同一份实例，
//     避免两处各自注册、语言表不一致
//   - highlightLines 把 hljs 输出的 HTML 转成「带类名 + 原文」的 token 再按行切分，
//     解决跨行 <span>（块注释、模板字符串、多行字符串）被硬截断导致的错色/标签残留
// ============================================================
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

export { hljs }

// 扩展名 → hljs 语言名（未列出的走纯文本，仍可折叠）
const EXT_LANG: Record<string, string> = {
  bash: 'bash', sh: 'bash', zsh: 'bash', shell: 'bash',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', tsx: 'typescript',
  json: 'json',
  py: 'python',
  go: 'go',
  md: 'markdown', markdown: 'markdown',
  xml: 'xml', html: 'xml', htm: 'xml', vue: 'xml', svg: 'xml', xsl: 'xml',
  css: 'css', scss: 'css', less: 'css',
  yml: 'yaml', yaml: 'yaml',
  sql: 'sql',
}

/** 走代码预览（行号 + 高亮 + 折叠）的扩展名；json/xml/md/css 等有独立 viewer 的不在内 */
const CODE_EXTS = new Set([
  'go', 'py', 'js', 'mjs', 'cjs', 'jsx', 'ts', 'tsx',
  'c', 'h', 'cc', 'cpp', 'cxx', 'hpp', 'hh', 'cs',
  'java', 'kt', 'kts', 'scala', 'rs', 'rb', 'php', 'swift', 'lua', 'pl', 'pm', 'r', 'm', 'mm',
  'sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd',
  'yml', 'yaml', 'toml', 'ini', 'cfg', 'conf', 'properties', 'gradle', 'groovy',
  'sql', 'css', 'scss', 'less', 'scpt',
  'proto', 'graphql', 'gql', 'tf', 'hcl',
])

/** 扩展名对应的 hljs 语言名，未注册返回 null（按纯文本渲染） */
export function langForExt(ext: string): string | null {
  const lang = EXT_LANG[String(ext || '').toLowerCase()]
  return lang && hljs.getLanguage(lang) ? lang : null
}

/** 该扩展名是否走代码预览组件 */
export function isCodeExt(ext: string): boolean {
  return CODE_EXTS.has(String(ext || '').toLowerCase())
}

export interface CodeToken {
  text: string
  cls?: string
}

const NAMED_ENTITIES: Record<string, string> = {
  amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0',
}

function decodeEntities(src: string): string {
  return src.replace(/&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z]+);/g, (m, g: string) => {
    if (g[0] === '#') {
      const code = g[1] === 'x' || g[1] === 'X' ? parseInt(g.slice(2), 16) : parseInt(g.slice(1), 10)
      return Number.isFinite(code) && code >= 0 ? String.fromCodePoint(code) : m
    }
    return NAMED_ENTITIES[g] !== undefined ? NAMED_ENTITIES[g] : m
  })
}

// hljs 输出只有 <span class="..."> 嵌套 + 文本，按标签栈还原每个文本片段所属类名
function parseTokens(html: string): CodeToken[] {
  const out: CodeToken[] = []
  const stack: string[] = []
  const re = /<\/?span[^>]*>|[^<]+/g
  let m: RegExpExecArray | null
  while ((m = re.exec(html)) !== null) {
    const chunk = m[0]
    if (chunk[0] === '<') {
      if (chunk[1] === '/') { stack.pop(); continue }
      const cm = /class="([^"]*)"/.exec(chunk)
      stack.push(cm ? cm[1] : '')
      continue
    }
    const text = decodeEntities(chunk)
    if (!text) continue
    const cls = stack.filter(Boolean).join(' ')
    out.push(cls ? { text, cls } : { text })
  }
  return out
}

/**
 * 把源码高亮后按行切分：返回与源码行一一对应的 token 二维数组。
 * lang 为空（未注册语言）时直接按行切成纯文本 token，不做高亮。
 */
export function highlightLines(code: string, lang: string | null): CodeToken[][] {
  const src = String(code || '').replace(/\r\n?/g, '\n')
  if (!lang) {
    return src.split('\n').map(line => (line ? [{ text: line }] : []))
  }
  const html = hljs.highlight(src, { language: lang }).value
  const lines: CodeToken[][] = [[]]
  for (const token of parseTokens(html)) {
    const parts = token.text.split('\n')
    for (let i = 0; i < parts.length; i++) {
      if (i > 0) lines.push([])
      if (parts[i]) lines[lines.length - 1].push({ text: parts[i], cls: token.cls })
    }
  }
  return lines
}
