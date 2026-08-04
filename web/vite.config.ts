import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    host: true,
    port: 5173,
    // Fail rather than move. Without this, a second dev server takes 5174 and
    // says so in a line of startup output nobody reads — and when the one on
    // 5173 later stops, the browser tab pointed at it shows a dead page while
    // Vite is demonstrably running. That looked exactly like the app being
    // broken, twice, before anybody thought to check the port.
    strictPort: true,
    // Proxy to the gateway so the browser sees a single origin in development,
    // exactly as it will in the LAN deployment behind Caddy.
    proxy: {
      '/api': { target: 'http://localhost:8180', changeOrigin: true },
      '/media': { target: 'http://localhost:8180', changeOrigin: true },
    },
  },
})
