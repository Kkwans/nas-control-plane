import '@fontsource-variable/jetbrains-mono/wght.css'
import '@fontsource-variable/manrope/wght.css'
import './styles/tokens.css'
import './styles/base.css'
import './styles/motion.css'

import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import router from './router'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.mount('#app')
