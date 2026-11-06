<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import ConfigPanel from './components/ConfigPanel.vue'
import ResultTable from './components/ResultTable.vue'
import HistoryDialog from './components/HistoryDialog.vue'
import OptimizePanel from './components/OptimizePanel.vue'

const config = ref({
  threads: 200,
  pingTimes: 4,
  port: 443,
  sources: ['ipv4'],
  ipFile: 'ip.txt',
  ipText: '',
  customSources: '',
  httping: true,
  httpingCode: 0,
  colo: '',
  probeEnabled: false,
  probeDomain: '',
  maxDelay: 200,
  minDelay: 0,
  maxLossRate: 1.0,
  minSpeed: 5,
  downloadTime: 10,
  testCount: 10,
  disableDownload: false,
  testAll: false,
  url: 'https://speed.cloudflare.com/__down?bytes=90000000',
  outputFile: 'result.csv',
  printNum: 10,
  debug: false,
})

const running = ref(false)
const phase = ref('idle') // idle | fetch | ping | download | done
const progress = ref({ current: 0, total: 0, available: 0, ip: '', port: 0, speed: 0, msg: '' })
const results = ref([])
const history = ref([])
const historyOpen = ref(false)
const snackbar = ref({ show: false, text: '', color: 'success' })
const version = ref('v1.0.0')
const elapsed = ref(0)
const currentPage = ref('prefer-ip')
const updateDialog = ref(false)
const updateInfo = ref(null)
const diagnosis = ref(null)
const diagnosisDialog = ref(false)
const updateState = ref('idle') // idle | downloading | downloaded | installing
const downloadProgress = ref(0)
const downloadedPath = ref('')

// 测速前检测到系统代理时弹出的提醒
const proxyDialog = ref(false)
const proxyInfo = ref(null)

let timer = null
let startedAt = 0
let es = null

// 导航菜单：后续新增页面只需在此追加
const navItems = [
  { key: 'prefer-ip', title: '优选IP', icon: 'mdi-map-marker-radius-outline' },
  { key: 'optimize', title: '优化工具', icon: 'mdi-tools' },
]

const pageMeta = {
  'prefer-ip': {
    title: '优选IP',
    desc: '测速 Cloudflare CDN 各 IP 的延迟与下载速度，筛出当前网络下最快的节点',
  },
  optimize: {
    title: '优化工具',
    desc: '把优选结果应用到直连访问与客户端订阅，减少连接超时',
  },
}

const currentMeta = computed(() => pageMeta[currentPage.value] || { title: '', desc: '' })

// 更新说明为 Markdown 格式，渲染后仍需 sanitize 防止注入
const renderedUpdateBody = computed(() => {
  const body = updateInfo.value?.body
  if (!body) return ''
  return DOMPurify.sanitize(marked.parse(body, { gfm: true, breaks: true }))
})

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
    case 'fetch': return '获取在线 IP 中'
    case 'probe': return '域名探测中'
    case 'ping': return '延迟测速中'
    case 'download': return '下载测速中'
    case 'done': return '测速完成'
    default: return '就绪'
  }
})

