<script setup>
import { ref, computed, onMounted, watch } from 'vue'

const props = defineProps({
  results: { type: Array, default: () => [] },
})
const emit = defineEmits(['toast'])

const STORAGE_DOMAIN = 'cfstx.hosts.domain'
const STORAGE_IP = 'cfstx.hosts.ip'

// entries 一定要给数组：后端若返回 null，模板里的 entries.length 会抛
// TypeError，整个 Hosts 卡片会被 Vue 卸载，表现为「看不到这个功能」。
const hosts = ref({ isAdmin: false, hostsPath: '', entries: [], backupDir: '' })
// 记住上次填写的域名与 IP，切换页面或重启后不用重填
const domain = ref(localStorage.getItem(STORAGE_DOMAIN) || '')
// 要写入 hosts 的 IP：可以直接手填，也可以点下面的测速结果填入
const targetIP = ref(localStorage.getItem(STORAGE_IP) || '')
const busy = ref(false)

watch(domain, (v) => localStorage.setItem(STORAGE_DOMAIN, v || ''))
watch(targetIP, (v) => localStorage.setItem(STORAGE_IP, v || ''))

// hosts 只能映射 IP、不能指定端口，所以只列 443 端口的结果。
// 用点击列表而不是下拉框：避免浮层组件在 WebView2 中的兼容问题。
// 与后端 task.NormalizeProbeDomain 保持一致：用户从地址栏粘来的
// https://xxx/ 如果原样写进 hosts，是一条完全无效的记录。
function normalizeDomain() {
  let v = String(domain.value || '').trim()
  if (!v) return
  const scheme = v.indexOf('://')
  if (scheme >= 0) v = v.slice(scheme + 3)
  const cut = v.search(/[/?#]/)
  if (cut >= 0) v = v.slice(0, cut)
  v = v.replace(/:\d+$/, '').replace(/\.$/, '')
  domain.value = v.toLowerCase()
}

// hosts 只做域名解析，不涉及端口：浏览器访问 https 固定走 443。
// 所以粘贴 1.2.3.4:10001 时不能静默去掉端口 —— 那样会写出一条
// 「看起来成功、实际连 443 连不上」的记录。
//
// 这里把用户可能粘的东西拆开：
//   27.44.142.135:10001#HKG 39.84MB/s  →  ip=27.44.142.135 port=10001
//   52.74.51.158:443#SIN 34.58MB/s     →  ip=52.74.51.158  port=443
//   [2606:4700::1]:8443                →  ip=2606:4700::1   port=8443
//   104.16.0.1                         →  ip=104.16.0.1     port=0
function parseIPInput(raw) {
  let v = String(raw || '').trim()
  if (!v) return { ip: '', port: 0 }

  // 去掉导出的 TXT 格式里的 #备注
  const hash = v.indexOf('#')
  if (hash >= 0) v = v.slice(0, hash).trim()

  // 去掉可能粘进来的协议和路径
  const scheme = v.indexOf('://')
  if (scheme >= 0) v = v.slice(scheme + 3)
  const cut = v.search(/[/?]/)
  if (cut >= 0) v = v.slice(0, cut)

  let ip = v
  let port = 0
  if (v.startsWith('[')) {
    // [IPv6]:port 或 [IPv6]
    const end = v.indexOf(']')
    if (end > 0) {
      ip = v.slice(1, end)
      const rest = v.slice(end + 1)
      if (rest.startsWith(':')) port = Number(rest.slice(1)) || 0
    }
  } else if (/^(\d{1,3}\.){3}\d{1,3}:\d+$/.test(v)) {
    // IPv4:port
    const idx = v.indexOf(':')
    ip = v.slice(0, idx)
    port = Number(v.slice(idx + 1)) || 0
  }
  return { ip: ip.trim(), port }
}

function normalizeIPInput() {
  const raw = String(targetIP.value || '').trim()
  if (!raw) return
  const { ip, port } = parseIPInput(raw)

  // 非 443 端口保留原样，让下面的错误提示留在界面上，
  // 用户才能看到自己粘的是哪个端口、为什么不能用
  if (port && port !== 443) {
    targetIP.value = raw
    return
  }
  targetIP.value = ip
}

// 即时校验，让用户在点按钮之前就知道格式对不对
const ipState = computed(() => {
  const raw = String(targetIP.value || '').trim()
  if (!raw) return { ok: false, msg: '' }

  const { ip, port } = parseIPInput(raw)

  // 端口不是 443 时直接判负 —— 写进 hosts 也不会生效
  if (port && port !== 443) {
    return {
      ok: false,
      msg: `${port} 端口不能用：hosts 只做域名解析，浏览器访问 https 固定走 443，写进去会连 ${ip}:443 而失败`,
    }
  }

  const isV4 = /^(\d{1,3}\.){3}\d{1,3}$/.test(ip) &&
    ip.split('.').every((o) => Number(o) >= 0 && Number(o) <= 255)
  if (isV4) return { ok: true, msg: '' }

  // IPv6：只要包含冒号且字符合法就放行，精确校验交给后端
  if (ip.includes(':') && /^[0-9a-fA-F:.]+$/.test(ip)) {
    return { ok: true, msg: '' }
  }
  return { ok: false, msg: 'IP 格式不正确' }
})

const candidates = computed(() =>
  (props.results || [])
    .filter((r) => (r.port || 443) === 443)
    .map((r) => ({
      value: r.ip,
      delay: Number(r.delay || 0).toFixed(0),
      speed: Number(r.speed || 0).toFixed(1),
      colo: r.colo || '',
    }))
)

const clashRule = computed(() => {
  const d = domain.value.trim()
  if (!d) return '# 先在上方「域名」中填写你的域名，这里会自动生成'
  return `rules:\n  - DOMAIN-SUFFIX,${d},DIRECT`
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
    body: JSON.stringify(body),
  })
  if (!r.ok) {
    let msg = await r.text()
    try {
      msg = JSON.parse(msg).error || msg
    } catch (_) { /* 保留原文 */ }
    throw new Error(msg)
  }
  return r.json()
}

async function loadHosts() {
  try {
    const r = await httpGet('/api/hosts')
    // 后端字段缺失或为 null 时补默认值，避免整个卡片渲染失败
    hosts.value = {
      isAdmin: !!r?.isAdmin,
      hostsPath: r?.hostsPath || '',
      entries: Array.isArray(r?.entries) ? r.entries : [],
      backupDir: r?.backupDir || '',
      exePath: r?.exePath || '',
    }
  } catch (e) {
    emit('toast', '读取 hosts 状态失败：' + e.message, 'error')
  }
}

async function apply() {
  busy.value = true
  // 用解析出的纯 IP，避免把 #备注 之类的东西带进 hosts
  const { ip } = parseIPInput(targetIP.value)
  const d = domain.value.trim()
  try {
    await httpPost('/api/hosts/apply', { domain: d, ip })
    await loadHosts()
    emit('toast', `已写入 hosts：${ip} → ${d}（DNS 缓存已刷新）`, 'success')
  } catch (e) {
    emit('toast', String(e.message || e), 'error')
  } finally {
    busy.value = false
  }
}

async function remove() {
  busy.value = true
  try {
    await httpPost('/api/hosts/remove', {})
    await loadHosts()
    emit('toast', '已从 hosts 移除', 'info')
  } catch (e) {
    emit('toast', String(e.message || e), 'error')
  } finally {
    busy.value = false
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
    try {
      ok = document.execCommand('copy')
    } catch (_) {
      ok = false
    }
    document.body.removeChild(ta)
  }
  emit('toast', ok ? `${label}已复制` : '复制失败', ok ? 'success' : 'error')
}

onMounted(loadHosts)
</script>

<template>
  <div class="optimize">
    <!-- Hosts 加速 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-rocket-launch-outline</v-icon>
        <span class="section-title">Hosts 加速</span>
        <span class="section-desc">让浏览器直连时也走你筛选出的 IP</span>
      </div>

      <v-alert
        v-if="!hosts.isAdmin"
        type="warning"
        variant="tonal"
        density="compact"
        class="mb-4 admin-alert"
        icon="mdi-shield-key-outline"
      >
        <div>当前不是管理员权限，无法修改 hosts。</div>
        <ol class="admin-steps">
          <li>关闭 CFSTX</li>
          <li>找到下面的程序文件，右键 →「<strong>以管理员身份运行</strong>」</li>
          <li>重新打开本页面即可</li>
        </ol>
        <div v-if="hosts.exePath" class="exe-line">
          <span class="mono exe-path">{{ hosts.exePath }}</span>
          <v-btn size="x-small" variant="text" @click="copy(hosts.exePath, '程序路径')">
            复制路径
          </v-btn>
        </div>
      </v-alert>

      <v-text-field
        v-model="domain"
        label="域名"
        placeholder="fast3.example.com"
        hint="只填域名本身，不要带 https:// 或末尾斜杠；粘贴完整网址会自动清理"
        persistent-hint
        density="compact"
        prepend-inner-icon="mdi-web"
        clearable
        autocomplete="off"
        @blur="normalizeDomain"
      />

      <v-text-field
        v-model="targetIP"
        label="IP"
        placeholder="例如 1.2.3.4"
        density="compact"
        prepend-inner-icon="mdi-ip-network-outline"
        :error="!!ipState.msg"
        :error-messages="ipState.msg"
        hint="可以直接粘贴 IP:端口#备注；hosts 只对 443 端口有效，其它端口会提示原因"
        persistent-hint
        class="ip-input"
        autocomplete="off"
        @blur="normalizeIPInput"
      />

      <div class="ip-label">
        从测速结果中选择
        <span v-if="candidates.length" class="ip-count">（{{ candidates.length }} 个 443 端口的结果）</span>
      </div>

      <div v-if="!candidates.length" class="empty">
        暂无测速结果 —— 也可以直接在上面手动填写 IP
      </div>

      <div v-else class="ip-list">
        <button
          v-for="c in candidates"
          :key="c.value"
          type="button"
          class="ip-item"
          :class="{ active: targetIP === c.value }"
          @click="targetIP = c.value"
        >
          <span class="ip-addr mono">{{ c.value }}</span>
          <span class="ip-meta">{{ c.delay }}ms · {{ c.speed }}MB/s · {{ c.colo }}</span>
        </button>
      </div>

      <div class="d-flex justify-end mt-2">
        <v-btn
          color="primary"
          variant="flat"
          :loading="busy"
          :disabled="!hosts.isAdmin || !domain.trim() || !ipState.ok"
          @click="apply"
        >
          应用到 Hosts
        </v-btn>
      </div>

      <div class="hint note">
        <strong>原理</strong>：hosts 让这台电脑把域名直接解析到你选的 IP，绕过 DNS。
        <br />
        <strong>生效</strong>：写入后会自动刷新 DNS 缓存，不用重启电脑，浏览器强制刷新（Ctrl+F5）即可。
        <br />
        <strong>注意</strong>：IP 失效后该域名会打不开，重新测速再应用一次即可；修改前会自动备份原 hosts。
      </div>

      <v-divider class="my-3" />

      <div class="d-flex align-center mb-2">
        <span class="text-body-2 text-medium-emphasis">当前已应用</span>
        <v-spacer />
        <v-btn
          size="small"
          variant="text"
          color="error"
          :disabled="busy || !hosts.entries.length || !hosts.isAdmin"
          @click="remove"
        >
          移除
        </v-btn>
      </div>

      <div v-if="!hosts.entries.length" class="empty">暂无</div>
      <div v-for="e in hosts.entries" :key="e.domain" class="entry">
        <span class="mono entry-ip">{{ e.ip }}</span>
        <span class="mono entry-domain">{{ e.domain }}</span>
      </div>

      <div class="path-line">hosts 路径：<span class="mono">{{ hosts.hostsPath }}</span></div>
    </v-card>

    <!-- 客户端优化 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-tune-variant</v-icon>
        <span class="section-title">客户端优化</span>
        <span class="section-desc">减少「订阅更新失败」</span>
      </div>

      <!-- 1. 订阅走直连 -->
      <div class="step">
        <div class="step-no">1</div>
        <div class="step-body">
          <div class="step-title">让订阅更新走直连</div>
          <div class="hint">
            更新订阅时如果走了代理，就会用到当前节点；节点失效时订阅永远更新不了，形成死循环。
            在规则里给你的域名加一条 DIRECT：
          </div>
          <pre class="code-block">{{ clashRule }}</pre>
          <v-btn size="small" variant="tonal" prepend-icon="mdi-content-copy" class="mt-1" @click="copy(clashRule, '规则片段')">
            复制规则
          </v-btn>
        </div>
      </div>

      <!-- 2. 超时 -->
      <div class="step">
        <div class="step-no">2</div>
        <div class="step-body">
          <div class="step-title">把订阅请求超时调大</div>
          <div class="hint">
            客户端默认等待约 20 秒。国内到 Cloudflare 偶尔会因丢包触发 TCP 重传，单次请求可能超过 15 秒。
            在客户端设置里找到「HTTP 请求超时」，改为 <strong>60 秒</strong>。
          </div>
        </div>
      </div>

      <!-- 3. 排查 -->
      <div class="step">
        <div class="step-no">3</div>
        <div class="step-body">
          <div class="step-title">仍然失败时按顺序排查</div>
          <ul class="checklist">
            <li>先用浏览器直接打开订阅链接，确认能返回内容</li>
            <li>关闭「更新订阅时使用代理」后再试</li>
            <li>减少填在订阅里的 IP 数量（10~20 个足够）</li>
            <li>把订阅链接里的域名加入 DIRECT 规则</li>
            <li>查看客户端日志里的具体报错：timeout / 404 / 解析失败</li>
          </ul>
        </div>
      </div>
    </v-card>
  </div>
</template>

<style scoped>
.optimize {
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

.hint {
  font-size: 12px;
  line-height: 1.6;
  color: #71717a;
  margin: 0 0 8px;
}

.hint.note {
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: 6px;
  background: #17171c;
  border-left: 3px solid #4f6acd;
}

/* 非管理员提示：给出可照做的步骤和程序路径 */
.admin-steps {
  margin: 6px 0 8px;
  padding-left: 20px;
  font-size: 12.5px;
  line-height: 1.9;
}

.exe-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.exe-path {
  font-size: 11.5px;
  color: #d4b26a;
  background: rgba(0, 0, 0, 0.28);
  padding: 3px 8px;
  border-radius: 4px;
  word-break: break-all;
}

.empty {
  font-size: 13px;
  color: #71717a;
  padding: 8px 0;
}

/* IP 输入与候选列表 */
.ip-input {
  margin-top: 4px;
}

.ip-label {
  font-size: 12px;
  color: #8b8b96;
  margin: 16px 0 8px;
}

.ip-count {
  color: #5b5b66;
}

.ip-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 220px;
  overflow-y: auto;
}

.ip-item {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  padding: 9px 12px;
  border-radius: 6px;
  border: 1px solid #26262e;
  background: #17171c;
  cursor: pointer;
  text-align: left;
  font-family: inherit;
  transition: border-color 0.15s, background 0.15s;
}

.ip-item:hover {
  border-color: #4f6acd;
}

.ip-item.active {
  border-color: #4f6acd;
  background: rgba(79, 106, 205, 0.14);
}

.ip-addr {
  font-size: 13px;
  color: #e4e4e7;
  min-width: 118px;
}

.ip-meta {
  font-size: 12px;
  color: #8b8b96;
}

.entry {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 8px 12px;
  border-radius: 6px;
  background: #17171c;
  margin-bottom: 6px;
}

.entry-ip {
  font-size: 13px;
  color: #7c9cff;
}

.entry-domain {
  font-size: 13px;
  color: #a1a1aa;
}

.path-line {
  font-size: 11px;
  color: #5b5b66;
  margin-top: 12px;
}

/* 指引步骤 */
.step {
  display: flex;
  gap: 12px;
  padding: 12px 0;
  border-top: 1px solid #23232b;
}

.step:first-of-type {
  border-top: none;
  padding-top: 0;
}

.step-no {
  flex: 0 0 auto;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: rgba(79, 106, 205, 0.18);
  color: #7c9cff;
  font-size: 12px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
}

.step-body {
  flex: 1;
  min-width: 0;
}

.step-title {
  font-size: 13.5px;
  font-weight: 600;
  color: #d4d4d8;
  margin-bottom: 4px;
}

.code-block {
  margin: 8px 0 6px;
  padding: 10px 12px;
  border-radius: 6px;
  background: #131318;
  border: 1px solid #26262e;
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 12px;
  color: #a5b4fc;
  white-space: pre-wrap;
  word-break: break-all;
}

.checklist {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.9;
  color: #8b8b96;
}

.checklist li::marker {
  color: #4f6acd;
}
</style>
