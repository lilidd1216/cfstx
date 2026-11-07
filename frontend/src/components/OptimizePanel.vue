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
// 一个域名归一化成纯 hostname。与后端 task.NormalizeProbeDomain 一致：
// 用户从地址栏粘来的 https://xxx/ 如果原样写进 hosts，是一条无效记录。
function normalizeOneDomain(s) {
  let v = String(s || '').trim()
  if (!v) return ''
  const scheme = v.indexOf('://')
  if (scheme >= 0) v = v.slice(scheme + 3)
  const cut = v.search(/[/?#]/)
  if (cut >= 0) v = v.slice(0, cut)
  v = v.replace(/:\d+$/, '').replace(/\.$/, '')
  return v.toLowerCase()
}

// 支持一次填多个域名：hosts 不支持 *.xxx 通配符，
// 想覆盖多个子域名只能逐个写全，所以这里允许换行/逗号/分号/空格分隔。
function splitDomainInput(raw) {
  return String(raw || '')
    .split(/[\s,;]+/)
    .map(normalizeOneDomain)
    .filter(Boolean)
}

function normalizeDomain() {
  const list = splitDomainInput(domain.value)
  if (!list.length) return
  domain.value = [...new Set(list)].join('\n')
}

// 域名侧的问题提前拦住，别等点了按钮才报错
const domainState = computed(() => {
  const raw = String(domain.value || '').trim()
  if (!raw) return { ok: false, msg: '', count: 0 }

  const list = splitDomainInput(raw)
  if (!list.length) return { ok: false, msg: '', count: 0 }

  const wildcard = list.find((d) => d.includes('*'))
  if (wildcard) {
    return {
      ok: false,
      msg: `hosts 不支持通配符（${wildcard}）。请把要用到的子域名逐个写全，每行一个`,
      count: list.length,
    }
  }
  return { ok: true, msg: '', count: list.length }
})

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
  const list = splitDomainInput(domain.value)
  if (!list.length) return '# 先在上方「域名」中填写你的域名，这里会自动生成'
  const rules = list.map((d) => `  - DOMAIN-SUFFIX,${d},DIRECT`).join('\n')
  return `rules:\n${rules}`
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
  const list = splitDomainInput(domain.value)
  const label = list.length === 1 ? list[0] : `${list.length} 个域名`
  try {
    await httpPost('/api/hosts/apply', { domain: list.join('\n'), ip })
    await loadHosts()
    emit('toast', `已写入 hosts：${ip} → ${label}（DNS 缓存已刷新）`, 'success')
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

      <v-textarea
        v-model="domain"
        label="域名"
        :placeholder="'fast3.example.com'"
        hint="多个域名用换行或逗号分隔，都会指向同一个 IP；hosts 不支持 *.xxx 通配符，需要逐个写全"
        persistent-hint
        rows="2"
        auto-grow
        density="compact"
        prepend-inner-icon="mdi-web"
        :error="!!domainState.msg"
        :error-messages="domainState.msg"
        autocomplete="off"
        class="domain-input"
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
          :disabled="!hosts.isAdmin || !domainState.ok || !ipState.ok"
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

    <!-- 进阶：多账号与节点池 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-server-network</v-icon>
        <span class="section-title">进阶：多账号与节点池</span>
        <span class="section-desc">单个账号 10 万请求/天不够用时</span>
      </div>

      <v-alert type="info" variant="tonal" density="compact" class="mb-4" icon="mdi-gauge">
        <strong>先确认你缺不缺</strong>：Cloudflare 后台 → Workers 和 Pages → 看「请求」曲线。
        日请求不到 5 万就别折腾，多账号的维护成本远高于收益。
      </v-alert>

      <v-expansion-panels variant="accordion" class="adv-panels">

        <!-- ① 方案对比 -->
        <v-expansion-panel>
          <v-expansion-panel-title>
            <span class="panel-title">① 三条路：先选方向</span>
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <table class="cmp-table">
              <thead>
                <tr><th>方案</th><th>额度</th><th>成本</th><th>复杂度</th><th>风险</th></tr>
              </thead>
              <tbody>
                <tr>
                  <td><strong>付费 Workers</strong></td>
                  <td>1000 万/天</td>
                  <td>$5/月</td>
                  <td>后台点一下</td>
                  <td>无</td>
                </tr>
                <tr>
                  <td><strong>多账号多节点</strong></td>
                  <td>10 万 × N</td>
                  <td>免费</td>
                  <td>每个账号部署一次</td>
                  <td>账号关联、ToS</td>
                </tr>
                <tr>
                  <td><strong>Snippets 绕过</strong></td>
                  <td>不按 Worker 计费</td>
                  <td>免费</td>
                  <td>中</td>
                  <td>功能受限，不能用 connect()</td>
                </tr>
              </tbody>
            </table>

            <div class="hint mt-3">
              <strong>怎么选</strong>：
              <ul class="checklist">
                <li>能接受每月 $5 → <strong>直接付费</strong>，1000 万请求是免费的 100 倍，最省心</li>
                <li>完全不想付费、且有多个现成账号 → 多账号</li>
                <li>只想跑纯 HTTP 转发、不需要 connect() → Snippets</li>
              </ul>
            </div>

            <div class="hint note-box mt-3">
              <strong>Claude 的判断</strong>：为了省 $5 去注册并维护 N 个账号，
              要承担 N 份封号风险，通常不划算。多账号适合「就是不想付钱」的人，不适合「想省事」的人。
            </div>
          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- ② 多账号配置 -->
        <v-expansion-panel>
          <v-expansion-panel-title>
            <span class="panel-title">② 多账号多节点：具体怎么配</span>
          </v-expansion-panel-title>
          <v-expansion-panel-text>

            <div class="warn-strip">
              <strong>前提</strong>：必须是<strong>真正不同的 Cloudflare 账号</strong>。
              同一个账号下开 3 个 Worker，额度是<strong>共享</strong>的，不会变成 30 万。
            </div>

            <div class="step">
              <div class="step-no">1</div>
              <div class="step-body">
                <div class="step-title">面板账号：记下 UUID</div>
                <div class="hint">
                  打开已有的 EdgeTunnel 2.0 面板 → <strong>⚙️ 详细配置信息</strong> → 复制 <code>UUID</code>。
                  这一串是所有节点共用的身份标识。
                </div>
              </div>
            </div>

            <div class="step">
              <div class="step-no">2</div>
              <div class="step-body">
                <div class="step-title">第二账号：部署节点</div>
                <div class="hint">
                  换一个 Cloudflare 账号，用同一份 <code>edgetunnel-main.zip</code> 部署 Pages。
                  <strong>不要设 ADMIN，不要绑 KV</strong> —— 它只当节点，不需要管理面板。
                </div>
              </div>
            </div>

            <div class="step">
              <div class="step-no">3</div>
              <div class="step-body">
                <div class="step-title">给节点设 UUID</div>
                <div class="hint">
                  第二账号的项目 → 设置 → 变量 → 添加：名称为 <code>UUID</code>，值为第一步复制的那个。
                  然后<strong>重新部署一次</strong>让变量生效。
                </div>
                <pre class="code-block">变量名  UUID
变量值  8fc6a982-9862-4362-846f-a7039c5e2e47   ← 换成你自己的</pre>
              </div>
            </div>

            <div class="step">
              <div class="step-no">4</div>
              <div class="step-body">
                <div class="step-title">给节点绑域名</div>
                <div class="hint">
                  第二账号的项目 → 自定义域 → 绑一个<strong>和面板不同的</strong>子域名，例如：
                </div>
                <pre class="code-block">面板     edt2.example.com
节点 2   edt2-2.example.com
节点 3   edt2-3.example.com</pre>
                <div class="hint mt-1">绑完等 10~30 分钟验证通过。</div>
              </div>
            </div>

            <div class="step">
              <div class="step-no">5</div>
              <div class="step-body">
                <div class="step-title">面板把节点域名加进 HOSTS</div>
                <div class="hint">
                  回到面板 → <strong>⚙️ 详细配置信息</strong> → <code>HOSTS</code>，把节点 2、节点 3 的域名都加进去，保存。
                </div>
                <pre class="code-block">HOSTS:
edt2.example.com
edt2-2.example.com
edt2-3.example.com</pre>
              </div>
            </div>

            <div class="step">
              <div class="step-no">6</div>
              <div class="step-body">
                <div class="step-title">重新拉订阅验证</div>
                <div class="hint">
                  客户端删掉旧订阅重新导入。节点列表里应该出现不同域名的节点，顺序每次刷新都不一样。
                </div>
                <div class="hint mt-1">
                  <strong>顺序会变是正常的</strong> —— 面板每次生成订阅都会把 HOSTS 打乱，
                  让流量自然分散到不同账号。
                </div>
              </div>
            </div>

            <div class="hint note-box mt-3">
              <strong>它为什么能降低 SNI 阻断</strong>
              <br />
              面板在替换订阅里的域名时，会把域名中的 <code>*</code> 换成 3~16 位随机字符：
              <br />
              <code>*.example.com</code> → <code>a3k9x2f.example.com</code>
              <br />
              每次订阅拿到的子域都不同，批量检测很难建立稳定的黑名单。
              这条与账号数量无关，<strong>单个账号也能用</strong>。
            </div>

            <div class="hint note-box mt-3 warn">
              <strong>三个必须注意的</strong>
              <ul class="checklist">
                <li><strong>账号之间不能有关联特征</strong>：同一邮箱后缀、同一张卡、同一 IP 批量注册，会被风控串起来一起封</li>
                <li><strong>不要公开分享节点</strong>：一旦被滥用，轻则限速重则封号</li>
                <li><strong>某个账号挂了只影响它自己的节点</strong>：这正是多账号的价值——故障被隔离了</li>
              </ul>
            </div>

          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- ③ 开源项目 -->
        <v-expansion-panel>
          <v-expansion-panel-title>
            <span class="panel-title">③ 开源项目：别人是怎么做的</span>
          </v-expansion-panel-title>
          <v-expansion-panel-text>

            <div class="hint mb-3">
              这几个项目都在解决同一个问题——<strong>把请求分散到多个 Worker / 多个账号</strong>。
              按需选用，不必全上。
            </div>

            <div class="proj-card">
              <div class="proj-head">
                <span class="proj-name">FlareTunnel</span>
                <span class="proj-lang">Go</span>
                <span class="proj-tag">多账号管理</span>
              </div>
              <div class="proj-desc">
                自动部署多个 Worker 作为代理端点，支持随机 / 轮询负载均衡。
                最有价值的是<strong>按账号的配额统计</strong>——能看出哪个账号快用完了。
                还带黑名单过滤。
              </div>
              <div class="proj-meta">
                <span>适合：想统一管理多个账号，需要知道每个账号的剩余额度</span>
                <br />
                <a href="https://github.com/MorDavid/FlareTunnel" target="_blank" rel="noopener">github.com/MorDavid/FlareTunnel</a>
              </div>
            </div>

            <div class="proj-card">
              <div class="proj-head">
                <span class="proj-name">flaregun</span>
                <span class="proj-lang">npm</span>
                <span class="proj-tag">一行命令部署</span>
              </div>
              <div class="proj-desc">
                <code>flaregun up 5</code> 直接部署 5 个 Worker。
                三种轮换策略：round-robin、random、<strong>adaptive</strong>
                （遇到 429 / 5xx 自动指数退避）。所有命令支持 <code>--json</code>，方便脚本化。
              </div>
              <div class="proj-meta">
                <span>适合：想要最少手工步骤，快速拉起一批</span>
                <br />
                <a href="https://www.npmjs.com/package/@miclivs/flaregun" target="_blank" rel="noopener">npmjs.com/package/@miclivs/flaregun</a>
              </div>
            </div>

            <div class="proj-card">
              <div class="proj-head">
                <span class="proj-name">ech-snippets</span>
                <span class="proj-lang">JS</span>
                <span class="proj-tag">绕过额度</span>
              </div>
              <div class="proj-desc">
                思路和别人不一样：把代码改成用 <strong>Cloudflare Snippets</strong> 部署。
                Snippets <strong>不按 Worker 请求计费</strong>，因此完全绕开每天 10 万的限制。
              </div>
              <div class="proj-meta">
                <span>适合：纯 HTTP 转发场景</span>
                <br />
                <span class="proj-warn">注意：Snippets 不能使用 connect()，只能做 HTTP 层转发</span>
                <br />
                <a href="https://github.com/Neomanbeta/ech-snippets" target="_blank" rel="noopener">github.com/Neomanbeta/ech-snippets</a>
              </div>
            </div>

            <div class="proj-card">
              <div class="proj-head">
                <span class="proj-name">cf-relay</span>
                <span class="proj-lang">npm</span>
                <span class="proj-tag">极简中继</span>
              </div>
              <div class="proj-desc">
                一键部署的边缘中继 Worker，通过 <code>x-relay-target</code> / <code>x-relay-path</code>
                请求头转发。官方建议「在多个 Cloudflare 账号部署同一 Worker」来获得更多出口 IP 和配额。
              </div>
              <div class="proj-meta">
                <span>适合：只要一个最简单的转发层</span>
                <br />
                <a href="https://www.npmjs.com/package/cf-relay" target="_blank" rel="noopener">npmjs.com/package/cf-relay</a>
              </div>
            </div>

            <div class="hint note-box mt-3">
              <strong>和 EdgeTunnel 2.0 自带方案的关系</strong>
              <br />
              EdgeTunnel 2.0 的「单面板多节点」已经覆盖了大部分需求，上面这些项目是<strong>替代或补充</strong>：
              <ul class="checklist">
                <li>想要配额统计 → FlareTunnel</li>
                <li>想要最少手工 → flaregun</li>
                <li>想彻底绕开 Worker 计费 → ech-snippets</li>
                <li>只要转发，不要面板 → cf-relay</li>
              </ul>
            </div>

          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- ④ 和 CFSTX 配合 -->
        <v-expansion-panel>
          <v-expansion-panel-title>
            <span class="panel-title">④ 和 CFSTX 配合：优选 IP 怎么喂进去</span>
          </v-expansion-panel-title>
          <v-expansion-panel-text>

            <div class="hint mb-3">
              多节点部署好之后，每个<strong>域名</strong>都需要各自的优选 IP——
              不同域名的边缘路由不一样，同一批 IP 不一定都服务它们。
            </div>

            <div class="step">
              <div class="step-no">1</div>
              <div class="step-body">
                <div class="step-title">在「优选IP」页打开自定义测速域名</div>
                <div class="hint">
                  填你要用的那个节点域名，测出只服务它的 IP。
                  <strong>有几个节点域名就测几次</strong>，各测各的。
                </div>
              </div>
            </div>

            <div class="step">
              <div class="step-no">2</div>
              <div class="step-body">
                <div class="step-title">导出 TXT</div>
                <div class="hint">
                  CFSTX 导出的格式是 <code>IP:端口#地区 速度</code>，
                  和 EdgeTunnel 2.0 的「自定义优选」格式<strong>天然兼容</strong>：
                </div>
                <pre class="code-block">27.44.142.135:10001#HKG 40.66MB/s
120.240.179.158:10001#HKG 25.22MB/s
104.234.174.220:443#SIN 12.06MB/s</pre>
              </div>
            </div>

            <div class="step">
              <div class="step-no">3</div>
              <div class="step-body">
                <div class="step-title">粘进 EdgeTunnel 2.0</div>
                <div class="hint">
                  面板 → 自定义优选 → 手动添加 → 粘贴 → 保存。
                  需要 EdgeTunnel 自动定时拉取的，可以用「优选 API」，
                  填一个返回同样格式的 URL。
                </div>
              </div>
            </div>

            <div class="hint note-box mt-3">
              <strong>当前限制</strong>：CFSTX 一次只能探测<strong>一个</strong>域名。
              有多个节点域名时，只能一个个测、分别导出。
              <br />
              多域名探测在计划中，还没做。
            </div>

          </v-expansion-panel-text>
        </v-expansion-panel>

      </v-expansion-panels>
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

/* 域名输入允许换行 */
.domain-input :deep(textarea) {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 12.5px;
  line-height: 1.7;
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

/* ---------------------------------------------------------------- */
/* 进阶：多账号与节点池                                              */
/* ---------------------------------------------------------------- */

.adv-panels {
  background: transparent;
}

.adv-panels :deep(.v-expansion-panel) {
  background: #17171c !important;
  border: 1px solid #26262e;
  border-radius: 8px !important;
  margin-bottom: 8px;
}

.adv-panels :deep(.v-expansion-panel-title) {
  min-height: 46px;
  font-size: 13px;
  color: #d4d4d8;
  padding: 0 14px;
}

.adv-panels :deep(.v-expansion-panel-title--active) {
  color: #c7d2fe;
}

.adv-panels :deep(.v-expansion-panel-text__wrapper) {
  padding: 4px 14px 16px;
}

.adv-panels :deep(.v-expansion-panel-title .v-expansion-panel-title__icon) {
  margin-left: auto;
}

.panel-title {
  font-weight: 600;
}

/* 方案对比表 */
.cmp-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
  margin: 4px 0 8px;
}

.cmp-table th {
  text-align: left;
  font-weight: 600;
  color: #a1a1aa;
  padding: 7px 10px;
  border-bottom: 1px solid #2a2a33;
  white-space: nowrap;
}

.cmp-table td {
  padding: 7px 10px;
  color: #8b8b96;
  border-bottom: 1px solid #202027;
  vertical-align: top;
}

.cmp-table td strong {
  color: #d4d4d8;
}

.cmp-table tbody tr:last-child td {
  border-bottom: none;
}

/* 提示块 */
.note-box {
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: 6px;
  background: #17171c;
  border-left: 3px solid #4f6acd;
  font-size: 12px;
  line-height: 1.8;
  color: #8b8b96;
}

.note-box code,
.hint code {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 11.5px;
  color: #a5b4fc;
  background: rgba(79, 106, 205, 0.12);
  padding: 1px 4px;
  border-radius: 3px;
}

.note-box.warn {
  border-left-color: #e8a33d;
}

/* 前提/警告条 */
.warn-strip {
  margin-bottom: 14px;
  padding: 10px 12px;
  border-radius: 6px;
  background: rgba(232, 163, 61, 0.08);
  border: 1px solid rgba(232, 163, 61, 0.25);
  font-size: 12px;
  line-height: 1.8;
  color: #d4b26a;
}

.warn-strip strong {
  color: #e8a33d;
}

/* 开源项目卡片 */
.proj-card {
  margin-bottom: 10px;
  padding: 12px 14px;
  border-radius: 8px;
  background: #131318;
  border: 1px solid #26262e;
}

.proj-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}

.proj-name {
  font-size: 13.5px;
  font-weight: 600;
  color: #e4e4e7;
}

.proj-lang {
  font-size: 10.5px;
  font-weight: 600;
  color: #7c9cff;
  background: rgba(79, 106, 205, 0.16);
  padding: 1px 7px;
  border-radius: 10px;
}

.proj-tag {
  font-size: 10.5px;
  color: #5fbf7f;
  background: rgba(95, 191, 127, 0.14);
  padding: 1px 7px;
  border-radius: 10px;
}

.proj-desc {
  font-size: 12px;
  line-height: 1.75;
  color: #8b8b96;
  margin-bottom: 6px;
}

.proj-desc code {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 11px;
  color: #a5b4fc;
  background: rgba(79, 106, 205, 0.12);
  padding: 1px 4px;
  border-radius: 3px;
}

.proj-meta {
  font-size: 11.5px;
  line-height: 1.8;
  color: #71717a;
}

.proj-meta a {
  color: #7c9cff;
  text-decoration: none;
  word-break: break-all;
}

.proj-meta a:hover {
  text-decoration: underline;
}

.proj-warn {
  color: #d4b26a;
}
</style>
