<script setup>
import { computed } from 'vue'

const config = defineModel('config', { type: Object, required: true })
const props = defineProps({
  running: { type: Boolean, default: false },
})
const emit = defineEmits(['pick-ip-file', 'open-bestcf', 'open-cmliu'])

const disabled = computed(() => props.running)

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
        :disabled="disabled"
        density="compact"
        prepend-inner-icon="mdi-web-check"
        class="mt-1"
      />
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
            v-model.number="config.maxDelay"
            label="延迟上限 (ms)"
            type="number"
            min="0"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">只保留平均延迟低于该值的 IP。</div>
        </v-col>

        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model.number="config.minDelay"
            label="延迟下限 (ms)"
            type="number"
            min="0"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">只保留高于该值的 IP，用于排除延迟低但速度差的节点。</div>
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
    </v-card>

    <!-- IP 来源 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-server-network</v-icon>
        <span class="section-title">IP 来源</span>
        <span class="section-desc">可多选，开始测速时合并并按 IP 去重</span>
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
  line-height: 1.5;
  color: #71717a;
  margin: 0 0 10px;
  min-height: 34px;
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
