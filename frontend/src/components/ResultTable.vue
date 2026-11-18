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
  { title: '地区', key: 'colo', align: 'center', width: '92px' },
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

// 稳定性达标的节点，用于「复制稳定节点」和 Clash 配置生成。
//
// 门槛定在 B（成功率 90%+）而不是 S/A：A 级要求 100% 成功，
// 而国内到 Cloudflare 的链路拿不到这个数字，实测一批节点里
// 一个 A 都没有。按 S/A 筛会让这个功能永远筛出空列表。
function isStable(r) {
  const s = stabMap.value.get(keyOf(r))
  return !!s && (s.score === 'S' || s.score === 'A' || s.score === 'B')
}

const stableList = computed(() => props.results.filter(isStable))

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

// IATA 三字码 → 中文城市名。
// Cloudflare 的 cf-ray 头部返回的是机场代码（SIN/HKG/LAX），
// 复制出去给别人看时没人知道那是什么，所以这里翻译一层。
const COLO_NAMES = {
  // 亚太
  HKG: '香港', SIN: '新加坡', NRT: '东京', KIX: '大阪', ITM: '大阪',
  TPE: '台北', KHH: '高雄', ICN: '首尔', PUS: '釜山',
  KUL: '吉隆坡', BKK: '曼谷', MNL: '马尼拉', HAN: '河内', SGN: '胡志明',
  CGK: '雅加达', DPS: '巴厘岛', DEL: '新德里', BOM: '孟买', MAA: '金奈',
  SYD: '悉尼', MEL: '墨尔本', AKL: '奥克兰', PER: '珀斯',
  // 北美
  LAX: '洛杉矶', SJC: '圣何塞', SFO: '旧金山', SEA: '西雅图',
  ORD: '芝加哥', IAD: '华盛顿', EWR: '纽约', JFK: '纽约',
  ATL: '亚特兰大', DFW: '达拉斯', MIA: '迈阿密', DEN: '丹佛',
  YYZ: '多伦多', YVR: '温哥华',
  // 欧洲
  FRA: '法兰克福', AMS: '阿姆斯特丹', LHR: '伦敦', MAN: '曼彻斯特',
  CDG: '巴黎', MAD: '马德里', FCO: '罗马', MXP: '米兰',
  WAW: '华沙', ARN: '斯德哥尔摩', HEL: '赫尔辛基', DUB: '都柏林',
  // 中东 / 非洲 / 南美
  DXB: '迪拜', TLV: '特拉维夫', JNB: '约翰内斯堡', GRU: '圣保罗',
  // 中国大陆（CF 一般不给大陆分配边缘节点，留作兜底）
  PEK: '北京', SHA: '上海', CAN: '广州', SZX: '深圳',
}

// coloLabel 返回中文城市名；未收录的代码原样返回，避免显示空白。
function coloLabel(colo) {
  if (!colo || colo === 'N/A') return 'N/A'
  return COLO_NAMES[String(colo).toUpperCase()] || colo
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
    .filter((x) => {
      if (x.score < 50) return false
      // 跑过稳定性测试的话，C/D 级不推荐——用户跑这个功能就是为了筛稳定节点，
      // 再把 60% 成功率的节点标成「推荐」等于把结论又推翻
      const s = stabMap.value.get(keyOf(x))
      if (s && (s.score === 'C' || s.score === 'D')) return false
      return true
    })
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

// 复制到剪贴板，WebView2 下 clipboard API 可能不可用，回退到 execCommand
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch (_) {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    document.body.appendChild(ta)
    ta.select()
    let ok = false
    try {
      ok = document.execCommand('copy')
    } catch (_) {
      ok = false
    }
    document.body.removeChild(ta)
    return ok
  }
}

// 复制为可直接粘贴到客户端 / 订阅框的格式
async function copyAs(format) {
  let list = props.results
  if (format === 'stable') {
    list = stableList.value
    if (!list.length) {
      emit(
        'copied',
        props.stability.length
          ? '这批 IP 里没有 B 级以上的节点，建议重新测速换一批'
          : '还没有稳定性数据，请先点「测试稳定性」'
      )
      return
    }
  }

  const lines = list.map((r) => {
    const port = r.port || 443
    switch (format) {
      case 'ip':
        return r.ip
      case 'ipport':
      case 'stable':
        return `${r.ip}:${port}`
      default:
        return `${r.ip}:${port}#${coloLabel(r.colo)}`
    }
  })
  if (!lines.length) return

  const ok = await copyText(lines.join('\n'))
  const label = format === 'stable' ? `S/A 级节点 ${lines.length} 个` : `${lines.length} 个 IP`
  emit('copied', ok ? `已复制 ${label}` : '复制失败，请检查剪贴板权限')
}

