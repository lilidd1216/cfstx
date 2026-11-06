<script setup>
import { computed } from 'vue'

const props = defineProps({
  results: { type: Array, default: () => [] },
  total: { type: Number, default: 0 },
  loading: { type: Boolean, default: false },
})
const emit = defineEmits(['export', 'export-txt', 'copied'])

const headers = [
  { title: 'IP 地址', key: 'ip', sortable: false, width: '24%' },
  { title: '端口', key: 'port', align: 'center', width: '70px' },
  { title: '平均延迟', key: 'delay', align: 'end' },
  { title: '丢包率', key: 'lossRate', align: 'end' },
  { title: '下载速度', key: 'speed', align: 'end' },
  { title: '地区', key: 'colo', align: 'center', width: '84px' },
  { title: '评分', key: 'score', align: 'end', width: '70px' },
]

function keyOf(r) {
  return `${r.ip}:${r.port || 443}`
}

// 综合评分：速度 50% + 延迟 35% + 丢包 15%
// 未开启下载测速（速度全为 0）时退化为：延迟 70% + 丢包 30%
const scored = computed(() => {
  const list = props.results
  if (!list.length) return []

  const maxSpeed = Math.max(...list.map((r) => r.speed), 0)
  const delays = list.map((r) => r.delay)
  const minDelay = Math.min(...delays)
  const maxDelay = Math.max(...delays)

  const hasSpeed = maxSpeed > 0
  const wSpeed = hasSpeed ? 0.5 : 0
  const wDelay = hasSpeed ? 0.35 : 0.7
  const wLoss = hasSpeed ? 0.15 : 0.3
  const span = maxDelay - minDelay

  return list.map((r) => {
    const speedScore = hasSpeed ? (r.speed / maxSpeed) * 100 : 0
    const delayScore = span > 0 ? ((maxDelay - r.delay) / span) * 100 : 100
    const lossScore = (1 - Math.min(Math.max(r.lossRate, 0), 1)) * 100
    const score = speedScore * wSpeed + delayScore * wDelay + lossScore * wLoss
    return { ...r, score: Math.round(score) }
  })
})

const maxSpeed = computed(() => Math.max(...props.results.map((r) => r.speed), 1))

// 综合评分最高的 3 个，在表格中标记为「推荐」
const topThree = computed(() =>
  [...scored.value].sort((a, b) => b.score - a.score || a.delay - b.delay).slice(0, 3)
)

const topKeys = computed(() => new Set(topThree.value.map(keyOf)))

// 一句话结论：把表格从「一堆数据」变成「一个答案」
const summary = computed(() => {
  const n = props.total || props.results.length
  const best = topThree.value[0]
  if (!best) return `共筛选出 ${n} 个可用 IP`
  const port = best.port || 443
  return `共筛选出 ${n} 个可用 IP，最优 ${best.ip}:${port}（${best.delay.toFixed(0)}ms · ${best.speed.toFixed(1)}MB/s）`
})

function isTop(item) {
  return topKeys.value.has(keyOf(item))
}

function rowProps({ item }) {
  return isTop(item) ? { class: 'top-row' } : {}
}

function speedColor(speed) {
  const ratio = speed / maxSpeed.value
  if (ratio >= 0.75) return 'success'
  if (ratio >= 0.4) return 'primary'
  return 'info'
}

function delayColor(delay) {
  if (delay < 100) return 'success'
  if (delay < 250) return 'warning'
  return 'error'
}

function scoreColor(score) {
  if (score >= 80) return 'success'
  if (score >= 60) return 'primary'
  if (score >= 40) return 'warning'
  return 'error'
}

// 复制为可直接粘贴到客户端 / 订阅框的格式
async function copyAs(format) {
  const lines = props.results.map((r) => {
    const port = r.port || 443
    switch (format) {
      case 'ip':
        return r.ip
      case 'ipport':
        return `${r.ip}:${port}`
      default:
        return `${r.ip}:${port}#${r.colo || 'N/A'}`
    }
  })
  if (!lines.length) return

  const text = lines.join('\n')
  let ok = false
  try {
    await navigator.clipboard.writeText(text)
    ok = true
  } catch (_) {
    // WebView2 下 clipboard API 可能不可用，回退到 execCommand
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    document.body.appendChild(ta)
    ta.select()
    try {
      ok = document.execCommand('copy')
    } catch (_) {
      ok = false
    }
    document.body.removeChild(ta)
  }

  emit('copied', ok ? `已复制 ${lines.length} 个 IP` : '复制失败，请检查剪贴板权限')
}
</script>

