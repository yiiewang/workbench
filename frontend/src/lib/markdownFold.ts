// Markdown 标题：生成锚点 id + 收集大纲 + 折叠交互
// 说明：marked v12 默认不输出 heading id，所以在 v-html 落地后于 DOM 上补 id，
// 这样不依赖 marked 的 renderer API 版本差异，也顺带得到大纲数据。

export interface MdHeading {
  level: number
  text: string
  id: string
}

// GitHub 风格 slug：小写、空白转 -、去掉标点，保留中文/字母/数字
function slugify(text: string): string {
  const s = (text || '')
    .toLowerCase()
    .trim()
    .replace(/\s+/g, '-')
    .replace(/[^\p{L}\p{N}_-]/gu, '')
    .replace(/-{2,}/g, '-')
    .replace(/^-+|-+$/g, '')
  return s || 'section'
}

function makeUnique(base: string, used: Map<string, number>): string {
  const n = used.get(base) ?? 0
  used.set(base, n + 1)
  return n === 0 ? base : `${base}-${n}`
}

// 给所有标题补 id、插入折叠按钮、绑定折叠交互；返回大纲（供目录使用）
export function setupHeadingFold(root: HTMLElement | null | undefined): MdHeading[] {
  if (!root) return []
  const headings = Array.from(root.querySelectorAll<HTMLElement>('h1,h2,h3,h4,h5,h6'))
  if (!headings.length) return []

  const used = new Map<string, number>()
  const outline: MdHeading[] = []

  headings.forEach((h) => {
    const level = Number(h.tagName.slice(1))
    const text = (h.textContent || '').replace(/^[+\-−]\s*/, '')
    if (!h.id) h.id = makeUnique(slugify(text), used)
    outline.push({ level, text, id: h.id })

    // 该标题的 section：后续兄弟元素，直到遇到 level 更小或相等的下一个标题
    const section: HTMLElement[] = []
    let node = h.nextElementSibling
    while (node) {
      const tag = node.tagName.toLowerCase()
      if (/^h[1-6]$/.test(tag) && Number(tag.slice(1)) <= level) break
      // 记录归属，供"跳转到被折叠的标题"时逐级展开（同一元素可归属多层标题）
      const owners = node.getAttribute('data-fold-owners')
      node.setAttribute('data-fold-owners', owners ? `${owners} ${h.id}` : h.id)
      section.push(node as HTMLElement)
      node = node.nextElementSibling
    }
    if (!section.length) return // 空 section 没有折叠意义（例如文末标题）

    // 幂等：重复调用不重复插入按钮
    let btn = h.querySelector<HTMLButtonElement>('.md-fold-btn')
    if (!btn) {
      btn = document.createElement('button')
      btn.type = 'button'
      btn.className = 'md-fold-btn'
      btn.title = 'Toggle section'
      btn.setAttribute('aria-label', 'Toggle section')
      h.insertBefore(btn, h.firstChild)
      h.classList.add('md-foldable')

      btn.addEventListener('click', (e) => {
        e.preventDefault()
        e.stopPropagation()
        const folded = h.classList.toggle('md-folded')
        section.forEach((el) => el.classList.toggle('md-fold-hidden', folded))
        if (btn) btn.textContent = folded ? '+' : '\u2212'
      })
    }
  })

  return outline
}

// 目标处于折叠区时，逐级展开它所属的标题（保留更内层的折叠状态）
function expandAncestors(root: HTMLElement, target: HTMLElement): void {
  const owners = new Set<string>()
  let node: HTMLElement | null = target
  while (node && node !== root) {
    const v = node.getAttribute('data-fold-owners')
    if (v) v.split(' ').forEach((o) => o && owners.add(o))
    node = node.parentElement
  }
  owners.forEach((id) => {
    const h = root.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`)
    if (!h || !h.classList.contains('md-folded')) return
    h.querySelector<HTMLButtonElement>('.md-fold-btn')?.click()
  })
}

// 定位到指定 id：先展开所属折叠区，再平滑滚动
export function scrollToId(root: HTMLElement | null | undefined, id: string): boolean {
  if (!root || !id) return false
  const target = root.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`)
  if (!target) return false
  expandAncestors(root, target)
  target.scrollIntoView({ behavior: 'smooth', block: 'start' })
  return true
}
