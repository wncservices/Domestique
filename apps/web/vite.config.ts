import ui from '@nuxt/ui/vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'

// The API listens on :8080 (`domestique serve`); in dev we proxy to it so the
// frontend runs from Vite with hot reload against the real backend.
export default defineConfig({
  plugins: [vue(), ui()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.DOMESTIQUE_API ?? 'http://localhost:8080',
        changeOrigin: true,
      },
      // /sso/* (mode: oidc's login/callback/logout) is a real backend route,
      // not something Vite's SPA fallback should ever serve. Without this a
      // dev session run as `just api` + `just web` would send the Sign in
      // link and the logout fetch nowhere — both would silently hit Vite's
      // own index.html instead of the API.
      '/sso': {
        target: process.env.DOMESTIQUE_API ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      // Two entries: the app, and the logged-out page served on the apex host.
      // Separate bundles on purpose — the landing page should not pull in the
      // router or the API client to render three paragraphs.
      input: {
        main: fileURLToPath(new URL('./index.html', import.meta.url)),
        landing: fileURLToPath(new URL('./landing.html', import.meta.url)),
      },
    },
  },
})
