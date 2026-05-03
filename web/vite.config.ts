import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import pkg from './package.json'

// Stamp the build with the package.json version so the UI can show
// a real version string instead of a hardcoded one. Read at build
// time, not runtime, to avoid shipping package.json to the client.
export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  build: {
    outDir: '../internal/webapi/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/oauth': 'http://localhost:8080',
    },
  },
})
