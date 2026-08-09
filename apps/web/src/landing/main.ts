import ui from '@nuxt/ui/vue-plugin'
import { createApp } from 'vue'
import { applyColorMode, initColorMode } from '../color-mode'
import '../styles.css'
import Landing from './Landing.vue'

// The logged-out page is its own Vite entry rather than a route in the app:
// it is served on a different host, to people who are not signed in, and it
// has no business pulling in the router, the API client or the library state.
// Sharing styles.css is the point — same palette, same surfaces, same dark
// mode — while sharing nothing that can fail.
initColorMode()

createApp(Landing).use(ui).mount('#landing')

// Again after mount: unhead rewrites the html class. See applyColorMode.
applyColorMode()
