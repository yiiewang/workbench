// AI 对话核心逻辑（组件无关）：状态 + 发送/流式/引用/会话持久化/消息操作。
// 由 PC 浮动面板（AiChat.vue）与移动端全屏对话页（MAI.vue）共用，
// 视图层各自实现（onScrollBottom 由组件注入，基于各自的消息列表 ref）。
import { ref, computed, watch, reactive, shallowReactive, type InjectionKey } from 'vue'
import { loggedIn, currentUser } from '../stores/auth'
import {
  getAIStatus, chatStream, approveAITool, getAIRun,
  listAISessions, getAISession, createAISession, appendAISessionMessages, deleteAISession,
  type ChatMessage, type AISessionSummary, type AiPayloadMessage,
  type MsgStep, type ToolCallView, type AIRun, type AIGateway,
  type RagHit, type TokenUsage,
} from '../api/ai'
import { getFile, createDir, uploadFiles } from '../api/files'
import { showToast, errMsg, isBinaryExt, extFromPath, copyToClipboard } from '../lib/common'
import { renderMarkdown, linkRagRefs } from '../lib/markdownRenderer'

function newId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return 'xxxxxxx-xxxx-4xxx'.replace(/x/g, () => Math.floor(Math.random() * 16).toString(16))
}

// 消息级持久化的稳定主键：历史会话沿用后端返回的 id，新建/恢复路径缺失时补一个。
// 统一在这里兜底，避免每个消息创建点都要记得赋值。
function msgId(m: ChatMessage): string {
  return m.clientMsgId || (m.clientMsgId = newId())
}

// 消息渲染段：连续的过程步骤（思考/工具）归为一个可折叠过程块，正文片段独立成段
export interface MsgSegment {
  kind: 'process' | 'text'
  steps: MsgStep[]
  text?: string
}

// 一个会话的全部可变状态：消息、落库 ID、后台 run、流控与持久化防重入。
// 多个会话可各自并行生成；切换会话只换 currentKey，进行中的流仍写自己会话的消息对象。
interface ChatSession {
  key: string
  messages: ChatMessage[]
  dbSessionId: number | null
  sessionId: string
  streaming: boolean
  abortCtrl: AbortController | null
  runId: string | null
  resumeStopped: boolean
  resumedRunId: string | null
  persisting: boolean
  persistQueued: boolean
  lastPersistAt: number
  /** clientMsgId -> 上次成功落库的消息 JSON，用于只提交变动的消息 */
  saved: Map<string, string>
  /** regenerate 待提交的截断锚点：删掉该消息之后的旧回复 */
  truncateAfter: string | null
}

interface PendingRun { id: string; key: string; sessionId: string; dbSessionId: number | null }

export interface ModelSettings {
  /** 上下文预算（token）：历史消息按该预算裁剪，留 25% 给输出与工具结果 */
  contextTokens: number
  /** 单次回答输出上限，0 = 网关默认 */
  maxTokens: number
  /** 推理强度，空 = 不传 */
  reasoningEffort: '' | 'low' | 'medium' | 'high' | 'max'
  /** 模型网关 id，空 = 默认网关 */
  gateway: string
}

const MODEL_SETTINGS_KEY = 'ai.chat.model-settings'
const DEFAULT_MODEL_SETTINGS: ModelSettings = { contextTokens: 128_000, maxTokens: 0, reasoningEffort: '', gateway: '' }

function loadModelSettings(): ModelSettings {
  try {
    const raw = localStorage.getItem(MODEL_SETTINGS_KEY)
    if (!raw) return { ...DEFAULT_MODEL_SETTINGS }
    const v = JSON.parse(raw)
    return {
      contextTokens: Number(v.contextTokens) > 0 ? Number(v.contextTokens) : DEFAULT_MODEL_SETTINGS.contextTokens,
      maxTokens: Number(v.maxTokens) > 0 ? Number(v.maxTokens) : 0,
      reasoningEffort: ['low', 'medium', 'high', 'max'].includes(v.reasoningEffort) ? v.reasoningEffort : '',
      gateway: typeof v.gateway === 'string' ? v.gateway : '',
    }
  } catch {
    return { ...DEFAULT_MODEL_SETTINGS }
  }
}

