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
  <div class="config-panel">
    <v-expansion-panels multiple variant="accordion" class="config-panels">
      <!-- 延迟测速 -->
      <v-expansion-panel value="latency">
        <v-expansion-panel-title>
          <v-icon start size="20">mdi-timer-outline</v-icon>
          延迟测速
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <div class="field-row">
            <span class="field-label">线程数</span>
            <v-text-field
              v-model.number="config.threads"
              type="number"
              min="1"
              max="1000"
              :disabled="disabled"
              density="compact"
              style="max-width: 110px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">测速次数</span>
            <v-text-field
              v-model.number="config.pingTimes"
              type="number"
              min="1"
              max="10"
              :disabled="disabled"
              density="compact"
              style="max-width: 110px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">端口</span>
            <v-text-field
              v-model.number="config.port"
              type="number"
              min="1"
              max="65535"
              :disabled="disabled"
              density="compact"
              style="max-width: 110px"
            />
          </div>

          <v-divider class="my-3" />

          <div class="field-row">
            <span class="field-label">HTTPing 模式</span>
            <v-switch v-model="config.httping" :disabled="disabled" density="compact" />
          </div>
          <template v-if="config.httping">
            <div class="field-row">
              <span class="field-label">有效状态码</span>
              <v-text-field
                v-model.number="config.httpingCode"
                type="number"
                placeholder="留空表示 200/301/302"
                :disabled="disabled"
                density="compact"
                style="max-width: 180px"
              />
            </div>
            <div class="field-row">
              <span class="field-label">地区筛选</span>
              <v-text-field
                v-model="config.colo"
                placeholder="如 HKG,LAX,NRT"
                :disabled="disabled"
                density="compact"
              />
            </div>
          </template>
        </v-expansion-panel-text>
      </v-expansion-panel>

      <!-- 过滤条件 -->
      <v-expansion-panel value="filter">
        <v-expansion-panel-title>
          <v-icon start size="20">mdi-filter-variant</v-icon>
          过滤条件
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <div class="field-row">
            <span class="field-label">延迟上限 (ms)</span>
            <v-text-field
              v-model.number="config.maxDelay"
              type="number"
              min="0"
              :disabled="disabled"
              density="compact"
              style="max-width: 130px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">延迟下限 (ms)</span>
            <v-text-field
              v-model.number="config.minDelay"
              type="number"
              min="0"
              :disabled="disabled"
              density="compact"
              style="max-width: 130px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">丢包率上限</span>
            <v-text-field
              v-model.number="config.maxLossRate"
              type="number"
              step="0.05"
              min="0"
              max="1"
              :disabled="disabled"
              density="compact"
              style="max-width: 130px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">速度下限 (MB/s)</span>
            <v-text-field
              v-model.number="config.minSpeed"
              type="number"
              step="0.1"
              min="0"
              :disabled="disabled"
              density="compact"
              style="max-width: 130px"
            />
          </div>
        </v-expansion-panel-text>
      </v-expansion-panel>

      <!-- 下载测速 -->
      <v-expansion-panel value="download">
        <v-expansion-panel-title>
          <v-icon start size="20">mdi-download-network-outline</v-icon>
          下载测速
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <div class="field-row">
            <span class="field-label">测速数量</span>
            <v-text-field
              v-model.number="config.testCount"
              type="number"
              min="1"
              :disabled="disabled || config.disableDownload"
              density="compact"
              style="max-width: 110px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">单 IP 时长 (秒)</span>
            <v-text-field
              v-model.number="config.downloadTime"
              type="number"
              min="1"
              :disabled="disabled || config.disableDownload"
              density="compact"
              style="max-width: 110px"
            />
          </div>
          <div class="field-row">
            <span class="field-label">禁用下载测速</span>
            <v-switch v-model="config.disableDownload" :disabled="disabled" density="compact" />
          </div>
          <div class="field-row">
            <span class="field-label">测速全部 IP</span>
            <v-switch v-model="config.testAll" :disabled="disabled" density="compact" />
          </div>
        </v-expansion-panel-text>
      </v-expansion-panel>

      <!-- IP 来源 -->
      <v-expansion-panel value="source">
        <v-expansion-panel-title>
          <v-icon start size="20">mdi-server-network</v-icon>
          IP 来源
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <v-select
            v-model="config.ipFile"
            :items="[
              { title: 'ip.txt（IPv4 默认）', value: 'ip.txt' },
              { title: 'ipv6.txt（IPv6）', value: 'ipv6.txt' },
            ]"
            label="IP 数据文件"
            :disabled="disabled"
          />
          <v-btn
            variant="tonal"
            size="small"
            prepend-icon="mdi-folder-search-outline"
            :disabled="disabled"
            block
            @click="emit('pick-ip-file')"
          >
            选择其他文件
          </v-btn>
          <v-text-field
            v-model="config.ipText"
            label="手动指定 IP 段"
            placeholder="1.1.1.1,2.2.2.2/24"
            :disabled="disabled"
            class="mt-3"
          />
        </v-expansion-panel-text>
      </v-expansion-panel>

      <!-- 高级 -->
      <v-expansion-panel value="advanced">
        <v-expansion-panel-title>
          <v-icon start size="20">mdi-cog-outline</v-icon>
          高级选项
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <v-text-field
            v-model="config.url"
            label="测速地址"
            :disabled="disabled"
          />
          <v-text-field
            v-model="config.outputFile"
            label="结果文件"
            :disabled="disabled"
          />
          <div class="field-row">
            <span class="field-label">调试模式</span>
            <v-switch v-model="config.debug" :disabled="disabled" density="compact" />
          </div>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>
  </div>
</template>

<style scoped>
.config-panel {
  height: 100%;
  overflow-y: auto;
  padding-right: 4px;
}

.config-panels {
  background: transparent;
}

.field-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.field-label {
  font-size: 13px;
  color: #a1a1aa;
  white-space: nowrap;
}
</style>