<template>
  <v-card class="result-card" elevation="0">
    <v-card-title class="d-flex align-center py-3">
      <v-icon class="mr-2" size="20">mdi-table-large</v-icon>
      <span class="text-subtitle-1 font-weight-medium">测速结果</span>
      <v-chip size="small" variant="tonal" class="ml-3">
        <span v-if="total > results.length">显示 {{ results.length }} / 共 {{ total }} 个 IP</span>
        <span v-else>{{ results.length }} 个 IP</span>
      </v-chip>
      <v-spacer />

      <v-btn
        variant="tonal"
        size="small"
        prepend-icon="mdi-file-delimited-outline"
        :disabled="!results.length"
        @click="emit('export')"
      >
        导出 CSV
      </v-btn>
      <v-btn
        variant="tonal"
        size="small"
        prepend-icon="mdi-file-document-outline"
        :disabled="!results.length"
        class="ml-2"
        @click="emit('export-txt')"
      >
        导出 TXT
      </v-btn>

      <v-menu location="bottom end">
        <template #activator="{ props: menuProps }">
          <v-btn
            v-bind="menuProps"
            variant="tonal"
            size="small"
            prepend-icon="mdi-content-copy"
            :disabled="!results.length"
            class="ml-2"
          >
            复制
          </v-btn>
        </template>
        <v-list density="compact" class="copy-menu">
          <v-list-item @click="copyAs('ip')">
            <v-list-item-title>纯 IP</v-list-item-title>
            <v-list-item-subtitle class="mono">1.2.3.4</v-list-item-subtitle>
          </v-list-item>
          <v-list-item @click="copyAs('ipport')">
            <v-list-item-title>IP:端口</v-list-item-title>
            <v-list-item-subtitle class="mono">1.2.3.4:443</v-list-item-subtitle>
          </v-list-item>
          <v-list-item @click="copyAs('full')">
            <v-list-item-title>IP:端口#地区</v-list-item-title>
            <v-list-item-subtitle class="mono">1.2.3.4:443#HKG</v-list-item-subtitle>
          </v-list-item>
        </v-list>
      </v-menu>
    </v-card-title>

    <!-- 一句话结论 -->
    <div v-if="results.length" class="summary">
      <v-icon size="15" color="success" class="mr-1">mdi-check-circle-outline</v-icon>
      <span>{{ summary }}</span>
    </div>

    <v-divider />

    <v-data-table
      :headers="headers"
      :items="scored"
      :loading="loading"
      :row-props="rowProps"
      item-value="ip"
      density="comfortable"
      class="result-table"
      no-data-text="暂无测速结果，点击左侧「开始测速」"
      items-per-page="-1"
      hide-default-footer
    >
      <template #item.ip="{ item }">
        <div class="d-flex align-center ga-2">
          <span class="mono text-body-2">{{ item.ip }}</span>
          <v-chip v-if="isTop(item)" size="x-small" color="success" variant="flat">推荐</v-chip>
        </div>
      </template>

      <template #item.port="{ item }">
        <span class="mono text-caption text-medium-emphasis">{{ item.port || 443 }}</span>
      </template>

      <template #item.delay="{ item }">
        <span class="metric-value" :class="`text-${delayColor(item.delay)}`">
          {{ item.delay.toFixed(1) }} ms
        </span>
      </template>

      <template #item.lossRate="{ item }">
        <span class="metric-value" :class="item.lossRate > 0 ? 'text-warning' : 'text-success'">
          {{ (item.lossRate * 100).toFixed(1) }}%
        </span>
      </template>

      <template #item.speed="{ item }">
        <div class="d-flex align-center justify-end ga-2">
          <v-progress-linear
            :model-value="(item.speed / maxSpeed) * 100"
            :color="speedColor(item.speed)"
            height="6"
            rounded
            style="max-width: 90px"
          />
          <span class="metric-value" :class="`text-${speedColor(item.speed)}`" style="min-width: 76px; text-align: right">
            {{ item.speed.toFixed(2) }}
          </span>
        </div>
      </template>

      <template #item.colo="{ item }">
        <v-chip size="x-small" variant="tonal" :color="item.colo === 'N/A' ? 'grey' : 'secondary'">
          {{ item.colo }}
        </v-chip>
      </template>

      <template #item.score="{ item }">
        <span class="score-value" :class="`text-${scoreColor(item.score)}`">{{ item.score }}</span>
      </template>
    </v-data-table>
  </v-card>
</template>

<style scoped>
.result-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  display: flex;
  flex-direction: column;
}

/* 一句话结论 */
.summary {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 20px 12px;
  font-size: 12.5px;
  color: #a1a1aa;
  line-height: 1.5;
}

.result-table {
  background: transparent !important;
  max-height: 480px;
  overflow: auto;
}

.result-table :deep(th) {
  font-size: 12px;
  color: #8b8b96 !important;
  font-weight: 500;
}

.result-table :deep(td) {
  border-bottom: 1px solid #23232b !important;
}

/* 推荐的 3 行高亮 */
.result-table :deep(tr.top-row) {
  background: rgba(34, 197, 94, 0.07) !important;
}

.result-table :deep(tr.top-row:hover) {
  background: rgba(34, 197, 94, 0.12) !important;
}

.score-value {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 13px;
  font-weight: 600;
}

.copy-menu {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
}

.copy-menu :deep(.v-list-item-subtitle) {
  font-size: 11px;
  opacity: 0.7;
}
</style>
