<script setup>
import { computed } from 'vue'
import { countryOptions } from '../constants/countries.js'

const config = defineModel('config', { type: Object, required: true })
// 面板密码单独走一个 model，与测速凭据一样不混进 config。
const password = defineModel('password', { type: String, default: '' })

const props = defineProps({
  status: { type: Object, default: () => ({}) },
  saving: { type: Boolean, default: false },
  testing: { type: Boolean, default: false },
})
const emit = defineEmits(['save', 'run-now', 'test-panel'])

const disabled = computed(() => !!props.status.running)

const statusLabel = computed(() => {
  switch (props.status.lastStatus) {
    case 'running': return '运行中'
    case 'success': return '上次成功'
    case 'warn': return '上次成功（有告警）'
    case 'skipped': return '上次跳过（无达标 IP）'
    case 'busy': return '上次因任务占用跳过'
    case 'error': return '上次失败'
    case 'canceled': return '上次已取消'
    default: return '尚未运行'
  }
})

const statusColor = computed(() => {
  switch (props.status.lastStatus) {
    case 'success': return 'success'
    case 'warn': return 'warning'
    case 'skipped':
    case 'busy': return 'info'
    case 'error': return 'error'
    case 'running': return 'primary'
    default: return 'grey'
  }
})

const passwordHint = computed(() =>
  props.status.hasPassword ? '已保存，留空表示不修改' : '请输入面板管理员密码'
)

function fmtTime(s) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleString()
}

function onSave() {
  emit('save')
}
</script>

