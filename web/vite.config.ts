/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { createHash } from 'node:crypto'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, type Plugin } from 'vite'

// The Go server listens on :8080 in development (`make dev`).
const apiTarget = process.env.SYNCPHONY_API_URL ?? 'http://localhost:8080'

/**
 * Writes dist/sw.js from the sw.js template: the list of files to precache
 * (every fingerprinted asset, plus the icons and manifest) and a version
 * that changes whenever any of them does, so browsers pick up new builds.
 */
function serviceWorker(): Plugin {
  return {
    name: 'syncphony-service-worker',
    apply: 'build',
    generateBundle(_, bundle) {
      const assets = Object.keys(bundle)
        .filter((f) => f.startsWith('assets/') && !f.endsWith('.map'))
        .sort()
      const publicFiles = readdirSync('public')
        .filter((f) => /\.(png|svg|webmanifest)$/.test(f))
        .sort()
      const hash = createHash('sha256')
      for (const f of assets) hash.update(f)
      for (const f of publicFiles) hash.update(f).update(readFileSync(`public/${f}`))
      hash.update(readFileSync('index.html')).update(readFileSync('sw.js'))
      const precache = [...publicFiles, ...assets].map((f) => `/${f}`)
      const source = readFileSync('sw.js', 'utf8')
        .replace("'__VERSION__'", JSON.stringify(hash.digest('hex').slice(0, 12)))
        .replace('__PRECACHE__', JSON.stringify(precache))
      this.emitFile({ type: 'asset', fileName: 'sw.js', source })
    },
  }
}

export default defineConfig({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true }),
    react(),
    tailwindcss(),
    serviceWorker(),
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