const phaseColor = computed(() => {
  switch (phase.value) {
    case 'fetch': return 'info'
    case 'probe': return 'warning'
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

// 结果表格复制后的提示
function handleCopied(msg) {
  toast(msg, String(msg).startsWith('已复制') ? 'success' : 'error')
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

// ---------------------------------------------------------------------------
// 场景预设
// ---------------------------------------------------------------------------

const PRESET_LABELS = { beginner: '新手推荐', worker: 'Worker 专用', fast: '极速扫描' }

// 只覆盖测速策略相关字段，保留用户已填的域名、文件路径、端口等
const PRESET_CONFIGS = {
  beginner: {
    sources: ['ipv4', 'official'],
    httping: true,
    maxDelay: 200,
    minDelay: 0,
    maxLossRate: 0,
    minSpeed: 0,
    disableDownload: true,
    probeEnabled: false,
    testCount: 10,
  },
  worker: {
    sources: ['ipv4', 'bestcf', 'official'],
    httping: true,
    maxDelay: 200,
    minDelay: 0,
    maxLossRate: 0,
    minSpeed: 5,
    disableDownload: false,
    probeEnabled: true,
    testCount: 10,
  },
  fast: {
    sources: ['ipv4', 'bestcf'],
    httping: true,
    maxDelay: 300,
    minDelay: 0,
    maxLossRate: 1,
    minSpeed: 0,
    disableDownload: true,
    probeEnabled: false,
    testCount: 10,
  },
}

function applyPreset(key) {
  const patch = PRESET_CONFIGS[key]
  if (!patch) return
  config.value = { ...config.value, ...patch }
  const name = PRESET_LABELS[key] || key
  if (key === 'worker' && !config.value.probeDomain) {
    toast(`已套用「${name}」，请填写你的域名`, 'warning')
  } else {
    toast(`已套用「${name}」预设`, 'info')
  }
}

// 点击「开始测速」：先检测系统代理。
// 走代理测出的延迟是到代理出口的，不能代表本地网络到 Cloudflare 的真实质量。
async function start() {
  try {
    const p = await httpGet('/api/proxy')
    if (p?.enabled) {
      proxyInfo.value = p
      proxyDialog.value = true
      return
    }
  } catch (_) {
    // 代理检测失败不阻塞测速
  }
  doStart()
}

// 用户在代理提醒中选择「仍然开始」
function startAnyway() {
  proxyDialog.value = false
  doStart()
}

async function doStart() {
  running.value = true
  results.value = []
  phase.value = 'ping'
  progress.value = { current: 0, total: 0, available: 0, ip: '', port: 0, speed: 0, msg: '' }
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

async function exportTxt() {
  try {
    const res = await httpPost('/api/export/txt')
    toast('已导出到 ' + res.path, 'success')
  } catch (e) {
    toast(String(e), 'error')
  }
}

async function exportLog() {
  try {
    const res = await httpPost('/api/export/log')
    toast('日志已导出到 ' + res.path, 'success')
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

function openBestCF() {
  httpPost('/api/open-url', { url: 'https://bestcf.pages.dev/' }).catch(() => {})
}

function openCmliu() {
  httpPost('/api/open-url', { url: 'https://github.com/cmliu/cmliu' }).catch(() => {})
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
      if (data.stage === 'fetch') {
        phase.value = 'fetch'
        progress.value = {
          current: data.current,
          total: data.total,
          available: 0,
          ip: '',
          port: 0,
          speed: 0,
          msg: data.msg || '',
        }
      } else if (data.stage === 'probe') {
        phase.value = 'probe'
        progress.value = {
          current: data.current,
          total: data.total,
          available: 0,
          ip: '',
          port: 0,
          speed: 0,
          msg: '',
        }
      } else if (data.stage === 'ping') {
        phase.value = 'ping'
        progress.value = {
          current: data.current,
          total: data.total,
          available: data.available,
          ip: '',
          port: 0,
          speed: 0,
          msg: '',
        }
      } else {
        phase.value = 'download'
        progress.value = {
          current: data.current,
          total: data.total,
          available: progress.value.available,
          ip: data.ip,
          port: data.port || 0,
          speed: data.speed || 0,
          msg: '',
        }
      }
      break

    case 'test:result':
      // 每测完一个达标的 IP 就插入列表，按速度降序保持有序
      results.value = [...results.value, data].sort((a, b) => b.speed - a.speed)
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
      if (data?.diagnosis) {
        // 一个结果都没有：弹窗说明卡在哪一步，而不是静默失败
        diagnosis.value = data.diagnosis
        diagnosisDialog.value = true
      } else {
        toast(`测速完成，共 ${results.value.length} 个结果`, 'success')
      }
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
            v-if="currentPage === 'prefer-ip'"
            variant="text"
            size="small"
            prepend-icon="mdi-restore"
            :disabled="running"
            @click="resetConfig"
          >
            恢复默认
          </v-btn>
        </div>

        <template v-if="currentPage === 'prefer-ip'">
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

            <div v-if="phase === 'fetch'" class="status-block wide">
              <div class="status-label">正在抓取</div>
              <div class="status-value mono text-truncate">{{ progress.msg || 'bestcf.pages.dev' }}</div>
            </div>

            <div v-if="phase === 'ping'" class="status-block">
              <div class="status-label">可用 IP</div>
              <div class="status-value mono text-success">{{ progress.available }}</div>
            </div>

            <div v-if="phase === 'download'" class="status-block wide">
              <div class="status-label">当前 IP</div>
              <div class="status-value mono">
                {{ progress.ip || '—' }}<span v-if="progress.port" class="text-medium-emphasis">:{{ progress.port }}</span>
              </div>
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
              {{ phase === 'fetch' ? '在线 IP 获取进度' : phase === 'probe' ? '域名探测进度' : phase === 'ping' ? '延迟测速进度' : '下载测速进度' }}
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
          @open-bestcf="openBestCF"
          @open-cmliu="openCmliu"
          @apply-preset="applyPreset"
        />

        <!-- 结果 -->
        <ResultTable
          :results="displayResults"
          :total="results.length"
          :loading="running"
          @export="exportCsv"
          @export-txt="exportTxt"
          @copied="handleCopied"
        />
        </template>

        <OptimizePanel
          v-else-if="currentPage === 'optimize'"
          :results="results"
          @toast="(text, color) => toast(text, color)"
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

    <!-- 检测到系统代理时的提醒 -->
    <v-dialog v-model="proxyDialog" max-width="520">
      <v-card class="diagnosis-card">
        <v-card-title class="d-flex align-center py-4">
          <v-icon color="warning" class="mr-2">mdi-shield-alert-outline</v-icon>
          <span class="text-h6">检测到系统代理已开启</span>
        </v-card-title>
        <v-divider />
        <v-card-text class="py-5">
          <div class="proxy-addr mono">{{ proxyInfo?.address }}</div>
          <p class="text-body-2 mt-4" style="line-height: 1.75">
            测速流量会经过系统代理。此时得到的<b>延迟</b>和<b>下载速度</b>反映的是
            <b>你的电脑到代理出口</b>的情况，而不是本地网络直连 Cloudflare 的真实质量。
          </p>
          <p class="text-body-2 mt-2" style="line-height: 1.75">
            如果要筛选用于<b>直连</b>的 IP，建议先关闭代理再测速。
          </p>
        </v-card-text>
        <v-divider />
        <v-card-actions class="py-3">
          <v-spacer />
          <v-btn variant="text" @click="proxyDialog = false">取消</v-btn>
          <v-btn color="warning" variant="flat" @click="startAnyway">仍然开始</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- 测速结果为 0 时的诊断弹窗 -->
    <v-dialog v-model="diagnosisDialog" max-width="600">
      <v-card class="diagnosis-card" v-if="diagnosis">
        <v-card-title class="d-flex align-center py-4">
          <v-icon color="warning" class="mr-2">mdi-alert-circle-outline</v-icon>
          <span class="text-h6">本次测速没有筛选出任何 IP</span>
        </v-card-title>
        <v-divider />
        <v-card-text class="pt-4">
          <div class="diagnosis-reason">{{ diagnosis.reason }}</div>

          <div class="diagnosis-steps mt-4">
            <div class="diag-row" :class="{ zero: !diagnosis.totalTargets }">
              <span class="diag-label">加载到的测速目标</span>
              <span class="diag-value">{{ diagnosis.totalTargets }}</span>
            </div>
            <div
              v-if="diagnosis.probePassed >= 0"
              class="diag-row"
              :class="{ zero: diagnosis.totalTargets > 0 && !diagnosis.probePassed }"
            >
              <span class="diag-label">域名探测通过</span>
              <span class="diag-value">{{ diagnosis.probePassed }}</span>
            </div>
            <div
              class="diag-row"
              :class="{
                zero:
                  (diagnosis.probePassed < 0 ? diagnosis.totalTargets : diagnosis.probePassed) > 0 &&
                  !diagnosis.available,
              }"
            >
              <span class="diag-label">延迟测速连通</span>
              <span class="diag-value">{{ diagnosis.available }}</span>
            </div>
            <div class="diag-row" :class="{ zero: diagnosis.available > 0 && !diagnosis.afterDelay }">
              <span class="diag-label">通过延迟/丢包过滤</span>
              <span class="diag-value">{{ diagnosis.afterDelay }}</span>
            </div>
            <div class="diag-row" :class="{ zero: diagnosis.afterDelay > 0 && !diagnosis.downloadTested }">
              <span class="diag-label">完成下载测速</span>
              <span class="diag-value">{{ diagnosis.downloadTested }}</span>
            </div>
            <div class="diag-row" :class="{ zero: diagnosis.downloadTested > 0 && !diagnosis.maxSpeed }">
              <span class="diag-label">最高下载速度</span>
              <span class="diag-value">{{ (diagnosis.maxSpeed || 0).toFixed(2) }} MB/s</span>
            </div>
          </div>

          <v-alert type="info" variant="tonal" density="compact" class="mt-4">
            <div class="text-body-2">{{ diagnosis.suggestion }}</div>
          </v-alert>
        </v-card-text>
        <v-card-actions class="px-4 pb-3">
          <v-btn
            variant="tonal"
            size="small"
            prepend-icon="mdi-file-document-outline"
            @click="exportLog"
          >
            导出日志
          </v-btn>
          <v-spacer />
          <v-btn variant="text" @click="diagnosisDialog = false">知道了</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

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
          <div v-if="renderedUpdateBody" class="update-body" v-html="renderedUpdateBody"></div>
          <div v-else class="update-body">暂无更新说明</div>
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

        <div v-else-if="updateState === 'downloaded'" class="px-6 pb-4">
          <v-alert
            type="info"
            variant="tonal"
            density="compact"
            class="mb-3 text-caption"
          >
            首次更新时 Windows 可能提示「无法验证发布者」，请点击「更多信息」→「仍要运行」继续
          </v-alert>
          <div class="d-flex justify-end ga-2">
            <v-btn variant="text" @click="updateDialog = false">稍后</v-btn>
            <v-btn color="primary" prepend-icon="mdi-cog-play" @click="installUpdate">立即安装</v-btn>
          </div>
        </div>

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

/* 测速诊断弹窗 */
.diagnosis-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
}

/* 系统代理提醒弹窗 */
.proxy-addr {
  display: inline-block;
  padding: 5px 12px;
  border-radius: 6px;
  background: #26262e;
  border: 1px solid #3a3a46;
  color: #fbbf24;
  font-size: 13px;
}

.diagnosis-reason {
  font-size: 15px;
  color: #fbbf24;
  line-height: 1.6;
}

.diagnosis-steps {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.diag-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 9px 12px;
  border-radius: 6px;
  background: #17171c;
  font-size: 13px;
}

.diag-row.zero {
  background: rgba(239, 68, 68, 0.12);
  border-left: 3px solid #ef4444;
}

.diag-label {
  color: #8b8b96;
}

.diag-value {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  color: #e4e4e7;
  font-weight: 600;
}

.diag-row.zero .diag-value {
  color: #ef4444;
}

.update-body {
  font-size: 13px;
  line-height: 1.7;
  color: #a1a1aa;
  max-height: 320px;
  overflow-y: auto;
  padding: 14px 16px;
  background: #16161a;
  border-radius: 8px;
  border: 1px solid #23232b;
}

.update-body :deep(h1),
.update-body :deep(h2),
.update-body :deep(h3) {
  color: #e4e4e7;
  font-weight: 600;
  margin: 14px 0 8px;
  line-height: 1.4;
}

.update-body :deep(h1) { font-size: 18px; }
.update-body :deep(h2) { font-size: 16px; }
.update-body :deep(h3) { font-size: 14px; }

.update-body :deep(h1:first-child),
.update-body :deep(h2:first-child),
.update-body :deep(h3:first-child) {
  margin-top: 0;
}

.update-body :deep(p) {
  margin: 8px 0;
}

.update-body :deep(ul),
.update-body :deep(ol) {
  padding-left: 20px;
  margin: 8px 0;
}

.update-body :deep(li) {
  margin: 4px 0;
}

.update-body :deep(code) {
  background: #26262e;
  padding: 2px 5px;
  border-radius: 4px;
  font-family: 'JetBrains Mono', 'Cascadia Code', Consolas, monospace;
  font-size: 12px;
  color: #c4b5fd;
}

.update-body :deep(pre) {
  background: #101014;
  padding: 12px;
  border-radius: 6px;
  overflow-x: auto;
  margin: 8px 0;
}

.update-body :deep(pre code) {
  background: none;
  padding: 0;
  color: #d4d4d8;
}

.update-body :deep(a) {
  color: #8b5cf6;
  text-decoration: none;
}

.update-body :deep(a:hover) {
  text-decoration: underline;
}

.update-body :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin: 10px 0;
  font-size: 12px;
}

.update-body :deep(th),
.update-body :deep(td) {
  border: 1px solid #2a2a33;
  padding: 6px 10px;
  text-align: left;
}

.update-body :deep(th) {
  background: #22222a;
  color: #d4d4d8;
  font-weight: 600;
}

.update-body :deep(blockquote) {
  border-left: 3px solid #3a3a45;
  padding-left: 12px;
  margin: 8px 0;
  color: #71717a;
}

.update-body :deep(hr) {
  border: none;
  border-top: 1px solid #2a2a33;
  margin: 12px 0;
}

.update-body :deep(strong) {
  color: #e4e4e7;
  font-weight: 600;
}
</style>
