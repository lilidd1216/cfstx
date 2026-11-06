<script setup>
import { computed } from 'vue'

const config = defineModel('config', { type: Object, required: true })
const props = defineProps({
  running: { type: Boolean, default: false },
})
const emit = defineEmits(['pick-ip-file'])

const disabled = computed(() => props.running)
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
        <span class="section-desc">指定要测速的 IP 段</span>
      </div>

      <v-row dense>
        <v-col cols="12" sm="6" md="5">
          <v-select
            v-model="config.ipFile"
            :items="[
              { title: 'ip.txt（IPv4 默认）', value: 'ip.txt' },
              { title: 'ipv6.txt（IPv6）', value: 'ipv6.txt' },
            ]"
            label="IP 数据文件"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">程序目录下的内置 IP 列表，也可点击右侧选择其他文件。</div>
        </v-col>

        <v-col cols="12" sm="6" md="2" class="pt-1">
          <v-btn
            variant="tonal"
            size="default"
            prepend-icon="mdi-folder-search-outline"
            :disabled="disabled"
            block
            height="40"
            @click="emit('pick-ip-file')"
          >
            选择文件
          </v-btn>
        </v-col>

        <v-col cols="12" md="5">
          <v-text-field
            v-model="config.ipText"
            label="手动指定 IP 段"
            placeholder="1.1.1.1,2.2.2.2/24"
            :disabled="disabled"
            density="compact"
          />
          <div class="hint">直接输入 IP 或网段，英文逗号分隔；填写后优先于上面的文件。</div>
        </v-col>
      </v-row>
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
</style>
