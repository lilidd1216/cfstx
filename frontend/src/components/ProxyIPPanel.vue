<script setup>
import { ref, computed, onMounted, watch } from 'vue'

const props = defineProps({
  state: {
    type: Object,
    default: () => ({
      running: false, done: 0, total: 0, okCount: 0,
      domain: '', results: [], finished: false, stopped: false,
    }),
  },
  // 主页面的测速是否正在进行。两者同时跑会抢带宽和连接，
  // 实测可用数会明显偏低，所以要提醒用户。
  speedTesting: { type: Boolean, default: false },
})
const emit = defineEmits(['toast'])

const STORAGE_TEXT = 'cfstx.proxyip.text'
const STORAGE_DOMAIN = 'cfstx.proxyip.domain'

const text = ref(localStorage.getItem(STORAGE_TEXT) || '')
const probeDomain = ref(localStorage.getItem(STORAGE_DOMAIN) || '')
const sources = ref([])
const importing = ref('')
const busy = ref(false)

watch(text, (v) => localStorage.setItem(STORAGE_TEXT, v || ''))
watch(probeDomain, (v) => localStorage.setItem(STORAGE_DOMAIN, v || ''))

// 与后端 task.ParseProxyIPCandidates 保持同样的校验，
// 否则会显示「已识别 4 个」而后端只解析出 3 个，用户会以为丢数据。
const HOST_LABEL = /^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$/

// 解析一行，返回归一化后的 host:port；非法返回 null。
function parseCandidate(line) {
  let s = line.trim()
  if (!s) return null
  const scheme = s.indexOf('://')
  if (scheme >= 0) s = s.slice(scheme + 3)
  const cut = s.search(/[/?]/)
  if (cut >= 0) s = s.slice(0, cut)
  if (!s) return null

  let host = s
  let portStr = ''
  if (s.startsWith('[')) {
    // [IPv6]:port
    const end = s.lastIndexOf(']')
    if (end < 0) return null
    host = s.slice(1, end)
    const rest = s.slice(end + 1)
    if (rest && !rest.startsWith(':')) return null
    portStr = rest.slice(1)
  } else if ((s.match(/:/g) || []).length === 1) {
    // host:port
    const idx = s.indexOf(':')
    host = s.slice(0, idx)
    portStr = s.slice(idx + 1)
  }

  host = host.trim()
  if (!host) return null

  let port = 443
  if (portStr) {
    const p = Number(portStr)
    if (!Number.isInteger(p) || p < 1 || p > 65535) return null
    port = p
  }

  // IPv4
  if (/^(\d{1,3}\.){3}\d{1,3}$/.test(host)) {
    const ok = host.split('.').every((o) => Number(o) >= 0 && Number(o) <= 255)
    return ok ? `${host}:${port}` : null
  }
  // 裸 IPv6
  if (host.includes(':')) return `[${host}]:${port}`
  // 域名：每段都要合法，总长不超过 253
  if (host.length > 253) return null
  const labels = host.replace(/\.$/, '').split('.')
  if (labels.length < 2) return null
  if (!labels.every((l) => l.length > 0 && l.length <= 63 && HOST_LABEL.test(l))) return null
  return `${host.toLowerCase()}:${port}`
}

// 一行的去重键（host:port）。先去掉 #备注 再解析，
// 与后端 ParseProxyIPCandidates 的规则保持一致，否则数字会对不上。
function candidateKey(line) {
  let s = String(line || '').trim()
  if (!s || s.startsWith('#')) return null
  const hash = s.indexOf('#')
  if (hash >= 0) s = s.slice(0, hash).trim()
  if (!s) return null
  return parseCandidate(s)
}

// 前端先算一遍数量，让用户知道自己粘了多少行（后端会再解析一次）
const candidateCount = computed(() => {
  const seen = new Set()
  for (const raw of String(text.value || '').split('\n')) {
    const key = candidateKey(raw)
    if (key) seen.add(key)
  }
  return seen.size
})

// 把新导入的候选并入现有列表，按 host:port 去重。
// 三个数据源有大量重叠，累加时必须去重，否则重复检测浪费好几分钟。
function mergeCandidates(newText) {
  const seen = new Set()
  const out = []
  const push = (line) => {
    const key = candidateKey(line)
    if (!key || seen.has(key)) return
    seen.add(key)
    out.push(String(line).trim())
  }
  for (const line of String(text.value || '').split('\n')) push(line)
  for (const line of String(newText || '').split('\n')) push(line)
  text.value = out.join('\n')
}

