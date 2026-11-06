<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import ConfigPanel from './components/ConfigPanel.vue'
import ResultTable from './components/ResultTable.vue'
import HistoryDialog from './components/HistoryDialog.vue'

const config = ref({
  threads: 200,
  pingTimes: 4,
  port: 443,
  ipFile: 'ip.txt',
  ipText: '',
  httping: false,
  httpingCode: 0,
  colo: '',
  maxDelay: 9999,
  minDelay: 0,
  maxLossRate: 1.0,
  minSpeed: 0,
  downloadTime: 10,
  testCount: 10,
  disableDownload: false,
  testAll: false,
  url: 'https://cf.xiu2.xyz/url',
  outputFile: 'result.csv',
  printNum: 10,
  debug: false,
})

const running = ref(false)
const phase = ref('idle') // idle | ping | download | done
const progress = ref({ current: 0, total: 0, available: 0, ip: '', speed: 0 })
const results = ref([])
const history = ref([])
const historyOpen = ref(false)
const snackbar = ref({ show: false, text: '', color: 'success' })
const version = ref('v1.0.0')
const elapsed = ref(0)

let timer = null
let startedAt = 0
let es = null

// ---------------------------------------------------------------------------
// HTTP 通信（替代 Wails 绑定）
// ---------------------------------------------------------------------------

