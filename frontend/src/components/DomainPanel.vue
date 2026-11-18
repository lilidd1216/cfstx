<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  // 由 App 统一维护的体检状态
  state: {
    type: Object,
    default: () => ({ running: false, done: 0, total: 0, results: [] }),
  },
  // 配置里已有的探测域名，作为「测试域名」的默认值
  defaultHost: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
})
const emit = defineEmits(['start', 'stop', 'toast'])

const host = ref('')
const domainsText = ref('')
const loadedDefaults = ref(false)

// 首次拿到 defaultHost 时自动填入
watch(
  () => props.defaultHost,
  (v) => {
    if (v && !host.value) host.value = v
  },
  { immediate: true }
)

async function loadDefaults(force = false) {
  if (loadedDefaults.value && !force) return
  try {
    const r = await fetch('/api/domain/defaults')
    const j = await r.json()
    if (Array.isArray(j.domains) && j.domains.length) {
      if (force || !domainsText.value) {
        domainsText.value = j.domains.join('\n')
      }
      loadedDefaults.value = true
    }
  } catch (_) {
    /* 忽略：用户可以手动填 */
  }
}
loadDefaults()

function start() {
  const list = domainsText.value
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  if (!host.value.trim()) {
    emit('toast', '请填写测试域名（你自己的入口域名）', 'warning')
    return
  }
  if (!list.length) {
    emit('toast', '请至少填一个优选域名', 'warning')
    return
  }
  emit('start', { host: host.value.trim(), domains: list })
}

const headers = [
  { title: '优选域名', key: 'domain', width: '30%' },
  { title: 'IP 数', key: 'total', align: 'end', width: '70px' },
  { title: '可用', key: 'available', align: 'end', width: '70px' },
  { title: '成功率', key: 'successRate', align: 'end', width: '86px' },
  { title: '最优 TLS', key: 'bestTls', align: 'end', width: '92px' },
  { title: '平均 TLS', key: 'avgTls', align: 'end', width: '92px' },
  { title: '评级', key: 'rating', align: 'center', width: '86px' },
]

function ratingColor(r) {
  switch (r) {
    case '推荐': return 'success'
    case '可用': return 'info'
    case '一般': return 'warning'
    case '不推荐': return 'error'
    default: return 'grey'
  }
}

function tlsColor(ms) {
  if (!ms) return 'text-medium-emphasis'
  if (ms < 600) return 'text-success'
  if (ms < 1200) return 'text-warning'
  return 'text-error'
}

// 把达到「可用」标准的域名整理成可直接粘贴的 CNAME 配置
function copyBest() {
  const best = props.state.results.filter(
    (r) => r.rating === '推荐' || r.rating === '可用'
  )
  if (!best.length) {
    emit('toast', '没有达到「可用」标准的域名，建议换一批再测', 'warning')
    return
  }
  const lines = best.map(
    (r) => `${r.domain}\t# ${r.rating} · 成功率 ${(r.successRate * 100).toFixed(0)}% · 平均 ${r.avgTls.toFixed(0)}ms`
  )
  const text = lines.join('\n')

  const done = () => emit('toast', `已复制 ${best.length} 个可用域名`, 'success')
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(done, () => emit('toast', '复制失败', 'error'))
  } else {
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    ok ? done() : emit('toast', '复制失败', 'error')
  }
}
</script>

