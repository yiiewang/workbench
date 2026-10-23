// Markdown 渲染出的 <table> 需要一层滚动容器：
// 1) 宽表格横向滚动（边缘渐变提示由 CSS 实现）
// 2) 超高表格内部纵向滚动，thead 才能 sticky 常驻
// 注意：sticky 表头依赖该容器承载纵向滚动，若让外层文档滚动，横向滚动容器会阻断 sticky。
export function wrapMarkdownTables(root: HTMLElement | null | undefined): void {
  if (!root || typeof document === 'undefined') return
  root.querySelectorAll('table').forEach((table) => {
    // 已包裹（重复调用时跳过），避免在 watch 中反复嵌套
    if (table.parentElement?.classList.contains('md-table-scroll')) return
    const wrapper = document.createElement('div')
    wrapper.className = 'md-table-scroll'
    table.parentElement?.insertBefore(wrapper, table)
    wrapper.appendChild(table)
  })
}
