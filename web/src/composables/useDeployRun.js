import { computed, onMounted, onUnmounted, ref } from 'vue'
import { fetchHealth, fetchRun, fetchRuns, startDeploy, streamRun } from '../api/deploy.js'

export function useDeployRun() {
  const health = ref(null)
  const run = ref(null)
  const history = ref([])
  const busy = ref(false)
  const error = ref('')
  let es = null
  let pollTimer = null
  let activeRunId = null

  const progress = computed(() => {
    if (!run.value?.steps?.length) return 0
    const active = run.value.steps.filter((s) => s.status !== 'skipped')
    if (!active.length) return 0
    const done = active.filter((s) => s.status === 'success').length
    const running = active.some((s) => s.status === 'running')
    const partial = done + (running ? 0.35 : 0)
    return Math.min(100, Math.round((partial / active.length) * 100))
  })

  const statusLabel = computed(() => {
    if (!run.value) return '空闲'
    const map = { running: '进行中', success: '成功', failed: '失败', pending: '等待' }
    return map[run.value.status] || run.value.status
  })

  function stopPoll() {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  function closeStream() {
    if (es) {
      es.close()
      es = null
    }
  }

  function applyRun(data) {
    run.value = data
    if (data.status === 'success' || data.status === 'failed') {
      busy.value = false
      activeRunId = null
      stopPoll()
      closeStream()
      loadHistory()
    }
  }

  function startPoll(runId) {
    stopPoll()
    pollTimer = setInterval(async () => {
      if (!busy.value || !runId) return
      try {
        applyRun(await fetchRun(runId))
      } catch (_) {
        /* ignore transient fetch errors while task runs */
      }
    }, 2000)
  }

  function attachRun(runId) {
    activeRunId = runId
    closeStream()
    es = streamRun(runId, applyRun)
    startPoll(runId)
  }

  async function loadHealth() {
    health.value = await fetchHealth()
  }

  async function loadHistory() {
    history.value = await fetchRuns()
  }

  async function resumeRunningJob() {
    const running = history.value.find((r) => r.status === 'running')
    if (!running) return
    const detail = await fetchRun(running.id)
    // Do not attach to zombie jobs (no log progress after server restart)
    if (detail.status !== 'running') {
      run.value = detail
      return
    }
    busy.value = true
    activeRunId = running.id
    run.value = detail
    attachRun(running.id)
  }

  async function start(mode) {
    error.value = ''
    busy.value = true
    try {
      const { run_id } = await startDeploy(mode)
      run.value = await fetchRun(run_id)
      attachRun(run_id)
    } catch (e) {
      error.value = e.message || String(e)
      busy.value = false
      stopPoll()
      closeStream()
    }
  }

  onMounted(async () => {
    await loadHealth()
    await loadHistory()
    await resumeRunningJob()
  })

  onUnmounted(() => {
    stopPoll()
    closeStream()
  })

  return {
    health,
    run,
    history,
    busy,
    error,
    progress,
    statusLabel,
    start,
    loadHistory,
  }
}
