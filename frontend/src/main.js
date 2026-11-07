import { createApp } from 'vue'
import App from './App.vue'
import 'vuetify/styles'
import '@mdi/font/css/materialdesignicons.css'
import { createVuetify } from 'vuetify'

const vuetify = createVuetify({
  theme: {
    defaultTheme: 'cfstxDark',
    themes: {
      cfstxDark: {
        dark: true,
        colors: {
          background: '#121216',
          surface: '#1b1b21',
          'surface-variant': '#26262e',
          'surface-bright': '#2e2e38',
          primary: '#8b5cf6',
          'primary-darken-1': '#7c3aed',
          secondary: '#14b8a6',
          accent: '#f59e0b',
          error: '#ef4444',
          info: '#38bdf8',
          success: '#22c55e',
          warning: '#f59e0b',
          'on-surface': '#e4e4e7',
          'on-background': '#e4e4e7',
        },
      },
    },
  },
  defaults: {
    VBtn: { rounded: 'lg' },
    VTextField: { variant: 'outlined', density: 'comfortable', hideDetails: 'auto' },
    VSelect: { variant: 'outlined', density: 'comfortable', hideDetails: 'auto' },
    VSwitch: { color: 'primary', hideDetails: 'auto' },
    VCard: { rounded: 'xl' },
    VSlider: { color: 'primary', hideDetails: 'auto' },
  },
})

const app = createApp(App)

// 组件渲染抛错时 Vue 会卸载整个组件，界面上表现为「某块功能凭空消失」，
// 不报任何东西，排查起来非常困难（v1.1.4 的 Hosts 卡片就这样丢了两个版本）。
// 这里至少把错误打到控制台，并且不静默吞掉。
app.config.errorHandler = (err, _instance, info) => {
  console.error('[CFSTX] 渲染出错：', err, '\n组件位置：', info)
}

app.use(vuetify).mount('#app')
