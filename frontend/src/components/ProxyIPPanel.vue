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

// 前端先算一遍数量，让用户知道自己粘了多少行（后端会再解析一次）
const candidateCount = computed(() => {
  const seen = new Set()
  for (const raw of String(text.value || '').split('\n')) {
    let line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const hash = line.indexOf('#')
    if (hash >= 0) line = line.slice(0, hash).trim()
    if (!line) continue
    const scheme = line.indexOf('://')
    if (scheme >= 0) line = line.slice(scheme + 3)
    const cut = line.search(/[/?]/)
    if (cut >= 0) line = line.slice(0, cut)
    if (line) seen.add(line)
  }
  return seen.size
})

const results = computed(() => props.state.results || [])
const okResults = computed(() => results.value.filter((r) => r.ok))

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
    text.value = r.text || ''
    emit('toast', `已导入「${src.name}」${r.count} 个候选`, 'success')
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

function copyProxyIP() {
  if (!okResults.value.length) {
    emit('toast', '还没有可用的 ProxyIP', 'warning')
    return
  }
  const body = okResults.value.map((r) => `PROXYIP=${r.addr}`).join('\n')
  copy(body, `PROXYIP 配置（${okResults.value.length} 条）`)
}

function copyAddresses() {
  if (!okResults.value.length) {
    emit('toast', '还没有可用的 ProxyIP', 'warning')
    return
  }
  copy(okResults.value.map((r) => r.addr).join('\n'), '可用地址')
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
        <span class="section-desc">一行一个：IP、IP:端口、域名都行</span>
      </div>

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
          <span class="source-desc">{{ importing === s.key ? '抓取中…' : s.description }}</span>
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
        <v-spacer />
        <v-btn size="small" variant="tonal" prepend-icon="mdi-content-copy" @click="copyProxyIP">
          复制 PROXYIP
        </v-btn>
        <v-btn size="small" variant="text" @click="copyAddresses">复制地址</v-btn>
      </div>

      <div class="result-list">
        <div
          v-for="r in results"
          :key="r.addr"
          class="result-row"
          :class="{ bad: !r.ok }"
        >
          <span class="badge" :class="r.ok ? 'ok' : 'fail'">{{ r.ok ? '可用' : '无效' }}</span>
          <span class="addr mono">{{ r.addr }}</span>
          <span class="exit">{{ exitLabel(r) }}</span>
          <span class="timing">
            <template v-if="r.ok">{{ r.tlsDelay }} / {{ r.httpDelay }} ms</template>
          </span>
          <span class="err">{{ r.ok ? '' : r.error }}</span>
        </div>
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