// ---- Clash Verge 配置生成 ----
const clashDialog = ref(false)
// 是否在脚本里按 IP 过滤掉不稳定节点（默认关闭，属于「激进」选项）
const filterStableOnly = ref(false)
// 统一延迟口径：排除握手耗时，让 url-test 比较真实 RTT。
// 默认关闭——它会把「握手慢」的成本藏起来，节点 TLS 还没达标时开反而有害。
const unifyDelay = ref(false)
// TCP 并发：对「DNS 解析出多个 IP」的网页访问有效，对固定 IP 的节点无效
const tcpConcurrent = ref(false)
// 把 fallback 里的明文 DNS 换成 DoH
const dnsOptimize = ref(false)
// 域名嗅探：Fake-IP 下 QUIC / 纯 IP 流量靠它才能命中域名规则。
// 属于「修复」性质，默认开启。
const sniffEnabled = ref(true)

function openClashDialog() {
  // 有稳定节点时默认勾上过滤，否则保持关闭
  filterStableOnly.value = stableList.value.length > 0
  clashDialog.value = true
}

// 生成可以直接粘进 Clash Verge「编辑脚本」的 JS。
//
// 为什么不生成完整策略组：CFSTX 只知道 IP，不知道用户在订阅里给节点起的名字，
// 硬塞节点名只会拼出一份连不上的配置。这里改成「改参数 + 按 IP 过滤」，
// 既不需要节点名，也能让 url-test 不再被烂节点污染。
const clashScript = computed(() => {
  const p = []
  let step = 0
  const next = () => {
    step++
    return `  // ${step}) `
  }

  p.push('// 由 CFSTX 生成于 ' + new Date().toLocaleString('zh-CN'))
  p.push('// 用法：Clash Verge → 订阅 → 右键 → 编辑脚本（Script）→ 粘贴 → 保存 → 重新应用订阅')
  p.push('')
  p.push('function main(config, profileName) {')
  p.push("  const groups = config['proxy-groups'] || []")
  p.push('')

  // ---- 1) 策略组参数 ----
  p.push(next() + '策略组参数优化')
  p.push("  const auto = groups.find((g) => g.type === 'url-test')")
  p.push('  if (auto) {')
  p.push('    auto.interval = 120    // 5 分钟 → 2 分钟，更早发现节点挂掉')
  p.push('    auto.tolerance = 100   // 抑制在相近延迟之间反复横跳')
  p.push('    auto.lazy = false      // 默认 true 时闲置不检测，第二天才发现 timeout')
  p.push('  }')
  p.push("  const fb = groups.find((g) => g.type === 'fallback')")
  p.push('  if (fb) {')
  p.push('    fb.interval = 60')
  p.push('    fb.lazy = false')
  p.push('  }')

  // ---- 2) 按 IP 过滤节点 ----
  if (filterStableOnly.value && stableList.value.length) {
    p.push('')
    p.push(next() + '只保留稳定性达标的节点（B 级以上）')
    p.push('  const stableIPs = new Set([')
    for (const r of stableList.value) {
      p.push(`    '${r.ip}',`)
    }
    p.push('  ])')
    p.push('  if (Array.isArray(config.proxies)) {')
    p.push('    // 先记下要被删掉的节点名，好同步清理策略组里的引用，')
    p.push('    // 否则策略组会指向不存在的节点，mihomo 直接报配置错误')
    p.push('    const removed = new Set(')
    p.push('      config.proxies')
    p.push('        .filter((x) => x.server && !stableIPs.has(x.server))')
    p.push('        .map((x) => x.name)')
    p.push('    )')
    p.push('    config.proxies = config.proxies.filter((x) => !removed.has(x.name))')
    p.push('    for (const g of groups) {')
    p.push('      if (Array.isArray(g.proxies)) {')
    p.push('        g.proxies = g.proxies.filter((n) => !removed.has(n))')
    p.push('      }')
    p.push('    }')
    p.push('  }')
  }

  // ---- 3) 统一延迟口径 ----
  if (unifyDelay.value) {
    p.push('')
    p.push(next() + '统一延迟口径：排除握手耗时，让 url-test 比较真实 RTT')
    p.push("  config['unified-delay'] = true")
    p.push('  // 必须用 https 测试地址：mihomo 会复用连接发第二次请求来测 RTT，')
    p.push('  // http 明文可能被劫持导致第二次失败，那样会退回含握手的延迟（等于没开）')
    p.push('  for (const g of groups) {')
    p.push("    if (typeof g.url === 'string' && g.url.indexOf('http://') === 0) {")
    p.push("      g.url = 'https://' + g.url.slice(7)")
    p.push('    }')
    p.push('  }')
  }

  // ---- 4) TCP 并发 ----
  if (tcpConcurrent.value) {
    p.push('')
    p.push(next() + 'TCP 并发：同一域名解析出多个 IP 时并行握手取最快')
    p.push('  // 只影响普通网页访问；节点 server 是固定 IP 时不起作用')
    p.push("  config['tcp-concurrent'] = true")
  }

  // ---- 5) DNS 兜底换 DoH ----
  if (dnsOptimize.value) {
    p.push('')
    p.push(next() + 'DNS：fallback 里的明文 DNS 换成 DoH，避免兜底本身被污染')
    p.push('  if (config.dns && Array.isArray(config.dns.fallback)) {')
    p.push('    config.dns.fallback = [')
    p.push("      'https://8.8.8.8/dns-query',")
    p.push("      'https://1.1.1.1/dns-query',")
    p.push('    ]')
    p.push('  }')
  }

  // ---- 6) 域名嗅探 ----
  if (sniffEnabled.value) {
    p.push('')
    p.push(next() + '域名嗅探：Fake-IP 模式下让 QUIC / 纯 IP 流量也能命中域名规则')
    p.push('  // 不开的话，YouTube / Google 这类走 QUIC 的流量拿不到域名，')
    p.push('  // 规则只能按 IP 匹配，容易分流到错误的分组')
    p.push('  config.sniffer = {')
    p.push('    enable: true,')
    p.push("    'force-dns-mapping': true,")
    p.push("    'parse-pure-ip': true,")
    p.push('    sniff: {')
    p.push("      HTTP: { ports: ['80', '8080-8880'], 'override-destination': true },")
    p.push("      TLS:  { ports: ['443', '8443'], 'override-destination': true },")
    p.push("      QUIC: { ports: ['443'] },")
    p.push('    },')
    p.push('    // 这几个走了会出问题（局域网设备发现、推送），跳过嗅探')
    p.push("    'skip-domain': [")
    p.push("      'Mijia Cloud',")
    p.push("      'dlg.io.mi.com',")
    p.push("      '+.push.apple.com',")
    p.push('    ],')
    p.push('  }')
  }

  p.push('')
  p.push('  return config')
  p.push('}')
  return p.join('\n')
})

