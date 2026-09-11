import { createApp } from 'vue'
import { createPinia } from 'pinia'

// 字体：与原项目一致，用 @fontsource 加载 Inter Variable（多语言 unicode-range 子集）
import '@fontsource-variable/inter'

// 全局样式：设计 token 全部在此（Tailwind v4 CSS-first）
import './styles/globals.css'

import App from './App.vue'
import router from './router'
import i18n from './i18n'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(i18n)

app.mount('#app')
