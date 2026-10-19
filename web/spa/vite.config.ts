import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    // The dev server proxies to the CONSOLE (:8080), not to the product and
    // not to the VMS.
    //
    // The console is the thing that holds the product's and the VMS's
    // credentials, and the browser must never hold either of them. One hop,
    // through the process that is allowed to know them, is the whole reason the
    // console is a server and not a static bundle.
    proxy: {
      '^/v1': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '^/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '^/vms': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../static/spa',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
  },
})
