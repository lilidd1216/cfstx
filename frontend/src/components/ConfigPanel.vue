<script setup>
import { computed } from 'vue'
import { countryOptions } from '../constants/countries.js'

const config = defineModel('config', { type: Object, required: true })
// 端到端凭据单独走一个 model，不混进 config，避免被一起持久化。
const secrets = defineModel('secrets', {
  type: Object,
  default: () => ({ uuid: '', password: '', wsPath: '' }),
})
const props = defineProps({
  running: { type: Boolean, default: false },
  e2eCredStatus: {
    type: Object,
    default: () => ({ hasUuid: false, hasPassword: false, hasPath: false }),
  },
  poolCount: { type: Number, default: 0 },
  // 在线 IP 抓取状态：由 App 统一维护，这里只负责展示
  fetchStatus: { type: Object, default: () => ({ fetching: false, total: 0, time: '' }) },
  // 从面板自动获取端到端凭据时的加载状态
  fetchingE2E: { type: Boolean, default: false },
})
const emit = defineEmits([
  'pick-ip-file', 'open-bestcf', 'open-cmliu', 'open-cfdomains', 'apply-preset',
  'fetch-sources', 'clear-pool', 'fetch-e2e-from-panel',
])

const disabled = computed(() => props.running)

// 端到端协议与内置目标（v-combobox 允许自定义）
const protocols = [
  { title: 'VLESS', value: 'vless' },
  { title: 'Trojan', value: 'trojan' },
]
const targets = [
  'http://cp.cloudflare.com/generate_204',
  'http://www.gstatic.com/generate_204',
]

// 国家/地区选项已抽取到 ../constants/countries.js，与自动同步面板共用同一份。

