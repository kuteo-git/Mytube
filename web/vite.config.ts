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
    // Proxy to the gateway so the browser sees a single origin in development,
    // exactly as it will in the LAN deployment behind Caddy.
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/media': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
})
