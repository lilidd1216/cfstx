<script setup>
import { ref, computed, onMounted, watch } from 'vue'

const props = defineProps({
  results: { type: Array, default: () => [] },
})
const emit = defineEmits(['toast'])

const STORAGE_DOMAIN = 'cfstx.hosts.domain'
const STORAGE_IP = 'cfstx.hosts.ip'

const hosts = ref({ isAdmin: false, hostsPath: '', entries: [], backupDir: '' })
// 记住上次填写的域名与 IP，切换页面或重启后不用重填
const domain = ref(localStorage.getItem(STORAGE_DOMAIN) || '')
const selectedIP = ref(localStorage.getItem(STORAGE_IP) || '')
const busy = ref(false)

watch(domain, (v) => localStorage.setItem(STORAGE_DOMAIN, v || ''))
watch(selectedIP, (v) => localStorage.setItem(STORAGE_IP, v || ''))

// hosts 只能映射 IP、不能指定端口，所以只列 443 端口的结果。
// 用点击列表而不是下拉框：避免浮层组件在 WebView2 中的兼容问题。
const candidates = computed(() =>
  props.results
    .filter((r) => (r.port || 443) === 443)
    .map((r) => ({
      value: r.ip,
      delay: r.delay.toFixed(0),
      speed: r.speed.toFixed(1),
      colo: r.colo,
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
    hosts.value = await httpGet('/api/hosts')
  } catch (e) {
    emit('toast', '读取 hosts 状态失败：' + e.message, 'error')
  }
}

async function apply() {
  busy.value = true
  try {
    await httpPost('/api/hosts/apply', { domain: domain.value.trim(), ip: selectedIP.value })
    await loadHosts()
    emit('toast', `已写入 hosts：${selectedIP.value} → ${domain.value.trim()}`, 'success')
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
        class="mb-4"
        icon="mdi-shield-key-outline"
      >
        当前不是管理员权限，无法修改 hosts。请关闭 CFSTX，右键选择「以管理员身份运行」后再试。
      </v-alert>

      <v-text-field
        v-model="domain"
        label="域名"
        placeholder="fast3.example.com"
        density="compact"
        prepend-inner-icon="mdi-web"
        clearable
        autocomplete="off"
        hide-details="auto"
      />

      <div class="ip-label">选择要应用的 IP（仅列出 443 端口的结果）</div>

      <div v-if="!candidates.length" class="empty">
        暂无可用 IP —— 先在「优选IP」页面跑一次测速
      </div>

      <div v-else class="ip-list">
        <button
          v-for="c in candidates"
          :key="c.value"
          type="button"
          class="ip-item"
          :class="{ active: selectedIP === c.value }"
          @click="selectedIP = c.value"
        >
          <span class="ip-addr mono">{{ c.value }}</span>
          <span class="ip-meta">{{ c.delay }}ms · {{ c.speed }}MB/s · {{ c.colo }}</span>
        </button>
      </div>

      <div class="d-flex justify-end mt-3">
        <v-btn
          color="primary"
          variant="flat"
          :loading="busy"
          :disabled="!hosts.isAdmin || !domain.trim() || !selectedIP"
          @click="apply"
        >
          应用到 Hosts
        </v-btn>
      </div>

      <div class="hint note">
        <strong>原理</strong>：hosts 让这台电脑把域名直接解析到你选的 IP，绕过 DNS。
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

.empty {
  font-size: 13px;
  color: #71717a;
  padding: 8px 0;
}

/* IP 候选列表 */
.ip-label {
  font-size: 12px;
  color: #8b8b96;
  margin: 16px 0 8px;
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
