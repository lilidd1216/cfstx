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
  httping: true,
  httpingCode: 0,
  colo: '',
  maxDelay: 200,
  minDelay: 0,
  maxLossRate: 1.0,
  minSpeed: 5,
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
const currentPage = ref('prefer-ip')
const updateDialog = ref(false)
const updateInfo = ref(null)
const updateState = ref('idle') // idle | downloading | downloaded | installing
const downloadProgress = ref(0)
const downloadedPath = ref('')

let timer = null
let startedAt = 0
let es = null

// 导航菜单：后续新增页面只需在此追加
const navItems = [
  { key: 'prefer-ip', title: '优选IP', icon: 'mdi-map-marker-radius-outline' },
]

const pageMeta = {
  'prefer-ip': {
    title: '优选IP',
    desc: '测速 Cloudflare CDN 各 IP 的延迟与下载速度，筛出当前网络下最快的节点',
  },
}

const currentMeta = computed(() => pageMeta[currentPage.value] || { title: '', desc: '' })

// ---------------------------------------------------------------------------
// HTTP 通信
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

// 按「显示结果数量」截断表格；0 表示不显示，空值表示全部
const displayResults = computed(() => {
  const n = Number(config.value.printNum)
  if (n === 0) return []
  if (!n || n >= results.value.length) return results.value
  return results.value.slice(0, n)
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
// 数据
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
// 更新检查
// ---------------------------------------------------------------------------

async function checkUpdate() {
  try {
    const r = await httpGet('/api/update/check')
    if (r && r.hasUpdate) {
      updateInfo.value = r
      updateState.value = 'idle'
      downloadProgress.value = 0
      downloadedPath.value = ''
      updateDialog.value = true
    }
  } catch (_) {
    // 网络异常时静默忽略
  }
}

function openReleasePage() {
  const url = updateInfo.value?.url || 'https://gitee.com/liliaic/cfstx/releases'
  httpPost('/api/open-url', { url }).catch(() => {})
  updateDialog.value = false
}

function findSetupAsset(assets) {
  if (!Array.isArray(assets)) return null
  return assets.find((a) => /^CFSTX-Setup-.*\.exe$/i.test(a.name)) || null
}

async function startDownload() {
  const asset = findSetupAsset(updateInfo.value?.assets)
  if (!asset) {
    toast('未找到安装包，已为你打开 Release 页面', 'warning')
    openReleasePage()
    return
  }
  updateState.value = 'downloading'
  downloadProgress.value = 0
  try {
    const r = await httpPost('/api/update/download', {
      url: asset.url,
      filename: asset.name,
    })
    downloadedPath.value = r.path
    updateState.value = 'downloaded'
  } catch (e) {
    toast(String(e), 'error')
    updateState.value = 'idle'
  }
}

async function installUpdate() {
  if (!downloadedPath.value) return
  updateState.value = 'installing'
  try {
    await httpPost('/api/update/install', { path: downloadedPath.value })
  } catch (e) {
    toast(String(e), 'error')
    updateState.value = 'downloaded'
  }
}

// ---------------------------------------------------------------------------
// SSE 事件
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

    case 'update:progress':
      downloadProgress.value = data?.percent || 0
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
}

onMounted(async () => {
  bindEvents()
  await loadConfig()
  await loadHistory()
  try {
    const v = await httpGet('/api/version')
    if (v?.version) version.value = v.version
  } catch (_) { /* 忽略 */ }

  // 延迟检查更新，避免影响启动速度
  setTimeout(checkUpdate, 1500)
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
    <!-- 左侧导航栏 -->
    <v-navigation-drawer permanent width="220" color="surface" class="nav-drawer">
      <div class="brand">
        <v-icon color="primary" size="30">mdi-speedometer</v-icon>
        <div>
          <div class="brand-name">CFSTX</div>
          <div class="brand-sub">Cloudflare 优选测速</div>
        </div>
      </div>

      <v-divider />

      <v-list nav density="comfortable" class="nav-list">
        <v-list-item
          v-for="item in navItems"
          :key="item.key"
          :prepend-icon="item.icon"
          :title="item.title"
          :active="currentPage === item.key"
          color="primary"
          rounded="lg"
          @click="currentPage = item.key"
        />
      </v-list>

      <template #append>
        <v-divider />
        <div class="nav-footer">
          <div class="d-flex align-center justify-space-between mb-2">
            <span class="text-caption text-medium-emphasis">{{ version }}</span>
            <v-btn icon size="x-small" variant="text" title="历史记录" @click="historyOpen = true">
              <v-icon size="16">mdi-history</v-icon>
            </v-btn>
          </div>
          <v-btn
            variant="text"
            size="small"
            block
            prepend-icon="mdi-open-in-new"
            class="justify-start"
            @click="openRepo"
          >
            项目主页
          </v-btn>
        </div>
      </template>
    </v-navigation-drawer>

    <!-- 主内容区 -->
    <v-main class="main-bg">
      <div class="page">
        <!-- 页头 -->
        <div class="page-header">
          <div>
            <h1 class="page-title">{{ currentMeta.title }}</h1>
            <p class="page-desc">{{ currentMeta.desc }}</p>
          </div>
          <v-btn
            variant="text"
            size="small"
            prepend-icon="mdi-restore"
            :disabled="running"
            @click="resetConfig"
          >
            恢复默认
          </v-btn>
        </div>

        <!-- 测速操作栏（吸顶） -->
        <v-card class="action-bar" elevation="0">
          <div class="action-inner">
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

            <v-divider vertical class="mx-2" style="height: 40px" />

            <div class="status-block">
              <div class="status-label">状态</div>
              <div class="d-flex align-center ga-2">
                <span class="status-dot" :class="`bg-${phaseColor}`" />
                <span class="status-value">{{ phaseLabel }}</span>
              </div>
            </div>

            <div class="status-block">
              <div class="status-label">用时</div>
              <div class="status-value mono">{{ elapsed }} s</div>
            </div>

            <div v-if="phase === 'ping'" class="status-block">
              <div class="status-label">可用 IP</div>
              <div class="status-value mono text-success">{{ progress.available }}</div>
            </div>

            <div v-if="phase === 'download'" class="status-block wide">
              <div class="status-label">当前 IP</div>
              <div class="status-value mono">{{ progress.ip || '—' }}</div>
            </div>

            <div v-if="phase === 'download'" class="status-block">
              <div class="status-label">当前速度</div>
              <div class="status-value mono text-primary">{{ (progress.speed || 0).toFixed(2) }} MB/s</div>
            </div>

            <v-spacer />

            <div class="status-block">
              <div class="status-label">结果</div>
              <div class="status-value mono">{{ results.length }} 个</div>
            </div>
          </div>

          <v-progress-linear
            v-if="running"
            :model-value="progressPercent"
            :color="phase === 'download' ? 'secondary' : 'primary'"
            height="3"
            striped
          />
        </v-card>

        <!-- 进度条 -->
        <v-card v-if="running" class="progress-card" elevation="0">
          <div class="d-flex align-center justify-space-between mb-2">
            <span class="text-body-2 text-medium-emphasis">
              {{ phase === 'ping' ? '延迟测速进度' : '下载测速进度' }}
            </span>
            <span class="mono text-body-2">
              {{ progress.current }} / {{ progress.total }}
              <span class="text-medium-emphasis ml-2">{{ progressPercent.toFixed(1) }}%</span>
            </span>
          </div>
          <v-progress-linear
            :model-value="progressPercent"
            :color="phase === 'download' ? 'secondary' : 'primary'"
            height="8"
            rounded
            striped
          />
        </v-card>

        <!-- 设置 -->
        <ConfigPanel
          v-model:config="config"
          :running="running"
          @pick-ip-file="pickIpFile"
        />

        <!-- 结果 -->
        <ResultTable
          :results="displayResults"
          :total="results.length"
          :loading="running"
          @export="exportCsv"
        />
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

    <!-- 发现新版本 -->
    <v-dialog v-model="updateDialog" max-width="620">
      <v-card class="update-card">
        <v-card-title class="d-flex align-center py-4">
          <v-icon color="primary" class="mr-2">mdi-update</v-icon>
          <span>发现新版本 {{ updateInfo?.latest }}</span>
        </v-card-title>
        <v-divider />
        <v-card-text>
          <div class="d-flex align-center ga-3 mb-4">
            <v-chip size="small" variant="tonal">当前 {{ updateInfo?.current }}</v-chip>
            <v-icon size="16">mdi-arrow-right</v-icon>
            <v-chip size="small" color="primary" variant="tonal">{{ updateInfo?.latest }}</v-chip>
          </div>
          <div class="update-body">{{ updateInfo?.body || '暂无更新说明' }}</div>
        </v-card-text>
        <v-divider />

        <v-card-actions v-if="updateState === 'idle'" class="pa-3">
          <v-spacer />
          <v-btn variant="text" @click="updateDialog = false">稍后</v-btn>
          <v-btn color="primary" prepend-icon="mdi-download" @click="startDownload">立即更新</v-btn>
        </v-card-actions>

        <div v-else-if="updateState === 'downloading'" class="px-6 pb-5">
          <div class="d-flex justify-space-between text-caption mb-2">
            <span>正在下载安装包...</span>
            <span class="mono">{{ downloadProgress.toFixed(1) }}%</span>
          </div>
          <v-progress-linear :model-value="downloadProgress" color="primary" height="8" rounded />
        </div>

        <v-card-actions v-else-if="updateState === 'downloaded'" class="pa-3">
          <v-spacer />
          <v-btn variant="text" @click="updateDialog = false">稍后</v-btn>
          <v-btn color="primary" prepend-icon="mdi-cog-play" @click="installUpdate">立即安装</v-btn>
        </v-card-actions>

        <div v-else-if="updateState === 'installing'" class="px-6 pb-5">
          <div class="d-flex align-center ga-2 text-caption">
            <v-progress-circular indeterminate size="16" width="2" color="primary" />
            <span>正在启动安装程序，应用即将退出...</span>
          </div>
        </div>
      </v-card>
    </v-dialog>
  </v-app>
</template>

<style scoped>
.nav-drawer {
  border-right: 1px solid #23232b !important;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 20px 18px;
}

.brand-name {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: #f4f4f5;
  line-height: 1.2;
}

.brand-sub {
  font-size: 11px;
  color: #71717a;
  margin-top: 2px;
}

.nav-list {
  padding: 8px 10px;
}

.nav-footer {
  padding: 12px 16px 16px;
}

.main-bg {
  background: #121216;
}

.page {
  height: 100%;
  overflow-y: auto;
  padding: 24px 28px 32px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.page-title {
  font-size: 24px;
  font-weight: 600;
  color: #f4f4f5;
  margin: 0 0 6px;
}

.page-desc {
  font-size: 13px;
  color: #71717a;
  margin: 0;
}

.action-bar {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  position: sticky;
  top: 0;
  z-index: 10;
}

.action-inner {
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 14px 20px;
}

.status-block {
  min-width: 84px;
}

.status-block.wide {
  min-width: 150px;
}

.status-label {
  font-size: 11px;
  color: #71717a;
  margin-bottom: 3px;
}

.status-value {
  font-size: 14px;
  color: #e4e4e7;
  font-weight: 500;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}

.progress-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  padding: 16px 20px;
}

.mono {
  font-family: 'JetBrains Mono', 'Cascadia Code', Consolas, monospace;
  font-variant-numeric: tabular-nums;
}

.update-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
}

.update-body {
  white-space: pre-wrap;
  font-size: 13px;
  line-height: 1.6;
  color: #a1a1aa;
  max-height: 260px;
  overflow-y: auto;
  padding: 12px;
  background: #16161a;
  border-radius: 8px;
  border: 1px solid #23232b;
}
</style>
