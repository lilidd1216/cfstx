<script setup>
import { computed } from 'vue'

const props = defineProps({
  results: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
})
const emit = defineEmits(['export'])

const headers = [
  { title: 'IP 地址', key: 'ip', sortable: false, width: '28%' },
  { title: '平均延迟', key: 'delay', align: 'end' },
  { title: '丢包率', key: 'lossRate', align: 'end' },
  { title: '下载速度', key: 'speed', align: 'end' },
  { title: '地区', key: 'colo', align: 'center', width: '90px' },
]

const maxSpeed = computed(() => {
  if (!props.results.length) return 1
  return Math.max(...props.results.map(r => r.speed), 1)
})

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
</script>

<template>
  <v-card class="result-card" elevation="0">
    <v-card-title class="d-flex align-center py-3">
      <v-icon class="mr-2" size="20">mdi-table-large</v-icon>
      <span class="text-subtitle-1 font-weight-medium">测速结果</span>
      <v-chip size="small" variant="tonal" class="ml-3">{{ results.length }} 个 IP</v-chip>
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
    </v-card-title>

    <v-divider />

    <v-data-table
      :headers="headers"
      :items="results"
      :loading="loading"
      item-value="ip"
      density="comfortable"
      class="result-table"
      no-data-text="暂无测速结果，点击左侧「开始测速」"
      items-per-page="-1"
      hide-default-footer
    >
      <template #item.ip="{ item }">
        <span class="mono text-body-2">{{ item.ip }}</span>
      </template>

      <template #item.delay="{ item }">
        <span class="metric-value" :class="`text-${delayColor(item.delay)}`">
          {{ item.delay.toFixed(1) }} ms
        </span>
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
        <v-chip size="x-small" variant="tonal" :color="item.colo === 'N/A' ? 'grey' : 'secondary'">
          {{ item.colo }}
        </v-chip>
      </template>
    </v-data-table>
  </v-card>
</template>

<style scoped>
.result-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.result-table {
  background: transparent !important;
  flex: 1;
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
</style>
