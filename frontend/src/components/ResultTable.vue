<script setup>
import { computed, ref, reactive } from 'vue'

const props = defineProps({
  results: { type: Array, default: () => [] },
  total: { type: Number, default: 0 },
  loading: { type: Boolean, default: false },
  // 稳定性测试结果（按 ip:port 索引），以及运行状态
  stability: { type: Array, default: () => [] },
  stabilityRunning: { type: Boolean, default: false },
  stabilityProgress: { type: Object, default: () => ({ done: 0, total: 0 }) },
  // 弹窗里「测试域名」的默认值，通常来自用户配置的 Worker 域名
  defaultDomain: { type: String, default: '' },
})
const emit = defineEmits([
  'export',
  'export-txt',
  'copied',
  'test-stability',
  'stop-stability',
])

const headers = [
  { title: 'IP 地址', key: 'ip', sortable: false, width: '22%' },
  { title: '端口', key: 'port', align: 'center', width: '76px' },
  { title: '平均延迟', key: 'delay', align: 'end' },
  { title: 'TLS', key: 'tls', align: 'end', width: '104px' },
  { title: '丢包率', key: 'lossRate', align: 'end' },
  { title: '下载速度', key: 'speed', align: 'end' },
  { title: '地区', key: 'colo', align: 'center', width: '78px' },
  { title: '稳定性', key: 'stability', align: 'center', width: '104px' },
  { title: '评分', key: 'score', align: 'end', width: '64px' },
]

// ---- 稳定性测试弹窗 ----
const dialog = ref(false)
const form = reactive({
  domain: props.defaultDomain || '',
  rounds: 10,
  concurrency: 8,
  keepAlive: true,
})

function openDialog() {
  // 每次打开都用最新的默认域名，避免首次渲染时 props 还没到
  if (!form.domain) form.domain = props.defaultDomain || ''
  dialog.value = true
}

function startStability() {
  dialog.value = false
  emit('test-stability', { ...form })
}

// 把稳定性结果数组转成查找表，供表格列使用
const stabMap = computed(() => {
  const m = new Map()
  for (const s of props.stability) {
    m.set(`${s.ip}:${s.port || 443}`, s)
  }
  return m
})

function stabOf(item) {
  return stabMap.value.get(keyOf(item))
}

function tlsColor(ms) {
  if (!ms) return 'grey'
  if (ms < 500) return 'success'
  if (ms < 1200) return 'warning'
  return 'error'
}

function scoreColorOf(score) {
  switch (score) {
    case 'S': return 'success'
    case 'A': return 'teal'
    case 'B': return 'info'
    case 'C': return 'warning'
    default: return 'error'
  }
}

// 悬停提示：把「测没测长连接」和「测了但断了」区分开，
// 否则两者都会显示成「未测」，误导用户。
function keepAliveText(s) {
  const base = `成功 ${s.success}/${s.attempts} 次`
  if (!s.keepAliveTested) {
    return `${base} · 长连接未测`
  }
  // 探测 5 次，第一次必然新建连接，所以最多复用 4 次
  return s.keepAliveOk
    ? `${base} · 长连接正常（复用 ${s.reused}/4 次）`
    : `${base} · 长连接异常（复用 ${s.reused}/4 次）`
}

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
    let score = speedScore * wSpeed + delayScore * wDelay + lossScore * wLoss

    // 跑过稳定性测试的话，不稳的节点必须降权——
    // 否则会出现「评分 100、标着推荐，但下载必然断流」这种自相矛盾的结论。
    const stab = stabMap.value.get(keyOf(r))
    if (stab) {
      if (stab.score === 'D') score *= 0.3
      else if (stab.score === 'C') score *= 0.7
    }
    return { ...r, score: Math.round(score) }
  })
})

const maxSpeed = computed(() => Math.max(...props.results.map((r) => r.speed), 1))

// 综合评分最高的 3 个，在表格中标记为「推荐」。
// 加 50 分下限：结果不足 3 个时，原来的逻辑会把明显不可用的节点
// 也标成「推荐」，这个标签就失去意义了。
const topThree = computed(() =>
  [...scored.value]
    .filter((x) => x.score >= 50)
    .sort((a, b) => b.score - a.score || a.delay - b.delay)
    .slice(0, 3)
)

const topKeys = computed(() => new Set(topThree.value.map(keyOf)))