const results = computed(() => props.state.results || [])
const okResults = computed(() => results.value.filter((r) => r.ok))
// 本机无法验证的（典型：域名解析到 Cloudflare 自有网段，
// 但从 Cloudflare 内部访问是通的），单独统计
const uncertainResults = computed(() => results.value.filter((r) => !r.ok && r.uncertain))

// v4 / v6 互斥筛选：按 ProxyIP 的出口类型。
// 出口是 v6 的只能代理到 IPv6 目标，某些场景要按这个挑。
const v4Only = ref(false)
const v6Only = ref(false)
function onV4Change(v) {
  if (v) v6Only.value = false
}
function onV6Change(v) {
  if (v) v4Only.value = false
}

const filteredResults = computed(() => {
  const rs = results.value
  if (v4Only.value) return rs.filter((r) => r.ok && !r.exitIpv6)
  if (v6Only.value) return rs.filter((r) => r.ok && r.exitIpv6)
  return rs
})
const filteredOK = computed(() => filteredResults.value.filter((r) => r.ok))

// 复制前 N 名，N 用预设按钮切换
const TOP_OPTIONS = [3, 5, 10, 20]
const topN = ref(10)

function topResults() {
  return filteredOK.value.slice(0, topN.value)
}

const percent = computed(() => {
  const { done, total } = props.state
  if (!total) return 0
  return Math.min(100, Math.round((done / total) * 100))
})

const summary = computed(() => {
  if (!props.state.finished) return ''
  const { okCount, total, seconds, stopped } = props.state
  const head = stopped ? '已停止' : '检测完成'
  const cost = seconds ? `，用时 ${seconds.toFixed(0)} 秒` : ''
  return `${head}：${total} 个候选中 ${okCount} 个可用${cost}`
})

async function httpGet(path) {
  const r = await fetch(path)
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

async function httpPost(path, body) {
  const r = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body || {}),
  })
  if (!r.ok) {
    let msg = await r.text()
    try { msg = JSON.parse(msg).error || msg } catch (_) { /* 保留原文 */ }
    throw new Error(msg)
  }
  return r.json()
}

async function loadSources() {
  try {
    sources.value = await httpGet('/api/proxyip/sources')
  } catch (_) { /* 数据源加载失败不影响手动粘贴 */ }
}

async function importSource(src) {
  importing.value = src.key
  try {
    const r = await httpPost('/api/proxyip/import', { key: src.key })
    // 累加而不是替换：点哪个加哪个，重复的按 host:port 去掉
    const before = candidateCount.value
    mergeCandidates(r.text || '')
    const added = candidateCount.value - before
    emit('toast', `「${src.name}」加入 ${added} 个（去重后共 ${candidateCount.value} 个）`, 'success')
  } catch (e) {
    emit('toast', String(e.message || e), 'error')
  } finally {
    importing.value = ''
  }
}

async function start() {
  if (!candidateCount.value) {
    emit('toast', '请先粘贴或导入候选列表', 'warning')
    return
  }
  busy.value = true
  try {
    await httpPost('/api/proxyip/check', {
      text: text.value,
      domain: probeDomain.value.trim(),
      concurrency: 32,
    })
  } catch (e) {
    emit('toast', String(e.message || e), 'error')
  } finally {
    busy.value = false
  }
}

async function stop() {
  try {
    await httpPost('/api/proxyip/stop', {})
    emit('toast', '已请求停止检测', 'info')
  } catch (e) {
    emit('toast', String(e.message || e), 'error')
  }
}

async function copy(text, label) {
  let ok = false
  try {
    await navigator.clipboard.writeText(text)
    ok = true
  } catch (_) {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    document.body.appendChild(ta)
    ta.select()
    try { ok = document.execCommand('copy') } catch (_) { ok = false }
    document.body.removeChild(ta)
  }
  emit('toast', ok ? `${label}已复制` : '复制失败', ok ? 'success' : 'error')
}

// 复制前 N 名的地址列表，一行一个
function copyTopAddresses() {
  const list = topResults()
  if (!list.length) {
    emit('toast', '还没有可用的 ProxyIP', 'warning')
    return
  }
  copy(list.map((r) => r.addr).join('\n'), `前 ${list.length} 个地址`)
}

