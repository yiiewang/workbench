// 定时任务执行记录：加载、展开、运行中步骤轮询（AiJobsView 专用）。
// job_runs 先落 running，前端用 pipelineRunId 轮询 pipeline_runs.steps 补齐实时步骤。
import { ref, onBeforeUnmount } from 'vue'
import { listAIJobRuns, type AIJobRun } from '../api/aiJobs'
import { getPipelineRun, type PipelineStepResult } from '../api/pipelines'

export function useJobRuns(currentJobId: () => number | null) {
  const runs = ref<AIJobRun[]>([])
  const runsLoading = ref(false)
  const runsOpen = ref(new Set<number>())
  // 运行中任务的逐步快照（key = job_runs.id）
  const runLiveSteps = ref<Record<number, PipelineStepResult[] | undefined>>({})
  const runPollTimers: Record<number, number> = {}

  function parseRunSteps(raw?: string): PipelineStepResult[] {
    if (!raw) return []
    try {
      const v = JSON.parse(raw)
      return Array.isArray(v) ? (v as PipelineStepResult[]) : []
    } catch {
      return []
    }
  }

  /** 展开区展示的步骤：运行中优先用轮询快照，否则用落库摘要 */
  function runStepList(r: AIJobRun): PipelineStepResult[] {
    if (r.status === 'running') {
      const live = runLiveSteps.value[r.id]
      if (live && live.length) return live
    }
    return parseRunSteps(r.stepsSummary)
  }

  async function loadRuns(id: number) {
    runsLoading.value = true
    try {
      const data = await listAIJobRuns(id)
      runs.value = data?.runs || []
    } catch {
      runs.value = []
    } finally {
      runsLoading.value = false
    }
  }

  function stopRunPoll(id: number) {
    const t = runPollTimers[id]
    if (t) {
      clearInterval(t)
      delete runPollTimers[id]
    }
  }

  function stopAllRunPolls() {
    for (const id of Object.keys(runPollTimers)) stopRunPoll(Number(id))
  }

  async function refreshLiveRun(r: AIJobRun) {
    if (!r.pipelineRunId) return
    try {
      const detail = await getPipelineRun(r.pipelineRunId)
      runLiveSteps.value = { ...runLiveSteps.value, [r.id]: detail.steps }
      if (detail.status !== 'running') {
        stopRunPoll(r.id)
        const id = currentJobId()
        if (id) loadRuns(id)
      }
    } catch { /* 轮询失败静默，下一轮再试 */ }
  }

  function startRunPoll(r: AIJobRun) {
    if (!r.pipelineRunId || runPollTimers[r.id]) return
    refreshLiveRun(r)
    runPollTimers[r.id] = window.setInterval(() => refreshLiveRun(r), 3000)
  }

  /** 展开/收起某条执行记录；展开运行中的记录时启动轮询 */
  function toggleRun(i: number) {
    const s = new Set(runsOpen.value)
    const opening = !s.has(i)
    const r = runs.value[i]
    if (opening) s.add(i)
    else {
      s.delete(i)
      if (r) stopRunPoll(r.id)
    }
    runsOpen.value = s
    if (opening && r?.status === 'running') startRunPoll(r)
  }

  /** 切换选中任务时清空列表与所有轮询 */
  function resetRuns() {
    stopAllRunPolls()
    runs.value = []
    runLiveSteps.value = {}
  }

  onBeforeUnmount(stopAllRunPolls)

  return { runs, runsLoading, runsOpen, loadRuns, runStepList, toggleRun, resetRuns, stopAllRunPolls }
}
