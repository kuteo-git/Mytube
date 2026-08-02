import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// Kept separate from vite.config.ts so the dev server config (proxy, tailwind)
// has no bearing on the test run. The alias has to be repeated because it is the
// one thing tests genuinely share with the app.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  test: {
    environment: 'jsdom',
    // Without a real origin jsdom runs opaque, and an opaque origin has no
    // localStorage at all — which the app reads while merely rendering.
    environmentOptions: { jsdom: { url: 'http://localhost/' } },
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
  },
})