// ---- 流式回复的展示态维护 ----

// 思考过程只保留最近 4k 字：reasoning 与 steps 各存一份，全量落库会到 100KB+，
// 回放时也没人从头读；更早的内容折叠成一行提示。
const REASONING_KEEP = 4_000
const FOLD_RE = /^…（前 \d+ 字已折叠）\n/

function keepReasoningTail(text: string): string {
  // 超过 1.5 倍才重组，避免每个流式 delta 都做一次字符串替换
  if (text.length <= REASONING_KEEP * 1.5) return text
  const body = text.replace(FOLD_RE, '')
  return `…（前 ${body.length - REASONING_KEEP} 字已折叠）\n` + body.slice(-REASONING_KEEP)
}

// 时序步骤维护：reasoning/tool/text 按发生顺序追加进 steps（相邻同类增量合并到同一步），
// 渲染层按此还原"思考 → 调工具 → 分析 → 回答"的时间线
function appendStep(steps: MsgStep[], kind: MsgStep['kind'], text: string): MsgStep {
  const last = steps[steps.length - 1]
  if (last && last.kind === kind) {
    last.text = (last.text || '') + text
    return last
  }
  const step: MsgStep = { kind, text }
  steps.push(step)
  return step
}

// 流失败分类：只有「用户主动停止」和「服务端业务错误」不可恢复。刷新/网络中断时
// 后台 run 仍在跑（runCtx 与请求连接解耦），必须保留 pending 记录交给恢复轮询，
// 也不能把 reader 的原生错误写成「⚠」占位污染会话快照。
type StreamFailure = 'resumable' | 'aborted' | 'error'

function classifyStreamFailure(e: any, runId: string | null, stopped: boolean): StreamFailure {
  if (runId != null && !stopped && !e?.business) return 'resumable'
  return e?.name === 'AbortError' ? 'aborted' : 'error'
}

