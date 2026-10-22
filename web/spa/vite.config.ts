import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  // The console lives under /console, so the bundle's own URLs live there
  // too. Without this the built index.html points at /assets/..., which is
  // a path the showcase owns, and the console's JavaScript 404s on a page
  // that otherwise loads.
  //
  // `base` is a top-level option, not a `build` option: under `build` it is
  // silently ignored, which is how the first build of this file shipped
  // /assets/ references into a page served from /console.
  base: '/console/',
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
    // Into the console server's own directory, so the Go build can embed the
    // bundle. The output is gitignored; the placeholder beside it is not.
    outDir: '../consoleui/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      // The harness is not product code. `src/test/**` is the scaffolding that
      // proves things about the product, and measuring it would let the
      // harness inflate the number it is supposed to be checking.
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/test/**',
        'src/main.tsx',
        'src/**/*.d.ts',
        'src/**/*.test.{ts,tsx}',
        'src/**/*.spec.{ts,tsx}',
        // `src/api.ts` declares TYPES only. It has no runtime code, and the
        // v8 provider reports a file with no statements as 0%, which is a
        // number that means nothing and a row that makes the report look
        // worse than it is.
        'src/api.ts',
      ],
      reporter: ['text', 'lcov', 'json-summary'],
      // The numbers are FLOORS.
      //
      // A floor that has been passed is a floor that must be raised, and the
      // Go side's `internal/covercheck` says so out loud: it fails a package
      // that is MEASURED ABOVE its floor and asks for the floor to be raised.
      // Vitest's thresholds only fail BELOW, so the ratchet runs one way
      // here. That asymmetry is stated rather than hidden, and the person
      // who raises the floor is the one who reads the number.
      //
      // The group thresholds do NOT inherit the top-level ones in Vitest 2,
      // so each states all four metrics explicitly, and Vitest applies a
      // glob as a GROUP rather than per file -- which is why the numbers
      // below are the groups' measured values with a margin rather than the
      // lowest file's.
      thresholds: {
        statements: 85,
        lines: 85,
        functions: 80,
        branches: 75,
        'src/lib/**': {
          statements: 95,
          lines: 95,
          functions: 90,
          branches: 90,
        },
        'src/hooks/**': {
          statements: 95,
          lines: 95,
          functions: 95,
          branches: 80,
        },
        'src/components/ui/**': {
          statements: 95,
          lines: 95,
          functions: 88,
          branches: 85,
        },
      },
    },
  },
})
