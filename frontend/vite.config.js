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
  // Vitest (testes unitários + cobertura). A cobertura conta TODO o src/
  // (componentes inclusive) — só fica fora o próprio código de teste.
  test: {
    environment: 'jsdom',
    // globals: a Testing Library desmonta o que renderizou entre testes sozinha
    // (usa o afterEach global). Hook em setupFiles quebra no Vitest 5.
    globals: true,
    include: ['src/**/*.test.{js,jsx,mjs}'],
    setupFiles: ['./src/test-utils/polyfills.js'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{js,jsx}'],
      // fora: o próprio código de teste e a infra compartilhada de teste
      exclude: ['src/**/*.test.{js,jsx,mjs}', 'src/test-utils/**'],
      reporter: ['text-summary', 'lcov', 'json-summary'],
      reportsDirectory: 'coverage',
    },
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