// 按 edgetunnel 的 PROXYIP 变量格式复制：
// 源码里用逗号或换行分隔并随机选一个，这里给单行逗号分隔，直接粘就行。
function copyForEdgeTunnel() {
  const list = topResults()
  if (!list.length) {
    emit('toast', '还没有可用的 ProxyIP', 'warning')
    return
  }
  copy(list.map((r) => r.addr).join(','), `前 ${list.length} 个（PROXYIP 格式）`)
}

function clearAll() {
  text.value = ''
}

function exitLabel(r) {
  if (!r.exitIp) return '—'
  return `${r.exitLoc || '??'}·${r.exitColo || '??'}·${r.exitIpv6 ? 'v6' : 'v4'}`
}

onMounted(loadSources)
</script>

<template>
  <div class="proxyip">
    <!-- 输入区 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-swap-horizontal-bold</v-icon>
        <span class="section-title">候选列表</span>
        <span class="section-desc">点下面的源可以累加，重复的自动去掉</span>
      </div>

      <v-alert
        v-if="speedTesting"
        type="warning"
        variant="tonal"
        density="compact"
        class="mb-4"
        icon="mdi-speedometer"
      >
        正在测速。「优选IP」的测速会占满带宽与连接数，此时检测 ProxyIP 的结果会明显偏少。
        建议等测速结束后再来检测。
      </v-alert>

      <div class="source-row">
        <button
          v-for="s in sources"
          :key="s.key"
          type="button"
          class="source-btn"
          :disabled="!!importing || props.state.running"
          @click="importSource(s)"
        >
          <span class="source-name">
            {{ s.name }}
            <span v-if="s.count" class="source-count">{{ s.count }}</span>
          </span>
          <span class="source-desc">{{ importing === s.key ? '加入中…' : s.description }}</span>
        </button>
      </div>

      <v-textarea
        v-model="text"
        class="cand-input"
        placeholder="68.68.32.120:443&#10;165.22.137.198&#10;ProxyIP.HK.CMLiussss.net"
        rows="7"
        density="compact"
        variant="outlined"
        hide-details
        :disabled="props.state.running"
      />

      <div class="cand-meta">
        <span class="text-caption">
          已识别 <strong>{{ candidateCount }}</strong> 个候选
          <span v-if="candidateCount > 2000" class="warn-text">（单次上限 2000，请删减）</span>
        </span>
        <v-spacer />
        <v-btn
          size="x-small"
          variant="text"
          :disabled="props.state.running || !text"
          @click="clearAll"
        >
          清空
        </v-btn>
      </div>

      <div class="domain-row">
        <v-text-field
          v-model="probeDomain"
          label="探测域名"
          placeholder="留空则用 speed.cloudflare.com"
          density="compact"
          variant="outlined"
          hide-details
          prepend-inner-icon="mdi-web-check"
          :disabled="props.state.running"
        />
      </div>

      <div class="d-flex justify-end mt-3" style="gap: 8px">
        <v-btn
          v-if="props.state.running"
          color="error"
          variant="tonal"
          @click="stop"
        >
          停止
        </v-btn>
        <v-btn
          color="primary"
          variant="flat"
          :loading="busy"
          :disabled="props.state.running || !candidateCount"
          @click="start"
        >
          开始检测
        </v-btn>
      </div>

      <!-- 进度 -->
      <div v-if="props.state.running || props.state.finished" class="progress-box">
        <div class="progress-line">
          <span>{{ props.state.done }} / {{ props.state.total }}</span>
          <span class="ok-count">可用 {{ props.state.okCount }}</span>
          <v-spacer />
          <span>{{ percent }}%</span>
        </div>
        <v-progress-linear
          :model-value="percent"
          color="primary"
          height="6"
          rounded
        />
        <div v-if="summary" class="summary">{{ summary }}</div>
      </div>

      <div class="hint note">
        <strong>它是干什么的</strong>：edgetunnel 这类 Workers 脚本需要配置
        <code>PROXYIP</code> —— 一个能代为转发到 Cloudflare 的第三方服务器。
        公共列表里的地址衰减很快，别人测着 20ms，到你这里可能根本连不上。
        <br />
        <strong>怎么判断</strong>：连接后用探测域名做 TLS 握手，只有返回
        <em>该域名的 Cloudflare 证书</em>、并且能取回 <code>/cdn-cgi/trace</code>
        的才算可用。Cloudflare 自有网段的 IP 会被自动排除（证书会通过，但配进节点不工作）。
      </div>
    </v-card>

    <!-- 结果区 -->
    <v-card v-if="results.length" class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-format-list-checks</v-icon>
        <span class="section-title">检测结果</span>
        <span class="section-desc">{{ okResults.length }} 个可用 / 共 {{ results.length }} 个</span>
      </div>

      <!-- v4 / v6 互斥筛选（按出口类型） -->
      <div class="filter-row">
        <span class="filter-label">按出口筛选</span>
        <v-checkbox
          v-model="v4Only"
          label="只看 v4 出口"
          density="compact"
          hide-details
          color="primary"
          class="filter-box"
          @update:model-value="onV4Change"
        />
        <v-checkbox
          v-model="v6Only"
          label="只看 v6 出口"
          density="compact"
          hide-details
          color="primary"
          class="filter-box"
          @update:model-value="onV6Change"
        />
        <span v-if="v4Only || v6Only" class="filter-hint">
          显示 {{ filteredOK.length }} 个可用
        </span>
      </div>

      <!-- 复制区：前 N 名 + edgetunnel 格式 -->
      <div class="copy-bar">
        <span class="copy-label">复制前</span>
        <button
          v-for="n in TOP_OPTIONS"
          :key="n"
          type="button"
          class="top-btn"
          :class="{ active: topN === n }"
          @click="topN = n"
        >
          {{ n }}
        </button>
        <span class="copy-label">个</span>

        <v-btn size="small" variant="text" @click="copyTopAddresses">
          复制地址列表
        </v-btn>
        <v-btn
          size="small"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-content-copy"
          @click="copyForEdgeTunnel"
        >
          复制为 PROXYIP
        </v-btn>
      </div>

      <div class="copy-tip">
        <strong>复制为 PROXYIP</strong>：生成 <code>地址:端口,地址:端口,...</code> 一行，直接粘进
        edgetunnel 的 <code>PROXYIP</code> 设置（它支持多个，每次随机选一个用）。
        <br />
        <strong>复制地址列表</strong>：一行一个，粘到网页版检测工具或别处用。
      </div>

      <div class="result-list">
        <div
          v-for="r in filteredResults"
          :key="r.addr"
          class="result-row"
          :class="{ bad: !r.ok && !r.uncertain, unknown: r.uncertain }"
        >
          <span class="badge" :class="r.ok ? 'ok' : (r.uncertain ? 'unknown' : 'fail')">
            {{ r.ok ? '可用' : (r.uncertain ? '待验证' : '无效') }}
          </span>
          <span class="addr mono">{{ r.addr }}</span>
          <span class="exit">{{ exitLabel(r) }}</span>
          <span class="timing">
            <template v-if="r.ok">{{ r.tlsDelay }} / {{ r.httpDelay }} ms</template>
          </span>
          <span class="err">{{ r.ok ? '' : r.error }}</span>
        </div>
      </div>

      <div
        v-if="(v4Only || v6Only) && !filteredResults.length"
        class="empty-filter"
      >
        当前筛选下没有结果 —— 换另一边，或者取消筛选
      </div>

      <div v-if="uncertainResults.length" class="hint note mt-3 uncertain-note">
        <strong>「待验证」是什么意思</strong>：这些域名在本机解析出来的是 Cloudflare
        自己的 IP，从本机没法直连验证。但 Cloudflare 内部访问时解析结果可能不同 ——
        实测 <code>proxyip.cmliussss.net</code> 在 Worker 里是能正常用的。
        建议配进去实测，别直接丢掉。
      </div>

      <div class="hint note mt-3">
        配进 Workers 时写成 <code>PROXYIP=地址:端口</code>，端口别丢 —— 除了 443，常见还有
        8443 / 2053 / 2096。出口是 <code>v6</code> 的 ProxyIP 只能代理到 IPv6 目标，
        如果目标站点只有 IPv4 会连不上。
      </div>
    </v-card>
  </div>