<template>
  <div class="autosync">
    <!-- 状态 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-sync</v-icon>
        <span class="section-title">自动同步状态</span>
        <v-spacer />
        <v-chip size="small" :color="statusColor" variant="flat">{{ statusLabel }}</v-chip>
      </div>

      <div class="status-grid">
        <div class="status-item">
          <div class="status-label">下次运行</div>
          <div class="status-value">{{ fmtTime(status.nextRun) }}</div>
        </div>
        <div class="status-item">
          <div class="status-label">上次运行</div>
          <div class="status-value">{{ fmtTime(status.lastRun) }}</div>
        </div>
        <div class="status-item">
          <div class="status-label">上次推送</div>
          <div class="status-value">{{ status.lastPushedCount || 0 }} 个 IP</div>
        </div>
      </div>

      <v-alert
        v-if="status.lastError"
        type="error"
        variant="tonal"
        density="compact"
        class="mt-4"
      >{{ status.lastError }}</v-alert>
      <v-alert
        v-else-if="status.lastWarn"
        type="warning"
        variant="tonal"
        density="compact"
        class="mt-4"
      >{{ status.lastWarn }}</v-alert>
    </v-card>

    <!-- 面板连接 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-cloud-cog</v-icon>
        <span class="section-title">edgetunnel 面板</span>
        <span class="section-desc">程序只写入 ADD.txt，不会修改面板其它配置</span>
      </div>

      <v-row dense>
        <v-col cols="12" md="7">
          <v-text-field
            v-model="config.panelUrl"
            label="面板地址"
            placeholder="https://fast2.example.com"
            density="compact"
            variant="outlined"
            hide-details
            prepend-inner-icon="mdi-web"
          />
        </v-col>
        <v-col cols="12" md="5">
          <v-text-field
            v-model="password"
            type="password"
            label="管理员密码"
            :placeholder="passwordHint"
            density="compact"
            variant="outlined"
            hide-details
            prepend-inner-icon="mdi-lock"
            autocomplete="new-password"
          />
        </v-col>
        <v-col cols="12" md="7">
          <v-text-field
            v-model="config.userAgent"
            label="User-Agent"
            placeholder="留空使用内置 UA"
            density="compact"
            variant="outlined"
            hide-details
            prepend-inner-icon="mdi-account-details"
          />
        </v-col>
        <v-col cols="12" md="5" class="d-flex align-center">
          <v-btn
            variant="tonal"
            color="primary"
            :loading="testing"
            :disabled="!config.panelUrl"
            @click="emit('test-panel')"
          >
            测试面板连接
          </v-btn>
        </v-col>
      </v-row>
      <div class="hint">
        只填域名（如 https://fast2.lds1216.ccwu.cc），不要带 /admin 路径。登录 cookie 与 User-Agent 绑定，UA 一般留空即可。
      </div>
    </v-card>

    <!-- 调度 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-timer-sync-outline</v-icon>
        <span class="section-title">定时设置</span>
      </div>

      <v-switch
        v-model="config.enabled"
        color="primary"
        density="compact"
        hide-details
        label="启用定时自动优选"
        class="mb-2"
      />

      <v-row dense class="align-center">
        <v-col cols="12" md="5">
          <v-text-field
            v-model="config.dailyTime"
            type="time"
            label="每天运行时间"
            density="compact"
            variant="outlined"
            hide-details
            prepend-inner-icon="mdi-clock-outline"
          />
        </v-col>
        <v-col cols="12" md="7">
          <span class="run-hint">每天 {{ config.dailyTime || '03:00' }} 自动测速并推送一次</span>
        </v-col>
      </v-row>

      <div class="hint">
        定时任务复用当前已保存的测速配置（来源、端口、并发等）；测速进行中时本次会自动跳过，不会打断手动测速。
      </div>

      <div class="actions">
        <v-btn
          variant="flat"
          color="primary"
          :loading="status.running"
          :disabled="disabled || !config.panelUrl"
          @click="emit('run-now')"
        >
          <v-icon start>mdi-play</v-icon>
          立即同步一次
        </v-btn>
        <v-btn variant="tonal" :loading="saving" @click="onSave">
          <v-icon start>mdi-content-save</v-icon>
          保存设置
        </v-btn>
      </div>
    </v-card>

    <!-- 筛选 -->
    <v-card class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-filter-variant</v-icon>
        <span class="section-title">推送筛选</span>
        <span class="section-desc">多个条件可同时生效</span>
      </div>

      <v-row dense>
        <v-col cols="12" md="3">
          <v-text-field
            v-model.number="config.filters.speedTopN"
            type="number"
            label="速度 Top N"
            hint="0 表示不按速度截断"
            persistent-hint
            density="compact"
            variant="outlined"
            min="0"
          />
        </v-col>
        <v-col cols="12" md="3">
          <v-text-field
            v-model.number="config.filters.maxDelay"
            type="number"
            label="延迟上限"
            suffix="ms"
            hint="0 表示不限"
            persistent-hint
            density="compact"
            variant="outlined"
            min="0"
          />
        </v-col>
        <v-col cols="12" md="3">
          <v-text-field
            v-model.number="config.filters.minSpeed"
            type="number"
            label="速度下限"
            suffix="MB/s"
            hint="0 表示不限"
            persistent-hint
            density="compact"
            variant="outlined"
            min="0"
            step="0.5"
          />
        </v-col>
        <v-col cols="12" md="3">
          <v-select
            v-model="config.filters.countries"
            :items="countryOptions"
            label="国家/地区"
            multiple
            chips
            closable-chips
            density="compact"
            variant="outlined"
            hide-details
          />
        </v-col>
      </v-row>

      <div class="hint">
        顺序：地区 → 速度下限 → 延迟上限 → 按速度降序取前 N。
        例：速度下限 15、延迟上限 200、速度 Top N 30 = 速度 ≥15MB/s 且延迟 ≤200ms 的最快 30 个。
        地区筛选下，无法识别地区的 IP 不会推送。
      </div>

      <v-alert
        v-if="!config.filters.speedTopN"
        type="info"
        variant="tonal"
        density="compact"
        class="mt-3"
      >
        未设置速度 Top N，将推送全部达标的 IP（最多 200 个）。
      </v-alert>
    </v-card>

    <!-- 上次推送 -->
    <v-card v-if="status.lastPushed?.length" class="section" elevation="0">
      <div class="section-head">
        <v-icon size="18" color="primary">mdi-format-list-numbered</v-icon>
        <span class="section-title">上次推送的 IP</span>
        <span class="section-desc">共 {{ status.lastPushed.length }} 个</span>
      </div>

      <v-table density="compact" class="pushed-table">
        <thead>
          <tr>
            <th>IP:端口</th>
            <th>地区</th>
            <th>速度</th>
            <th>延迟</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in status.lastPushed" :key="`${item.ip}:${item.port}`">
            <td class="mono">{{ item.ip }}:{{ item.port }}</td>
            <td>{{ item.colo || '—' }}</td>
            <td>{{ item.speed ? item.speed.toFixed(2) + ' MB/s' : '—' }}</td>
            <td>{{ item.delay ? item.delay.toFixed(0) + ' ms' : '—' }}</td>
          </tr>
        </tbody>
      </v-table>
    </v-card>
  </div>
</template>

<style scoped>
.autosync {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
/* 与其它面板保持一致的卡片样式 */
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
}
.section-desc {
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.6);
  margin-left: 4px;
}
.status-grid {
  display: flex;
  gap: 40px;
  flex-wrap: wrap;
}
.status-item {
  min-width: 140px;
}
.status-label {
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.6);
}
.status-value {
  font-size: 14px;
  font-weight: 600;
  margin-top: 4px;
}
.hint {
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.6);
  margin-top: 8px;
  line-height: 1.6;
}
.run-hint {
  font-size: 13px;
  color: rgba(var(--v-theme-on-surface), 0.7);
}
.actions {
  display: flex;
  gap: 12px;
  margin-top: 16px;
}
.pushed-table {
  background: transparent;
}
</style>
