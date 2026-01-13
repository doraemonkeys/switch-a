import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

// Plugin to redirect /admin to /admin/ for better UX
// Without this, users visiting /admin get an error instead of the app
function trailingSlashRedirect(): Plugin {
  return {
    name: 'trailing-slash-redirect',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (req.url === '/admin') {
          res.writeHead(302, { Location: '/admin/' })
          res.end()
          return
        }
        next()
      })
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), trailingSlashRedirect()],
  // Base path for production build - all assets will be prefixed with /admin/
  // This matches the server mount point: mux.Handle("/admin/", ...)
  base: '/admin/',
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
        lines: 60,
        functions: 60,
        statements: 60,
        branches: 50,
      },
    },
  },
})
