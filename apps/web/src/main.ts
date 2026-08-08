import ui from '@nuxt/ui/vue-plugin'
import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import { followSystemColorScheme } from './color-mode'
import './styles.css'

// domestique is a single screen, but Nuxt UI's link components import
// vue-router unconditionally, so it needs a router to exist. One catch-all
// route keeps a refresh on any path rendering the app rather than blanking.
const router = createRouter({
  history: createWebHistory(),
  routes: [{ path: '/:pathMatch(.*)*', component: App }],
})

followSystemColorScheme()

createApp(App).use(router).use(ui).mount('#app')