async function httpGet(path) {
  const r = await fetch(path)
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

async function httpPost(path, body) {
  const r = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

async function httpDelete(path) {
  const r = await fetch(path, { method: 'DELETE' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

// ---------------------------------------------------------------------------
// 计算属性
// ---------------------------------------------------------------------------

const progressPercent = computed(() => {
  if (!progress.value.total) return 0
  return Math.min(100, (progress.value.current / progress.value.total) * 100)
})

const phaseLabel = computed(() => {
  switch (phase.value) {
    case 'ping': return '延迟测速中'
    case 'download': return '下载测速中'
    case 'done': return '测速完成'
    default: return '就绪'
  }
})

const phaseColor = computed(() => {
  switch (phase.value) {
    case 'ping': return 'primary'
    case 'download': return 'secondary'
    case 'done': return 'success'
    default: return 'grey'
  }
})

const phaseIcon = computed(() => {
  switch (phase.value) {
    case 'ping': return 'mdi-progress-clock'
    case 'download': return 'mdi-download'
    case 'done': return 'mdi-check-circle'
    default: return 'mdi-circle-outline'
  }
})

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

function toast(text, color = 'success') {
  snackbar.value = { show: true, text, color }
}

function startTimer() {
  stopTimer()
  startedAt = Date.now()
  elapsed.value = 0
  timer = setInterval(() => {
    elapsed.value = Math.floor((Date.now() - startedAt) / 1000)
  }, 1000)
}

function stopTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

// ---------------------------------------------------------------------------
// 数据加载
// ---------------------------------------------------------------------------

async function loadConfig() {
  try {
    const c = await httpGet('/api/config')
    if (c) config.value = c
  } catch (_) { /* 忽略 */ }
}

async function loadHistory() {
  try {
    history.value = (await httpGet('/api/history')) || []
  } catch (_) { /* 忽略 */ }
}

// ---------------------------------------------------------------------------
// 操作
// ---------------------------------------------------------------------------

async function resetConfig() {
  try {
    const c = await httpPost('/api/config/reset')
    if (c) config.value = c
    toast('已恢复默认参数', 'info')
  } catch (e) {
    toast(String(e), 'error')
  }
}

async function start() {
  running.value = true
  results.value = []
  phase.value = 'ping'
  progress.value = { current: 0, total: 0, available: 0, ip: '', speed: 0 }
  startTimer()
  try {
    await httpPost('/api/test/start', config.value)
  } catch (e) {
    toast(String(e), 'error')
    running.value = false
    phase.value = 'idle'
    stopTimer()
  }
}

async function stop() {
  try {
    await httpPost('/api/test/stop')
    toast('已请求停止测速', 'info')
  } catch (e) {
    toast(String(e), 'error')
  }
}

async function exportCsv() {
  try {
    const res = await httpPost('/api/export')
    toast('已导出到 ' + res.path, 'success')
  } catch (e) {
    toast(String(e), 'error')
  }
}

async function pickIpFile() {
  const path = window.prompt('输入 IP 数据文件路径（相对或绝对）', config.value.ipFile)
  if (path) config.value.ipFile = path
}

function loadRecord(rec) {
  config.value = { ...rec.config }
  results.value = rec.results || []
  phase.value = 'done'
  historyOpen.value = false
  toast('已载入历史记录', 'info')
}

async function deleteHistory(id) {
  try {
    await httpDelete('/api/history/' + id)
    await loadHistory()
  } catch (e) {
    toast(String(e), 'error')
  }
}

async function clearHistory() {
  try {
    await httpDelete('/api/history')
    await loadHistory()
    toast('历史记录已清空', 'info')
  } catch (e) {
    toast(String(e), 'error')
  }
}

function openRepo() {
  httpPost('/api/open-url', { url: 'https://gitee.com/liliaic/cfstx' }).catch(() => {})
}

// ---------------------------------------------------------------------------
// 事件（SSE）
// ---------------------------------------------------------------------------

function handleEvent(event, data) {
  switch (event) {
    case 'test:started':
      phase.value = 'ping'
      break

    case 'test:progress':
      if (data.stage === 'ping') {
        phase.value = 'ping'
        progress.value = {
          current: data.current,
          total: data.total,
          available: data.available,
          ip: '',
          speed: 0,
        }
      } else {
        phase.value = 'download'
        progress.value = {
          current: data.current,
          total: data.total,
          available: progress.value.available,
          ip: data.ip,
          speed: data.speed || 0,
        }
      }
      break

    case 'test:phase':
      phase.value = data
      break

    case 'test:finished':
      results.value = data?.results || []
      phase.value = 'done'
      running.value = false
      stopTimer()
      loadHistory()
      toast(`测速完成，共 ${results.value.length} 个结果`, 'success')
      break
  }
}

function bindEvents() {
  es = new EventSource('/api/events')
  es.onmessage = (e) => {
    let msg
    try {
      msg = JSON.parse(e.data)
    } catch (_) {
      return
    }
    handleEvent(msg.event, msg.data)
  }
  es.onerror = () => {
    // EventSource 会自动重连
  }
}

onMounted(async () => {
  bindEvents()
  await loadConfig()
  await loadHistory()
  try {
    const v = await httpGet('/api/version')
    if (v?.version) version.value = v.version
  } catch (_) { /* 忽略 */ }
})

onBeforeUnmount(() => {
  if (es) {
    es.close()
    es = null
  }
  stopTimer()
})
</script>

<template>
  <v-app>
    <v-app-bar color="surface" elevation="0" height="64" class="app-bar">
      <div class="d-flex align-center ml-4">
        <v-icon color="primary" size="30" class="mr-3">mdi-speedometer</v-icon>
        <div>
          <div class="text-h6 font-weight-bold glow" style="line-height: 1.1">CFSTX</div>
          <div class="text-caption text-medium-emphasis">Cloudflare CDN 优选测速</div>
        </div>
        <v-chip size="x-small" variant="tonal" color="primary" class="ml-4">{{ version }}</v-chip>
      </div>

      <v-spacer />

      <v-btn icon variant="text" title="历史记录" @click="historyOpen = true">
        <v-icon>mdi-history</v-icon>
      </v-btn>
      <v-btn icon variant="text" title="项目主页" @click="openRepo">
        <v-icon>mdi-open-in-new</v-icon>
      </v-btn>
    </v-app-bar>

    <v-main class="main-bg">
      <div class="layout">
        <!-- 左侧参数面板 -->
        <aside class="left-col">
          <v-card class="panel-card" elevation="0">
            <v-card-title class="d-flex align-center py-3">
              <v-icon class="mr-2" size="20">mdi-tune-variant</v-icon>
              <span class="text-subtitle-1 font-weight-medium">测速参数</span>
              <v-spacer />
              <v-btn size="small" variant="text" :disabled="running" @click="resetConfig">重置</v-btn>
            </v-card-title>
            <v-divider />
            <div class="panel-body">
              <ConfigPanel
                v-model:config="config"
                :running="running"
                @pick-ip-file="pickIpFile"
              />
            </div>
          </v-card>
        </aside>

        <!-- 右侧主区域 -->
        <section class="right-col">
          <v-card class="panel-card control-card" elevation="0">
            <div class="d-flex align-center pa-4 ga-5">
              <v-btn
                v-if="!running"
                color="primary"
                size="large"
                prepend-icon="mdi-play"
                class="px-8"
                @click="start"
              >
                开始测速
              </v-btn>
              <v-btn
                v-else
                color="error"
                size="large"
                prepend-icon="mdi-stop"
                class="px-8"
                @click="stop"
              >
                停止
              </v-btn>

              <v-divider vertical class="mx-1" style="height: 42px" />

              <div class="status-block">
                <div class="text-caption text-medium-emphasis">状态</div>
                <div class="d-flex align-center ga-2">
                  <v-icon size="16" :color="phaseColor">{{ phaseIcon }}</v-icon>
                  <span class="text-body-2 font-weight-medium">{{ phaseLabel }}</span>
                </div>
              </div>

              <div class="status-block">
                <div class="text-caption text-medium-emphasis">用时</div>
                <div class="metric-value text-body-1">{{ elapsed }} s</div>
              </div>

              <v-spacer />

              <div v-if="phase === 'ping'" class="status-block">
                <div class="text-caption text-medium-emphasis">可用 IP</div>
                <div class="metric-value text-body-1 text-success">{{ progress.available }}</div>
              </div>

              <div v-if="phase === 'download'" class="status-block">
                <div class="text-caption text-medium-emphasis">当前 IP</div>
                <div class="mono text-body-2">{{ progress.ip || '—' }}</div>
              </div>

              <div v-if="phase === 'download'" class="status-block">
                <div class="text-caption text-medium-emphasis">当前速度</div>
                <div class="metric-value text-body-1 text-primary">
                  {{ (progress.speed || 0).toFixed(2) }} MB/s
                </div>
              </div>
            </div>

            <v-progress-linear
              v-if="running"
              :model-value="progressPercent"
              :color="phase === 'download' ? 'secondary' : 'primary'"
              height="4"
              striped
            />
          </v-card>

          <!-- 进度详情 -->
          <v-card v-if="running" class="panel-card progress-card" elevation="0">
            <div class="d-flex align-center justify-space-between mb-2">
              <span class="text-body-2 text-medium-emphasis">
                {{ phase === 'ping' ? '延迟测速进度' : '下载测速进度' }}
              </span>
              <span class="metric-value text-body-2">
                {{ progress.current }} / {{ progress.total }}
                <span class="text-medium-emphasis ml-2">{{ progressPercent.toFixed(1) }}%</span>
              </span>
            </div>
            <v-progress-linear
              :model-value="progressPercent"
              :color="phase === 'download' ? 'secondary' : 'primary'"
              height="10"
              rounded
              striped
            />
          </v-card>

          <ResultTable :results="results" :loading="running" @export="exportCsv" />
        </section>
      </div>
    </v-main>

    <HistoryDialog
      v-model="historyOpen"
      :history="history"
      @load="loadRecord"
      @delete="deleteHistory"
      @clear="clearHistory"
    />

    <v-snackbar
      v-model="snackbar.show"
      :color="snackbar.color"
      timeout="3000"
      location="bottom right"
    >
      {{ snackbar.text }}
    </v-snackbar>
  </v-app>
</template>

<style scoped>
.main-bg {
  background: #121216;
  padding: 16px;
}

.layout {
  display: flex;
  gap: 16px;
  height: calc(100vh - 64px - 32px);
  min-height: 0;
}

.left-col {
  width: 380px;
  flex-shrink: 0;
  min-height: 0;
}

.right-col {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
  min-height: 0;
}

.panel-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
}

.panel-body {
  flex: 1;
  overflow: hidden;
  padding: 12px;
  min-height: 0;
}

.left-col :deep(.v-card) {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.control-card {
  flex-shrink: 0;
}

.progress-card {
  flex-shrink: 0;
  padding: 16px;
}

.status-block {
  min-width: 90px;
}

.app-bar {
  border-bottom: 1px solid #23232b;
}
</style>
