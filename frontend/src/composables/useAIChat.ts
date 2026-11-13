// AI 对话核心逻辑（组件无关）：状态 + 发送/流式/引用/会话持久化/消息操作。
// 由 PC 浮动面板（AiChat.vue）与移动端全屏对话页（MAI.vue）共用，
// 视图层各自实现（onScrollBottom 由组件注入，基于各自的消息列表 ref）。
import { ref, computed, watch } from 'vue'
import { loggedIn, currentUser } from '../stores/auth'
import {
  getAIStatus, chatStream, approveAITool,
  listAISessions, getAISession, createAISession, updateAISession, deleteAISession,
  type ChatMessage, type AISessionSummary, type AiPayloadMessage,
  type MsgStep, type ToolCallView,
} from '../api/ai'
import { getFile, createDir, uploadFiles } from '../api/files'
import { showToast, isBinaryExt, extFromPath, copyToClipboard } from '../lib/common'
import { renderMarkdown, linkRagRefs } from '../lib/markdownRenderer'

function newId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return 'xxxxxxx-xxxx-4xxx'.replace(/x/g, () => Math.floor(Math.random() * 16).toString(16))
}

// 消息渲染段：连续的过程步骤（思考/工具）归为一个可折叠过程块，正文片段独立成段
export interface MsgSegment {
  kind: 'process' | 'text'
  steps: MsgStep[]
  text?: string
}

