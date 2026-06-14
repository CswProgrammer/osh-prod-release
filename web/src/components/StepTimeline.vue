<script setup>
import { computed } from 'vue'
import { NTimeline, NTimelineItem, NTag } from 'naive-ui'
import {
  CheckmarkCircleOutline,
  CloseCircleOutline,
  EllipseOutline,
  RemoveOutline,
  SyncOutline,
} from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'

const props = defineProps({
  steps: { type: Array, default: () => [] },
})

const visibleSteps = computed(() => props.steps)

function tagType(status) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'error'
  if (status === 'running') return 'warning'
  if (status === 'skipped') return 'default'
  return 'default'
}

function statusLabel(status) {
  const map = {
    success: 'success',
    failed: 'failed',
    running: 'running',
    skipped: '已跳过',
    pending: 'pending',
  }
  return map[status] || status
}

function icon(status) {
  if (status === 'success') return CheckmarkCircleOutline
  if (status === 'failed') return CloseCircleOutline
  if (status === 'running') return SyncOutline
  if (status === 'skipped') return RemoveOutline
  return EllipseOutline
}

function iconColor(status) {
  if (status === 'success') return '#22c55e'
  if (status === 'failed') return '#ef4444'
  if (status === 'running') return '#f59e0b'
  if (status === 'skipped') return '#475569'
  return '#64748b'
}
</script>

<template>
  <div class="timeline-wrap glass">
    <h3>发布步骤</h3>
    <NTimeline v-if="visibleSteps.length" size="large">
      <NTimelineItem
        v-for="step in visibleSteps"
        :key="step.id"
        :class="{ 'step-skipped': step.status === 'skipped' }"
        :type="step.status === 'failed' ? 'error' : step.status === 'success' ? 'success' : step.status === 'running' ? 'warning' : 'default'"
      >
        <template #icon>
          <NIcon
            :component="icon(step.status)"
            :size="20"
            :color="iconColor(step.status)"
            :class="{ spin: step.status === 'running' }"
          />
        </template>
        <div class="step-head">
          <span class="step-title">{{ step.title }}</span>
          <NTag size="tiny" :type="tagType(step.status)" :bordered="false">{{ statusLabel(step.status) }}</NTag>
        </div>
        <p v-if="step.message" class="step-msg">{{ step.message }}</p>
      </NTimelineItem>
    </NTimeline>
    <p v-else class="empty-hint">点击「一键部署绿环境」后，此处显示各步骤状态</p>
  </div>
</template>

<style scoped>
.timeline-wrap {
  padding: 1.25rem 1.5rem;
  height: 100%;
}
h3 {
  margin: 0 0 1rem;
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.step-skipped {
  opacity: 0.55;
}
.step-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}
.step-title {
  font-weight: 600;
  font-size: 0.92rem;
}
.step-msg {
  margin: 0.35rem 0 0;
  font-size: 0.8rem;
  color: var(--muted);
  font-family: var(--mono);
  word-break: break-all;
}
.empty-hint {
  margin: 2rem 0;
  text-align: center;
  color: var(--muted);
  font-size: 0.9rem;
}
.spin {
  animation: spin 1.2s linear infinite;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
