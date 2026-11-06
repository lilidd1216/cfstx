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

createApp(App).use(vuetify).mount('#app')