// 一句话结论：把表格从「一堆数据」变成「一个答案」
const summary = computed(() => {
  const n = props.total || props.results.length
  const best = topThree.value[0]
  if (!best) return `共筛选出 ${n} 个可用 IP，但没有达到推荐标准的节点`
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

      <v-btn
        variant="tonal"
        size="small"
        class="ml-2"
        :color="stabilityRunning ? 'error' : 'primary'"
        :prepend-icon="stabilityRunning ? 'mdi-stop-circle-outline' : 'mdi-shield-check-outline'"
        :disabled="!results.length"
        @click="stabilityRunning ? emit('stop-stability') : openDialog()"
      >
        <span v-if="stabilityRunning">
          停止 {{ stabilityProgress.done }}/{{ stabilityProgress.total }}
        </span>
        <span v-else>测试稳定性</span>
      </v-btn>
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

      <template #item.tls="{ item }">
        <span
          v-if="stabOf(item) && stabOf(item).tlsAvg > 0"
          class="metric-value"
          :class="`text-${tlsColor(stabOf(item).tlsAvg)}`"
          style="white-space: nowrap"
          :title="`TLS 抖动 ${stabOf(item).tlsJitter.toFixed(0)}ms · 最长 ${stabOf(item).totalMax.toFixed(0)}ms`"
        >
          {{ stabOf(item).tlsAvg.toFixed(0) }} ms
        </span>
        <span v-else class="text-medium-emphasis">—</span>
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

      <template #item.stability="{ item }">
        <v-chip
          v-if="stabOf(item)"
          size="x-small"
          variant="flat"
          :color="scoreColorOf(stabOf(item).score)"
          style="white-space: nowrap"
          :title="keepAliveText(stabOf(item))"
        >
          {{ stabOf(item).score }} · {{ (stabOf(item).successRate * 100).toFixed(0) }}%
        </v-chip>
        <span v-else class="text-medium-emphasis">—</span>
      </template>

      <template #item.score="{ item }">
        <span class="score-value" :class="`text-${scoreColor(item.score)}`">{{ item.score }}</span>
      </template>
    </v-data-table>

    <!-- 稳定性测试配置 -->
    <v-dialog v-model="dialog" max-width="520">
      <v-card class="stab-dialog">
        <v-card-title class="d-flex align-center py-3">
          <v-icon class="mr-2" size="20">mdi-shield-check-outline</v-icon>
          <span class="text-subtitle-1 font-weight-medium">测试稳定性</span>
        </v-card-title>
        <v-divider />
        <v-card-text class="pt-4">
          <v-alert density="compact" variant="tonal" type="info" class="mb-4 text-caption">
            延迟测速只测 TCP 握手。实际使用还要 TLS 握手 + WebSocket + Worker 执行——
            实测同一个 IP 的 TCP 只要 165ms，TLS 却要 473ms~4872ms，且可能
            <strong>5 次里失败 3 次</strong>。这里多轮采样，把 TCP / TLS 分开计时，
            并检查长连接会不会被中途切断。
          </v-alert>

          <v-text-field
            v-model="form.domain"
            label="测试域名（填你自己的 Worker 域名）"
            placeholder="例如 fast2.lds1216.ccwu.cc"
            hint="留空则使用 CF 官方测速端点；填自己的域名能测出和实际使用一致的链路"
            persistent-hint
            density="compact"
            variant="outlined"
            class="mb-4"
            clearable
          />

          <div class="d-flex ga-3">
            <v-select
              v-model="form.rounds"
              :items="[
                { title: '5 轮（快速）', value: 5 },
                { title: '10 轮（推荐）', value: 10 },
                { title: '20 轮（深度）', value: 20 },
              ]"
              label="采样轮数"
              density="compact"
              variant="outlined"
              hide-details
            />
            <v-select
              v-model="form.concurrency"
              :items="[
                { title: '4', value: 4 },
                { title: '8', value: 8 },
                { title: '16', value: 16 },
              ]"
              label="并发 IP 数"
              density="compact"
              variant="outlined"
              hide-details
            />
          </div>

          <v-switch
            v-model="form.keepAlive"
            label="长连接保活探测"
            color="primary"
            density="compact"
            hide-details
            class="mt-3"
          >
            <template #append>
              <span class="text-caption text-medium-emphasis">
                每 IP 多约 8 秒，但能发现「下到一半断流」
              </span>
            </template>
          </v-switch>
        </v-card-text>
        <v-divider />
        <v-card-actions class="px-4 py-3">
          <span class="text-caption text-medium-emphasis">
            将测试 {{ results.length }} 个 IP
          </span>
          <v-spacer />
          <v-btn variant="text" @click="dialog = false">取消</v-btn>
          <v-btn
            color="primary"
            variant="flat"
            prepend-icon="mdi-play"
            :disabled="!results.length"
            @click="startStability"
          >
            开始测试
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
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

.stab-dialog {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
}

.stab-dialog :deep(.v-alert) {
  font-size: 12px;
  line-height: 1.6;
}
</style>