async function copyClashScript() {
  const ok = await copyText(clashScript.value)
  emit('copied', ok ? '已复制 Clash 脚本，粘到订阅的「编辑脚本」里即可' : '复制失败，请检查剪贴板权限')
  if (ok) clashDialog.value = false
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
            <v-list-item-subtitle class="mono">1.2.3.4:443#香港</v-list-item-subtitle>
          </v-list-item>
          <v-divider class="my-1" />
          <v-list-item :disabled="!stableList.length" @click="copyAs('stable')">
            <v-list-item-title>
              仅稳定节点（B 级以上）
              <v-chip v-if="stableList.length" size="x-small" color="success" class="ml-2">
                {{ stableList.length }}
              </v-chip>
            </v-list-item-title>
            <v-list-item-subtitle class="mono">
              粘到 edgetunnel 的「自定义优选」
            </v-list-item-subtitle>
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

      <v-btn
        v-if="stability.length"
        variant="tonal"
        size="small"
        class="ml-2"
        prepend-icon="mdi-shield-lock-outline"
        @click="openClashDialog"
      >
        Clash 配置
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
        <v-chip
          size="x-small"
          variant="tonal"
          :color="item.colo === 'N/A' ? 'grey' : 'secondary'"
          :title="item.colo === 'N/A' ? '未获取到地区码' : `机场代码 ${item.colo}`"
          style="white-space: nowrap"
        >
          {{ coloLabel(item.colo) }}
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

    <!-- Clash Verge 配置 -->
    <v-dialog v-model="clashDialog" max-width="680">
      <v-card class="stab-dialog">
        <v-card-title class="d-flex align-center py-3">
          <v-icon class="mr-2" size="20">mdi-shield-lock-outline</v-icon>
          <span class="text-subtitle-1 font-weight-medium">Clash Verge 配置</span>
        </v-card-title>
        <v-divider />
        <v-card-text class="pt-4">
          <v-alert density="compact" variant="tonal" type="info" class="mb-4 text-caption">
            粘到 <strong>Clash Verge → 订阅 → 右键「编辑脚本」（Script）</strong> 里，保存后重新应用订阅。
            脚本不会被订阅更新覆盖。
          </v-alert>

          <v-switch
            v-model="filterStableOnly"
            :disabled="!stableList.length"
            color="primary"
            density="compact"
            hide-details
          >
            <template #label>
              <span class="text-body-2">只保留稳定节点</span>
              <span class="text-caption text-medium-emphasis ml-2">
                {{ stableList.length
                  ? `B 级以上，${stableList.length} 个（会删掉订阅里其余节点）`
                  : '这批 IP 没有 B 级以上的节点' }}
              </span>
            </template>
          </v-switch>

          <v-switch
            v-model="unifyDelay"
            color="primary"
            density="compact"
            hide-details
          >
            <template #label>
              <span class="text-body-2">统一延迟口径</span>
              <span class="text-caption text-medium-emphasis ml-2">
                排除握手耗时，并把测试地址改成 https
              </span>
            </template>
          </v-switch>

          <v-switch
            v-model="tcpConcurrent"
            color="primary"
            density="compact"
            hide-details
          >
            <template #label>
              <span class="text-body-2">TCP 并发</span>
              <span class="text-caption text-medium-emphasis ml-2">
                网页访问更快；对固定 IP 节点无效
              </span>
            </template>
          </v-switch>

          <v-switch
            v-model="dnsOptimize"
            color="primary"
            density="compact"
            hide-details
          >
            <template #label>
              <span class="text-body-2">DNS 兜底换 DoH</span>
              <span class="text-caption text-medium-emphasis ml-2">
                fallback 的明文 DNS 可能被污染
              </span>
            </template>
          </v-switch>

          <v-switch
            v-model="sniffEnabled"
            color="primary"
            density="compact"
            hide-details
            class="mb-3"
          >
            <template #label>
              <span class="text-body-2">域名嗅探</span>
              <span class="text-caption text-medium-emphasis ml-2">
                Fake-IP 下 QUIC 流量靠它命中域名规则
              </span>
            </template>
          </v-switch>

          <v-alert
            v-if="unifyDelay"
            density="compact"
            variant="tonal"
            type="warning"
            class="mb-3 text-caption"
          >
            统一延迟会让 url-test 看不到握手成本。建议先用稳定性测试筛掉 TLS 慢的节点再开，
            否则容易选到「RTT 低但首次打开要等几秒」的节点。
          </v-alert>

          <pre class="code-preview">{{ clashScript }}</pre>
        </v-card-text>
        <v-divider />
        <v-card-actions class="px-4 py-3">
          <v-spacer />
          <v-btn variant="text" @click="clashDialog = false">关闭</v-btn>
          <v-btn
            color="primary"
            variant="flat"
            prepend-icon="mdi-content-copy"
            @click="copyClashScript"
          >
            复制脚本
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

/* 生成的脚本预览 */
.code-preview {
  max-height: 340px;
  overflow: auto;
  margin: 0;
  padding: 12px 14px;
  border-radius: 8px;
  background: #131318;
  border: 1px solid #2a2a33;
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 11.5px;
  line-height: 1.65;
  color: #c8c8d0;
  white-space: pre;
  tab-size: 2;
}
</style>
