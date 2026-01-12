import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/admin/api': {
        target: 'http://localhost:28081',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      // Logic layers: api, lib, hooks, config - require high coverage
      // View layers: components - medium coverage
      // Assembly layers: pages - excluded from thresholds
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.{test,spec}.{ts,tsx}',
        'src/main.tsx',
        'src/test-setup.ts',
        'src/pages/**',
      ],
      thresholds: {
        // Phase 1: Establishing quality baseline
        // branches is harder to achieve but more meaningful for logic coverage
        lines: 70,
        functions: 70,
        statements: 70,
        branches: 60,
      },
    },
  },
})
