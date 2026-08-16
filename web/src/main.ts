import { createApp } from 'vue'
import App from './App.vue'
import './styles.css'
import './mobile.css'
import { applyTheme, getStoredTheme } from './theme'

applyTheme(getStoredTheme(), { persist: false })
createApp(App).mount('#app')