// 把用户从地址栏粘来的 https://xxx/ 归一成纯 host
function normalizeE2EHost() {
  let v = String(config.value.e2eHost || '').trim()
  if (v.includes('://')) v = v.split('://')[1]
  v = v.split(/[/?#]/)[0]
  config.value.e2eHost = v.toLowerCase()
}

// 只有勾了「需要联网抓取」的来源，「获取 IP」才有意义。
// ip.txt / 手动输入是本地数据，不需要联网。
const ONLINE_SOURCES = ['bestcf', 'official', 'cmliu', 'cfdomains', 'custom']
const hasOnlineSource = computed(() =>
  (config.value.sources || []).some((s) => ONLINE_SOURCES.includes(s))
)

// 与后端 task.NormalizeProbeDomain 保持一致：粘贴浏览器地址后立即清理，
// 避免 https://xxx/ 被原样拼进探测 URL（v1.1.5 的全数剔除问题）。
function normalizeProbeDomain() {
  let v = String(config.value.probeDomain || '').trim()
  if (!v) return
  const scheme = v.indexOf('://')
  if (scheme >= 0) v = v.slice(scheme + 3)
  const cut = v.search(/[/?#]/)
  if (cut >= 0) v = v.slice(0, cut)
  v = v.replace(/:\d+$/, '').replace(/\.$/, '')
  config.value.probeDomain = v.toLowerCase()
}

// 场景预设：覆盖大多数用户的实际用法，套用后仍可手动微调
const presets = [
  {
    key: 'beginner',
    icon: 'mdi-school-outline',
    color: 'success',
    label: '新手推荐',
    desc: '内置 + 官方网段，只测延迟，最快出结果',
  },
  {
    key: 'worker',
    icon: 'mdi-cloud-outline',
    color: 'primary',
    label: 'Worker 专用',
    desc: '开启域名探测，只保留真正服务你域名的 IP',
  },
  {
    key: 'fast',
    icon: 'mdi-flash-outline',
    color: 'warning',
    label: '极速扫描',
    desc: '不测下载，延迟上限放宽，快速筛一轮',
  },
  {
    key: 'e2e',
    icon: 'mdi-shield-check-outline',
    color: 'deep-purple',
    label: '客户端口径',
    desc: '开启端到端实测，只保留客户端里真正能用的 IP',
  },
]

// 可多选的 IP 来源；ip.txt 始终启用，由界面上固定勾选的只读项表示
const sources = computed({
  get: () => config.value.sources || [],
  set: (val) => {
    config.value.sources = val
  },
})

const manualEnabled = computed(() => sources.value.includes('manual'))
const customEnabled = computed(() => sources.value.includes('custom'))
</script>

<template>
  <div class="settings">
    <!-- 场景预设 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-lightning-bolt-outline</v-icon>
        <span class="section-title">场景预设</span>
        <span class="section-desc">一键套用常用组合，之后仍可手动微调</span>
      </div>

      <div class="preset-row">
        <button
          v-for="p in presets"
          :key="p.key"
          type="button"
          class="preset-item"
          :disabled="disabled"
          @click="emit('apply-preset', p.key)"
        >
          <v-icon :color="p.color" size="20" class="preset-icon">{{ p.icon }}</v-icon>
          <div class="preset-body">
            <div class="preset-title">{{ p.label }}</div>
            <div class="preset-desc">{{ p.desc }}</div>
          </div>
        </button>
      </div>
    </v-card>

    <!-- 延迟测速 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-timer-outline</v-icon>
        <span class="section-title">延迟测速</span>
        <span class="section-desc">决定测速速度与准确度</span>
      </div>

      <v-row dense>
        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model.number="config.threads"
            label="线程数"
            type="number"
            min="1"
            max="1000"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">并发测速线程。越大越快，但占用带宽和设备资源，弱设备建议 100~200。</div>
        </v-col>

        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model.number="config.pingTimes"
            label="测速次数"
            type="number"
            min="1"
            max="10"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">每个 IP 的延迟测试次数。次数越多结果越稳定，耗时也越长。</div>
        </v-col>

        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model.number="config.port"
            label="端口"
            type="number"
            min="1"
            max="65535"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">测速使用的 TCP 端口，默认 443（HTTPS）。</div>
        </v-col>
      </v-row>

      <v-divider class="my-3" />

      <div class="toggle-row">
        <div class="toggle-text">
          <div class="toggle-label">HTTPing 模式</div>
          <div class="hint">改用 HTTP 协议测速，适合验证某个域名解析到该 IP 后能否正常访问。</div>
        </div>
        <v-switch v-model="config.httping" :disabled="disabled" density="compact" inset />
      </div>

      <v-row v-if="config.httping" dense class="mt-1">
        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model.number="config.httpingCode"
            label="有效状态码"
            type="number"
            placeholder="留空 = 200/301/302"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">HTTPing 模式下认为可用的状态码，只支持填一个。</div>
        </v-col>

        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model="config.colo"
            label="地区筛选"
            placeholder="如 HKG,LAX,NRT"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">仅 HTTPing 可用。按 IATA 机场码或国家码过滤节点地区。</div>
        </v-col>
      </v-row>

      <v-divider class="my-3" />

      <div class="toggle-row">
        <div class="toggle-text">
          <div class="toggle-label">自定义测速域名</div>
          <div class="hint">
            开启后会先用 <code>域名/cdn-cgi/trace</code> 探测哪些 IP 真正服务你的 Worker / Pages 域名，
            自动剔除不适配的 IP（这类 IP 在代理客户端里会表现为连接超时）。
            该路径绕过 Worker，<strong>不消耗 Workers 请求配额</strong>。
          </div>
        </div>
        <v-switch v-model="config.probeEnabled" :disabled="disabled" density="compact" inset />
      </div>

      <v-text-field
        v-if="config.probeEnabled"
        v-model="config.probeDomain"
        label="域名"
        placeholder="例如：fast3.example.com"
        hint="只填域名本身，不要带 https:// 或末尾斜杠；粘贴完整网址会自动清理"
        persistent-hint
        :disabled="disabled"
        density="compact"
        prepend-inner-icon="mdi-web-check"
        class="mt-1"
        @blur="normalizeProbeDomain"
      />
    </v-card>

    <!-- 端到端测速 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="deep-purple">mdi-shield-check-outline</v-icon>
        <span class="section-title">端到端测速（可选）</span>
        <span class="section-desc">用和 Clash / V2RayN 相同的协议真实请求目标，测出客户端实际体验</span>
        <v-switch
          v-model="config.e2eEnabled"
          :disabled="disabled"
          density="compact"
          inset
          hide-details
          class="ml-auto"
        />
      </div>
      <div class="hint mb-2">
        不填凭据会自动跳过，不影响现有测速。建议先把「自定义测速域名」填成同一个域名。
      </div>
      <template v-if="config.e2eEnabled">
        <v-row dense>
          <v-col cols="12" sm="4" md="2">
            <v-select
              v-model="config.e2eProtocol"
              :items="protocols"
              item-title="title"
              item-value="value"
              label="协议"
              :disabled="disabled"
              density="compact"
            />
          </v-col>
          <v-col cols="12" sm="8" md="5">
            <v-text-field
              v-model="config.e2eHost"
              label="域名 / SNI"
              placeholder="fast2.example.com"
              :disabled="disabled"
              density="compact"
              @blur="normalizeE2EHost"
            />
          </v-col>
          <v-col cols="12" sm="4" md="2">
            <v-text-field
              v-model.number="config.e2ePort"
              label="端口"
              type="number"
              :disabled="disabled"
              density="compact"
            />
          </v-col>
          <v-col cols="12" sm="8" md="3">
            <v-combobox
              v-model="config.e2eTarget"
              :items="targets"
              label="测速目标"
              :disabled="disabled"
              density="compact"
            />
          </v-col>
        </v-row>
        <div class="e2e-fetch-row">
          <v-btn
            size="small"
            variant="tonal"
            color="primary"
            prepend-icon="mdi-cloud-download-outline"
            :disabled="disabled || fetchingE2E"
            :loading="fetchingE2E"
            @click="emit('fetch-e2e-from-panel')"
          >从面板自动获取</v-btn>
          <span class="e2e-fetch-hint">
            读取 edgetunnel 面板里的 UUID / WS 路径（需先在「自动同步」页填好面板地址和密码）
          </span>
        </div>
        <v-row dense>
          <v-col v-if="config.e2eProtocol === 'vless'" cols="12" sm="6" md="4">
            <v-text-field
              v-model="secrets.uuid"
              label="UUID"
              type="password"
              :placeholder="e2eCredStatus.hasUuid ? '已保存，留空不修改' : ''"
              :disabled="disabled"
              density="compact"
              prepend-inner-icon="mdi-key-outline"
            />
          </v-col>
          <v-col v-else cols="12" sm="6" md="4">
            <v-text-field
              v-model="secrets.password"
              label="Trojan 口令"
              type="password"
              :placeholder="e2eCredStatus.hasPassword ? '已保存，留空不修改' : ''"
              :disabled="disabled"
              density="compact"
              prepend-inner-icon="mdi-key-outline"
            />
          </v-col>
          <v-col cols="12" sm="6" md="8">
            <v-text-field
              v-model="secrets.wsPath"
              label="WS 路径"
              :placeholder="e2eCredStatus.hasPath ? '已保存，留空不修改' : '/proxyip=...'"
              :disabled="disabled"
              density="compact"
              prepend-inner-icon="mdi-link-variant"
            />
          </v-col>
        </v-row>
        <v-row dense>
          <v-col cols="6" sm="3" md="2">
            <v-text-field v-model.number="config.e2eConcurrency" label="并发" type="number" min="1" max="64" :disabled="disabled" density="compact" />
          </v-col>
          <v-col cols="6" sm="3" md="2">
            <v-text-field v-model.number="config.e2eRetries" label="重试" type="number" min="1" max="5" :disabled="disabled" density="compact" />
          </v-col>
          <v-col cols="6" sm="3" md="2">
          </v-col>
          <v-col cols="6" sm="3" md="2">
            <v-text-field v-model.number="config.e2eLimit" label="候选上限" type="number" min="1" max="2000" :disabled="disabled" density="compact" />
          </v-col>
          <v-col cols="12" sm="12" md="4" class="d-flex align-center">
            <v-switch v-model="config.e2eInsecure" label="跳过证书校验" :disabled="disabled" density="compact" inset hide-details />
            <v-chip v-if="e2eCredStatus.hasUuid || e2eCredStatus.hasPassword" size="small" color="success" variant="tonal" class="ml-2">凭据已保存</v-chip>
          </v-col>
        </v-row>
        <div class="hint">
          端到端延迟取「冷启动 + 复用连接」两次测量的较小值，与 Clash/V2RayN 面板口径一致；默认上限 3000ms。
        </div>
      </template>
    </v-card>

    <!-- 过滤条件 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-filter-variant</v-icon>
        <span class="section-title">过滤条件</span>
        <span class="section-desc">只保留符合要求的 IP，留默认值表示不过滤</span>
      </div>

      <v-row dense>
        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model.number="config.e2eMaxLatency"
            label="端到端上限 (ms)"
            type="number"
            min="0"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">最终筛选依据：真实隧道延迟低于该值才保留（推荐 2000~3000）。</div>
        </v-col>


        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model.number="config.maxLossRate"
            label="丢包率上限"
            type="number"
            step="0.05"
            min="0"
            max="1"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">0~1 之间，0 表示完全不允许丢包。</div>
        </v-col>

        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model.number="config.minSpeed"
            label="速度下限 (MB/s)"
            type="number"
            step="0.1"
            min="0"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">只保留下载速度高于该值的 IP，需配合「测速数量」使用。</div>
        </v-col>

        <v-col cols="12">
          <v-select
            v-model="config.countryFilter"
            :items="countryOptions"
            item-title="title"
            item-value="value"
            label="国家/地区（可多选）"
            multiple
            chips
            closable-chips
            density="compact"
            :disabled="disabled"
            hint="只保留所选国家/地区的边缘节点；不选表示不过滤。启用端到端后按真实机房识别，未识别的不误杀。"
            persistent-hint
          />
        </v-col>
      </v-row>
    </v-card>

    <!-- 下载测速 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-download-network-outline</v-icon>
        <span class="section-title">下载测速</span>
        <span class="section-desc">对延迟最低的 IP 实际下载测试速度</span>
      </div>

      <v-row dense>
        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model.number="config.testCount"
            label="测速数量"
            type="number"
            min="1"
            :disabled="disabled || config.disableDownload"
            density="compact"
          />
          <div class="hint">找到多少个满足条件的 IP 后停止测速。</div>
        </v-col>

        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model.number="config.downloadTime"
            label="单 IP 时长 (秒)"
            type="number"
            min="1"
            :disabled="disabled || config.disableDownload"
            density="compact"
          />
          <div class="hint">每个 IP 的最大下载测速时间，太短会导致速度偏低。</div>
        </v-col>

        <v-col cols="12" md="6">
          <v-text-field
            v-model="config.url"
            label="测速地址"
            :disabled="disabled || config.disableDownload"
            density="compact"
          />
          <div class="hint">需支持直连下载且文件较大；默认地址不保证长期可用，建议自建。</div>
        </v-col>
      </v-row>

      <v-row dense class="mt-1">
        <v-col cols="12" sm="6">
          <div class="toggle-row compact">
            <div class="toggle-text">
              <div class="toggle-label">测速全部 IP</div>
              <div class="hint">对 IP 段中每个 IP 测速（仅 IPv4），默认每段随机一个。</div>
            </div>
            <v-switch v-model="config.testAll" :disabled="disabled" density="compact" inset />
          </div>
        </v-col>

        <v-col cols="12" sm="6">
          <div class="toggle-row compact">
            <div class="toggle-text">
              <div class="toggle-label">禁用下载测速</div>
              <div class="hint">只测延迟不测速度，速度更快，结果按延迟排序。</div>
            </div>
            <v-switch v-model="config.disableDownload" :disabled="disabled" density="compact" inset />
          </div>
        </v-col>
      </v-row>

      <div class="toggle-row">
        <div class="toggle-text">
          <div class="toggle-label">两轮测速（先粗排再精测）</div>
          <div class="hint">对端到端通过的候选先做短测粗排，再对前 {{ config.testCount }} 个做完整精测；粗排值不写进结果。</div>
        </div>
        <v-switch
          v-model="config.coarseEnabled"
          :disabled="disabled || config.disableDownload"
          density="compact"
          inset
        />
      </div>
      <v-row v-if="config.coarseEnabled && !config.disableDownload" dense>
        <v-col cols="6" sm="3" md="2">
          <v-text-field v-model.number="config.coarseLimit" label="粗排候选" type="number" min="10" max="500" :disabled="disabled" density="compact" />
        </v-col>
        <v-col cols="6" sm="3" md="2">
          <v-text-field v-model.number="config.coarseTimeout" label="单次时长 (ms)" type="number" min="500" max="10000" :disabled="disabled" density="compact" />
        </v-col>
      </v-row>
    </v-card>

    <!-- IP 来源 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-server-network</v-icon>
        <span class="section-title">IP 来源</span>
        <span class="section-desc">可多选，开始测速时合并并按 IP 去重</span>
        <v-spacer />
        <!-- 抓取中显示进度，抓完显示总数 —— 这段等待可能持续一分多钟 -->
        <span
          v-if="fetchStatus.fetching"
          class="fetch-status fetch-status-active"
          :title="fetchStatus.msg || ''"
        >
          {{ fetchStatus.progress ? `解析中 ${fetchStatus.progress}` : '正在获取…' }}
        </span>
        <span
          v-else-if="fetchStatus.total"
          class="fetch-status"
          :title="`获取于 ${fetchStatus.time}，本次启动内测速会直接复用`"
        >
          已获取 {{ fetchStatus.total }} 个在线 IP · {{ fetchStatus.time }}
        </span>
        <!-- 不用 v-btn 的 loading：它会把文字换成转圈图标，看不到进度数字 -->
        <v-btn
          size="small"
          variant="tonal"
          color="primary"
          :prepend-icon="fetchStatus.fetching ? 'mdi-loading' : 'mdi-cloud-download-outline'"
          :disabled="!hasOnlineSource || disabled || fetchStatus.fetching"
          class="fetch-btn"
          @click="emit('fetch-sources')"
        >
          {{ fetchStatus.fetching
            ? (fetchStatus.progress ? fetchStatus.progress : '获取中')
            : '获取 IP' }}
        </v-btn>
      </div>

      <div class="source-list">
        <!-- 内置 ip.txt：强制勾选，不可取消 -->
        <div class="source-item locked">
          <v-checkbox
            :model-value="true"
            :disabled="true"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">
              内置 ip.txt
              <v-chip size="x-small" color="primary" variant="flat" class="ml-2">必选</v-chip>
            </div>
            <div class="hint">
              {{ config.ipFile || 'ip.txt' }} · IPv4 列表，支持 IP、IP 段（CIDR）和 IP:端口。
            </div>
          </div>
          <v-btn
            size="small"
            variant="tonal"
            prepend-icon="mdi-folder-search-outline"
            :disabled="disabled"
            @click="emit('pick-ip-file')"
          >
            更换文件
          </v-btn>
        </div>

        <!-- 内置 ipv6.txt -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="ipv6"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">内置 ipv6.txt</div>
            <div class="hint">IPv6 地址列表。需要测速地址支持 IPv6，否则结果会很少。</div>
          </div>
        </div>

        <!-- 在线优选 IP -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="bestcf"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">
              优选IP BestIP
              <a class="source-link" href="#" @click.prevent="emit('open-bestcf')">bestcf.pages.dev</a>
            </div>
            <div class="hint">
              每次开始测速前，在线抓取该站点「优选IP BestIP」区块下全部非 GitHub 链接，
              提取其中的 IP 与端口，按 IP 去重后与其他来源合并。同一 IP 出现多次时优先保留 443 等标准端口。
            </div>
          </div>
        </div>

        <!-- Cloudflare 官方 IP 段 -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="official"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">
              Cloudflare 官方 IP 段
              <span class="source-badge">api.cloudflare.com</span>
            </div>
            <div class="hint">
              官方实时发布的权威网段清单，最稳定。每段按 /24 各随机取一个 IP 参与测速。
            </div>
          </div>
        </div>

        <!-- cmliu CF-CIDR -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="cmliu"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">
              cmliu CF-CIDR
              <a class="source-link" href="#" @click.prevent="emit('open-cmliu')">GitHub</a>
            </div>
            <div class="hint">
              社区长期维护的 Cloudflare 网段列表，覆盖面通常比官方 API 更广。
            </div>
          </div>
        </div>

        <!-- BestCF CF 官方域名池 -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="cfdomains"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">
              CF 官方域名池
              <a class="source-link" href="#" @click.prevent="emit('open-cfdomains')">CF-Pages-BestCF</a>
            </div>
            <div class="hint">
              BestCF 维护的一份托管在 Cloudflare 上的真实域名清单，先在线抓取（jsDelivr 等国内镜像优先），
              再通过 DoH 解析成 IP 参与测速。数量大、可用率高，适合做兜底。
            </div>
          </div>
        </div>

        <!-- 自定义在线源 -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="custom"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">自定义 IP 源</div>
            <v-textarea
              v-model="config.customSources"
              placeholder="https://example.com/ip.txt&#10;https://another.site/list.txt"
              :disabled="disabled || !customEnabled"
              density="compact"
              hide-details
              rows="2"
              auto-grow
              class="source-textarea mt-2 mb-1"
            />
            <div class="hint">
              每行一个 URL，与最佳 IP 一起抓取合并。支持 <code>IP:端口</code>、纯 IP、<code>域名:端口</code>；
              列表里的域名会通过 DoH 自动解析成 IP。
            </div>
          </div>
        </div>

        <!-- 手动指定 -->
        <div class="source-item">
          <v-checkbox
            v-model="sources"
            value="manual"
            :disabled="disabled"
            density="compact"
            hide-details
            color="primary"
            class="source-check"
          />
          <div class="source-body">
            <div class="source-title">手动指定 IP 段</div>
            <v-text-field
              v-model="config.ipText"
              placeholder="1.1.1.1,2.2.2.2/24,1.1.1.1:8443"
              :disabled="disabled || !manualEnabled"
              density="compact"
              hide-details
              class="mt-2 mb-1"
            />
            <div class="hint">英文逗号分隔，支持 IP、IP 段和 IP:端口。</div>
          </div>
        </div>
      </div>

      <div class="hint source-foot">
        合并后按 IP 去重；同一 IP 出现多个端口时，优先保留先加载来源的端口（内置列表默认使用上方「端口」设置）。
      </div>
    </v-card>

    <!-- 高级 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-cog-outline</v-icon>
        <span class="section-title">高级选项</span>
        <span class="section-desc">一般保持默认即可</span>
      </div>

      <div class="d-flex align-center mb-2">
        <v-icon size="16" class="mr-1">mdi-database-clock-outline</v-icon>
        <span class="text-caption">历史优质 IP：{{ poolCount }} 个（下次测速自动优先参与）</span>
        <v-btn
          v-if="poolCount > 0"
          size="x-small"
          variant="text"
          color="error"
          class="ml-2"
          :disabled="disabled"
          @click="emit('clear-pool')"
        >清空</v-btn>
      </div>

      <v-row dense>
        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model="config.outputFile"
            label="结果文件"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">测速结果保存路径，留空则不写入文件。</div>
        </v-col>

        <v-col cols="12" sm="6" md="4">
          <v-text-field
            v-model.number="config.printNum"
            label="显示结果数量"
            type="number"
            min="0"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">表格最多显示多少条结果，0 表示不显示（导出仍为全部结果）。</div>
        </v-col>

        <v-col cols="12" sm="6" md="4">
          <div class="toggle-row compact">
            <div class="toggle-text">
              <div class="toggle-label">调试模式</div>
              <div class="hint">输出详细错误日志，用于排查下载测速为 0 的原因。</div>
            </div>
            <v-switch v-model="config.debug" :disabled="disabled" density="compact" inset />
          </div>
        </v-col>
      </v-row>
    </v-card>
  </div>
</template>

<style scoped>
.settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  padding: 16px 20px;
}
.e2e-fetch-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.e2e-fetch-hint {
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.6);
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

/* 在线 IP 缓存状态：数字用等宽，避免总数变化时宽度跳动 */
.fetch-status {
  font-size: 12px;
  color: #8b8b96;
  font-variant-numeric: tabular-nums;
  margin-right: 4px;
  cursor: default;
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fetch-status-active {
  color: #60a5fa;
}

/* 抓取中让 loading 图标转起来（v-btn 的 loading 属性会吞掉文字，不能用） */
.fetch-btn :deep(.v-icon.mdi-loading) {
  animation: fetch-spin 1s linear infinite;
}

@keyframes fetch-spin {
  to {
    transform: rotate(360deg);
  }
}

.hint {
  font-size: 12px;
  line-height: 1.5;
  color: #71717a;
  margin: 0 0 10px;
  min-height: 34px;
}

/* 场景预设 */
.preset-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 10px;
}

.preset-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border-radius: 8px;
  border: 1px solid #26262e;
  background: #17171c;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
  font-family: inherit;
}

.preset-item:hover:not(:disabled) {
  border-color: #4f6acd;
  background: rgba(79, 106, 205, 0.08);
}

.preset-item:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.preset-icon {
  margin-top: 1px;
  flex: 0 0 auto;
}

.preset-body {
  min-width: 0;
}

.preset-title {
  font-size: 13.5px;
  font-weight: 600;
  color: #e4e4e7;
  margin-bottom: 2px;
}

.preset-desc {
  font-size: 11.5px;
  line-height: 1.45;
  color: #71717a;
}

.section :deep(.v-field) {
  margin-bottom: 2px;
}

.toggle-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.toggle-row.compact {
  margin-bottom: 4px;
}

.toggle-text {
  flex: 1;
  min-width: 0;
}

.toggle-label {
  font-size: 14px;
  color: #d4d4d8;
  margin-bottom: 2px;
}

/* IP 来源勾选列表 */
.source-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.source-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid #26262e;
  background: #17171c;
}

.source-item.locked {
  border-color: #2f3d68;
  background: linear-gradient(90deg, rgba(79, 106, 205, 0.10), transparent 70%);
}

.source-check {
  flex: 0 0 auto;
  margin-top: -2px;
}

.source-body {
  flex: 1;
  min-width: 0;
}

.source-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 14px;
  color: #d4d4d8;
  margin-bottom: 2px;
}

.source-item .hint {
  min-height: 0;
  margin: 0;
}

.source-link {
  font-size: 12px;
  color: #7c9cff;
  text-decoration: none;
}

.source-link:hover {
  text-decoration: underline;
}

.source-foot {
  min-height: 0;
  margin: 12px 0 0;
}

.source-textarea :deep(textarea) {
  font-family: 'Cascadia Mono', 'Consolas', monospace;
  font-size: 12px;
}

.source-badge {
  font-size: 11px;
  color: #8b8b96;
  font-family: 'Cascadia Mono', 'Consolas', monospace;
}

.source-body code {
  background: #26262e;
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 11px;
  color: #a5b4fc;
}
</style>
