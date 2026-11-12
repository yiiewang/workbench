// AI 对话核心逻辑（组件无关）：状态 + 发送/流式/引用/会话持久化/消息操作。
// 由 PC 浮动面板（AiChat.vue）与移动端全屏对话页（MAI.vue）共用，
// 视图层各自实现（onScrollBottom 由组件注入，基于各自的消息列表 ref）。
import { ref, computed, watch } from 'vue'
import { loggedIn, currentUser } from '../stores/auth'
import {
  getAIStatus, chatStream, approveAITool,
  listAISessions, getAISession, createAISession, updateAISession, deleteAISession,
  type ChatMessage, type AISessionSummary, type AiPayloadMessage,
} from '../api/ai'
import { getFile, createDir, uploadFiles } from '../api/files'
import { showToast, isBinaryExt, extFromPath, copyToClipboard } from '../lib/common'
import { renderMarkdown } from '../lib/markdownRenderer'

function newId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return 'xxxxxxx-xxxx-4xxx'.replace(/x/g, () => Math.floor(Math.random() * 16).toString(16))
}

export function useAIChat(onScrollBottom?: () => void) {
  const aiEnabled = ref(false)
  const aiModel = ref('')
  const messages = ref<ChatMessage[]>([])
  const input = ref('')
  const streaming = ref(false)
  let abortCtrl: AbortController | null = null
  let sessionId = newId()

  // payload 展开后总条数预算（后端上限 30，留余量）：Agent 对话一条
  // "AI 回复"展开为 assistant(tool_calls) + N 条 tool 消息，按显示条数截断
  // 会超限，必须按展开后条数计
  const HISTORY_BUDGET = 28
  const CTX_TEXT_LIMIT = 20_000

  // ---- 消息渲染窗口：长会话只渲染最近 N 条（顶部"加载更早"逐批展开）----
  const INITIAL_VISIBLE = 5
  const LOAD_MORE_STEP = 10
  const visibleCount = ref(INITIAL_VISIBLE)
  const visibleMessages = computed(() => messages.value.slice(-visibleCount.value))
  const hiddenCount = computed(() => Math.max(0, messages.value.length - visibleCount.value))
  function showMore() {
    visibleCount.value += LOAD_MORE_STEP
  }

  const pendingCtxFiles = ref<string[]>([])
  const showFilePicker = ref(false)

  function scrollBottom() {
    onScrollBottom?.()
  }

  async function fetchStatus() {
    if (!loggedIn.value) {
      aiEnabled.value = false
      return
    }
    try {
      const st = await getAIStatus()
      aiEnabled.value = !!st.enabled
      aiModel.value = st.model || ''
    } catch {
      aiEnabled.value = false
    }
  }
  watch(loggedIn, fetchStatus, { immediate: true })

  function removeCtxFile(p: string) {
    pendingCtxFiles.value = pendingCtxFiles.value.filter(x => x !== p)
  }

  function onPickFiles(paths: string[]) {
    showFilePicker.value = false
    for (const p of paths) {
      if (isBinaryExt(extFromPath(p))) {
        showToast('二进制文件无法引用：' + p.split('/').pop())
        continue
      }
      if (!pendingCtxFiles.value.includes(p)) pendingCtxFiles.value.push(p)
    }
  }

  // 拉取待引用文件文本（不放 system：部分网关对 system 转发不稳，并入 user 消息必然可见）
  async function buildCtxText(files: string[]): Promise<string> {
    const parts: string[] = []
    for (const p of files) {
      try {
        const resp = await getFile(p)
        const text = await resp.text()
        if (!text) continue
        const truncated = text.length > CTX_TEXT_LIMIT
          ? text.slice(0, CTX_TEXT_LIMIT) + '\n…（内容过长，已截断）'
          : text
        parts.push(`—— 引用文件：${p} ——\n${truncated}`)
      } catch {
        showToast('读取文件失败，已忽略：' + p.split('/').pop())
      }
    }
    return parts.join('\n\n')
  }

  const NEW_CHAT_FLAG = 'workbench-ai-new-chat'
  const dbSessionId = ref<number | null>(null)
  const sessionsOpen = ref(false)
  const sessions = ref<AISessionSummary[]>([])
  const sessionsLoading = ref(false)

  let persisting = false
  async function persistSession() {
    if (persisting) return
    persisting = true
    try {
      const msgs = messages.value
      if (!msgs.length) return
      if (dbSessionId.value == null) {
        const firstUser = msgs.find(m => m.role === 'user')
        const data = await createAISession({
          title: (firstUser?.content || '新对话').slice(0, 30),
          messages: msgs,
        })
        dbSessionId.value = data.id
      } else {
        await updateAISession(dbSessionId.value, { messages: msgs })
      }
    } catch { /* 保存失败静默 */ } finally {
      persisting = false
    }
  }

  let lastPersistAt = 0
  function throttledPersist() {
    const now = Date.now()
    if (now - lastPersistAt < 3000) return
    lastPersistAt = now
    persistSession()
  }

  async function loadSessions() {
    sessionsLoading.value = true
    try {
      const data = await listAISessions()
      sessions.value = data?.sessions || []
    } catch {
      sessions.value = []
    } finally {
      sessionsLoading.value = false
    }
  }

  function toggleSessions() {
    sessionsOpen.value = !sessionsOpen.value
    if (sessionsOpen.value) loadSessions()
  }

  async function loadSession(id: number) {
    if (streaming.value) return
    try {
      const detail = await getAISession(id)
      messages.value = (detail?.messages || []).filter(m => m?.role && typeof m.content === 'string')
      dbSessionId.value = detail.id
      sessionId = newId()
      visibleCount.value = INITIAL_VISIBLE
      sessionsOpen.value = false
      try { sessionStorage.removeItem(NEW_CHAT_FLAG) } catch { /* 忽略 */ }
      scrollBottom()
    } catch (e: any) {
      showToast('载入会话失败：' + (e?.message || ''))
    }
  }

  async function removeSession(id: number, e: Event) {
    e.stopPropagation()
    try {
      await deleteAISession(id)
      sessions.value = sessions.value.filter(s => s.id !== id)
      if (dbSessionId.value === id) dbSessionId.value = null
    } catch {
      showToast('删除失败')
    }
  }

  async function maybeRestoreLastSession() {
    if (messages.value.length || dbSessionId.value != null) return
    try {
      if (sessionStorage.getItem(NEW_CHAT_FLAG)) return
    } catch { /* 存储不可用时继续尝试恢复 */ }
    try {
      const data = await listAISessions()
      const latest = data?.sessions?.[0]
      if (latest) {
        await loadSession(latest.id)
        try { sessionStorage.removeItem(NEW_CHAT_FLAG) } catch { /* 忽略 */ }
      }
    } catch { /* 恢复失败：保持空白新会话 */ }
  }

  function clearChat() {
    if (streaming.value) stopStreaming()
    messages.value = []
    visibleCount.value = INITIAL_VISIBLE
    sessionId = newId()
    dbSessionId.value = null
    try { sessionStorage.setItem(NEW_CHAT_FLAG, '1') } catch { /* 忽略 */ }
    sessionsOpen.value = false
  }

  function timeAgo(iso: string): string {
    const t = new Date(iso).getTime()
    if (!Number.isFinite(t)) return ''
    const diff = Date.now() - t
    if (diff < 60e3) return '刚刚'
    if (diff < 36e5) return Math.floor(diff / 60e3) + ' 分钟前'
    if (diff < 864e5) return Math.floor(diff / 36e5) + ' 小时前'
    return new Date(t).toLocaleDateString()
  }

  function stopStreaming() {
    abortCtrl?.abort()
    abortCtrl = null
  }

  async function send() {
    const text = input.value.trim()
    if (!text || streaming.value) return
    input.value = ''
    const ctxFiles = [...pendingCtxFiles.value]
    const userMsg: ChatMessage = { role: 'user', content: text }
    if (ctxFiles.length) userMsg.files = ctxFiles
    messages.value.push(userMsg)
    pendingCtxFiles.value = []
    await streamReply(userMsg)
  }

  // 展示态消息 → 上游 payload 消息：
  // 带工具调用的 assistant 消息展开为 assistant(tool_calls) + 逐条 tool(结果)，
  // 模型多轮决策需要看到自己调过什么、拿到了什么结果。
  function toPayloadMessages(msgs: ChatMessage[]): AiPayloadMessage[] {
    const out: AiPayloadMessage[] = []
    for (const m of msgs) {
      if (m.role === 'assistant' && m.toolCalls?.length) {
        out.push({
          role: 'assistant',
          content: m.content || '',
          tool_calls: m.toolCalls.map(t => ({
            id: t.id,
            type: 'function' as const,
            function: { name: t.name, arguments: t.arguments },
          })),
        })
        for (const t of m.toolCalls) {
          out.push({
            role: 'tool',
            tool_call_id: t.id,
            content: JSON.stringify(t.result ?? { error: 'no result' }),
          })
        }
      } else {
        out.push({ role: m.role, content: m.content })
      }
    }
    return out
  }

  // 历史按"轮"预算截断：user + 其回复是一轮（原子单位）。
  // 从尾部往前收集轮次，展开后总条数不超预算——避免把 assistant
  // 与其 tool 消息拆开（OpenAI 语义：tool 消息必须紧跟对应 tool_calls），
  // 也避免超过后端 aiMaxMessages 上限。
  function buildPayloadHistory(msgs: ChatMessage[]): AiPayloadMessage[] {
    const rounds: ChatMessage[][] = []
    for (const m of msgs) {
      if (m.role === 'user') rounds.push([m])
      else if (rounds.length) rounds[rounds.length - 1].push(m)
    }
    let count = 0
    let start = rounds.length
    for (let i = rounds.length - 1; i >= 0; i--) {
      const r = rounds[i]
      const cost = r.reduce((s, m) => s + (m.toolCalls?.length ? 1 + m.toolCalls.length : 1), 0)
      if (count + cost > HISTORY_BUDGET) break
      count += cost
      start = i
    }
    return toPayloadMessages(rounds.slice(start).flat())
  }

  async function streamReply(userMsg: ChatMessage) {
    const reply: ChatMessage = { role: 'assistant', content: '', reasoning: '', toolCalls: [] }
    messages.value.push(reply)
    // 关键：push 后取响应式代理再操作——直接改原始对象（raw）不会触发
    // 视图更新（审批按钮/工具卡片依赖此处的实时变更）
    const live = messages.value[messages.value.length - 1]
    streaming.value = true
    scrollBottom()

    abortCtrl = new AbortController()
    const qaId = newId()
    const ctxText = await buildCtxText(userMsg.files || [])
    const history = messages.value.slice(0, -2)
    const question = ctxText
      ? `【引用文件内容】\n${ctxText}\n【引用文件结束】\n\n我的问题：${userMsg.content}`
      : userMsg.content
    const payload: AiPayloadMessage[] = [
      ...buildPayloadHistory(history),
      { role: 'user', content: question },
    ]
    try {
      await chatStream(payload, {
        signal: abortCtrl.signal,
        qaId,
        sessionId,
        onReasoning: (delta) => {
          live.reasoning = (live.reasoning || '') + delta
          scrollBottom()
          throttledPersist()
        },
        onDelta: (delta) => {
          live.content += delta
          scrollBottom()
          throttledPersist()
        },
        onToolCall: (tc) => {
          live.toolCalls = live.toolCalls || []
          live.toolCalls.push({ id: tc.id, name: tc.name, arguments: tc.arguments })
          scrollBottom()
        },
        onToolResult: (tr) => {
          const tc = live.toolCalls?.find(t => t.id === tr.id)
          if (tc) tc.result = tr.result
          scrollBottom()
          throttledPersist()
        },
        onApprovalRequest: (ar) => {
          const tc = live.toolCalls?.find(t => t.id === ar.id)
          if (tc) tc.approval = 'pending'
          scrollBottom()
        },
        onApprovalResolved: (ar) => {
          // 超时场景由后端通知收尾；用户自己点击的场景与本地状态一致（幂等）
          const tc = live.toolCalls?.find(t => t.id === ar.id)
          if (tc && tc.approval === 'pending') tc.approval = ar.outcome
          scrollBottom()
        },
      })
    } catch (e: any) {
      if (e?.name === 'AbortError') {
        if (!live.content) live.content = '（已停止）'
        // 中断时未决的审批卡片定格为已取消（后端断开兜底会作废审批）
        for (const tc of live.toolCalls || []) {
          if (tc.approval === 'pending') tc.approval = 'declined'
        }
      } else {
        live.content = live.content || '⚠ ' + (e?.message || '请求失败')
        showToast(e?.message || 'AI 请求失败')
      }
    } finally {
      streaming.value = false
      abortCtrl = null
      scrollBottom()
      persistSession()
    }
  }

  // 外部触发提问（workbench:ai-ask 事件核心）：引用文件 + 提问文本 + 发送
  async function askWith(files: string[], question: string) {
    if (!loggedIn.value || !aiEnabled.value) return false
    sessionsOpen.value = false
    if (streaming.value) return false
    pendingCtxFiles.value = files
    if (question) input.value = question
    await send()
    return true
  }

  function copyMessage(m: ChatMessage) {
    copyToClipboard(m.content)
    showToast('已复制')
  }

  // 用户对写工具调用的审批决定（Human-in-the-loop）：
  // 先更新本地卡片状态（防重复点击），提交失败回滚为待确认允许重试
  async function approveToolCall(id: string, approved: boolean) {
    const mark = (val: 'pending' | 'approved' | 'declined') => {
      for (const m of messages.value) {
        const tc = m.toolCalls?.find(t => t.id === id)
        if (tc) tc.approval = val
      }
    }
    mark(approved ? 'approved' : 'declined')
    try {
      await approveAITool(id, approved)
    } catch (e: any) {
      showToast(e?.msg || e?.message || '提交失败，请重试')
      mark('pending')
    }
  }

  async function regenerate() {
    if (streaming.value || !messages.value.length) return
    const last = messages.value[messages.value.length - 1]
    if (last?.role !== 'assistant') return
    messages.value.pop()
    let userIdx = messages.value.length - 1
    while (userIdx >= 0 && messages.value[userIdx].role !== 'user') userIdx--
    if (userIdx < 0) return
    await streamReply(messages.value[userIdx])
  }

  async function saveReplyAsFile(m: ChatMessage, idx: number) {
    let userIdx = idx - 1
    while (userIdx >= 0 && messages.value[userIdx].role !== 'user') userIdx--
    const topic = (userIdx >= 0 ? messages.value[userIdx].content : 'AI 回复')
      .slice(0, 20).replace(/[\\/:*?"<>|#\s]+/g, '-').replace(/^-+|-+$/g, '') || 'AI 回复'
    const d = new Date()
    const p = (n: number) => String(n).padStart(2, '0')
    const name = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}-${topic}.md`
    try {
      try { await createDir('/ai') } catch { /* 目录已存在时忽略 */ }
      const file = new File([`> 来源对话：${topic}\n\n${m.content}\n`], name, { type: 'text/markdown' })
      await uploadFiles('/ai', [file])
      showToast('已保存到 /ai/' + name)
    } catch (e: any) {
      showToast('保存失败：' + (e?.msg || e?.message || ''))
    }
  }

  const userInitial = computed(() => (currentUser.value?.userName || 'U').charAt(0).toUpperCase())

  function isStreamingTail(i: number): boolean {
    // i 为 visibleMessages 索引（尾部与 messages 对齐）
    return streaming.value && i === visibleMessages.value.length - 1 && visibleMessages.value[i]?.role === 'assistant'
  }

  const openReasoning = ref(new Set<number>())
  function toggleReasoning(i: number) {
    const s = new Set(openReasoning.value)
    if (s.has(i)) s.delete(i)
    else s.add(i)
    openReasoning.value = s
  }

  // markdown → HTML（共享渲染层，含脚本类标签剔除兜底）
  function renderMd(text: string): string {
    return renderMarkdown(text)
  }

  function onInputKeydown(e: KeyboardEvent) {
    if (e.key === '@' && !e.isComposing) {
      e.preventDefault()
      showFilePicker.value = true
      return
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    }
  }

  return {
    aiEnabled, aiModel, messages, input, streaming,
    visibleMessages, hiddenCount, showMore,
    pendingCtxFiles, showFilePicker,
    dbSessionId, sessions, sessionsOpen, sessionsLoading,
    openReasoning,
    fetchStatus, maybeRestoreLastSession,
    send, stopStreaming, askWith,
    removeCtxFile, onPickFiles,
    toggleSessions, loadSessions, loadSession, removeSession, clearChat, timeAgo,
    copyMessage, regenerate, saveReplyAsFile, approveToolCall,
    isStreamingTail, toggleReasoning, renderMd, userInitial,
    onInputKeydown,
  }
}
