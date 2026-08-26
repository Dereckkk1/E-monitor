import { defineConfig } from '@playwright/test';

/**
 * Playwright entrou aqui por um motivo estreito e concreto: a `/sso` foi
 * entregue funcionando “em teste” e não funcionava em navegador nenhum
 * (ver `docs/features/hub-sso.md`). O `frontend/` não tinha infraestrutura de
 * teste nenhuma, e a tela que precisava de rede era justamente a que estava
 * quebrada.
 *
 * NÃO é uma suíte de e2e do produto. Os testes daqui falseiam a API no browser
 * com `page.route` e exercitam só a lógica da página — por isso precisam do
 * Vite no ar e de mais nada: sem Postgres, sem NATS, sem MinIO, sem Go.
 *
 *   npm run dev            # noutro terminal, ou deixe o webServer subir
 *   npm run test:e2e
 */
export default defineConfig({
    testDir: './e2e',
    timeout: 60_000,
    reporter: 'list',
    use: {
        baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5174',
    },
    webServer: {
        command: 'npm run dev -- --port 5174 --strictPort',
        url: 'http://localhost:5174/sso',
        reuseExistingServer: true,
        timeout: 120_000,
    },
});