</template>

<style scoped>
.proxyip {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  padding: 16px 20px;
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

/* 数据源按钮 */
.source-row {
  display: flex;
  gap: 10px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}

.source-btn {
  flex: 1 1 220px;
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px 14px;
  border-radius: 8px;
  border: 1px solid #26262e;
  background: #17171c;
  cursor: pointer;
  text-align: left;
  font-family: inherit;
  transition: border-color 0.15s, background 0.15s;
}

.source-btn:hover:not(:disabled) {
  border-color: #4f6acd;
  background: rgba(79, 106, 205, 0.08);
}

.source-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.source-name {
  font-size: 13px;
  font-weight: 600;
  color: #d4d4d8;
  display: flex;
  align-items: center;
  gap: 6px;
}

.source-count {
  font-size: 11px;
  font-weight: 500;
  color: #7c9cff;
  background: rgba(79, 106, 205, 0.16);
  padding: 1px 6px;
  border-radius: 10px;
}

.source-desc {
  font-size: 11.5px;
  color: #71717a;
  line-height: 1.5;
}

.cand-input :deep(textarea) {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 12.5px;
  line-height: 1.7;
}

.cand-meta {
  display: flex;
  align-items: center;
  margin-top: 6px;
  color: #8b8b96;
}

.warn-text {
  color: #e8a33d;
}

.domain-row {
  margin-top: 10px;
}

/* 进度 */
.progress-box {
  margin-top: 14px;
  padding: 12px 14px;
  border-radius: 8px;
  background: #17171c;
  border: 1px solid #26262e;
}

.progress-line {
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 12px;
  color: #a1a1aa;
  margin-bottom: 8px;
}

.ok-count {
  color: #5fbf7f;
}

.summary {
  margin-top: 8px;
  font-size: 12.5px;
  color: #a1a1aa;
}

.hint {
  font-size: 12px;
  line-height: 1.75;
  color: #71717a;
  margin: 0;
}

.hint.note {
  margin-top: 14px;
  padding: 10px 12px;
  border-radius: 6px;
  background: #17171c;
  border-left: 3px solid #4f6acd;
}

.hint code {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 11.5px;
  color: #a5b4fc;
  background: rgba(79, 106, 205, 0.12);
  padding: 1px 4px;
  border-radius: 3px;
}

/* v4 / v6 筛选 */
.filter-row {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 6px;
  flex-wrap: wrap;
}

.filter-label {
  font-size: 12px;
  color: #8b8b96;
}

.filter-box {
  flex: 0 0 auto;
  margin: 0;
}

.filter-box :deep(.v-label) {
  font-size: 12.5px;
  opacity: 1;
}

.filter-hint {
  font-size: 11.5px;
  color: #7c9cff;
}

/* 复制区 */
.copy-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 4px 0 10px;
  flex-wrap: wrap;
}

