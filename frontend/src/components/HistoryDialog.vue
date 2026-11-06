<script setup>
const dialog = defineModel({ type: Boolean, default: false })
defineProps({
  history: { type: Array, default: () => [] },
})
const emit = defineEmits(['load', 'delete', 'clear'])
</script>

<template>
  <v-dialog v-model="dialog" max-width="880" scrollable>
    <v-card class="history-card">
      <v-card-title class="d-flex align-center py-4">
        <v-icon class="mr-2">mdi-history</v-icon>
        <span>历史记录</span>
        <v-chip size="small" variant="tonal" class="ml-3">{{ history.length }}</v-chip>
        <v-spacer />
        <v-btn v-if="history.length" variant="text" color="error" size="small" @click="emit('clear')">
          清空
        </v-btn>
        <v-btn icon variant="text" @click="dialog = false">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-divider />

      <v-card-text>
        <div v-if="!history.length" class="text-center py-12 text-medium-emphasis">
          <v-icon size="48" class="mb-3">mdi-inbox-outline</v-icon>
          <div>暂无历史记录</div>
        </div>

        <v-expansion-panels v-else variant="accordion">
          <v-expansion-panel v-for="h in history" :key="h.id" :value="h.id">
            <v-expansion-panel-title>
              <div class="d-flex align-center ga-3 w-100 pr-2">
                <v-icon size="18" color="primary">mdi-clock-outline</v-icon>
                <span class="text-body-2">{{ h.time }}</span>
                <v-chip size="x-small" variant="tonal">{{ h.count }} 个 IP</v-chip>
                <span v-if="h.bestIp" class="text-caption text-medium-emphasis">
                  最快 <span class="mono">{{ h.bestIp }}</span>
                  <span class="mono ml-1 text-success">{{ h.bestSpeed.toFixed(2) }} MB/s</span>
                </span>
                <v-spacer />
                <v-btn size="small" variant="tonal" @click.stop="emit('load', h)">载入</v-btn>
                <v-btn size="small" variant="text" color="error" @click.stop="emit('delete', h.id)">
                  <v-icon size="16">mdi-delete-outline</v-icon>
                </v-btn>
              </div>
            </v-expansion-panel-title>

            <v-expansion-panel-text>
              <v-table density="compact" class="history-table">
                <thead>
                  <tr>
                    <th>IP</th>
                    <th>延迟</th>
                    <th>丢包</th>
                    <th>速度 (MB/s)</th>
                    <th>地区</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="r in h.results.slice(0, 30)" :key="r.ip">
                    <td class="mono">{{ r.ip }}</td>
                    <td class="mono">{{ r.delay.toFixed(1) }} ms</td>
                    <td class="mono">{{ (r.lossRate * 100).toFixed(1) }}%</td>
                    <td class="mono text-success">{{ r.speed.toFixed(2) }}</td>
                    <td>{{ r.colo }}</td>
                  </tr>
                </tbody>
              </v-table>
              <div v-if="h.results.length > 30" class="text-caption text-medium-emphasis mt-2">
                仅显示前 30 条，共 {{ h.results.length }} 条
              </div>
              <div class="text-caption text-medium-emphasis mt-2">
                耗时 {{ h.duration.toFixed(1) }} 秒
              </div>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<style scoped>
.history-card {
  background: #1b1b21 !important;
  border: 1px solid #2a2a33;
}

.history-table :deep(th) {
  font-size: 12px;
  color: #8b8b96 !important;
}

.history-table {
  background: transparent !important;
}
</style>
