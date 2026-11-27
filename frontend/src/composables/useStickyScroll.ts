// 消息列表 stick-to-bottom：流式增量默认跟随底部，用户上滚即暂停、滚回底部恢复。
// 不能用纯距离阈值判定——高频增量拉底下用户单次滚轮滚不出逃逸距离，
// 表现为"完全滚不动"，因此以 scroll 事件更新跟随状态。
import { ref, nextTick, type Ref } from 'vue'

/** 距底多少 px 内视为贴底 */
const STICK_THRESHOLD = 40
/** 程序拉底自身触发的 scroll 事件忽略窗口（ms） */
const PROGRAM_SCROLL_WINDOW = 120

export function useStickyScroll(el: Ref<HTMLElement | null>) {
  const stickBottom = ref(true)
  let lastProgScroll = 0

  /** scroll 事件入口：更新跟随状态（程序拉底引起的事件不算用户交互） */
  function onStickScroll() {
    if (Date.now() - lastProgScroll < PROGRAM_SCROLL_WINDOW) return
    const node = el.value
    if (!node) return
    stickBottom.value = node.scrollHeight - node.scrollTop - node.clientHeight < STICK_THRESHOLD
  }

  /** force=true 无条件拉底（发消息 / 载入会话 / 流结束） */
  async function scrollToBottom(force = false) {
    await nextTick()
    const node = el.value
    if (!node) return
    if (!force && !stickBottom.value) return
    stickBottom.value = true // 强制拉底场景恢复跟随
    lastProgScroll = Date.now()
    node.scrollTop = node.scrollHeight
  }

  return { stickBottom, onStickScroll, scrollToBottom }
}