.copy-label {
  font-size: 12px;
  color: #8b8b96;
}

.top-btn {
  min-width: 34px;
  padding: 3px 8px;
  border-radius: 6px;
  border: 1px solid #2a2a33;
  background: #17171c;
  color: #a1a1aa;
  font-size: 12px;
  font-family: inherit;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}

.top-btn:hover {
  border-color: #4f6acd;
}

.top-btn.active {
  border-color: #4f6acd;
  background: rgba(79, 106, 205, 0.18);
  color: #c7d2fe;
  font-weight: 600;
}

.empty-filter {
  font-size: 12.5px;
  color: #71717a;
  padding: 14px 0;
  text-align: center;
}

.copy-tip {
  font-size: 11.5px;
  line-height: 1.8;
  color: #71717a;
  margin: -4px 0 10px;
  padding: 8px 12px;
  border-radius: 6px;
  background: #17171c;
  border-left: 3px solid #3d3d47;
}

.copy-tip code {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 11px;
  color: #a5b4fc;
  background: rgba(79, 106, 205, 0.12);
  padding: 1px 4px;
  border-radius: 3px;
}

/* 结果列表 */
.result-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 460px;
  overflow-y: auto;
}

.result-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border-radius: 6px;
  background: #17171c;
  border: 1px solid #26262e;
  font-size: 12.5px;
}

.result-row.bad {
  opacity: 0.62;
}

.result-row.unknown {
  border-color: #4a3f2a;
  background: rgba(232, 163, 61, 0.06);
}

.badge {
  flex: 0 0 auto;
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 10px;
  font-weight: 600;
}

.badge.ok {
  color: #5fbf7f;
  background: rgba(95, 191, 127, 0.14);
}

.badge.fail {
  color: #8b8b96;
  background: rgba(139, 139, 150, 0.12);
}

.badge.unknown {
  color: #e8a33d;
  background: rgba(232, 163, 61, 0.16);
}

.uncertain-note {
  border-left-color: #e8a33d !important;
}

.addr {
  flex: 0 0 200px;
  color: #e4e4e7;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.exit {
  flex: 0 0 110px;
  color: #7c9cff;
}

.timing {
  flex: 0 0 120px;
  color: #8b8b96;
}

.err {
  flex: 1;
  min-width: 0;
  color: #8b6b6b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
