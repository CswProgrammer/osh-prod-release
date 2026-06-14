<script setup>
import { ref, watch } from 'vue'
import {
  NLayout,
  NLayoutHeader,
  NLayoutContent,
  NButton,
  useMessage,
} from 'naive-ui'
import { TimeOutline, PulseOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import DeployHero from '../components/DeployHero.vue'
import StepTimeline from '../components/StepTimeline.vue'
import LogTerminal from '../components/LogTerminal.vue'
import RunHistoryDrawer from '../components/RunHistoryDrawer.vue'
import { useDeployRun } from '../composables/useDeployRun.js'

const showHistory = ref(false)
const {
  health,
  run,
  history,
  busy,
  error,
  progress,
  statusLabel,
  start,
} = useDeployRun()

const message = useMessage()

watch(error, (v) => {
  if (v) message.error(v)
})

watch(
  () => run.value?.status,
  (s, prev) => {
    if (s === 'success' && prev === 'running') message.success('绿环境部署完成')
    if (s === 'failed') message.error('发布失败，请查看日志')
  }
)
</script>

<template>
  <NLayout class="shell">
    <NLayoutHeader class="topbar glass" bordered>
      <div class="brand">
        <div class="logo">OSH</div>
        <div>
          <div class="brand-title">OSH Prod Release</div>
          <div class="brand-sub">绿环境一键部署</div>
        </div>
      </div>
      <div class="topbar-actions">
        <span v-if="health" class="health">
          <NIcon :component="PulseOutline" :color="health.mock_mode ? '#f59e0b' : '#22c55e'" />
          {{ health.mock_mode ? '演示模式' : `绿环境 · ${health.prod_host}` }}
        </span>
        <NButton quaternary @click="showHistory = true">
          <template #icon><NIcon :component="TimeOutline" /></template>
          历史
        </NButton>
      </div>
    </NLayoutHeader>

    <NLayoutContent class="content">
      <div class="container">
        <DeployHero
          :busy="busy"
          :progress="progress"
          :status-label="statusLabel"
          :run-id="run?.id"
          :run-mode="run?.mode"
          :mock-mode="health?.mock_mode"
          :green-url="health?.green_url"
          @start="start"
        />
        <div class="main-grid">
          <StepTimeline :steps="run?.steps || []" />
          <LogTerminal :lines="run?.log || []" />
        </div>
      </div>
    </NLayoutContent>
  </NLayout>

  <RunHistoryDrawer v-model:show="showHistory" :history="history" />
</template>

<style scoped>
.shell {
  min-height: 100vh;
  background: transparent;
}
.topbar {
  height: 64px;
  padding: 0 1.5rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 1rem 1.25rem 0;
  border-radius: 14px !important;
}
.brand {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}
.logo {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: linear-gradient(135deg, #2563eb, #22c55e);
  display: grid;
  place-items: center;
  font-weight: 800;
  font-size: 0.75rem;
}
.brand-title {
  font-weight: 700;
  font-size: 0.95rem;
}
.brand-sub {
  font-size: 0.75rem;
  color: var(--muted);
}
.topbar-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}
.health {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.85rem;
  color: var(--muted);
}
.content {
  padding: 1rem 1.25rem 2rem;
}
.container {
  max-width: 1180px;
  margin: 0 auto;
}
.main-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1.25rem;
  align-items: start;
}
@media (max-width: 960px) {
  .main-grid {
    grid-template-columns: 1fr;
  }
}
</style>
