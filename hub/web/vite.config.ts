import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'

// Production builds emit content-hashed asset filenames under assets/ by
// default (e.g. assets/index-DPlHunjN.js); the hub serves /assets/* with
// `Cache-Control: immutable` (server.go). outDir stays `dist` so the hub's
// `//go:embed all:web/dist` picks up the built bundle. emptyOutDir clears
// any stale files from a previous build.
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