export function useAIChat(onScrollBottom?: (force?: boolean) => void) {
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

  // scrollBottom(force?) 滚动到消息列表底部。force=true 用于用户主动行为
  // （发消息/载入会话/流结束）无条件拉底；流式增量回调不传 force——由视图层
  // 做贴底检测（用户上滚阅读思考过程时不打断），解决"思考过程无法滚动"。
  function scrollBottom(force = false) {
    onScrollBottom?.(force)
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
  let persistQueued = false
  async function persistSession() {
    // 防重入 + 排队补一轮：流末的最终保存（含 usage）可能与节流保存竞态，
    // 直接丢弃会让库停留在中途快照（usage 丢失、刷新后看不到 token 消耗）。
    // 被挡时置队列标记，当前保存完成后自动再存一轮拿到最新数据
    if (persisting) {
      persistQueued = true
      return
    }
    persisting = true
    try {
      for (;;) {
        persistQueued = false
        const msgs = messages.value
        if (!msgs.length) return
        try {
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
          // 新内容已落库："点过新对话"的空白语义失效——不清除的话，
          // 该 tab 内刷新页面将永远不自动恢复历史（标记只在明确空白时有效）
          try { sessionStorage.removeItem(NEW_CHAT_FLAG) } catch { /* 忽略 */ }
        } catch { /* 保存失败静默 */ return }
        if (!persistQueued) return // 保存期间无新请求：完成
      }
    } finally {
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
      scrollBottom(true)
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
  // content 为空且无工具调用的 assistant 消息（历史中断的脏数据）跳过——
  // 上游拒绝空 content 的 assistant 消息，会导致后续提问整体 400。
  function toPayloadMessages(msgs: ChatMessage[]): AiPayloadMessage[] {
    const out: AiPayloadMessage[] = []
    for (const m of msgs) {
      if (m.role === 'assistant' && !m.content && !m.toolCalls?.length) continue
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
    const reply: ChatMessage = { role: 'assistant', content: '', reasoning: '', toolCalls: [], steps: [] }
    messages.value.push(reply)
    // 关键：push 后取响应式代理再操作——直接改原始对象（raw）不会触发
    // 视图更新（审批按钮/工具卡片依赖此处的实时变更）
    const live = messages.value[messages.value.length - 1]
    streaming.value = true
    scrollBottom(true)

    // 时序步骤维护：reasoning/tool/text 按发生顺序追加进 steps（相邻同类增量合并
    // 到同一步），渲染层按此还原"思考 → 调工具 → 分析 → 回答"的时间线
    function appendStep(kind: MsgStep['kind'], text: string): MsgStep {
      const steps = live.steps!
      const last = steps[steps.length - 1]
      if (last && last.kind === kind) {
        last.text = (last.text || '') + text
        return last
      }
      const step: MsgStep = { kind, text }
      steps.push(step)
      return step
    }

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
          appendStep('reasoning', delta)
          scrollBottom()
          throttledPersist()
        },
        onRagHits: (hits) => {
          live.ragHits = hits
        },
        onUsage: (u) => {
          live.usage = u
        },
        onDelta: (delta) => {
          live.content += delta
          appendStep('text', delta)
          scrollBottom()
          throttledPersist()
        },
        onToolCall: (tc) => {
          live.toolCalls = live.toolCalls || []
          const call: ToolCallView = { id: tc.id, name: tc.name, arguments: tc.arguments }
          live.toolCalls.push(call)
          live.steps!.push({ kind: 'tool', call })
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
        if (!live.content) {
          live.content = '（已停止）'
          appendStep('text', '（已停止）')
        }
        // 中断时未决的审批卡片定格为已取消（后端断开兜底会作废审批）
        for (const tc of live.toolCalls || []) {
          if (tc.approval === 'pending') tc.approval = 'declined'
        }
      } else {
        const msg = '⚠ ' + (e?.message || '请求失败')
        live.content = live.content || msg
        if (!live.steps!.some(s => s.kind === 'text')) appendStep('text', msg)
        showToast(e?.message || 'AI 请求失败')
      }
    } finally {
      streaming.value = false
      abortCtrl = null
      // 兜底：中断/静默断流后无正文（哪怕已有思考与工具调用）——补"（已中断）"
      // 文本：让用户知道回复被打断；空 assistant 落库会让下次提问 400
      if (!live.content) {
        live.content = '（已中断）'
        const last = live.steps![live.steps!.length - 1]
        if (last?.kind === 'text') last.text = '（已中断）'
        else live.steps!.push({ kind: 'text', text: '（已中断）' })
      }
      scrollBottom(true)
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

  // ---- 回复时序分段：steps（或缺省时从扁平字段归一化重建）→ 交替的
  // "过程块（思考+工具，可折叠）"与"正文段"。text 步骤切断过程块——
  // Agent 中间轮的说明文本自然显示在两块过程之间。
  function legacySteps(m: ChatMessage): MsgStep[] {
    const steps: MsgStep[] = []
    if (m.reasoning) steps.push({ kind: 'reasoning', text: m.reasoning })
    for (const c of m.toolCalls || []) steps.push({ kind: 'tool', call: c })
    if (m.content) steps.push({ kind: 'text', text: m.content })
    return steps
  }

  function messageSegments(m: ChatMessage): MsgSegment[] {
    const steps = m.steps?.length ? m.steps : legacySteps(m)
    const out: MsgSegment[] = []
    let cur: MsgStep[] = []
    for (const s of steps) {
      if (s.kind === 'text') {
        if (cur.length) out.push({ kind: 'process', steps: cur })
        cur = []
        out.push({ kind: 'text', steps: [], text: s.text || '' })
      } else {
        cur.push(s)
      }
    }
    if (cur.length) out.push({ kind: 'process', steps: cur })
    return out
  }

  // markdown → HTML（共享渲染层，含脚本类标签剔除兜底）；
  // 带 RAG 命中时把正文 [n] 编号替换为可悬浮预览的引用元素
  function renderMessageMd(m: ChatMessage): string {
    const html = renderMarkdown(m.content || '')
    return m.ragHits?.length ? linkRagRefs(html, m.ragHits) : html
  }

  // 正文片段渲染（时序分段中的 text 段），同样应用 RAG 引用装饰
  function renderTextMd(text: string, m: ChatMessage): string {
    const html = renderMarkdown(text || '')
    return m.ragHits?.length ? linkRagRefs(html, m.ragHits) : html
  }

  // token 消耗展示：>1000 缩写为 K（气泡底部小字）
  function fmtTokens(n: number): string {
    if (n <= 0) return '0'
    if (n < 1000) return String(n)
    return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K'
  }

  function usageText(m: ChatMessage): string {
    const u = m.usage
    if (!u || !u.totalTokens) return ''
    return `prompt ${fmtTokens(u.promptTokens)} · completion ${fmtTokens(u.completionTokens)} · 共 ${fmtTokens(u.totalTokens)} tokens`
  }

  // assistant 消息是否有任何产出（正文/思考/工具/时序步骤任一）：
  // 中断的消息可能只有思考与工具调用、无正文——操作行（重新生成）仍应显示
  function hasReplyContent(m: ChatMessage): boolean {
    return !!(m.content || m.reasoning || m.toolCalls?.length || m.steps?.length)
  }

  // 模型是否实际引用了检索片段：正文出现 [n]（n ≤ 命中数）才算——
  // 检索命中 ≠ 被引用（模型判定无关而忽略时，引用来源列表不应展示垃圾命中）
  function hasCitedRefs(m: ChatMessage): boolean {
    if (!m.ragHits?.length) return false
    const re = /\[(\d{1,2})\]/g
    let match: RegExpExecArray | null
    while ((match = re.exec(m.content || ''))) {
      if (Number(match[1]) <= m.ragHits.length) return true
    }
    return false
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
    fetchStatus, maybeRestoreLastSession,
    send, stopStreaming, askWith,
    removeCtxFile, onPickFiles,
    toggleSessions, loadSessions, loadSession, removeSession, clearChat, timeAgo,
    copyMessage, regenerate, saveReplyAsFile, approveToolCall,
    isStreamingTail, messageSegments, renderMessageMd, renderTextMd, usageText, hasCitedRefs, hasReplyContent, userInitial,
    onInputKeydown,
  }
}