<template>
  <v-card class="section" elevation="0">
    <div class="section-head">
      <v-icon size="18" color="primary">mdi-earth</v-icon>
      <span class="section-title">优选域名体检</span>
      <span class="section-desc">
        优选域名解析出的 IP 好坏混杂，DNS 轮询又是随机的——先测清楚再 CNAME
      </span>
      <v-spacer />
      <v-btn
        v-if="state.results.length"
        size="small"
        variant="tonal"
        color="success"
        prepend-icon="mdi-content-copy"
        @click="copyBest"
      >
        复制可用域名
      </v-btn>
    </div>

    <v-alert density="compact" variant="tonal" type="info" class="mb-4 text-caption">
      测试域名必须填<strong>你自己的入口域名</strong>（如 <code>fast2.lds1216.ccwu.cc</code>）。
      CF 边缘按 Host 决定后端路由，用别人的域名做 Host 测出来的数字没有参考价值。
    </v-alert>

    <v-text-field
      v-model="host"
      label="测试域名（你自己的入口域名）"
      placeholder="fast2.lds1216.ccwu.cc"
      density="compact"
      variant="outlined"
      class="mb-3"
      hide-details
      :disabled="disabled || state.running"
    />

    <v-textarea
      v-model="domainsText"
      label="待测的优选域名（每行一个）"
      placeholder="cu.877774.xyz&#10;saas.sin.fan&#10;cf.090227.xyz"
      density="compact"
      variant="outlined"
      rows="7"
      class="mb-3 domains-input"
      hide-details
      :disabled="disabled || state.running"
    />

    <div class="d-flex align-center ga-2 mb-4">
      <v-btn
        size="small"
        variant="text"
        prepend-icon="mdi-restore"
        :disabled="state.running"
        @click="loadDefaults(true)"
      >
        恢复默认列表
      </v-btn>
      <v-spacer />
      <v-btn
        v-if="state.running"
        variant="tonal"
        size="small"
        color="error"
        prepend-icon="mdi-stop-circle-outline"
        @click="emit('stop')"
      >
        停止 {{ state.done }}/{{ state.total }}
      </v-btn>
      <v-btn
        v-else
        variant="flat"
        size="small"
        color="primary"
        prepend-icon="mdi-play"
        :disabled="disabled"
        @click="start"
      >
        开始体检
      </v-btn>
    </div>

    <v-data-table
      :headers="headers"
      :items="state.results"
      item-value="domain"
      density="comfortable"
      class="domain-table"
      no-data-text="还没有结果，填好测试域名后点「开始体检」"
      items-per-page="-1"
      hide-default-footer
    >
      <template #item.domain="{ item }">
        <span class="mono text-body-2">{{ item.domain }}</span>
      </template>

      <template #item.total="{ item }">
        <span class="mono text-caption">{{ item.total }}</span>
      </template>

      <template #item.available="{ item }">
        <span class="mono text-caption" :class="item.available ? 'text-success' : 'text-error'">
          {{ item.available }}
        </span>
      </template>

      <template #item.successRate="{ item }">
        <span class="mono" :class="item.successRate >= 0.8 ? 'text-success' : item.successRate >= 0.5 ? 'text-warning' : 'text-error'">
          {{ (item.successRate * 100).toFixed(0) }}%
        </span>
      </template>

      <template #item.bestTls="{ item }">
        <span class="mono" :class="tlsColor(item.bestTls)">
          {{ item.bestTls ? item.bestTls.toFixed(0) + ' ms' : '—' }}
        </span>
      </template>

      <template #item.avgTls="{ item }">
        <span class="mono" :class="tlsColor(item.avgTls)">
          {{ item.avgTls ? item.avgTls.toFixed(0) + ' ms' : '—' }}
        </span>
      </template>

      <template #item.rating="{ item }">
        <v-chip size="x-small" variant="flat" :color="ratingColor(item.rating)" :title="item.error || ''">
          {{ item.rating }}
        </v-chip>
      </template>
    </v-data-table>
  </v-card>
</template>

<style scoped>
.section {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  padding: 18px 20px;
}

.section-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 14px;
}

.section-title {
  font-size: 15px;
  font-weight: 600;
  color: #e4e4e7;
}

.section-desc {
  font-size: 12px;
  color: #71717a;
  margin-left: 4px;
}

.domains-input :deep(textarea) {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 12.5px;
  line-height: 1.7;
}

.domain-table {
  background: transparent !important;
}

.domain-table :deep(th) {
  font-size: 12px;
  color: #8b8b96 !important;
  font-weight: 500;
}

.domain-table :deep(td) {
  border-bottom: 1px solid #23232b !important;
}
</style>
