import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // Recharts depende de victory-vendor/lodash com CJS circular requires.
  // Sem optimizeDeps.include, Vite 8 (Rolldown) faz pre-bundling parcial
  // que gera "require_isUnsafeProperty is not a function" em runtime.
  // Listar explicitamente força um bundle único e consistente.
  optimizeDeps: {
    include: ['recharts'],
  },
  server: {
    port: 3000,
    proxy: {
      '/v1': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
