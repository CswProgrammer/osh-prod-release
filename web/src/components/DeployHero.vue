<script setup>
import { computed } from 'vue'
import { NButton, NProgress, NPopconfirm, NSpace, NTag } from 'naive-ui'
import { RocketOutline, FlashOutline, CodeSlashOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'

const props = defineProps({
  busy: Boolean,
  progress: Number,
  statusLabel: String,
  runId: String,
  runMode: String,
  mockMode: Boolean,
  greenUrl: String,
})

const modeLabel = computed(() => {
  const map = {
    standard: '完整部署（含 25 备份上传网盘）',
    skip_backup: '跳过备份（不更新网盘）',
    code_only: '仅更代码',
  }
  return map[props.runMode] || props.runMode || ''
})

defineEmits(['start'])

const ringStatus = computed(() => {
  if (props.statusLabel === '成功') return 'success'
  if (props.statusLabel === '失败') return 'error'
  if (props.busy) return 'warning'
  return 'default'
})
</script>

<template>
  <section class="hero glass">
    <div class="hero-grid">
      <div class="hero-left">
        <div class="eyebrow">
          <NTag size="small" :bordered="false" type="success">25 → 149 绿</NTag>
          <NTag size="small" :bordered="false" type="info">不动蓝项目</NTag>
          <NTag v-if="mockMode" size="small" :bordered="false" type="warning">演示模式</NTag>
        </div>
        <h1>OSH 一键部署绿环境</h1>
        <p class="subtitle">
          25 备份上传网盘 → 149 下载并更新<strong>绿环境</strong>前后端（:28080）→ HTTP 验收
        </p>
        <p v-if="greenUrl" class="hint">
          部署后访问：<a :href="greenUrl" target="_blank" rel="noopener">{{ greenUrl }}</a>
        </p>
        <div class="meta">
          <span>任务 <code>{{ runId || '—' }}</code></span>
          <span class="dot-sep">·</span>
          <span :class="['status', statusLabel === '成功' ? 'ok' : statusLabel === '失败' ? 'err' : '']">
            {{ statusLabel }}
          </span>
          <template v-if="runMode">
            <span class="dot-sep">·</span>
            <NTag size="small" :type="runMode === 'standard' ? 'success' : 'warning'" :bordered="false">
              {{ modeLabel }}
            </NTag>
          </template>
        </div>
        <NSpace class="actions" :size="12">
          <NPopconfirm @positive-click="$emit('start', 'standard')">
            <template #trigger>
              <NButton type="primary" size="large" :loading="busy" :disabled="busy" class="cta">
                <template #icon><NIcon :component="RocketOutline" /></template>
                一键部署绿环境
              </NButton>
            </template>
            将执行：25 备份上传网盘 → 149 下载并部署到绿环境（不修改蓝项目 /opt/osh）
          </NPopconfirm>
          <NPopconfirm @positive-click="$emit('start', 'skip_backup')">
            <template #trigger>
              <NButton size="large" :disabled="busy">
                <template #icon><NIcon :component="FlashOutline" /></template>
                跳过备份
              </NButton>
            </template>
            不会执行 25 备份与网盘上传，149 直接拉取网盘现有包。若要更新网盘请用「一键部署绿环境」。
          </NPopconfirm>
          <NButton size="large" quaternary :disabled="busy" @click="$emit('start', 'code_only')">
            <template #icon><NIcon :component="CodeSlashOutline" /></template>
            仅更代码
          </NButton>
        </NSpace>
      </div>
      <div class="hero-ring">
        <NProgress
          type="circle"
          :percentage="progress"
          :status="ringStatus"
          :stroke-width="8"
          :size="148"
          :show-indicator="false"
        />
        <div class="ring-label">{{ progress }}%</div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.hero {
  padding: 1.75rem 2rem;
  margin-bottom: 1.25rem;
}
.hero-grid {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 2rem;
  align-items: center;
}
.eyebrow {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
  flex-wrap: wrap;
}
h1 {
  margin: 0 0 0.35rem;
  font-size: 1.85rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}
.subtitle {
  margin: 0 0 0.5rem;
  color: var(--muted);
  font-size: 0.95rem;
  line-height: 1.5;
}
.subtitle strong {
  color: #22c55e;
  font-weight: 600;
}
.hint {
  margin: 0 0 0.75rem;
  font-size: 0.85rem;
  color: var(--muted);
}
.hint a {
  color: #60a5fa;
  text-decoration: none;
}
.hint a:hover {
  text-decoration: underline;
}
.meta {
  font-size: 0.85rem;
  color: var(--muted);
  margin-bottom: 1.25rem;
}
.meta code {
  font-family: var(--mono);
  color: var(--text);
}
.status.ok { color: var(--ok); }
.status.err { color: var(--err); }
.dot-sep { margin: 0 0.4rem; opacity: 0.4; }
.actions { flex-wrap: wrap; }
.cta {
  background: linear-gradient(135deg, #059669, #22c55e) !important;
  box-shadow: 0 4px 24px rgba(34, 197, 94, 0.25);
}
.hero-ring {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 148px;
  height: 148px;
}
.ring-label {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 1.35rem;
  font-weight: 700;
  pointer-events: none;
  line-height: 1;
}
@media (max-width: 768px) {
  .hero-grid { grid-template-columns: 1fr; }
  .hero-ring { justify-content: flex-start; }
}
</style>
