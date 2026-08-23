import '@fontsource-variable/jetbrains-mono/wght.css'
import 'element-plus/dist/index.css'
import './styles/tokens.css'
import './styles/base.css'
import './styles/motion.css'

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { vLoading } from 'element-plus'

import App from './App.vue'
import router from './router'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.directive('loading', vLoading)
app.mount('#app')