export function useAIChat(onScrollBottom?: (force?: boolean) => void) {
  const aiEnabled = ref(false)
  const aiModel = ref('')
  const gateways = ref<AIGateway[]>([])
  const modelSettings = ref<ModelSettings>(loadModelSettings())
  const currentModel = computed(() => {
    const g = gateways.value.find(x => x.id === modelSettings.value.gateway)
    return g?.model || aiModel.value
  })
  function updateModelSettings(patch: Partial<ModelSettings>) {
    modelSettings.value = { ...modelSettings.value, ...patch }
    try {
      localStorage.setItem(MODEL_SETTINGS_KEY, JSON.stringify(modelSettings.value))
    } catch { /* 本地存储不可用时仅内存生效 */ }
  }
  // shallowReactive 让会话增删也进响应式依赖，streamingSessions 才能随新会话更新
  const chatSessions = shallowReactive(new Map<string, ChatSession>())
  function newSession(msgs: ChatMessage[] = [], dbId: number | null = null): ChatSession {
    const list = reactive<ChatMessage[]>(msgs)
    // 历史会话载入时自带 clientMsgId，先记为已落库，避免切回后重发整份消息
    const saved = new Map<string, string>()
    for (const m of list) if (m.clientMsgId) saved.set(m.clientMsgId, JSON.stringify(m))
    const s = shallowReactive<ChatSession>({
      key: 'n:' + newId(), messages: list, dbSessionId: dbId,
      sessionId: newId(), streaming: false, abortCtrl: null, runId: null,
      resumeStopped: false, resumedRunId: null,
      persisting: false, persistQueued: false, lastPersistAt: 0,
      saved, truncateAfter: null,
    })
    chatSessions.set(s.key, s)
    return s
  }
  // 会话落库后 key 不变，按 dbSessionId 反查内存态，避免同一会话出现两份状态
  function findByDbId(id: number): ChatSession | undefined {
    for (const s of chatSessions.values()) if (s.dbSessionId === id) return s
    return undefined
  }
  const currentKey = ref('')
  const current = computed(() => chatSessions.get(currentKey.value)!)
  const messages = ref<ChatMessage[]>([])
  const input = ref('')
  // 视图只看当前展示会话：导出名/类型不变，PC 面板与移动端组件零改动
  const streaming = computed(() => current.value.streaming)
  const dbSessionId = computed(() => current.value.dbSessionId)
  // 会话列表用：哪些已落库会话正在生成（切走后仍在后台跑的也算）
  const streamingSessions = computed(() => {
    const ids = new Set<number>()
    for (const s of chatSessions.values()) {
      if (s.streaming && s.dbSessionId != null) ids.add(s.dbSessionId)
    }
    return ids
  })
  const initialSession = newSession()
  currentKey.value = initialSession.key
  messages.value = initialSession.messages

  // 后台运行恢复：每个会话的 run 记录（runId + 归属）存 sessionStorage，
  // 刷新后遍历记录、按会话重建内存态并各自轮询补齐回复
  const RUN_ID_KEY = 'workbench-ai-active-run'
  const RESUME_POLL_MS = 300 // 与后端 300ms 落库节流对齐，增量持续可见
  const resuming = new Set<string>() // 正在恢复的 runId，避免重复启动轮询
  function readPendingRuns(): PendingRun[] {
    try {
      const parsed = JSON.parse(sessionStorage.getItem(RUN_ID_KEY) || '[]')
      return Array.isArray(parsed) ? parsed : []
    } catch { return [] }
  }
  function writePendingRuns(list: PendingRun[]) {
    try {
      if (list.length) sessionStorage.setItem(RUN_ID_KEY, JSON.stringify(list))
      else sessionStorage.removeItem(RUN_ID_KEY)
    } catch { /* 存储不可用时仅内存态 */ }
  }
  function setRunId(s: ChatSession, id: string | null) {
    const prev = s.runId
    s.runId = id
    // 刷新后会话 key 会变，结束轮询时还要按 runId 删掉旧记录
    const list = readPendingRuns().filter(r => r.key !== s.key && r.id !== prev)
    if (id) list.push({ id, key: s.key, sessionId: s.sessionId, dbSessionId: s.dbSessionId })
    writePendingRuns(list)
  }
  // 流可能先于 3s 节流保存就已开跑，会话首次落库后补上归属
  function backfillPendingRuns(s: ChatSession) {
    if (s.dbSessionId == null) return
    const list = readPendingRuns()
    let changed = false
    for (const r of list) {
      if (r.key === s.key && r.dbSessionId == null) {
        r.dbSessionId = s.dbSessionId
        changed = true
      }
    }
    if (changed) writePendingRuns(list)
  }
  function forgetRun(id: string) {
    writePendingRuns(readPendingRuns().filter(r => r.id !== id))
  }

  // payload 展开后总条数预算（后端上限 30，留余量）：Agent 对话一条
  // "AI 回复"展开为 assistant(tool_calls) + N 条 tool 消息，按显示条数截断
  // 会超限，必须按展开后条数计
  const HISTORY_BUDGET = 28
  // 引用文件只带前 2k 字：全文并入 user 消息会撑大每轮上下文，也让模型倾向
  // 直接复述而不定位；不足的部分由模型用 read_file 按需读取。
  const CTX_TEXT_LIMIT = 2_000

  // ---- 消息渲染窗口：长会话只渲染最近 N 条（顶部"加载更早"逐批展开）----
  const INITIAL_VISIBLE = 5
  const LOAD_MORE_STEP = 10
  const visibleCount = ref(INITIAL_VISIBLE)
  const visibleMessages = computed(() => messages.value.slice(-visibleCount.value))
  const hiddenCount = computed(() => Math.max(0, messages.value.length - visibleCount.value))
  function showMore() {
    visibleCount.value += LOAD_MORE_STEP
  }

  // 切换当前展示的会话：只换引用，不打断其他会话正在进行的流
  function activate(s: ChatSession) {
    currentKey.value = s.key
    messages.value = s.messages
    visibleCount.value = INITIAL_VISIBLE
    scrollBottom(true)
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
      gateways.value = st.gateways || []
      // 默认选中配置里的 default；本地存过但已失效（网关被删）时回退默认
      const ids = gateways.value.map(g => g.id)
      if (!modelSettings.value.gateway || !ids.includes(modelSettings.value.gateway)) {
        const def = st.default && ids.includes(st.default) ? st.default : (ids[0] || '')
        if (def) updateModelSettings({ gateway: def })
      }
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
          ? text.slice(0, CTX_TEXT_LIMIT) + `\n…（内容过长，已截断；完整内容请用 read_file 读取 ${p}）`
          : text
        parts.push(`—— 引用文件：${p} ——\n${truncated}`)
      } catch {
        showToast('读取文件失败，已忽略：' + p.split('/').pop())
      }
    }
    return parts.join('\n\n')
  }

  const NEW_CHAT_FLAG = 'workbench-ai-new-chat'
  const sessionsOpen = ref(false)
  const sessions = ref<AISessionSummary[]>([])
  const sessionsLoading = ref(false)

  // 持久化绑定会话：只提交与上次落库快照不同的消息；后台会话的流也只写自己的记录。
  // 展示态消息在流式过程中会被反复增量修改（正文/思考/工具结果/usage），
  // 用 JSON 快照 diff 而不是在各回调里打脏标记，任何字段变化都能被捕获。
  async function persistSession(s: ChatSession) {
    // 防重入 + 排队补一轮：流末的最终保存（含 usage）可能与节流保存竞态，
    // 直接丢弃会让库停留在中途快照（usage 丢失、刷新后看不到 token 消耗）。
    // 被挡时置队列标记，当前保存完成后自动再存一轮拿到最新数据
    if (s.persisting) {
      s.persistQueued = true
      return
    }
    s.persisting = true
    try {
      for (;;) {
        s.persistQueued = false
        const msgs = s.messages
        if (!msgs.length) return
        const changed: { id: string; json: string }[] = []
        for (const m of msgs) {
          const id = msgId(m)
          const json = JSON.stringify(m)
          if (s.saved.get(id) !== json) changed.push({ id, json })
        }
        if (!changed.length && !s.truncateAfter) return
        // 用提交时的快照而非当前对象：保存期间新产生的增量留到下一轮 diff 继续提交
        const payload = changed.map(c => JSON.parse(c.json) as ChatMessage)
        try {
          if (s.dbSessionId == null) {
            const firstUser = msgs.find(m => m.role === 'user')
            const data = await createAISession({
              title: (firstUser?.content || '新对话').slice(0, 30),
              messages: payload,
            })
            s.dbSessionId = data.id
            backfillPendingRuns(s)
          } else {
            await appendAISessionMessages(s.dbSessionId, {
              messages: payload,
              truncateAfter: s.truncateAfter || undefined,
            })
          }
          for (const c of changed) s.saved.set(c.id, c.json)
          s.truncateAfter = null
          // 新内容已落库："点过新对话"的空白语义失效——不清除的话，
          // 该 tab 内刷新页面将永远不自动恢复历史（标记只在明确空白时有效）。
          // 后台会话的保存不该抹掉用户刚点的新对话空白语义
          if (s === current.value) {
            try { sessionStorage.removeItem(NEW_CHAT_FLAG) } catch { /* 忽略 */ }
          }
        } catch { /* 保存失败静默 */ return }
        if (!s.persistQueued) return // 保存期间无新请求：完成
      }
    } finally {
      s.persisting = false
    }
  }

  function throttledPersist(s: ChatSession) {
    const now = Date.now()
    if (now - s.lastPersistAt < 3000) return
    s.lastPersistAt = now
    persistSession(s)
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

  // 载入会话但不切换展示：批量恢复后台 run 时给每个会话重建内存态
  async function ensureSession(id: number): Promise<ChatSession> {
    const cached = findByDbId(id)
    if (cached) return cached
    const detail = await getAISession(id)
    // 并发载入时可能已被别的路径建好，复用之，避免同一会话两份状态
    return findByDbId(id) || newSession(
      (detail?.messages || []).filter(m => m?.role && typeof m.content === 'string'),
      detail.id,
    )
  }

  async function loadSession(id: number) {
    // 已在内存的会话直接切过去（哪怕别的会话正在生成），保留其进行中的流
    try {
      const s = await ensureSession(id)
      activate(s)
      sessionsOpen.value = false
      try { sessionStorage.removeItem(NEW_CHAT_FLAG) } catch { /* 忽略 */ }
      resumePendingRuns()
    } catch (e: any) {
      showToast('载入会话失败：' + (errMsg(e, '')))
    }
  }

  async function removeSession(id: number, e: Event) {
    e.stopPropagation()
    try {
      await deleteAISession(id)
      sessions.value = sessions.value.filter(s => s.id !== id)
      const s = findByDbId(id)
      if (s) {
        // 先判后删：delete 之后 current 已解析为 undefined，
        // 再比较 s === current.value 恒为 false，删掉当前会话不会切新会话，
        // currentKey 悬空后 streaming/dbSessionId 等 computed 直接抛错
        const isCurrent = s === current.value
        s.abortCtrl?.abort()
        chatSessions.delete(s.key)
        if (isCurrent) activate(newSession())
      }
      writePendingRuns(readPendingRuns().filter(r => r.dbSessionId !== id))
    } catch {
      showToast('删除失败')
    }
  }

  async function maybeRestoreLastSession() {
    // 面板每次打开都扫一遍：后台 run 属于哪个会话就在哪个会话里恢复，
    // 不依赖用户手动切过去；「新对话」标记下同样要恢复其它会话的 run
    resumePendingRuns()
    if (messages.value.length || current.value.dbSessionId != null) return
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

  // 把后台 run 的结构化事件补齐到所属会话的回复消息（按 id 去重，可反复调用）。
  // 正文由 pollRun 按 delta 增量维护，这里不再用全量 result 覆盖。
  function applyRunSnapshot(run: AIRun, live: ChatMessage, s: ChatSession) {
    const calls = live.toolCalls || (live.toolCalls = [])
    for (const e of run.events || []) {
      const d: any = e.data || {}
      if (e.event === 'tool_call') {
        const fn = d.function || {}
        const id = String(d.id || '')
        if (!id || calls.some(c => c.id === id)) continue
        calls.push({ id, name: String(fn.name || ''), arguments: String(fn.arguments || '') })
      } else if (e.event === 'tool_result') {
        const tc = calls.find(c => c.id === d.id)
        if (tc && tc.result === undefined) tc.result = d.result
      } else if (e.event === 'rag_hits') {
        if (!live.ragHits?.length && Array.isArray(d.hits)) live.ragHits = d.hits
      } else if (e.event === 'usage' && typeof d.total_tokens === 'number') {
        live.usage = {
          promptTokens: Number(d.prompt_tokens) || 0,
          completionTokens: Number(d.completion_tokens) || 0,
          totalTokens: Number(d.total_tokens) || 0,
        }
      }
    }
    // 时序步骤交给渲染层从扁平字段归一化重建（避免半截 steps 与完整正文不一致）
    live.steps = undefined
    if (s === current.value) scrollBottom()
    throttledPersist(s)
  }

  // 按 offset 游标持续拉增量正文：首次以服务端累积正文为准（清掉本地半截或
  // 「（已停止）」占位），之后只追加 delta，刷新后就是接着往外输出。
  async function pollRun(id: string, live: ChatMessage, s: ChatSession) {
    if (s.resumedRunId === id) return
    s.resumedRunId = id
    s.streaming = true
    const deadline = Date.now() + 15 * 60 * 1000
    let cursor = 0
    let first = true
    try {
      for (;;) {
        if (s.resumeStopped) return
        let run: AIRun
        try { run = await getAIRun(id, cursor) } catch { return }
        const delta = run.delta ?? ''
        if (first) {
          live.content = delta
          first = false
        } else if (delta) {
          live.content += delta
        }
        cursor = run.offset ?? cursor
        applyRunSnapshot(run, live, s)
        if (run.status !== 'running' || Date.now() > deadline) {
          setRunId(s, null)
          if (!live.content) live.content = run.error || '（后台运行已结束，但无回复内容）'
          return
        }
        await new Promise(r => setTimeout(r, RESUME_POLL_MS))
      }
    } finally {
      s.resumedRunId = null
      s.streaming = false
      if (s === current.value) scrollBottom(true)
      persistSession(s)
    }
  }

  // 刷新后遍历所有 pending run：按 dbSessionId 重建对应会话，各自并行轮询补齐。
  // 不再按 org+user 找任意 running：多会话并行时那会把别的会话的回复串进来。
  async function resumeOne(p: PendingRun) {
    const dbId = p.dbSessionId
    // 尚无归属的记录可能刚开跑（3s 内未落库），等 backfill 补上，先跳过
    if (resuming.has(p.id) || dbId == null) return
    resuming.add(p.id)
    try {
      let run: AIRun
      try { run = await getAIRun(p.id) } catch { return }
      // 归属校验：run 的上游 sessionId 必须与发起该流的会话一致
      if (run.sessionId !== p.sessionId) { forgetRun(p.id); return }
      const updated = new Date(run.updatedAt).getTime()
      if (run.status !== 'running' && (!Number.isFinite(updated) || Date.now() - updated > 30 * 60 * 1000)) {
        forgetRun(p.id)
        return
      }
      let s: ChatSession
      try { s = await ensureSession(dbId) } catch { return }
      // 该会话已有活跃流（本 tab 正在 SSE 生成）或已在轮询，无需恢复
      if (s.streaming || s.resumedRunId) return
      // 保留原记录（含刷新前的 sessionId）：重写会让二次恢复的归属校验失效
      s.runId = run.id
      let live = s.messages[s.messages.length - 1]
      if (!live || live.role !== 'assistant') {
        s.messages.push({ role: 'assistant', content: '', toolCalls: [], steps: [] })
        live = s.messages[s.messages.length - 1]
      }
      s.resumeStopped = false
      await pollRun(run.id, live, s)
    } finally {
      resuming.delete(p.id)
    }
  }

  function resumePendingRuns() {
    for (const p of readPendingRuns()) void resumeOne(p).catch(() => {})
  }

  function clearChat() {
    // 新建会话而非清空数组：原会话若仍在生成，其流继续写自己的消息并落库
    activate(newSession())
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
    const s = current.value
    s.abortCtrl?.abort()
    s.abortCtrl = null
    s.resumeStopped = true // 恢复轮询也随"停止"一起停（服务端 run 仍会跑完）
  }

  async function send() {
    const text = input.value.trim()
    const s = current.value
    if (!text || s.streaming) return
    input.value = ''
    const ctxFiles = [...pendingCtxFiles.value]
    const userMsg: ChatMessage = { role: 'user', content: text }
    if (ctxFiles.length) userMsg.files = ctxFiles
    s.messages.push(userMsg)
    pendingCtxFiles.value = []
    await streamReply(userMsg, s)
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
  // 粗略 token 估算：约 4 字符/token；工具参数也计入（模型要看到才能续接调用）。
  function estimateTokens(m: ChatMessage): number {
    let chars = (m.content || '').length
    for (const tc of m.toolCalls || []) chars += (tc.arguments || '').length
    return Math.ceil(chars / 4)
  }

  function buildPayloadHistory(msgs: ChatMessage[]): AiPayloadMessage[] {
    const rounds: ChatMessage[][] = []
    for (const m of msgs) {
      if (m.role === 'user') rounds.push([m])
      else if (rounds.length) rounds[rounds.length - 1].push(m)
    }
    // 条数上限（后端 aiMaxMessages）与 token 预算取小；token 预算留 25% 给输出/工具
    const tokenBudget = Math.floor(modelSettings.value.contextTokens * 0.75)
    let count = 0
    let tokens = 0
    let start = rounds.length
    for (let i = rounds.length - 1; i >= 0; i--) {
      const r = rounds[i]
      const cost = r.reduce((s, m) => s + (m.toolCalls?.length ? 1 + m.toolCalls.length : 1), 0)
      const roundTokens = r.reduce((s, m) => s + estimateTokens(m), 0)
      if (count + cost > HISTORY_BUDGET || tokens + roundTokens > tokenBudget) break
      count += cost
      tokens += roundTokens
      start = i
    }
    return toPayloadMessages(rounds.slice(start).flat())
  }

  // 上游消息体：历史按 token 预算裁剪 + 引用文件全文并入当前 user 消息
  // （不放 system：部分网关对 system 转发不稳，并入 user 必然可见）。
  async function buildStreamPayload(s: ChatSession, userMsg: ChatMessage): Promise<AiPayloadMessage[]> {
    const ctxText = await buildCtxText(userMsg.files || [])
    const history = s.messages.slice(0, -2)
    const question = ctxText
      ? `【引用文件内容】\n${ctxText}\n【引用文件结束】\n\n我的问题：${userMsg.content}`
      : userMsg.content
    return [
      ...buildPayloadHistory(history),
      { role: 'user', content: question },
    ]
  }

  // 流事件 → 展示态：正文/思考增量、工具卡片与审批状态、usage/RAG 命中。
  // 每个分片即时滚动并节流落库（断连期间库里也能看到增量）。
  function buildStreamHandlers(live: ChatMessage, s: ChatSession, sc: () => void) {
    return {
      onReasoning: (delta: string) => {
        live.reasoning = keepReasoningTail((live.reasoning || '') + delta)
        const step = appendStep(live.steps!, 'reasoning', delta)
        step.text = keepReasoningTail(step.text || '')
        sc()
        throttledPersist(s)
      },
      onRagHits: (hits: RagHit[]) => {
        live.ragHits = hits
      },
      onUsage: (u: TokenUsage) => {
        live.usage = u
      },
      onDelta: (delta: string) => {
        live.content += delta
        appendStep(live.steps!, 'text', delta)
        sc()
        throttledPersist(s)
      },
      onToolCall: (tc: { id: string; name: string; arguments: string }) => {
        live.toolCalls = live.toolCalls || []
        const call: ToolCallView = { id: tc.id, name: tc.name, arguments: tc.arguments }
        live.toolCalls.push(call)
        live.steps!.push({ kind: 'tool', call })
        sc()
      },
      onToolResult: (tr: { id: string; name: string; result: unknown }) => {
        const tc = live.toolCalls?.find(t => t.id === tr.id)
        if (tc) tc.result = tr.result
        sc()
        throttledPersist(s)
      },
      onApprovalRequest: (ar: { id: string; name: string; arguments: string }) => {
        const tc = live.toolCalls?.find(t => t.id === ar.id)
        if (tc) tc.approval = 'pending'
        sc()
      },
      onApprovalResolved: (ar: { id: string; outcome: 'approved' | 'declined' | 'timeout' }) => {
        // 超时场景由后端通知收尾；用户自己点击的场景与本地状态一致（幂等）
        const tc = live.toolCalls?.find(t => t.id === ar.id)
        if (tc && tc.approval === 'pending') tc.approval = ar.outcome
        sc()
      },
    }
  }

  // 不可恢复失败的用户可见收尾：主动停止 → 「（已停止）」+ 未决审批定格；
  // 其它错误 → 「⚠ …」占位 + toast。
  function applyStreamFailure(live: ChatMessage, failure: StreamFailure, e: any) {
    if (failure === 'aborted') {
      if (!live.content) {
        live.content = '（已停止）'
        appendStep(live.steps!, 'text', '（已停止）')
      }
      // 中断时未决的审批卡片定格为已取消（后端断开兜底会作废审批）
      for (const tc of live.toolCalls || []) {
        if (tc.approval === 'pending') tc.approval = 'declined'
      }
      return
    }
    const msg = '⚠ ' + (errMsg(e, '请求失败'))
    live.content = live.content || msg
    if (!live.steps!.some(step => step.kind === 'text')) appendStep(live.steps!, 'text', msg)
    showToast(errMsg(e, 'AI 请求失败'))
  }

  // 流收尾：可恢复（后台 run 仍在跑）则保持流式态交给 pollRun 补齐，
  // 否则落定流式态并清 pending；无正文时补占位，避免空 assistant 落库导致下次 400。
  function finishStream(s: ChatSession, live: ChatMessage, recoverable: boolean, completed: boolean, runId: string | null) {
    s.abortCtrl = null
    if (recoverable && !completed && runId) {
      void pollRun(runId, live, s)
    } else {
      s.streaming = false
      // 用户主动停止/业务错误也不留 pending，否则面板重开会被 resumeOne
      // 当成待恢复任务、把已停止的生成又补回来
      setRunId(s, null)
      if (!live.content) {
        live.content = '（已中断）'
        const last = live.steps![live.steps!.length - 1]
        if (last?.kind === 'text') last.text = '（已中断）'
        else live.steps!.push({ kind: 'text', text: '（已中断）' })
      }
    }
    if (s === current.value) scrollBottom(true)
    persistSession(s)
  }

  // 一次流完全绑定发起它的会话：即使中途切到别的会话，增量仍写本会话的 live，
  // 持久化/停止/恢复也只作用于本会话。流状态用局部变量，避免多流互相覆盖。
  async function streamReply(userMsg: ChatMessage, s: ChatSession) {
    const reply: ChatMessage = { role: 'assistant', content: '', reasoning: '', toolCalls: [], steps: [] }
    s.messages.push(reply)
    // 关键：push 后取响应式代理再操作——直接改原始对象（raw）不会触发
    // 视图更新（审批按钮/工具卡片依赖此处的实时变更）
    const live = s.messages[s.messages.length - 1]
    s.streaming = true
    // 后台会话的增量不抢当前展示会话的滚动
    const sc = () => { if (s === current.value) scrollBottom() }
    scrollBottom(true)

    const abortCtrl = new AbortController()
    s.abortCtrl = abortCtrl
    s.resumeStopped = false
    let completed = false
    let recoverable = true
    let runId: string | null = null
    const qaId = newId()
    const payload = await buildStreamPayload(s, userMsg)
    try {
      await chatStream(payload, {
        signal: abortCtrl.signal,
        qaId,
        sessionId: s.sessionId,
        maxTokens: modelSettings.value.maxTokens || undefined,
        reasoningEffort: modelSettings.value.reasoningEffort || undefined,
        gateway: modelSettings.value.gateway || undefined,
        onRunId: (id) => { runId = id; setRunId(s, id) },
        onDone: () => { completed = true },
        ...buildStreamHandlers(live, s, sc),
      })
    } catch (e: any) {
      const failure = classifyStreamFailure(e, runId, s.resumeStopped)
      recoverable = failure === 'resumable'
      if (!recoverable) applyStreamFailure(live, failure, e)
    } finally {
      finishStream(s, live, recoverable, completed, runId)
    }
  }

  // 外部触发提问（workbench:ai-ask 事件核心）：引用文件 + 提问文本 + 发送
  async function askWith(files: string[], question: string) {
    if (!loggedIn.value || !aiEnabled.value) return false
    sessionsOpen.value = false
    if (current.value.streaming) return false
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
      showToast(errMsg(e, '提交失败，请重试'))
      mark('pending')
    }
  }

  async function regenerate() {
    const s = current.value
    if (s.streaming || !s.messages.length) return
    const last = s.messages[s.messages.length - 1]
    if (last?.role !== 'assistant') return
    s.messages.pop()
    let userIdx = s.messages.length - 1
    while (userIdx >= 0 && s.messages[userIdx].role !== 'user') userIdx--
    if (userIdx < 0) return
    // 追加式存储不会删除内存里已 pop 的旧回复，靠截断锚点让后端删掉它及其后的消息
    const anchor = s.messages[s.messages.length - 1]
    if (anchor) s.truncateAfter = msgId(anchor)
    await streamReply(s.messages[userIdx], s)
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
      showToast('保存失败：' + (errMsg(e, '')))
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
    aiEnabled, aiModel, currentModel, gateways, modelSettings, updateModelSettings, messages, input, streaming,
    visibleMessages, hiddenCount, showMore,
    pendingCtxFiles, showFilePicker,
    dbSessionId, streamingSessions, sessions, sessionsOpen, sessionsLoading,
    fetchStatus, maybeRestoreLastSession,
    send, stopStreaming, askWith,
    removeCtxFile, onPickFiles,
    toggleSessions, loadSessions, loadSession, removeSession, clearChat, timeAgo,
    copyMessage, regenerate, saveReplyAsFile, approveToolCall,
    isStreamingTail, messageSegments, renderMessageMd, renderTextMd, usageText, hasCitedRefs, hasReplyContent, userInitial,
    onInputKeydown,
  }
}

// 消息视图上下文：父组件 provide 自己的实例，AiMessage 用 inject 复用同一份状态与操作函数，
// 避免子组件再调一次 useAIChat 生成独立的会话状态（审批/复制/重新生成必须作用于当前会话）。
export type AIChatContext = ReturnType<typeof useAIChat>
export const aiChatKey: InjectionKey<AIChatContext> = Symbol('aiChat')
