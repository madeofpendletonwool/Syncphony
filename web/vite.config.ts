/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'

// The Go server listens on :8080 in development (`make dev`).
const apiTarget = process.env.SYNCPHONY_API_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    proxy: {
      '/api': apiTarget,
      '/ws': { target: apiTarget, ws: true },
    },
  },
  test: {
    environment: 'jsdom',
  },
})
