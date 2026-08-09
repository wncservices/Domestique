import ui from '@nuxt/ui/vue-plugin'
import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import { applyColorMode, initColorMode } from './color-mode'
import './styles.css'

// Three pages, because they answer three different questions: what is in the
// library, how do I put something in it, and who am I connected to. The
// catch-all keeps a refresh on any path rendering the app rather than the
// API's 404 — the Go side already falls back to index.html.
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: () => import('./pages/LibraryPage.vue') },
    { path: '/add', component: () => import('./pages/AddPage.vue') },
    { path: '/settings', component: () => import('./pages/SettingsPage.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

initColorMode()

createApp(App).use(router).use(ui).mount('#app')

// Again, after mount: see applyColorMode.
applyColorMode()
